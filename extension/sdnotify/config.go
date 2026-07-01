package sdnotify

import (
	"go.opentelemetry.io/collector/confmap/xconfmap"
)

var _ xconfmap.Validator = (*Config)(nil)

// Config controls how the sdnotify extension talks to systemd.
type Config struct{}

// Validate is called by the collector before Start.
func (c *Config) Validate() error {
	return nil
}
