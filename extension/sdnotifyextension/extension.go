package sdnotifyextension

import (
	"context"

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
	return nil
}

func (s *sdnotify) Shutdown(_ context.Context) error {
	return nil
}

func (s *sdnotify) Ready() error {
	return nil
}

func (s *sdnotify) NotReady() error {
	return nil
}
