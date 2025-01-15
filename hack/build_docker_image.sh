
#!/usr/bin/env bash
# Copyright 2025 SAP SE or an SAP affiliate company and Gardener contributors
# SPDX-License-Identifier: Apache-2.0

set -e

function effective_version() {
    local version
    local revision
    local dirty

    version=$(cat $REPO_DIR/VERSION)
    revision=$(git rev-parse --short HEAD)
    dirty=$(test -n "$(git status --porcelain)" && echo "-dirty" || true)

    echo "${version}-${revision}${dirty}"
}

REPO_DIR="$( cd "$(dirname "$( dirname "${BASH_SOURCE[0]}" )")" &> /dev/null && pwd )"

IMAGE_REPOSITORY=${1:-"europe-docker.pkg.dev/gardener-project/snapshots/gardener/otel/opentelemetry-collector"}
EFFECTIVE_VERSION=${2:-$(effective_version)}
LD_FLAGS=${3:-"-s -w"}

printf "Building image %s:%s\n" "${IMAGE_REPOSITORY}" "${EFFECTIVE_VERSION}"

BUILDER_VERSION=$(go list \
    -modfile="${REPO_DIR}/internal/tools/go.mod" \
    -mod=mod -f '{{ .Version }}' \
    -m go.opentelemetry.io/collector/cmd/builder)

printf "Using builder version %s\n" "${BUILDER_VERSION}"

docker build \
    --build-arg BUILD_DATE=$(date -u +'%Y-%m-%dT%H:%M:%SZ') \
    --build-arg BUILDER_VERSION="${BUILDER_VERSION}" \
    --build-arg EFFECTIVE_VERSION="${EFFECTIVE_VERSION}" \
    --build-arg LD_FLAGS="${LD_FLAGS}" \
    --build-arg REVISION=$(git rev-parse HEAD) \
    -t "${IMAGE_REPOSITORY}:${EFFECTIVE_VERSION}" \
    -t "${IMAGE_REPOSITORY}:latest" \
    -f "${REPO_DIR}/Dockerfile" \
    "${REPO_DIR}"