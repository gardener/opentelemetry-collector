// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package gardenerreceiver

import (
	"testing"

	corev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	gardenerfake "github.com/gardener/gardener/pkg/client/core/clientset/versioned/fake"
	gardenerinformers "github.com/gardener/gardener/pkg/client/core/informers/externalversions"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver/receivertest"
	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestCollectSeedOperationStates(t *testing.T) {
	fakeClient := gardenerfake.NewSimpleClientset()
	seed := &corev1beta1.Seed{
		ObjectMeta: metav1.ObjectMeta{Name: "test-seed"},
		Spec: corev1beta1.SeedSpec{
			Provider: corev1beta1.SeedProvider{
				Type:   "test-provider",
				Region: "test-region",
			},
		},
		Status: corev1beta1.SeedStatus{
			LastOperation: &corev1beta1.LastOperation{
				Type:     corev1beta1.LastOperationTypeReconcile,
				State:    corev1beta1.LastOperationStateSucceeded,
				Progress: 100,
			},
		},
	}

	factory := gardenerinformers.NewSharedInformerFactory(fakeClient, 0)
	informer := factory.Core().V1beta1().Seeds().Informer()
	err := informer.GetStore().Add(seed)
	require.NoError(t, err)

	gardenerReceiver := &gardenerReceiver{
		config:       &Config{Kubeconfig: "/tmp/fake-kubeconfig-for-testing", Resources: []string{"seeds"}},
		settings:     receivertest.NewNopSettings(component.MustNewType("gardener")),
		consumer:     new(consumertest.MetricsSink),
		seedInformer: informer,
		logger:       zap.NewNop(),
	}

	md := pmetric.NewMetrics()
	sm := gardenerReceiver.initScopeMetrics(&md)
	gardenerReceiver.collectSeedOperationStates(&sm, nowTimestamp())

	require.Equal(t, 1, md.MetricCount())
	require.Equal(t, 1, md.DataPointCount())

	m := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0)
	require.Equal(t, "garden.seed.operation", m.Name())

	dp := m.Gauge().DataPoints().At(0)
	require.Equal(t, int64(1), dp.IntValue())

	opType, ok := dp.Attributes().Get("gardener.operation.type")
	require.True(t, ok)
	require.Equal(t, "Reconcile", opType.Str())

	opState, ok := dp.Attributes().Get("gardener.operation.state")
	require.True(t, ok)
	require.Equal(t, "Succeeded", opState.Str())

	progress, ok := dp.Attributes().Get("gardener.operation.progress")
	require.True(t, ok)
	require.Equal(t, int64(100), progress.Int())
}

func TestCollectSeedOperationStates_NoLastOperation(t *testing.T) {
	fakeClient := gardenerfake.NewSimpleClientset()
	seed := &corev1beta1.Seed{
		ObjectMeta: metav1.ObjectMeta{Name: "test-seed"},
		Spec: corev1beta1.SeedSpec{
			Provider: corev1beta1.SeedProvider{Type: "aws", Region: "eu-west-1"},
		},
	}

	factory := gardenerinformers.NewSharedInformerFactory(fakeClient, 0)
	informer := factory.Core().V1beta1().Seeds().Informer()
	require.NoError(t, informer.GetStore().Add(seed))

	gardenerReceiver := &gardenerReceiver{
		config:       &Config{Kubeconfig: "/tmp/fake-kubeconfig-for-testing", Resources: []string{"seeds"}},
		settings:     receivertest.NewNopSettings(component.MustNewType("gardener")),
		consumer:     new(consumertest.MetricsSink),
		seedInformer: informer,
		logger:       zap.NewNop(),
	}

	md := pmetric.NewMetrics()
	sm := gardenerReceiver.initScopeMetrics(&md)
	gardenerReceiver.collectSeedOperationStates(&sm, nowTimestamp())

	// No last operation: metric is emitted but with no data points
	require.Equal(t, 1, md.MetricCount())
	require.Equal(t, 0, md.DataPointCount())
}

func TestEmitSeeds_Empty(t *testing.T) {
	fakeClient := gardenerfake.NewSimpleClientset()

	factory := gardenerinformers.NewSharedInformerFactory(fakeClient, 0)
	informer := factory.Core().V1beta1().Seeds().Informer()

	set := receivertest.NewNopSettings(component.MustNewType("gardener"))
	consumer := new(consumertest.MetricsSink)
	cfg := &Config{
		Kubeconfig: "/tmp/fake-kubeconfig-for-testing",
		Resources:  []string{"seed"},
	}

	gardenerReceiver := &gardenerReceiver{
		config:       cfg,
		settings:     set,
		consumer:     consumer,
		seedInformer: informer,
		logger:       zap.NewNop(),
	}

	md := pmetric.NewMetrics()
	sm := gardenerReceiver.initScopeMetrics(&md)
	gardenerReceiver.collectSeedInfoMetrics(&sm, nowTimestamp())

	require.Equal(t, 0, consumer.DataPointCount(), "unexpected data points")
	require.Equal(t, 0, md.MetricCount(), "unexpected metric count")
	require.Equal(t, 0, md.DataPointCount(), "unexpected data point count")
}

