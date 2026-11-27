// ============================================================
// fencing.go: 세대(epoch) 발급
// 상세: 노드 잠금을 새로 잡을 때마다 세대가 오른다. 낡은 프로세스의 status 기록은 writeStatus(operation_controller.go)
// 가 세대로 걸러 폐기한다 — 복구 판정(recovery.go 의 HasStep)이 세대를 가로질러 이력을 봐야 해서다.
// 생성일: 2026-08-01 | 수정일: 2026-10-11
// ============================================================
package operation

// NextEpoch 는 다음 세대다.
func NextEpoch(cur int64) int64 { return cur + 1 }
