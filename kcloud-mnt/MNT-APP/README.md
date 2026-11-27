# openKcloud CMP — AI반도체 클라우드 통합관리 플랫폼 (Mockup)

> **정부 R&D 과제**: AI반도체 클라우드 플랫폼 구축 및 최적화 기술 개발 (IITP)
> **작성**: 오케스트로(주) · **연동**: ETRI · 이노그리드 · KETI · 경희대
> **상태**: 목업 v1.0 (ETRI 260615 검토 의견 반영 완료)

이종 AI반도체(GPU·NPU·PIM·HBM-PIM)를 단일 콘솔에서 관리하는 CMP(Cloud Management Platform)의 UI 목업이다.
OpenStack(IaaS) · Kubernetes(PaaS) · Kubeflow(AI 개발환경) · Rebellions ModelZoo(마켓플레이스)를 하나의 포탈로 통합.

---

## 🚀 실행

### 로컬 개발
```bash
npm install
npm run dev            # http://localhost:3000 (또는 3001)
```

### 원격 배포 (Netlify · 원클릭)
```bash
# Windows
deploy.bat 더블클릭
```
첫 실행 시 브라우저로 Netlify 로그인 1회 후 자동 배포. 이후 실행은 같은 URL에 덮어쓰기.

### 로그인 계정
Keycloak(OIDC PKCE) 로그인 — `http://localhost:3000` 으로 열면 Keycloak 로그인 화면으로 넘어간다.
- 접속 PC hosts 에 `172.30.1.200 keycloak.dev.okestro` 등록, MNT-CORE(8000)·MNT-IAAS(8001) 실행
- `localhost` 로만 연다(다른 PC 에서 IP 로 열면 PKCE 가 막힌다). 3001 로 띄우면 CORE·IAAS `CORS_ORIGINS` 에 추가
- 비밀번호는 개발 Keycloak 관리자에게. Netlify 목업 배포(`npm run build`)는 Keycloak 에 닿지 않아 로그인할 수 없다

| 역할 | 개발 계정 | 사이드바 초점 |
|---|---|---|
| **관리자** | `admin` | 개요 → 옵저버빌리티 → 클라우드자원 → 마켓플레이스 → AI개발환경 → AI인프라 → 운영 |
| **유저** | `gy.yang` | 개요 → 클라우드자원 → 마켓플레이스 → AI개발환경 → 옵저버빌리티 → AI인프라 |

---

## 📊 ETRI 260615 피드백 반영 현황 (총 44건)

### 📈 진척 요약
| 상태 | 건수 | 비율 |
|---|---:|---:|
| ✅ **반영 완료** | **28건** | **64%** |
| 🟡 부분 반영 | 5건 | 11% |
| ⚙️ 이노그리드 대기 | 6건 | 14% |
| ❌ 스코프 제외 | 4건 | 9% |
| 📝 명세서 갱신 | 1건 | 2% |
| ➖ 이슈 없음 | 1건 | 2% |

**즉시 반영률 64%** · **이노그리드 대기 제외 실질 진행률 84%** (32/38)

### 🗂️ 상세 매트릭스

#### 1. 개요
| # | 항목 | 상태 | 반영 위치 |
|--:|---|:---:|---|
| 1 | 일부 기능 클릭 원활치 않음 | ✅ | 전 페이지 액션·Sheet·toast 완비 |
| 2 | 명세↔목업 지속 검토 | 🟡 | 진행 중, 정기 채널은 협의 |

#### 2. 옵저버빌리티
| # | 항목 | 상태 | 반영 위치 / 대기 사유 |
|--:|---|:---:|---|
| 3 | Avg P99 지연·에러율·RPS | ⚙️ | 이노그리드 모니터링 설치 대기 |
| 4 | 활용율 히트맵 | ⚙️ | 이노그리드 API 업데이트 대기 |
| 5 | 응답시간 분포 | ⚙️ | 이노그리드 vLLM 테스트 대기 |
| 6 | **권장조치 자동 적용** | ✅ | `/monitoring/insights` — 정보 제공까지 유지, 자동 적용은 차차년도 명시 |
| 7 | 서비스 플로우 vLLM 내부 추적 | ⚙️ | 이노그리드 테스트 대기 |
| 8 | 분산 트레이스 vLLM 내부 추적 | ⚙️ | 이노그리드 추가 테스트 대기 |
| 9 | 트레이스 기본 정보 범위 | ⚙️ | 이노그리드 후속 대기 |
| 10 | 로그 이벤트 | ➖ | 이슈 없음 |
| 11 | **SLO 정의** | ❌ | 제외 결정 — nav 라벨 "알람·SLO"→"알람" |
| 12 | **알람 CRUD 기능** | ✅ | `/monitoring/alerts` — 3탭 (활성/정책/채널), 정책 편집·복제·테스트 발송 |

