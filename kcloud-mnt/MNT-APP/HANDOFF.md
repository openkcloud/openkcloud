# 인계 문서 (HANDOFF) — openKcloud CMP Mockup

> **목적**: 다른 Claude 세션(Mac Mini 등)이 이 저장소를 clone 후 **컨텍스트를 100% 복원**하고 즉시 이어서 작업할 수 있도록 하는 완전 인계.
> **기준 시점**: 2026-09-07 · 최신 커밋 `88970df`

---

## 0. 첫 30초에 파악할 것

- **누구**: 오케스트로(주) 윤호영(hy.yun@okestro.com)이 개발
- **뭐**: 정부 IITP R&D 과제 "AI반도체 클라우드 플랫폼" CMP UI 목업 (Next.js)
- **어디**: https://github.com/openkcloud/kcloud-mnt (`main`)
- **왜**: ETRI 260615 검토 의견 반영 후 시연·평가 자료로 사용
- **지금 진행률**: **28/44건 반영 완료 (64%)**, 이노그리드 대기 6건 제외 실질 84%

---

## 1. 프로젝트 정체성

**정체성**: openKcloud CMP는 이종 AI반도체(GPU·NPU·PIM·HBM-PIM)를 단일 콘솔에서 관리하는 CMP(Cloud Management Platform) UI 목업. OpenStack(IaaS) + Kubernetes(PaaS) + Kubeflow(AI 개발환경) + Rebellions ModelZoo(마켓플레이스) 통합.

**주요 참여기관**:
- **오케스트로(주)**: 포탈 UI·통합 관리 프레임워크 (본 저장소 담당)
- **이노그리드**: 옵저버빌리티 데이터·API (모니터링·트레이스)
- **ETRI**: 검토·명세 · 자원할당 프레임워크
- **KETI**: 최적화 알고리즘
- **경희대**: 초거대 AI 클라우드

**스택** (변경 시 참고):
- Next.js 16 (App Router · **Webpack build** — Turbopack 아님, Netlify 배포 시 필터 문자 이슈로 전환)
- React 19 · TypeScript · Tailwind 4 · shadcn/ui (New York)
- TanStack Query v5 · Zustand · react-hook-form · zod
- MSW v2 (Service Worker 목업, `src/mocks/`)
- Recharts + 자체 viz 컴포넌트
- Netlify (정적 export 배포)

---

## 2. 현재까지 완료된 작업 (스프린트 8건 + 정리 2건)

| 순서 | 커밋 | 스프린트 | ETRI 대응 § | 주요 파일 |
|--:|---|---|---|---|
| 1 | `47139b3` | 사이드바 역할 분기 + 대시보드 옵저버빌리티 바로가기 | §3 | `src/lib/nav.ts`, `src/components/app-sidebar.tsx`, `src/app/(portal)/dashboard/page.tsx` |
| 2 | `1bede22` | IaaS 자원 관리 (보안그룹·키페어·라우터 신설 + FIP 회수·해제) | §4.A | `src/app/(portal)/iaas/{security-groups,keypairs,routers}/page.tsx`, `iaas/networks/page.tsx` |
| 3 | `5436795` | PaaS 완성도 (워크로드 생성·PV/SC 관리자·Ingress 관계) | §4.B, §8.i | `src/app/(portal)/paas/{workloads,storage,services}/page.tsx` |
| 4 | `99e9cfa` | 마켓플레이스 시그니처 시나리오 + 4단계 배포 위저드 | §5 | `src/app/(portal)/catalog/page.tsx` |
| 5 | `755af71` | AI 개발환경 UX (8단계 흐름·YAML 업로드·HPO·MLPerf·TTFT/TPOT/E2E) | §6 | `src/app/(portal)/ai-development/{page,pipelines,experiments,model-insights}` |
| 6 | `eba9349` | IAM 인터랙티브 권한 매트릭스 (Switch + 커스텀 권한) | §7 | `src/app/(portal)/iam/roles/page.tsx` |
| 정리 | `dc05fee` | 스코프 제외 — `/ai-accelerators` 페이지 제거 + 네트워크 생성 버튼 제거 | §4.A.iii, §4.A.v (트랙 D) | `nav.ts`, dashboard QuickLink, 여러 참조 파일 |
| 하향 | `91cf747` | 권장조치 자동 적용 → 정보 제공 수준으로 하향 | §2.B.i | `src/app/(portal)/monitoring/insights/page.tsx` |
| 트랙B | `248cd51` | SLO 제외 + 알람 CRUD + OpenShift 제외 + 생성 주체 명시 | §2.E, §4.A | `monitoring/alerts`, `iaas/{providers,networks,volumes}`, `nav.ts` |
| 문서 | `477eac9` | README에 피드백 반영 매트릭스 + 프로젝트 개요 정리 | — | `README.md` |
| 이관 | `88970df` | Mac 이관 가이드 + `deploy.sh` (macOS 배포) | — | `MAC_SETUP.md`, `deploy.sh` |

