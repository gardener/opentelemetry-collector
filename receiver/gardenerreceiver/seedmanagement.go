// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package gardenerreceiver

import (
	seedmanagementv1alpha1 "github.com/gardener/gardener/pkg/apis/seedmanagement/v1alpha1"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

func (r *gardenerReceiver) collectManagedSeedMetrics(sm *pmetric.ScopeMetrics, now pcommon.Timestamp) {
	list := r.managedSeedInformer.GetStore().List()

	if len(list) == 0 {
		r.logger.Debug("No managed seeds found")
		return
	}

	metric := sm.Metrics().AppendEmpty()
	metric.SetName("garden.managed_seed.info")
	metric.SetDescription("Information about a managed seed")
	metric.SetUnit("")

	gauge := metric.SetEmptyGauge()

	for _, item := range list {
		ms := item.(*seedmanagementv1alpha1.ManagedSeed)
		dp := gauge.DataPoints().AppendEmpty()
		dp.SetTimestamp(now)
		dp.SetIntValue(1)
		dp.Attributes().PutStr("gardener.seed.name", ms.Name)
		shootName := ""
		if ms.Spec.Shoot != nil {
			shootName = ms.Spec.Shoot.Name
		}
		dp.Attributes().PutStr("gardener.shoot.name", shootName)
	}
}

func (r *gardenerReceiver) collectGardenletMetrics(sm *pmetric.ScopeMetrics, now pcommon.Timestamp) {
	list := r.gardenletInformer.GetStore().List()

	if len(list) == 0 {
		r.logger.Debug("No gardenlets found")
		return
	}

	conditionMetric := sm.Metrics().AppendEmpty()
	conditionMetric.SetName("garden.gardenlet.condition")
	conditionMetric.SetDescription("Condition state of a Gardenlet")
	conditionMetric.SetUnit("")
	conditionGauge := conditionMetric.SetEmptyGauge()

	generationMetric := sm.Metrics().AppendEmpty()
	generationMetric.SetName("garden.gardenlet.generation")
	generationMetric.SetDescription("Generation of a Gardenlet")
	generationMetric.SetUnit("{generation}")
	generationGauge := generationMetric.SetEmptyGauge()

	observedGenerationMetric := sm.Metrics().AppendEmpty()
	observedGenerationMetric.SetName("garden.gardenlet.observed_generation")
	observedGenerationMetric.SetDescription("Observed generation of a Gardenlet")
	observedGenerationMetric.SetUnit("{observed_generation}")
	observedGenerationGauge := observedGenerationMetric.SetEmptyGauge()

	for _, item := range list {
		gl := item.(*seedmanagementv1alpha1.Gardenlet)

		for _, condition := range gl.Status.Conditions {
			dp := conditionGauge.DataPoints().AppendEmpty()
			dp.SetTimestamp(now)
			dp.SetIntValue(1)
			dp.Attributes().PutStr("gardener.gardenlet.name", gl.Name)
			dp.Attributes().PutStr("gardener.condition.type", string(condition.Type))
			dp.Attributes().PutStr("gardener.condition.status", string(condition.Status))
			dp.Attributes().PutStr("gardener.condition.reason", condition.Reason)
		}

		genDp := generationGauge.DataPoints().AppendEmpty()
		genDp.SetTimestamp(now)
		genDp.SetIntValue(gl.Generation)
		genDp.Attributes().PutStr("gardener.gardenlet.name", gl.Name)

		obsDp := observedGenerationGauge.DataPoints().AppendEmpty()
		obsDp.SetTimestamp(now)
		obsDp.SetIntValue(gl.Status.ObservedGeneration)
		obsDp.Attributes().PutStr("gardener.gardenlet.name", gl.Name)
	}
}
