// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package sdnotify

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/coreos/go-systemd/v22/daemon"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension"
	"go.opentelemetry.io/collector/extension/extensioncapabilities"
	"go.uber.org/zap"
)

type sdnotify struct {
	cfg    *Config
	logger *zap.Logger
	host   component.Host
	isNoop bool

	sigCh        chan os.Signal
	shutdownCh   chan struct{}
	shutdownOnce sync.Once
}

// Extension is the union of capability interfaces sdnotify implements.
type Extension interface {
	extension.Extension
	extensioncapabilities.PipelineWatcher
}

var _ Extension = (*sdnotify)(nil)

func newSDNotify(cfg *Config, logger *zap.Logger) *sdnotify {
	return &sdnotify{
		cfg:        cfg,
		logger:     logger,
		sigCh:      make(chan os.Signal, 1),
		shutdownCh: make(chan struct{}),
	}
}

func (s *sdnotify) Start(_ context.Context, host component.Host) error {
	s.host = host

	// If NOTIFY_SOCKET environment variable is unset, then the sd_notify protocol is no-op.
	if os.Getenv("NOTIFY_SOCKET") == "" {
		s.isNoop = true
		s.logger.Warn("NOTIFY_SOCKET is not set; sd_notify support is disabled")
		return nil
	}

	// SIGHUP handling: we only emit RELOADING=1 (with MONOTONIC_USEC per sd_notify(3))
	// so that Type=notify-reload units see the correct "entering reload" transition.
	// We do NOT translate SIGHUP into a process exit or a fatal-error report.
	//
	// The OpenTelemetry Collector installs its own SIGHUP handler in
	// otelcol.Collector.Run that performs an in-process reload:
	// service.Shutdown -> setupConfigurationComponents. That reload path invokes
	// PipelineWatcher.NotReady on this extension (which sends STOPPING=1) and,
	// after the fresh config is up, PipelineWatcher.Ready (which sends READY=1).
	// The end-to-end datagram sequence systemd sees on SIGHUP is therefore
	// RELOADING=1 -> STOPPING=1 -> READY=1, which satisfies Type=notify-reload's
	// contract that READY=1 is re-asserted once reload completes.
	//
	// Consequence: SIGHUP does NOT cycle the process. MainPID stays the same and
	// NRestarts stays 0. If you need a supervisor-driven restart instead of an
	// in-process reload, use `systemctl restart` (or Restart=on-failure combined
	// with a real fatal), not SIGHUP.
	monotonicEpoch := time.Now()
	signal.Notify(s.sigCh, syscall.SIGHUP)

	go func() {
		for {
			select {
			case <-s.shutdownCh:
				return
			case <-s.sigCh:
				// Per sd_notify(3): MONOTONIC_USEC must be CLOCK_MONOTONIC in microseconds,
				// formatted as a decimal string, in the same datagram as RELOADING=1.
				monotonicUSec := uint64(time.Since(monotonicEpoch) / time.Microsecond)
				msg := fmt.Sprintf(
					"%s\nMONOTONIC_USEC=%d",
					daemon.SdNotifyReloading,
					monotonicUSec,
				)

				sent, err := daemon.SdNotify(false, msg)
				if err != nil {
					s.logger.Warn("sdnotify RELOADING=1 failed", zap.Error(err))
				} else if sent {
					s.logger.Info(
						"sdnotify: SIGHUP received, sent RELOADING=1 to systemd",
						zap.Uint64("monotonic_usec", monotonicUSec),
					)
				}
				// Deliberately no ReportStatus / no exit: otelcol.Collector.Run
				// owns the SIGHUP-triggered reload; STOPPING=1/READY=1 are emitted
				// by NotReady/Ready as the reload tears down and rebuilds pipelines.
			}
		}
	}()

	return nil
}

func (s *sdnotify) Shutdown(_ context.Context) error {
	if s.isNoop {
		return nil
	}
	s.shutdownOnce.Do(func() {
		close(s.shutdownCh)
		signal.Stop(s.sigCh)
	})
	return nil
}

func (s *sdnotify) Ready() error {
	sent, err := daemon.SdNotify(false, daemon.SdNotifyReady)
	if err != nil {
		return fmt.Errorf("sdnotify READY=1: %w", err)
	}
	if sent {
		s.logger.Info("sdnotify: sent READY=1 to systemd")
	} else {
		s.logger.Info("sdnotify: NOTIFY_SOCKET not set; READY=1 was a no-op")
	}
	return nil
}

func (s *sdnotify) NotReady() error {
	sent, err := daemon.SdNotify(false, daemon.SdNotifyStopping)
	if err != nil {
		// Best-effort: don't block shutdown on a notify failure.
		s.logger.Warn("sdnotify STOPPING=1 failed", zap.Error(err))
		return nil
	}
	if sent {
		s.logger.Info("sdnotify: sent STOPPING=1 to systemd")
	}
	return nil
}