---

## 3. ETRI 260615 피드백 44건 — 현재 상태

**진척 요약** (README와 동일 · 참고용):
- ✅ 반영 완료: 28건 (64%)
- 🟡 부분 반영: 5건 (11%)
- ⚙️ 이노그리드 대기: 6건 (14%)
- ❌ 스코프 제외: 4건 (9%)
- 📝 명세 갱신: 1건 (2%)
- ➖ 이슈 없음: 1건 (2%)

**전체 44건 매트릭스는 `README.md`의 §"ETRI 260615 피드백 반영 현황" 참조**.
**원본 PDF**: `H:\내 드라이브\정부R&D과제(수행)\★16-2. AI반도체 클라우드 플랫폼 구축 및 최적화 기술 개발\10. 목업자료\ETRI 피드백 자료\260615_openKcloud Portal 검토 의견_v1.0.pdf` (Mac에서는 Google Drive 경로).

**피드백 정리 xlsx**: 같은 폴더의 `ETRI_피드백_정리_260615.xlsx` (44행 매트릭스, 오케스트로가 만든 조치사항 초안 포함).

---

## 4. 결정 사항 이력 (트랙 B — 무엇을 왜 그렇게 결정했나)

다른 Claude가 결정 배경을 알아야 스코프를 흔들지 않는다.

| # | 항목 | 최종 결정 | 결정 이유 |
|--:|---|---|---|
| §2.B.i | 권장조치 자동 적용 | **정보 제공까지만, 자동 적용은 차차년도** | 이노그리드는 정보 제공까지만 담당. 자동 적용은 과업 범위 외. |
| §2.E SLO | SLO 정의 | **제외** | SLO를 이번 과제에 도입하지 않기로 결정. nav 라벨 "알람"으로. |
| §2.E 알람 CRUD | 알람 관리 기능 | **반영** | 알람 정책·채널 CRUD UI 신설. |
| §4.A.i.2 네트워크 주체 | 네트워크 생성 주체 정의 | **반영 (관리자/사용자 이중 시나리오)** | 관리자: 공용·외부망, 사용자: 프로젝트 전용 내부 네트워크. |
| §4.A.i.4 볼륨 사용자 생성 | 사용자 직접 생성 여부 | **반영 (사용자 자율)** | 관리자 승인 없이 프로젝트 쿼터 내 CRUD 가능. |
| §4.A.vi OpenShift | 지원 범위 | **제외** | 자원풀 유형에서 OpenShift 옵션 삭제. |
| §4.A.iii 네트워크 생성(C) | 명세 없는 UI | **스코프 제외** | 버튼 제거, 안내 문구로 대체. |
| §4.A.v AI반도체 관리 | 명세 없는 UI | **스코프 제외** | `/ai-accelerators` 페이지 전체 삭제. 단, fixture(accelerators)와 VM/워크로드/마켓의 가속기 정보 표시는 유지 (참조 안 다침). |

---

## 5. 남은 작업 — 우선순위별

### 🟡 부분 반영 5건 — 오케스트로 단독 진행 가능

| # | 항목 | 예상 | 관련 파일 |
|--:|---|---|---|
| 16 | **자원 신청 통합 위저드 재정비** — 프로젝트 생성→네트워크→볼륨→가속기→VM 다단계 | ~반나절 | `src/components/resource-request-wizard.tsx`, `src/app/(portal)/iaas/instances/page.tsx` |
| 25+27 | **K8s 클러스터 생성 위저드** — Portal→Magnum→Nova/Neutron→Cluster 5단계 시각화 + VM 수·가속기 선정 단계 | ~반나절 | `src/app/(portal)/paas/clusters/page.tsx` (신규 다이얼로그 추가) |
| 43 | 스토리지 명세 갱신 | 문서 담당 몫 | (코드 아님) |
| 2 | 명세↔목업 정기 검토 채널 | 미정 | (프로세스) |

