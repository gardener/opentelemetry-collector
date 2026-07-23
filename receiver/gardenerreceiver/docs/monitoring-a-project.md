# Monitoring a Gardener Project

This document describes how to deploy an OpenTelemetry Collector with the
Gardener receiver to monitor the resources of a single Gardener project. The
examples below describe the deployment of an OpenTelemetry Collector to a Kubernetes
cluster.

## Required Permissions

The receiver only reads from the API server. When only the resources of a
specific project should be monitored, the Gardener
[`viewer` role](https://gardener.cloud/docs/gardener/project/projects/) grants
the collector's `ServiceAccount` read access to the project's resources.

## Prerequisites

* A Kubernetes cluster to deploy the OpenTelemetry Collector to.
* A `ServiceAccount` for a Gardener project with the `viewer` role.
* Installed Helm CLI

## Monitoring a Gardener Project with an OTLP Backend

This section walks through a full deployment that forwards the project's
Gardener metrics to an OTLP backend. The collector is managed by the
[OpenTelemetry Operator], which is installed from its upstream Helm chart and
reconciles an `OpenTelemetryCollector` custom resource into a running collector
`Deployment`.

[OpenTelemetry Operator]: https://opentelemetry.io/docs/platforms/kubernetes/operator/

### 1. Deploy the OpenTelemetry Operator

Add the upstream Helm repository and install the operator. The operator also
requires TLS certificates for its admission webhooks; if
[cert-manager](https://cert-manager.io/) is not available in the cluster, let
the chart generate a self-signed certificate instead:

```bash
helm repo add open-telemetry https://open-telemetry.github.io/opentelemetry-helm-charts
helm repo update

helm install opentelemetry-operator open-telemetry/opentelemetry-operator \
  --namespace opentelemetry-operator-system --create-namespace \
  --set admissionWebhooks.certManager.enabled=false \
  --set admissionWebhooks.autoGenerateCert.enabled=true
```

### 2. Create the viewer kubeconfig secret

Create the namespace for the collector and store the project viewer kubeconfig
in a secret. The collector reads the kubeconfig from a file, so the secret is
later mounted into the collector pod.

```bash
kubectl create namespace gardener-monitoring 

kubectl create secret generic gardener-viewer-kubeconfig \
  --namespace gardener-monitoring \
  --from-file=kubeconfig=<PATH_TO_VIEWER_KUBECONFIG>
```

### 3. Deploy the OpenTelemetry Collector

Apply an `OpenTelemetryCollector` resource. It mounts the kubeconfig secret,
points the receiver at it via the `kubeconfig` option, and restricts the
`Shoot` watches to the project namespace via the `namespace` option. Only the
`shoots` resource is enabled here; the project-scoped `viewer` role does not
grant access to the cluster-scoped resources (`seeds`, `managedseeds`,
`gardenlets`).

```yaml
apiVersion: opentelemetry.io/v1beta1
kind: OpenTelemetryCollector
metadata:
  name: gardener
  namespace: gardener-monitoring
spec:
  image: europe-docker.pkg.dev/gardener-project/snapshots/gardener/otel/opentelemetry-collector:latest
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
        namespace: garden-<PROJECT>
        resources:
          - shoots
    exporters:
      otlp:
        endpoint: <OTLP_BACKEND_ENDPOINT>
    service:
      pipelines:
        metrics:
          receivers: [gardener]
          exporters: [otlp]
```

Save the manifest (for example as `collector.yaml`) and apply it:

```bash
kubectl apply -f collector.yaml
```

The operator reconciles the resource into a collector `Deployment`. Verify that
it is running.

If your OTLP backend requires authentication, please consult the
[OpenTelemetry Collector documentation](https://opentelemetry.io/docs/collector/configuration/#authentication)
for the appropriate configuration.

## Monitor a Gardener Project with Prometheus

This section deploys the same `gardener` receiver, but exports the metrics via a
`prometheus` exporter that is scraped by a Prometheus instance managed by the
[Prometheus Operator]. A `ServiceMonitor` connects the two.

[Prometheus Operator]: https://prometheus-operator.dev/

### 1. Deploy the OpenTelemetry Operator

Deploy the OpenTelemetry Operator and create the viewer kubeconfig secret as
described in steps 1 and 2 of
[Monitoring a Gardener Project with an OTLP Backend](#monitoring-a-gardener-project-with-an-otlp-backend).

### 2. Deploy the OpenTelemetry Collector

Apply an `OpenTelemetryCollector` resource with a `prometheus` exporter instead
of the `otlp` exporter. The exporter listens on port `8889`; the `ports` entry
tells the operator to expose that port on the generated `gardener-collector`
`Service` under the name `prometheus`, which the `ServiceMonitor` targets below.
As in the OTLP example, `resources` is restricted to `shoots` so the receiver
only starts project-namespaced shoot and binding watches.

```yaml
apiVersion: opentelemetry.io/v1beta1
kind: OpenTelemetryCollector
metadata:
  name: gardener
  namespace: gardener-monitoring
spec:
  image: europe-docker.pkg.dev/gardener-project/snapshots/gardener/otel/opentelemetry-collector:latest
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
        namespace: garden-<PROJECT>
        resources:
          - shoots
    exporters:
      prometheus:
        endpoint: 0.0.0.0:8889
    service:
      pipelines:
        metrics:
          receivers: [gardener]
          exporters: [prometheus]
```

### 3. Deploy the Prometheus Operator

Install the Prometheus Operator from the `kube-prometheus-stack` Helm chart.
The chart bundles a full monitoring stack, all unnecessary components are disabled
here:

```bash
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update

helm install prometheus-operator prometheus-community/kube-prometheus-stack \
  --namespace monitoring --create-namespace \
  --set prometheus.enabled=false \
  --set alertmanager.enabled=false \
  --set grafana.enabled=false \
  --set defaultRules.enabled=false \
  --set kubeApiServer.enabled=false \
  --set kubeControllerManager.enabled=false \
  --set kubeScheduler.enabled=false \
  --set kubeProxy.enabled=false \
  --set kubeEtcd.enabled=false \
  --set kubeStateMetrics.enabled=false \
  --set nodeExporter.enabled=false \
  --set kubelet.enabled=false \
  --set coreDns.enabled=false \
  --set kubeDns.enabled=false
```

### 4. Create a Prometheus instance and ServiceMonitor

Deploy a `Prometheus` instance with the required RBAC and a `ServiceMonitor`
that scrapes the collector's `prometheus` port. The `Prometheus` instance runs
under a dedicated `ServiceAccount`.

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: prometheus
  namespace: gardener-monitoring
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: prometheus
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
  name: prometheus
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: prometheus
subjects:
  - kind: ServiceAccount
    name: prometheus
    namespace: gardener-monitoring
---
apiVersion: monitoring.coreos.com/v1
kind: Prometheus
metadata:
  name: gardener
  namespace: gardener-monitoring
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
  namespace: gardener-monitoring
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
```

Once reconciled, the Gardener metrics are available in the Prometheus instance.
