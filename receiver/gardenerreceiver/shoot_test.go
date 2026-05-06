// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package gardenerreceiver

import (
	"testing"

	corev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	securityv1alpha1 "github.com/gardener/gardener/pkg/apis/security/v1alpha1"
	seedmanagementv1alpha1 "github.com/gardener/gardener/pkg/apis/seedmanagement/v1alpha1"
	gardenerfake "github.com/gardener/gardener/pkg/client/core/clientset/versioned/fake"
	gardenerinformers "github.com/gardener/gardener/pkg/client/core/informers/externalversions"
	securityfake "github.com/gardener/gardener/pkg/client/security/clientset/versioned/fake"
	securityinformers "github.com/gardener/gardener/pkg/client/security/informers/externalversions"
	seedmanagementfake "github.com/gardener/gardener/pkg/client/seedmanagement/clientset/versioned/fake"
	seedmanagementinformers "github.com/gardener/gardener/pkg/client/seedmanagement/informers/externalversions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver/receivertest"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestEmitShoots_Empty(t *testing.T) {
	fakeClient := gardenerfake.NewSimpleClientset()

	factory := gardenerinformers.NewSharedInformerFactory(fakeClient, 0)
	informer := factory.Core().V1beta1().Shoots().Informer()

	set := receivertest.NewNopSettings(component.MustNewType("gardener"))
	consumer := new(consumertest.MetricsSink)
	cfg := &Config{
		Kubeconfig: "/tmp/fake-kubeconfig-for-testing",
		Resources:  []string{"shoot"},
	}

	gardenerReceiver := &gardenerReceiver{
		config:        cfg,
		settings:      set,
		consumer:      consumer,
		shootInformer: informer,
		logger:        zap.NewNop(),
	}

	md := pmetric.NewMetrics()
	sm := gardenerReceiver.initScopeMetrics(&md)
	gardenerReceiver.collectShootInfoMetrics(&sm, nowTimestamp(), shootLookups{
		managedSeedShoots:  map[string]struct{}{},
		seedByName:         map[string]*corev1beta1.Seed{},
		projectByNamespace: map[string]projectBillingInfo{},
	})

	require.Equal(t, 0, consumer.DataPointCount(), "unexpected data points")
	require.Equal(t, 0, md.MetricCount(), "unexpected metric count")
	require.Equal(t, 0, md.DataPointCount(), "unexpected data point count")
}

func TestEmitShoots(t *testing.T) {
	fakeClient := gardenerfake.NewSimpleClientset()
	shoot := &corev1beta1.Shoot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-shoot",
			Namespace: "garden-dev",
		},
		Spec: corev1beta1.ShootSpec{
			Provider: corev1beta1.Provider{
				Type: "test-provider",
				Workers: []corev1beta1.Worker{
					{Name: "test-worker"},
				},
			},
			Region: "test-region",
			Kubernetes: corev1beta1.Kubernetes{
				Version: "1.26.0",
			},
			SeedName: ptr.To("test-seed"),
		},
		Status: corev1beta1.ShootStatus{
			Gardener: corev1beta1.Gardener{
				Version: "1.80.0",
			},
			TechnicalID: "shoot--dev--test-shoot",
		},
	}

	factory := gardenerinformers.NewSharedInformerFactory(fakeClient, 0)
	informer := factory.Core().V1beta1().Shoots().Informer()
	err := informer.GetStore().Add(shoot)
	require.NoError(t, err, "failed to add shoot to informer store")

	set := receivertest.NewNopSettings(component.MustNewType("gardener"))
	consumer := new(consumertest.MetricsSink)
	cfg := &Config{
		Kubeconfig: "/tmp/fake-kubeconfig-for-testing",
		Resources:  []string{"shoot"},
	}

	gardenerReceiver := &gardenerReceiver{
		config:        cfg,
		settings:      set,
		consumer:      consumer,
		shootInformer: informer,
		logger:        zap.NewNop(),
	}

	md := pmetric.NewMetrics()
	sm := gardenerReceiver.initScopeMetrics(&md)
	gardenerReceiver.collectShootInfoMetrics(&sm, nowTimestamp(), shootLookups{
		managedSeedShoots:  map[string]struct{}{},
		seedByName:         map[string]*corev1beta1.Seed{},
		projectByNamespace: map[string]projectBillingInfo{},
	})

	require.Equal(t, 0, consumer.DataPointCount(), "unexpected data points")
	require.Equal(t, 1, md.MetricCount(), "unexpected metric count")
	require.Equal(t, 1, md.DataPointCount(), "unexpected data point count")
	metrics := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0)
	require.Equal(t, "garden.shoot.info", metrics.Name(), "unexpected metric name")
	dp := metrics.Gauge().DataPoints().At(0)

	require.Equal(t, int64(1), dp.IntValue(), "unexpected metric value")
	attributes := dp.Attributes()

	name, ok := attributes.Get("gardener.shoot.name")
	require.True(t, ok, "missing name attribute")
	require.Equal(t, "test-shoot", name.Str(), "unexpected name attribute")

	project, ok := attributes.Get("gardener.project.name")
	require.True(t, ok, "missing project attribute")
	require.Equal(t, "dev", project.Str(), "unexpected project attribute")

	iaas, ok := attributes.Get("cloud.provider")
	require.True(t, ok, "missing cloud.provider attribute")
	require.Equal(t, "test-provider", iaas.Str(), "unexpected cloud.provider attribute")

	region, ok := attributes.Get("cloud.region")
	require.True(t, ok, "missing cloud.region attribute")
	require.Equal(t, "test-region", region.Str(), "unexpected cloud.region attribute")

	version, ok := attributes.Get("gardener.kubernetes.version")
	require.True(t, ok, "missing k8s.version attribute")
	require.Equal(t, "1.26.0", version.Str(), "unexpected k8s.version attribute")

	version, ok = attributes.Get("gardener.version")
	require.True(t, ok, "missing gardener.version attribute")
	require.Equal(t, "1.80.0", version.Str(), "unexpected gardener.version attribute")

	seed, ok := attributes.Get("gardener.seed.name")
	require.True(t, ok, "missing gardener.seed.name attribute")
	require.Equal(t, "test-seed", seed.Str(), "unexpected gardener.seed.name attribute")

	id, ok := attributes.Get("gardener.shoot.technical_id")
	require.True(t, ok, "missing gardener.shoot.technical_id attribute")
	require.Equal(t, "shoot--dev--test-shoot", id.Str(), "unexpected gardener.shoot.technical_id attribute")
}

func TestEmitShootOperations(t *testing.T) {
	fakeClient := gardenerfake.NewSimpleClientset()
	shoot := &corev1beta1.Shoot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-shoot",
			Namespace: "garden-dev",
		},
		Spec: corev1beta1.ShootSpec{
			Provider: corev1beta1.Provider{
				Type: "test-provider",
				Workers: []corev1beta1.Worker{
					{Name: "test-worker"},
				},
			},
			Region: "test-region",
			Kubernetes: corev1beta1.Kubernetes{
				Version: "1.26.0",
			},
			SeedName: ptr.To("test-seed"),
		},
		Status: corev1beta1.ShootStatus{
			LastOperation: &corev1beta1.LastOperation{
				Type:     corev1beta1.LastOperationTypeReconcile,
				State:    corev1beta1.LastOperationStateSucceeded,
				Progress: 100,
			},
		},
	}

	factory := gardenerinformers.NewSharedInformerFactory(fakeClient, 0)
	informer := factory.Core().V1beta1().Shoots().Informer()
	err := informer.GetStore().Add(shoot)
	require.NoError(t, err, "failed to add shoot to informer store")

	set := receivertest.NewNopSettings(component.MustNewType("gardener"))
	consumer := new(consumertest.MetricsSink)
	cfg := &Config{
		Kubeconfig: "/tmp/fake-kubeconfig-for-testing",
		Resources:  []string{"shoot"},
	}

	gardenerReceiver := &gardenerReceiver{
		config:        cfg,
		settings:      set,
		consumer:      consumer,
		shootInformer: informer,
		logger:        zap.NewNop(),
	}

	md := pmetric.NewMetrics()
	sm := gardenerReceiver.initScopeMetrics(&md)
	gardenerReceiver.collectShootOperationStates(&sm, nowTimestamp())

	// collectShootOperationStates emits 2 metrics (operation_states + operation_progress_percent),
	// each with 5 data points (one per operation type: Create, Reconcile, Delete, Migrate, Restore).
	require.Equal(t, 0, consumer.DataPointCount(), "unexpected data points")
	require.Equal(t, 2, md.MetricCount(), "unexpected metric count")
	require.Equal(t, 10, md.DataPointCount(), "unexpected data point count")

	scopeMetrics := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	statesMetric := scopeMetrics.At(0)
	require.Equal(t, "garden.shoot.operation_states", statesMetric.Name(), "unexpected metric name")

	// Find the data point for the active Reconcile operation.
	var reconcileDp pmetric.NumberDataPoint
	for i := 0; i < statesMetric.Gauge().DataPoints().Len(); i++ {
		dp := statesMetric.Gauge().DataPoints().At(i)
		opType, _ := dp.Attributes().Get("gardener.operation.type")
		if opType.Str() == "Reconcile" {
			reconcileDp = dp
			break
		}
	}
	require.NotNil(t, reconcileDp, "missing Reconcile data point")

	attributes := reconcileDp.Attributes()

	name, ok := attributes.Get("gardener.shoot.name")
	require.True(t, ok, "missing name attribute")
	require.Equal(t, "test-shoot", name.Str(), "unexpected name attribute")

	project, ok := attributes.Get("gardener.project.name")
	require.True(t, ok, "missing project attribute")
	require.Equal(t, "dev", project.Str(), "unexpected project attribute")

	opType, ok := attributes.Get("gardener.operation.type")
	require.True(t, ok, "missing type attribute")
	require.Equal(t, "Reconcile", opType.Str(), "unexpected type attribute")

	state, ok := attributes.Get("gardener.operation.state")
	require.True(t, ok, "missing state attribute")
	require.Equal(t, "Succeeded", state.Str(), "unexpected state attribute")

	require.Equal(t, int64(1), reconcileDp.IntValue(), "active operation should have value 1")

	// Verify progress metric contains the right progress for the Reconcile operation.
	progressMetric := scopeMetrics.At(1)
	require.Equal(t, "garden.shoot.operation_progress_percent", progressMetric.Name(), "unexpected progress metric name")
	for i := 0; i < progressMetric.Gauge().DataPoints().Len(); i++ {
		dp := progressMetric.Gauge().DataPoints().At(i)
		opType, _ := dp.Attributes().Get("gardener.operation.type")
		if opType.Str() == "Reconcile" {
			require.Equal(t, int64(100), dp.IntValue(), "unexpected progress value")
			break
		}
	}
}

