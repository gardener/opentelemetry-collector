// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package sdnotify provides an OpenTelemetry Collector extension that
// integrates the collector with systemd via the sd_notify(3) protocol.
//
// When enabled, the extension notifies systemd of the collector's lifecycle:
//   - READY=1 is sent once all pipelines have started, unblocking
//     `systemctl start` for units of Type=notify or Type=notify-reload.
//   - STOPPING=1 is sent when pipelines shut down.
//   - RELOADING=1 (with MONOTONIC_USEC) is sent on SIGHUP so systemd knows a
//     configuration reload is in progress; a second READY=1 follows once the
//     pipelines are back up. This enables zero-downtime reloads via
//     Type=notify-reload units with ReloadSignal=SIGHUP.
//   - WATCHDOG=1 is sent periodically (every WatchdogSec/2) when the unit sets
//     WatchdogSec=, acting as a keep-alive so systemd can detect a hung
//     collector and restart it. If WATCHDOG_USEC is unset, no pings are sent.
//
// If the NOTIFY_SOCKET environment variable is not set (i.e. the collector
// is not running under systemd), the extension operates as a no-op and does
// not fail startup.
package sdnotify

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension"
)

// componentType is the name of this extension in configuration.
const componentType = "sdnotify"

// NewFactory returns the factory for the sdnotify extension.
func NewFactory() extension.Factory {
	return extension.NewFactory(
		component.MustNewType(componentType),
		createDefaultConfig,
		createExtension,
		component.StabilityLevelDevelopment,
	)
}

func createDefaultConfig() component.Config {
	return &Config{}
}

func createExtension(
	_ context.Context,
	set extension.Settings,
	cfg component.Config,
) (extension.Extension, error) {
	return newSDNotify(cfg.(*Config), set.Logger), nil
}