func TestEmitSeeds(t *testing.T) {
	fakeClient := gardenerfake.NewSimpleClientset()
	seed := &corev1beta1.Seed{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-seed",
		},
		Spec: corev1beta1.SeedSpec{
			Provider: corev1beta1.SeedProvider{
				Type:   "test-provider",
				Region: "test-region",
			},
		},
		Status: corev1beta1.SeedStatus{
			KubernetesVersion: ptr.To("1.27.0"),
		},
	}

	factory := gardenerinformers.NewSharedInformerFactory(fakeClient, 0)
	informer := factory.Core().V1beta1().Seeds().Informer()
	err := informer.GetStore().Add(seed)
	require.NoError(t, err, "failed to add seed to informer store")

	set := receivertest.NewNopSettings(component.MustNewType("gardener"))
	consumer := new(consumertest.MetricsSink)
	cfg := &Config{
		Kubeconfig: "/tmp/fake-kubeconfig-for-testing",
		Resources:  []string{"seed"},
	}

	gardenerReceiver := &gardenerReceiver{
		config:       cfg,
		settings:     set,
		consumer:     consumer,
		seedInformer: informer,
		logger:       zap.NewNop(),
	}

	md := pmetric.NewMetrics()
	sm := gardenerReceiver.initScopeMetrics(&md)
	gardenerReceiver.collectSeedInfoMetrics(&sm, nowTimestamp())

	require.Equal(t, 0, consumer.DataPointCount(), "unexpected data points")
	require.Equal(t, 1, md.MetricCount(), "unexpected metric count")
	require.Equal(t, 1, md.DataPointCount(), "unexpected data point count")
	metrics := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0)
	require.Equal(t, "garden.seed.info", metrics.Name(), "unexpected metric name")
	dp := metrics.Gauge().DataPoints().At(0)
	require.Equal(t, int64(1), dp.IntValue(), "unexpected metric value")
	attributes := dp.Attributes()
	name, ok := attributes.Get("gardener.seed.name")
	require.True(t, ok, "missing name attribute")
	require.Equal(t, "test-seed", name.Str(), "unexpected name attribute")
	iaas, ok := attributes.Get("cloud.provider")
	require.True(t, ok, "missing cloud.provider attribute")
	require.Equal(t, "test-provider", iaas.Str(), "unexpected cloud.provider attribute")
	region, ok := attributes.Get("cloud.region")
	require.True(t, ok, "missing cloud.region attribute")
	require.Equal(t, "test-region", region.Str(), "unexpected cloud.region attribute")
	version, ok := attributes.Get("gardener.kubernetes.version")
	require.True(t, ok, "missing k8s.version attribute")
	require.Equal(t, "1.27.0", version.Str(), "unexpected k8s.version attribute")
	visible, ok := attributes.Get("gardener.seed.visible")
	require.True(t, ok, "missing gardener.seed.visible attribute")
	require.True(t, visible.Bool(), "unexpected gardener.seed.visible attribute")
	protected, ok := attributes.Get("gardener.seed.protected")
	require.True(t, ok, "missing gardener.seed.protected attribute")
	require.False(t, protected.Bool(), "unexpected gardener.seed.protected attribute")
}

func TestEmitSeeds_Protected(t *testing.T) {
	fakeClient := gardenerfake.NewSimpleClientset()
	seed := &corev1beta1.Seed{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-seed",
		},
		Spec: corev1beta1.SeedSpec{
			Provider: corev1beta1.SeedProvider{
				Type:   "test-provider",
				Region: "test-region",
			},
			Settings: &corev1beta1.SeedSettings{
				Scheduling: &corev1beta1.SeedSettingScheduling{
					Visible: true,
				},
			},
			Taints: []corev1beta1.SeedTaint{
				{
					Key:   corev1beta1.SeedTaintProtected,
					Value: ptr.To("true"),
				},
			},
		},
		Status: corev1beta1.SeedStatus{
			KubernetesVersion: ptr.To("1.27.0"),
		},
	}

	factory := gardenerinformers.NewSharedInformerFactory(fakeClient, 0)
	informer := factory.Core().V1beta1().Seeds().Informer()
	err := informer.GetStore().Add(seed)
	require.NoError(t, err, "failed to add seed to informer store")

	set := receivertest.NewNopSettings(component.MustNewType("gardener"))
	consumer := new(consumertest.MetricsSink)
	cfg := &Config{
		Kubeconfig: "/tmp/fake-kubeconfig-for-testing",
		Resources:  []string{"seed"},
	}

	gardenerReceiver := &gardenerReceiver{
		config:       cfg,
		settings:     set,
		consumer:     consumer,
		seedInformer: informer,
		logger:       zap.NewNop(),
	}

	md := pmetric.NewMetrics()
	sm := gardenerReceiver.initScopeMetrics(&md)
	gardenerReceiver.collectSeedInfoMetrics(&sm, nowTimestamp())

	require.Equal(t, 0, consumer.DataPointCount(), "unexpected data points")
	require.Equal(t, 1, md.MetricCount(), "unexpected metric count")
	require.Equal(t, 1, md.DataPointCount(), "unexpected data point count")
	metrics := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0)
	require.Equal(t, "garden.seed.info", metrics.Name(), "unexpected metric name")
	dp := metrics.Gauge().DataPoints().At(0)
	require.Equal(t, int64(1), dp.IntValue(), "unexpected metric value")
	attributes := dp.Attributes()
	protected, ok := attributes.Get("gardener.seed.protected")
	require.True(t, ok, "missing gardener.seed.protected attribute")
	require.True(t, protected.Bool(), "unexpected gardener.seed.protected attribute")
}