func TestEmitShootConditions(t *testing.T) {
	fakeClient := gardenerfake.NewSimpleClientset()
	shoot := &corev1beta1.Shoot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-shoot",
			Namespace: "garden-dev",
		},
		Spec: corev1beta1.ShootSpec{
			Provider: corev1beta1.Provider{
				Type: "test-provider",
				Workers: []corev1beta1.Worker{
					{Name: "test-worker"},
				},
			},
			Region: "test-region",
			Kubernetes: corev1beta1.Kubernetes{
				Version: "1.26.0",
			},
			SeedName: ptr.To("test-seed"),
		},
		Status: corev1beta1.ShootStatus{
			Conditions: []corev1beta1.Condition{
				{
					Type:   "TestCondition",
					Status: corev1beta1.ConditionTrue,
					Reason: "TestReason",
				},
			},
		},
	}

	factory := gardenerinformers.NewSharedInformerFactory(fakeClient, 0)
	informer := factory.Core().V1beta1().Shoots().Informer()
	err := informer.GetStore().Add(shoot)
	require.NoError(t, err, "failed to add shoot to informer store")

	set := receivertest.NewNopSettings(component.MustNewType("gardener"))
	consumer := new(consumertest.MetricsSink)
	cfg := &Config{
		Kubeconfig: "/tmp/fake-kubeconfig-for-testing",
		Resources:  []string{"shoot"},
	}

	gardenerReceiver := &gardenerReceiver{
		config:        cfg,
		settings:      set,
		consumer:      consumer,
		shootInformer: informer,
		logger:        zap.NewNop(),
	}

	md := pmetric.NewMetrics()
	sm := gardenerReceiver.initScopeMetrics(&md)
	gardenerReceiver.collectShootConditions(&sm, nowTimestamp(), shootLookups{
		managedSeedShoots:  map[string]struct{}{},
		seedByName:         map[string]*corev1beta1.Seed{},
		projectByNamespace: map[string]projectBillingInfo{},
	})

	require.Equal(t, 0, consumer.DataPointCount(), "unexpected data points")
	require.Equal(t, 1, md.MetricCount(), "unexpected metric count")
	require.Equal(t, 1, md.DataPointCount(), "unexpected data point count")
	metrics := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0)
	require.Equal(t, "garden.shoot.condition", metrics.Name(), "unexpected metric name")

	dp := metrics.Gauge().DataPoints().At(0)
	attributes := dp.Attributes()

	name, ok := attributes.Get("gardener.shoot.name")
	require.True(t, ok, "missing name attribute")
	require.Equal(t, "test-shoot", name.Str(), "unexpected name attribute")

	project, ok := attributes.Get("gardener.project.name")
	require.True(t, ok, "missing project attribute")
	require.Equal(t, "dev", project.Str(), "unexpected project attribute")

	conditionType, ok := attributes.Get("gardener.condition.type")
	require.True(t, ok, "missing condition.type attribute")
	require.Equal(t, "TestCondition", conditionType.Str(), "unexpected condition.type attribute")

	conditionStatus, ok := attributes.Get("gardener.condition.status")
	require.True(t, ok, "missing condition.status attribute")
	require.Equal(t, "True", conditionStatus.Str(), "unexpected condition.status attribute")

	conditionReason, ok := attributes.Get("gardener.condition.reason")
	require.True(t, ok, "missing condition.reason attribute")
	require.Equal(t, "TestReason", conditionReason.Str(), "unexpected condition.reason attribute")
}

func TestEmitShootStatus(t *testing.T) {
	fakeClient := gardenerfake.NewSimpleClientset()
	shoot := &corev1beta1.Shoot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-shoot",
			Namespace: "garden-dev",
			Labels: map[string]string{
				"shoot.gardener.cloud/status": "healthy",
			},
		},
		Spec: corev1beta1.ShootSpec{
			Provider: corev1beta1.Provider{
				Type: "test-provider",
				Workers: []corev1beta1.Worker{
					{Name: "test-worker"},
				},
			},
			Region: "test-region",
			Kubernetes: corev1beta1.Kubernetes{
				Version: "1.26.0",
			},
			SeedName: ptr.To("test-seed"),
		},
		Status: corev1beta1.ShootStatus{
			TechnicalID: "shoot--dev--test-shoot",
		},
	}

	factory := gardenerinformers.NewSharedInformerFactory(fakeClient, 0)
	informer := factory.Core().V1beta1().Shoots().Informer()
	err := informer.GetStore().Add(shoot)
	require.NoError(t, err, "failed to add shoot to informer store")

	set := receivertest.NewNopSettings(component.MustNewType("gardener"))
	consumer := new(consumertest.MetricsSink)
	cfg := &Config{
		Kubeconfig: "/tmp/fake-kubeconfig-for-testing",
		Resources:  []string{"shoot"},
	}

	gardenerReceiver := &gardenerReceiver{
		config:        cfg,
		settings:      set,
		consumer:      consumer,
		shootInformer: informer,
		logger:        zap.NewNop(),
	}

	md := pmetric.NewMetrics()
	sm := gardenerReceiver.initScopeMetrics(&md)
	gardenerReceiver.collectShootStatusMetric(&sm, nowTimestamp())

	require.Equal(t, 0, consumer.DataPointCount(), "unexpected data points")
	require.Equal(t, 1, md.MetricCount(), "unexpected metric count")
	require.Equal(t, 4, md.DataPointCount(), "unexpected data point count")
	metrics := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0)
	require.Equal(t, "garden.shoot.status", metrics.Name(), "unexpected metric name")

	statusValues := []string{"healthy", "progressing", "unhealthy", "unknown"}
	for i, status := range statusValues {
		dp := metrics.Gauge().DataPoints().At(i)
		attributes := dp.Attributes()

		name, ok := attributes.Get("gardener.shoot.name")
		require.True(t, ok, "missing name attribute")
		require.Equal(t, "test-shoot", name.Str(), "unexpected name attribute")

		project, ok := attributes.Get("gardener.project.name")
		require.True(t, ok, "missing project attribute")
		require.Equal(t, "dev", project.Str(), "unexpected project attribute")

		statusAttr, ok := attributes.Get("gardener.shoot.status")
		require.True(t, ok, "missing status attribute")
		require.Equal(t, status, statusAttr.Str(), "unexpected status attribute")

		if status == "healthy" {
			require.Equal(t, int64(1), dp.IntValue(), "expected value 1 for active status")
		} else {
			require.Equal(t, int64(0), dp.IntValue(), "expected value 0 for inactive status")
		}
	}
}

