// SPDX-FileCopyrightText: Copyright Contributors to the Gardener project
//
// SPDX-License-Identifier: Apache-2.0

package sdnotify

import (
	"go.opentelemetry.io/collector/confmap"
)

var _ confmap.Validator = (*Config)(nil)

// Config controls how the sdnotify extension talks to systemd.
type Config struct{}

// Validate is called by the collector before Start.
func (c *Config) Validate() error {
	return nil
}
