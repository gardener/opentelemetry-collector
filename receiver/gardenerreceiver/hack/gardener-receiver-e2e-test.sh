#!/usr/bin/env bash
# Contributors to the Gardener project
# SPDX-License-Identifier: Apache-2.0


set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# The gardenerreceiver Go module root (one level up from hack/); the e2e suite
# that deploys the components and validates the metrics is rooted here.
MODULE_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
# The collector repository root (two levels up from the module), which holds the
# Dockerfile and the top-level `docker-image` make target.
REPO_ROOT="$(cd "${MODULE_DIR}/../.." && pwd)"

# GARDENER_DIR is exported so the Go suite can read the provider-local Shoot
# manifest it applies to the virtual garden.
export GARDENER_DIR="${GARDENER_DIR:-${GOPATH:-$(go env GOPATH)}/src/github.com/gardener/gardener}"
COLLECTOR_IMAGE_REPO="${COLLECTOR_IMAGE_REPO:-opentelemetry-collector}"
KIND_REGISTRY_HOST="${KIND_REGISTRY_HOST:-localhost:5001}"
KIND_REGISTRY_CLUSTER="${KIND_REGISTRY_CLUSTER:-registry.local.gardener.cloud:5001}"
COLLECTOR_IMAGE_TAG="${COLLECTOR_IMAGE_TAG:-e2e}"

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

build_and_push_collector_image() {
  local host_image="${KIND_REGISTRY_HOST}/${COLLECTOR_IMAGE_REPO}:${COLLECTOR_IMAGE_TAG}"
  local cluster_image="${KIND_REGISTRY_CLUSTER}/${COLLECTOR_IMAGE_REPO}:${COLLECTOR_IMAGE_TAG}"

  group "Check KinD registry is reachable (${KIND_REGISTRY_HOST})" >&2
  if ! curl -fsS "http://${KIND_REGISTRY_HOST}/v2/" >/dev/null; then
    echo "KinD-local registry not reachable at http://${KIND_REGISTRY_HOST}/v2/;" \
      "is 'gardener kind-up' done and did it provision the local registry?" >&2
    endgroup >&2
    return 1
  fi
  endgroup >&2

  group "Build collector image from source" >&2
  make -C "${REPO_ROOT}" docker-image IMAGE_REPOSITORY="${KIND_REGISTRY_HOST}/${COLLECTOR_IMAGE_REPO}" EFFECTIVE_VERSION="${COLLECTOR_IMAGE_TAG}" >&2
  endgroup >&2

  group "Push collector image to KinD registry (${host_image})" >&2
  docker push "${host_image}" >&2
  endgroup >&2

  echo "${cluster_image}"
}

run_e2e_test() {
  local collector_image="$1"

  # The Go/Ginkgo suite deploys the collectors, the Prometheus stack, and the
  # Shoot, waits for the operator-generated workloads to become Ready, then
  # port-forwards Prometheus and asserts the expected metrics appear. It reads
  # KUBECONFIG, GARDENER_API_KUBECONFIG, GARDENER_DIR, TEST_NAMESPACE, and
  # COLLECTOR_IMAGE from the environment (all exported above / passed here).
    COLLECTOR_IMAGE="${collector_image}" \
    make -C "${MODULE_DIR}" test-e2e-validate
}

main() {
  bring_up_kind
  deploy_gardener

  local collector_image
  collector_image=$(build_and_push_collector_image)
  echo "Testing collector image: ${collector_image}"

  run_e2e_test "${collector_image}"
}

main "$@"
