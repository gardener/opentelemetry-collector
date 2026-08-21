// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package gardenerreceiver

import (
	"testing"

	corev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

var expectedOperationTypes = []string{
	string(corev1beta1.LastOperationTypeCreate),
	string(corev1beta1.LastOperationTypeReconcile),
	string(corev1beta1.LastOperationTypeDelete),
	string(corev1beta1.LastOperationTypeMigrate),
	string(corev1beta1.LastOperationTypeRestore),
	string(corev1beta1.LastOperationTypeLiveMigrate),
}

func requireOperationTypes(t *testing.T, dataPoints pmetric.NumberDataPointSlice) {
	t.Helper()

	require.Equal(t, len(expectedOperationTypes), dataPoints.Len(), "unexpected operation data point count")

	operationTypes := make([]string, 0, dataPoints.Len())
	for i := 0; i < dataPoints.Len(); i++ {
		opType, ok := dataPoints.At(i).Attributes().Get("gardener.operation.type")
		require.Truef(t, ok, "missing gardener.operation.type attribute on data point %d", i)
		operationTypes = append(operationTypes, opType.Str())
	}

	require.ElementsMatch(t, expectedOperationTypes, operationTypes, "unexpected operation types")
}

func requireReconcileOperationDataPoint(t *testing.T, dataPoints pmetric.NumberDataPointSlice) pmetric.NumberDataPoint {
	t.Helper()

	for i := 0; i < dataPoints.Len(); i++ {
		dp := dataPoints.At(i)
		opType, ok := dp.Attributes().Get("gardener.operation.type")
		require.Truef(t, ok, "missing gardener.operation.type attribute on data point %d", i)
		if opType.Str() == string(corev1beta1.LastOperationTypeReconcile) {
			return dp
		}
	}

	t.Fatalf("missing operation data point for %q", corev1beta1.LastOperationTypeReconcile)
	return pmetric.NumberDataPoint{}
}