func TestEmitShootNodeMetrics(t *testing.T) {
	fakeClient := gardenerfake.NewSimpleClientset()
	shoot := &corev1beta1.Shoot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-shoot",
			Namespace: "garden-dev",
		},
		Spec: corev1beta1.ShootSpec{
			Provider: corev1beta1.Provider{
				Type: "test-provider",
				Workers: []corev1beta1.Worker{
					{
						Name:    "test-worker",
						Minimum: 1,
						Maximum: 3,
						Machine: corev1beta1.Machine{
							Architecture: ptr.To("AMD64"),
							Type:         "test-type",
							Image: &corev1beta1.ShootMachineImage{
								Name:    "test-image",
								Version: ptr.To("1.0)"),
							},
						},
					},
				},
			},
			Region: "test-region",
			Kubernetes: corev1beta1.Kubernetes{
				Version: "1.26.0",
			},
			SeedName: ptr.To("test-seed"),
		},
	}

	factory := gardenerinformers.NewSharedInformerFactory(fakeClient, 0)
	informer := factory.Core().V1beta1().Shoots().Informer()
	err := informer.GetStore().Add(shoot)
	require.NoError(t, err, "failed to add shoot to informer store")

	set := receivertest.NewNopSettings(component.MustNewType("gardener"))
	consumer := new(consumertest.MetricsSink)
	cfg := &Config{
		Kubeconfig: "/tmp/fake-kubeconfig-for-testing",
		Resources:  []string{"shoot"},
	}

	gardenerReceiver := &gardenerReceiver{
		config:        cfg,
		settings:      set,
		consumer:      consumer,
		shootInformer: informer,
		logger:        zap.NewNop(),
	}

	md := pmetric.NewMetrics()
	sm := gardenerReceiver.initScopeMetrics(&md)
	gardenerReceiver.collectShootNodeMetrics(&sm, nowTimestamp())

	require.Equal(t, 0, consumer.DataPointCount(), "unexpected data points")
	require.Equal(t, 5, md.MetricCount(), "unexpected metric count")
	require.Equal(t, 5, md.DataPointCount(), "unexpected data point count")

	minWorkerMetric := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0)
	require.Equal(t, "garden.shoot.worker.min", minWorkerMetric.Name(), "unexpected metric name")
	require.Equal(t, 1, minWorkerMetric.Gauge().DataPoints().Len(), "unexpected data point count for min worker metric")
	require.Equal(t, int64(1), minWorkerMetric.Gauge().DataPoints().At(0).IntValue(), "unexpected min worker value")

	worker, ok := minWorkerMetric.Gauge().DataPoints().At(0).Attributes().Get("gardener.worker.name")
	require.True(t, ok, "missing worker attribute for min worker metric")
	require.Equal(t, "test-worker", worker.Str(), "unexpected worker attribute value for min worker metric")

	machineType, ok := minWorkerMetric.Gauge().DataPoints().At(0).Attributes().Get("gardener.worker.machine.type")
	require.True(t, ok, "missing worker attribute for machine type")
	require.Equal(t, "test-type", machineType.Str(), "unexpected machine type attribute")

	maxWorkerMetric := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(1)
	require.Equal(t, "garden.shoot.worker.max", maxWorkerMetric.Name(), "unexpected metric name")
	require.Equal(t, 1, maxWorkerMetric.Gauge().DataPoints().Len(), "unexpected data point count for max worker metric")
	require.Equal(t, int64(3), maxWorkerMetric.Gauge().DataPoints().At(0).IntValue(), "unexpected max worker value")

	worker, ok = maxWorkerMetric.Gauge().DataPoints().At(0).Attributes().Get("gardener.worker.name")
	require.True(t, ok, "missing worker attribute for max worker metric")
	require.Equal(t, "test-worker", worker.Str(), "unexpected worker attribute value for min worker metric")

	machineType, ok = maxWorkerMetric.Gauge().DataPoints().At(0).Attributes().Get("gardener.worker.machine.type")
	require.True(t, ok, "missing worker attribute for machine type")
	require.Equal(t, "test-type", machineType.Str(), "unexpected machine type attribute")

	shootNodeInfoMetric := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(2)
	require.Equal(t, "garden.shoot.node", shootNodeInfoMetric.Name(), "unexpected metric name")
	require.Equal(t, 1, shootNodeInfoMetric.Gauge().DataPoints().Len(), "unexpected data point count for shoot node info metric")
	require.Equal(t, int64(1), shootNodeInfoMetric.Gauge().DataPoints().At(0).IntValue(), "unexpected shoot node info metric value")

	minNodesMetric := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(3)
	require.Equal(t, "garden.shoot.nodes.min", minNodesMetric.Name(), "unexpected metric name")
	require.Equal(t, 1, minNodesMetric.Gauge().DataPoints().Len(), "unexpected data point count for min nodes metric")
	require.Equal(t, int64(1), minNodesMetric.Gauge().DataPoints().At(0).IntValue(), "unexpected min nodes value")

	maxNodesMetric := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(4)
	require.Equal(t, "garden.shoot.nodes.max", maxNodesMetric.Name(), "unexpected metric name")
	require.Equal(t, 1, maxNodesMetric.Gauge().DataPoints().Len(), "unexpected data point count for max nodes metric")
	require.Equal(t, int64(3), maxNodesMetric.Gauge().DataPoints().At(0).IntValue(), "unexpected max nodes value")
}

func TestCollectShootOperationsTotal(t *testing.T) {
	fakeClient := gardenerfake.NewSimpleClientset()
	shoots := []*corev1beta1.Shoot{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "shoot-1", Namespace: "garden-dev"},
			Spec: corev1beta1.ShootSpec{
				Provider:   corev1beta1.Provider{Type: "aws"},
				Region:     "eu-west-1",
				Kubernetes: corev1beta1.Kubernetes{Version: "1.26.0"},
				SeedName:   ptr.To("seed-1"),
			},
			Status: corev1beta1.ShootStatus{
				LastOperation: &corev1beta1.LastOperation{
					Type:  corev1beta1.LastOperationTypeReconcile,
					State: corev1beta1.LastOperationStateSucceeded,
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "shoot-2", Namespace: "garden-dev"},
			Spec: corev1beta1.ShootSpec{
				Provider:   corev1beta1.Provider{Type: "aws"},
				Region:     "eu-west-1",
				Kubernetes: corev1beta1.Kubernetes{Version: "1.26.0"},
				SeedName:   ptr.To("seed-1"),
			},
			Status: corev1beta1.ShootStatus{
				LastOperation: &corev1beta1.LastOperation{
					Type:  corev1beta1.LastOperationTypeReconcile,
					State: corev1beta1.LastOperationStateSucceeded,
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "shoot-3", Namespace: "garden-dev"},
			Spec: corev1beta1.ShootSpec{
				Provider:   corev1beta1.Provider{Type: "aws"},
				Region:     "eu-west-1",
				Kubernetes: corev1beta1.Kubernetes{Version: "1.26.0"},
				SeedName:   ptr.To("seed-1"),
			},
			Status: corev1beta1.ShootStatus{
				LastOperation: &corev1beta1.LastOperation{
					Type:  corev1beta1.LastOperationTypeDelete,
					State: corev1beta1.LastOperationStateProcessing,
				},
			},
		},
	}

	factory := gardenerinformers.NewSharedInformerFactory(fakeClient, 0)
	informer := factory.Core().V1beta1().Shoots().Informer()
	for _, s := range shoots {
		require.NoError(t, informer.GetStore().Add(s))
	}

	r := &gardenerReceiver{
		config:        &Config{Kubeconfig: "/tmp/fake", Resources: []string{"shoots"}},
		settings:      receivertest.NewNopSettings(component.MustNewType("gardener")),
		consumer:      new(consumertest.MetricsSink),
		shootInformer: informer,
		logger:        zap.NewNop(),
	}

	md := pmetric.NewMetrics()
	sm := r.initScopeMetrics(&md)
	r.collectShootOperationsTotal(&sm, nowTimestamp())

	require.Equal(t, 1, md.MetricCount())
	// 2 unique key combinations: (Reconcile/Succeeded/aws/seed-1/1.26.0/eu-west-1) and (Delete/Processing/...)
	require.Equal(t, 2, md.DataPointCount())
	m := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0)
	require.Equal(t, "garden.shoot.operations_total", m.Name())
}

