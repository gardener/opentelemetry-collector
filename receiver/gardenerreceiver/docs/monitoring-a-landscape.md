# Monitoring a Gardener Landscape

This document describes how to deploy an OpenTelemetry Collector with the
Gardener receiver to monitor an entire Gardener landscape.

## Required Permissions

The receiver only reads from the API server. The `ServiceAccount` used by the
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

The receiver needs a `kubeconfig`, which points to the virtual garden API server.

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
