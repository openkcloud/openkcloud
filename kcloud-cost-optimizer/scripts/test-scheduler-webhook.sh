#!/usr/bin/env bash
set -euo pipefail

KCL_OPTIMIZER_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${KCL_OPTIMIZER_ROOT}/scripts/go-env.sh"

KCL_SCHEDULER_ROOT="$(cd "${KCL_OPTIMIZER_ROOT}/../kcloud-cost-scheduler" && pwd)"
cd "${KCL_SCHEDULER_ROOT}"

go test ./pkg/webhook -v
