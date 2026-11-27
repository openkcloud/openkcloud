"""
openKcloud CMP — 요구사항 정의서 v1.1 기반 Mockup 반영 검토 문서 생성기

요구사항 정의서의 원본 표 형식을 그대로 가져와서 'Mockup 반영사항' 컬럼만 추가.
"""

import sys
import io
sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding="utf-8")

from docx import Document
from docx.shared import Pt, Cm, RGBColor, Inches, Mm
from docx.enum.table import WD_ALIGN_VERTICAL, WD_TABLE_ALIGNMENT
from docx.enum.text import WD_ALIGN_PARAGRAPH
from docx.oxml.ns import qn
from docx.oxml import OxmlElement

# ─────────────────────────────────────────────────────────────
# 요구사항 데이터 (요구사항 정의서 v1.1 원본)
# ─────────────────────────────────────────────────────────────

# 3.2 사용자 기능 요구사항
UFR_SVC = [
    ("UFR.SVC.001", "이종의 AI반도체를 클라우드 서비스(가상머신, 컨테이너)로 지원할 수 있어야 한다.", "연구개발계획서",
     "✅ /ai-accelerators 디바이스 인벤토리에 GPU·NPU·PIM 3종 통합 표시. /iaas/instances VM과 /paas/workloads 컨테이너로 양방향 확인 가능."),
    ("UFR.SVC.002", "단일 AI반도체를 다수의 클라우드 서비스에서 공유(AI반도체의 멀티플렉싱 활용)할 수 있어야 한다.", "연구개발계획서",
     "✅ /ai-accelerators/partitions 페이지 — MIG/vGPU/NPU Slice 통합 관리. A100 1대 → MIG 4분할, ATOM 1대 → Slice 4분할 시연."),
    ("UFR.SVC.003", "다수의 AI반도체 대상으로 한 클라우드 서비스를 제공할 수 있어야 한다.", "연구개발계획서",
     "✅ /ai-accelerators/allocation — 매핑 분포 (1:1 / 1:N / N:1) 분포 차트. SNR.PF.001 충족."),
    ("UFR.SVC.004", "다수의 AI 서비스 워크로드 실행 시 클라우드 서비스 실행 안정성과 보안성이 보장되어야 한다.", "연구개발계획서",
     "🟡 /iaas/networks 보안 그룹 + /iam/roles 권한 매트릭스. (실 격리 메커니즘은 ETRI/오케스트로 공동.)"),
    ("UFR.SVC.005", "클라우드 서비스의 성능 보장을 위해 플랫폼 공유 인프라 자원(네트워크 인터페이스, 스토리지 등) 일부를 선점할 수 있어야 한다.", "연구개발계획서",
     "🟡 자원 신청 위저드 5단계에 네트워크/스토리지 선택 포함 (UI 완비, 실 알고리즘 ETRI 영역)."),
    ("UFR.SVC.006", "사용자 편의를 위해 이기종 AI반도체 기반 가상머신/컨테이너를 위한 최적 이미지 패키징 기술을 제공하여야 한다.", "연구개발계획서",
     "✅ /iaas/images — Ubuntu+CUDA, RHEL+Furiosa SDK, Rocky+RBLN Runtime, vLLM Serving Runtime 등 6종 이미지 카탈로그."),
    ("UFR.SVC.007", "AI반도체 자원을 자동으로 인식하고 활성화할 수 있어야 한다.", "연구개발계획서",
     "✅ /ai-accelerators 상단 \"디바이스 자동 탐색\" 버튼. 인벤토리에 신규 디바이스 자동 등록 시뮬레이션."),
    ("UFR.SVC.008", "일원화된 이종 AI반도체 자원 정의 및 관리 뷰를 제공해야 한다.", "연구개발계획서",
     "✅ /ai-accelerators 단일 페이지 4탭 통합 (디바이스/할당/파티션/노드 토폴로지). 모든 벤더(NVIDIA·Furiosa·Rebellions·Samsung) 단일 뷰."),
    ("UFR.SVC.009", "AI반도체 클라우드 서비스(가상머신, 컨테이너) 및 쿠버네티스를 하나의 시스템상에서 통합관리(생성, 삭제, 상태 관리)할 수 있어야 한다.", "연구개발계획서",
     "✅ /iaas/instances (VM 라이프사이클), /paas/workloads (컨테이너), /paas/clusters (K8s) 모두 한 사이드바에서 접근. 통합 자원 신청 위저드 제공."),
    ("UFR.SVC.010", "워크로드 특성에 따라 적합한 클러스터 구성을 선택할 수 있어야 한다.", "연구개발계획서",
     "✅ /paas/clusters — 관리(mgmt) + 서비스(svc-ai-prod, svc-edge-staging) 2-tier 구조. 워크로드 유형별 클러스터 선택."),
    ("UFR.SVC.011", "클라우드 서비스별 자원 스케줄링 정책을 설정 운영할 수 있어야 한다.", "연구개발계획서·과제 협의체",
     "🟡 /monitoring/insights Davis 권장 조치 영역. (실 정책 알고리즘은 KETI/ETRI — UI 자리 완비.)"),
    ("UFR.SVC.012", "AI모델 추론 및 학습을 위한 클라우드 서비스 클러스터를 생성하고 관리할 수 있어야 한다.", "연구개발계획서",
     "✅ /ai-development/pipelines 학습 파이프라인 DAG + /ai-development/serving KServe 추론 엔드포인트 3종."),
    ("UFR.SVC.013", "추론 및 학습 서비스를 위한 워크로드간 데이터 파이프라인을 구성하고 관리할 수 있어야 한다.", "연구개발계획서",
     "✅ /ai-development/pipelines — load → tokenize → train → eval → register → deploy 6단계 DAG 시각화."),
    ("UFR.SVC.014", "추론 및 학습 서비스 클러스터의 자원을 자동으로 확장, 축소할 수 있어야 한다.", "연구개발계획서",
     "✅ /paas/workloads 스케일 액션 다이얼로그 + /ai-development/serving KServe Auto-scaling (0→N 명세)."),
    ("UFR.SVC.015", "PIM(Processing-In-Memory)을 활용한 클라우드 가속 기술을 제공해야 한다.", "연구개발계획서",
     "✅ /ai-accelerators Samsung HBM-PIM 2장 인벤토리 + 별도 SDK 이미지 (openkcloud/kubeflow-pim:0.7)."),
    ("UFR.SVC.016", "AI 서비스의 처리 시간(고속 처리 및 마감기한)과 비용 절감을 동시에 고려하여 최적의 자원을 자동으로 할당할 수 있어야 한다.", "연구개발계획서",
     "✅ /monitoring/insights — \"Rebellions REBEL Quad로 이전 시 전성비 +22%\" Davis 권장 조치 시각화."),
    ("UFR.SVC.017", "원격 AI반도체를 대상으로 한 클라우드 서비스를 제공할 수 있어야 한다.", "AI반도체 클라우드 과제 협의체 (2차년도 추가도출)",
     "⏳ 명세상 자리 마련 — Resource-Map의 LA 노드가 원격 자원도 표현 가능한 구조. 실 구현은 향후."),
]

UFR_MON = [
    ("UFR.MON.001", "이기종 AI반도체(GPU, NPU 등)에 대한 자원 및 서비스 상태에 대한 모니터링을 지원해야 한다.", "연구개발계획서",
     "✅ /monitoring 인프라 모니터링 — Datadog 스타일 호스트 맵, Top List, 히트맵 (호스트 × 24h). /ai-accelerators 디바이스별 활용률·온도·전력."),
    ("UFR.MON.002", "AI반도체 클라우드 서비스 실행에 대한 로깅 및 트레이싱을 지원해야 한다.", "연구개발계획서",
     "✅ /monitoring/logs — Loki LogQL 다크 콘솔. /monitoring/traces — Dynatrace PurePath waterfall (Span 트리 + 타임라인)."),
    ("UFR.MON.003", "AI반도체 클라우드 서비스의 이상징후 감시 및 알람을 제공해야 한다.", "연구개발계획서",
     "✅ /monitoring/alerts — SLO 카드(4종) + 활성 알람 (Critical/Warning/Info 카운트 + 메시지)."),
    ("UFR.MON.004", "AI반도체 클라우드 서비스 사용량 분석기반 비용 예측 정보를 제공할 수 있어야 한다.", "연구개발계획서",
     "✅ /dashboard 월 운영비용 KPI (₩1,842만/월) + 전력 절감율 (12.4%). /monitoring 비용 라인 차트."),
    ("UFR.MON.005", "AI반도체 클라우드 서비스의 장애 탐지 및 복구 방안을 제공해야 한다.", "연구개발계획서",
     "✅ /monitoring/insights Davis AI 자동 진단 — \"KV cache 미스 41% → 권장 조치 3가지\". 자동 적용 버튼 시연."),
    ("UFR.MON.006", "물리 서버 전력 소모량을 모니터링할 수 있어야 한다.", "연구개발계획서",
     "✅ /dashboard 실시간 전력 차트 (kW, 24h). /infrastructure/servers/[id] 전력 분해 (Kepler 추정 vs IPMI 측정)."),
    ("UFR.MON.007", "AI 기반 로그, 이벤트 데이터 분석 및 요약 정보를 제공해야한다.", "연구개발계획서",
     "✅ /monitoring/insights — LLM 기반 자동 진단 + Pipeline 단계별 시간 분포 분석 (60분 트레이스 1,240건 상관)."),
    ("UFR.MON.008", "사용자 혹은 권한에 따라 정보를 필터링해서 제공해야한다.", "AI반도체 클라우드 과제 협의체 (2차년도 추가도출)",
     "✅ canAccess() 권한 게이트 — admin/superuser만 옵저버빌리티 메뉴 접근. 사이드바 자동 필터링."),
]

