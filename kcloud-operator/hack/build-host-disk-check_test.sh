#!/usr/bin/env bash
# ============================================================
# build-host-disk-check_test.sh: build-host-disk-check.sh 의 임계·JSON 출력 시험(가짜 df/docker)
# 생성일: 2026-09-22
# ============================================================
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
mk_df() { # $1=used% $2=avail(G)
  cat > "$tmp/df" <<EOF
#!/usr/bin/env bash
echo "Filesystem Size Used Avail Use% Mounted on"
echo "/dev/root 935G $((935-$2))G ${2}G $1% /"
EOF
  chmod +x "$tmp/df"
}
cat > "$tmp/docker" <<'EOF'
#!/usr/bin/env bash
case "$*" in
  "system df --format"*) echo "Images 77 97.27GB";;
  *) echo "";;
esac
EOF
chmod +x "$tmp/docker"
mk_df 60 374
out="$(PATH="$tmp:$PATH" bash "$here/build-host-disk-check.sh")"; echo "$out" | grep -q 'free=40%' || { echo "FAIL a: $out"; exit 1; }
mk_df 90 94
if PATH="$tmp:$PATH" bash "$here/build-host-disk-check.sh" > "$tmp/o" 2>&1; then echo "FAIL b: exit 0"; exit 1; fi
grep -q 'WARN' "$tmp/o" || { echo "FAIL b: no WARN"; exit 1; }
mk_df 60 374
PATH="$tmp:$PATH" bash "$here/build-host-disk-check.sh" --json | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["freePercent"]==40, d'
echo "PASS 3/3"
