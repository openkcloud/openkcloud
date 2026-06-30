#!/usr/bin/env bash
# collect-node-facts.sh — comprehensive read-only fact collection per node.
# Writes per-probe files and an aggregated report.txt per node.
#
# SAFETY CONTRACT:
#   - BatchMode=yes: no passwords, no keyboard interaction
#   - All remote commands are read-only (cat, uname, lscpu, free, df, ip,
#     timedatectl, sudo -n true probe, lspci|grep, command -v)
#   - No writes to remote hosts; artifacts saved locally only
#   - Idempotent: each run creates a new timestamped directory
#
# OUTPUT:
#   artifacts/node-discovery/<timestamp>/<node>/
#     status.txt         — reachable | unreachable
#     os-release.txt     — /etc/os-release contents
#     kernel.txt         — uname -r output
#     lscpu.txt          — CPU topology
#     memory.txt         — free -h output
#     disk.txt           — df -h output
#     network.txt        — ip -br addr output
#     time.txt           — timedatectl output
#     sudo-probe.txt     — sudo -n true exit code
#     accelerators.txt   — lspci nvidia/furiosa hits
#     runtimes.txt       — containerd/kubelet/kubectl presence
#     report.txt         — aggregated single-file summary
#
# USAGE: bash infra/scripts/collect-node-facts.sh
#
# shellcheck shell=bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
TIMESTAMP="$(date +%Y%m%dT%H%M%S)"
OUTDIR="${REPO_ROOT}/artifacts/node-discovery/${TIMESTAMP}"

mkdir -p "${OUTDIR}"

# ssh_probe <ip> <port> <remote_command>
ssh_probe() {
  local ip="$1" port="$2"
  shift 2
  ssh \
    -o BatchMode=yes \
    -o ConnectTimeout=5 \
    -o StrictHostKeyChecking=no \
    -o LogLevel=ERROR \
    -p "${port}" \
    "kcloud@${ip}" \
    "$@"
}

# ssh_reachable <ip> <port>
ssh_reachable() {
  local ip="$1" port="$2"
  ssh \
    -o BatchMode=yes \
    -o ConnectTimeout=5 \
    -o StrictHostKeyChecking=no \
    -o LogLevel=ERROR \
    -p "${port}" \
    "kcloud@${ip}" \
    'echo ok' > /dev/null 2>&1
}

# collect <label> <outfile> <ip> <port> <remote_command>
collect() {
  local label="$1" outfile="$2" ip="$3" port="$4"
  shift 4
  if ssh_probe "${ip}" "${port}" "$@" > "${outfile}" 2>&1; then
    printf '  [ok] %s\n' "${label}"
  else
    printf '  [warn] %s: non-zero exit\n' "${label}"
  fi
}

NODES=(
  "node1 192.0.2.41  122"
  "node2 192.0.2.195 122"
  "node3 192.0.2.196 122"
  "node4 192.0.2.114 22"
  "node5 192.0.2.111 122"
)

COLLECTED=0
SKIPPED=0

for entry in "${NODES[@]}"; do
  read -r name ip port <<< "${entry}"
  printf '\n=== %s (%s:%s) ===\n' "${name}" "${ip}" "${port}"
  node_dir="${OUTDIR}/${name}"
  mkdir -p "${node_dir}"

  if ! ssh_reachable "${ip}" "${port}"; then
    printf '  [skip] SSH unreachable\n'
    printf 'unreachable\n' > "${node_dir}/status.txt"
    (( SKIPPED++ )) || true
    continue
  fi
  printf 'reachable\n' > "${node_dir}/status.txt"
  printf '  [ok] SSH reachable\n'

  collect "os-release"   "${node_dir}/os-release.txt"  "${ip}" "${port}" \
    'cat /etc/os-release'

  collect "kernel"       "${node_dir}/kernel.txt"       "${ip}" "${port}" \
    'uname -r'

  collect "lscpu"        "${node_dir}/lscpu.txt"        "${ip}" "${port}" \
    'lscpu'

  collect "memory"       "${node_dir}/memory.txt"       "${ip}" "${port}" \
    'free -h'

  collect "disk"         "${node_dir}/disk.txt"         "${ip}" "${port}" \
    'df -h'

  collect "network"      "${node_dir}/network.txt"      "${ip}" "${port}" \
    'ip -br addr'

  collect "timedatectl"  "${node_dir}/time.txt"         "${ip}" "${port}" \
    'timedatectl'

  # sudo capability probe: exit code only, no elevated commands executed
  collect "sudo-probe"   "${node_dir}/sudo-probe.txt"   "${ip}" "${port}" \
    'sudo -n true 2>&1; printf "sudo_rc=%s\n" "$?"'

  # PCI accelerator enumeration — read-only lspci + grep (NVIDIA / Furiosa / Rebellions)
  collect "accelerators" "${node_dir}/accelerators.txt" "${ip}" "${port}" \
    'lspci 2>/dev/null | grep -iE "nvidia|furiosa|rebellions|rbln" || printf "no-accelerator-found\n"'

  # Container runtime and Kubernetes binary presence
  collect "runtimes"     "${node_dir}/runtimes.txt"     "${ip}" "${port}" \
    'for b in containerd kubelet kubectl; do
       if command -v "$b" > /dev/null 2>&1; then
         printf "%s=%s\n" "$b" "$(command -v "$b")"
       else
         printf "%s=not-found\n" "$b"
       fi
     done'

  # Aggregate all probes into a single report file
  {
    printf 'node: %s\nip: %s\nport: %s\ntimestamp: %s\n' \
      "${name}" "${ip}" "${port}" "${TIMESTAMP}"
    for label in os-release kernel lscpu memory disk network time \
                 sudo-probe accelerators runtimes; do
      local_file="${node_dir}/${label}.txt"
      # use 'time' file for timedatectl probe
      if [[ "${label}" == "time" ]]; then
        local_file="${node_dir}/time.txt"
      fi
      printf '\n--- %s ---\n' "${label}"
      if [[ -f "${local_file}" ]]; then
        cat "${local_file}"
      else
        printf '(not collected)\n'
      fi
    done
  } > "${node_dir}/report.txt"

  printf '  report: %s/report.txt\n' "${node_dir}"
  (( COLLECTED++ )) || true
done

printf '\nSummary: collected=%d skipped=%d\n' "${COLLECTED}" "${SKIPPED}"
printf 'Artifacts: %s/\n' "${OUTDIR}"
