#!/usr/bin/env bash
# Copyright 2026 SAP SE or an SAP affiliate company and Gardener contributors
# SPDX-License-Identifier: Apache-2.0


set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# The gardenerreceiver Go module root (one level up from hack/); the e2e suite
# that deploys the components and validates the metrics is rooted here.
MODULE_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

# GARDENER_DIR is exported so the Go suite can read the provider-local Shoot
# manifest it applies to the virtual garden.
export GARDENER_DIR="${GARDENER_DIR:-${GOPATH:-$(go env GOPATH)}/src/github.com/gardener/gardener}"
COLLECTOR_IMAGE_REPO="${COLLECTOR_IMAGE_REPO:-europe-docker.pkg.dev/gardener-project/snapshots/gardener/observability/opentelemetry-collector}"
# Exported so the Go suite picks up the same namespace.
export TEST_NAMESPACE="${TEST_NAMESPACE:-gardener-monitoring-test}"

export KUBECONFIG="${GARDENER_DIR}/dev-setup/kubeconfigs/runtime/kubeconfig"

# The virtual-garden kubeconfig: mounted into the collectors as the gardener
# receiver's credentials, and used by the Go suite to create the Shoot.
# Exported (as GARDENER_API_KUBECONFIG) so the suite can consume it.
export GARDENER_API_KUBECONFIG="${GARDENER_RECEIVER_KUBECONFIG:-${GARDENER_DIR}/dev-setup/kubeconfigs/virtual-garden/kubeconfig}"

# `::group::` / `::endgroup::` render as collapsible sections in the GitHub
# Actions log and are harmless plain output when run locally.
group() { echo "::group::$*"; }
endgroup() { echo "::endgroup::"; }

teardown() {
  if [[ -n "${SKIP_TEARDOWN:-}" ]]; then
    echo "SKIP_TEARDOWN set; leaving KinD cluster running."
    return
  fi
  group "Tear down KinD cluster"
  make -C "${GARDENER_DIR}" kind-down || true
  endgroup
}
trap teardown EXIT

bring_up_kind() {
  group "Bring up KinD cluster (gardener kind-up)"
  make -C "${GARDENER_DIR}" kind-up
  endgroup
}

deploy_gardener() {
  group "Deploy Gardener (gardener-up)"
  make -C "${GARDENER_DIR}" gardener-up
  endgroup
}

resolve_collector_image() {
  local registry="${COLLECTOR_IMAGE_REPO%%/*}"
  local repository="${COLLECTOR_IMAGE_REPO#*/}"

  local tag
  tag=$(curl -fsSL "https://${registry}/v2/${repository}/tags/list" | jq -r '
    [
      (.manifest // {}) | to_entries[]
      | select(
          .value.mediaType == "application/vnd.docker.distribution.manifest.list.v2+json"
          or .value.mediaType == "application/vnd.oci.image.index.v1+json"
        )
      | .value as $manifest
      | $manifest.tag[]?
      | {
          tag: .,
          uploaded: ($manifest.timeUploadedMs | tonumber)
        }
    ]
    | max_by(.uploaded).tag // empty
  ')

  if [[ -z "${tag}" ]]; then
    echo "No Collector image tag found in ${COLLECTOR_IMAGE_REPO}" >&2
    return 1
  fi

  echo "${COLLECTOR_IMAGE_REPO}:${tag}"
}

run_e2e_test() {
  local collector_image="$1"

  group "Deploy components and validate Shoot metrics"
  # The Go/Ginkgo suite deploys the collectors, the Prometheus stack, and the
  # Shoot, waits for the operator-generated workloads to become Ready, then
  # port-forwards Prometheus and asserts the expected metrics appear. It reads
  # KUBECONFIG, GARDENER_API_KUBECONFIG, GARDENER_DIR, TEST_NAMESPACE, and
  # COLLECTOR_IMAGE from the environment (all exported above / passed here).
  COLLECTOR_IMAGE="${collector_image}" \
    go test -C "${MODULE_DIR}" -tags=e2e -timeout=30m ./test/e2e/...
  endgroup
}

main() {
  bring_up_kind
  deploy_gardener

  group "Resolve collector image reference"
  local collector_image
  collector_image=$(resolve_collector_image)
  echo "Resolved Collector image: ${collector_image}"
  endgroup

  run_e2e_test "${collector_image}"
}

main "$@"