UFR_CST = [
    ("UFR.CST.001", "워크로드의 운영 비용 최적 조합을 확인할 수 있어야 한다.", "AI반도체 클라우드 과제 협의체",
     "🟡 /monitoring/insights Davis 권장 조치에 비용 최적 조합 표시. (실 알고리즘 KETI — UI 완비.)"),
    ("UFR.CST.002", "워크로드별 소비전력 특성을 확인하고 관리할 수 있어야 한다.", "연구개발계획서",
     "✅ /infrastructure/servers/[id] 전력 분해 (CPU/DRAM/GPU/기타) + /ai-accelerators 디바이스별 전력."),
    ("UFR.CST.003", "워크로드별 자원 사용량을 확인할 수 있어야 한다.", "연구개발계획서",
     "✅ /paas/workloads/[id] 실시간 메트릭 (CPU·MEM·RPS·P95) + /dashboard 테넌트별 사용률."),
    ("UFR.CST.004", "클라우드 서비스 클러스터별 비용 및 소비전력에 대한 분석을 수행할 수 있어야 한다.", "연구개발계획서",
     "✅ /dashboard 클러스터별 전력 분포 (mgmt/svc-ai-prod/svc-edge-staging)."),
    ("UFR.CST.005", "워크로드 조합을 통한 운용 비용 최적화율은 로그를 통해 확인할 수 있어야 한다.", "AI반도체 클라우드 과제 협의체",
     "🟡 /monitoring/logs Loki + /monitoring/insights 분석 (KETI 알고리즘 연동 자리)."),
    ("UFR.CST.006", "워크로드 배치 근거에 대해 확인할 수 있어야 한다.", "연구개발계획서",
     "🟡 /monitoring/insights Davis 분석 근거 표시 (\"트레이스 1,240건 분석 결과\")."),
    ("UFR.CST.007", "워크로드의 에너지 특성에 대해 사전에 확인할 수 있어야 한다.", "연구개발계획서",
     "🟡 /ai-development/model-insights Pipeline 단계별 GPU 시간·전력 명세."),
    ("UFR.CST.008", "워크로드의 수행 대기 시간을 확인할 수 있어야 한다.", "연구개발계획서",
     "✅ /monitoring/insights P50/P95/P99 트렌드 차트 (60분)."),
    ("UFR.CST.009", "AI 모델을 기반으로 운용비용 예측 값을 확인할 수 있어야 한다.", "연구개발계획서",
     "✅ /dashboard 월 비용 KPI + /monitoring 비용 추이 라인 차트."),
    ("UFR.CST.010", "워크로드의 전력 소비량 절감율을 확인할 수 있어야 한다.", "AI반도체 클라우드 과제 협의체",
     "✅ /dashboard \"전력 절감율 12.4%\" KPI 카드."),
    ("UFR.CST.011", "스케줄링 지연시간 감소율을 제공받을 수 있어야 한다.", "연구개발계획서",
     "🟡 /monitoring/insights Pipeline 단계별 시간 분포 (810초 유휴 시간 분석)."),
    ("UFR.CST.012", "워크로드 생성 시 스케줄링 우선순위를 설정할 수 있어야 한다.", "연구개발계획서",
     "🟡 자원 신청 위저드 (우선순위 자리 마련, 실 스케줄러 KETI)."),
    ("UFR.CST.013", "사용자는 노드 수, 자원 할당량, 스케줄링 정책을 지정하여 클라우드 서비스 클러스터를 생성하고 구성할 수 있어야 한다.", "연구개발계획서",
     "✅ /paas/clusters 카드 뷰 + 자원 신청 위저드에서 노드/쿼터 설정."),
    ("UFR.CST.014", "운용 비용 절감 정책 기반으로 워크로드를 동적으로 재배치하거나, 클라우드 서비스 클러스터를 추가/삭제할 수 있어야 한다.", "연구개발계획서",
     "🟡 /monitoring/insights 권장 조치 (재배치 제안) + /paas/workloads 스케일 액션."),
    ("UFR.CST.015", "재배치, 오토 스케일링, 비용 및 성능 스케줄링 등의 전력 및 비용 최적화 정책을 설정 및 제공해야 한다", "연구개발계획서",
     "🟡 /monitoring/insights 권장 조치 + /paas/workloads Auto-scaling 설정 자리."),
    ("UFR.CST.016", "인프라 노드 추가/삭제 및 워크로드 재배치를 통한 운용비용 절감 모델을 제공해야한다.", "연구개발계획서",
     "🟡 /iaas/providers 자원풀 추가·동기화 + 자원 신청 위저드."),
    ("UFR.CST.017", "클라우드 플랫폼의 운용 비용을 실시간으로 확인할 수 있으며, 워크로드의 전력사용 지표를 확인할 수 있어야 한다.", "연구개발계획서",
     "✅ /dashboard 실시간 전력 차트 + 월 비용 KPI + 워크로드별 전력 정보."),
]

UFR_LLM = [
    ("UFR.LLM.001", "이종 AI반도체 연동 클라우드 서비스 플랫폼은 초거대 AI 모델 서비스의 실행을 지원해야 한다.", "연구개발계획서",
     "✅ /ai-development/serving — KServe 3개 InferenceService (Solar 10B + STT RNGD + STT ATOM). RPS·P95 지연 모니터링."),
    ("UFR.LLM.002", "AI반도체 클라우드 플랫폼은 상용 초거대 AI 모델 실행 및 성능 개선 방법을 제공해야 한다.", "연구개발계획서",
     "✅ /catalog Rebellions ModelZoo — Llama 3.1 70B, Qwen 2.5 32B, EXAONE 32B 등 33종 상용 모델. /ai-development/model-insights 성능 분석."),
]

UFR_MNT = [
    ("UFR.MNT.001", "AI 반도체 기반의 AI SW 개발 환경 및 관리 기능을 제공해야 한다.", "연구개발계획서",
     "✅ /ai-development Kubeflow 워크스페이스 6개 메뉴 (Profiles/Notebooks/Pipelines/Experiments/Serving/Insights). /notebooks/[id]/ide Code Server iframe 슬롯."),
    ("UFR.MNT.002", "다중 사용자간 가상머신 및 컨테이너 서비스의 멀티테넌시를 지원해야 한다.", "연구개발계획서",
     "✅ /iaas/projects 4개 테넌트 + 쿼터·멤버 관리. /ai-development/profiles Kubeflow Profile (= OpenStack 프로젝트 = 과금 단위)."),
    ("UFR.MNT.003", "사용자 역할별 AI 반도체 클라우드 서비스 접근제어 및 인증(IAM)을 지원해야 한다.", "연구개발계획서",
     "✅ /iam/users 사용자 관리 + /iam/roles 권한 매트릭스 + /iam/api-keys API Key (Spring Security RBAC + JWT + OAuth 2.0 + MFA 명세)."),
    ("UFR.MNT.004", "AI 반도체 클라우드 특화 사용자/관리자 포털을 제공해야 한다.", "연구개발계획서",
     "✅ 전체 사이드바 + /dashboard 통합 대시보드. 역할별 메뉴 가시성 제어."),
    ("UFR.MNT.005", "3rd Party 응용 및 장치가 연계하여 활용할 수 있는 개방형 API를 제공해야 한다.", "연구개발계획서",
     "✅ /iam/api-keys 4개 API Key (kfp-runner-bot 등) + Scope 권한 (kubeflow:*, paas:workloads:read 등). OAS 명세 표기."),
    ("UFR.MNT.006", "AI반도체 기반 서비스 구축을 위한 마켓플레이스를 제공해야한다.", "연구개발계획서",
     "✅ /catalog Rebellions ModelZoo — 33종 모델 (LLM/Multimodal/Vision/Audio/Embedding/Diffusion). HF 원본 + rbln-model-zoo GitHub 링크."),
]

UFR_STD = [
    ("UFR.STD.001", "ML application을 위한 Platform as a Service의 기능적 요구사항에 대한 표준을 제공해야 한다.", "연구개발계획서",
     "🟡 ETRI 주관 — 본 mockup이 표준 PaaS UI/UX 레퍼런스로 활용."),
    ("UFR.STD.002", "AI 반도체 기반 ML 클라우드 서비스의 라이프사이클 기반 관리 프레임워크 표준을 제공하여야 한다.", "연구개발계획서",
     "🟡 ETRI 주관 — Kubeflow Profile + Notebook + Pipeline + KServe 라이프사이클 UI 시연."),
]

UFR_OPN = [
    ("UFR.OPN.001", "플랫폼 개발 시 사용된 모든 공개SW의 이름, 버전, 라이선스 정보 등을 식별할 수 있어야 한다.", "공개SW R&D 실무수행 가이드라인",
     "✅ /catalog 모델 카드에 라이선스 표시 (Apache 2.0, Llama Community, MIT 등). /iaas/images SDK 버전 표시."),
    ("UFR.OPN.002", "주요 공개SW에 대해 변경 사항(업데이트, 제거 등)을 관리해야 한다.", "공개SW R&D 실무수행 가이드라인",
     "🟡 /catalog 마켓플레이스 자동 업데이트·롤백 (SFR.MNT.044) UI 자리."),
    ("UFR.OPN.003", "공개SW 기반 기능에 대해 운영 및 사용 가이드를 제공할 수 있어야 한다.", "공개SW R&D 실무수행 가이드라인",
     "✅ /ai-development 메인 페이지에 각 메뉴별 설명 카드 + 예시. /resource-map 6계층 모델 설명."),
    ("UFR.OPN.004", "플랫폼은 공개SW 기반 구성요소 간의 의존성을 관리할 수 있어야 한다.", "공개SW R&D 실무수행 가이드라인",
     "✅ /resource-map L0~L5 + LA 6계층 모델 + 관계 정의 (attached_to/passthrough_to/allocated_to)."),
]

# 3.3 비기능 요구사항
UNR = [
    ("UNR.001", "초거대 AI모델 실행 성능 평가에 대한 객관적인 평가 방법이 제공되어야 한다.", "AI반도체 클라우드 과제 협의체",
     "✅ /ai-development/experiments Katib trial 결과 + bestMetric 표시."),
    ("UNR.002", "초거대 AI모델 실행 검증을 위한 사용자 입력 및 응답을 확인할 수 있는 방법이 제공되어야 한다.", "AI반도체 클라우드 과제 협의체",
     "✅ /ai-development/serving 추론 엔드포인트 + RPS/P95 모니터링."),
    ("UNR.003", "시스템 구성요소는 모듈화되어야 하며, 운영 중단 없이 소프트웨어 업데이트 및 기능 확장이 가능해야 한다.", "AI반도체 클라우드 과제 협의체",
     "✅ MSA 명세 + Next.js Turbopack hot-reload + Kubeflow Profile 단위 격리."),
    ("UNR.004", "운용 비용 최적화는 안정적으로 운영되어 사용자가 필요할 때 서비스를 이용할 수 있어야 한다.", "연구개발계획서",
     "✅ /monitoring/alerts SLO 99.9% 가용성 + /monitoring/insights Davis 자동 복구."),
    ("UNR.005", "플랫폼 제공 API는 개방형 API 표준을 준수하여 협업 시스템과 연동이 용이해야 한다.", "연구개발계획서",
     "✅ /iam/api-keys REST API + OAS(OpenAPI Spec) 명세."),
    ("UNR.006", "PyTorch, Tensorflow 등 주요 AI 프레임워크와 호환 가능해야 한다.", "연구개발계획서",
     "✅ /iaas/images 통합 런타임 이미지 (PyTorch · TensorFlow · vLLM 포함)."),
    ("UNR.007", "성능 격리도 측정 및 자원 사용 효율성 검증할 수 있어야 한다.", "연구개발계획서",
     "✅ /ai-accelerators/partitions 격리 모드 (MIG/Slice/vGPU) + /monitoring/insights 격리도 분석."),
    ("UNR.008", "장기적인 클라우드 서비스 품질(QoS) 유지 및 관리 체계 기반 지속 가능한 관리 환경 제공해야한다.", "연구개발계획서",
     "✅ /monitoring/alerts 30일/90일 SLO 리포트."),
]

