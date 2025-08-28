# Stage 1: Build the otel collector
FROM golang:1.25 AS builder

# Copy the source code
COPY . /src
WORKDIR /src

# Build otel-collector
ARG TARGETOS TARGETARCH
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH make build

# Stage 2: Create the final image
FROM gcr.io/distroless/static:nonroot AS collector

# Add container image labels
ARG BUILD_DATE
ARG EFFECTIVE_VERSION
ARG REVISION
LABEL org.opencontainers.image.title="Gardener OpenTelemetry Collector"
LABEL org.opencontainers.image.version="${EFFECTIVE_VERSION}"
LABEL org.opencontainers.image.description="An otel collector distribution for Gardener"
LABEL org.opencontainers.image.source="https://github.com/gardener/opentelemetry-collector"
LABEL org.opencontainers.image.created="${BUILD_DATE}"
LABEL org.opencontainers.image.revision="${REVISION}"
LABEL org.opencontainers.image.licenses="Apache-2.0"
LABEL org.opencontainers.image.authors="@gardener/opentelemetry-collector-maintainers"

# Copy the otel collector from the builder stage
COPY --from=builder /src/bin/otelcol /bin/otelcol

# Expose the collector ports
# 3301: Vali
# 4137, 4318: OTLP
EXPOSE 3301 4317 4318

# Command to run the otel collector
ENTRYPOINT ["/bin/otelcol"]
