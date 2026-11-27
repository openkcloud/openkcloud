// ============================================================
// rebellions_cdi_prep.go: Rebellions device plugin 사전 점검·CDI 이름 파일 init container
// 상세: rbln-smi 가 없으면 플러그인 기동을 막는다(파드 전용 RSD 그룹을 못 만들면 노드 전체 그룹이 들어간다).
//       CDI 가 켜진 노드에서 플러그인이 요청하는 이름(v0.3: rbln<N>, v0.4: runtime)을
//       /var/run/cdi/kcloud-rebellions.yaml 로 정의한다. CDI 가 꺼진 containerd 는 이 파일을 읽지 않는다.
//       1.34 라인과 1.28 라인이 같은 파일을 쓴다.
// 생성일: 2026-10-24 | 수정일: 2026-10-24
// ============================================================

package controller

import corev1 "k8s.io/api/core/v1"

const (
	rbllnsPrepContainerName = "rbln-cdi-prep"
	rbllnsCDIVolumeName     = "cdi"
	rbllnsCDIHostDir        = "/var/run/cdi"

	// init container 종료 코드. rbllnsPrepScript 의 fail 호출과 같아야 한다(시험이 대조한다).
	rbllnsPrepExitNoSmi       = 3
	rbllnsPrepExitNoDevice    = 4
	rbllnsPrepExitWriteFailed = 5

	// 종료 코드에 대응하는 RebellionsDevicePluginReady 사유. 스크립트 메시지도 이 토큰으로 시작한다.
	rbllnsReasonNoSmi       = "RblnSmiMissing"
	rbllnsReasonNoDevice    = "NoRblnDevice"
	rbllnsReasonWriteFailed = "CDIWriteFailed"
	rbllnsReasonPrepFailed  = "PrepFailed"
)

// rbllnsPrepScript 는 POSIX sh 다. ROOT 아래에서 호스트 경로를 찾으므로 시험은 ROOT 를 임시
// 디렉터리로 바꿔 같은 스크립트를 실행한다. 파일은 임시 이름으로 쓴 뒤 mv 로 바꾼다 — containerd 가
// 쓰는 도중의 파일을 읽지 않게 한다. 파일 이름은 신판 플러그인이 시작할 때 지우는 접두사
// (rebellions.ai-npu_)와 겹치지 않는다. kubelet 이 container command 의 $$ 와 $(VAR) 를 바꾸므로
// 스크립트에 쓰지 않는다(시험이 확인한다).
const rbllnsPrepScript = `set -u
ROOT="${ROOT:-/host}"
TERM_LOG="${TERM_LOG:-/dev/termination-log}"
OUT="$ROOT/var/run/cdi/kcloud-rebellions.yaml"

fail() {
  printf '%s\n' "$2" > "$TERM_LOG"
  echo "$2" >&2
  exit "$1"
}

if [ ! -x "$ROOT/usr/bin/rbln-smi" ] && [ ! -x "$ROOT/driver/usr/bin/rbln-smi" ]; then
  fail 3 "RblnSmiMissing: 호스트 /usr/bin 과 /usr/local/bin 에 rbln-smi 가 없다. 파드 전용 RSD 그룹을 만들 수 없어 플러그인을 띄우지 않는다"
fi

DEVS=""
for d in "$ROOT"/dev/rbln[0-9]*; do
  [ -e "$d" ] || continue
  DEVS="$DEVS ${d##*/}"
done
[ -n "$DEVS" ] || fail 4 "NoRblnDevice: /dev/rbln* 가 없다. 드라이버가 올라오면 kubelet 이 다시 시도한다"

if [ "$GEN" = "v0.4" ] && [ -e "$ROOT/var/run/cdi/rbln.yaml" ]; then
  rm -f "$OUT" || fail 5 "CDIWriteFailed: $OUT 를 지우지 못했다"
  echo "벤더 툴킷 스펙 rbln.yaml 이 있어 runtime 파일을 쓰지 않는다"
  exit 0
fi

TMP="$OUT.tmp"
if ! {
  echo 'cdiVersion: "0.5.0"'
  echo "kind: $CDI_KIND"
  echo "devices:"
  if [ "$GEN" = "v0.4" ]; then
    echo "- name: runtime"
    echo "  containerEdits:"
    echo "    env:"
    echo "    - RBLN_KCLOUD_CDI=1"
  else
    for n in $DEVS; do
      echo "- name: $n"
      echo "  containerEdits:"
      echo "    deviceNodes:"
      echo "    - path: /dev/$n"
    done
  fi
} > "$TMP"; then
  rm -f "$TMP"
  fail 5 "CDIWriteFailed: $TMP 를 쓰지 못했다"
fi
mv -f "$TMP" "$OUT" || { rm -f "$TMP"; fail 5 "CDIWriteFailed: $OUT 로 바꾸지 못했다"; }
echo "wrote $OUT gen=$GEN kind=$CDI_KIND:$DEVS"
`

// rbllnsPrepInitContainer 는 사전 점검·CDI 이름 파일 init container 다. image 가 비면 nil 이다 —
// 이미지가 빈 init container 는 API 가 거절해 DaemonSet 전체가 만들어지지 않는다.
func rbllnsPrepInitContainer(image, gen, cdiKind string) *corev1.Container {
	if image == "" {
		return nil
	}
	return &corev1.Container{
		Name:            rbllnsPrepContainerName,
		Image:           image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Command:         []string{"sh", "-c", rbllnsPrepScript},
		Env: []corev1.EnvVar{
			{Name: "GEN", Value: gen},
			{Name: "CDI_KIND", Value: cdiKind},
		},
		SecurityContext: &corev1.SecurityContext{RunAsUser: int64Ptr(0)},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "host-dev", MountPath: "/host/dev", ReadOnly: true},
			{Name: "host-usr-bin", MountPath: "/host/usr/bin", ReadOnly: true},
			{Name: "host-driver-usr-bin", MountPath: "/host/driver/usr/bin", ReadOnly: true},
			{Name: rbllnsCDIVolumeName, MountPath: "/host/var/run/cdi"},
		},
	}
}

// rbllnsPrepReason 은 init container 종료 코드를 사유로 옮긴다. 0 은 성공이라 빈 문자열이다.
func rbllnsPrepReason(code int32) string {
	switch code {
	case 0:
		return ""
	case rbllnsPrepExitNoSmi:
		return rbllnsReasonNoSmi
	case rbllnsPrepExitNoDevice:
		return rbllnsReasonNoDevice
	case rbllnsPrepExitWriteFailed:
		return rbllnsReasonWriteFailed
	default:
		return rbllnsReasonPrepFailed
	}
}
