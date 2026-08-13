// SPDX-FileCopyrightText: Contributors to the Gardener project
//
// SPDX-License-Identifier: Apache-2.0

package gardenerreceiver

import (
	"testing"

	corev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	"github.com/stretchr/testify/require"
)

func TestMapConditionStatus(t *testing.T) {
	tests := []struct {
		name     string
		status   corev1beta1.ConditionStatus
		expected int64
	}{
		{name: "true", status: corev1beta1.ConditionTrue, expected: 1},
		{name: "false", status: corev1beta1.ConditionFalse, expected: 0},
		{name: "unknown", status: corev1beta1.ConditionUnknown, expected: -1},
		{name: "progressing", status: corev1beta1.ConditionProgressing, expected: 2},
		{name: "empty", status: corev1beta1.ConditionStatus(""), expected: -1},
		{name: "unexpected", status: corev1beta1.ConditionStatus("Unexpected"), expected: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, mapConditionStatus(tt.status))
		})
	}
}
