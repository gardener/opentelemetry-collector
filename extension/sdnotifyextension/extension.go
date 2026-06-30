package sdnotifyextension

import (
	"context"
	"fmt"
	"os"

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
}

type sdnotify struct {
	cfg    *Config
	logger *zap.Logger

	host component.Host // captured in Start, so Ready can resolve siblings
}

var (
	_ Extension                             = (*sdnotify)(nil)
	_ extensioncapabilities.PipelineWatcher = (*sdnotify)(nil)
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