### ⚙️ 이노그리드 대기 6건 — 오케스트로 몫 아님

3·4·5·7·8·9번 (ETRI PDF §2.A~C 옵저버빌리티 데이터·API). 이노그리드에 일정 문의 필요.

### 📝 명세서 갱신 1건

44번 이미지 패키징 — Glance 업로드만 있는 상태. 범위 축소 or 자동 패키징 UI 추가 협의 필요.

---

## 6. 프로젝트 구조 · 주요 파일 위치

```
openkcloud-cmp/
├── src/
│   ├── app/
│   │   ├── (portal)/          # 로그인 후 화면
│   │   │   ├── dashboard/     # 통합 대시보드
│   │   │   ├── monitoring/    # 옵저버빌리티 5개 페이지
│   │   │   ├── iaas/          # OpenStack 자원 9개 페이지
│   │   │   ├── paas/          # K8s 자원 5개 페이지
│   │   │   ├── resource-map/  # 6계층 자원 모델
│   │   │   ├── infrastructure/servers/  # 물리 서버
│   │   │   ├── topology/      # 기술인벤토리 (Smartscape)
│   │   │   ├── ai-development/ # Kubeflow 6개 페이지
│   │   │   ├── catalog/       # 마켓플레이스 (SignatureShowcase)
│   │   │   ├── iam/           # IAM (users·roles·api-keys·audit)
│   │   │   └── settings/
│   │   ├── (fullscreen)/notebooks/[id]/ide/   # Code Server iframe
│   │   └── login/
│   ├── components/
│   │   ├── ui/                # shadcn/ui
│   │   ├── viz/               # 시각화 컴포넌트
│   │   ├── app-sidebar.tsx    # role 기반 사이드바
│   │   ├── page-header.tsx    # 공용 PageHeader
│   │   └── resource-request-wizard.tsx  # 자원 신청 위저드 (개선 대상 #16)
│   ├── lib/
│   │   ├── nav.ts             # NAV_SECTIONS + getNavByRole
│   │   ├── auth.ts            # useAuth (Zustand) · MOCK_USERS
│   │   ├── api.ts             # TanStack Query hooks
│   │   ├── types.ts           # 도메인 타입
│   │   └── model-zoo.ts       # 마켓플레이스 모델 데이터
│   └── mocks/
│       ├── browser.ts         # MSW setup
│       ├── fixtures.ts        # 모든 도메인 시드 데이터 (976줄)
│       └── handlers/          # 도메인별 API 핸들러
├── docs/
│   ├── 요구사항_정의서_mockup_반영_검토서.{docx,xlsx}
│   ├── IaaS_PaaS_기능명세서.xlsx  # 113개 기능 매트릭스
│   └── generate_*.py           # xlsx/docx 자동 생성 스크립트
├── public/
│   ├── fonts/PretendardVariable.woff2
│   ├── mockServiceWorker.js   # MSW 워커 (Netlify _headers로 MIME 명시)
│   └── _headers               # Netlify: SW MIME + no-cache
├── next.config.ts             # output:'export', webpack build, ignoreBuildErrors
├── deploy.bat                 # Windows Netlify 배포
├── deploy.sh                  # macOS/Linux Netlify 배포 (100755)
├── README.md                  # 프로젝트 소개 + 피드백 매트릭스
├── MAC_SETUP.md               # Mac 이관 가이드
└── HANDOFF.md                 # 이 파일
```

---

## 7. 자주 참조되는 코드 패턴

### 로그인·역할
```ts
// src/lib/auth.ts
Role = "admin" | "user"
MOCK_USERS = { admin: {password:"admin", user:{displayName:"최현화", org:"ETRI"}},
               user:  {password:"user",  user:{displayName:"윤호영", org:"오케스트로(주)"}} }
```

### 사이드바 순서 (role별 분기)
```ts
// src/lib/nav.ts — getNavByRole(role) 사용
// admin: 개요→옵저버빌리티→클라우드→마켓→AI개발→AI인프라→운영
// user:  개요→클라우드→마켓→AI개발→옵저버빌리티→AI인프라
```

### MSW mock 호출
```ts
// src/lib/api.ts
const { data, isLoading } = api.iaas.instances(filters);
// → /api/iaas/instances 요청, src/mocks/handlers/iaas.ts가 응답
```