func TestCollectShootCustomizationMetrics(t *testing.T) {
	fakeClient := gardenerfake.NewSimpleClientset()
	shoot := &corev1beta1.Shoot{
		ObjectMeta: metav1.ObjectMeta{Name: "test-shoot", Namespace: "garden-dev"},
		Spec: corev1beta1.ShootSpec{
			Provider: corev1beta1.Provider{
				Type: "aws",
				Workers: []corev1beta1.Worker{
					{Name: "pool-1", Zones: []string{"eu-west-1a", "eu-west-1b"}},
					{Name: "pool-2", Zones: []string{"eu-west-1a"}},
				},
			},
			Region:     "eu-west-1",
			Kubernetes: corev1beta1.Kubernetes{Version: "1.26.0"},
			Hibernation: &corev1beta1.Hibernation{
				Enabled:   ptr.To(true),
				Schedules: []corev1beta1.HibernationSchedule{{Start: ptr.To("00 18 * * 1,2,3,4,5")}},
			},
			Maintenance: &corev1beta1.Maintenance{
				TimeWindow: &corev1beta1.MaintenanceTimeWindow{Begin: "220000+0100", End: "230000+0100"},
				AutoUpdate: &corev1beta1.MaintenanceAutoUpdate{
					KubernetesVersion:   true,
					MachineImageVersion: ptr.To(true),
				},
			},
			Addons: &corev1beta1.Addons{
				NginxIngress:        &corev1beta1.NginxIngress{Addon: corev1beta1.Addon{Enabled: true}},
				KubernetesDashboard: &corev1beta1.KubernetesDashboard{Addon: corev1beta1.Addon{Enabled: true}},
			},
		},
	}

	factory := gardenerinformers.NewSharedInformerFactory(fakeClient, 0)
	informer := factory.Core().V1beta1().Shoots().Informer()
	require.NoError(t, informer.GetStore().Add(shoot))

	r := &gardenerReceiver{
		config:        &Config{Kubeconfig: "/tmp/fake", Resources: []string{"shoots"}},
		settings:      receivertest.NewNopSettings(component.MustNewType("gardener")),
		consumer:      new(consumertest.MetricsSink),
		shootInformer: informer,
		logger:        zap.NewNop(),
	}

	md := pmetric.NewMetrics()
	sm := r.initScopeMetrics(&md)
	r.collectShootCustomizationMetrics(&sm, nowTimestamp())

	require.Equal(t, 18, md.MetricCount(), "expected 18 customization metrics")

	names := map[string]int64{}
	for i := 0; i < md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().Len(); i++ {
		m := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(i)
		names[m.Name()] = m.Gauge().DataPoints().At(0).IntValue()
	}

	require.Equal(t, int64(1), names["garden.shoots.hibernation.enabled_total"])
	require.Equal(t, int64(1), names["garden.shoots.hibernation.schedule_total"])
	require.Equal(t, int64(1), names["garden.shoots.maintenance.window_total"])
	require.Equal(t, int64(1), names["garden.shoots.maintenance.autoupdate.k8s_version_total"])
	require.Equal(t, int64(1), names["garden.shoots.maintenance.autoupdate.image_version_total"])
	require.Equal(t, int64(1), names["garden.shoots.custom.addon.nginx_ingress_total"])
	require.Equal(t, int64(1), names["garden.shoots.custom.addon.kube_dashboard_total"])
	require.Equal(t, int64(1), names["garden.shoots.custom.worker.multiple_pools_total"])
	require.Equal(t, int64(1), names["garden.shoots.custom.worker.multi_zones_total"])
}

// ---------------------------------------------------------------------------
// buildShootLookups
// ---------------------------------------------------------------------------

func newShootLookupReceiver(t *testing.T) *gardenerReceiver {
	t.Helper()
	gardenClient := gardenerfake.NewSimpleClientset()
	gardenFactory := gardenerinformers.NewSharedInformerFactory(gardenClient, 0)

	seedMgmtClient := seedmanagementfake.NewSimpleClientset()
	seedMgmtFactory := seedmanagementinformers.NewSharedInformerFactory(seedMgmtClient, 0)

	securityClient := securityfake.NewSimpleClientset()
	securityFactory := securityinformers.NewSharedInformerFactory(securityClient, 0)

	r := &gardenerReceiver{
		config:                     &Config{Kubeconfig: "/tmp/fake", Resources: []string{"shoots"}},
		settings:                   receivertest.NewNopSettings(component.MustNewType("gardener")),
		consumer:                   new(consumertest.MetricsSink),
		logger:                     zap.NewNop(),
		shootInformer:              gardenFactory.Core().V1beta1().Shoots().Informer(),
		seedInformer:               gardenFactory.Core().V1beta1().Seeds().Informer(),
		projectInformer:            gardenFactory.Core().V1beta1().Projects().Informer(),
		managedSeedInformer:        seedMgmtFactory.Seedmanagement().V1alpha1().ManagedSeeds().Informer(),
		secretBindingInformer:      gardenFactory.Core().V1beta1().SecretBindings().Informer(),
		credentialsBindingInformer: securityFactory.Security().V1alpha1().CredentialsBindings().Informer(),
	}
	return r
}

func TestBuildShootLookups_Empty(t *testing.T) {
	r := newShootLookupReceiver(t)
	l := r.buildShootLookups()
	assert.Empty(t, l.managedSeedShoots)
	assert.Empty(t, l.seedByName)
	assert.Empty(t, l.projectByNamespace)
	assert.Empty(t, l.secretBindingRefNS)
	assert.Empty(t, l.credentialsBindingRefNS)
}

func TestBuildShootLookups_ManagedSeed(t *testing.T) {
	r := newShootLookupReceiver(t)

	ms := &seedmanagementv1alpha1.ManagedSeed{
		ObjectMeta: metav1.ObjectMeta{Name: "seed-ms"},
		Spec:       seedmanagementv1alpha1.ManagedSeedSpec{Shoot: &seedmanagementv1alpha1.Shoot{Name: "seed-shoot"}},
	}
	require.NoError(t, r.managedSeedInformer.GetStore().Add(ms))

	l := r.buildShootLookups()
	_, ok := l.managedSeedShoots["seed-shoot"]
	assert.True(t, ok, "seed-shoot should be in managedSeedShoots")
	assert.NotContains(t, l.managedSeedShoots, "other-shoot")
}

func TestBuildShootLookups_ManagedSeedNilShoot(t *testing.T) {
	r := newShootLookupReceiver(t)

	// ManagedSeed with nil Shoot.Spec.Shoot — must not panic or add an empty key.
	ms := &seedmanagementv1alpha1.ManagedSeed{
		ObjectMeta: metav1.ObjectMeta{Name: "seed-no-shoot"},
		Spec:       seedmanagementv1alpha1.ManagedSeedSpec{Shoot: nil},
	}
	require.NoError(t, r.managedSeedInformer.GetStore().Add(ms))

	l := r.buildShootLookups()
	assert.Empty(t, l.managedSeedShoots, "nil Shoot reference must not add an entry")
}

func TestBuildShootLookups_SeedByName(t *testing.T) {
	r := newShootLookupReceiver(t)

	seed := &corev1beta1.Seed{
		ObjectMeta: metav1.ObjectMeta{Name: "my-seed"},
		Spec:       corev1beta1.SeedSpec{Provider: corev1beta1.SeedProvider{Type: "gcp", Region: "europe-west1"}},
	}
	require.NoError(t, r.seedInformer.GetStore().Add(seed))

	l := r.buildShootLookups()
	s, ok := l.seedByName["my-seed"]
	require.True(t, ok)
	assert.Equal(t, "gcp", s.Spec.Provider.Type)
	assert.Equal(t, "europe-west1", s.Spec.Provider.Region)
}

func TestBuildShootLookups_ProjectByNamespace(t *testing.T) {
	r := newShootLookupReceiver(t)

	proj := &corev1beta1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name: "my-project",
			Annotations: map[string]string{
				"billing.gardener.cloud/costObject":     "CC-123",
				"billing.gardener.cloud/costObjectType": "CostCenter",
			},
		},
		Spec: corev1beta1.ProjectSpec{
			Namespace: ptr.To("garden-my-project"),
			Owner:     &rbacv1.Subject{Name: "alice"},
		},
	}
	require.NoError(t, r.projectInformer.GetStore().Add(proj))

	l := r.buildShootLookups()
	pi, ok := l.projectByNamespace["garden-my-project"]
	require.True(t, ok)
	assert.Equal(t, "CC-123", pi.costObject)
	assert.Equal(t, "CostCenter", pi.costObjectType)
	assert.Equal(t, "alice", pi.costObjectOwner)
}

func TestBuildShootLookups_ProjectNilNamespace(t *testing.T) {
	r := newShootLookupReceiver(t)

	// Project with no Namespace — must be skipped.
	proj := &corev1beta1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "headless"},
		Spec:       corev1beta1.ProjectSpec{Namespace: nil},
	}
	require.NoError(t, r.projectInformer.GetStore().Add(proj))

	l := r.buildShootLookups()
	assert.Empty(t, l.projectByNamespace, "project without Namespace must not add an entry")
}