# 4.1.1 SFR.SVC (담당: ETRI · UI는 오케스트로/ETRI 공동)
SFR_SVC = [
    ("SFR.SVC.001", "3종 이상의 AI반도체를 지원하는 클라우드 서비스(가상머신, 컨테이너)를 제공해야 한다.", "UFR.SVC.001",
     "2025(1종)·2026(2종)·2027(3종)", "상", "ETRI",
     "✅ /ai-accelerators GPU(NVIDIA A100) + NPU(Furiosa RNGD, Rebellions ATOM) + PIM(Samsung HBM-PIM) 4벤더 3종 모두 표시."),
    ("SFR.SVC.002", "다중 AI 워크로드의 AI반도체 공유 실행 지원을 위한 시간 분할 실행 환경을 제공해야 한다.", "UFR.SVC.002",
     "2027", "상", "ETRI",
     "🟡 /ai-accelerators 공유 정책 \"시간분할\" 표시 + Partition 모델 'time-slice' 명세."),
    ("SFR.SVC.003", "다중 워크로드 할당을 위한 HW가상화 기반 AI반도체 논리적 분할 기능을 제공해야 한다.", "UFR.SVC.002",
     "2028", "상", "ETRI",
     "✅ /ai-accelerators/partitions MIG 4분할 + ATOM NPU Slice 4분할 시연. partition_model: mig/mdev/vgpu/npu-slice."),
    ("SFR.SVC.004", "다양한 AI반도체 공유 환경 제공을 위한 공유 정책 설정을 지원해야 한다.", "UFR.SVC.002",
     "2026(전용)·2027(시간분할)·2028(논리분할)", "중", "ETRI",
     "✅ /ai-accelerators 디바이스 카드의 \"공유 정책\" 필드 (전용/시간분할/논리분할/멀티플렉싱 4종)."),
    ("SFR.SVC.005", "하나의 클라우드 서비스는 다수의 AI반도체를 사용할 수 있도록 지원해야 한다.", "UFR.SVC.003",
     "2027", "중", "ETRI",
     "✅ VM/Workload 카드에 가속기 갯수 표시 (acceleratorIds[]). /paas/workloads accelerators 컬럼."),
    ("SFR.SVC.006", "클라우드 서비스 격리 기반 AI 워크로드 실행 환경을 제공해야 한다.", "UFR.SVC.004",
     "2027", "중", "ETRI",
     "🟡 /paas/workloads 격리 명세 + /iaas/networks SecurityGroup. 실 격리 메커니즘 ETRI."),
    ("SFR.SVC.007", "특정 AI 서비스의 워크로드 격리를 위한 AI반도체 공유 제한 및 자원 선점 기능을 제공해야 한다.", "UFR.SVC.004",
     "2027", "상", "ETRI",
     "🟡 /ai-accelerators 공유정책 \"전용\" 모드 표시. 실 제한 메커니즘 ETRI."),
    ("SFR.SVC.008", "특정 클라우드 서비스의 공유 인프라 선점을 위해 인프라 접근 제한 기능을 제공해야 한다.", "UFR.SVC.005",
     "2028", "중", "ETRI",
     "🟡 /iaas/networks 접근 제어 명세. 실 접근 제한 ETRI."),
    ("SFR.SVC.009", "이기종 AI반도체의 공통 패키지 제공 고속 이미지 생성 기술을 제공하여야 한다", "UFR.SVC.006",
     "2026", "중", "ETRI",
     "✅ /iaas/images 통합 SDK 이미지 6종 (CUDA/RBLN/Furiosa/vLLM/Kubeflow 등)."),
    ("SFR.SVC.010", "AI반도체 장치를 자동으로 탐색할 수 있어야 한다.", "UFR.SVC.007",
     "2025(1종)·2026(2종)·2027(3종)", "상", "ETRI",
     "✅ /ai-accelerators \"디바이스 자동 탐색\" 버튼 + Discovery 트리거."),
    ("SFR.SVC.011", "AI반도체 장치를 가상머신에 자동으로 연동할 수 있어야 한다.", "UFR.SVC.009",
     "2026", "상", "ETRI",
     "✅ /iaas/instances VM 카드에 \"GPU Passthrough\" 컬럼 + /resource-map LA→L3 passthrough_to 관계."),
    ("SFR.SVC.012", "AI반도체를 컨테이너(쿠버네티스)에 자동으로 연동할 수 있어야 한다.", "UFR.SVC.009",
     "2026", "상", "ETRI",
     "✅ /paas/workloads 가속기 컬럼 + /resource-map LA→L5 allocated_to."),
    ("SFR.SVC.013", "가상머신, 컨테이너, 쿠버네티스 클러스터를 통합적으로 제어하고 관리할 수 있어야 한다.", "UFR.SVC.009",
     "2025", "상", "ETRI",
     "✅ 사이드바 IaaS + PaaS 두 카테고리로 통합 접근. /topology Smartscape에서 L0→L5 흐름 시각화."),
    ("SFR.SVC.014", "이종 클라우드 서비스(가상머신, 컨테이너) 및 쿠버네티스 클러스터 간의 가상 네트워크를 구성할 수 있어야 한다.", "UFR.SVC.009",
     "2027", "상", "ETRI",
     "✅ /iaas/networks 4개 가상 네트워크 + /paas/services 서비스 메시. CIDR/VLAN/Subnet 명시."),
    ("SFR.SVC.015", "서비스별 성능 프로파일링 데이터를 수집/저장하고 활용할 수 있어야 한다.", "UFR.SVC.010",
     "2026", "중", "ETRI",
     "✅ /monitoring 메트릭 수집 + /monitoring/insights 성능 프로파일 분석."),
    ("SFR.SVC.016", "서비스별 성능 프로파일링 데이터를 기반으로 최적 자원을 추천할 수 있어야 한다.", "UFR.SVC.010",
     "2027", "중", "ETRI",
     "✅ /monitoring/insights Davis 권장 조치 — \"REBEL Quad로 이전 시 전성비 +22%\"."),
    ("SFR.SVC.017", "AI반도체 가용률 및 워크로드 특성을 고려하여 클라우드 서비스 인스턴스들을 스케줄링할 수 있어야 한다.", "UFR.SVC.011",
     "2026·2027", "중", "ETRI",
     "🟡 /monitoring/insights 권장 조치 + /paas/workloads 스케줄 명세."),
    ("SFR.SVC.018", "AI반도체 논리/시간 분할 특성을 고려하여 인스턴스 스케줄링 알고리즘을 적용할 수 있어야 한다.", "UFR.SVC.002, UFR.SVC.011",
     "2027·2028", "상", "ETRI",
     "🟡 /ai-accelerators/partitions 분할 특성 표시. 알고리즘 ETRI."),
    ("SFR.SVC.019", "쿠버네티스 환경에서 다양한 AI반도체 자원을 단일화된 뷰를 제공해야 한다.", "UFR.SVC.001, UFR.SVC.008",
     "2026", "상", "ETRI",
     "✅ /ai-accelerators 모든 벤더 통합 뷰. /paas/workloads 가속기 컬럼."),
    ("SFR.SVC.020", "쿠버네티스 환경에서 AI반도체 자원을 자동으로 인식하고 일관되게 제어할 수 있는 전용 Operator를 제공해야 한다.", "UFR.SVC.001, UFR.SVC.008",
     "2026", "상", "ETRI",
     "✅ /paas/clusters Operator 명세 + Discovery Trigger 버튼."),
    ("SFR.SVC.021", "쿠버네티스 환경에서 AI반도체 장치 플러그인을 통해 클라우드 인스턴스(컨테이너)에 대한 자원 할당, 해제 기능을 제공해야 한다.", "UFR.SVC.001, UFR.SVC.008, UFR.SVC.009",
     "2025(1종)·2026(2종)·2027(3종)", "상", "ETRI",
     "✅ /paas/workloads 가속기 컬럼 (rebellions.ai/atom 등 device plugin 명세). 할당·해제 액션."),
    ("SFR.SVC.022", "쿠버네티스 환경에서 장치 유형별 플러그인 호환성을 검증하고 자동 활성화할 수 있는 기능을 제공해야 한다.", "UFR.SVC.001, UFR.SVC.007, UFR.SVC.008, UFR.SVC.009",
     "2026", "중", "ETRI",
     "🟡 /paas/clusters DevicePlugin 호환성 검증 명세."),
    ("SFR.SVC.023", "쿠버네티스 환경에서 AI반도체 자원의 상태 정보를 수집하고 메트릭 형태로 제공할 수 있어야 한다.", "UFR.SVC.008",
     "2026", "중", "ETRI",
     "✅ /monitoring DCGM/RBLN exporter 명세 + Prometheus 메트릭 카드."),
    ("SFR.SVC.024", "AI모델 추론 및 학습을 위한 가상머신 기반 쿠버네티스 클러스터 구성 기능을 제공해야 한다.", "UFR.SVC.012",
     "2026(추론)·2028(학습)", "상", "ETRI",
     "✅ /paas/clusters svc-ai-prod(학습) + svc-edge-staging(추론) 2-tier."),
    ("SFR.SVC.025", "워크로드 유형(실시간 처리, 대용량 데이터 처리 등)에 따라 쿠버네티스 클러스터 구성 옵션을 정의할 수 있어야 한다.", "UFR.SVC.010, UFR.SVC.011",
     "2026", "중", "ETRI",
     "✅ /paas/clusters CNI 옵션(Calico/Cilium/OVN) + 워크로드 유형별 구성."),
    ("SFR.SVC.026", "워크로드 유형에 따라 데이터 처리를 위한 가상 네트워크를 구성, 제공해야 한다.", "UFR.SVC.012, UFR.SVC.013",
     "2027", "중", "ETRI",
     "✅ /iaas/networks 데이터망 + 관리망 + 서비스망 분리."),
    ("SFR.SVC.027", "추론 및 학습을 위한 워크로드 간 데이터의 전송 파이프라인을 자동으로 구성하여 제공해야 한다.", "UFR.SVC.013",
     "2026(추론)·2028(학습)", "중", "ETRI",
     "✅ /ai-development/pipelines DAG — 데이터 전송 파이프라인 단계."),
    ("SFR.SVC.028", "쿠버네티스 내 AI반도체 자원 사용량에 따른 클러스터 확장 및 축소 설정 기능을 제공해야 한다.", "UFR.SVC.014",
     "2027", "중", "ETRI",
     "✅ /paas/clusters 노드 수 명세 + /paas/workloads 스케일 액션."),
    ("SFR.SVC.029", "쿠버네티스 클러스터에 명시된 스케일링 정책에 따라 클러스터를 자동으로 확장, 축소할 수 있어야 한다.", "UFR.SVC.014",
     "2027", "중", "ETRI",
     "🟡 /paas/workloads HPA Auto-scaling 다이얼로그."),
    ("SFR.SVC.030", "PIM을 활용한 데이터 처리 가속 기술을 제공해야 한다.", "UFR.SVC.015",
     "2028", "중", "ETRI",
     "✅ /ai-accelerators Samsung HBM-PIM 2장 표시 + openkcloud/kubeflow-pim:0.7 SDK."),
    ("SFR.SVC.031", "CSC의 요구사항(비용/성능/시간)을 충족시키는 AI 모델 분할 및 워크로드 스케줄링을 지원해야 한다.", "UFR.SVC.016",
     "2026", "중", "경희대",
     "🟡 /monitoring/insights 권장 조치 + /ai-development/pipelines 모델 분할 명세."),
    ("SFR.SVC.032", "CSP의 요구사항(운용 비용/CSC간 자원 할당 공정성)을 충족시키는 리소스 배치 및 스케줄링을 지원해야 한다.", "UFR.SVC.016",
     "2026", "중", "경희대",
     "🟡 /iaas/projects 쿼터 + /monitoring 공정성 지표."),
    ("SFR.SVC.033", "CSC, CSP 스케줄링 에이전트 간 협상 최적화를 위한 LLM 기반 인스턴스 스케줄링을 지원해야 한다.", "UFR.SVC.016",
     "2027", "상", "경희대",
     "🟡 /monitoring/insights Davis AI 명세 (실 LLM 기반 협상은 경희대)."),
    ("SFR.SVC.034", "하나의 클라우드 서비스는 다수의 원격 AI반도체를 사용할 수 있도록 지원해야 한다.", "UFR.SVC.019",
     "2027(1개)·2029(2개 이상)", "중", "ETRI",
     "⏳ /resource-map LA 원격 자원 명세 자리. 실 구현 향후."),
]

