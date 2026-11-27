# 서비스 클러스터 삭제 (관리자 즉시 삭제) — 설계 문서

작성일: 2026-10-19
대상 화면: `/paas/clusters` (서비스 클러스터 관리)

## 목표

클러스터 관리 화면에서 **관리자가 서비스 클러스터(Magnum)를 즉시 삭제**할 수 있게 한다.
삭제로 더 이상 존재하지 않게 된 클러스터를 참조하는 포털 접근 매핑(`paas_project_cluster`)은
**리컨실리에이션(sweep)** 으로 정리한다.

## 결정 사항 (브레인스토밍 확정)

- 삭제 방식: **관리자 즉시 삭제** (자원신청→승인 플로 아님). 템플릿 CRUD·접근권한 관리와 동일하게
  **프론트 역할 게이팅**(MNT-PAAS는 백엔드 인증 가드가 없음).
- 버튼 위치: **목록 행 + 상세 모달** 둘 다.
- 매핑 정리: **삭제 추적 방식이 아니라 리컨실리에이션**. 삭제는 Magnum DELETE만 하고,
  매핑 정리는 "존재하지 않는 클러스터를 참조하는 행 제거" sweep이 담당한다.
  - 이유: Magnum DELETE는 **비동기**라 `DELETE_IN_PROGRESS` → `DELETE_COMPLETE`(소멸) 또는
    `DELETE_FAILED`(클러스터 잔존) 로 끝난다. "삭제 요청 시점"에 매핑을 지우면 `DELETE_FAILED` 때
    **살아있는 클러스터의 접근권한만 사라지는 버그**가 생긴다.
  - 프론트 세션에서 완료를 감지해 지우는 방식은 **관리자가 완료 전 화면을 떠나면 영영 정리 안 됨** →
    데이터 정합성을 "탭 유지"에 의존하게 되어 부적절.
  - sweep은 **자가 치유**: 아무 관리자나 다음에 클러스터 화면을 열면 고아가 정리된다. 손으로
    Magnum에서 직접 지운 클러스터 등 **다른 경로로 생긴 고아도** 함께 정리된다.

## 아키텍처

기존 "템플릿 삭제" 패턴을 3개 레이어에 미러링 + CORE에 리컨실리에이션 엔드포인트 1개 추가.
새로운 아키텍처·백엔드 간 결합 없음(프론트가 관리자 토큰으로 CORE 호출하는 기존 패턴 유지).

```
[clusters/page.tsx] --DELETE--> [MNT-PAAS /clusters/{id}] --DELETE--> Magnum
        |
        └─ 목록 로드 성공 시(관리자) --reconcile(existingIds)--> [MNT-CORE /clusters/access/reconcile]
                                                                      └─ paas_project_cluster 고아 행 삭제
```

## 변경 상세

### 1. 백엔드 — `MNT-PAAS/app/routers/clusters.py`

- `@router.delete("/clusters/{cluster_id}")` 추가. 본문은 `templates.py`의 `delete_template` 미러링:
  ```python
  @router.delete("/clusters/{cluster_id}")
  def delete_cluster(cluster_id: str):
      try:
          return magnum.request("DELETE", f"/clusters/{cluster_id}")
      except Exception as e:  # noqa: BLE001
          raise _err(e)
  ```
- 모듈 상단 docstring의 "생성·삭제는 … 여기는 읽기 + kubeconfig" 문구를 실제(삭제 지원)에 맞게 수정.
- 인증 가드 없음 — 템플릿 CRUD와 동일, 프론트 역할 게이팅.

### 2. 백엔드 — `MNT-CORE/app/routers/cluster_access.py`

- 리컨실리에이션 엔드포인트 추가 (admin 전용, 기존 `require_admin` 의존성 사용):
  ```python
  class ReconcileBody(BaseModel):
      existingClusterIds: list[str]

  @router.post("/clusters/access/reconcile", dependencies=[Depends(require_admin)])
  def reconcile_cluster_access(body: ReconcileBody, db: Session = Depends(get_db)):
      """현재 존재하는 클러스터 목록에 없는 cluster_id 의 매핑 행을 모두 삭제(고아 정리).
      안전: 빈 목록이면 아무것도 지우지 않는다(일시적 빈 응답에 전체 삭제 방지)."""
      ids = [c for c in body.existingClusterIds if c]
      if not ids:
          return {"purged": 0}
      res = db.execute(
          text("DELETE FROM paas_project_cluster WHERE cluster_id <> ALL(:ids)"),
          {"ids": ids},
      )
      db.commit()
      return {"purged": res.rowcount}
  ```
- **안전 가드**: 빈 ID 목록이면 no-op(삭제 안 함).

### 3. 프론트 훅

