// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package gardenerreceiver

import (
	"testing"

	corev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	seedmanagementv1alpha1 "github.com/gardener/gardener/pkg/apis/seedmanagement/v1alpha1"
	seedmanagementfake "github.com/gardener/gardener/pkg/client/seedmanagement/clientset/versioned/fake"
	seedmanagementinformers "github.com/gardener/gardener/pkg/client/seedmanagement/informers/externalversions"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver/receivertest"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCollectManagedSeedMetrics(t *testing.T) {
	fakeClient := seedmanagementfake.NewSimpleClientset()
	ms := &seedmanagementv1alpha1.ManagedSeed{
		ObjectMeta: metav1.ObjectMeta{Name: "my-managed-seed"},
		Spec: seedmanagementv1alpha1.ManagedSeedSpec{
			Shoot: &seedmanagementv1alpha1.Shoot{Name: "my-shoot"},
		},
	}

	factory := seedmanagementinformers.NewSharedInformerFactory(fakeClient, 0)
	informer := factory.Seedmanagement().V1alpha1().ManagedSeeds().Informer()
	require.NoError(t, informer.GetStore().Add(ms))

	r := &gardenerReceiver{
		config:              &Config{Kubeconfig: "/tmp/fake", Resources: []string{"managedseeds"}},
		settings:            receivertest.NewNopSettings(component.MustNewType("gardener")),
		consumer:            new(consumertest.MetricsSink),
		managedSeedInformer: informer,
		logger:              zap.NewNop(),
	}

	md := pmetric.NewMetrics()
	sm := r.initScopeMetrics(&md)
	r.collectManagedSeedMetrics(&sm, nowTimestamp())

	require.Equal(t, 1, md.MetricCount())
	require.Equal(t, 1, md.DataPointCount())
	m := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0)
	require.Equal(t, "garden.managed_seed.info", m.Name())

	dp := m.Gauge().DataPoints().At(0)
	require.Equal(t, int64(1), dp.IntValue())
	seedName, ok := dp.Attributes().Get("gardener.seed.name")
	require.True(t, ok)
	require.Equal(t, "my-managed-seed", seedName.Str())
	shootName, ok := dp.Attributes().Get("gardener.shoot.name")
	require.True(t, ok)
	require.Equal(t, "my-shoot", shootName.Str())
}

func TestCollectManagedSeedMetrics_Empty(t *testing.T) {
	fakeClient := seedmanagementfake.NewSimpleClientset()
	factory := seedmanagementinformers.NewSharedInformerFactory(fakeClient, 0)
	informer := factory.Seedmanagement().V1alpha1().ManagedSeeds().Informer()

	r := &gardenerReceiver{
		config:              &Config{Kubeconfig: "/tmp/fake", Resources: []string{"managedseeds"}},
		settings:            receivertest.NewNopSettings(component.MustNewType("gardener")),
		consumer:            new(consumertest.MetricsSink),
		managedSeedInformer: informer,
		logger:              zap.NewNop(),
	}

	md := pmetric.NewMetrics()
	sm := r.initScopeMetrics(&md)
	r.collectManagedSeedMetrics(&sm, nowTimestamp())

	require.Equal(t, 0, md.MetricCount())
}

func TestCollectGardenletMetrics(t *testing.T) {
	fakeClient := seedmanagementfake.NewSimpleClientset()
	gl := &seedmanagementv1alpha1.Gardenlet{
		ObjectMeta: metav1.ObjectMeta{Name: "my-gardenlet", Generation: 5},
		Status: seedmanagementv1alpha1.GardenletStatus{
			ObservedGeneration: 4,
			Conditions: []corev1beta1.Condition{
				{
					Type:   "GardenletReconciled",
					Status: corev1beta1.ConditionTrue,
					Reason: "ReconcileSucceeded",
				},
			},
		},
	}

	factory := seedmanagementinformers.NewSharedInformerFactory(fakeClient, 0)
	informer := factory.Seedmanagement().V1alpha1().Gardenlets().Informer()
	require.NoError(t, informer.GetStore().Add(gl))

	r := &gardenerReceiver{
		config:            &Config{Kubeconfig: "/tmp/fake", Resources: []string{"gardenlets"}},
		settings:          receivertest.NewNopSettings(component.MustNewType("gardener")),
		consumer:          new(consumertest.MetricsSink),
		gardenletInformer: informer,
		logger:            zap.NewNop(),
	}

	md := pmetric.NewMetrics()
	sm := r.initScopeMetrics(&md)
	r.collectGardenletMetrics(&sm, nowTimestamp())

	// Expect 3 metrics: condition, generation, observed_generation
	require.Equal(t, 3, md.MetricCount())

	metrics := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	names := map[string]bool{}
	for i := 0; i < metrics.Len(); i++ {
		names[metrics.At(i).Name()] = true
	}
	require.True(t, names["garden.gardenlet.condition"])
	require.True(t, names["garden.gardenlet.generation"])
	require.True(t, names["garden.gardenlet.observed_generation"])

	// Check generation values
	for i := 0; i < metrics.Len(); i++ {
		m := metrics.At(i)
		switch m.Name() {
		case "garden.gardenlet.generation":
			require.Equal(t, int64(5), m.Gauge().DataPoints().At(0).IntValue())
		case "garden.gardenlet.observed_generation":
			require.Equal(t, int64(4), m.Gauge().DataPoints().At(0).IntValue())
		case "garden.gardenlet.condition":
			require.Equal(t, 1, m.Gauge().DataPoints().Len())
			condType, ok := m.Gauge().DataPoints().At(0).Attributes().Get("gardener.condition.type")
			require.True(t, ok)
			require.Equal(t, "GardenletReconciled", condType.Str())
		}
	}
}