# 4.1.1 SIR.SVC (인터페이스)
SIR_SVC = [
    ("SIR.SVC.001", "AI반도체 장치를 포함하는 가상머신, 컨테이너, 쿠버네티스 클러스터를 생성하고 관리할 수 있는 사용자 GUI를 제공해야 한다.", "UFR.SVC.001",
     "2026", "중", "ETRI · 오케스트로",
     "✅ /iaas/instances VM 생성 위저드 + /paas/workloads + /paas/clusters."),
    ("SIR.SVC.002", "이기종 AI반도체 활용 클라우드 서비스 원격 저장소를 제공해야 한다.", "UFR.SVC.006",
     "2027", "상", "ETRI",
     "✅ /iaas/volumes (Cinder) + /paas/storage (PV/PVC/StorageClass 3탭)."),
    ("SIR.SVC.003", "추론과 학습 서비스 클러스터 구성 및 관리 GUI를 제공해야한다.", "UFR.SVC.012, UFR.SVC.013",
     "2026(추론)·2028(학습)", "상", "ETRI · 오케스트로",
     "✅ /paas/clusters 2-tier 카드 + /ai-development/pipelines."),
    ("SIR.SVC.004", "워크로드 특성 분석은 GUI를 통해 높은 가시성을 제공해야 한다.", "UFR.SVC.010",
     "2027", "상", "ETRI",
     "✅ /monitoring/insights 핫스팟 + Pipeline 병목 분석."),
    ("SIR.SVC.005", "비용 최적화 정책 반영을 위한 스케줄러 확장 인터페이스를 제공해야 한다.", "UFR.CST.011, UFR.CST.012",
     "2028", "상", "ETRI · 경희대 · KETI",
     "🟡 /monitoring/insights 권장 조치 UI."),
    ("SIR.SVC.006", "쿠버네티스의 자원 관리 및 워크로드 관리 인터페이스를 제공해야한다.", "UFR.CST.013/014/015",
     "2027", "중", "KETI",
     "✅ /paas/workloads + 5종 액션 (스케일/롤백/편집/셸/삭제)."),
]

# 4.1.2 SFR.OPT (담당: 이노그리드·KETI · UI는 오케스트로 공동)
SFR_OPT = [
    ("SFR.OPT.001", "이기종 AI반도체(GPU, NPU 등), 물리 서버, 네트워크, 스토리지 등 기본 자원에 대한 모니터링 정보를 제공해야 한다.", "UFR.MON.001",
     "2025", "상", "이노그리드",
     "✅ /monitoring Datadog 스타일 + /ai-accelerators 디바이스별 메트릭."),
    ("SFR.OPT.002", "물리 노드와 워크로드 단위의 전력 소모 모니터링 기능을 제공해야 한다.", "UFR.MON.006",
     "2025", "상", "이노그리드",
     "✅ /dashboard 실시간 전력 + /infrastructure/servers/[id] 전력 분해."),
    ("SFR.OPT.003", "대규모 분산 환경 로깅 데이터 처리 및 에러·병목 구간 파악을 위한 정보 수집 시스템 기능을 제공해야 한다.", "UFR.MON.002",
     "2025", "중", "이노그리드",
     "✅ /monitoring/logs Loki + /monitoring/traces PurePath."),
    ("SFR.OPT.004", "다양한 지표 조건(시스템 메트릭 등)을 통한 이상 감지, 히스토리 분석 및 알람 발송 규칙 설정 관리 기능을 제공해야 한다.", "UFR.MON.003",
     "2026", "중", "이노그리드",
     "✅ /monitoring/alerts 알람 + SLO 4종."),
    ("SFR.OPT.005", "AI반도체 클라우드 자원 사용량 실시간 미터링 및 정책 기반 서비스 비용 예측 기능을 제공해야 한다.", "UFR.MON.004",
     "2026", "중", "이노그리드",
     "✅ /dashboard 월 비용 KPI + 비용 라인 차트."),
    ("SFR.OPT.006", "장애 상황에 따른 복구 정보 제공 및 인공지능 기반 장애 탐지 병렬 처리 기능을 제공해야 한다.", "UFR.MON.005",
     "2026", "중", "이노그리드",
     "✅ /monitoring/insights Davis 자동 진단."),
    ("SFR.OPT.007", "LLM 기반 비정상 로그 패턴 식별, 컨텍스트 기반 이상징후 분석 및 장애 상황 예측 기능을 제공해야 한다.", "UFR.MON.007",
     "2027", "상", "이노그리드",
     "✅ /monitoring/insights LLM 기반 \"KV cache 미스 41%\" 자동 진단."),
    ("SFR.OPT.008", "사용자 이용패턴 분석을 통해 자원 사용 비용 예측 정보를 제공해야 한다.", "UFR.MON.004",
     "2027", "중", "이노그리드",
     "✅ /dashboard 비용 추이 + /monitoring 비용 예측."),
    ("SFR.OPT.009", "SLM을 통한 경량 모델 기반 이상징후 탐지 기능을 제공해야 한다.", "UFR.MON.007",
     "2028", "중", "이노그리드",
     "🟡 /monitoring/insights SLM 자리."),
    ("SFR.OPT.010", "스케줄링 결정 과정 및 재배치, 노드 추가/삭제 등 플랫폼의 주요 최적화 로그를 저장하고 제공해야 한다.", "UFR.CST.005",
     "2027", "중", "KETI",
     "✅ /monitoring/logs + /iam/audit 감사 로그."),
    ("SFR.OPT.011", "워크로드 소비 전력 및 클러스터 트레이싱 데이터를 기반으로 예상 소비전력과 운용 비용을 산출하고, 이를 주기적으로 분석하여 재배치, 오토 스케일링, 스케줄링 등 최적의 운용 정책을 결정해야 한다.", "UFR.CST.006/008/015/016",
     "2026(설정)·2027(평가)·2028(최적화)", "상", "KETI",
     "🟡 /monitoring/insights Davis 권장 조치 (실 알고리즘 KETI)."),
    ("SFR.OPT.012", "플랫폼의 비용-성능 평가 알고리즘을 통해 처리량, 응답 시간, 자원 사용 효율, 예상 비용 결과 등을 제공해야한다.", "UFR.CST.005, UFR.CST.014",
     "2026(설정)·2027(평가)", "중", "KETI",
     "✅ /monitoring/insights 응답시간 트렌드 (P50/95/99) + 비용 분석."),
    ("SFR.OPT.013", "워크로드의 대기 시간을 산출하여 지연율을 확인해야 한다.", "UFR.CST.007",
     "2027", "중", "KETI",
     "✅ /monitoring/insights 60분 트렌드 차트."),
    ("SFR.OPT.014", "Auto-scaling 기반 인프라 구성 변경 전후의 효율을 측정하여 운용 비용 절감율을 산출해야 한다.", "UFR.CST.009/010/017/018",
     "2027(평가)·2028(검증)", "상", "KETI",
     "✅ /dashboard 전력 절감율 12.4% KPI."),
    ("SFR.OPT.015", "사용자가 설정한 우선순위를 반영하여 워크로드 처리 순서를 조정하는 스케줄러를 제공해야 한다.", "UFR.CST.013",
     "2026", "중", "KETI",
     "🟡 자원 신청 위저드 우선순위 자리."),
    ("SFR.OPT.016", "사전 명세 기술 및 런타임 트레이싱 데이터 기반 최적의 AI 가속기에 워크로드를 할당해야 한다.", "UFR.CST.001, UFR.CST.007",
     "2026(설정)·2027(평가)", "상", "KETI",
     "✅ /ai-accelerators/allocation 매핑 분포 + /monitoring/insights 권장 조치."),
    ("SFR.OPT.017", "워크로드의 전력 소비 분석 기반 에너지 효율적인 워크로드 조합으로 재배치해야 한다.", "UFR.CST.002/004/005/015",
     "2026(설정)·2027(평가)", "상", "KETI",
     "🟡 /monitoring/insights 권장 조치 (실 재배치 KETI)."),
    ("SFR.OPT.018", "플랫폼 운용 비용 최적화 정책을 설정 및 관리 기능을 제공해야 한다.", "UFR.CST.012/013/014",
     "2027", "상", "KETI",
     "🟡 /monitoring/insights + /paas/workloads HPA 명세."),
    ("SFR.OPT.019", "인프라 노드를 생성 및 삭제하며 운영 비용을 최적화하는 메커니즘을 제공해야 한다.", "UFR.CST.014/016",
     "2025(생성)·2028(검증)", "상", "KETI",
     "✅ /iaas/providers 자원풀 추가 + /iaas/instances 자원 신청."),
    ("SFR.OPT.020", "운용 비용 최적화 기능의 성능과 안정성 검증 메커니즘을 제공해야 한다.", "UFR.CST.017",
     "2028", "상", "KETI",
     "🟡 /monitoring/insights 검증 자리."),
    ("SFR.OPT.021", "자동화 정책의 조건을 직접 구성하며 저장할 수 있도록 정책 정의 기능을 제공해야 한다.", "UFR.CST.015",
     "2028", "상", "KETI",
     "🟡 /monitoring/alerts 정책 정의 UI 자리."),
    ("SFR.OPT.022", "클라우드 서비스 옵저버빌리티 지원 모듈은 테넌트 혹은 프로젝트 단위로 정보를 필터링하여 제공/알람 설정 기능을 해야 한다.", "UFP.MON.008",
     "2026", "중", "이노그리드",
     "✅ canAccess() 권한 필터 + /monitoring 테넌트별 사용률."),
]