func TestResolveBillingInfo_ViaCredentialsBinding(t *testing.T) {
	r := newShootLookupReceiver(t)

	// Project A owns the shoot
	projA := &corev1beta1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "project-a",
			Annotations: map[string]string{"billing.gardener.cloud/costObject": "CO-A"},
		},
		Spec: corev1beta1.ProjectSpec{
			Namespace: ptr.To("garden-a"),
			Owner:     &rbacv1.Subject{Name: "owner-a"},
		},
	}
	// Project B owns the credentials
	projB := &corev1beta1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name: "project-b",
			Annotations: map[string]string{
				"billing.gardener.cloud/costObject":     "CO-B",
				"billing.gardener.cloud/costObjectType": "CostCenter",
			},
		},
		Spec: corev1beta1.ProjectSpec{
			Namespace: ptr.To("garden-b"),
			Owner:     &rbacv1.Subject{Name: "owner-b"},
		},
	}
	require.NoError(t, r.projectInformer.GetStore().Add(projA))
	require.NoError(t, r.projectInformer.GetStore().Add(projB))

	// CredentialsBinding in garden-a references credentials in garden-b
	cb := &securityv1alpha1.CredentialsBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "my-creds", Namespace: "garden-a"},
		CredentialsRef: corev1.ObjectReference{
			Namespace: "garden-b",
			Name:      "my-secret",
		},
	}
	require.NoError(t, r.credentialsBindingInformer.GetStore().Add(cb))

	l := r.buildShootLookups()

	shoot := &corev1beta1.Shoot{
		ObjectMeta: metav1.ObjectMeta{Name: "my-shoot", Namespace: "garden-a"},
		Spec:       corev1beta1.ShootSpec{CredentialsBindingName: ptr.To("my-creds")},
	}

	pi := l.resolveBillingInfo(shoot)
	assert.Equal(t, "CO-B", pi.costObject, "billing should come from credentials-owning project B")
	assert.Equal(t, "CostCenter", pi.costObjectType)
	assert.Equal(t, "owner-b", pi.costObjectOwner)
}

func TestResolveBillingInfo_ViaSecretBinding(t *testing.T) {
	r := newShootLookupReceiver(t)

	projA := &corev1beta1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "project-a",
			Annotations: map[string]string{"billing.gardener.cloud/costObject": "CO-A"},
		},
		Spec: corev1beta1.ProjectSpec{
			Namespace: ptr.To("garden-a"),
			Owner:     &rbacv1.Subject{Name: "owner-a"},
		},
	}
	projC := &corev1beta1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name: "project-c",
			Annotations: map[string]string{
				"billing.gardener.cloud/costObject":     "CO-C",
				"billing.gardener.cloud/costObjectType": "WBS",
			},
		},
		Spec: corev1beta1.ProjectSpec{
			Namespace: ptr.To("garden-c"),
			Owner:     &rbacv1.Subject{Name: "owner-c"},
		},
	}
	require.NoError(t, r.projectInformer.GetStore().Add(projA))
	require.NoError(t, r.projectInformer.GetStore().Add(projC))

	// SecretBinding in garden-a references a secret in garden-c
	sb := &corev1beta1.SecretBinding{ //nolint:staticcheck // SA1019
		ObjectMeta: metav1.ObjectMeta{Name: "legacy-binding", Namespace: "garden-a"},
		SecretRef:  corev1.SecretReference{Namespace: "garden-c", Name: "infra-secret"},
	}
	require.NoError(t, r.secretBindingInformer.GetStore().Add(sb))

	l := r.buildShootLookups()

	shoot := &corev1beta1.Shoot{
		ObjectMeta: metav1.ObjectMeta{Name: "legacy-shoot", Namespace: "garden-a"},
		Spec:       corev1beta1.ShootSpec{SecretBindingName: ptr.To("legacy-binding")}, //nolint:staticcheck // SA1019
	}

	pi := l.resolveBillingInfo(shoot)
	assert.Equal(t, "CO-C", pi.costObject, "billing should come from secret-owning project C")
	assert.Equal(t, "WBS", pi.costObjectType)
	assert.Equal(t, "owner-c", pi.costObjectOwner)
}

func TestResolveBillingInfo_FallbackToShootNamespace(t *testing.T) {
	r := newShootLookupReceiver(t)

	proj := &corev1beta1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name: "project-own",
			Annotations: map[string]string{
				"billing.gardener.cloud/costObject":     "CO-OWN",
				"billing.gardener.cloud/costObjectType": "IO",
			},
		},
		Spec: corev1beta1.ProjectSpec{
			Namespace: ptr.To("garden-own"),
			Owner:     &rbacv1.Subject{Name: "owner-own"},
		},
	}
	require.NoError(t, r.projectInformer.GetStore().Add(proj))

	l := r.buildShootLookups()

	// Shoot with no binding references at all
	shoot := &corev1beta1.Shoot{
		ObjectMeta: metav1.ObjectMeta{Name: "workerless-shoot", Namespace: "garden-own"},
		Spec:       corev1beta1.ShootSpec{},
	}

	pi := l.resolveBillingInfo(shoot)
	assert.Equal(t, "CO-OWN", pi.costObject, "billing should fall back to shoot's own project")
	assert.Equal(t, "IO", pi.costObjectType)
	assert.Equal(t, "owner-own", pi.costObjectOwner)
}

func TestResolveBillingInfo_BindingNotFound(t *testing.T) {
	r := newShootLookupReceiver(t)

	proj := &corev1beta1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "project-x",
			Annotations: map[string]string{"billing.gardener.cloud/costObject": "CO-X"},
		},
		Spec: corev1beta1.ProjectSpec{
			Namespace: ptr.To("garden-x"),
			Owner:     &rbacv1.Subject{Name: "owner-x"},
		},
	}
	require.NoError(t, r.projectInformer.GetStore().Add(proj))

	l := r.buildShootLookups()

	// Shoot references a CredentialsBinding that doesn't exist in the informer
	shoot := &corev1beta1.Shoot{
		ObjectMeta: metav1.ObjectMeta{Name: "orphan-shoot", Namespace: "garden-x"},
		Spec:       corev1beta1.ShootSpec{CredentialsBindingName: ptr.To("missing-binding")},
	}

	pi := l.resolveBillingInfo(shoot)
	assert.Equal(t, "CO-X", pi.costObject, "should fall back to shoot namespace when binding not found")
}

func TestResolveBillingInfo_SameNamespaceCredentialsBinding(t *testing.T) {
	r := newShootLookupReceiver(t)

	proj := &corev1beta1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "project-same",
			Annotations: map[string]string{"billing.gardener.cloud/costObject": "CO-SAME"},
		},
		Spec: corev1beta1.ProjectSpec{
			Namespace: ptr.To("garden-same"),
			Owner:     &rbacv1.Subject{Name: "owner-same"},
		},
	}
	require.NoError(t, r.projectInformer.GetStore().Add(proj))

	cb := &securityv1alpha1.CredentialsBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "local-creds", Namespace: "garden-same"},
		CredentialsRef: corev1.ObjectReference{
			Namespace: "garden-same",
			Name:      "local-secret",
		},
	}
	require.NoError(t, r.credentialsBindingInformer.GetStore().Add(cb))

	l := r.buildShootLookups()

	shoot := &corev1beta1.Shoot{
		ObjectMeta: metav1.ObjectMeta{Name: "same-ns-shoot", Namespace: "garden-same"},
		Spec:       corev1beta1.ShootSpec{CredentialsBindingName: ptr.To("local-creds")},
	}

	pi := l.resolveBillingInfo(shoot)
	assert.Equal(t, "CO-SAME", pi.costObject, "billing should resolve to same project when binding references the same namespace")
	assert.Equal(t, "owner-same", pi.costObjectOwner)
}

func TestResolveBillingInfo_SameNamespaceSecretBinding(t *testing.T) {
	r := newShootLookupReceiver(t)

	proj := &corev1beta1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name: "project-local",
			Annotations: map[string]string{
				"billing.gardener.cloud/costObject":     "CO-LOCAL",
				"billing.gardener.cloud/costObjectType": "WBS",
			},
		},
		Spec: corev1beta1.ProjectSpec{
			Namespace: ptr.To("garden-local"),
			Owner:     &rbacv1.Subject{Name: "owner-local"},
		},
	}
	require.NoError(t, r.projectInformer.GetStore().Add(proj))

	sb := &corev1beta1.SecretBinding{ //nolint:staticcheck // SA1019
		ObjectMeta: metav1.ObjectMeta{Name: "local-sb", Namespace: "garden-local"},
		SecretRef:  corev1.SecretReference{Namespace: "garden-local", Name: "local-infra-secret"},
	}
	require.NoError(t, r.secretBindingInformer.GetStore().Add(sb))

	l := r.buildShootLookups()

	shoot := &corev1beta1.Shoot{
		ObjectMeta: metav1.ObjectMeta{Name: "local-shoot", Namespace: "garden-local"},
		Spec:       corev1beta1.ShootSpec{SecretBindingName: ptr.To("local-sb")}, //nolint:staticcheck // SA1019
	}

	pi := l.resolveBillingInfo(shoot)
	assert.Equal(t, "CO-LOCAL", pi.costObject, "billing should resolve to same project when secret binding references the same namespace")
	assert.Equal(t, "WBS", pi.costObjectType)
	assert.Equal(t, "owner-local", pi.costObjectOwner)
}

