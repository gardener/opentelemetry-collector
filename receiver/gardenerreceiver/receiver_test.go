// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package gardenerreceiver

import (
	"context"
	"errors"
	"testing"
	"time"

	corev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	gardenerfake "github.com/gardener/gardener/pkg/client/core/clientset/versioned/fake"
	gardenerinformers "github.com/gardener/gardener/pkg/client/core/informers/externalversions"
	seedmanagementfake "github.com/gardener/gardener/pkg/client/seedmanagement/clientset/versioned/fake"
	seedmanagementinformers "github.com/gardener/gardener/pkg/client/seedmanagement/informers/externalversions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver/receiverhelper"
	"go.opentelemetry.io/collector/receiver/receivertest"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestNewFactory(t *testing.T) {
	factory := NewFactory()
	require.NotNil(t, factory, "NewFactory() should not return nil")

	require.Equal(t, component.MustNewType("gardener"), factory.Type(), "unexpected factory type")
}

func TestCreateDefaultConfig(t *testing.T) {
	factory := NewFactory()
	cfg := factory.CreateDefaultConfig()
	require.NotNil(t, cfg, "CreateDefaultConfig() should not return nil")

	require.NoError(t, componenttest.CheckConfigStruct(cfg), "CheckConfigStruct() should succeed")
}

func TestCreateMetricsReceiver_DefaultConfig(t *testing.T) {
	factory := NewFactory()
	cfg := factory.CreateDefaultConfig()

	set := receivertest.NewNopSettings(component.MustNewType("gardener"))
	consumer := consumertest.NewNop()

	receiver, err := factory.CreateMetrics(context.Background(), set, cfg, consumer)
	require.Error(t, err, "CreateMetrics() should fail with missing kubeconfig")
	require.Nil(t, receiver, "receiver should be nil on error")
}

func TestCreateMetricsReceiver_InvalidKubeConfig(t *testing.T) {
	factory := NewFactory()
	cfg := factory.CreateDefaultConfig()
	// Use a fake kubeconfig path for testing - the receiver creation will fail
	// but we're testing factory functionality, not actual K8s connectivity
	rCfg := cfg.(*Config)
	rCfg.Kubeconfig = "/tmp/fake-kubeconfig-for-testing"

	set := receivertest.NewNopSettings(component.MustNewType("gardener"))
	consumer := consumertest.NewNop()

	// This will fail because the kubeconfig doesn't exist, but that's expected in unit tests
	// We're just testing that the factory can create the receiver object
	receiver, err := factory.CreateMetrics(context.Background(), set, cfg, consumer)
	require.Error(t, err, "CreateMetrics() should fail with invalid kubeconfig")
	require.Nil(t, receiver, "receiver should be nil on error")
}

// newTestReceiver builds a gardenerReceiver with pre-wired fake informers and
// a real ObsReport so that sendMetrics / emitMetrics can be exercised without
// a live Kubernetes cluster.
func newTestReceiver(t *testing.T, cfg *Config, c consumer.Metrics) *gardenerReceiver {
	t.Helper()

	set := receivertest.NewNopSettings(component.MustNewType("gardener"))
	obsrecv, err := receiverhelper.NewObsReport(receiverhelper.ObsReportSettings{
		ReceiverID:             set.ID,
		ReceiverCreateSettings: set,
	})
	require.NoError(t, err)

	// Shoot informer
	gardenClient := gardenerfake.NewSimpleClientset()
	gardenFactory := gardenerinformers.NewSharedInformerFactory(gardenClient, 0)
	shootInformer := gardenFactory.Core().V1beta1().Shoots().Informer()
	seedInformer := gardenFactory.Core().V1beta1().Seeds().Informer()
	projectInformer := gardenFactory.Core().V1beta1().Projects().Informer()

	// SeedManagement informer
	seedMgmtClient := seedmanagementfake.NewSimpleClientset()
	seedMgmtFactory := seedmanagementinformers.NewSharedInformerFactory(seedMgmtClient, 0)
	managedSeedInformer := seedMgmtFactory.Seedmanagement().V1alpha1().ManagedSeeds().Informer()
	gardenletInformer := seedMgmtFactory.Seedmanagement().V1alpha1().Gardenlets().Informer()

	return &gardenerReceiver{
		settings:            set,
		config:              cfg,
		consumer:            c,
		logger:              zap.NewNop(),
		obsrecv:             obsrecv,
		shootInformer:       shootInformer,
		seedInformer:        seedInformer,
		projectInformer:     projectInformer,
		managedSeedInformer: managedSeedInformer,
		gardenletInformer:   gardenletInformer,
	}
}

func TestShutdown_BeforeStart(t *testing.T) {
	cfg := &Config{
		CollectionInterval: 50 * time.Millisecond,
		SyncPeriod:         time.Hour,
		Resources:          []string{"shoots", "seeds"},
	}
	r := newTestReceiver(t, cfg, consumertest.NewNop())
	// Shutdown before Start must not panic (cancel is nil).
	require.NoError(t, r.Shutdown(context.Background()))
}

func TestShutdown_AfterStart(t *testing.T) {
	cfg := &Config{
		CollectionInterval: 50 * time.Millisecond,
		SyncPeriod:         time.Hour,
		Resources:          []string{"shoots", "seeds"},
	}
	r := newTestReceiver(t, cfg, consumertest.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel

	// Shutdown should call cancel and not return an error.
	require.NoError(t, r.Shutdown(ctx))

	// After shutdown the context must be done.
	select {
	case <-ctx.Done():
	default:
		t.Fatal("expected context to be cancelled after Shutdown")
	}
}

func TestSendMetrics_EmptyInformers(t *testing.T) {
	cfg := &Config{
		CollectionInterval: time.Second,
		SyncPeriod:         time.Hour,
		Resources:          []string{"shoots", "seeds", "projects", "managedseeds", "gardenlets"},
	}
	sink := new(consumertest.MetricsSink)
	r := newTestReceiver(t, cfg, sink)

	require.NoError(t, r.sendMetrics(context.Background()))
	// With empty informers every collector is a no-op; the consumer still
	// receives exactly one (empty) Metrics batch.
	require.Len(t, sink.AllMetrics(), 1)
}

// errorConsumer is a consumer.Metrics that always returns the provided error.
type errorConsumer struct{ err error }

func (e *errorConsumer) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: false}
}

