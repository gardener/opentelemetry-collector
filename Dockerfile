# Stage 1: Build the otel collector
FROM golang:1.23 AS builder

# Copy the source code
COPY . /src
WORKDIR /src

# Install otel collector builder
ARG BUILDER_VERSION=latest
RUN go install go.opentelemetry.io/collector/cmd/builder@${BUILDER_VERSION}

# Build the otel collector distribution
ARG LD_FLAGS="-s -w"
RUN /go/bin/builder --skip-get-modules --skip-compilation --config manifest.yml

WORKDIR /src/_build
RUN go mod download && go mod tidy
ARG TARGETOS
ARG TARGETARCH
RUN GO111MODULE=on CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags "${LD_FLAGS}" -o otelcol .

# Stage 2: Create the final image
FROM gcr.io/distroless/static:nonroot

# Add container image labels
ARG BUILD_DATE
ARG EFFECTIVE_VERSION
LABEL org.opencontainers.image.title="Gardener Opentelemetry Collector"
LABEL org.opencontainers.image.version="${EFFECTIVE_VERSION}"
LABEL org.opencontainers.image.description="A otel collector distribution for Gardener"
LABEL org.opencontainers.image.source="https://github.com/gardener/opentelemetry-collector"
LABEL org.opencontainers.image.created="${BUILD_DATE}"
LABEL org.opencontainers.image.revision="git-commit-sha"
LABEL org.opencontainers.image.licenses="Apache-2.0"
LABEL org.opencontainers.image.authors="@gardener/opentelemetry-collector-maintainers"

# Copy the otel collector from the builder stage
COPY --from=builder /src/_build/otelcol /bin/otelcol

# Expose the collector ports
# 3301: Vali
# 4137, 4318: OTLP
EXPOSE 3301 4317 4318

# Command to run the otel collector
ENTRYPOINT ["/bin/otelcol"]
