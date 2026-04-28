// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package gardenerreceiver

import (
	corev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

func (r *gardenerReceiver) collectProjectMetrics(sm *pmetric.ScopeMetrics, now pcommon.Timestamp) {
	projectList := r.projectInformer.GetStore().List()

	if len(projectList) == 0 {
		r.logger.Debug("No projects found")
		return
	}

	metric := sm.Metrics().AppendEmpty()
	metric.SetName("garden.project.info")
	metric.SetDescription("Status of projects")
	metric.SetUnit("")

	gauge := metric.SetEmptyGauge()

	for _, item := range projectList {
		project := item.(*corev1beta1.Project)
		dp := gauge.DataPoints().AppendEmpty()
		dp.SetTimestamp(now)
		dp.SetIntValue(1)
		dp.Attributes().PutStr("gardener.project.name", project.Name)
		dp.Attributes().PutStr("gardener.project.phase", string(project.Status.Phase))
	}
}

func (r *gardenerReceiver) collectUserMetrics(sm *pmetric.ScopeMetrics, now pcommon.Timestamp) {
	projectList := r.projectInformer.GetStore().List()

	if len(projectList) == 0 {
		r.logger.Debug("No projects found for user metrics")
		return
	}

	// Count unique users by Subject.Kind (User, Group, ServiceAccount)
	kindCount := map[string]int64{}
	for _, item := range projectList {
		project := item.(*corev1beta1.Project)
		for _, member := range project.Spec.Members {
			kindCount[member.Kind]++
		}
	}

	if len(kindCount) == 0 {
		return
	}

	metric := sm.Metrics().AppendEmpty()
	metric.SetName("garden.users")
	metric.SetDescription("Count of users by kind across all projects")
	metric.SetUnit("{user}")

	gauge := metric.SetEmptyGauge()

	for kind, count := range kindCount {
		dp := gauge.DataPoints().AppendEmpty()
		dp.SetTimestamp(now)
		dp.SetIntValue(count)
		dp.Attributes().PutStr("gardener.user.kind", kind)
	}
}
