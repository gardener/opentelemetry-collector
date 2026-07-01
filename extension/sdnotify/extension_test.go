// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package sdnotify

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestSDNotify_SystemdIntegration spins up a real systemd-PID-1 container,
// runs otelcol (built fresh via ocb with this extension wired in) as a
// Type=notify unit, and asserts via systemctl that the unit reached
// active/running -- which is only possible if extension.Ready() successfully
// sent READY=1 over $NOTIFY_SOCKET.
//
// All files needed to build the image live under testdata/. The Docker build
// context is the repo root (two levels up from this file) so the Dockerfile
// can COPY the in-tree extension source for ocb to consume.
func TestSDNotify_SystemdIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)

	req := testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:       "../..",
			Dockerfile:    "extension/sdnotify/testdata/Dockerfile",
			KeepImage:     false,
			PrintBuildLog: true,
		},
		HostConfigModifier: func(hc *container.HostConfig) {
			// Both kernel capabilities are required to run systemd in a container.
			// https://github.com/systemd/systemd/blob/main/docs/CONTAINER_INTERFACE.md#what-you-shouldnt-do
			hc.CapAdd = []string{"SYS_ADMIN", "MKNOD"}
			// Should run without the default seccomp profile, because it denies mounting.
			// https://docs.docker.com/engine/security/seccomp
			hc.SecurityOpt = []string{"seccomp=unconfined"}
			// Either pre-mount all cgroup hierarchies in full into the container, or leave that to systemd which will do so if they are missing.
			// https://github.com/systemd/systemd/blob/main/docs/CONTAINER_INTERFACE.md#execution-environment
			hc.CgroupnsMode = container.CgroupnsModeHost
			hc.Mounts = []mount.Mount{
				{
					Type:   mount.TypeBind,
					Source: "/sys/fs/cgroup",
					Target: "/sys/fs/cgroup",
				},
			}
		},
		// Wait for systemd to finish booting AND for the otelcol.service to
		// reach `active`. The latter only happens after sdnotify's Ready()
		// has sent READY=1 -- which is the property we are testing.
		WaitingFor: wait.ForExec([]string{
			"systemctl", "is-active", "otelcol.service",
		}).WithStartupTimeout(3 * time.Minute).
			WithPollInterval(2 * time.Second).
			WithExitCode(0),
	}

	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err, "container failed to start; sdnotify integration prerequisites not met")
	t.Cleanup(func() {
		_ = ctr.Terminate(context.Background())
	})

	// Belt-and-suspenders: explicit state assertions for nicer failure output.
	show := execAndCollect(t, ctx, ctr,
		"systemctl", "show", "otelcol.service",
		"-p", "ActiveState", "-p", "SubState", "-p", "Result",
	)
	require.Contains(t, show, "ActiveState=active")
	require.Contains(t, show, "SubState=running")
	require.Contains(t, show, "Result=success")

	// Confirm extension.Ready()'s log line landed in the journal. If the base
	// image ever strips journalctl, drop this; the systemctl checks above are
	// sufficient on their own.
	journal := execAndCollect(t, ctx, ctr,
		"journalctl", "-u", "otelcol.service", "--no-pager",
	)
	require.True(t,
		strings.Contains(journal, "sent READY=1 to systemd"),
		"expected sdnotify READY=1 log line in journal, got:\n%s", journal,
	)

	// Exercise the NotReady() path: stop the unit and verify a clean exit.
	// STOPPING=1 is best-effort in the extension (it only logs on failure),
	// so we assert on systemd's view of the shutdown, not on a log line.
	_ = execAndCollect(t, ctx, ctr, "systemctl", "stop", "otelcol.service")

	stopped := execAndCollect(t, ctx, ctr,
		"systemctl", "show", "otelcol.service",
		"-p", "ActiveState", "-p", "Result",
	)
	require.Contains(t, stopped, "ActiveState=inactive")
	require.Contains(t, stopped, "Result=success")
}

// execAndCollect runs argv in the container and returns combined output.
// Errors fail the test rather than being returned.
func execAndCollect(t *testing.T, ctx context.Context, ctr testcontainers.Container, argv ...string) string {
	t.Helper()

	_, r, err := ctr.Exec(ctx, argv)
	require.NoError(t, err, "exec failed: %v", argv)

	out, err := io.ReadAll(r)
	require.NoError(t, err, "reading exec output failed: %v", argv)
	t.Logf("$ %s\n%s", strings.Join(argv, " "), out)

	return string(out)
}
