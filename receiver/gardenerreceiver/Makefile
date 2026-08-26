# SPDX-FileCopyrightText: Contributors to the Gardener project
#
# SPDX-License-Identifier: Apache-2.0

include ../../Makefile.Common

GINKGO := go tool -modfile $(TOOLS_MOD_FILE) ginkgo

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

# Deploy-and-validate half of the e2e flow, split out so it can run on its own
# against a cluster already running Gardener + the operators (KinD brought up
# separately). Build-tagged (e2e) so it stays out of the normal `go test ./...`.
# Expects KUBECONFIG, GARDENER_API_KUBECONFIG, GARDENER_DIR, COLLECTOR_IMAGE (and
# optionally TEST_NAMESPACE) to be set — see hack/gardener-receiver-e2e-test.sh.
test-e2e-validate:
	$(GINKGO) --tags=e2e --timeout=30m --github-output -v ./test/e2e/...
