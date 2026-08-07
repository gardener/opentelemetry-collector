# SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
#
# SPDX-License-Identifier: Apache-2.0

include ../../Makefile.Common

.PHONY: bench-memory bench-memory-profile test-e2e test-e2e-validate

# Steady-state and churn memory benchmarks. Build-tagged so they don't run in
# normal `go test ./...`. count=5 gives benchstat enough samples to call
# significance across PRs.
bench-memory:
	go test -tags=bench -run=^$$ -bench=. -benchmem -benchtime=1x -count=5 \
	    -timeout=30m ./... | tee bench-memory.txt

# Heap profile of the largest scale point. Useful for diagnosing what dominates
# the cache footprint (annotations, managedFields, status conditions, ...).
bench-memory-profile:
	go test -tags=bench -run=^$$ -bench=BenchmarkScale_ShootCache/N=10000 \
	    -memprofile=mem.prof -benchtime=1x ./...
	go tool pprof -top -unit=mb mem.prof

test-e2e:
	./hack/gardener-receiver-e2e-test.sh

# Metric-validation half of the e2e flow, split out so it can run on its own
# against an already-provisioned cluster. Build-tagged (e2e) so it stays out of
# the normal `go test ./...`. Expects KUBECONFIG (and optionally TEST_NAMESPACE)
# to point at the runtime cluster.
test-e2e-validate:
	go test -tags=e2e -timeout=15m ./test/e2e/...
