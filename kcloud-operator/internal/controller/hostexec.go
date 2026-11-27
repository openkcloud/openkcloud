// ============================================================
// hostexec.go: kcloud-host-exec 이미지 조회 — 호스트 명령 실행 Job 이 공유하는 단일 출처
// 상세: MIG apply/관측 Job, ACPP 재부팅 Job, NodeReboot 참여자, DRA CDI init container 가 모두
// 같은 nsenter 이미지를 쓴다. 차트가 HOST_EXEC_IMAGE 로 주입하고, 하위 호환을 위해 구 이름
// ACPP_MIG_JOB_IMAGE 도 한 릴리스 동안 함께 낸다.
// 생성일: 2026-09-09 | 수정일: 2026-10-11
// ============================================================
package controller

import "os"

// HostExecImageEnv 는 kcloud-host-exec 이미지를 지정하는 환경변수다.
const HostExecImageEnv = "HOST_EXEC_IMAGE"

// HostExecImage 는 호스트 명령 실행 Job 이미지를 돌려준다. 미설정이면 빈 문자열.
func HostExecImage() string { return os.Getenv(HostExecImageEnv) }
