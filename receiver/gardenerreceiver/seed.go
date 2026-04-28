// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package gardenerreceiver

import (
	corev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
	"k8s.io/utils/ptr"
)

func (r *gardenerReceiver) collectSeedInfoMetrics(sm *pmetric.ScopeMetrics, now pcommon.Timestamp) {
	seedList := r.seedInformer.GetStore().List()

	if len(seedList) == 0 {
		r.logger.Debug("No seeds found")
		return
	}

	// Create a gauge metric for seed
	metric := sm.Metrics().AppendEmpty()
	metric.SetName("garden.seed.info")
	metric.SetDescription("Information about Gardener seeds")
	metric.SetUnit("")

	gauge := metric.SetEmptyGauge()

	// Create a data point for each seed
	for _, seedListItem := range seedList {
		seed := seedListItem.(*corev1beta1.Seed)

		dp := gauge.DataPoints().AppendEmpty()
		dp.SetTimestamp(now)
		dp.SetIntValue(1)
		dp.Attributes().PutStr("gardener.seed.name", seed.Name)
		dp.Attributes().PutStr("cloud.provider", seed.Spec.Provider.Type)
		dp.Attributes().PutStr("cloud.region", seed.Spec.Provider.Region)
		dp.Attributes().PutStr("gardener.kubernetes.version", ptr.Deref(seed.Status.KubernetesVersion, ""))
		dp.Attributes().PutBool("gardener.seed.visible", isVisible(seed))
		dp.Attributes().PutBool("gardener.seed.protected", isProtected(seed))
	}

	r.logger.Debug("Sending seed metrics",
		zap.Int("seed_count", len(seedList)))
}

func (r *gardenerReceiver) emitSeedCapacityMetrics(sm *pmetric.ScopeMetrics, now pcommon.Timestamp) {
	seedList := r.seedInformer.GetStore().List()

	if len(seedList) == 0 {
		r.logger.Debug("No seeds found for capacity metrics")
		return
	}

	// Create a gauge metric for seed capacity
	metric := sm.Metrics().AppendEmpty()
	metric.SetName("garden.seed.capacity")
	metric.SetDescription("Seed capacity")
	metric.SetUnit("{shoot}")

	gauge := metric.SetEmptyGauge()

	// Create a data point for each seed
	for _, seedListItem := range seedList {
		seed := seedListItem.(*corev1beta1.Seed)
		for kind, resource := range seed.Status.Capacity {
			dp := gauge.DataPoints().AppendEmpty()
			dp.SetTimestamp(now)
			dp.SetIntValue(resource.Value())
			dp.Attributes().PutStr("gardener.seed.name", seed.Name)
			dp.Attributes().PutStr("cloud.provider", seed.Spec.Provider.Type)
			dp.Attributes().PutStr("cloud.region", seed.Spec.Provider.Region)
			dp.Attributes().PutStr("gardener.seed.resource", kind.String())
			dp.Attributes().PutBool("gardener.seed.visible", isVisible(seed))
			dp.Attributes().PutBool("gardener.seed.protected", isProtected(seed))
		}
	}

	r.logger.Debug("Sending seed capacity metrics",
		zap.Int("seed_count", len(seedList)))
}

func (r *gardenerReceiver) collectSeedConditions(sm *pmetric.ScopeMetrics, now pcommon.Timestamp) {
	seedList := r.seedInformer.GetStore().List()

	if len(seedList) == 0 {
		r.logger.Debug("No seeds found for condition metrics")
		return
	}

	metric := sm.Metrics().AppendEmpty()
	metric.SetName("garden.seed.condition")
	metric.SetDescription("Condition state of a Seed")
	metric.SetUnit("")

	gauge := metric.SetEmptyGauge()

	for _, seedListItem := range seedList {
		seed := seedListItem.(*corev1beta1.Seed)
		for _, condition := range seed.Status.Conditions {
			dp := gauge.DataPoints().AppendEmpty()
			dp.SetTimestamp(now)
			dp.SetIntValue(1)
			dp.Attributes().PutStr("gardener.seed.name", seed.Name)
			dp.Attributes().PutStr("gardener.condition.type", string(condition.Type))
			dp.Attributes().PutStr("gardener.condition.status", string(condition.Status))
			dp.Attributes().PutStr("gardener.condition.reason", condition.Reason)
		}
	}
}

func (r *gardenerReceiver) collectSeedAllocatableMetrics(sm *pmetric.ScopeMetrics, now pcommon.Timestamp) {
	seedList := r.seedInformer.GetStore().List()

	if len(seedList) == 0 {
		r.logger.Debug("No seeds found for allocatable metrics")
		return
	}

	metric := sm.Metrics().AppendEmpty()
	metric.SetName("garden.seed.usage")
	metric.SetDescription("Seed allocatable (available for scheduling)")
	metric.SetUnit("{shoot}")

	gauge := metric.SetEmptyGauge()

	for _, seedListItem := range seedList {
		seed := seedListItem.(*corev1beta1.Seed)
		for kind, resource := range seed.Status.Allocatable {
			dp := gauge.DataPoints().AppendEmpty()
			dp.SetTimestamp(now)
			dp.SetIntValue(resource.Value())
			dp.Attributes().PutStr("gardener.seed.name", seed.Name)
			dp.Attributes().PutStr("cloud.provider", seed.Spec.Provider.Type)
			dp.Attributes().PutStr("cloud.region", seed.Spec.Provider.Region)
			dp.Attributes().PutStr("gardener.seed.resource", kind.String())
			dp.Attributes().PutBool("gardener.seed.visible", isVisible(seed))
			dp.Attributes().PutBool("gardener.seed.protected", isProtected(seed))
		}
	}
}

func (r *gardenerReceiver) collectSeedOperationStates(sm *pmetric.ScopeMetrics, now pcommon.Timestamp) {
	seedList := r.seedInformer.GetStore().List()

	if len(seedList) == 0 {
		r.logger.Debug("No seeds found for operation state metrics")
		return
	}

	metric := sm.Metrics().AppendEmpty()
	metric.SetName("garden.seed.operation")
	metric.SetDescription("Operation state of a Seed. Available operations: 'Create'|'Reconcile'|'Delete'|'Restore'|'Migrate'.")
	metric.SetUnit("")

	gauge := metric.SetEmptyGauge()

	for _, seedListItem := range seedList {
		seed := seedListItem.(*corev1beta1.Seed)
		if seed.Status.LastOperation == nil {
			continue
		}
		dp := gauge.DataPoints().AppendEmpty()
		dp.SetTimestamp(now)
		dp.SetIntValue(1)
		dp.Attributes().PutStr("gardener.seed.name", seed.Name)
		dp.Attributes().PutStr("gardener.operation.type", string(seed.Status.LastOperation.Type))
		dp.Attributes().PutStr("gardener.operation.state", string(seed.Status.LastOperation.State))
		dp.Attributes().PutInt("gardener.operation.progress", int64(seed.Status.LastOperation.Progress))
	}
}

func isProtected(seed *corev1beta1.Seed) bool {
	for _, t := range seed.Spec.Taints {
		if t.Key == "seed.gardener.cloud/protected" {
			return true
		}
	}
	return false
}

func isVisible(seed *corev1beta1.Seed) bool {
	if seed.Spec.Settings == nil || seed.Spec.Settings.Scheduling == nil {
		return true
	}
	return seed.Spec.Settings.Scheduling.Visible
}