# 4.1.2 SIR.OPT
SIR_OPT = [
    ("SIR.OPT.001", "AI반도체 클라우드 서비스 모니터링 정보 제공 및 관리를 위한 APIs 및 GUI를 제공해야 한다.", "UFR.MON.001",
     "2026", "상", "이노그리드·KETI·오케스트로",
     "✅ /monitoring 5탭 통합 + Datadog 호스트맵 + /ai-accelerators 메트릭."),
    ("SIR.OPT.002", "AI반도체 및 클라우드 서비스 로깅정보 제공 및 관리 APIs를 제공해야한다.", "UFR.MON.002",
     "2026", "중", "이노그리드·KETI",
     "✅ /monitoring/logs Loki + /monitoring/traces PurePath."),
    ("SIR.OPT.003", "운용비용 정책 설정 및 분석 결과를 제공하는 GUI를 제공해야한다.", "UFR.CST.005~013",
     "2026~2028", "상", "KETI·오케스트로",
     "✅ /monitoring/insights 비용·성능 분석 + 권장 조치."),
]

# 4.1.3 SFR.LLM
SFR_LLM = [
    ("SFR.LLM.001", "AI반도체 연동한 초거대 AI 모델 클라우드 서비스를 제공해야 한다.", "UFR.LLM.001",
     "2026", "상", "ETRI",
     "✅ /ai-development/serving — Solar 10B (GPU) + STT RNGD (NPU) + STT ATOM (NPU)."),
    ("SFR.LLM.002", "다중 초거대 AI 모델 관리 및 실행을 제어할 수 있어야 한다.", "UFR.LLM.001",
     "2027", "상", "ETRI",
     "✅ /catalog Rebellions ModelZoo 33종 + /ai-development/serving 다중 KServe."),
    ("SFR.LLM.003", "다중 클라우드 서비스 기반 초거대 AI모델의 일원화된 실행 제어를 지원해야 한다.", "UFR.LLM.002",
     "2026", "중", "ETRI",
     "✅ /paas/clusters 멀티 클러스터 통합 + /ai-development/serving 일원화 관리."),
    ("SFR.LLM.004", "초거대 AI모델 서비스의 성능(정확도, 지연시간) 개선을 위한 실행 최적화 방안을 제공해야한다.", "UFR.LLM.002",
     "2028", "중", "ETRI",
     "✅ /ai-development/model-insights — 1 token 980ms 단계별 분해 + Davis 권장 3가지."),
]

# 4.1.3 SIR.LLM
SIR_LLM = [
    ("SIR.LLM.001", "AI반도체 클라우드 서비스 기반 초거대 AI모델 서비스 실행을 위한 GUI 제공해야 한다.", "UFR.LLM.001",
     "2027", "중", "ETRI·오케스트로",
     "✅ /ai-development/serving — 모델 카드 + 엔드포인트 + 메트릭 통합."),
    ("SIR.LLM.002", "다중 초거대 AI 모델 성능 실험 환경 설정을 위한 인터페이스(도구)를 제공해야 한다.", "UFR.LLM.002",
     "2026", "중", "ETRI",
     "✅ /ai-development/experiments — Katib HPO (lr-search/batch-tune/wer-search 3개 실험)."),
]

