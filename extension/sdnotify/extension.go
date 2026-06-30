package sdnotify

import (
	"context"
	"fmt"
	"os"
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

	host component.Host // captured in Start, so Ready can resolve siblings

	// configNotified flips to true after the first NotifyConfigSnapshot call,
	// which always fires at startup with the initial config. We only want to
	// emit RELOADING=1/READY=1 for subsequent calls (actual reloads), not for
	// that initial notification -- which is handled by Ready() instead.
	configNotified bool
}

var (
	_ Extension                                  = (*sdnotify)(nil)
	_ extensioncapabilities.PipelineWatcher      = (*sdnotify)(nil)
	_ extensioncapabilities.ConfigSnapshotWatcher = (*sdnotify)(nil)
)

func newSDNotify(cfg *Config, logger *zap.Logger) *sdnotify {
	return &sdnotify{cfg: cfg, logger: logger}
}

func (s *sdnotify) Start(_ context.Context, host component.Host) error {
	s.host = host

	// If the variable is not set, the protocol is a no-op.
	if s.cfg.FailIfNotSupervised && os.Getenv("NOTIFY_SOCKET") == "" {
		return fmt.Errorf("sdnotify: NOTIFY_SOCKET not set; not running under systemd")
	}

	return nil
}

func (s *sdnotify) Shutdown(_ context.Context) error {
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
// The collector's reload pipeline calls NotifyConfigSnapshot *after* the new
// config has been applied, so by the time we are invoked the reload is
// effectively complete. We still emit the RELOADING+MONOTONIC_USEC marker
// (carrying the wall-clock instant the new config was observed) immediately
// followed by READY=1, so the systemd-side state machine progresses through
// the "reloading" state and back to "active" cleanly. This is enough for
// `systemctl reload` UX to report success and for Type=notify-reload units to
// transition correctly.
//
// The initial NotifyConfigSnapshot at startup is skipped here: Ready() (from
// PipelineWatcher) is the proper place to send READY=1 the first time.
func (s *sdnotify) NotifyConfigSnapshot(_ context.Context, _ extensioncapabilities.ConfigSnapshot) error {
	if !s.configNotified {
		s.configNotified = true
		return nil
	}

	// Per sd_notify(3): MONOTONIC_USEC must be CLOCK_MONOTONIC in microseconds,
	// formatted as a decimal string, in the same datagram as RELOADING=1.
	monotonicUS := uint64(monotonicNow() / time.Microsecond)
	msg := fmt.Sprintf("%s\nMONOTONIC_USEC=%d", daemon.SdNotifyReloading, monotonicUS)

	sent, err := daemon.SdNotify(false, msg)
	if err != nil {
		// Best-effort: a reload notification failure shouldn't propagate into
		// the collector's reload pipeline.
		s.logger.Warn("sdnotify RELOADING=1 failed", zap.Error(err))
		return nil
	}
	if sent {
		s.logger.Info("sdnotify: sent RELOADING=1 to systemd",
			zap.Uint64("monotonic_usec", monotonicUS))
	}

	// Reload is already complete by the time we get here, so signal READY=1
	// immediately. This makes Type=notify-reload units transition back to
	// active without an intermediate hang.
	sent, err = daemon.SdNotify(false, daemon.SdNotifyReady)
	if err != nil {
		s.logger.Warn("sdnotify READY=1 after reload failed", zap.Error(err))
		return nil
	}
	if sent {
		s.logger.Info("sdnotify: sent READY=1 to systemd after reload")
	}

	return nil
}

// monotonicNow returns CLOCK_MONOTONIC as a time.Duration since some
// unspecified epoch. Go's runtime keeps a monotonic reading inside time.Time
// values, but exposes no direct API for "give me the raw monotonic clock";
// time.Since(zero) where zero is captured at process init is the standard
// idiom for getting a monotonic-only measurement.
var monotonicEpoch = time.Now()

func monotonicNow() time.Duration {
	return time.Since(monotonicEpoch)
}
