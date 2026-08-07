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

func (r *gardenerReceiver) collectSeedCapacityMetrics(sm *pmetric.ScopeMetrics, now pcommon.Timestamp) {
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
			dp.Attributes().PutStr("gardener.seed.resource", kind.String())
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
			dp.Attributes().PutStr("gardener.seed.resource", kind.String())
		}
	}
}

func (r *gardenerReceiver) collectSeedOperationStates(sm *pmetric.ScopeMetrics, now pcommon.Timestamp) {
	seedList := r.seedInformer.GetStore().List()

	if len(seedList) == 0 {
		r.logger.Debug("No seeds found for operation state metrics")
		return
	}

	statesMetric := sm.Metrics().AppendEmpty()
	statesMetric.SetName("garden.seed.operation_states")
	statesMetric.SetDescription("Operation state of a Seed. Available operations: 'Create'|'Reconcile'|'Delete'|'Migrate'|'Restore'|'LiveMigrate'.")
	statesMetric.SetUnit("")
	statesGauge := statesMetric.SetEmptyGauge()

	progressMetric := sm.Metrics().AppendEmpty()
	progressMetric.SetName("garden.seed.operation_progress_percent")
	progressMetric.SetDescription("Operation progress of a Seed in percent.")
	progressMetric.SetUnit("%")
	progressGauge := progressMetric.SetEmptyGauge()

	allOperationTypes := []corev1beta1.LastOperationType{
		corev1beta1.LastOperationTypeCreate,
		corev1beta1.LastOperationTypeReconcile,
		corev1beta1.LastOperationTypeDelete,
		corev1beta1.LastOperationTypeMigrate,
		corev1beta1.LastOperationTypeRestore,
		corev1beta1.LastOperationTypeLiveMigrate,
	}

	for _, seedListItem := range seedList {
		seed := seedListItem.(*corev1beta1.Seed)

		for _, opType := range allOperationTypes {
			statesDp := statesGauge.DataPoints().AppendEmpty()
			statesDp.SetTimestamp(now)
			statesDp.Attributes().PutStr("gardener.seed.name", seed.Name)
			statesDp.Attributes().PutStr("gardener.operation.type", string(opType))

			progressDp := progressGauge.DataPoints().AppendEmpty()
			progressDp.SetTimestamp(now)
			progressDp.Attributes().PutStr("gardener.seed.name", seed.Name)
			progressDp.Attributes().PutStr("gardener.operation.type", string(opType))

			if seed.Status.LastOperation != nil && seed.Status.LastOperation.Type == opType {
				statesDp.Attributes().PutStr("gardener.operation.state", string(seed.Status.LastOperation.State))
				statesDp.SetIntValue(1)
				progressDp.SetIntValue(int64(seed.Status.LastOperation.Progress))
			} else {
				statesDp.Attributes().PutStr("gardener.operation.state", "")
				statesDp.SetIntValue(0)
				progressDp.SetIntValue(0)
			}
		}
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
