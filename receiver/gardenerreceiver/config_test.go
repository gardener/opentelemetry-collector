// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package gardenerreceiver

import (
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
