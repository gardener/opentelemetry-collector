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

# Poll Prometheus (assumed reachable at localhost:9090) until the given
# count() query returns a non-zero result or the timeout elapses.
poll_prometheus_metric() {
  local description="$1"
  local query="$2"

  echo "Polling Prometheus for ${description}..."
  local deadline=$((SECONDS + 300))
  local count=0
  while (( SECONDS < deadline )); do
    count=$(curl -fsSG "http://localhost:9090/api/v1/query" \
      --data-urlencode "query=${query}" \
      | jq -r '.data.result[0].value[1] // "0"' 2>/dev/null || echo "0")
    if [[ "${count}" != "0" && -n "${count}" ]]; then
      echo "Found ${description} (count=${count})."
      return 0
    fi
    echo "${description} not present yet; retrying in 10s..."
    sleep 10
  done

  echo "Timed out waiting for ${description}" >&2
  return 1
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

  group "Wait for Shoot metrics"
  kubectl wait --namespace "${TEST_NAMESPACE}" \
    --for=create service/prometheus-operated --timeout=5m

  kubectl port-forward --namespace "${TEST_NAMESPACE}" \
    service/prometheus-operated 9090:9090 >/dev/null 2>&1 &
  local port_forward_pid=$!

  local scrape_rc=0 otlp_rc=0

  # Scraped via the Prometheus exporter + ServiceMonitor: Prometheus-style
  # name (dots become underscores).
  poll_prometheus_metric "garden_shoot_info metric (scraped)" \
    'count(garden_shoot_info)' || scrape_rc=$?

  # Pushed via the second collector's otlphttp exporter into Prometheus' OTLP
  # receiver with NoTranslation: raw OpenTelemetry name (dots preserved).
  poll_prometheus_metric "garden.shoot.info metric (OTLP)" \
    'count({__name__="garden.shoot.info"})' || otlp_rc=$?

  kill "${port_forward_pid}" 2>/dev/null || true

  if (( scrape_rc != 0 || otlp_rc != 0 )); then
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

  run_collector_e2e_test "${collector_image}"
}

main "$@"
