// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package gardenerreceiver

import (
	"testing"
	"time"

	corev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	gardenerfake "github.com/gardener/gardener/pkg/client/core/clientset/versioned/fake"
	gardenerinformers "github.com/gardener/gardener/pkg/client/core/informers/externalversions"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver/receivertest"
	"go.uber.org/zap"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newProjectReceiver(t *testing.T, projects ...*corev1beta1.Project) *gardenerReceiver {
	t.Helper()
	fakeClient := gardenerfake.NewSimpleClientset()
	factory := gardenerinformers.NewSharedInformerFactory(fakeClient, 0)
	informer := factory.Core().V1beta1().Projects().Informer()
	for _, p := range projects {
		require.NoError(t, informer.GetStore().Add(p))
	}
	return &gardenerReceiver{
		config: &Config{
			Kubeconfig:         "/tmp/fake-kubeconfig-for-testing",
			CollectionInterval: 30 * time.Second,
			Resources:          []string{"projects"},
		},
		settings:        receivertest.NewNopSettings(component.MustNewType("gardener")),
		consumer:        new(consumertest.MetricsSink),
		projectInformer: informer,
		logger:          zap.NewNop(),
	}
}

func TestCollectProjectMetrics_Empty(t *testing.T) {
	r := newProjectReceiver(t)
	md := pmetric.NewMetrics()
	sm := r.initScopeMetrics(&md)
	r.collectProjectMetrics(&sm, nowTimestamp())
	require.Equal(t, 0, md.MetricCount())
}

func TestCollectProjectMetrics(t *testing.T) {
	project := &corev1beta1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "my-project"},
		Status:     corev1beta1.ProjectStatus{Phase: corev1beta1.ProjectReady},
	}
	r := newProjectReceiver(t, project)
	md := pmetric.NewMetrics()
	sm := r.initScopeMetrics(&md)
	r.collectProjectMetrics(&sm, nowTimestamp())

	require.Equal(t, 1, md.MetricCount())
	require.Equal(t, 1, md.DataPointCount())
	m := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0)
	require.Equal(t, "garden.project.info", m.Name())
	dp := m.Gauge().DataPoints().At(0)
	require.Equal(t, int64(1), dp.IntValue())
	name, ok := dp.Attributes().Get("gardener.project.name")
	require.True(t, ok)
	require.Equal(t, "my-project", name.Str())
	phase, ok := dp.Attributes().Get("gardener.project.phase")
	require.True(t, ok)
	require.Equal(t, "Ready", phase.Str())
}

func TestCollectUserMetrics(t *testing.T) {
	project := &corev1beta1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "my-project"},
		Spec: corev1beta1.ProjectSpec{
			Members: []corev1beta1.ProjectMember{
				{Subject: rbacv1.Subject{Kind: "User", Name: "alice"}, Role: "admin"},
				{Subject: rbacv1.Subject{Kind: "User", Name: "bob"}, Role: "viewer"},
				{Subject: rbacv1.Subject{Kind: "ServiceAccount", Name: "sa1"}, Role: "viewer"},
			},
		},
	}
	r := newProjectReceiver(t, project)
	md := pmetric.NewMetrics()
	sm := r.initScopeMetrics(&md)
	r.collectUserMetrics(&sm, nowTimestamp())

	require.Equal(t, 1, md.MetricCount())
	require.Equal(t, "garden.users", md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Name())

	// There should be 2 data points: one for "User" (count 2), one for "ServiceAccount" (count 1)
	require.Equal(t, 2, md.DataPointCount())
}