func TestEmitSeeds_Visible(t *testing.T) {
	fakeClient := gardenerfake.NewSimpleClientset()
	seed := &corev1beta1.Seed{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-seed",
		},
		Spec: corev1beta1.SeedSpec{
			Provider: corev1beta1.SeedProvider{
				Type:   "test-provider",
				Region: "test-region",
			},
			Settings: &corev1beta1.SeedSettings{
				Scheduling: &corev1beta1.SeedSettingScheduling{
					Visible: true,
				},
			},
		},
		Status: corev1beta1.SeedStatus{
			KubernetesVersion: ptr.To("1.27.0"),
		},
	}

	factory := gardenerinformers.NewSharedInformerFactory(fakeClient, 0)
	informer := factory.Core().V1beta1().Seeds().Informer()
	err := informer.GetStore().Add(seed)
	require.NoError(t, err, "failed to add seed to informer store")

	set := receivertest.NewNopSettings(component.MustNewType("gardener"))
	consumer := new(consumertest.MetricsSink)
	cfg := &Config{
		Kubeconfig: "/tmp/fake-kubeconfig-for-testing",
		Resources:  []string{"seed"},
	}

	gardenerReceiver := &gardenerReceiver{
		config:       cfg,
		settings:     set,
		consumer:     consumer,
		seedInformer: informer,
		logger:       zap.NewNop(),
	}

	md := pmetric.NewMetrics()
	sm := gardenerReceiver.initScopeMetrics(&md)
	gardenerReceiver.collectSeedInfoMetrics(&sm, nowTimestamp())

	require.Equal(t, 0, consumer.DataPointCount(), "unexpected data points")
	require.Equal(t, 1, md.MetricCount(), "unexpected metric count")
	require.Equal(t, 1, md.DataPointCount(), "unexpected data point count")
	metrics := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0)
	require.Equal(t, "garden.seed.info", metrics.Name(), "unexpected metric name")
	dp := metrics.Gauge().DataPoints().At(0)
	require.Equal(t, int64(1), dp.IntValue(), "unexpected metric value")
	attributes := dp.Attributes()
	visible, ok := attributes.Get("gardener.seed.visible")
	require.True(t, ok, "missing gardener.seed.visible attribute")
	require.True(t, visible.Bool(), "unexpected gardener.seed.visible attribute")
}

func TestEmitSeedCapacity(t *testing.T) {
	fakeClient := gardenerfake.NewSimpleClientset()
	seed := &corev1beta1.Seed{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-seed",
		},
		Spec: corev1beta1.SeedSpec{
			Provider: corev1beta1.SeedProvider{
				Type:   "test-provider",
				Region: "test-region",
			},
		},
		Status: corev1beta1.SeedStatus{
			KubernetesVersion: ptr.To("1.27.0"),
			Capacity: v1.ResourceList{
				"cpu":    resource.MustParse("4"),
				"memory": resource.MustParse("8192"),
			},
		},
	}

	factory := gardenerinformers.NewSharedInformerFactory(fakeClient, 0)
	informer := factory.Core().V1beta1().Seeds().Informer()
	err := informer.GetStore().Add(seed)
	require.NoError(t, err, "failed to add seed to informer store")

	set := receivertest.NewNopSettings(component.MustNewType("gardener"))
	consumer := new(consumertest.MetricsSink)
	cfg := &Config{
		Kubeconfig: "/tmp/fake-kubeconfig-for-testing",
		Resources:  []string{"seed"},
	}

	gardenerReceiver := &gardenerReceiver{
		config:       cfg,
		settings:     set,
		consumer:     consumer,
		seedInformer: informer,
		logger:       zap.NewNop(),
	}

	md := pmetric.NewMetrics()
	sm := gardenerReceiver.initScopeMetrics(&md)
	gardenerReceiver.collectSeedCapacityMetrics(&sm, nowTimestamp())

	require.Equal(t, 0, consumer.DataPointCount(), "unexpected data points")
	require.Equal(t, 1, md.MetricCount(), "unexpected metric count")
	require.Equal(t, 2, md.DataPointCount(), "unexpected data point count")
	metrics := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0)
	require.Equal(t, "garden.seed.capacity", metrics.Name(), "unexpected metric name")

	resourceValues := map[string]int64{}
	for i := 0; i < metrics.Gauge().DataPoints().Len(); i++ {
		dp := metrics.Gauge().DataPoints().At(i)
		res, ok := dp.Attributes().Get("gardener.seed.resource")
		require.True(t, ok, "missing resource attribute")
		resourceValues[res.Str()] = dp.IntValue()
	}
	require.Equal(t, int64(4), resourceValues["cpu"], "unexpected metric value for cpu")
	require.Equal(t, int64(8192), resourceValues["memory"], "unexpected metric value for memory")
}

