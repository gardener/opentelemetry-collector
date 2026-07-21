// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package gardenerreceiver

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/validation"
)

type Config struct {
	// Kubeconfig is the path to the Kubernetes configuration file.
	Kubeconfig         string        `mapstructure:"kubeconfig"`
	Namespace          string        `mapstructure:"namespace"`
	SyncPeriod         time.Duration `mapstructure:"sync_period"`
	CollectionInterval time.Duration `mapstructure:"collection_interval"`
	Resources          []string      `mapstructure:"resources"`
}

func (cfg *Config) Validate() error {
	if cfg.SyncPeriod < 0 {
		return fmt.Errorf("sync_period must not be negative")
	}
	if cfg.CollectionInterval <= 0 {
		return fmt.Errorf("collection_interval must be positive")
	}
	if err := validateNamespace(cfg.Namespace); err != nil {
		return err
	}
	if err := validateKubeconfig(cfg.Kubeconfig); err != nil {
		return err
	}
	return validateResources(cfg.Resources)
}

func validateNamespace(namespace string) error {
	if namespace == "" {
		return nil
	}
	if errs := validation.IsDNS1123Label(namespace); len(errs) > 0 {
		return fmt.Errorf("invalid namespace %q: %s", namespace, strings.Join(errs, "; "))
	}
	return nil
}

func validateKubeconfig(kubeconfig string) error {
	if kubeconfig == "" {
		return nil
	}
	info, err := os.Stat(kubeconfig)
	if err != nil {
		return fmt.Errorf("kubeconfig %q is not accessible: %w", kubeconfig, err)
	}
	if info.IsDir() {
		return fmt.Errorf("kubeconfig %q must be a file, not a directory", kubeconfig)
	}
	return nil
}

var validResources = []string{"shoots", "seeds", "projects", "managedseeds", "gardenlets"}

func validateResources(resources []string) error {
	for _, res := range resources {
		if !slices.Contains(validResources, res) {
			return fmt.Errorf("invalid resource type: %s; valid types are %v", res, validResources)
		}
	}
	return nil
}

func (cfg *Config) HasShootResource() bool {
	return slices.Contains(cfg.Resources, "shoots")
}

func (cfg *Config) HasSeedResource() bool {
	return slices.Contains(cfg.Resources, "seeds")
}

func (cfg *Config) HasProjectResource() bool {
	return slices.Contains(cfg.Resources, "projects")
}

func (cfg *Config) HasManagedSeedResource() bool {
	return slices.Contains(cfg.Resources, "managedseeds")
}

func (cfg *Config) HasGardenletResource() bool {
	return slices.Contains(cfg.Resources, "gardenlets")
}
