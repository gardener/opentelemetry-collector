// SPDX-FileCopyrightText: Contributors to the Gardener project
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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newCloudProfileReceiver(t *testing.T, cloudProfiles ...*corev1beta1.CloudProfile) *gardenerReceiver {
	t.Helper()
	fakeClient := gardenerfake.NewSimpleClientset()
	factory := gardenerinformers.NewSharedInformerFactory(fakeClient, 0)
	informer := factory.Core().V1beta1().CloudProfiles().Informer()
	for _, cp := range cloudProfiles {
		require.NoError(t, informer.GetStore().Add(cp))
	}
	return &gardenerReceiver{
		config: &Config{
			Kubeconfig:         "/tmp/fake-kubeconfig-for-testing",
			CollectionInterval: 30 * time.Second,
			Resources:          []string{"cloudprofiles"},
		},
		settings:             receivertest.NewNopSettings(component.MustNewType("gardener")),
		consumer:             new(consumertest.MetricsSink),
		cloudProfileInformer: informer,
		logger:               zap.NewNop(),
	}
}

func TestCollectCloudProfileMetrics_Empty(t *testing.T) {
	r := newCloudProfileReceiver(t)
	md := pmetric.NewMetrics()
	sm := r.initScopeMetrics(&md)
	r.collectCloudProfileMetrics(&sm, nowTimestamp())
	require.Equal(t, 0, md.MetricCount())
}

func TestCollectCloudProfileMetrics(t *testing.T) {
	cloudProfile := &corev1beta1.CloudProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cloudProfileName",
		},
		Status: corev1beta1.CloudProfileStatus{
			Kubernetes: &corev1beta1.KubernetesStatus{
				Versions: []corev1beta1.ExpirableVersionStatus{
					{
						Version:        "1.36.5",
						Classification: corev1beta1.ClassificationSupported,
					},
					{
						Version:        "1.37.1",
						Classification: corev1beta1.ClassificationPreview,
					},
				},
			},
			MachineImages: []corev1beta1.MachineImageStatus{
				{
					Name: "my_os",
					Versions: []corev1beta1.ExpirableVersionStatus{
						{
							Version:        "v1",
							Classification: corev1beta1.ClassificationDeprecated,
						},
						{
							Version:        "v2",
							Classification: corev1beta1.ClassificationSupported,
						},
					},
				},
				{
					Name: "second_os",
					Versions: []corev1beta1.ExpirableVersionStatus{
						{
							Version:        "v1",
							Classification: corev1beta1.ClassificationSupported,
						},
					},
				},
			},
		},
	}
	r := newCloudProfileReceiver(t, cloudProfile)
	md := pmetric.NewMetrics()
	sm := r.initScopeMetrics(&md)
	r.collectCloudProfileMetrics(&sm, nowTimestamp())

	require.Equal(t, 2, md.MetricCount())
	require.Equal(t, 5, md.DataPointCount())
	m := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0)
	require.Equal(t, "garden.cloud_profile.kubernetes.version.info", m.Name())
	dp := m.Gauge().DataPoints().At(0)
	require.Equal(t, int64(1), dp.IntValue())
	name, ok := dp.Attributes().Get("gardener.cloud_profile.name")
	require.True(t, ok)
	require.Equal(t, "cloudProfileName", name.Str())
	version, ok := dp.Attributes().Get("gardener.cloud_profile.kubernetes.version")
	require.True(t, ok)
	require.Equal(t, "1.36.5", version.Str())
	classification, ok := dp.Attributes().Get("gardener.cloud_profile.kubernetes.classification")
	require.True(t, ok)
	require.Equal(t, "supported", classification.Str())

	dp = m.Gauge().DataPoints().At(1)
	require.Equal(t, int64(1), dp.IntValue())
	name, ok = dp.Attributes().Get("gardener.cloud_profile.name")
	require.True(t, ok)
	require.Equal(t, "cloudProfileName", name.Str())
	version, ok = dp.Attributes().Get("gardener.cloud_profile.kubernetes.version")
	require.True(t, ok)
	require.Equal(t, "1.37.1", version.Str())
	classification, ok = dp.Attributes().Get("gardener.cloud_profile.kubernetes.classification")
	require.True(t, ok)
	require.Equal(t, "preview", classification.Str())

	m = md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(1)
	require.Equal(t, "garden.cloud_profile.machine_image.version.info", m.Name())
	dp = m.Gauge().DataPoints().At(0)
	require.Equal(t, int64(1), dp.IntValue())
	name, ok = dp.Attributes().Get("gardener.cloud_profile.name")
	require.True(t, ok)
	require.Equal(t, "cloudProfileName", name.Str())
	machineImageName, ok := dp.Attributes().Get("gardener.cloud_profile.machine_image.name")
	require.True(t, ok)
	require.Equal(t, "my_os", machineImageName.Str())
	version, ok = dp.Attributes().Get("gardener.cloud_profile.machine_image.version.name")
	require.True(t, ok)
	require.Equal(t, "v1", version.Str())
	classification, ok = dp.Attributes().Get("gardener.cloud_profile.machine_image.version.classification")
	require.True(t, ok)
	require.Equal(t, "deprecated", classification.Str())

	dp = m.Gauge().DataPoints().At(1)
	require.Equal(t, int64(1), dp.IntValue())
	name, ok = dp.Attributes().Get("gardener.cloud_profile.name")
	require.True(t, ok)
	require.Equal(t, "cloudProfileName", name.Str())
	machineImageName, ok = dp.Attributes().Get("gardener.cloud_profile.machine_image.name")
	require.True(t, ok)
	require.Equal(t, "my_os", machineImageName.Str())
	version, ok = dp.Attributes().Get("gardener.cloud_profile.machine_image.version.name")
	require.True(t, ok)
	require.Equal(t, "v2", version.Str())
	classification, ok = dp.Attributes().Get("gardener.cloud_profile.machine_image.version.classification")
	require.True(t, ok)
	require.Equal(t, "supported", classification.Str())

	dp = m.Gauge().DataPoints().At(2)
	require.Equal(t, int64(1), dp.IntValue())
	name, ok = dp.Attributes().Get("gardener.cloud_profile.name")
	require.True(t, ok)
	require.Equal(t, "cloudProfileName", name.Str())
	machineImageName, ok = dp.Attributes().Get("gardener.cloud_profile.machine_image.name")
	require.True(t, ok)
	require.Equal(t, "second_os", machineImageName.Str())
	version, ok = dp.Attributes().Get("gardener.cloud_profile.machine_image.version.name")
	require.True(t, ok)
	require.Equal(t, "v1", version.Str())
	classification, ok = dp.Attributes().Get("gardener.cloud_profile.machine_image.version.classification")
	require.True(t, ok)
	require.Equal(t, "supported", classification.Str())
}
