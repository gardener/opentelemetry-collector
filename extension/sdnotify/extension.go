package sdnotify

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/coreos/go-systemd/v22/daemon"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension"
	"go.opentelemetry.io/collector/extension/extensioncapabilities"
	"go.uber.org/zap"
)

// Extension is the union of capability interfaces sdnotify implements.
type Extension interface {
	extension.Extension
	extensioncapabilities.PipelineWatcher
	extensioncapabilities.ConfigSnapshotWatcher
}

type sdnotify struct {
	cfg    *Config
	logger *zap.Logger
	host   component.Host
	isNoop bool

	// configNotified flips to true after the first NotifyConfigSnapshot call,
	// which always fires at startup with the initial config. That first call
	// is handled by Ready() (from PipelineWatcher) instead, so we ignore it.
	configNotified bool

	// reload tracks the SIGHUP -> NotifyConfigSnapshot handshake for
	// Type=notify-reload behaviour (see Config.HandleReloadSignal).
	//   sigCh:    channel wired via signal.Notify for SIGHUP
	//   stopCh:   closed on Shutdown to unwind the goroutine
	//   pending:  true after we have sent RELOADING=1 and are waiting for the
	//             next NotifyConfigSnapshot to send the paired READY=1.
	//   mu:       guards pending across the signal goroutine and the
	//             NotifyConfigSnapshot callback.
	sigCh      chan os.Signal
	shutdownCh chan struct{}
	pending    atomic.Bool
}

var (
	_ Extension                                   = (*sdnotify)(nil)
	_ extensioncapabilities.PipelineWatcher       = (*sdnotify)(nil)
	_ extensioncapabilities.ConfigSnapshotWatcher = (*sdnotify)(nil)
)

func newSDNotify(cfg *Config, logger *zap.Logger) *sdnotify {
	return &sdnotify{
		cfg:    cfg,
		logger: logger,
		// configNotified: false,
		sigCh:      make(chan os.Signal, 1),
		shutdownCh: make(chan struct{}),
	}
}

func (s *sdnotify) Start(_ context.Context, host component.Host) error {
	monotonicEpoch := time.Now()

	s.host = host

	// If NOTIFY_SOCKET environment variable is unset, then the sd_notify protocol is no-op.
	if os.Getenv("NOTIFY_SOCKET") == "" {
		s.isNoop = true
		return fmt.Errorf("sdnotify: NOTIFY_SOCKET not set; not running under systemd")
	}

	// For services configured with Type=notify-reload, systemd signals the main
	// process with SIGHUP when a reload is requested. The process is responsible
	// for reloading its configuration and informing systemd when the reload has
	// completed, allowing systemd to track the reload status correctly.
	signal.Notify(s.sigCh, syscall.SIGHUP)

	go func() {
		for {
			select {
			case <-s.shutdownCh:
				return
			case <-s.sigCh:
				// onReloadSignal sends RELOADING=1 + MONOTONIC_USEC to systemd. Per sd_notify(3),
				// MONOTONIC_USEC must be CLOCK_MONOTONIC in microseconds as a decimal string,
				// and it must be sent in the same datagram as RELOADING=1.
				// If another SIGHUP is already pending, we still re-emit RELOADING=1
				// (it's cheap, and systemd's timeout resets on it). The flag stays true.
				s.pending.Store(true)

				monotonicUS := uint64(time.Since(monotonicEpoch) / time.Microsecond)
				msg := fmt.Sprintf("%s\nMONOTONIC_USEC=%d", daemon.SdNotifyReloading, monotonicUS)

				sent, err := daemon.SdNotify(false, msg)
				if err != nil {
					s.logger.Warn("sdnotify RELOADING=1 failed", zap.Error(err))
					return
				}
				if sent {
					s.logger.Info("sdnotify: SIGHUP received, sent RELOADING=1 to systemd",
						zap.Uint64("monotonic_usec", monotonicUS))
				}
			}
		}
	}()

	return nil
}

func (s *sdnotify) Shutdown(_ context.Context) error {
	if s.shutdownCh != nil {
		close(s.shutdownCh)
		signal.Stop(s.sigCh)
	}
	return nil
}

func (s *sdnotify) Ready() error {
	sent, err := daemon.SdNotify(false, daemon.SdNotifyReady)
	if err != nil {
		return fmt.Errorf("sdnotify READY=1: %w", err)
	}

	if !sent {
		s.logger.Info("sdnotify: NOTIFY_SOCKET not set; READY=1 was a no-op")
	} else {
		s.logger.Info("sdnotify: sent READY=1 to systemd")
	}

	return nil
}

func (s *sdnotify) NotReady() error {
	sent, err := daemon.SdNotify(false, daemon.SdNotifyStopping)
	if err != nil {
		// Don't block shutdown on a notify failure - just log it.
		s.logger.Warn("sdnotify STOPPING=1 failed", zap.Error(err))
		return nil
	}

	if sent {
		s.logger.Info("sdnotify: sent STOPPING=1 to systemd")
	}

	return nil
}

// NotifyConfigSnapshot is called by the collector when the effective
// configuration is set (once at startup) and after each subsequent reload.
//
// For Type=notify-reload units, systemd expects the service to bracket each
// reload with:
//
//	RELOADING=1\nMONOTONIC_USEC=<current CLOCK_MONOTONIC in microseconds>
//	... (reload work happens) ...
//	READY=1
//
// The RELOADING=1 half is emitted in the SIGHUP handler (when systemd itself
// initiates the reload); the READY=1 half is emitted here, after the collector
// has actually applied the new configuration. If NotifyConfigSnapshot fires
// without a preceding SIGHUP (e.g. the confmap file watcher spontaneously
// picked up a change), we do not emit anything -- systemd was not asked to
// track this reload, and sending READY=1 unpaired would be a protocol error.
//
// The initial NotifyConfigSnapshot at startup is likewise skipped: Ready()
// (from PipelineWatcher) is the proper place to send READY=1 the first time.
func (s *sdnotify) NotifyConfigSnapshot(_ context.Context, _ extensioncapabilities.ConfigSnapshot) error {
	if !s.configNotified {
		s.configNotified = true
		return nil
	}

	s.pending.Store(false)

	if !s.pending.Load() {
		// Not a systemd-initiated reload -- nothing to acknowledge.
		return nil
	}

	sent, err := daemon.SdNotify(false, daemon.SdNotifyReady)
	if err != nil {
		s.logger.Warn("sdnotify READY=1 after reload failed", zap.Error(err))
		return nil
	}
	if sent {
		s.logger.Info("sdnotify: sent READY=1 to systemd after reload")
	}

	return nil
}