### 페이지 헤더
```tsx
import { PageHeader } from "@/components/page-header";
<PageHeader title="..." description="..." actions={<Button>...</Button>} />
// description은 ReactNode 허용 (JSX 가능)
```

### 액션 피드백 (mock)
```ts
import { toast } from "sonner";
toast.success("생성됨", { description: "..." });
toast.error("삭제 거부 — 사용 중");
```

### 톤 (설명 서술 시)
평어체 (`~한다`) · dry 톤 · 강조 부사 제거 (`즉시`, `한눈에`, `매우` 등 사용 금지).
docs/generate_iaas_paas_spec.py의 `to_dry()` 함수 참조.

---

## 8. 배포 · 실행 · 검증

### 로컬 실행
```sh
npm install
npm run dev   # http://localhost:3000 (또는 3001)
```

### 프로덕션 빌드 · 배포
```sh
npm run build    # → out/ 정적 파일
./deploy.sh      # (Mac) 또는 deploy.bat (Windows)
```

### Windows dev 서버 종료
```powershell
Get-Process node | Stop-Process -Force
# 또는 taskkill //IM node.exe //F
```

### 라우트 헬스 체크
```sh
for path in /dashboard/ /iaas/security-groups/ /catalog/ /iam/roles/; do
  code=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:3001$path")
  echo "$code  $path"
done
```

### 흔한 트러블
- **Slack/GitHub 아이콘 없음**: lucide-react v1.x 제한. `MessageSquare`(Slack), 커스텀 SVG(GitHub, `src/components/icons.tsx`) 사용.
- **Turbopack 빌드 시 `~` 문자 파일명**: Netlify 404 유발. `next build --webpack`으로 해결됨 (`package.json` scripts).
- **한글이 든 .bat 파일 실행 오류**: CP949/UTF-8 mangling. `deploy.bat`은 ASCII만 사용.
- **폴더 드래그 시 Netlify Drop 빈 배포**: 브라우저 API 이슈. `deploy.sh`/`deploy.bat`으로 CLI 배포 권장.

---

## 9. Next Actions — 인수자가 바로 시작할 수 있는 것

인수자가 이 프로젝트 재개할 때 우선순위:

1. **git pull** — Mac에서 최신 반영
2. **npm install** — 의존성
3. **npm run dev** — 로컬 확인 (5분)
4. 남은 작업 중 선택:
   - (A) **#25+27 K8s 클러스터 생성 위저드** — `src/app/(portal)/paas/clusters/page.tsx`에 신규 다이얼로그 추가. Portal→Magnum→Nova VM→Neutron Network→K8s Cluster(Control+Worker) 5단계 시각화. 위저드 각 단계별 진행 상태 표시. 참조: 스프린트 3 `CreateWorkloadDialog`(workloads/page.tsx) 패턴.
   - (B) **#16 자원 신청 통합 위저드 재정비** — `src/components/resource-request-wizard.tsx` 확장. 프로젝트→네트워크→볼륨→가속기→VM 5단계, 각 단계에서 기존 자원 선택 or 새로 생성. 스프린트 4 `DeploymentWizard`(catalog/page.tsx) 패턴 참조.
   - (C) **이노그리드 후속 데이터 문의** — 이노그리드에 일정 확인 메일 초안 작성.

---

## 10. 기타 컨텍스트

- **Google Drive 문서 접근**:
  - Windows: `H:\내 드라이브\정부R&D과제(수행)\★16-2. AI반도체 클라우드 플랫폼 구축 및 최적화 기술 개발\10. 목업자료\ETRI 피드백 자료\`
  - Mac: `~/Library/CloudStorage/GoogleDrive-hy.yun@okestro.com/내 드라이브/정부R&D과제(수행)/★16-2. AI반도체 클라우드 플랫폼 구축 및 최적화 기술 개발/10. 목업자료/ETRI 피드백 자료/`
- **한글 파일명 처리**: Git·Bash에서는 문제없지만 일부 스크립트에서 quoting 필요.
- **CRLF/LF 경고**: Windows에서 만든 파일은 CRLF. Mac에서 pull 시 자동 변환. `.gitattributes` 없이도 동작.
- **user memory**: `~/.claude/projects/C--Users-YUN--claude-cloudnative/memory/project_openkcloud_cmp.md` (요약본), MEMORY.md 인덱스에 등록됨.

---

**이 문서를 시작점으로 Claude가 즉시 이어서 작업 가능**. 질문이 있으면 저장소 README·docs/·과거 커밋 메시지를 우선 참조.