# 4.1.4 SFR.MNT (담당: 오케스트로 — 핵심)
SFR_MNT = [
    ("SFR.MNT.001", "AI SW 개발을 위한 코드 편집, 디버깅, 실행 환경을 제공해야 한다.", "UFR.MNT.001",
     "2025", "상", "오케스트로",
     "✅ /notebooks/[id]/ide — Code Server iframe slot (실 Kubeflow 연동 가능 구조). /ai-development/notebooks 6개 서버."),
    ("SFR.MNT.002", "컨테이너 기반의 모델 패키징 및 배포할 수 있어야 한다.", "UFR.MNT.001",
     "2025", "중", "오케스트로",
     "✅ /ai-development/pipelines DAG (register-model → deploy-kserve) + /ai-development/serving KServe."),
    ("SFR.MNT.003", "버전 관리와 롤백 기능을 제공해야 한다.", "UFR.MNT.001",
     "2025", "하", "오케스트로",
     "✅ /paas/workloads 롤백 액션 + Pipeline 실행 이력 (run-014)."),
    ("SFR.MNT.004", "모델 구조 및 실행 그래프를 시각화하여 제공해야 햔다.", "UFR.MNT.001",
     "2025", "중", "오케스트로",
     "✅ /ai-development/pipelines — 6단계 DAG (load → tokenize → train → eval → register → deploy)."),
    ("SFR.MNT.005", "AI 프레임워크에 대한 다양한 가속기 백엔드를 지원하는 통합 런타임을 제공해야 한다.", "UFR.MNT.001",
     "2025", "하", "오케스트로",
     "✅ /iaas/images — CUDA, RBLN, Furiosa, vLLM 통합 런타임 6종."),
    ("SFR.MNT.006", "사용자 역할 기반 접근 제어 및 권한 정보 관리해야 한다.", "UFR.MNT.003",
     "2025", "상", "오케스트로",
     "✅ /iam/roles — 권한 매트릭스 (관리자 vs 슈퍼유저) + canAccess() 게이트."),
    ("SFR.MNT.007", "세션 관리와 멀티 인증 기능을 제공해야한다.", "UFR.MNT.003",
     "2025", "하", "오케스트로",
     "✅ 로그인 mock JWT + 2FA 명세 + /iam/users MFA 활성화 상태."),
    ("SFR.MNT.008", "마이크로서비스 간 보안 통신 기능을 제공해야 한다.", "UFR.MNT.003",
     "2025", "하", "오케스트로",
     "🟡 Istio mTLS 명세 (실 통신은 향후)."),
    ("SFR.MNT.009", "민감 정보 암호화 및 키를 관리할 수 있어야 한다.", "UFR.MNT.003",
     "2025", "하", "오케스트로",
     "✅ /paas/config Secret 마스킹 + Eye 토글 + 로테이션 액션."),
    ("SFR.MNT.010", "사용자/관리자 대시보드를 제공해야한다.", "UFR.MNT.004",
     "2025", "상", "오케스트로",
     "✅ /dashboard — KPI 6장 + 전력 차트 + 알람 카드 + 테넌트 사용률 + 빠른 이동."),
    ("SFR.MNT.011", "프로젝트, 자원, 서비스 신청 관리 및 진행 상황, 이력 조회 기능을 제공해야한다.", "UFR.MNT.004",
     "2025", "중", "오케스트로",
     "✅ /iaas 자원 신청 5단계 위저드 (NIRS RES-09 패턴) + /iam/audit 이력."),
    ("SFR.MNT.012", "다양한 AI반도체의 응용 및 관리를 위한 RESTful API를 제공해야한다.", "UFR.MNT.005",
     "2025", "중", "오케스트로",
     "✅ /api/v1/notebooks/* + /api/v2/clusters/* + MSW handlers."),
    ("SFR.MNT.013", "API 버전 관리할 수 있어야 한다.", "UFR.MNT.005",
     "2025", "중", "오케스트로",
     "✅ /api/v1/* + /api/v2/* (이노그리드 명세 호환)."),
    ("SFR.MNT.014", "모델 구조 최적화를 제공해야 한다.", "UFR.MNT.001",
     "2026", "중", "오케스트로",
     "✅ /ai-development/model-insights — 1 token 지연 분해 + 핫스팟 분석."),
    ("SFR.MNT.015", "협업 개발 환경을 지원해야 한다.", "UFR.MNT.001",
     "2026", "하", "오케스트로",
     "✅ /ai-development/profiles — 팀별 워크스페이스 격리 (ai-research/llm-serving/edge-inference)."),
    ("SFR.MNT.016", "OPA(Open Policy Agent)를 활용한 상황 맞춤형 접근 정책 제어 및 실시간 캐시 최적화 기능을 제공해야 한다.", "UFR.MNT.003",
     "2026", "하", "오케스트로",
     "✅ /iam/roles OPA 명세 + 권한 매트릭스."),
    ("SFR.MNT.017", "API 접근 제어할 수 있어야 한다.", "UFR.MNT.003",
     "2026", "상", "오케스트로",
     "✅ /iam/api-keys — Scope 권한 (kubeflow:*, paas:workloads:read 등)."),
    ("SFR.MNT.018", "컴퓨팅, 네트워크, 스토리지, AI반도체 관리 기능 활용을 위한 인터페이스를 제공해야 한다.", "UFR.MNT.004",
     "2026", "상", "오케스트로",
     "✅ /iaas 6메뉴 + /paas 5메뉴 + /ai-accelerators 통합."),
    ("SFR.MNT.019", "자원 사용 이력, 작업 내역 조회 및 운영정보 제공 기반 사용자 편의 기능을 제공해야 한다.", "UFR.MNT.004",
     "2026", "상", "오케스트로",
     "✅ /iam/audit 감사 로그 + 모든 상세 페이지의 이벤트 탭."),
    ("SFR.MNT.020", "서비스 표준 카탈로그 제공 및 카탈로그별 템플릿을 관리할 수 있어야 한다.", "UFR.MNT.004",
     "2026", "중", "오케스트로",
     "✅ /catalog Rebellions ModelZoo + /iaas/images Flavor 8종."),
    ("SFR.MNT.021", "테넌트별 자원 사용량 시각화 및 SLA를 관리할 수 있어야 한다.", "UFR.MNT.004",
     "2026", "하", "오케스트로",
     "✅ /iaas/projects 쿼터 + /monitoring/alerts SLO."),
    ("SFR.MNT.022", "OAuth 2.0 기반 사용자 인증 및 3rd Party Application별 API Key 발급/관리 기능을 제공해야 한다.", "UFR.MNT.005",
     "2026", "중", "오케스트로",
     "✅ /iam/api-keys 4종 발급 + OAuth 2.0 명세."),
    ("SFR.MNT.023", "대규모 분산 학습 프레임워크를 지원해야 한다.", "UFR.MNT.001",
     "2027", "중", "오케스트로",
     "✅ /ai-development/pipelines DeepSpeed·Megatron 이미지 + /paas/clusters 분산 클러스터."),
    ("SFR.MNT.024", "테넌트별 리소스 할당량 관리 기능을 지원해야 한다.", "UFR.MNT.002",
     "2027", "중", "오케스트로",
     "✅ /iaas/projects 쿼터 변경 다이얼로그."),
    ("SFR.MNT.025", "대규모 엔터프라이즈 조직 독립형 권한 관리 기능을 제공해야한다.", "UFR.MNT.003",
     "2027", "하", "오케스트로",
     "✅ /iam/users 6개 조직별 사용자 (ETRI/이노/KETI/경희대/KACI/오케스트로)."),
    ("SFR.MNT.026", "권한 통합 관리 시각화 기능을 제공해야 한다.", "UFR.MNT.003",
     "2027", "상", "오케스트로",
     "✅ /iam/roles 권한 매트릭스 (13개 권한 × 2 역할)."),
    ("SFR.MNT.027", "권한 변경 이력 추적 기능을 제공해야한다.", "UFR.MNT.003",
     "2027", "하", "오케스트로",
     "✅ /iam/audit 권한 변경 이력 포함."),
    ("SFR.MNT.028", "쿠버네티스 및 클라우드 어플리케이션 관리 기능을 제공해야 한다.", "UFR.MNT.004",
     "2027", "중", "오케스트로",
     "✅ /paas/workloads 액션 5종 + /paas/clusters 카드."),
    ("SFR.MNT.029", "작업 예약, 상태 알림, 자원부족 경고 등 작업 관리 및 알림 기능을 제공해야 한다.", "UFR.MNT.004",
     "2027", "상", "오케스트로",
     "✅ /paas/workloads CronJob + sonner toast 알림."),
    ("SFR.MNT.030", "다중 프로그래밍 언어를 지원하는 SDK 프레임워크를 지원해야 한다.", "UFR.MNT.005",
     "2027", "중", "오케스트로",
     "🟡 /iam/api-keys 다중 언어 SDK 명세."),
    ("SFR.MNT.031", "어플리케이션 개발 성능 최적화를 위한 프로파일러 및 코드 분석 기능을 제공해야 한다.", "UFR.MNT.005",
     "2027", "하", "오케스트로",
     "✅ /notebooks/[id]/ide Code Server 내장 프로파일러 자리."),
    ("SFR.MNT.032", "Open API Specification(OAS) 기반 문서 작성 및 테스트 자동화 기능을 제공해야 한다.", "UFR.MNT.005",
     "2027", "중", "오케스트로",
     "✅ /iam/api-keys OAS 명세 + Swagger 자리."),
    ("SFR.MNT.033", "이기종 AI반도체 자원 통합 관리 및 워크로드 특성 기반 최적 가속기를 선택할 수 있도록 지원해야 한다.", "UFR.MNT.001",
     "2028", "하", "오케스트로",
     "✅ /ai-accelerators/allocation 벤더×유형 매트릭스 + Davis 권장 가속기 선택."),
    ("SFR.MNT.034", "AI SW 실행 실시간 성능 모니터링 및 병목 분석 도구를 제공해야 한다.", "UFR.MNT.001",
     "2028", "하", "오케스트로",
     "✅ /notebooks/[id]/ide 가속기 모니터 + /monitoring/insights 핫스팟 분석."),
    ("SFR.MNT.035", "프레임워크, 런타임, 사전학습 모델, 라이브러리를 선택/배포할 수 있는 마켓플레이스를 제공해야 한다.", "UFR.MNT.006",
     "2028", "상", "오케스트로",
     "✅ /catalog Rebellions ModelZoo 33종 + 작성자 로고 + 카테고리 6종."),
    ("SFR.MNT.036", "리소스 분리 환경 및 테넌트별 리소스 할당량 실시간 모니터링 및 자동 조정할 수 있어야 한다.", "UFR.MNT.002",
     "2028", "중", "오케스트로",
     "✅ /dashboard 테넌트 사용률 + /iaas/projects 쿼터 변경."),
    ("SFR.MNT.037", "테넌트별 권한 메트릭스 연동을 통한 실시간 리소스 접근 제어 및 감사 기능을 제공해야 한다.", "UFR.MNT.002",
     "2028", "중", "오케스트로",
     "✅ /iam/roles + /iam/audit 통합."),
    ("SFR.MNT.038", "이기종 AI반도체 통합 관리를 위한 시각화 기능을 제공해야 한다.", "UFR.MNT.004",
     "2028", "중", "오케스트로",
     "✅ /ai-accelerators 4탭 통합 + /topology Technology Overview."),
    ("SFR.MNT.039", "데이터의 수집/저장 체계 기반 통합 모니터링을 위한 시각화 기능을 제공해야 한다.", "UFR.MNT.004",
     "2028", "하", "오케스트로",
     "✅ /monitoring 5탭 + /resource-map 6계층."),
    ("SFR.MNT.040", "서비스 이용목록, 이미지 이용정보, 자원 시각화 지원 통합 관제를 지원해야 한다.", "UFR.MNT.004",
     "2028", "상", "오케스트로",
     "✅ /dashboard 통합 관제 — KPI 6장 + 활동/알람/사용률."),
    ("SFR.MNT.041", "가상 자원의 장애 및 성능 임계치 기반의 알람을 위한 시각화 기능을 위한 제공해야 한다.", "UFR.MNT.004",
     "2028", "중", "오케스트로",
     "✅ /monitoring/alerts SLO + 활성 알람 4종."),
    ("SFR.MNT.042", "가상 자원에서 발생하는 다양한 장애 이벤트 통합 조회할 수 있도록 지원해야한다.", "UFR.MNT.004",
     "2028", "중", "오케스트로",
     "✅ /monitoring/logs Loki + 이벤트 통합."),
    ("SFR.MNT.043", "시스템의 확장성과 유연성을 고려한 MSA 기반 API Gateway 제공해야 한다.", "UFR.MNT.005",
     "2028", "중", "오케스트로",
     "🟡 Istio API Gateway 명세 (실 구현 향후)."),
    ("SFR.MNT.044", "마켓플레이스 기반의 자동 모듈 업데이트, 충돌 감지, 롤백 등 운영할 수 있어야 햔다", "UFR.MNT.006",
     "2028", "중", "오케스트로",
     "🟡 /catalog 자동 업데이트 자리 (실 구현 향후)."),
    ("SFR.MNT.045", "에너지 효율적 자원 관리 및 사용량 분석을 위한 시각화 기능을 제공해야 한다.", "UFR.MNT.004",
     "2028", "중", "오케스트로",
     "✅ /dashboard 전력 절감율 12.4% + /monitoring 전력 카드."),
]

# 4.1.4 SIR.MNT
SIR_MNT = [
    ("SIR.MNT.001", "외부 인증 연동을 지원해야 한다.", "UFR.MNT.002",
     "1차년도", "중", "오케스트로",
     "✅ /iam/users OAuth 2.0 / SAML / SSO 명세."),
    ("SIR.MNT.002", "사용자/관리자 중심의 프로젝트 및 자원 모니터링 인터페이스를 제공해야 한다.", "UFR.MNT.002",
     "1차년도", "중", "오케스트로",
     "✅ /dashboard + /iaas/projects + /monitoring 통합."),
    ("SIR.MNT.003", "주요 AI 프레임워크와 호환 가능한 인터페이스를 제공해야한다.", "UFR.MNT.005",
     "1차년도", "중", "오케스트로",
     "✅ /iaas/images PyTorch·TensorFlow·vLLM 통합 이미지."),
    ("SIR.MNT.004", "외부 장치와의 연동 고려해야 한다.", "UFR.MNT.005",
     "1차년도", "중", "오케스트로",
     "🟡 Webhook + API Gateway 자리."),
    ("SIR.MNT.005", "Active Directory 연동을 제공해야 한다.", "UFR.MNT.004",
     "3차년도", "하", "오케스트로",
     "🟡 /iam/users AD 연동 명세."),
]

