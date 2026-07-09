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
	"strconv"
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

// noopHost is a minimal component.Host for unit tests.
type noopHost struct{}

func (noopHost) GetExtensions() map[component.ID]component.Component { return nil }

// startFakeNotifySocket opens a unix socket, points $NOTIFY_SOCKET at it, and
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
				return
			}
			msgs <- string(buf[:n])
		}
	}()
	return msgs
}

func TestStart_NoNotifySocket_IsNoop(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")
	s := newSDNotify(&Config{}, zaptest.NewLogger(t))
	require.NoError(t, s.Start(context.Background(), noopHost{}))
	require.NoError(t, s.Shutdown(context.Background()))
}

func TestShutdown_BeforeStart_IsNoop(t *testing.T) {
	s := newSDNotify(&Config{}, zaptest.NewLogger(t))
	require.NoError(t, s.Shutdown(context.Background()))
}

func TestShutdown_IsIdempotent(t *testing.T) {
	_ = startFakeNotifySocket(t)

	s := newSDNotify(&Config{}, zaptest.NewLogger(t))
	require.NoError(t, s.Start(context.Background(), noopHost{}))

	require.NoError(t, s.Shutdown(context.Background()))
	require.NoError(t, s.Shutdown(context.Background()))
}

func TestReady_SendsREADY(t *testing.T) {
	msgs := startFakeNotifySocket(t)

	s := newSDNotify(&Config{}, zaptest.NewLogger(t))
	require.NoError(t, s.Start(context.Background(), noopHost{}))
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })

	require.NoError(t, s.Ready())
	select {
	case got := <-msgs:
		require.Equal(t, "READY=1", got)
	case <-time.After(2 * time.Second):
		t.Fatal("no datagram received on fake NOTIFY_SOCKET")
	}
}

func TestSIGTERM_SendsSTOPPING(t *testing.T) {
	msgs := startFakeNotifySocket(t)

	s := newSDNotify(&Config{}, zaptest.NewLogger(t))
	require.NoError(t, s.Start(context.Background(), noopHost{}))
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })

	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGTERM))
	select {
	case got := <-msgs:
		require.Equal(t, "STOPPING=1", got)
	case <-time.After(2 * time.Second):
		t.Fatal("no STOPPING=1 datagram received after SIGTERM")
	}
}

func TestSIGHUP_SendsRELOADING(t *testing.T) {
	msgs := startFakeNotifySocket(t)

	s := newSDNotify(&Config{}, zaptest.NewLogger(t))
	require.NoError(t, s.Start(context.Background(), noopHost{}))
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })

	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGHUP))
	select {
	case got := <-msgs:
		require.Contains(t, got, "RELOADING=1")
		require.Contains(t, got, "MONOTONIC_USEC=")
	case <-time.After(2 * time.Second):
		t.Fatal("no event received at NOTIFY_SOCKET after SIGHUP")
	}
}

func TestWatchdog_SendsWATCHDOG(t *testing.T) {
	msgs := startFakeNotifySocket(t)

	t.Setenv("WATCHDOG_USEC", "100000") // 100ms
	t.Setenv("WATCHDOG_PID", strconv.Itoa(os.Getpid()))

	s := newSDNotify(&Config{}, zaptest.NewLogger(t))
	require.NoError(t, s.Start(context.Background(), noopHost{}))
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })

	ctx, cancel := context.WithTimeout(context.Background(), 325*time.Millisecond)
	defer cancel()

	// We expect to receive 6 notifications because:
	//   - WATCHDOG_USEC is set to 100ms, so a notification is sent every 50ms.
	//   - Over a 300ms interval, this results in 300 / 50 = 6 notifications.
	//
	// We wait 325ms instead of exactly 300ms to avoid timing-related flakiness.
	// Waiting exactly 300ms could cause the test to occasionally miss the last
	// notification due to scheduling or timer jitter.
	//
	// Note: We cannot use testing/synctest here because it does not support
	// signals.Notify. See: https://github.com/golang/go/issues/78494
	count := 0
	for {
		select {
		case got := <-msgs:
			require.Equal(t, "WATCHDOG=1", got)
			count++
		case <-ctx.Done():
			require.Equal(t, 6, count,
				"expected 6 WATCHDOG=1 notifications within 300ms, got %d", count)
			return
		}
	}
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

