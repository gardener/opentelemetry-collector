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
	if testing.Short() {
		t.Skip("skipping systemd integration test in -short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	t.Cleanup(cancel)

	req := testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:       "../..",
			Dockerfile:    "extension/sdnotify/testdata/Dockerfile.systemd",
			KeepImage:     false,
			PrintBuildLog: true,
		},
		// Systemd as PID 1 needs either Privileged or a specific cap/cgroup
		// recipe. Privileged + cgroupns=host + tmpfs for /run, /run/lock,
		// /tmp is the most portable combination across Linux, Docker Desktop
		// on macOS, and common CI runners.
		HostConfigModifier: func(hc *container.HostConfig) {
			hc.Privileged = true
			hc.CgroupnsMode = container.CgroupnsModeHost
			hc.Tmpfs = map[string]string{
				"/run":      "rw",
				"/run/lock": "rw",
				"/tmp":      "rw",
			}
		},
		// Poll `systemctl is-active otelcol.service` until it exits 0.
		// This is the load-bearing assertion: a Type=notify unit only
		// transitions to active after systemd receives READY=1 from the
		// process, which only happens via daemon.SdNotify in extension.Ready().
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
// Errors fail the test rather than being returned -- every caller treats them
// the same way.
func execAndCollect(t *testing.T, ctx context.Context, ctr testcontainers.Container, argv ...string) string {
	t.Helper()
	_, r, err := ctr.Exec(ctx, argv)
	require.NoError(t, err, "exec failed: %v", argv)
	out, err := io.ReadAll(r)
	require.NoError(t, err, "reading exec output failed: %v", argv)
	t.Logf("$ %s\n%s", strings.Join(argv, " "), out)
	return string(out)
}
