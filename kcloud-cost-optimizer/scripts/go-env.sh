#!/usr/bin/env bash
# Project-local Go 1.24.5 environment for WSL2.
# Usage: source scripts/go-env.sh

KCL_GO_PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

export GOROOT="${KCL_GO_PROJECT_ROOT}/.tools/go"
export GOPATH="${KCL_GO_PROJECT_ROOT}/.cache/go-path"
export GOMODCACHE="${KCL_GO_PROJECT_ROOT}/.cache/go-mod"
export GOCACHE="${KCL_GO_PROJECT_ROOT}/.cache/go-build"
export GOTOOLCHAIN="local"
export PATH="${GOROOT}/bin:${GOPATH}/bin:${PATH}"

unset KCL_GO_PROJECT_ROOT
