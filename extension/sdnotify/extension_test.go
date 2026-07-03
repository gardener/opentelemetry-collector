// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package sdnotify

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.opentelemetry.io/collector/component"
	"go.uber.org/zap/zaptest"
)

// nopHost is a minimal component.Host for unit tests.
type nopHost struct{}

func (nopHost) GetExtensions() map[component.ID]component.Component { return nil }

// startFakeNotifySocket opens a unix socket , points $NOTIFY_SOCKET at it, and
// returns a channel that receives every payload systemd would have seen.
func startFakeNotifySocket(t *testing.T) <-chan string {
	t.Helper()

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "notify.sock")

	conn, err := net.ListenPacket("unixgram", sockPath)
	require.NoError(t, err, "listen on fake NOTIFY_SOCKET")
	t.Cleanup(func() { _ = conn.Close() })

	t.Setenv("NOTIFY_SOCKET", sockPath)

	msgs := make(chan string, 8)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, _, err := conn.ReadFrom(buf)
			if err != nil {
				return // socket closed on cleanup
			}
			msgs <- string(buf[:n])
		}
	}()
	return msgs
}

func TestStart_NoNotifySocket_IsNoop(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")

	s := newSDNotify(&Config{}, zaptest.NewLogger(t))
	require.NoError(t, s.Start(context.Background(), nopHost{}))
	require.True(t, s.isNoop, "expected extension to run as no-op without NOTIFY_SOCKET")

	// Shutdown must be safe even though we never registered a signal handler.
	require.NoError(t, s.Shutdown(context.Background()))
}

func TestReady_SendsREADY(t *testing.T) {
	msgs := startFakeNotifySocket(t)

	s := newSDNotify(&Config{}, zaptest.NewLogger(t))
	require.NoError(t, s.Start(context.Background(), nopHost{}))
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })

	require.NoError(t, s.Ready())
	select {
	case got := <-msgs:
		require.Equal(t, "READY=1", got)
	case <-time.After(2 * time.Second):
		t.Fatal("no datagram received on fake NOTIFY_SOCKET")
	}
}

func TestNotReady_SendsSTOPPING(t *testing.T) {
	msgs := startFakeNotifySocket(t)

	s := newSDNotify(&Config{}, zaptest.NewLogger(t))
	require.NoError(t, s.Start(context.Background(), nopHost{}))
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })

	require.NoError(t, s.NotReady())
	select {
	case got := <-msgs:
		require.Equal(t, "STOPPING=1", got)
	case <-time.After(2 * time.Second):
		t.Fatal("no datagram received on fake NOTIFY_SOCKET")
	}
}

func TestSIGHUP_SendsRELOADING(t *testing.T) {
	msgs := startFakeNotifySocket(t) // NOTIFY_SOCKET must be set for the signal handler to run

	s := newSDNotify(&Config{}, zaptest.NewLogger(t))
	require.NoError(t, s.Start(context.Background(), nopHost{}))
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })

	// Deliver SIGHUP to ourselves; signal.Notify in Start() intercepts it.
	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGHUP))

	select {
	case got := <-msgs:
		// Per sd_notify(3): RELOADING=1 must be sent together with
		// MONOTONIC_USEC in a single datagram.
		require.Contains(t, got, "RELOADING=1")
		require.Contains(t, got, "MONOTONIC_USEC=")
	case <-time.After(2 * time.Second):
		t.Fatal("no datagram received on fake NOTIFY_SOCKET after SIGHUP")
	}
}

func TestShutdown_IsIdempotent(t *testing.T) {
	_ = startFakeNotifySocket(t)

	s := newSDNotify(&Config{}, zaptest.NewLogger(t))
	require.NoError(t, s.Start(context.Background(), nopHost{}))

	require.NoError(t, s.Shutdown(context.Background()))
	// Second call must not panic on close(closed channel).
	require.NoError(t, s.Shutdown(context.Background()))
}