func TestResolveBillingInfo_CostObjectFromBindingProjectOverridesShootProject(t *testing.T) {
	r := newShootLookupReceiver(t)

	// Shoot's own project has a costObject
	projShoot := &corev1beta1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name: "project-shoot",
			Annotations: map[string]string{
				"billing.gardener.cloud/costObject":     "CO-SHOOT",
				"billing.gardener.cloud/costObjectType": "IO",
			},
		},
		Spec: corev1beta1.ProjectSpec{
			Namespace: ptr.To("garden-shoot"),
			Owner:     &rbacv1.Subject{Name: "owner-shoot"},
		},
	}
	// Binding's project also has a costObject — this one should win
	projCreds := &corev1beta1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name: "project-creds",
			Annotations: map[string]string{
				"billing.gardener.cloud/costObject":     "CO-CREDS",
				"billing.gardener.cloud/costObjectType": "CostCenter",
			},
		},
		Spec: corev1beta1.ProjectSpec{
			Namespace: ptr.To("garden-creds"),
			Owner:     &rbacv1.Subject{Name: "owner-creds"},
		},
	}
	require.NoError(t, r.projectInformer.GetStore().Add(projShoot))
	require.NoError(t, r.projectInformer.GetStore().Add(projCreds))

	cb := &securityv1alpha1.CredentialsBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "cross-creds", Namespace: "garden-shoot"},
		CredentialsRef: corev1.ObjectReference{
			Namespace: "garden-creds",
			Name:      "infra-secret",
		},
	}
	require.NoError(t, r.credentialsBindingInformer.GetStore().Add(cb))

	l := r.buildShootLookups()

	shoot := &corev1beta1.Shoot{
		ObjectMeta: metav1.ObjectMeta{Name: "billed-shoot", Namespace: "garden-shoot"},
		Spec:       corev1beta1.ShootSpec{CredentialsBindingName: ptr.To("cross-creds")},
	}

	pi := l.resolveBillingInfo(shoot)
	assert.Equal(t, "CO-CREDS", pi.costObject, "billing should come from the credentials-owning project, not the shoot's project")
	assert.Equal(t, "CostCenter", pi.costObjectType)
	assert.Equal(t, "owner-creds", pi.costObjectOwner)
}

func TestResolveBillingInfo_CostObjectFromSecretBindingProjectOverridesShootProject(t *testing.T) {
	r := newShootLookupReceiver(t)

	projShoot := &corev1beta1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name: "project-s",
			Annotations: map[string]string{
				"billing.gardener.cloud/costObject":     "CO-S",
				"billing.gardener.cloud/costObjectType": "WBS",
			},
		},
		Spec: corev1beta1.ProjectSpec{
			Namespace: ptr.To("garden-s"),
			Owner:     &rbacv1.Subject{Name: "owner-s"},
		},
	}
	projSecret := &corev1beta1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name: "project-sec",
			Annotations: map[string]string{
				"billing.gardener.cloud/costObject":     "CO-SEC",
				"billing.gardener.cloud/costObjectType": "IO",
			},
		},
		Spec: corev1beta1.ProjectSpec{
			Namespace: ptr.To("garden-sec"),
			Owner:     &rbacv1.Subject{Name: "owner-sec"},
		},
	}
	require.NoError(t, r.projectInformer.GetStore().Add(projShoot))
	require.NoError(t, r.projectInformer.GetStore().Add(projSecret))

	sb := &corev1beta1.SecretBinding{ //nolint:staticcheck // SA1019
		ObjectMeta: metav1.ObjectMeta{Name: "cross-sb", Namespace: "garden-s"},
		SecretRef:  corev1.SecretReference{Namespace: "garden-sec", Name: "infra-secret"},
	}
	require.NoError(t, r.secretBindingInformer.GetStore().Add(sb))

	l := r.buildShootLookups()

	shoot := &corev1beta1.Shoot{
		ObjectMeta: metav1.ObjectMeta{Name: "legacy-billed", Namespace: "garden-s"},
		Spec:       corev1beta1.ShootSpec{SecretBindingName: ptr.To("cross-sb")}, //nolint:staticcheck // SA1019
	}

	pi := l.resolveBillingInfo(shoot)
	assert.Equal(t, "CO-SEC", pi.costObject, "billing should come from the secret-owning project, not the shoot's project")
	assert.Equal(t, "IO", pi.costObjectType)
	assert.Equal(t, "owner-sec", pi.costObjectOwner)
}

// ---------------------------------------------------------------------------
// collectShootCustomizationMetrics — table-driven
// ---------------------------------------------------------------------------

func collectCustomizationMetrics(t *testing.T, shoots ...*corev1beta1.Shoot) map[string]int64 {
	t.Helper()
	fakeClient := gardenerfake.NewSimpleClientset()
	factory := gardenerinformers.NewSharedInformerFactory(fakeClient, 0)
	informer := factory.Core().V1beta1().Shoots().Informer()
	for _, s := range shoots {
		require.NoError(t, informer.GetStore().Add(s))
	}
	r := &gardenerReceiver{
		config:        &Config{Kubeconfig: "/tmp/fake", Resources: []string{"shoots"}},
		settings:      receivertest.NewNopSettings(component.MustNewType("gardener")),
		consumer:      new(consumertest.MetricsSink),
		shootInformer: informer,
		logger:        zap.NewNop(),
	}
	md := pmetric.NewMetrics()
	sm := r.initScopeMetrics(&md)
	r.collectShootCustomizationMetrics(&sm, nowTimestamp())

	result := map[string]int64{}
	if md.ResourceMetrics().Len() == 0 {
		return result
	}
	metrics := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	for i := 0; i < metrics.Len(); i++ {
		m := metrics.At(i)
		// For labeled metrics collect the first data point value per name.
		if m.Gauge().DataPoints().Len() > 0 {
			result[m.Name()] = m.Gauge().DataPoints().At(0).IntValue()
		}
	}
	return result
}

func TestCollectShootCustomizationMetrics_Empty(t *testing.T) {
	names := collectCustomizationMetrics(t) // no shoots
	assert.Empty(t, names, "no metrics expected for empty shoot list")
}

func TestCollectShootCustomizationMetrics_NoOptionals(t *testing.T) {
	// Minimal shoot with none of the optional customization fields set.
	shoot := &corev1beta1.Shoot{
		ObjectMeta: metav1.ObjectMeta{Name: "bare", Namespace: "garden-dev"},
		Spec: corev1beta1.ShootSpec{
			Provider:   corev1beta1.Provider{Type: "aws"},
			Region:     "eu-west-1",
			Kubernetes: corev1beta1.Kubernetes{Version: "1.28.0"},
		},
	}
	names := collectCustomizationMetrics(t, shoot)
	assert.Equal(t, int64(0), names["garden.shoots.hibernation.enabled_total"])
	assert.Equal(t, int64(0), names["garden.shoots.custom.addon.nginx_ingress_total"])
	assert.Equal(t, int64(0), names["garden.shoots.custom.worker.multiple_pools_total"])
	assert.Equal(t, int64(18), int64(len(names)), "expected exactly 18 scalar metrics")
}

