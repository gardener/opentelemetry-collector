package sdnotifyextension

import (
	"go.opentelemetry.io/collector/confmap/xconfmap"
)

var _ xconfmap.Validator = (*Config)(nil)

// Config controls how the sdnotify extension talks to systemd.
type Config struct {
	// FailIfNotSupervised makes Start return an error when the process is
	// not running under systemd (NOTIFY_SOCKET unset).
	FailIfNotSupervised bool `mapstructure:"fail_if_not_supervised"`
}

// Validate is called by the collector before Start.
func (c *Config) Validate() error {
	return nil
}