// execAndCollect runs argv in the container and returns combined output.
// Errors fail the test rather than being returned.
func execAndCollect(ctx context.Context, t *testing.T, ctr testcontainers.Container, argv ...string) string {
	t.Helper()

	_, r, err := ctr.Exec(ctx, argv, tcexec.Multiplexed())
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
			hc.Privileged = true
			// Both kernel capabilities are required to run systemd in a container.
			// https://github.com/systemd/systemd/blob/main/docs/CONTAINER_INTERFACE.md#what-you-shouldnt-do
			hc.CapAdd = []string{"SYS_ADMIN", "MKNOD"}
			// Run without the default seccomp profile, because it denies mounting.
			// https://docs.docker.com/engine/security/seccomp
			hc.SecurityOpt = []string{"seccomp=unconfined"}
			// Either pre-mount all cgroup hierarchies into the container, or leave
			// that to systemd which will do so if they are missing.
			// https://github.com/systemd/systemd/blob/main/docs/CONTAINER_INTERFACE.md#execution-environment
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

// TestSDNotify_LifecycleIntegration exercises the full sd_notify lifecycle
// against a real systemd inside a container:
//   - boot -> unit reaches active/running (only possible if READY=1 was sent)
//   - SIGHUP -> the extension emits RELOADING=1 and otelcol's own reload
//     drives NotReady/Ready, producing a STOPPING=1 -> READY=1 pair in the
//     same PID (Type=notify-reload contract: no process cycling, NRestarts=0)
//   - systemctl stop -> unit exits cleanly (Result=success), confirming
//     STOPPING=1 was emitted before shutdown
func TestSDNotify_LifecycleIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)

	ctr := startSystemdContainer(
		ctx,
		t,
		[]string{"systemctl", "is-active", "otelcol.service"},
		0,
	)
	beforePID := strings.TrimSpace(
		execAndCollect(ctx, t, ctr, "systemctl", "show", "otelcol.service", "-p", "MainPID"),
	)

	// Reload flow
	_ = execAndCollect(ctx, t, ctr, "systemctl", "kill", "-s", "SIGHUP", "otelcol.service")

	// Wait for otelcol's reload to complete: RELOADING=1 emitted by the
	// extension, plus a second READY=1 from the rebuilt pipelines.
	require.Eventually(t, func() bool {
		journal := execAndCollect(ctx, t, ctr, "journalctl", "-u", "otelcol.service", "--no-pager")
		return strings.Contains(journal, "sent RELOADING=1 to systemd") &&
			strings.Count(journal, "sent READY=1 to systemd") >= 2
	}, 15*time.Second, 200*time.Millisecond,
		"RELOADING=1 + second READY=1 in journal after SIGHUP-triggered reload")

	// Unit must still be active with the same MainPID: this was an in-process
	// reload, not a supervisor-driven restart.
	show := execAndCollect(ctx, t, ctr,
		"systemctl", "show", "otelcol.service",
		"-p", "ActiveState", "-p", "SubState", "-p", "MainPID", "-p", "NRestarts",
	)
	require.Contains(t, show, "ActiveState=active",
		"unit should stay active after SIGHUP-triggered in-process reload; show:\n%s", show)
	require.Contains(t, show, "NRestarts=0",
		"SIGHUP must not trigger a supervisor restart; show:\n%s", show)
	afterPID := strings.TrimSpace(
		execAndCollect(ctx, t, ctr, "systemctl", "show", "otelcol.service", "-p", "MainPID"),
	)
	require.Equal(t, beforePID, afterPID,
		"MainPID must not change on in-process reload; before=%s after=%s", beforePID, afterPID)

	// The journal should contain the full reload sequence:
	// RELOADING=1 -> STOPPING=1 (from PipelineWatcher.NotReady) -> READY=1.
	journal := execAndCollect(ctx, t, ctr, "journalctl", "-u", "otelcol.service", "--no-pager")
	require.Contains(t, journal, "sent RELOADING=1 to systemd",
		"expected RELOADING=1 log line after SIGHUP; journal:\n%s", journal)
	require.Contains(t, journal, "sent STOPPING=1 to systemd",
		"expected STOPPING=1 log line from PipelineWatcher.NotReady during reload; journal:\n%s", journal)
	require.GreaterOrEqual(t, strings.Count(journal, "sent READY=1 to systemd"), 2,
		"expected >=2 READY=1 log lines (boot + reload); journal:\n%s", journal)

	// Shutdown flow
	_ = execAndCollect(ctx, t, ctr, "systemctl", "stop", "otelcol.service")
	stopped := execAndCollect(ctx, t, ctr,
		"systemctl", "show", "otelcol.service",
		"-p", "ActiveState", "-p", "Result",
	)
	require.Contains(t, stopped, "ActiveState=inactive")
	require.Contains(t, stopped, "Result=success",
		"unit should exit cleanly after systemctl stop; show:\n%s", stopped)
}
