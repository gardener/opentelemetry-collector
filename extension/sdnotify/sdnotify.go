// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package sdnotify

import (
	"context"
	"fmt"
	"os"
	"os/signal"
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

	ctx    context.Context
	cancel context.CancelFunc
	sigCh  chan os.Signal
}

// Extension is the union of capability interfaces sdnotify implements.
type Extension interface {
	extension.Extension
	extensioncapabilities.PipelineWatcher
}

var _ Extension = (*sdnotify)(nil)

func newSDNotify(cfg *Config, logger *zap.Logger) *sdnotify {
	return &sdnotify{
		cfg:    cfg,
		logger: logger,
		sigCh:  make(chan os.Signal, 1),
	}
}

func (s *sdnotify) Start(startCtx context.Context, host component.Host) error {
	s.host = host

	// If NOTIFY_SOCKET environment variable is unset, then the sd_notify protocol is no-op.
	if os.Getenv("NOTIFY_SOCKET") == "" {
		s.logger.Warn("NOTIFY_SOCKET is not set; sd_notify support is disabled")

		return nil
	}

	// STOPPING=1 must be sent only on genuine termination (SIGINT / SIGTERM).
	s.ctx, s.cancel = signal.NotifyContext(startCtx, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-s.ctx.Done()

		// We don't want to send STOPPING=1, if we s.cancel().
		errStr := context.Cause(s.ctx).Error()
		if errStr == syscall.SIGINT.String()+" signal received" || errStr == syscall.SIGTERM.String()+" signal received" {
			sent, err := daemon.SdNotify(false, daemon.SdNotifyStopping)
			if err != nil {
				s.logger.Warn("sdnotify STOPPING=1 failed", zap.Error(err))

				return
			} else if sent {
				s.logger.Info("sdnotify: sent STOPPING=1 to systemd")
			}
		}
	}()

	// RELOADING=1\nMONOTONIC_USEC=X is send only for Type=notify-reload services.
	// Systemd signals the main process with SIGHUP when a reload is requested.
	// The process is responsible for reloading its configuration and informing
	// systemd when the reload has completed.
	monotonicEpoch := time.Now()
	signal.Notify(s.sigCh, syscall.SIGHUP)
	go func() {
		for {
			select {
			case <-s.ctx.Done():
				return

			// This extension should not restart the process, because the collector handles it by itself.
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
			}
		}
	}()

	// WATCHDOG=1 is the keep-alive ping that services need to issue in regular
	// intervals if WatchdogSec= is enabled for it.
	duration, err := daemon.SdWatchdogEnabled(false)
	switch {
	case err != nil:
		s.logger.Debug("sdnotify: SdWatchdogEnabled returned error; watchdog disabled",
			zap.Error(err))
	case duration == 0:
		s.logger.Debug("sdnotify: WATCHDOG_USEC not set; watchdog disabled")
	default:
		go func() {
			// Per sd_watchdog_enabled(3): It is recommended that a daemon sends a keep-alive
			// notification message to the service manager every half of the time returned here.
			ticker := time.NewTicker(duration / 2)
			defer ticker.Stop()
			for {
				select {
				case <-s.ctx.Done():
					return
				case <-ticker.C:
					if _, err := daemon.SdNotify(false, daemon.SdNotifyWatchdog); err != nil {
						s.logger.Debug("sdnotify WATCHDOG=1 failed", zap.Error(err))
					}
				}
			}
		}()
	}

	return nil
}

func (s *sdnotify) Shutdown(_ context.Context) error {
	if s.cancel != nil {
		// This extension should not stop the process, because the collector handles it by itself.
		signal.Stop(s.sigCh)
		s.cancel()
	}

	return nil
}

func (s *sdnotify) Ready() error {
	// READY=1 informs systemd that the collector is fully ready to receive traffic.
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
	return nil
}
