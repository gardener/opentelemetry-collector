# Stage 1: Build the otel collector
FROM golang:1.23 AS builder

# Copy the source code
COPY . /src
WORKDIR /src

# Install otel collector builder
ARG BUILDER_VERSION=latest
RUN go install go.opentelemetry.io/collector/cmd/builder@${BUILDER_VERSION}

# Build the control plane distribution
ARG LD_FLAGS="-s -w"
RUN mkdir -p ./_build/opentelemetry-collector-control-plane
RUN /go/bin/builder --skip-get-modules --skip-compilation --config collector-control-plane/manifest.yml

WORKDIR /src/_build/opentelemetry-collector-control-plane
RUN go mod download && go mod tidy
ARG TARGETOS
ARG TARGETARCH
RUN GO111MODULE=on CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags "${LD_FLAGS}" -o otelcol .

#Build the log shipper distribution
WORKDIR /src
RUN mkdir -p ./_build/opentelemetry-collector-log-shipper
RUN /go/bin/builder --skip-get-modules --skip-compilation --config collector-log-shipper/manifest.yml

WORKDIR /src/_build/opentelemetry-collector-log-shipper
RUN go mod download && go mod tidy
ARG TARGETOS
ARG TARGETARCH
RUN GO111MODULE=on CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags "${LD_FLAGS}" -o otelcol .

# Stage 2: Create the final images

# Create the control plane image
FROM gcr.io/distroless/static:nonroot AS control-plane

# Add container image labels
ARG BUILD_DATE
ARG EFFECTIVE_VERSION
ARG REVISION
LABEL org.opencontainers.image.title="Gardener Opentelemetry Collector"
LABEL org.opencontainers.image.version="${EFFECTIVE_VERSION}"
LABEL org.opencontainers.image.description="A otel collector distribution for Gardener"
LABEL org.opencontainers.image.source="https://github.com/gardener/opentelemetry-collector"
LABEL org.opencontainers.image.created="${BUILD_DATE}"
LABEL org.opencontainers.image.revision="${REVISION}"
LABEL org.opencontainers.image.licenses="Apache-2.0"
LABEL org.opencontainers.image.authors="@gardener/opentelemetry-collector-maintainers"

# Copy the otel collector from the builder stage
COPY --from=builder /src/_build/opentelemetry-collector-control-plane/otelcol /bin/otelcol

# Expose the collector ports
# 3301: Vali
# 4137, 4318: OTLP
EXPOSE 3301 4317 4318

# Command to run the otel collector
ENTRYPOINT ["/bin/otelcol"]

# Create the log shipper image
FROM gcr.io/distroless/static:nonroot AS log-shipper

# Add container image labels
ARG BUILD_DATE
ARG EFFECTIVE_VERSION
ARG REVISION
LABEL org.opencontainers.image.title="Gardener Opentelemetry Collector"
LABEL org.opencontainers.image.version="${EFFECTIVE_VERSION}"
LABEL org.opencontainers.image.description="A otel collector distribution for Gardener"
LABEL org.opencontainers.image.source="https://github.com/gardener/opentelemetry-collector"
LABEL org.opencontainers.image.created="${BUILD_DATE}"
LABEL org.opencontainers.image.revision="${REVISION}"
LABEL org.opencontainers.image.licenses="Apache-2.0"
LABEL org.opencontainers.image.authors="@gardener/opentelemetry-collector-maintainers"

# Copy the otel collector from the builder stage
COPY --from=builder /src/_build/opentelemetry-collector-log-shipper/otelcol /bin/otelcol

# Command to run the otel collector
ENTRYPOINT ["/bin/otelcol"]