#### 3. 포탈 메뉴 구성
| # | 항목 | 상태 | 반영 위치 |
|--:|---|:---:|---|
| 13 | **관리자/사용자 메뉴 차별화** | ✅ | `nav.ts` role별 순서 분기 (`getNavByRole`) |
| 14 | 사용자 메뉴 순서(안) | ✅ | 개요→클라우드→마켓→AI개발→옵저버→AI인프라 |
| 15 | 관리자 메뉴 순서(안) | ✅ | 개요→옵저버→클라우드→마켓→AI개발→AI인프라→운영 |

#### 4. 클라우드 자원 — IaaS
| # | 항목 | 상태 | 반영 위치 |
|--:|---|:---:|---|
| 16 | 자원 신청 워크플로우 명확화 | 🟡 | 프로젝트·VM 정리, 통합 위저드 재정비 미완 |
| 17 | **네트워크 생성 주체 정의** | ✅ | `/iaas/networks` — 사용자용/관리자용 2개 버튼 + 정책 배너 |
| 18 | 네트워크 생성(C) 명세 없음 | ❌ | 스코프 제외 — 기본 버튼 제거 |
| 19 | **볼륨 사용자 직접 생성** | ✅ | `/iaas/volumes` — 사용자 자율 CRUD 명시 배너 |
| 20 | 키페어·라우터 관리 | ✅ | `/iaas/keypairs`, `/iaas/routers` 신설 |
| 21 | Floating IP 관리 기능 | ✅ | `/iaas/networks` — 해제·회수·재할당 액션 |
| 22 | 보안 그룹 관리 | ✅ | `/iaas/security-groups` CRUD 신설 |
| 23 | AI 반도체 관리 명세 없음 | ❌ | 스코프 제외 — `/ai-accelerators` 페이지 제거 |
| 24 | **OpenShift 지원 범위** | ❌ | 제외 결정 — 자원풀 유형에서 옵션 제거 |

#### 5. 클라우드 자원 — PaaS
| # | 항목 | 상태 | 반영 위치 |
|--:|---|:---:|---|
| 25 | K8s 프로비저닝 5단계 흐름 | 🟡 | 워크로드 완성, Magnum→Nova/Neutron 위저드 미완 |
| 26 | Ingress·Storage 생성·연동 | ✅ | `/paas/services` — Ingress 관계 다이어그램 + 규칙 관리 |
| 27 | VM 개수·가속기 종류·개수 선정 | 🟡 | 워크로드 위저드 포함, 클러스터 위저드는 미완 |
| 28 | StorageClass·PV 관리자 UI | ✅ | `/paas/storage` — 관리자 생성 다이얼로그 |

#### 6. 서비스 마켓플레이스
| # | 항목 | 상태 | 반영 위치 |
|--:|---|:---:|---|
| 29 | 실행 대상·방식 구체화 | ✅ | `/catalog` — SignatureShowcase 3종 카드 |
| 30 | 조합형 실행 플로우 | ✅ | 4단계 배포 위저드 (모델→가속기→인프라→실행) |
| 31 | 원클릭 LLM (챗봇) | ✅ | 대표 카드 + 자동 배포 흐름 안내 |

#### 7. AI 노트북
| # | 항목 | 상태 | 반영 위치 |
|--:|---|:---:|---|
| 32 | Kubeflow 사용 순서 가이드 | ✅ | `/ai-development` 홈 — 8단계 흐름 가이드 배너 |
| 33 | Pipeline YAML 업로드 | ✅ | `/ai-development/pipelines` — 파일·붙여넣기 2가지 모드 |
| 34 | Experiments 정의 수단 | ✅ | Katib 알고리즘 선택 다이얼로그 |
| 35 | HPO 명칭 변경 | ✅ | 페이지 타이틀·nav 라벨 통일 |
| 36 | MLPerf 벤치마크 콘텐츠 | ✅ | 하드웨어·모델 벤치 2개 탭 신설 |
| 37 | Attention Layer 프로파일링 방식 | ✅ | 1분 주기 샘플링 명시 |
| 38 | KV Cache 미스율 산출 방식 | ✅ | vLLM 표준 메트릭 조합 명시 |
| 39 | 응답 지연 TTFT/TPOT/E2E 정의 | ✅ | 3종 분리 카드 + 정의 |

