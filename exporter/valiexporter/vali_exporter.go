package valiexporter // import "github.com/valyala/tsbs/cmd/tsbs_generate_queries/queries/devops/valiexporter"

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/exporter"
)

// NewFactory returns an exporter.Factory that constructs nop exporters.
func NewFactory() exporter.Factory {
	return exporter.NewFactory(
		component.MustNewType("vali"),
		func() component.Config { return &struct{}{} },
		exporter.WithTraces(createTraces, component.StabilityLevelAlpha),
		exporter.WithMetrics(createMetrics, component.StabilityLevelAlpha),
		exporter.WithLogs(createLogs, component.StabilityLevelAlpha),
	)
}

func createTraces(context.Context, exporter.Settings, component.Config) (exporter.Traces, error) {
	return nil, nil
}

func createMetrics(context.Context, exporter.Settings, component.Config) (exporter.Metrics, error) {
	return nil, nil
}

func createLogs(context.Context, exporter.Settings, component.Config) (exporter.Logs, error) {
	return nil, nil
}
