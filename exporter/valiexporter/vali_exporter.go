// Copyright 2025 SAP SE or an SAP affiliate company and Gardener contributors
// SPDX-License-Identifier: Apache-2.0

package valiexporter // import "github.com/gardener/opentelemetry-collector/exporter/valiexporter"

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/exporter"

	"github.com/gardener/opentelemetry-collector/exporter/valiexporter/internal/metadata"
)

// NewFactory returns an exporter.Factory that constructs nop exporters.
func NewFactory() exporter.Factory {
	return exporter.NewFactory(metadata.Type,
		func() component.Config { return &struct{}{} },
		exporter.WithLogs(createLogs, metadata.LogsStability),
	)
}

func createLogs(context.Context, exporter.Settings, component.Config) (exporter.Logs, error) {
	return nopInstance, nil
}

var nopInstance = &nop{
	Consumer: consumertest.NewNop(),
}

type nop struct {
	component.StartFunc
	component.ShutdownFunc
	consumertest.Consumer
}
