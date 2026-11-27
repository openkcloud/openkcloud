#!/usr/bin/env bash
# ============================================================
# investigate-pcie.sh: tt-smi -r 급정지 원인 조사용 읽기 전용 수집기(<tenstorrent-node>)
# 상세: SEL 상세·BMC/BIOS 버전·tenstorrent PCIe 장치와 루트 포트의 AER/DPC 설정·
#       커널의 AER 소유(_OSC)·이전 부팅 마지막 커널 줄·tt 펌웨어/kmd 버전을 모아
#       runs/<UTC>-investigate/ 에 남기고 ROOTCAUSE 판정 줄을 찍는다. 쓰기 명령 없음.
# 생성일: 2026-09-29
# ============================================================
set -euo pipefail
NODE="${NODE:-k8s-worker3}"
OUT_DIR="${OUT_DIR:-$(dirname "$0")/runs/$(date -u +%Y%m%dT%H%M%SZ)-investigate}"
mkdir -p "$OUT_DIR"
# 원격은 전부 읽기 명령. sudo 는 -n(비대화형): 암호 프롬프트로 멈추지 않고 즉시 실패.
# LC_ALL=C 로 클라이언트가 깨진 ko_KR.UTF-8 로캘을 원격에 넘기지 않게 한다 — 안 그러면
# 원격 bash 가 'setlocale: LC_ALL: cannot change locale' 경고를 stdout(2>&1)에 섞어
# 첫 줄 파싱(BDF 등)을 오염시킨다.
# 노드 SSH 계정. 소스에 박지 않는다 — 공개 저장소에 내부 계정이 실리면 안 된다.
SSH_USER="${SSH_USER:-ubuntu}"
remote() { LC_ALL=C ssh -o BatchMode=yes -o ConnectTimeout=5 -o ServerAliveInterval=2 -o ServerAliveCountMax=3 "${SSH_USER}@${NODE_IP}" "$1" 2>&1; }
save() { remote "$2" > "$OUT_DIR/$1" || true; }
TT_SMI=/home/kcloud/.local/bin/tt-smi   # 비대화형 ssh PATH 에 없어 전체 경로로 부른다

# PHASE=verdict OUT_DIR=<기존 run 디렉터리> 로 부르면 수집을 건너뛰고 파일만 다시 읽어
# 판정 줄을 찍는다(evidence.sh --command 가 라이브 없이 재실행할 수 있게).
if [ "${PHASE:-collect}" = collect ]; then
  # NODE_IP 해석은 remote() 를 쓰는 collect 에서만 필요하다 — 게이트 밖에 두면
  # PHASE=verdict 오프라인 재실행이 kubectl 에 의존해 클러스터 없이 실패한다.
  NODE_IP="$(kubectl get node "$NODE" -o jsonpath='{.status.addresses[?(@.type=="InternalIP")].address}')"
  save sel-detail.txt 'sudo -n ipmitool sel get 0x36d 0x36f'
  save sel-elist-tail.txt 'sudo -n ipmitool sel elist | tail -30'
  save sdr-critical.txt "sudo -n ipmitool sdr type 'Critical Interrupt'"
  save bmc-info.txt 'sudo -n ipmitool mc info'
  save bios.txt 'sudo -n dmidecode -t bios -t system -t baseboard'
  save tt-bdf.txt 'lspci -Dnn | grep -i -e tenstorrent -e 1e52'
  BDF="$(awk '{print $1; exit}' "$OUT_DIR/tt-bdf.txt")"
  save tt-lspci.txt "sudo -n lspci -vv -s $BDF"
  save tt-rootport.txt "p=\$(basename \$(dirname \$(readlink -f /sys/bus/pci/devices/$BDF))); echo ROOTPORT=\$p; sudo -n lspci -vv -s \$p"
  save aer-sysfs.txt "for f in /sys/bus/pci/devices/$BDF/aer_dev_fatal /sys/bus/pci/devices/$BDF/aer_dev_nonfatal; do echo \"== \$f\"; cat \$f 2>/dev/null; done"
  save cmdline.txt 'cat /proc/cmdline'
  save dmesg-aer.txt 'sudo -n dmesg -T | grep -iE "_OSC|AER|DPC|pcieport|[Ff]irmware.first|APEI|GHES|[Ss]urprise" | head -80'
  save boots.txt 'sudo -n journalctl --list-boots | tail -5'
  save prev-boot-tail.txt 'sudo -n journalctl -k -b -1 -o short-iso --no-pager | tail -60'
  save tt-versions.txt "$TT_SMI -v 2>&1; echo ---; modinfo tenstorrent 2>&1 | grep -E '^(version|srcversion|filename)'; echo ---; dpkg -l | grep -i -e tenstorrent -e tt-kmd -e tt-smi"
  save tt-snapshot.txt "$TT_SMI -s --snapshot_no_tty 2>&1 | head -120"
