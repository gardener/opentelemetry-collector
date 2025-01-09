#!/usr/bin/env bash

set -e

REPO_DIR="$( cd "$(dirname "$( dirname "${BASH_SOURCE[0]}" )")" &> /dev/null && pwd )"
LD_FLAGS=$@
BIN_DIR="${REPO_DIR}/bin"
NAME="otelcol"

# Build the distribution
pushd "${REPO_DIR}/_build" > /dev/null || exit
go mod download && go mod tidy
CGO_ENABLED=0 go build -ldflags "${LD_FLAGS}" -o ${BIN_DIR}/${NAME} .
popd > /dev/null || exit