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

	// For services configured with Type=notify-reload, systemd signals the main
	// process with SIGHUP when a reload is requested. The process is responsible
	// for reloading its configuration and informing systemd when the reload has
	// completed, allowing systemd to track the reload status correctly.
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
				monotonicUSec := uint64(max(time.Since(monotonicEpoch), 0) / time.Microsecond)
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

				// otelcol.Collector.Run owns the SIGHUP-triggered reload logic.
				// This extension should not restart the process because the
				// collector handles reloads itself.
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
	switch {
	case err != nil:
		return fmt.Errorf("sdnotify READY=1: %w", err)
	case sent:
		s.logger.Info("sdnotify: sent READY=1 to systemd")
	default:
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
