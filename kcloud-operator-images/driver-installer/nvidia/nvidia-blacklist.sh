#!/usr/bin/env bash
# ============================================================
# nvidia-blacklist.sh: unattended-upgrades 가 nvidia 드라이버 패키지를 못 바꾸게 막는다
# 상세: operator 가 드라이버 버전을 소유한다. apt-mark manual 은 autoremove 만 막고 unattended
#       업그레이드는 못 막으므로 Package-Blacklist 를 별도로 건다 — unattended-upgrades와
#       graphics-drivers PPA 가 재부팅마다 드라이버를 되돌리는 경로를 차단한다.
# 생성일: 2026-09-17
# ============================================================

# 테스트가 경로를 바꿀 수 있게 override 가능.
NVIDIA_BLACKLIST_PATH="${NVIDIA_BLACKLIST_PATH:-/etc/apt/apt.conf.d/99-nvidia-driver-hold}"

# write_unattended_blacklist 는 blacklist drop-in 을 호스트에 쓴다(멱등 — 매번 덮어쓰기).
# ns 의 stdin 으로 heredoc 을 넘겨 호스트의 cat 이 파일을 만든다.
write_unattended_blacklist() {
	ns "cat > '${NVIDIA_BLACKLIST_PATH}'" <<'BLK'
// operator 가 드라이버 버전을 소유하므로 unattended-upgrades 가 nvidia 드라이버 패키지를 못 바꾸게 막는다.
// driver-installer entrypoint 가 설치 시 적용(멱등). 컨테이너 toolkit·libnvidia-container 는 별도 관리.
Unattended-Upgrade::Package-Blacklist:: "nvidia-driver-";
Unattended-Upgrade::Package-Blacklist:: "nvidia-dkms-";
Unattended-Upgrade::Package-Blacklist:: "nvidia-kernel-";
Unattended-Upgrade::Package-Blacklist:: "nvidia-compute-utils-";
Unattended-Upgrade::Package-Blacklist:: "nvidia-utils-";
Unattended-Upgrade::Package-Blacklist:: "nvidia-firmware-";
Unattended-Upgrade::Package-Blacklist:: "libnvidia-";
Unattended-Upgrade::Package-Blacklist:: "xserver-xorg-video-nvidia";
BLK
}
