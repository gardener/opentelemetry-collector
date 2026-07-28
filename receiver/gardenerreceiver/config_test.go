// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package gardenerreceiver

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConfigValidation_Empty(t *testing.T) {
	cfg := &Config{}
	err := cfg.Validate()
	require.Error(t, err, "Validate() should fail for zero collection_interval")
}

func TestConfigValidation_ZeroCollectionInterval(t *testing.T) {
	cfg := &Config{CollectionInterval: 0}
	err := cfg.Validate()
	require.Error(t, err, "Validate() should fail for zero collection_interval")
}

func TestConfigValidation_ValidIntervals(t *testing.T) {
	cfg := &Config{CollectionInterval: 30 * time.Second}
	err := cfg.Validate()
	require.NoError(t, err, "Validate() should succeed for positive collection_interval")
}

func TestConfigValidation_InvalidResource(t *testing.T) {
	cfg := &Config{
		CollectionInterval: 30 * time.Second,
		Resources:          []string{"invalid-resource"},
	}
	err := cfg.Validate()
	require.Error(t, err, "Validate() should fail for invalid resource")
}

func TestConfigValidation_ValidResource(t *testing.T) {
	cfg := &Config{
		CollectionInterval: 30 * time.Second,
		Resources:          []string{"shoots", "seeds"},
	}
	err := cfg.Validate()
	require.NoError(t, err, "Validate() should succeed for valid resources")
}

func TestConfigValidation_EmptyResource(t *testing.T) {
	cfg := &Config{
		CollectionInterval: 30 * time.Second,
		Resources:          []string{},
	}
	err := cfg.Validate()
	require.NoError(t, err, "Validate() should succeed for empty resources")
}

func TestConfigValidation_ValidNamespace(t *testing.T) {
	cfg := &Config{
		CollectionInterval: 30 * time.Second,
		Namespace:          "garden-my-project",
	}
	err := cfg.Validate()
	require.NoError(t, err, "Validate() should succeed for a valid namespace")
}

func TestConfigValidation_EmptyNamespace(t *testing.T) {
	cfg := &Config{
		CollectionInterval: 30 * time.Second,
		Namespace:          "",
	}
	err := cfg.Validate()
	require.NoError(t, err, "Validate() should succeed for an empty namespace")
}

func TestConfigValidation_InvalidNamespace(t *testing.T) {
	for _, ns := range []string{"Invalid_Namespace", "-leading-dash", "trailing-dash-", "has.dot", "UPPER"} {
		cfg := &Config{
			CollectionInterval: 30 * time.Second,
			Namespace:          ns,
		}
		err := cfg.Validate()
		require.Error(t, err, "Validate() should fail for invalid namespace %q", ns)
	}
}

func TestConfigValidation_KubeconfigExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kubeconfig")
	require.NoError(t, os.WriteFile(path, []byte(`apiVersion: v1
kind: Config
clusters: []
contexts: []
users: []
`), 0o600))

	cfg := &Config{
		CollectionInterval: 30 * time.Second,
		Kubeconfig:         path,
	}
	err := cfg.Validate()
	require.NoError(t, err, "Validate() should succeed when kubeconfig points to a valid kubeconfig file")
}

func TestConfigValidation_KubeconfigMalformed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kubeconfig")
	require.NoError(t, os.WriteFile(path, []byte("apiVersion: ["), 0o600))

	cfg := &Config{
		CollectionInterval: 30 * time.Second,
		Kubeconfig:         path,
	}
	err := cfg.Validate()
	require.Error(t, err, "Validate() should fail when kubeconfig is malformed")
}

func TestConfigValidation_KubeconfigMissing(t *testing.T) {
	cfg := &Config{
		CollectionInterval: 30 * time.Second,
		Kubeconfig:         filepath.Join(t.TempDir(), "does-not-exist"),
	}
	err := cfg.Validate()
	require.Error(t, err, "Validate() should fail when kubeconfig does not exist")
}

func TestConfigValidation_KubeconfigIsDirectory(t *testing.T) {
	cfg := &Config{
		CollectionInterval: 30 * time.Second,
		Kubeconfig:         t.TempDir(),
	}
	err := cfg.Validate()
	require.Error(t, err, "Validate() should fail when kubeconfig points to a directory")
}

func TestHasShootResource(t *testing.T) {
	cfg := &Config{
		Resources: []string{"shoots", "seeds"},
	}
	require.True(t, cfg.HasShootResource(), "HasShootResource() should be true")

	cfg = &Config{
		Resources: []string{"seeds"},
	}
	require.False(t, cfg.HasShootResource(), "HasShootResource() should be false")

	cfg = &Config{
		Resources: []string{},
	}
	require.False(t, cfg.HasShootResource(), "HasShootResource() should be false for empty resources")
}

func TestHasSeedResource(t *testing.T) {
	cfg := &Config{
		Resources: []string{"shoots", "seeds"},
	}
	require.True(t, cfg.HasSeedResource(), "HasSeedResource() should be true")

	cfg = &Config{
		Resources: []string{"shoots"},
	}
	require.False(t, cfg.HasSeedResource(), "HasSeedResource() should be false")

	cfg = &Config{
		Resources: []string{},
	}
	require.False(t, cfg.HasSeedResource(), "HasSeedResource() should be false for empty resources")
}
