#!/usr/bin/env bash
# Copyright 2026 SAP SE or an SAP affiliate company and Gardener contributors
# SPDX-License-Identifier: Apache-2.0


set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# The gardenerreceiver Go module root (one level up from hack/); the metric
# validation runs as a build-tagged Go test rooted here.
MODULE_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

GARDENER_DIR="${GARDENER_DIR:-${GOPATH:-$(go env GOPATH)}/src/github.com/gardener/gardener}"
COLLECTOR_IMAGE_REPO="${COLLECTOR_IMAGE_REPO:-europe-docker.pkg.dev/gardener-project/snapshots/gardener/observability/opentelemetry-collector}"
# Exported so the Go validation test (hack/e2e) picks up the same namespace.
export TEST_NAMESPACE="${TEST_NAMESPACE:-gardener-monitoring-test}"

export KUBECONFIG="${GARDENER_DIR}/dev-setup/kubeconfigs/runtime/kubeconfig"

GARDENER_API_KUBECONFIG="${GARDENER_RECEIVER_KUBECONFIG:-${GARDENER_DIR}/dev-setup/kubeconfigs/virtual-garden/kubeconfig}"

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

run_collector_e2e_test() {
  local collector_image="$1"

  group "Create test namespace"
  kubectl create namespace "${TEST_NAMESPACE}" \
    --dry-run=client -o yaml | kubectl apply -f -
  endgroup

  group "Create gardener receiver kubeconfig secret"
  if [[ ! -f "${GARDENER_API_KUBECONFIG}" ]]; then
    echo "Gardener receiver kubeconfig not found: ${GARDENER_API_KUBECONFIG}" >&2
    return 1
  fi
  kubectl create secret generic gardener-viewer-kubeconfig \
    --namespace "${TEST_NAMESPACE}" \
    --from-file=kubeconfig="${GARDENER_API_KUBECONFIG}" \
    --dry-run=client -o yaml | kubectl apply -f -
  endgroup

  group "Deploy OpenTelemetry Collector with Prometheus exporter"
  kubectl apply -f - <<EOF
apiVersion: opentelemetry.io/v1beta1
kind: OpenTelemetryCollector
metadata:
  name: gardener-prometheus
  namespace: ${TEST_NAMESPACE}
spec:
  image: ${collector_image}
  mode: deployment
  replicas: 1
  ports:
    - name: prometheus
      port: 8889
      protocol: TCP
  volumes:
    - name: gardener-viewer-kubeconfig
      secret:
        secretName: gardener-viewer-kubeconfig
  volumeMounts:
    - name: gardener-viewer-kubeconfig
      mountPath: /var/run/secrets/gardener
      readOnly: true
  config:
    receivers:
      gardener:
        kubeconfig: /var/run/secrets/gardener/kubeconfig
    exporters:
      prometheus:
        endpoint: 0.0.0.0:8889
    service:
      pipelines:
        metrics:
          receivers: [gardener]
          exporters: [prometheus]
EOF
  endgroup

  group "Deploy OpenTelemetry Collector with OTLP exporter"
  kubectl apply -f - <<EOF
apiVersion: opentelemetry.io/v1beta1
kind: OpenTelemetryCollector
metadata:
  name: gardener-otlp
  namespace: ${TEST_NAMESPACE}
spec:
  image: ${collector_image}
  mode: deployment
  replicas: 1
  volumes:
    - name: gardener-viewer-kubeconfig
      secret:
        secretName: gardener-viewer-kubeconfig
  volumeMounts:
    - name: gardener-viewer-kubeconfig
      mountPath: /var/run/secrets/gardener
      readOnly: true
  config:
    receivers:
      gardener:
        kubeconfig: /var/run/secrets/gardener/kubeconfig
    exporters:
      otlphttp:
        # Prometheus OTLP receiver lives at /api/v1/otlp; the otlphttp
        # exporter appends /v1/metrics to reach /api/v1/otlp/v1/metrics.
        metrics_endpoint: http://prometheus-operated.${TEST_NAMESPACE}.svc:9090/api/v1/otlp/v1/metrics
    service:
      pipelines:
        metrics:
          receivers: [gardener]
          exporters: [otlphttp]
EOF
  endgroup

  group "Deploy Prometheus instance and ServiceMonitor"
  kubectl apply -f - <<EOF
apiVersion: v1
kind: ServiceAccount
metadata:
  name: prometheus
  namespace: ${TEST_NAMESPACE}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: ${TEST_NAMESPACE}-prometheus
rules:
  - apiGroups: [""]
    resources:
      - nodes
      - nodes/metrics
      - services
      - endpoints
      - pods
    verbs: ["get", "list", "watch"]
  - apiGroups: [""]
    resources:
      - configmaps
    verbs: ["get"]
  - apiGroups:
      - discovery.k8s.io
    resources:
      - endpointslices
    verbs: ["get", "list", "watch"]
  - apiGroups:
      - networking.k8s.io
    resources:
      - ingresses
    verbs: ["get", "list", "watch"]
  - nonResourceURLs: ["/metrics"]
    verbs: ["get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: ${TEST_NAMESPACE}-prometheus
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: ${TEST_NAMESPACE}-prometheus
subjects:
  - kind: ServiceAccount
    name: prometheus
    namespace: ${TEST_NAMESPACE}
---
apiVersion: monitoring.coreos.com/v1
kind: Prometheus
metadata:
  name: gardener
  namespace: ${TEST_NAMESPACE}
spec:
  serviceAccountName: prometheus
  enableFeatures:
    - otlp-write-receiver
  otlp:
    translationStrategy: NoTranslation
  serviceMonitorSelector:
    matchLabels:
      app: gardener-prometheus-collector
  serviceMonitorNamespaceSelector: {}
---
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: gardener-prometheus-collector
  namespace: ${TEST_NAMESPACE}
  labels:
    app: gardener-prometheus-collector
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: gardener-prometheus-collector
      operator.opentelemetry.io/collector-service-type: base
  endpoints:
    - port: prometheus
      interval: 30s
EOF
  endgroup

  group "Create Shoot"
  kubectl --kubeconfig="$GARDENER_API_KUBECONFIG" apply \
    -f "$GARDENER_DIR/example/provider-local/shoot.yaml"
  endgroup

  group "Wait for Collector deployment to become Ready"
  kubectl wait --namespace "${TEST_NAMESPACE}" \
    --for=create deployment/gardener-prometheus-collector --timeout=5m
  kubectl rollout status --namespace "${TEST_NAMESPACE}" \
    deployment/gardener-prometheus-collector --timeout=5m
  endgroup

  group "Wait for OTLP Collector deployment to become Ready"
  kubectl wait --namespace "${TEST_NAMESPACE}" \
    --for=create deployment/gardener-otlp-collector --timeout=5m
  kubectl rollout status --namespace "${TEST_NAMESPACE}" \
    deployment/gardener-otlp-collector --timeout=5m
  endgroup

  group "Wait for Prometheus instance to become Ready"
  kubectl wait --namespace "${TEST_NAMESPACE}" \
    --for=create statefulset/prometheus-gardener --timeout=5m
  kubectl rollout status --namespace "${TEST_NAMESPACE}" \
    statefulset/prometheus-gardener --timeout=5m
  endgroup

  group "Validate Shoot metrics"
  # The prometheus-operated service must exist before the Go test can
  # port-forward to it; everything else (readiness, querying, assertions) is
  # handled by the build-tagged Ginkgo suite in test/e2e.
  kubectl wait --namespace "${TEST_NAMESPACE}" \
    --for=create service/prometheus-operated --timeout=5m

  # KUBECONFIG and TEST_NAMESPACE are exported above and consumed by the test.
  ( cd "${MODULE_DIR}" && go test -tags=e2e -timeout=15m ./test/e2e/... )
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

  run_collector_e2e_test "${collector_image}"
}

main "$@"
