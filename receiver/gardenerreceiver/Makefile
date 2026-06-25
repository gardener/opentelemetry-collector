# SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
#
# SPDX-License-Identifier: Apache-2.0

include ../../Makefile.Common

.PHONY: bench-memory bench-memory-profile

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
