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

// execAndCollect runs argv in the container and returns combined output.
// Errors fail the test rather than being returned.
func execAndCollect(ctx context.Context, t *testing.T, ctr testcontainers.Container, argv ...string) string {
	t.Helper()

	_, r, err := ctr.Exec(ctx, argv)
	require.NoError(t, err, "exec failed: %v", argv)

	out, err := io.ReadAll(r)
	require.NoError(t, err, "reading exec output failed: %v", argv)
	t.Logf("$ %s\n%s", strings.Join(argv, " "), out)

	return string(out)
}

// startSystemdContainer builds (or reuses) the sdnotify test image and starts
// a fresh container running systemd as PID 1. The image is shared across test
// cases via the Repo/Tag stable name and KeepImage=true, so only the first
// test in a `go test` invocation pays the ocb build cost; subsequent cases
// hit Docker's image cache and add only container-start latency.
//
// The wait strategy defers to the caller: waitCmd is passed straight to
// wait.ForExec, so each case decides what "ready" means (unit active vs
// unit failed vs systemd booted).
func startSystemdContainer(
	ctx context.Context,
	t *testing.T,
	waitCmd []string,
	waitExitCode int,
) testcontainers.Container {
	t.Helper()

	req := testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:       "../..",
			Dockerfile:    "extension/sdnotify/testdata/Dockerfile",
			Repo:          "otelcol-sdnotify-test",
			Tag:           "latest",
			KeepImage:     true,
			PrintBuildLog: true,
		},
		HostConfigModifier: func(hc *container.HostConfig) {
			hc.CapAdd = []string{"SYS_ADMIN", "MKNOD"}
			hc.SecurityOpt = []string{"seccomp=unconfined"}
			hc.CgroupnsMode = container.CgroupnsModeHost
			hc.Mounts = []mount.Mount{
				{Type: mount.TypeBind, Source: "/sys/fs/cgroup", Target: "/sys/fs/cgroup"},
			}
		},
		WaitingFor: wait.ForExec(waitCmd).
			WithStartupTimeout(3 * time.Minute).
			WithPollInterval(2 * time.Second).
			WithExitCode(waitExitCode),
	}

	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err, "container failed to start")
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })

	return ctr
}

// installUnitFile copies a systemd unit file into the running container,
// reloads systemd, and returns. Used by tests that need a different unit
// configuration than the default one baked into the image.
func installUnitFile(ctx context.Context, t *testing.T, ctr testcontainers.Container, srcHostPath string) {
	t.Helper()

	err := ctr.CopyFileToContainer(ctx, srcHostPath, "/etc/systemd/system/otelcol.service", 0o644)
	require.NoError(t, err, "copying unit file")

	_ = execAndCollect(ctx, t, ctr, "systemctl", "daemon-reload")
}

// installConfigFile copies a collector config into the running container.
func installConfigFile(ctx context.Context, t *testing.T, ctr testcontainers.Container, srcHostPath string) {
	t.Helper()

	err := ctr.CopyFileToContainer(ctx, srcHostPath, "/etc/otelcol/config.yaml", 0o644)
	require.NoError(t, err, "copying collector config")
}

// waitForUnitState polls `systemctl show` until ActiveState matches `want`
// or the timeout expires. Returns the last observed `systemctl show` output
// so callers can log or assert on it.
func waitForUnitState(ctx context.Context, t *testing.T, ctr testcontainers.Container, want string, timeout time.Duration) string {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		last = execAndCollect(ctx, t, ctr,
			"systemctl", "show", "otelcol.service",
			"-p", "ActiveState", "-p", "SubState", "-p", "Result", "-p", "MainPID", "-p", "NRestarts",
		)
		if strings.Contains(last, "ActiveState="+want) {
			return last
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for ActiveState=%s; last show output:\n%s", want, last)
	return last
}