fi
BDF="$(awk '{print $1; exit}' "$OUT_DIR/tt-bdf.txt")"

# 판정 줄 — 파일만 읽는다(라이브 접속 없음). 값을 못 찾으면 unknown, 지어내지 않는다.
# sel_target: SEL Critical Interrupt 레코드의 Event Data(RAW) 3바이트 중 바이트2 를 이
#   BMC(Sensor ID 'PCIe Fatal Error', Entity 49.1 PCI Express Bus)가 PCI 버스 번호로
#   싣는다 — 그 값을 tenstorrent 장치 버스·루트 포트 버스와 대조한다. 해석은 BMC 벤더
#   의존이다. 레코드 원문에 'root'·BDF 같은 문자열 설명이 없어 바이트 값으로 판정한다.
sel_target=unknown
raw="$(grep -m1 -iE 'Event Data \(RAW\)' "$OUT_DIR/sel-detail.txt" 2>/dev/null | grep -oiE '[0-9a-f]{6}' | head -1 | tr 'A-F' 'a-f')"
sel_byte2="$(printf '%s' "$raw" | cut -c3-4)"
dev_bus="$(printf '%s' "$BDF" | cut -d: -f2 | tr 'A-F' 'a-f')"
rp_bus="$(grep -m1 '^ROOTPORT=' "$OUT_DIR/tt-rootport.txt" 2>/dev/null | cut -d= -f2 | cut -d: -f2 | tr 'A-F' 'a-f')"
if [ -n "$sel_byte2" ]; then
  if [ -n "$rp_bus" ] && [ "$sel_byte2" = "$rp_bus" ]; then
    sel_target=rootport
  elif [ -n "$dev_bus" ] && [ "$sel_byte2" = "$dev_bus" ]; then
    sel_target=device
  fi
fi

# aer_owner: _OSC 협상 결과. 'OS now controls [...]' 목록에 AER 가 들면 os,
#   'platform does not support [...AER...]' 이거나 'APEI firmware first mode' 면 firmware.
#   정규식은 공백 형태 'firmware first' 를 쓴다 — 이 노드 dmesg 원문이 하이픈 없이
#   그렇게 찍힌다.
aer_owner=unknown
grep -qiE 'OS now controls \[[^]]*AER' "$OUT_DIR/dmesg-aer.txt" 2>/dev/null && aer_owner=os
grep -qiE '[Ff]irmware.first|platform does not support \[[^]]*AER' "$OUT_DIR/dmesg-aer.txt" 2>/dev/null && aer_owner=firmware

# dpc: OS 가 DPC 를 쥐었고(_OSC) 또는 dmesg 가 DPC 활성화를 찍었으면 on, 아니면 off.
#   이 노드는 _OSC 가 DPC 를 OS 에 안 줬고 DPC 활성 로그도 없어 off.
dpc=off
grep -qiE 'OS now controls \[[^]]*DPC|DPC:.*(enabled|error containment)' "$OUT_DIR/dmesg-aer.txt" 2>/dev/null && dpc=on

# hypothesis: AER 가 firmware-first 면 플랫폼 오류 정책이 급정지의 상위 원인 후보.
hyp=board-link-down
[ "$aer_owner" = firmware ] && hyp=platform-policy

# fw: tt 스냅샷의 FLASH_BUNDLE_VERSION(펌웨어 번들). 없으면 unknown.
fw="$(grep -m1 -i 'FLASH_BUNDLE_VERSION' "$OUT_DIR/tt-snapshot.txt" 2>/dev/null | grep -oiE '0x[0-9a-f]+' | head -1)"
# kmd: modinfo tenstorrent 의 version 줄(=tt-kmd 버전).
kmd="$(grep -m1 '^version' "$OUT_DIR/tt-versions.txt" 2>/dev/null | awk '{print $2}')"

echo "ROOTCAUSE: sel_target=$sel_target aer_owner=$aer_owner dpc=$dpc hypothesis=$hyp fw=${fw:-unknown} kmd=${kmd:-unknown}" | tee "$OUT_DIR/verdict.txt"
