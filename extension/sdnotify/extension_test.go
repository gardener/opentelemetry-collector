// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package sdnotify

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componentstatus"
	"go.uber.org/zap/zaptest"
)

// fakeHost is a component.Host that records fatal-error events reported via
// componentstatus.ReportStatus, so tests can assert the SIGHUP -> fatal path
// without wiring the full collector service.
type fakeHost struct {
	mu     sync.Mutex
	fatals []error
}

func (h *fakeHost) GetExtensions() map[component.ID]component.Component { return nil }

func (h *fakeHost) Report(e *componentstatus.Event) {
	if e.Status() == componentstatus.StatusFatalError {
		h.mu.Lock()
		h.fatals = append(h.fatals, e.Err())
		h.mu.Unlock()
	}
}

func (h *fakeHost) fatalCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.fatals)
}

// startFakeNotifySocket opens a datagram socket in a temp dir, points
// $NOTIFY_SOCKET at it, and returns a channel that receives every payload
// systemd would have seen. Cleanup is registered via t.Cleanup.
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

// waitFor waits up to d for cond to return true. Fails the test otherwise.
func waitFor(t *testing.T, d time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", msg)
}

func TestStart_NoNotifySocket_IsNoop(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")

	s := newSDNotify(&Config{}, zaptest.NewLogger(t))
	require.NoError(t, s.Start(context.Background(), &fakeHost{}))
	require.True(t, s.isNoop, "expected extension to run as no-op without NOTIFY_SOCKET")

	// Shutdown must be safe even though we never registered a signal handler.
	require.NoError(t, s.Shutdown(context.Background()))
}

func TestReady_SendsREADY(t *testing.T) {
	msgs := startFakeNotifySocket(t)

	s := newSDNotify(&Config{}, zaptest.NewLogger(t))
	require.NoError(t, s.Start(context.Background(), &fakeHost{}))
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
	require.NoError(t, s.Start(context.Background(), &fakeHost{}))
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })

	require.NoError(t, s.NotReady())
	select {
	case got := <-msgs:
		require.Equal(t, "STOPPING=1", got)
	case <-time.After(2 * time.Second):
		t.Fatal("no datagram received on fake NOTIFY_SOCKET")
	}
}

func TestSIGHUP_ReportsFatalError(t *testing.T) {
	_ = startFakeNotifySocket(t) // NOTIFY_SOCKET must be set for the signal handler to run

	host := &fakeHost{}
	s := newSDNotify(&Config{}, zaptest.NewLogger(t))
	require.NoError(t, s.Start(context.Background(), host))
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })

	// Deliver SIGHUP to ourselves; signal.Notify in Start() intercepts it.
	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGHUP))

	waitFor(t, 2*time.Second, func() bool { return host.fatalCount() == 1 },
		"fatal-error event from SIGHUP handler")

	host.mu.Lock()
	require.ErrorIs(t, host.fatals[0], errSIGHUP)
	host.mu.Unlock()
}

func TestShutdown_IsIdempotent(t *testing.T) {
	_ = startFakeNotifySocket(t)

	s := newSDNotify(&Config{}, zaptest.NewLogger(t))
	require.NoError(t, s.Start(context.Background(), &fakeHost{}))

	require.NoError(t, s.Shutdown(context.Background()))
	// Second call must not panic on close(closed channel).
	require.NoError(t, s.Shutdown(context.Background()))
}
