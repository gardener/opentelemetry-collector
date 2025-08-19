# Gardener Opentelemetry-Collector Distro

[![reuse compliant](https://reuse.software/badge/reuse-compliant.svg)](https://reuse.software/)

Before continuing, you can familiarize yourself with the [OpenTelemetry project](https://opentelemetry.io/), which is a set of APIs, SDKs, and tools that can be used to instrument applications for observability.

Gardener currently leverages the OpenTelemetryCollector binary in the control-plane of `Shoot` clusters and as a log shipper
on the `Shoot` nodes themselves. More use cases are planned in the future.

Since the OpenTelemetry Collector uses a modular architecture, it can be built for specific use cases by selecting only the necessary components.
This repository takes care of defining listing these components for the Gardener use cases and 
managing the build and upload steps of the resulting distribution.

This opentelemetry distribution combines components from [Opentelemetry Collector Core](https://github.com/open-telemetry/opentelemetry-collector) and [Opentelemetry Collector Contrib](https://github.com/open-telemetry/opentelemetry-collector-contrib).
The complete list of components can be found in [manifest.yml](/manifest.yml)

## Manifest

The `manifest.yaml` describes all the necessary components that the OpenTelemetry Collector distribution should include.
They are grouped into receivers, processors, exporters, extensions and connector. Each is defined with its name and version.
For more details, consult the [OpenTelemetry Collector documentation](https://opentelemetry.io/docs/collector/).

## Usage

The repository depends on the [opentelemetry-collector-builder](https://github.com/open-telemetry/opentelemetry-collector/tree/main/cmd/builder) tool to build
the aforementioned distribution. This can easily be done by using the predefined [make targets](./Makefile):
```bash
make build
```
This will:
 - Download necessary tools in a `_tools/` directory (Including the opentelemetry-collector-builder tool)
 - Call the build tool with the `manifest.yaml` file to build the distribution in a `_build/` directory
 - Download all dependencies of the generated code in `_build` by calling `hack/build_distribution.sh`

## SAST Reporting

Since this repository contains no code of its own, but only a manifest file, an SAST report
can be created only on the generated code in the `_build` directory.

This can be done by running the following command:
```bash
make go-sec-report-build
```

This will generate a SAST report after going through the build process and will output the report in the `_build/gosec-report.sarif` file.

## Container Image

Images of the OpenTelemetry Collector distribution can be built by calling the `docker-image` target:
```bash
make docker-image
```
