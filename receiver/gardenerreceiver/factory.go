// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package gardenerreceiver

import (
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/receiver"
)

const (
	typeStr = "gardener"
)

func createDefaultConfig() component.Config {
	return &Config{
		SyncPeriod:         time.Hour,
		Resources:          []string{"shoots", "seeds"},
		CollectionInterval: 30 * time.Second,
	}
}

func NewFactory() receiver.Factory {
	return receiver.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		receiver.WithMetrics(newGardenerReceiver, component.StabilityLevelAlpha),
	)
}
