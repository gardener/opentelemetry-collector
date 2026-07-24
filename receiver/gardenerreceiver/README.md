# Gardener Receiver

<!-- markdownlint-disable MD013 -->

| Status        |                                                                             |
|---------------|-----------------------------------------------------------------------------|
| Stability     | alpha: metrics                                                              |
| Distributions | [Gardener OpenTelemetry Collector Distro](../../README.md)                  |
| Issues        | [GitHub Issues](https://github.com/gardener/opentelemetry-collector/issues) |
| Code Owners   | see [`CODEOWNERS`](../../CODEOWNERS)                                        |

The Gardener receiver scrapes the Gardener API server and emits metrics about
Gardener resources — `Shoot`s, `Seed`s, `Project`s, `ManagedSeed`s, and
`Gardenlet`s. It is intended to be deployed against a Gardener virtual garden
cluster (or a development garden) to provide an observability view over the
landscape of managed Kubernetes clusters.

The receiver uses [Kubernetes shared informers] to watch the relevant Gardener
resources, keeps the objects in an in-memory cache, and periodically translates
the cache contents into OTLP metric data points. List/watch traffic against the
API server is therefore independent of the configured collection interval.

[Kubernetes shared informers]: https://pkg.go.dev/k8s.io/client-go/tools/cache#SharedIndexInformer

## Configuration

The receiver is configured under the `gardener` receiver key. All options have
sensible defaults; an empty configuration is valid and watches every supported
resource cluster-wide using the in-cluster `ServiceAccount`, or the kubeconfig
referenced by `$KUBECONFIG`/`~/.kube/config` when in-cluster configuration is
not available.

| Option                | Type            | Default                                               | Description                                                                                                                                                           |
|-----------------------|-----------------|-------------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `kubeconfig`          | string          | _empty_                                               | Path to a kubeconfig pointing at the Gardener (virtual garden) API server. If empty, uses in-cluster config first, then falls back to `$KUBECONFIG`/`~/.kube/config`. |
| `namespace`           | string          | _empty_ (all namespaces)                              | Restrict monitoring namespace scoped resources to this namespace.                                                                                                     |
| `sync_period`         | duration        | `1h`                                                  | Resync period passed to the underlying shared informer factories.                                                                                                     |
| `collection_interval` | duration        | `30s`                                                 | How often the receiver translates the informer caches into metrics. Must be `> 0`.                                                                                    |
| `resources`           | list of strings | `[shoots, seeds, projects, managedseeds, gardenlets]` | Resources to watch and emit metrics for. Allowed values: `shoots`, `seeds`, `projects`, `managedseeds`, `gardenlets`.                                                 |

### Example

```yaml
receivers:
  gardener:
    kubeconfig: /var/run/secrets/gardener/virtual-garden/kubeconfig
    collection_interval: 60s
    resources:
      - shoots
      - seeds
      - projects

exporters:
  otlp:
    endpoint: otel-collector.example.com:4317

service:
  pipelines:
    metrics:
      receivers:  [gardener]
      exporters:  [otlp]
```

## Emitted Metrics

All metrics are emitted under the instrumentation scope
`github.com/gardener/opentelemetry-collector/receiver/gardenerreceiver`.
Per-object metrics carry attributes identifying the underlying Gardener object
(e.g. `gardener.project.name`, `gardener.shoot.name`, `gardener.shoot.uid`,
`gardener.shoot.technical_id`, `gardener.seed.name`). The stable, globally
unique identifier for a shoot is `gardener.shoot.uid`.

### Per-object metrics

| Resource group | Metric name                               | Notes                                                                                                                         |
|----------------|-------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------|
| Shoots         | `garden.shoot.info`                       | Static metadata as attributes (provider, region, k8s version, purpose, failure tolerance, billing/cost-object attributes, …). |
| Shoots         | `garden.shoot.hibernated`                 | `1` if the shoot is hibernated, `0` otherwise.                                                                                |
| Shoots         | `garden.shoot.creation_timestamp`         | Unix timestamp of `metadata.creationTimestamp`.                                                                               |
| Shoots         | `garden.shoot.condition`                  | State metric over condition type: one data point per condition with value `1`; condition type/status/reason are attributes.   |
| Shoots         | `garden.shoot.status`                     | State metric over Shoot status: one data point per status with `1` for the current status and `0` for the others.             |
| Shoots         | `garden.shoot.operation_states`           | One data point per supported operation type; current operation is `1`, all other operation types are `0`.                     |
| Shoots         | `garden.shoot.operation_progress_percent` | Progress of the current operation in percent; non-current operation types are emitted with `0`.                               |
| Shoots         | `garden.shoot.operations_total`           | Current last-operation counts grouped by operation type/state, provider, seed, Kubernetes version, and region.                |
| Shoots         | `garden.shoot.worker.min` / `.worker.max` | Per-worker-pool size bounds.                                                                                                  |
| Shoots         | `garden.shoot.nodes.min` / `.nodes.max`   | Aggregate node-count bounds across all worker pools.                                                                          |
| Shoots         | `garden.shoot.node.info`                  | Per-worker-pool node metadata (machine type, image, architecture, CRI, container runtimes, …).                                |
| Seeds          | `garden.seed.info`                        | Static seed metadata.                                                                                                         |
| Seeds          | `garden.seed.capacity`                    | Capacity reported by the seed (e.g. shoots).                                                                                  |
| Seeds          | `garden.seed.usage`                       | Allocatable resources reported by the seed.                                                                                   |
| Seeds          | `garden.seed.condition`                   | One data point per seed condition with value `1`; condition type/status/reason are attributes.                                |
| Seeds          | `garden.seed.operation`                   | Current operation type/state of the seed (with `gardener.operation.progress`).                                                |
| Projects       | `garden.project.info`                     | Static project metadata.                                                                                                      |
| Projects       | `garden.users`                            | Total project member count grouped by user kind.                                                                              |
| ManagedSeeds   | `garden.managed_seed.info`                | Static managed seed metadata.                                                                                                 |
| Gardenlets     | `garden.gardenlet.condition`              | One data point per gardenlet condition with value `1`; condition type/status/reason are attributes.                           |
| Gardenlets     | `garden.gardenlet.generation`             | `metadata.generation` of the gardenlet.                                                                                       |
| Gardenlets     | `garden.gardenlet.observed_generation`    | `status.observedGeneration` of the gardenlet.                                                                                 |

### Landscape-level shoot aggregations

In addition to per-shoot metrics, the receiver emits a set of landscape-wide
counters that summarize how shoots across the garden are configured. These are
useful for tracking adoption of features and operational settings.

| Metric name                                                      | Description                                                                        |
|------------------------------------------------------------------|------------------------------------------------------------------------------------|
| `garden.shoots.hibernation.enabled_total`                        | Count of shoots with hibernation enabled.                                          |
| `garden.shoots.hibernation.schedule_total`                       | Count of shoots with a hibernation schedule configured.                            |
| `garden.shoots.maintenance.window_total`                         | Count of shoots with a maintenance window configured.                              |
| `garden.shoots.maintenance.autoupdate.k8s_version_total`         | Count of shoots with auto-update for Kubernetes versions configured.               |
| `garden.shoots.maintenance.autoupdate.image_version_total`       | Count of shoots with auto-update for machine image versions configured.            |
| `garden.shoots.custom.worker.multiple_pools_total`               | Count of shoots with multiple worker pools.                                        |
| `garden.shoots.custom.worker.multi_zones_total`                  | Count of shoots with multi-zone worker pools.                                      |
| `garden.shoots.custom.worker.taints_total`                       | Count of shoots with worker-pool taints.                                           |
| `garden.shoots.custom.worker.labels_total`                       | Count of shoots with worker-pool labels.                                           |
| `garden.shoots.custom.worker.annotations_total`                  | Count of shoots with worker-pool annotations.                                      |
| `garden.shoots.custom.network.custom_domain_total`               | Count of shoots which use a custom DNS domain.                                     |
| `garden.shoots.custom.apiserver.audit_policy_total`              | Count of shoots with an audit log policy configured on the kube-apiserver.         |
| `garden.shoots.custom.apiserver.structured_authentication_total` | Count of shoots with structured authentication configured on the kube-apiserver.   |
| `garden.shoots.custom.kcm.node_cidr_mask_size_total`             | Count of shoots with a node CIDR mask size configured on the KCM.                  |
| `garden.shoots.custom.kcm.horizontal_pod_autoscale_total`        | Count of shoots with HPA configuration on the KCM.                                 |
| `garden.shoots.custom.kubelet.pod_pid_limit_total`               | Count of shoots with a pod PID limit configured on the kubelet(s).                 |
| `garden.shoots.custom.extensions_total`                          | Per-extension count, labeled by `gardener.extension.type`.                         |
| `garden.shoots.custom.apiserver.feature_gates_total`             | Per-feature-gate count for the kube-apiserver, labeled by `gardener.feature_gate`. |
| `garden.shoots.custom.apiserver.admission_plugins_total`         | Per-admission-plugin count, labeled by `gardener.admission_plugin`.                |
| `garden.shoots.custom.kcm.feature_gates_total`                   | Per-feature-gate count for the kube-controller-manager.                            |
| `garden.shoots.custom.scheduler.feature_gates_total`             | Per-feature-gate count for the kube-scheduler.                                     |
| `garden.shoots.custom.proxy.mode_total`                          | Count of shoots by kube-proxy mode, labeled by `gardener.proxy.mode`.              |

## Deploying an OpenTelemetry Collector with the Gardener Receiver

Deployment instructions, including the required permissions, are described in
separate documents depending on the monitoring scope:

- [Monitoring a Gardener landscape](docs/monitoring-a-landscape.md)
- [Monitoring a Gardener project](docs/monitoring-a-project.md)

## Information for Developers

The receiver is a standalone Go module wired into the distribution via the
`replace` directive in the top-level [`manifest.yml`](../../manifest.yml), so
local edits are picked up by a fresh distribution build without any publishing
step.

The Makefile in this directory delegates to
[`Makefile.Common`](../../Makefile.Common). All tools (`golangci-lint`,
`gotestsum`, `gosec`, `goimports`, `gci`, `addlicense`) are fetched on demand
via `go tool` — no manual installation is required.

### Make targets

Run from this directory:

| Target                       | What it does                                                                                                        |
|------------------------------|---------------------------------------------------------------------------------------------------------------------|
| `make` / `make all`          | Full pre-commit pipeline: `go-generate`, `tidy`, `go-fmt`, `go-lint`, `goimports`, `check-license-headers`, `test`. |
| `make test`                  | `tidy` + `go-lint` + `check-license-headers`, then `gotestsum --packages=./...` with shuffled tests.                |
| `make go-lint`               | `golangci-lint run` with the repo-wide config and `integration` build tag.                                          |
| `make go-check`              | `tidy` + `go-lint` + `gosec` + `check-license-headers`.                                                             |
| `make tidy`                  | `go mod tidy`.                                                                                                      |
| `make go-fmt`                | `gofmt -l -w .`.                                                                                                    |
| `make goimports`             | `goimports -w` and `gci write` with the project import grouping.                                                    |
| `make go-generate`           | `go generate ./...` followed by `gofmt`.                                                                            |
| `make gosec`                 | `gosec` security scan, non-failing, output to stdout.                                                               |
| `make gosec-report`          | Same scan, written to `gosec-report.sarif`.                                                                         |
| `make check-license-headers` | Verify SPDX headers on all `.go` and `.sh` files; fails if any are missing.                                         |
| `make add-license-headers`   | Add missing SPDX headers in place.                                                                                  |

### Building the distribution

The receiver is included by default in the Gardener OpenTelemetry Collector
distribution. From the repository root:

```bash
make build         # produces ./bin/otelcol
make docker-image  # produces a container image
```
