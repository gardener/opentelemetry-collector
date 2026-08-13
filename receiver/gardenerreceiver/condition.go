// SPDX-FileCopyrightText: Contributors to the Gardener project
//
// SPDX-License-Identifier: Apache-2.0

package gardenerreceiver

import (
	corev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
)

// conditionValueDescription documents the value encoding shared by every
// condition metric emitted by this receiver.
const conditionValueDescription = "Possible values: -1=Unknown, 0=False, 1=True, 2=Progressing."

// mapConditionStatus encodes a Gardener condition status as a numeric metric
// value, mirroring gardener-metrics-exporter: -1=Unknown, 0=False, 1=True,
// 2=Progressing.
func mapConditionStatus(status corev1beta1.ConditionStatus) int64 {
	switch status {
	case corev1beta1.ConditionTrue:
		return 1
	case corev1beta1.ConditionFalse:
		return 0
	case corev1beta1.ConditionProgressing:
		return 2
	default:
		return -1
	}
}