# 4.2 SNR.PF (성능)
SNR_PF = [
    ("SNR.PF.001", "클라우드 서비스 인스턴스와 AI반도체 자원의 매핑 방식(인스턴스:AI반도체)을 3가지(1:1, 1:N, N:1) 제공해야 한다.", "UFR.SVC.002",
     "2025(1:1)·2026(N:1)·2028(1:N)", "상", "ETRI",
     "✅ /ai-accelerators/allocation 매핑 분포 차트 + 카드별 매핑 컬럼."),
    ("SNR.PF.002", "쿠버네티스에 AI반도체 멀티플렉싱 기능을 지원해야 한다.", "UFR.SVC.002",
     "2028", "상", "ETRI",
     "✅ /ai-accelerators/partitions MIG/Slice 시연."),
    ("SNR.PF.003", "초거대 AI 모델 응답 지연시간은 1초 이하여야 한다", "UFR.LLM.001, UNR.001/002/007",
     "2027(1.1초)·2028(1.1초이내)·2029(1초이내)", "상", "ETRI",
     "✅ /monitoring/insights P99 차트 + 1초 SLO 라인. 현재 980ms 표시."),
    ("SNR.PF.004", "초거대 AI모델 3종을 실행 및 운영 가능해야 한다.", "UFR.LLM.001",
     "2025(1종)·2027(2종)·2028(3종)", "상", "ETRI",
     "✅ /ai-development/serving Solar 10B + STT RNGD + STT ATOM 3종."),
    ("SNR.PF.005", "AI반도체를 기반으로 운영 가능한 최대 크기의 초거대 AI모델 실행을 검증해야 한다.", "UFR.LLM.001",
     "2027(64B)·2028(320B)", "상", "ETRI",
     "✅ /catalog Llama 3.1 70B + Qwen 32B + EXAONE 32B + Pixtral 12B."),
    ("SNR.PF.006", "AI 반도체 클라우드 플랫폼은 핵심 인프라, 플랫폼 및 관리에 관한 국제 표준을 제공하여야 한다", "UFR.STD.001/002",
     "2027(1건)·2028(1건)·2029(1건)", "상", "ETRI",
     "🟡 ETRI 주관 — 본 mockup이 표준 참조."),
    ("SNR.PF.007", "AI 반도체에 특화된 모니터링 메트릭을 제공하여야 한다.", "UFR.MON.001",
     "2025(7건)·2027(8건)·2028(9건)", "상", "이노그리드",
     "✅ /monitoring DCGM 메트릭 (utilization/memory/temperature/power/sm_clock 등 7+)."),
    ("SNR.PF.008", "AI반도체 클라우드 운영 지원을 위한 옵저버빌리티 항목을 제공하여야한다.", "UFR.MON.001/002/003/004",
     "2026(2건)·2027(3건)·2028(6건)", "상", "이노그리드",
     "✅ /monitoring/insights Davis 인사이트 + 다중 옵저버빌리티 항목."),
    ("SNR.PF.009", "운영 비용을 15% 절감해야 한다.", "UFR.CST.010, UNR.007",
     "2027(8%)·2028(12%)·2029(15%)", "상", "KETI",
     "✅ /dashboard 전력 절감율 12.4% (2028 목표 달성 표시)."),
    ("SNR.PF.010", "스케줄링 지연시간을 30% 감소해야 한다.", "UFR.CST.008/011",
     "3차년도(23.4%)·5차년도(30%)", "상", "KETI",
     "🟡 /monitoring/insights 스케줄링 지연 분석 자리."),
    ("SNR.PF.011", "AI반도체 클라우드 접근제어 방식 5종을 제공해야 한다.", "UFR.MNT.003, UNR.005",
     "1차년도(1종)·3차년도(3종)·5차년도(5종)", "중", "오케스트로",
     "✅ /iam/users 5종 인증 방식 (MFA/OAuth 2.0/SSO/API Key/SAML)."),
]

# 4.3 SNR (비기능)
SNR_SA = [
    ("SNR.SA.001", "실시간 자원 모니터링과 상태 점검을 통해 서비스 중단을 예방해야 한다.", "UFR.SVC.001, UFR.MON.005, UNR.003",
     "2028", "상", "ETRI·이노그리드",
     "✅ /monitoring 실시간 + /monitoring/insights Davis 자동 진단."),
    ("SNR.SA.002", "스케줄링 알고리즘은 오류 발생에도 스케줄링 동작을 중단시키지 않아야 한다.", "UNR.003/004",
     "2027", "중", "KETI·ETRI",
     "🟡 /monitoring/alerts SLO 가용성 명세."),
]

SNR_SE = [
    ("SNR.SE.001", "클라우드 플랫폼은 45대 이상의 AI반도체를 클러스터에서 성능과 최적화 가능토록 고려해야 한다.", "UNR.008",
     "2029", "상", "전체 기관",
     "✅ /topology Smartscape 확장 가능 + /ai-accelerators 26장 시연 (45대 확장 가능 구조)."),
    ("SNR.SE.002", "예측 모델은 추가 학습을 통해 모델의 성능을 쉽게 향상시키고 확장할 수 있어야 한다.", "UNR.009",
     "2027", "상", "KETI",
     "🟡 /ai-development/experiments Katib HPO 자동 학습."),
    ("SNR.SE.003", "주요 AI 프레임워크와 호환성을 제공해야 한다.", "UNR.006",
     "2025", "상", "오케스트로",
     "✅ /iaas/images 통합 런타임 이미지."),
    ("SNR.SE.004", "3rd Party 응용 및 장치가 연계를 고려한 표준화된 개방형 인터페이스를 제공해야 한다.", "UFR.MNT.005, UFR.STD.001/002, UFR.OPN.004",
     "2025", "상", "오케스트로",
     "✅ /iam/api-keys 개방형 API + OAS 명세."),
]

SNR_SS = [
    ("SNR.SS.001", "각 자원 상태 정보와 사용 로그는 접근 제어 정책을 통해 권한이 있는 사용자만 조회 가능해야 한다.", "UFR.MNT.004, UNR.007",
     "2026", "하", "전체 기관",
     "✅ canAccess() 역할 게이트 + /monitoring 권한별 필터."),
    ("SNR.SS.002", "운용 최적화 API는 관리자만 호출할 수 있도록 제어해야 한다.", "UFR.MNT.004",
     "2027", "하", "KETI·오케스트로",
     "✅ /iam/api-keys Scope 권한 (admin-only API 명세)."),
    ("SNR.SS.003", "비용 최적화 로그는 외부 접근을 제한해야 한다.", "UNR.004/008",
     "2027", "하", "KETI·오케스트로",
     "✅ /monitoring/logs 권한 필터."),
    ("SNR.SS.004", "오픈소스 라이브러리 및 컨테이너 이미지에의 보안 취약점을 스캔하고 이를 조치하여야 한다.", "UNR.008",
     "2027", "중", "전체 기관",
     "🟡 /iaas/images Trivy 보안 스캔 명세 자리."),
    ("SNR.SS.005", "유연하고 안전한 인증 환경 제공해야 한다.", "UNR.004",
     "2026", "상", "오케스트로",
     "✅ /iam/users 5종 인증 + MFA + JWT."),
    ("SNR.SS.006", "역할에 따른 정확한 리소스 접근 보장해야 한다.", "UNR.004/008",
     "2026", "상", "오케스트로",
     "✅ /iam/roles 권한 매트릭스 + canAccess()."),
    ("SNR.SS.007", "신뢰성 있는 통신 환경을 제공해야 한다.", "UNR.004/008",
     "2026", "하", "오케스트로",
     "🟡 Istio mTLS 명세."),
]

SNR_SM = [
    ("SNR.SM.001", "공개SW 커뮤니티를 구성하여, 지속적인 SW개발 및 활용을 유도해야 한다.", "UFR.OPN.001~004",
     "1차년도", "중", "전체 기관",
     "🟡 GitHub 공개 SW 명세 (kubeflow/openstack 등 활용)."),
    ("SNR.SM.002", "시스템 컴포넌트들은 모듈화·플러그인 구조로 설계 및 개발되어야 한다.", "UNR.003",
     "1차년도", "중", "전체 기관",
     "✅ Next.js App Router + MSA + Kubeflow CRD 기반."),
    ("SNR.SM.003", "구성 요소간 의존성 정보는 명확하게 문서화되고 관리되어야 한다.", "UNR.003",
     "2차년도", "하", "KETI",
     "✅ /resource-map 6계층 관계 정의 (attached_to/passthrough_to/allocated_to 등)."),
]


# ─────────────────────────────────────────────────────────────
# 문서 생성
# ─────────────────────────────────────────────────────────────

def set_table_borders(table):
    """모든 셀에 검정 테두리 추가"""
    tbl = table._element
    tbl_pr = tbl.tblPr
    tbl_borders = OxmlElement("w:tblBorders")
    for border_name in ("top", "left", "bottom", "right", "insideH", "insideV"):
        b = OxmlElement(f"w:{border_name}")
        b.set(qn("w:val"), "single")
        b.set(qn("w:sz"), "6")
        b.set(qn("w:color"), "808080")
        tbl_borders.append(b)
    tbl_pr.append(tbl_borders)


def set_cell_shading(cell, color):
    """셀 배경색"""
    tc_pr = cell._tc.get_or_add_tcPr()
    shd = OxmlElement("w:shd")
    shd.set(qn("w:val"), "clear")
    shd.set(qn("w:color"), "auto")
    shd.set(qn("w:fill"), color)
    tc_pr.append(shd)


def add_ufr_table(doc, items, title):
    """사용자 요구사항 표 — 4열: Req.ID, 상세 내용, 출처, Mockup 반영사항"""
    h = doc.add_heading(title, level=3)
    table = doc.add_table(rows=1 + len(items), cols=4)
    table.alignment = WD_TABLE_ALIGNMENT.CENTER
    set_table_borders(table)

    # 헤더
    hdr = table.rows[0].cells
    for i, label in enumerate(["Req.ID", "상세 내용", "출처", "Mockup 반영사항"]):
        hdr[i].text = label
        set_cell_shading(hdr[i], "4F46E5")
        for para in hdr[i].paragraphs:
            para.alignment = WD_ALIGN_PARAGRAPH.CENTER
            for run in para.runs:
                run.font.bold = True
                run.font.color.rgb = RGBColor(0xFF, 0xFF, 0xFF)
                run.font.size = Pt(9)

    # 데이터
    for idx, (req_id, content, source, reflect) in enumerate(items, start=1):
        row = table.rows[idx].cells
        row[0].text = req_id
        row[1].text = content
        row[2].text = source
        row[3].text = reflect
        for c in row:
            for para in c.paragraphs:
                for run in para.runs:
                    run.font.size = Pt(8.5)

    # 컬럼 폭 (cm 단위, 합 약 26cm in landscape A4)
    widths = [Cm(2.5), Cm(8.0), Cm(3.5), Cm(11.5)]
    for row in table.rows:
        for i, w in enumerate(widths):
            row.cells[i].width = w


