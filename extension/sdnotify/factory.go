package sdnotify

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension"
)

// componentType is the name of this extension in configuration.
const componentType = "sdnotifyextension"

// NewFactory returns the factory for the sdnotify extension.
func NewFactory() extension.Factory {
	return extension.NewFactory(
		component.MustNewType(componentType),
		createDefaultConfig,
		createExtension,
		component.StabilityLevelAlpha,
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
