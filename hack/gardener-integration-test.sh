#!/usr/bin/env bash

set -euo pipefail

GARDENER_DIR="${GARDENER_DIR:-${GOPATH:-$(go env GOPATH)}/src/github.com/gardener/gardener}"
COLLECTOR_IMAGE_REPO="${COLLECTOR_IMAGE_REPO:-europe-docker.pkg.dev/gardener-project/snapshots/gardener/observability/opentelemetry-collector}"
TEST_NAMESPACE="${TEST_NAMESPACE:-gardener-monitoring-test}"

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

run_collector_integration_test() {
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

  group "Deploy OpenTelemetry Collector"
  kubectl apply -f - <<EOF
apiVersion: opentelemetry.io/v1beta1
kind: OpenTelemetryCollector
metadata:
  name: gardener
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
  serviceMonitorSelector:
    matchLabels:
      app: gardener-collector
  serviceMonitorNamespaceSelector: {}
---
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: gardener-collector
  namespace: ${TEST_NAMESPACE}
  labels:
    app: gardener-collector
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: gardener-collector
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
    --for=create deployment/gardener-collector --timeout=5m
  kubectl rollout status --namespace "${TEST_NAMESPACE}" \
    deployment/gardener-collector --timeout=5m
  endgroup

  group "Wait for Prometheus instance to become Ready"
  kubectl wait --namespace "${TEST_NAMESPACE}" \
    --for=create statefulset/prometheus-gardener --timeout=5m
  kubectl rollout status --namespace "${TEST_NAMESPACE}" \
    statefulset/prometheus-gardener --timeout=5m
  endgroup

  group "Wait for Shoot metrics"
  kubectl wait --namespace "${TEST_NAMESPACE}" \
    --for=create service/prometheus-operated --timeout=5m

  kubectl port-forward --namespace "${TEST_NAMESPACE}" \
    service/prometheus-operated 9090:9090 >/dev/null 2>&1 &
  local port_forward_pid=$!

  echo "Polling Prometheus for garden_shoot_info metric..."
  local deadline=$((SECONDS + 300))
  local count=0
  while (( SECONDS < deadline )); do
    count=$(curl -fsSG "http://localhost:9090/api/v1/query" \
      --data-urlencode 'query=count(garden_shoot_info)' \
      | jq -r '.data.result[0].value[1] // "0"' 2>/dev/null || echo "0")
    if [[ "${count}" != "0" && -n "${count}" ]]; then
      echo "Found garden_shoot_info metric (count=${count})."
      break
    fi
    echo "garden_shoot_info not present yet; retrying in 10s..."
    sleep 10
  done

  kill "${port_forward_pid}" 2>/dev/null || true

  if [[ "${count}" == "0" || -z "${count}" ]]; then
    echo "Timed out waiting for garden_shoot_info metric" >&2
    return 1
  fi
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

  run_collector_integration_test "${collector_image}"
}

main "$@"