func TestCollectSeedConditions(t *testing.T) {
	fakeClient := gardenerfake.NewSimpleClientset()
	seed := &corev1beta1.Seed{
		ObjectMeta: metav1.ObjectMeta{Name: "test-seed"},
		Spec: corev1beta1.SeedSpec{
			Provider: corev1beta1.SeedProvider{
				Type:   "test-provider",
				Region: "test-region",
			},
		},
		Status: corev1beta1.SeedStatus{
			Conditions: []corev1beta1.Condition{
				{
					Type:   "SeedSystemComponentsHealthy",
					Status: corev1beta1.ConditionTrue,
					Reason: "AllComponentsHealthy",
				},
			},
		},
	}

	factory := gardenerinformers.NewSharedInformerFactory(fakeClient, 0)
	informer := factory.Core().V1beta1().Seeds().Informer()
	require.NoError(t, informer.GetStore().Add(seed))

	r := &gardenerReceiver{
		config:       &Config{Kubeconfig: "/tmp/fake", Resources: []string{"seeds"}},
		settings:     receivertest.NewNopSettings(component.MustNewType("gardener")),
		consumer:     new(consumertest.MetricsSink),
		seedInformer: informer,
		logger:       zap.NewNop(),
	}

	md := pmetric.NewMetrics()
	sm := r.initScopeMetrics(&md)
	r.collectSeedConditions(&sm, nowTimestamp())

	require.Equal(t, 1, md.MetricCount())
	require.Equal(t, 1, md.DataPointCount())
	m := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0)
	require.Equal(t, "garden.seed.condition", m.Name())

	dp := m.Gauge().DataPoints().At(0)
	seedName, ok := dp.Attributes().Get("gardener.seed.name")
	require.True(t, ok)
	require.Equal(t, "test-seed", seedName.Str())
	condType, ok := dp.Attributes().Get("gardener.condition.type")
	require.True(t, ok)
	require.Equal(t, "SeedSystemComponentsHealthy", condType.Str())
}

func TestCollectSeedAllocatableMetrics(t *testing.T) {
	fakeClient := gardenerfake.NewSimpleClientset()
	seed := &corev1beta1.Seed{
		ObjectMeta: metav1.ObjectMeta{Name: "test-seed"},
		Spec: corev1beta1.SeedSpec{
			Provider: corev1beta1.SeedProvider{Type: "aws", Region: "eu-west-1"},
		},
		Status: corev1beta1.SeedStatus{
			Allocatable: v1.ResourceList{
				"shoots": resource.MustParse("100"),
			},
		},
	}

	factory := gardenerinformers.NewSharedInformerFactory(fakeClient, 0)
	informer := factory.Core().V1beta1().Seeds().Informer()
	require.NoError(t, informer.GetStore().Add(seed))

	r := &gardenerReceiver{
		config:       &Config{Kubeconfig: "/tmp/fake", Resources: []string{"seeds"}},
		settings:     receivertest.NewNopSettings(component.MustNewType("gardener")),
		consumer:     new(consumertest.MetricsSink),
		seedInformer: informer,
		logger:       zap.NewNop(),
	}

	md := pmetric.NewMetrics()
	sm := r.initScopeMetrics(&md)
	r.collectSeedAllocatableMetrics(&sm, nowTimestamp())

	require.Equal(t, 1, md.MetricCount())
	require.Equal(t, 1, md.DataPointCount())
	m := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0)
	require.Equal(t, "garden.seed.usage", m.Name())
	require.Equal(t, int64(100), m.Gauge().DataPoints().At(0).IntValue())
}
