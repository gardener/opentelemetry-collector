// SPDX-FileCopyrightText: Contributors to the Gardener project
//
// SPDX-License-Identifier: Apache-2.0

package gardenerreceiver

import (
	corev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

func (r *gardenerReceiver) collectCloudProfileMetrics(sm *pmetric.ScopeMetrics, now pcommon.Timestamp) {
	cloudProfileList := r.cloudProfileInformer.GetStore().List()

	if len(cloudProfileList) == 0 {
		r.logger.Debug("No cloud profiles found")
		return
	}

	kubernetesMetric := sm.Metrics().AppendEmpty()
	kubernetesMetric.SetName("garden.cloud_profile.kubernetes.version.info")
	kubernetesMetric.SetDescription("Information about Kubernetes versions in a CloudProfile")
	kubernetesMetric.SetUnit("")
	kubernetesGauge := kubernetesMetric.SetEmptyGauge()

	machineImageMetric := sm.Metrics().AppendEmpty()
	machineImageMetric.SetName("garden.cloud_profile.machine_image.version.info")
	machineImageMetric.SetDescription("Information about machine image versions in a CloudProfile")
	machineImageMetric.SetUnit("")
	machineImageGauge := machineImageMetric.SetEmptyGauge()

	for _, cloudProfileListItem := range cloudProfileList {
		cloudProfile := cloudProfileListItem.(*corev1beta1.CloudProfile)
		if cloudProfile.Status.Kubernetes != nil {
			for _, kubernetes := range cloudProfile.Status.Kubernetes.Versions {
				dp := kubernetesGauge.DataPoints().AppendEmpty()
				dp.SetTimestamp(now)
				dp.SetIntValue(1)
				dp.Attributes().PutStr("gardener.cloud_profile.name", cloudProfile.Name)
				dp.Attributes().PutStr("gardener.cloud_profile.kubernetes.classification", string(kubernetes.Classification))
				dp.Attributes().PutStr("gardener.cloud_profile.kubernetes.version", kubernetes.Version)
			}
		}

		for _, machineImage := range cloudProfile.Status.MachineImages {
			for _, version := range machineImage.Versions {
				dp := machineImageGauge.DataPoints().AppendEmpty()
				dp.SetTimestamp(now)
				dp.SetIntValue(1)
				dp.Attributes().PutStr("gardener.cloud_profile.name", cloudProfile.Name)
				dp.Attributes().PutStr("gardener.cloud_profile.machine_image.name", machineImage.Name)
				dp.Attributes().PutStr("gardener.cloud_profile.machine_image.version.classification", string(version.Classification))
				dp.Attributes().PutStr("gardener.cloud_profile.machine_image.version.name", version.Version)
			}
		}
	}
}