func (e *errorConsumer) ConsumeMetrics(_ context.Context, _ pmetric.Metrics) error {
	return e.err
}

func TestSendMetrics_ConsumerError(t *testing.T) {
	cfg := &Config{
		CollectionInterval: time.Second,
		SyncPeriod:         time.Hour,
		Resources:          []string{"shoots"},
	}
	downstreamError := errors.New("downstream unavailable")
	r := newTestReceiver(t, cfg, &errorConsumer{err: downstreamError})

	err := r.sendMetrics(context.Background())
	require.ErrorIs(t, err, downstreamError)
}

func TestEmitMetrics_StopsOnShutdown(t *testing.T) {
	cfg := &Config{
		CollectionInterval: 20 * time.Millisecond,
		SyncPeriod:         time.Hour,
		Resources:          []string{"shoots"},
	}
	sink := new(consumertest.MetricsSink)
	r := newTestReceiver(t, cfg, sink)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		r.emitMetrics(ctx)
		close(done)
	}()

	// Let at least one tick fire, then cancel.
	time.Sleep(60 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("emitMetrics did not stop after context cancellation")
	}

	// At least the initial send must have happened.
	assert.GreaterOrEqual(t, len(sink.AllMetrics()), 1)
}

func TestSendMetrics_ResourceGating(t *testing.T) {
	tests := []struct {
		name      string
		resources []string
		wantAny   string
	}{
		{"seeds only", []string{"seeds"}, "garden.seed"},
		{"projects only", []string{"projects"}, "garden.project"},
		{"managedseeds only", []string{"managedseeds"}, ""}, // empty store → no metric
		{"gardenlets only", []string{"gardenlets"}, ""},     // empty store → no metric
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{
				CollectionInterval: time.Second,
				SyncPeriod:         time.Hour,
				Resources:          tc.resources,
			}
			sink := new(consumertest.MetricsSink)
			r := newTestReceiver(t, cfg, sink)

			seed := &corev1beta1.Seed{
				ObjectMeta: metav1.ObjectMeta{Name: "s1"},
				Spec:       corev1beta1.SeedSpec{Provider: corev1beta1.SeedProvider{Type: "aws", Region: "eu"}},
				Status:     corev1beta1.SeedStatus{KubernetesVersion: ptr.To("1.27.0")},
			}
			project := &corev1beta1.Project{
				ObjectMeta: metav1.ObjectMeta{Name: "p1"},
				Status:     corev1beta1.ProjectStatus{Phase: corev1beta1.ProjectReady},
			}
			require.NoError(t, r.seedInformer.GetStore().Add(seed))
			require.NoError(t, r.projectInformer.GetStore().Add(project))

			require.NoError(t, r.sendMetrics(context.Background()))
			require.Len(t, sink.AllMetrics(), 1)

			if tc.wantAny == "" {
				return
			}
			found := false
			md := sink.AllMetrics()[0]
			for i := 0; i < md.ResourceMetrics().Len(); i++ {
				for j := 0; j < md.ResourceMetrics().At(i).ScopeMetrics().Len(); j++ {
					for k := 0; k < md.ResourceMetrics().At(i).ScopeMetrics().At(j).Metrics().Len(); k++ {
						if md.ResourceMetrics().At(i).ScopeMetrics().At(j).Metrics().At(k).Name() == tc.wantAny {
							found = true
						}
					}
				}
			}
			assert.True(t, found, "expected metric %q to be present", tc.wantAny)
		})
	}
}
