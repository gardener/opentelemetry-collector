// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// TestE2E is the entry point for the Gardener receiver end-to-end suite. It is
// guarded by the `e2e` build tag so it never runs as part of `go test ./...`;
// the cluster it asserts against is provisioned by
// hack/gardener-receiver-e2e-test.sh.
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Gardener Receiver E2E Suite")
}