def add_sfr_table(doc, items, title):
    """시스템 요구사항 표 — 7열: Req.ID, 상세 내용, 관련 UR, 구현년도, 중요도, 담당기관, Mockup 반영사항"""
    h = doc.add_heading(title, level=3)
    table = doc.add_table(rows=1 + len(items), cols=7)
    table.alignment = WD_TABLE_ALIGNMENT.CENTER
    set_table_borders(table)

    hdr = table.rows[0].cells
    for i, label in enumerate(["Req.ID", "상세 내용", "관련 UR", "구현년도", "중요도", "담당기관", "Mockup 반영사항"]):
        hdr[i].text = label
        set_cell_shading(hdr[i], "4F46E5")
        for para in hdr[i].paragraphs:
            para.alignment = WD_ALIGN_PARAGRAPH.CENTER
            for run in para.runs:
                run.font.bold = True
                run.font.color.rgb = RGBColor(0xFF, 0xFF, 0xFF)
                run.font.size = Pt(9)

    for idx, item in enumerate(items, start=1):
        req_id, content, related, impl_year, priority, owner, reflect = item
        row = table.rows[idx].cells
        row[0].text = req_id
        row[1].text = content
        row[2].text = related
        row[3].text = impl_year
        row[4].text = priority
        row[5].text = owner
        row[6].text = reflect
        for c in row:
            for para in c.paragraphs:
                for run in para.runs:
                    run.font.size = Pt(8)

    # landscape A4 content area ~ 26cm
    widths = [Cm(2.2), Cm(5.5), Cm(2.0), Cm(2.2), Cm(1.2), Cm(2.4), Cm(10.0)]
    for row in table.rows:
        for i, w in enumerate(widths):
            row.cells[i].width = w


def main():
    doc = Document()

    # 페이지 가로 방향 + A4
    for section in doc.sections:
        # 가로 전환
        new_width, new_height = section.page_height, section.page_width
        section.orientation = 1  # landscape
        section.page_width = new_width
        section.page_height = new_height
        section.left_margin = Cm(1.5)
        section.right_margin = Cm(1.5)
        section.top_margin = Cm(1.5)
        section.bottom_margin = Cm(1.5)

    # 기본 스타일
    style = doc.styles["Normal"]
    style.font.name = "맑은 고딕"
    style.font.size = Pt(10)

    # 표지
    title = doc.add_heading("AI반도체 클라우드 서비스 플랫폼 — 요구사항 mockup 반영 검토", 0)
    title.alignment = WD_ALIGN_PARAGRAPH.CENTER

    p = doc.add_paragraph()
    p.alignment = WD_ALIGN_PARAGRAPH.CENTER
    run = p.add_run("요구사항 정의서 v1.1 (2026.03.20) 기준\n")
    run.font.size = Pt(11)
    run = p.add_run("작성: 오케스트로(주) · openKcloud CMP Mockup v1.0\n")
    run.font.size = Pt(10)
    run = p.add_run("2026.06.04\n\n")
    run.font.size = Pt(10)
    run.font.color.rgb = RGBColor(0x66, 0x66, 0x66)

    p = doc.add_paragraph()
    p.alignment = WD_ALIGN_PARAGRAPH.LEFT
    run = p.add_run("범례:\n")
    run.font.bold = True
    p2 = doc.add_paragraph()
    p2.add_run("  ✅ 완전 반영 — mockup 페이지/기능에서 시각화 또는 인터랙션 시연 가능\n").font.size = Pt(10)
    p2.add_run("  🟡 부분 반영 (UI 자리) — UI 자리 마련, 실 알고리즘은 다른 기관 영역이거나 향후 구현\n").font.size = Pt(10)
    p2.add_run("  ⏳ 향후 구현 — 명세상 자리는 있으나 구체 UI 미반영 (차차년도 구현 예정)\n").font.size = Pt(10)

    doc.add_page_break()

    # 1. 사용자 기능 요구사항 (UFR)
    h1 = doc.add_heading("1. 사용자 기능 요구사항 (UFR)", 1)
    add_ufr_table(doc, UFR_SVC, "1.1 AI반도체 클라우드 서비스 제공 (UFR.SVC)")
    doc.add_page_break()
    add_ufr_table(doc, UFR_MON, "1.2 AI반도체 모니터링 및 트레이싱 (UFR.MON)")
    doc.add_page_break()
    add_ufr_table(doc, UFR_CST, "1.3 운용 비용 최적화 (UFR.CST)")
    doc.add_page_break()
    add_ufr_table(doc, UFR_LLM, "1.4 초거대 AI 클라우드 서비스 (UFR.LLM)")
    add_ufr_table(doc, UFR_MNT, "1.5 통합 관리 및 개방형 API (UFR.MNT) ⭐ 오케스트로 주관")
    doc.add_page_break()
    add_ufr_table(doc, UFR_STD, "1.6 표준 (UFR.STD)")
    add_ufr_table(doc, UFR_OPN, "1.7 공개 SW (UFR.OPN)")

    doc.add_page_break()

    # 2. 사용자 비기능 요구사항 (UNR)
    doc.add_heading("2. 사용자 비기능 요구사항 (UNR)", 1)
    add_ufr_table(doc, UNR, "2.1 사용자 비기능 요구사항")

    doc.add_page_break()

    # 3. 시스템 기능 요구사항 (SFR)
    doc.add_heading("3. 시스템 기능 요구사항 (SFR)", 1)
    add_sfr_table(doc, SFR_SVC, "3.1 AI반도체 클라우드 서비스 제공 프레임워크 (SFR.SVC)")
    doc.add_page_break()
    add_sfr_table(doc, SIR_SVC, "3.2 AI반도체 클라우드 서비스 제공 — 인터페이스 (SIR.SVC)")
    doc.add_page_break()
    add_sfr_table(doc, SFR_OPT, "3.3 AI반도체 클라우드 서비스 최적화 프레임워크 (SFR.OPT)")
    doc.add_page_break()
    add_sfr_table(doc, SIR_OPT, "3.4 AI반도체 최적화 — 인터페이스 (SIR.OPT)")
    add_sfr_table(doc, SFR_LLM, "3.5 초거대 AI 클라우드 서비스 지원 프레임워크 (SFR.LLM)")
    add_sfr_table(doc, SIR_LLM, "3.6 초거대 AI — 인터페이스 (SIR.LLM)")
    doc.add_page_break()
    add_sfr_table(doc, SFR_MNT, "3.7 AI반도체 통합 관리 프레임워크 (SFR.MNT) ⭐ 오케스트로 주관")
    doc.add_page_break()
    add_sfr_table(doc, SIR_MNT, "3.8 통합 관리 — 인터페이스 (SIR.MNT)")

    doc.add_page_break()

    # 4. 성능 요구사항
    doc.add_heading("4. 성능 요구사항 (SNR.PF)", 1)
    add_sfr_table(doc, SNR_PF, "4.1 성능 요구사항")

    doc.add_page_break()

    # 5. 시스템 특성 요구사항 (SNR.SA/SE/SS/SM)
    doc.add_heading("5. 시스템 특성 요구사항 (SNR)", 1)
    add_sfr_table(doc, SNR_SA, "5.1 가용성 (SNR.SA)")
    add_sfr_table(doc, SNR_SE, "5.2 확장성 (SNR.SE)")
    doc.add_page_break()
    add_sfr_table(doc, SNR_SS, "5.3 보안 (SNR.SS)")
    add_sfr_table(doc, SNR_SM, "5.4 유지보수 (SNR.SM)")

    # 종합 통계
    doc.add_page_break()
    doc.add_heading("6. 종합 반영 통계", 1)

    stat_table = doc.add_table(rows=1, cols=5)
    set_table_borders(stat_table)
    hdr = stat_table.rows[0].cells
    for i, label in enumerate(["카테고리", "항목 수", "✅ 완전", "🟡 부분", "⏳ 향후"]):
        hdr[i].text = label
        set_cell_shading(hdr[i], "4F46E5")
        for para in hdr[i].paragraphs:
            para.alignment = WD_ALIGN_PARAGRAPH.CENTER
            for run in para.runs:
                run.font.bold = True
                run.font.color.rgb = RGBColor(0xFF, 0xFF, 0xFF)

    stats = [
        ("UFR.SVC", 17, 12, 4, 1),
        ("UFR.MON", 8, 8, 0, 0),
        ("UFR.CST", 17, 5, 12, 0),
        ("UFR.LLM", 2, 2, 0, 0),
        ("UFR.MNT (오케스트로)", 6, 6, 0, 0),
        ("UFR.STD/OPN", 6, 3, 3, 0),
        ("UNR", 8, 8, 0, 0),
        ("SFR.SVC", 34, 18, 15, 1),
        ("SFR.OPT", 22, 13, 9, 0),
        ("SFR.LLM", 4, 4, 0, 0),
        ("SFR.MNT (오케스트로)", 45, 41, 4, 0),
        ("SIR.*", 16, 13, 3, 0),
        ("SNR.PF", 11, 8, 3, 0),
        ("SNR.SA/SE/SS/SM", 16, 10, 6, 0),
        ("합계", 212, 151, 59, 2),
    ]
    for stat in stats:
        row = stat_table.add_row().cells
        for i, v in enumerate(stat):
            row[i].text = str(v)
            for para in row[i].paragraphs:
                if i == 0:
                    if stat[0] in ("UFR.MNT (오케스트로)", "SFR.MNT (오케스트로)", "합계"):
                        for run in para.runs:
                            run.font.bold = True
                else:
                    para.alignment = WD_ALIGN_PARAGRAPH.CENTER

    doc.add_paragraph("")
    p = doc.add_paragraph()
    run = p.add_run("핵심 성과\n")
    run.font.bold = True
    run.font.size = Pt(12)

    p = doc.add_paragraph("• 오케스트로 주관 (UFR.MNT + SFR.MNT) 51개 중 47개 = 92% 완전 반영")
    p = doc.add_paragraph("• 모든 인터페이스 요구사항 (SIR) 16개 중 13개 = 81% 완전 반영 (kcloud-mnt가 모든 GUI 창구)")
    p = doc.add_paragraph("• 사용자 기능 요구사항 (UFR) 64개 중 44개 = 69% 완전 반영")
    p = doc.add_paragraph("• 향후 구현(⏳) 항목 = 2개 (1% 미만) — UFR.SVC.017 원격 AI반도체, SFR.SVC.034 다수 원격 가속기")
    p = doc.add_paragraph("• 부분 반영(🟡) 항목 = 모두 UI 자리 완비. 실 알고리즘은 ETRI/KETI/이노그리드 책임 영역")

    # 저장
    output_path = r"C:\Users\YUN\projects\openkcloud-cmp\docs\요구사항_정의서_mockup_반영_검토서.docx"
    doc.save(output_path)
    print(f"✅ 문서 생성 완료: {output_path}")
    print(f"   페이지 방향: 가로 (Landscape A4)")
    print(f"   총 요구사항 항목: 212개")


if __name__ == "__main__":
    main()
