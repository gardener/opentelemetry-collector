# Monitoring a Gardener Landscape

This document describes how to deploy an OpenTelemetry Collector with the
Gardener receiver to monitor an entire Gardener landscape.

## Required Permissions

The receiver only reads from the API server. The service-account used by the
collector must be allowed to `get`, `list`, and `watch` the resources selected
via `resources`, plus the `SecretBinding` and `CredentialsBinding` resources
when `shoots` are enabled (these are needed to resolve billing-relevant
attributes on shoot info metrics).

API groups touched:

- `core.gardener.cloud` (`shoots`, `seeds`, `projects`, `secretbindings`)
- `seedmanagement.gardener.cloud` (`managedseeds`, `gardenlets`)
- `security.gardener.cloud` (`credentialsbindings`)

## Collector Configuration

To monitor a whole landscape, a minimal collector configuration is sufficient.
The pipeline pairs a single `gardener` receiver with an `otlp` exporter that
forwards the metrics to a downstream collector or backend.

None of the receiver's options are strictly required — with an empty config it
watches every supported resource cluster-wide and authenticates via the
in-cluster service-account. The one setting that usually matters is
`kubeconfig`, which points the receiver at the virtual garden API server. It is
only optional when the collector runs inside the garden cluster with a suitable
service-account mounted; otherwise (falling back to `$KUBECONFIG`/
`~/.kube/config`) it should be set explicitly:

```yaml
receivers:
  gardener:
    kubeconfig: /var/run/secrets/gardener/virtual-garden/kubeconfig

exporters:
  otlp:
    endpoint: otel-collector.example.com:4317

service:
  pipelines:
    metrics:
      receivers: [gardener]
      exporters: [otlp]
```

All other options (`namespace`, `sync_period`, `collection_interval`,
`resources`) have sensible defaults; leaving `resources` unset watches all
supported resource types. See the [receiver README](../README.md) for the full
set of configuration options.