// startSystemdContainer builds/reuses the test image described by the given
// Dockerfile path and starts a fresh container running systemd as PID 1. The
// wait strategy runs waitCmd until it exits 0 (or the startup timeout expires).
func startSystemdContainer(
	ctx context.Context,
	t *testing.T,
	dockerfile string,
	waitCmd []string,
) testcontainers.Container {
	t.Helper()

	repo := "otelcol-sdnotify-test-" + strings.TrimPrefix(filepath.Ext(dockerfile), ".")
	req := testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:       "../..",
			Dockerfile:    dockerfile,
			Repo:          repo,
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
			WithStartupTimeout(60 * time.Second).
			WithPollInterval(1 * time.Second).
			WithExitCode(0),
	}

	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err, "container failed to start")
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })

	return ctr
}

// TestSDNotify_HappyPath_LifecycleIntegration exercises the full sd_notify
// lifecycle against a real systemd inside a container, using the "happy"
// scenario image (valid config, reachable exporter):
//   - boot -> unit reaches active/running (only possible if READY=1 was sent)
//   - SIGHUP -> the extension emits RELOADING=1 and otelcol's own reload
//     rebuilds the pipelines, producing a second READY=1 in the same PID.
//   - systemctl stop -> the extension's SIGTERM handler emits STOPPING=1
//     and the unit exits cleanly (Result=success)
func TestSDNotify_HappyPath_LifecycleIntegration(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	ctr := startSystemdContainer(
		ctx,
		t,
		"extension/sdnotify/testdata/Dockerfile.happy",
		[]string{"systemctl", "is-active", "otelcol.service"},
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

	// Unit must be back to active with no supervisor restart.
	var show string
	require.Eventually(t, func() bool {
		show = execAndCollect(ctx, t, ctr,
			"systemctl", "show", "otelcol.service",
			"-p", "ActiveState", "-p", "SubState", "-p", "NRestarts",
		)
		return strings.Contains(show, "ActiveState=active")
	}, 15*time.Second, 200*time.Millisecond,
		"unit should return to active after SIGHUP-triggered in-process reload; last show:\n%s", show)
	require.Contains(t, show, "NRestarts=0",
		"SIGHUP must not trigger a supervisor restart; show:\n%s", show)
	afterPID := strings.TrimSpace(
		execAndCollect(ctx, t, ctr, "systemctl", "show", "otelcol.service", "-p", "MainPID"),
	)
	require.Equal(t, beforePID, afterPID,
		"MainPID must not change on in-process reload; before=%s after=%s", beforePID, afterPID)

	// Reload journal: RELOADING=1 + >=2 READY=1 - the second is the post-reload
	// ready. STOPPING=1 must NOT appear yet - the extension only emits it on
	// genuine termination (SIGINT / SIGTERM), never mid-reload.
	journal := execAndCollect(ctx, t, ctr, "journalctl", "-u", "otelcol.service", "--no-pager")
	require.Contains(t, journal, "sent RELOADING=1 to systemd",
		"expected RELOADING=1 log line after SIGHUP; journal:\n%s", journal)
	require.NotContains(t, journal, "sent STOPPING=1 to systemd",
		"STOPPING=1 must not appear during an in-process reload; journal:\n%s", journal)
	require.GreaterOrEqual(t, strings.Count(journal, "sent READY=1 to systemd"), 2,
		"expected >=2 READY=1 log lines (boot + reload); journal:\n%s", journal)

	// Shutdown flow: systemctl stop SIGTERMs the process; the extension's
	// termination handler must emit STOPPING=1 before the unit exits.
	_ = execAndCollect(ctx, t, ctr, "systemctl", "stop", "otelcol.service")
	stopped := execAndCollect(ctx, t, ctr,
		"systemctl", "show", "otelcol.service",
		"-p", "ActiveState", "-p", "Result",
	)
	require.Contains(t, stopped, "ActiveState=inactive")
	require.Contains(t, stopped, "Result=success",
		"unit should exit cleanly after systemctl stop; show:\n%s", stopped)

	journal = execAndCollect(ctx, t, ctr, "journalctl", "-u", "otelcol.service", "--no-pager")
	require.Contains(t, journal, "sent STOPPING=1 to systemd",
		"expected STOPPING=1 log line after systemctl stop; journal:\n%s", journal)
}

// TestSDNotify_InvalidConfig_UnitFails verifies that when the collector config
// is invalid (pipeline references an exporter that isn't declared), the
// collector exits without ever sending READY=1 and systemd marks the unit as
// failed. This is the negative branch of the sd_notify handshake: no READY=1
// means systemd never considers the unit started.
func TestSDNotify_InvalidConfig_UnitFails(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	// `systemctl is-failed` exits 0 when the unit is in the failed state.
	ctr := startSystemdContainer(
		ctx,
		t,
		"extension/sdnotify/testdata/Dockerfile.invalidconfig",
		[]string{"systemctl", "is-failed", "otelcol.service"},
	)

	show := execAndCollect(ctx, t, ctr,
		"systemctl", "show", "otelcol.service",
		"-p", "ActiveState", "-p", "SubState", "-p", "Result",
	)
	require.Contains(t, show, "ActiveState=failed",
		"unit should be failed with an invalid config; show:\n%s", show)
	require.NotContains(t, show, "Result=success",
		"failed unit must not report Result=success; show:\n%s", show)

	journal := execAndCollect(ctx, t, ctr, "journalctl", "-u", "otelcol.service", "--no-pager")
	require.NotContains(t, journal, "sent READY=1 to systemd",
		"collector must not have sent READY=1 with an invalid config; journal:\n%s", journal)
}

// TestSDNotify_BadExporter_ReachesReady verifies that an unreachable exporter
// target does NOT prevent the extension from sending READY=1. The OTLP
// exporter starts asynchronously (queue + retry), so the collector reaches
// steady state and sdnotify signals systemd regardless of downstream health.
func TestSDNotify_BadExporter_ReachesReady(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	ctr := startSystemdContainer(
		ctx,
		t,
		"extension/sdnotify/testdata/Dockerfile.badexporter",
		[]string{"systemctl", "is-active", "otelcol.service"},
	)

	show := execAndCollect(ctx, t, ctr,
		"systemctl", "show", "otelcol.service",
		"-p", "ActiveState", "-p", "SubState",
	)
	require.Contains(t, show, "ActiveState=active",
		"unit should be active even when the exporter target is unreachable; show:\n%s", show)

	journal := execAndCollect(ctx, t, ctr, "journalctl", "-u", "otelcol.service", "--no-pager")
	require.Contains(t, journal, "sent READY=1 to systemd",
		"expected READY=1 to be emitted with an unreachable exporter; journal:\n%s", journal)
}

// TestSDNotify_NoNotifySocket_NoopBranch verifies the extension's no-op
// behavior when NOTIFY_SOCKET is unset. The unit uses Type=simple with
// Environment=NOTIFY_SOCKET= so systemd does not provide the socket. The
// collector must still start and stay running; the extension logs the no-op
// warning and treats Ready() as a no-op.
func TestSDNotify_NoNotifySocket_NoopBranch(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	ctr := startSystemdContainer(
		ctx,
		t,
		"extension/sdnotify/testdata/Dockerfile.nonotifysocket",
		[]string{"systemctl", "is-active", "otelcol.service"},
	)

	// Type=simple flips the unit to active as soon as the process is forked,
	// which can race the collector's own startup logs. Poll the journal until
	// both extension log lines have landed.
	require.Eventually(t, func() bool {
		journal := execAndCollect(ctx, t, ctr, "journalctl", "-u", "otelcol.service", "--no-pager")
		return strings.Contains(journal, "NOTIFY_SOCKET is not set; sd_notify support is disabled") &&
			strings.Contains(journal, "NOTIFY_SOCKET not set; READY=1 was a no-op")
	}, 30*time.Second, 500*time.Millisecond,
		"expected sdnotify no-op warning + READY=1 no-op log lines with unset NOTIFY_SOCKET")

	// And the unit must still be healthy - the extension must not have
	// crashed the process just because sd_notify was disabled.
	show := execAndCollect(ctx, t, ctr,
		"systemctl", "show", "otelcol.service",
		"-p", "ActiveState", "-p", "SubState",
	)
	require.Contains(t, show, "ActiveState=active",
		"unit should stay active with NOTIFY_SOCKET unset; show:\n%s", show)
}