func TestCollectShootCustomizationMetrics_FeatureGates(t *testing.T) {
	shoot := &corev1beta1.Shoot{
		ObjectMeta: metav1.ObjectMeta{Name: "fg-shoot", Namespace: "garden-dev"},
		Spec: corev1beta1.ShootSpec{
			Provider: corev1beta1.Provider{Type: "aws"},
			Region:   "us-east-1",
			Kubernetes: corev1beta1.Kubernetes{
				Version: "1.28.0",
				KubeAPIServer: &corev1beta1.KubeAPIServerConfig{
					KubernetesConfig: corev1beta1.KubernetesConfig{
						FeatureGates: map[string]bool{
							"FeatureA": true,
							"FeatureB": false, // disabled — must not be counted
						},
					},
				},
				KubeControllerManager: &corev1beta1.KubeControllerManagerConfig{
					KubernetesConfig: corev1beta1.KubernetesConfig{
						FeatureGates: map[string]bool{"FeatureC": true},
					},
				},
				KubeScheduler: &corev1beta1.KubeSchedulerConfig{
					KubernetesConfig: corev1beta1.KubernetesConfig{
						FeatureGates: map[string]bool{"FeatureD": true},
					},
				},
			},
		},
	}

	fakeClient := gardenerfake.NewSimpleClientset()
	factory := gardenerinformers.NewSharedInformerFactory(fakeClient, 0)
	informer := factory.Core().V1beta1().Shoots().Informer()
	require.NoError(t, informer.GetStore().Add(shoot))
	r := &gardenerReceiver{
		config:        &Config{Kubeconfig: "/tmp/fake", Resources: []string{"shoots"}},
		settings:      receivertest.NewNopSettings(component.MustNewType("gardener")),
		consumer:      new(consumertest.MetricsSink),
		shootInformer: informer,
		logger:        zap.NewNop(),
	}
	md := pmetric.NewMetrics()
	sm := r.initScopeMetrics(&md)
	r.collectShootCustomizationMetrics(&sm, nowTimestamp())

	metricsByName := map[string]pmetric.Metric{}
	metrics := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	for i := 0; i < metrics.Len(); i++ {
		metricsByName[metrics.At(i).Name()] = metrics.At(i)
	}

	// Only FeatureA is enabled → 1 data point for apiserver feature gates.
	apiFG, ok := metricsByName["garden.shoots.custom.apiserver.feature_gates_total"]
	require.True(t, ok, "apiserver feature gates metric must exist")
	assert.Equal(t, 1, apiFG.Gauge().DataPoints().Len(), "only enabled gates should be counted")
	fg, _ := apiFG.Gauge().DataPoints().At(0).Attributes().Get("gardener.feature_gate")
	assert.Equal(t, "FeatureA", fg.Str())

	_, hasKCM := metricsByName["garden.shoots.custom.kcm.feature_gates_total"]
	assert.True(t, hasKCM)
	_, hasSched := metricsByName["garden.shoots.custom.scheduler.feature_gates_total"]
	assert.True(t, hasSched)
}

func TestCollectShootCustomizationMetrics_AdmissionPlugins(t *testing.T) {
	shoot := &corev1beta1.Shoot{
		ObjectMeta: metav1.ObjectMeta{Name: "ap-shoot", Namespace: "garden-dev"},
		Spec: corev1beta1.ShootSpec{
			Provider: corev1beta1.Provider{Type: "aws"},
			Region:   "us-east-1",
			Kubernetes: corev1beta1.Kubernetes{
				Version: "1.28.0",
				KubeAPIServer: &corev1beta1.KubeAPIServerConfig{
					AdmissionPlugins: []corev1beta1.AdmissionPlugin{
						{Name: "PodSecurity"},
						{Name: "NodeRestriction", Disabled: ptr.To(true)}, // disabled — must not count
					},
				},
			},
		},
	}

	fakeClient := gardenerfake.NewSimpleClientset()
	factory := gardenerinformers.NewSharedInformerFactory(fakeClient, 0)
	informer := factory.Core().V1beta1().Shoots().Informer()
	require.NoError(t, informer.GetStore().Add(shoot))
	r := &gardenerReceiver{
		config:        &Config{Kubeconfig: "/tmp/fake", Resources: []string{"shoots"}},
		settings:      receivertest.NewNopSettings(component.MustNewType("gardener")),
		consumer:      new(consumertest.MetricsSink),
		shootInformer: informer,
		logger:        zap.NewNop(),
	}
	md := pmetric.NewMetrics()
	sm := r.initScopeMetrics(&md)
	r.collectShootCustomizationMetrics(&sm, nowTimestamp())

	metrics := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	for i := 0; i < metrics.Len(); i++ {
		m := metrics.At(i)
		if m.Name() == "garden.shoots.custom.apiserver.admission_plugins_total" {
			assert.Equal(t, 1, m.Gauge().DataPoints().Len(), "only enabled admission plugins should be counted")
			ap, _ := m.Gauge().DataPoints().At(0).Attributes().Get("gardener.admission_plugin")
			assert.Equal(t, "PodSecurity", ap.Str())
			return
		}
	}
	t.Fatal("admission plugins metric not found")
}

func TestCollectShootCustomizationMetrics_ProxyModes(t *testing.T) {
	mode := corev1beta1.ProxyModeIPTables
	shoot := &corev1beta1.Shoot{
		ObjectMeta: metav1.ObjectMeta{Name: "proxy-shoot", Namespace: "garden-dev"},
		Spec: corev1beta1.ShootSpec{
			Provider: corev1beta1.Provider{Type: "aws"},
			Region:   "us-east-1",
			Kubernetes: corev1beta1.Kubernetes{
				Version:   "1.28.0",
				KubeProxy: &corev1beta1.KubeProxyConfig{Mode: &mode},
			},
		},
	}
	names := collectCustomizationMetrics(t, shoot)
	// Proxy mode metric only present when modes exist, so it won't appear in the
	// scalar map — verify via the full metrics scan below.
	_ = names // scalar check already done; labeled metric is tested separately

	fakeClient := gardenerfake.NewSimpleClientset()
	factory := gardenerinformers.NewSharedInformerFactory(fakeClient, 0)
	informer := factory.Core().V1beta1().Shoots().Informer()
	require.NoError(t, informer.GetStore().Add(shoot))
	r := &gardenerReceiver{
		config:        &Config{Kubeconfig: "/tmp/fake", Resources: []string{"shoots"}},
		settings:      receivertest.NewNopSettings(component.MustNewType("gardener")),
		consumer:      new(consumertest.MetricsSink),
		shootInformer: informer,
		logger:        zap.NewNop(),
	}
	md := pmetric.NewMetrics()
	sm := r.initScopeMetrics(&md)
	r.collectShootCustomizationMetrics(&sm, nowTimestamp())

	metrics := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	for i := 0; i < metrics.Len(); i++ {
		m := metrics.At(i)
		if m.Name() == "garden.shoots.custom.proxy.mode_total" {
			require.Equal(t, 1, m.Gauge().DataPoints().Len())
			modeVal, _ := m.Gauge().DataPoints().At(0).Attributes().Get("gardener.proxy.mode")
			assert.Equal(t, "IPTables", modeVal.Str())
			return
		}
	}
	t.Fatal("proxy mode metric not found")
}

func TestCollectShootCustomizationMetrics_Extensions(t *testing.T) {
	// Two shoots each with the same extension — count should be 2.
	mkShoot := func(name string) *corev1beta1.Shoot {
		return &corev1beta1.Shoot{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "garden-dev"},
			Spec: corev1beta1.ShootSpec{
				Provider:   corev1beta1.Provider{Type: "aws"},
				Region:     "eu-west-1",
				Kubernetes: corev1beta1.Kubernetes{Version: "1.28.0"},
				Extensions: []corev1beta1.Extension{{Type: "shoot-dns-service"}},
			},
		}
	}

	fakeClient := gardenerfake.NewSimpleClientset()
	factory := gardenerinformers.NewSharedInformerFactory(fakeClient, 0)
	informer := factory.Core().V1beta1().Shoots().Informer()
	require.NoError(t, informer.GetStore().Add(mkShoot("s1")))
	require.NoError(t, informer.GetStore().Add(mkShoot("s2")))
	r := &gardenerReceiver{
		config:        &Config{Kubeconfig: "/tmp/fake", Resources: []string{"shoots"}},
		settings:      receivertest.NewNopSettings(component.MustNewType("gardener")),
		consumer:      new(consumertest.MetricsSink),
		shootInformer: informer,
		logger:        zap.NewNop(),
	}
	md := pmetric.NewMetrics()
	sm := r.initScopeMetrics(&md)
	r.collectShootCustomizationMetrics(&sm, nowTimestamp())

	metrics := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	for i := 0; i < metrics.Len(); i++ {
		m := metrics.At(i)
		if m.Name() == "garden.shoots.custom.extensions_total" {
			require.Equal(t, 1, m.Gauge().DataPoints().Len())
			extType, _ := m.Gauge().DataPoints().At(0).Attributes().Get("gardener.extension.type")
			assert.Equal(t, "shoot-dns-service", extType.Str())
			assert.Equal(t, int64(2), m.Gauge().DataPoints().At(0).IntValue())
			return
		}
	}
	t.Fatal("extensions metric not found")
}

// ---------------------------------------------------------------------------
// Optional-field edge cases
// ---------------------------------------------------------------------------

