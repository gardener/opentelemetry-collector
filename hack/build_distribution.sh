#!/usr/bin/env bash
# Copyright 2025 SAP SE or an SAP affiliate company and Gardener contributors
# SPDX-License-Identifier: Apache-2.0


set -e

REPO_DIR="$( cd "$(dirname "$( dirname "${BASH_SOURCE[0]}" )")" &> /dev/null && pwd )"
BIN_DIR="${REPO_DIR}/bin"
COLLECTOR_NAME=${1:-"control-plane"}
LD_FLAGS=${2:-"-s -w"}

# Build the Control Plane Collector
pushd "${REPO_DIR}/_build/opentelemetry-collector-${COLLECTOR_NAME}" > /dev/null || exit

go mod download && go mod tidy
GOOS=$(go env GOOS) GOARCH=$(go env GOARCH) CGO_ENABLED=0 GO111MODULE=on \
    go build -ldflags "${LD_FLAGS}" -o ${BIN_DIR}/${COLLECTOR_NAME} .

popd > /dev/null || exit