#### 8. IAM 접근제어
| # | 항목 | 상태 | 반영 위치 |
|--:|---|:---:|---|
| 40 | 권한 매트릭스 토글 방식 | ✅ | `/iam/roles` — Switch + 저장·되돌리기 |
| 41 | 권한 매트릭스 추가·삭제 | ✅ | 커스텀 권한 정의 다이얼로그 |

#### 9. 기타 (명세-목업 불일치)
| # | 항목 | 상태 | 반영 위치 |
|--:|---|:---:|---|
| 42 | 워크로드 생성 화면 부재 | ✅ | `/paas/workloads` — 5종 유형 2단계 위저드 |
| 43 | 스토리지 관련 기능 명세 | 🟡 | UI 완성 (스프린트 3), 명세 갱신은 문서 담당 |
| 44 | 이미지 패키징 기술 | 📝 | 범위 축소 or 자동 패키징 협의 필요 |

---

## 🛠️ 스택

| 계층 | 기술 |
|---|---|
| Framework | Next.js 16 (App Router · Webpack build) · React 19 · TypeScript |
| 스타일 | Tailwind 4 · shadcn/ui (New York) · Pretendard Variable |
| 상태·데이터 | TanStack Query v5 · Zustand · react-hook-form · zod |
| Mock | MSW v2 (Service Worker · fixtures.ts) |
| 시각화 | Recharts + 자체 viz 컴포넌트 (Sparkline · Sankey · Isometric 등) |
| 배포 | Netlify (정적 export · Webpack chunk) |

---

## 📁 페이지 인벤토리 (총 40+ 화면)

```
개요                → /dashboard (통합 대시보드 + 옵저버빌리티 바로가기)

옵저버빌리티        → /monitoring, /monitoring/insights (AI 모델 분석)
                    /monitoring/traces, /monitoring/logs, /monitoring/alerts

클라우드 자원 IaaS   → /iaas + projects·instances·networks·routers
                     ·security-groups·keypairs·volumes·images·providers

클라우드 자원 PaaS   → /paas + clusters·workloads·services·storage·config

AI 인프라           → /resource-map, /infrastructure/servers, /topology

AI 개발환경         → /ai-development + profiles·notebooks·pipelines
                     ·experiments·serving·model-insights

서비스 마켓플레이스  → /catalog

운영 (관리자)       → /iam + users·roles·api-keys·audit, /settings

개발환경 IDE       → /notebooks/[id]/ide (풀스크린 iframe 슬롯)
```

---

## 📚 프로젝트 문서

- `docs/요구사항_정의서_mockup_반영_검토서.docx` · `.xlsx` — 초기 요구사항 vs 목업 매핑
- `docs/IaaS_PaaS_기능명세서.xlsx` — 113개 기능의 대분류·API·상태 매트릭스
- `docs/generate_*.py` — 문서 자동 생성 스크립트

---

## 🗓️ 스프린트 히스토리

| 스프린트 | 커밋 | 반영 |
|---|---|---|
| 1. 사이드바 · 대시보드 | `47139b3` | role별 nav 분기, 대시보드 옵저버빌리티 바로가기 |
| 2. IaaS 자원 관리 | `1bede22` | 보안그룹·키페어·라우터 3개 신규 페이지 + FIP 회수/해제 |
| 3. PaaS 완성도 | `5436795` | 워크로드 생성·PV/SC 관리자 UI·Ingress 관계 뷰 |
| 4. 마켓플레이스 시그니처 | `99e9cfa` | SignatureShowcase 3종 + 4단계 배포 위저드 |
| 5. AI 개발환경 UX | `755af71` | 8단계 흐름 가이드·YAML 업로드·HPO·MLPerf·TTFT/TPOT/E2E |
| 6. IAM 완성 | `eba9349` | Switch 권한 매트릭스 + 커스텀 권한 CRUD |
| D. 스코프 정리 | `dc05fee` | 명세 없는 UI 제거 (네트워크 생성·AI반도체 관리) |
| B. 결정 반영 | `248cd51` | SLO 제외·알람 CRUD·OpenShift 제외·생성 주체 명시 |

---

## 🔗 링크

- **저장소**: https://github.com/openkcloud/kcloud-mnt
- **원본 피드백 PDF**: `260615_openKcloud Portal 검토 의견_v1.0.pdf` (ETRI)
- **연락**: 오케스트로(주) · 윤호영 (hy.yun@okestro.com)