func TestCollectShootInfoMetrics_NilSeedName(t *testing.T) {
	shoot := &corev1beta1.Shoot{
		ObjectMeta: metav1.ObjectMeta{Name: "no-seed", Namespace: "garden-dev"},
		Spec: corev1beta1.ShootSpec{
			Provider:   corev1beta1.Provider{Type: "aws"},
			Region:     "eu-west-1",
			Kubernetes: corev1beta1.Kubernetes{Version: "1.28.0"},
			SeedName:   nil, // not yet scheduled
		},
		Status: corev1beta1.ShootStatus{Gardener: corev1beta1.Gardener{Version: "1.90.0"}},
	}
	fakeClient := gardenerfake.NewSimpleClientset()
	factory := gardenerinformers.NewSharedInformerFactory(fakeClient, 0)
	informer := factory.Core().V1beta1().Shoots().Informer()
	require.NoError(t, informer.GetStore().Add(shoot))

	r := &gardenerReceiver{
		config:        &Config{Kubeconfig: "/tmp/fake", Resources: []string{"shoots"}},
		settings:      receivertest.NewNopSettings(component.MustNewType("gardener")),
		consumer:      new(consumertest.MetricsSink),
		shootInformer: informer,
		logger:        zap.NewNop(),
	}
	md := pmetric.NewMetrics()
	sm := r.initScopeMetrics(&md)
	l := shootLookups{
		managedSeedShoots:  map[string]struct{}{},
		seedByName:         map[string]*corev1beta1.Seed{},
		projectByNamespace: map[string]projectBillingInfo{},
	}
	r.collectShootInfoMetrics(&sm, nowTimestamp(), l)

	require.Equal(t, 1, md.DataPointCount())
	dp := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Gauge().DataPoints().At(0)
	seedName, _ := dp.Attributes().Get("gardener.seed.name")
	assert.Empty(t, seedName.Str(), "seed name should be empty string when SeedName is nil")
	seedIaaS, _ := dp.Attributes().Get("gardener.seed.iaas")
	assert.Empty(t, seedIaaS.Str())
}

func TestCollectShootInfoMetrics_NilPurpose(t *testing.T) {
	shoot := &corev1beta1.Shoot{
		ObjectMeta: metav1.ObjectMeta{Name: "no-purpose", Namespace: "garden-dev"},
		Spec: corev1beta1.ShootSpec{
			Provider:   corev1beta1.Provider{Type: "aws"},
			Region:     "eu-west-1",
			Kubernetes: corev1beta1.Kubernetes{Version: "1.28.0"},
			Purpose:    nil,
		},
		Status: corev1beta1.ShootStatus{Gardener: corev1beta1.Gardener{Version: "1.90.0"}},
	}
	fakeClient := gardenerfake.NewSimpleClientset()
	factory := gardenerinformers.NewSharedInformerFactory(fakeClient, 0)
	informer := factory.Core().V1beta1().Shoots().Informer()
	require.NoError(t, informer.GetStore().Add(shoot))

	r := &gardenerReceiver{
		config:        &Config{Kubeconfig: "/tmp/fake", Resources: []string{"shoots"}},
		settings:      receivertest.NewNopSettings(component.MustNewType("gardener")),
		consumer:      new(consumertest.MetricsSink),
		shootInformer: informer,
		logger:        zap.NewNop(),
	}
	md := pmetric.NewMetrics()
	sm := r.initScopeMetrics(&md)
	l := shootLookups{
		managedSeedShoots:  map[string]struct{}{},
		seedByName:         map[string]*corev1beta1.Seed{},
		projectByNamespace: map[string]projectBillingInfo{},
	}
	r.collectShootInfoMetrics(&sm, nowTimestamp(), l)

	require.Equal(t, 1, md.DataPointCount())
	dp := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Gauge().DataPoints().At(0)
	purpose, _ := dp.Attributes().Get("gardener.shoot.purpose")
	assert.Empty(t, purpose.Str(), "purpose should be empty string when nil")
}

func TestCollectShootInfoMetrics_IsSeed(t *testing.T) {
	shoot := &corev1beta1.Shoot{
		ObjectMeta: metav1.ObjectMeta{Name: "seed-shoot", Namespace: "garden-dev"},
		Spec: corev1beta1.ShootSpec{
			Provider:   corev1beta1.Provider{Type: "aws"},
			Region:     "eu-west-1",
			Kubernetes: corev1beta1.Kubernetes{Version: "1.28.0"},
		},
		Status: corev1beta1.ShootStatus{Gardener: corev1beta1.Gardener{Version: "1.90.0"}},
	}
	fakeClient := gardenerfake.NewSimpleClientset()
	factory := gardenerinformers.NewSharedInformerFactory(fakeClient, 0)
	informer := factory.Core().V1beta1().Shoots().Informer()
	require.NoError(t, informer.GetStore().Add(shoot))

	r := &gardenerReceiver{
		config:        &Config{Kubeconfig: "/tmp/fake", Resources: []string{"shoots"}},
		settings:      receivertest.NewNopSettings(component.MustNewType("gardener")),
		consumer:      new(consumertest.MetricsSink),
		shootInformer: informer,
		logger:        zap.NewNop(),
	}
	md := pmetric.NewMetrics()
	sm := r.initScopeMetrics(&md)
	l := shootLookups{
		managedSeedShoots:  map[string]struct{}{"seed-shoot": {}}, // shoot is used as a seed
		seedByName:         map[string]*corev1beta1.Seed{},
		projectByNamespace: map[string]projectBillingInfo{},
	}
	r.collectShootInfoMetrics(&sm, nowTimestamp(), l)

	dp := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Gauge().DataPoints().At(0)
	isSeed, _ := dp.Attributes().Get("gardener.shoot.is_seed")
	assert.True(t, isSeed.Bool(), "is_seed should be true when shoot is in managedSeedShoots")
}

func TestCollectShootCreationTimestamp(t *testing.T) {
	ts := metav1.Unix(1700000000, 0)
	shoot := &corev1beta1.Shoot{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "ts-shoot",
			Namespace:         "garden-dev",
			CreationTimestamp: ts,
		},
		Spec: corev1beta1.ShootSpec{
			Provider:   corev1beta1.Provider{Type: "aws"},
			Region:     "eu-west-1",
			Kubernetes: corev1beta1.Kubernetes{Version: "1.28.0"},
		},
	}
	fakeClient := gardenerfake.NewSimpleClientset()
	factory := gardenerinformers.NewSharedInformerFactory(fakeClient, 0)
	informer := factory.Core().V1beta1().Shoots().Informer()
	require.NoError(t, informer.GetStore().Add(shoot))

	r := &gardenerReceiver{
		config:        &Config{Kubeconfig: "/tmp/fake", Resources: []string{"shoots"}},
		settings:      receivertest.NewNopSettings(component.MustNewType("gardener")),
		consumer:      new(consumertest.MetricsSink),
		shootInformer: informer,
		logger:        zap.NewNop(),
	}
	md := pmetric.NewMetrics()
	sm := r.initScopeMetrics(&md)
	r.collectShootCreationTimestamp(&sm, nowTimestamp())

	require.Equal(t, 1, md.DataPointCount())
	m := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0)
	assert.Equal(t, "garden.shoot.creation_timestamp", m.Name())
	assert.Equal(t, int64(1700000000), m.Gauge().DataPoints().At(0).IntValue())
}

func TestCollectShootHibernatedMetric(t *testing.T) {
	tests := []struct {
		name       string
		hibernated bool
		wantValue  int64
	}{
		{"not hibernated", false, 0},
		{"hibernated", true, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			shoot := &corev1beta1.Shoot{
				ObjectMeta: metav1.ObjectMeta{Name: "h-shoot", Namespace: "garden-dev"},
				Spec: corev1beta1.ShootSpec{
					Provider:   corev1beta1.Provider{Type: "aws"},
					Region:     "eu-west-1",
					Kubernetes: corev1beta1.Kubernetes{Version: "1.28.0"},
				},
				Status: corev1beta1.ShootStatus{IsHibernated: tc.hibernated},
			}
			fakeClient := gardenerfake.NewSimpleClientset()
			factory := gardenerinformers.NewSharedInformerFactory(fakeClient, 0)
			informer := factory.Core().V1beta1().Shoots().Informer()
			require.NoError(t, informer.GetStore().Add(shoot))

			r := &gardenerReceiver{
				config:        &Config{Kubeconfig: "/tmp/fake", Resources: []string{"shoots"}},
				settings:      receivertest.NewNopSettings(component.MustNewType("gardener")),
				consumer:      new(consumertest.MetricsSink),
				shootInformer: informer,
				logger:        zap.NewNop(),
			}
			md := pmetric.NewMetrics()
			sm := r.initScopeMetrics(&md)
			r.collectShootHibernatedMetric(&sm, nowTimestamp())

			require.Equal(t, 1, md.DataPointCount())
			m := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0)
			assert.Equal(t, "garden.shoot.hibernated", m.Name())
			assert.Equal(t, tc.wantValue, m.Gauge().DataPoints().At(0).IntValue())
		})
	}
}