- `MNT-APP/src/lib/paas-api.ts` — `useDeleteCluster()` 추가 (`useDeleteTemplate` 미러링):
  ```ts
  export function useDeleteCluster() {
    const qc = useQueryClient();
    return useMutation({
      mutationFn: (id: string) => sendJson<void>("DELETE", `/api/paas/clusters/${id}`),
      onSuccess: () => qc.invalidateQueries({ queryKey: ["paas-clusters"] }),
    });
  }
  ```
- `MNT-APP/src/lib/member-api.ts` — `useReconcileClusterAccess()` 추가:
  ```ts
  export function useReconcileClusterAccess() {
    const qc = useQueryClient();
    return useMutation({
      mutationFn: (existingClusterIds: string[]) =>
        coreFetch<{ purged: number }>(`/clusters/access/reconcile`, {
          method: "POST", body: JSON.stringify({ existingClusterIds }),
        }),
      onSuccess: (r) => {
        if (r.purged > 0) {
          qc.invalidateQueries({ queryKey: ["core-my-clusters"] });
          qc.invalidateQueries({ queryKey: ["core-cluster-access"] });
        }
      },
    });
  }
  ```

### 4. 프론트 UI — `MNT-APP/src/app/(portal)/paas/clusters/page.tsx` (관리자 전용)

- **목록 행** (`ClusterRow`): `isAdmin`을 prop으로 받아, kubeconfig 버튼 옆에 `Trash2`(rose) 고스트 아이콘 버튼.
  - `isAdmin`일 때만 렌더. 상태가 `DELETE_`로 시작하면(이미 삭제 중) 비활성.
- **상세 모달** (`ClusterDetailDialog`): 하단 kubeconfig 버튼 옆에 파괴적 "클러스터 삭제" 버튼(`isAdmin`일 때만).
- 삭제 동작(행·모달 공통): 기존 `useConfirm({ destructive: true, confirmLabel: "삭제" })` 사용.
  - 설명에 클러스터 이름 + "되돌릴 수 없으며, 클러스터와 그 위의 모든 워크로드가 함께 삭제됩니다." 경고.
  - 확인 시 `useDeleteCluster().mutate(id)` → 성공 toast + (모달은) 닫힘, 실패 시 에러 toast(백엔드 magnum 메시지).
- **리컨실리에이션 트리거** (삭제 로직과 독립):
  - `ClustersPage`에서 `isAdmin && clusters.isSuccess && (clusters.data?.length ?? 0) > 0`일 때,
    `useReconcileClusterAccess()`를 `clusters.data`의 ID 집합으로 1회 호출하는 `useEffect`.
  - 같은 ID 집합으로 중복 호출하지 않도록 직전 호출 키(정렬된 ID join)를 `ref`로 기억해 변동 시에만 호출.
  - `clusters.isError`이거나 목록이 비어 있으면 호출하지 않음(안전 가드와 일치).

## 에러·상태 처리

- Magnum 삭제는 비동기 → 삭제 직후 목록에서 해당 클러스터가 `DELETE_IN_PROGRESS`(노란 배지)로 보이고,
  완료되면 목록에서 사라진다. `useClusters`는 `*_IN_PROGRESS`가 있으면 4초 폴링하므로 자동 반영.
- 삭제 API 실패(502 magnum error)는 에러 toast로 노출.
- reconcile는 백그라운드성 — 실패해도 사용자 플로를 막지 않음(조용히 무시, 다음 로드에서 재시도됨).

## 범위 밖

- 자원신청→승인 삭제 플로(티켓·승인) 구현.
- 삭제 확인 시 클러스터 이름 타이핑 요구(기존 파괴적 확인 패턴과 일관 위해 일반 확인 사용).
- 백엔드 주기적(cron) 리컨실리에이션 — 관리자 화면 로드 트리거로 충분하다고 판단.

## 검증

- pytest 하니스 없음 → 아래로 검증:
  - `python -m py_compile` : 변경한 `MNT-PAAS/app/routers/clusters.py`, `MNT-CORE/app/routers/cluster_access.py`
  - `tsc --noEmit` / `eslint` : 변경한 `paas-api.ts`, `member-api.ts`, `clusters/page.tsx`
- 수동 확인(로컬 Magnum 연동 시):
  1. 관리자로 클러스터 삭제 → 목록이 `DELETE_IN_PROGRESS`로 바뀜
  2. 완료 후 목록에서 소멸
  3. 접근 매핑이 있던 클러스터였다면, 관리자가 목록을 다시 열었을 때 reconcile로 매핑 purge 확인
  4. `DELETE_FAILED`로 끝난 경우 매핑이 **유지**되는지 확인(접근권한 보존)
