#!/usr/bin/env bash
# discover-nodes.sh — collect read-only OS and hardware facts from live cluster nodes.
#
# SAFETY CONTRACT:
#   - BatchMode=yes: no passwords, no keyboard interaction
#   - All remote commands are read-only probes (cat, uname, lscpu, free, df, ip, timedatectl)
#   - sudo -n true: capability probe only; no elevated commands run
#   - lspci | grep: read-only PCI enumeration
#   - command -v: binary presence check only
#   - No writes to remote hosts; all output saved locally
#
# OUTPUT: artifacts/node-discovery/<timestamp>/<node>/{status,os-release,kernel,
#         lscpu,memory,disk,network,time,sudo-probe,accelerators,runtimes}.txt
#
# USAGE: bash infra/scripts/discover-nodes.sh
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

# ssh_reachable <ip> <port> — returns 0 if SSH succeeds
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

# Live cluster nodes only — candidate nodes skipped (powered off / firewalled)
# format: "name ip port"
NODES=(
  "node1 192.0.2.41  122"
  "node2 192.0.2.195 122"
  "node3 192.0.2.196 122"
  "node4 192.0.2.114 22"
  "node5 192.0.2.111 122"
)

for entry in "${NODES[@]}"; do
  read -r name ip port <<< "${entry}"
  printf '\n=== %s (%s:%s) ===\n' "${name}" "${ip}" "${port}"
  node_dir="${OUTDIR}/${name}"
  mkdir -p "${node_dir}"

  if ! ssh_reachable "${ip}" "${port}"; then
    printf '  [skip] SSH unreachable\n'
    printf 'unreachable\n' > "${node_dir}/status.txt"
    continue
  fi
  printf 'reachable\n' > "${node_dir}/status.txt"
  printf '  [ok] SSH reachable\n'

  # OS identity
  if ssh_probe "${ip}" "${port}" 'cat /etc/os-release' \
      > "${node_dir}/os-release.txt" 2>&1; then
    printf '  [ok] os-release\n'
  else
    printf '  [warn] os-release: probe returned non-zero\n'
  fi

  # Kernel version
  if ssh_probe "${ip}" "${port}" 'uname -r' \
      > "${node_dir}/kernel.txt" 2>&1; then
    printf '  [ok] kernel\n'
  else
    printf '  [warn] kernel: probe returned non-zero\n'
  fi

  # CPU topology
  if ssh_probe "${ip}" "${port}" 'lscpu' \
      > "${node_dir}/lscpu.txt" 2>&1; then
    printf '  [ok] lscpu\n'
  else
    printf '  [warn] lscpu: probe returned non-zero\n'
  fi

  # Memory
  if ssh_probe "${ip}" "${port}" 'free -h' \
      > "${node_dir}/memory.txt" 2>&1; then
    printf '  [ok] memory\n'
  else
    printf '  [warn] memory: probe returned non-zero\n'
  fi

  # Disk layout
  if ssh_probe "${ip}" "${port}" 'df -h' \
      > "${node_dir}/disk.txt" 2>&1; then
    printf '  [ok] disk\n'
  else
    printf '  [warn] disk: probe returned non-zero\n'
  fi

  # Network interfaces
  if ssh_probe "${ip}" "${port}" 'ip -br addr' \
      > "${node_dir}/network.txt" 2>&1; then
    printf '  [ok] network\n'
  else
    printf '  [warn] network: probe returned non-zero\n'
  fi

  # Time / timezone
  if ssh_probe "${ip}" "${port}" 'timedatectl' \
      > "${node_dir}/time.txt" 2>&1; then
    printf '  [ok] timedatectl\n'
  else
    printf '  [warn] timedatectl: probe returned non-zero\n'
  fi

  # Sudo capability probe — exit code check only; no elevated commands run
  if ssh_probe "${ip}" "${port}" \
      'sudo -n true 2>&1; printf "sudo_rc=%s\n" "$?"' \
      > "${node_dir}/sudo-probe.txt" 2>&1; then
    printf '  [ok] sudo-probe\n'
  else
    printf '  [warn] sudo-probe: probe returned non-zero\n'
  fi

  # PCI accelerator enumeration (NVIDIA / Furiosa / Rebellions Atom+)
  if ssh_probe "${ip}" "${port}" \
      'lspci 2>/dev/null | grep -iE "nvidia|furiosa|rebellions|rbln" || printf "no-accelerator-found\n"' \
      > "${node_dir}/accelerators.txt" 2>&1; then
    printf '  [ok] accelerators\n'
  else
    printf '  [warn] accelerators: probe returned non-zero\n'
  fi

  # Container runtime / Kubernetes binary presence
  if ssh_probe "${ip}" "${port}" \
      'for b in containerd kubelet kubectl; do
         if command -v "$b" > /dev/null 2>&1; then
           printf "%s=%s\n" "$b" "$(command -v "$b")"
         else
           printf "%s=not-found\n" "$b"
         fi
       done' \
      > "${node_dir}/runtimes.txt" 2>&1; then
    printf '  [ok] runtimes\n'
  else
    printf '  [warn] runtimes: probe returned non-zero\n'
  fi

  printf '  artifacts: %s/\n' "${node_dir}"
done

printf '\nDiscovery complete. All artifacts in: %s/\n' "${OUTDIR}"
