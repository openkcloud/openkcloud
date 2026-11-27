"""
openKcloud CMP — IaaS / PaaS 기능 명세서 (현재 mockup 기준)
generate xlsx file.
"""
import sys, io, os
sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding="utf-8")

from openpyxl import Workbook
from openpyxl.styles import Alignment, Border, Side, Font, PatternFill
from openpyxl.utils import get_column_letter

# ─────────────────────────────────────────────────────────────
# 스타일
# ─────────────────────────────────────────────────────────────
FONT_NAME = "맑은 고딕"
HDR_FILL = PatternFill("solid", fgColor="1B3A8A")
HDR_FONT = Font(name=FONT_NAME, size=10, bold=True, color="FFFFFF")
CELL_FONT = Font(name=FONT_NAME, size=10)
ID_FONT = Font(name="Consolas", size=9, bold=True, color="1B3A8A")
WRAP = Alignment(wrap_text=True, vertical="top", horizontal="left")
CENTER = Alignment(wrap_text=True, vertical="center", horizontal="center")
BORDER = Border(
    left=Side(style="thin", color="C7CFE2"),
    right=Side(style="thin", color="C7CFE2"),
    top=Side(style="thin", color="C7CFE2"),
    bottom=Side(style="thin", color="C7CFE2"),
)
STATUS_FILLS = {
    "구현 완료":  PatternFill("solid", fgColor="DCFCE7"),
    "UI 반영":   PatternFill("solid", fgColor="DBEAFE"),
    "향후 구현":  PatternFill("solid", fgColor="FEF3C7"),
}
CATEGORY_FILLS = {
    "IaaS": PatternFill("solid", fgColor="EEF2FF"),  # indigo-50
    "PaaS": PatternFill("solid", fgColor="ECFDF5"),  # mint-50
}

# ─────────────────────────────────────────────────────────────
# 데이터: IaaS 기능
# 컬럼: (화면코드, 화면명, 기능ID, 기능명, 설명, UI요소, API, 자원유형, 상태)
# ─────────────────────────────────────────────────────────────
IAAS_FUNCS = [
    # IaaS 개요
    ("OVR", "/iaas (개요)", "IAAS.OVR.001", "계층 구조 시각화",
     "openKcloud는 물리 데이터센터(Center) 안에 여러 가용 영역(Zone)이 있고, 각 영역마다 OpenStack 클러스터(Pool)가 배치되며, 그 위에서 프로젝트(테넌트) 단위로 자원이 격리되는 4단계 계층 구조를 갖습니다. 이 페이지에서는 전체 계층을 카드 그리드로 한눈에 조회할 수 있어, 어떤 자원이 어디에 어떻게 배치되어 있는지 운영자가 즉시 파악할 수 있습니다.",
     "Card grid · Building2/Network 아이콘",
     "/api/iaas/centers · /api/iaas/zones · /api/iaas/pools",
     "Topology", "구현 완료"),
    ("OVR", "/iaas (개요)", "IAAS.OVR.002", "테넌트 쿼터 요약",
     "각 프로젝트(예: ETRI 연구팀, 오케스트로팀)에게 할당된 vCPU·RAM·GPU 한도와 현재 사용량을 진행률 바로 시각화합니다. 한도에 가까워진 프로젝트를 즉시 식별해 자원 부족으로 인한 서비스 장애를 사전에 예방하고, 자원 증설 타이밍을 데이터 기반으로 결정할 수 있습니다.",
     "Progress bar · StatCard",
     "/api/iaas/projects",
     "Tenant", "구현 완료"),
    ("OVR", "/iaas (개요)", "IAAS.OVR.003", "자원 분포 통계",
     "데이터센터·풀·프로젝트별로 등록된 자원 수를 집계해 카드와 차트로 요약합니다. 클라우드 운영 전체 규모를 숫자로 파악하고 어디에 부하가 집중되는지 한눈에 확인해 자원 배분 의사결정을 지원합니다.",
     "Stat cards · Recharts",
     "/api/iaas/centers · /api/iaas/zones · /api/iaas/pools",
     "Stats", "구현 완료"),

    # VM 인스턴스
    ("VM", "/iaas/instances (VM 인벤토리)", "IAAS.VM.001", "VM 목록 조회",
     "운영 중인 모든 가상머신을 테이블 형태로 조회합니다. VM 이름, 소속 프로젝트, 실행 상태, vCPU·RAM 사양, 배치된 호스트, 할당된 가속기까지 한 화면에 모아 보여 주어 자원 위치 파악과 장애 대응이 매우 빠릅니다.",
     "Table · Skeleton · 검색 Input",
     "GET /api/iaas/instances?tenantId=&status=",
     "VM", "구현 완료"),
    ("VM", "/iaas/instances", "IAAS.VM.002", "상태별 집계 카드",
     "전체 VM 수와 함께 Running·Stopped·Error 상태별 카운트를 카드로 표시합니다. 비정상 상태 VM이 몇 개나 있는지 즉시 확인되므로, 운영자가 대응 우선순위를 판단하기 좋습니다.",
     "StatBlock × 4",
     "GET /api/iaas/instances",
     "VM", "구현 완료"),
    ("VM", "/iaas/instances", "IAAS.VM.003", "테넌트·상태 필터",
     "특정 프로젝트의 VM만 보고 싶거나, Stopped 상태 VM만 모아 보고 싶을 때 사용합니다. 프로젝트 선택·상태 선택·이름 검색이 동시에 적용되어 수백 대의 VM 중에서도 원하는 항목만 빠르게 추려낼 수 있습니다.",
     "Select × 2 · Search Input",
     "GET /api/iaas/instances?tenantId={id}&status={status}",
     "VM", "구현 완료"),
    ("VM", "/iaas/instances/[id] (VM 상세)", "IAAS.VM.004", "VM 상세 패널",
     "VM 한 대를 클릭하면 그 VM의 IP·호스트·소속 프로젝트와 함께 CPU·메모리·디스크·네트워크 사용량 시계열 그래프가 한 화면에 펼쳐집니다. 가속기가 할당된 경우 GPU 점유 현황까지 함께 표시되어 ML 워크로드의 자원 효율을 직관적으로 파악할 수 있습니다.",
     "Card · Recharts AreaChart/LineChart · HealthRing",
     "GET /api/iaas/instances/:id",
     "VM", "구현 완료"),
    ("VM", "/iaas/instances/[id]", "IAAS.VM.005", "라이프사이클 액션 — 시작/중지/재시작",
     "운영자가 VM을 직접 켜고 끄거나 재부팅할 수 있습니다. 점검을 위해 일시 중지하거나 응답 없는 VM을 재시작하는 일상 운영 작업을 한 번의 클릭으로 처리합니다.",
     "Button × 3 · DropdownMenu",
     "POST /api/iaas/instances/:id/{start|stop|restart} (계획)",
     "VM", "UI 반영"),
    ("VM", "/iaas/instances/[id]", "IAAS.VM.006", "라이브 마이그레이션",
     "서비스 중단 없이 VM을 다른 물리 서버로 옮깁니다. 호스트 점검이나 부하 재배치 상황에서 사용자가 체감하지 못하는 사이 VM을 이전시키는 OpenStack의 핵심 기능을 GUI로 제공합니다.",
     "Button · Dialog",
     "POST /api/iaas/instances/:id/migrate (계획)",
     "VM", "UI 반영"),
    ("VM", "/iaas/instances/[id]", "IAAS.VM.007", "VM 스냅샷 생성",
     "현재 시점의 VM 디스크 상태를 스냅샷으로 저장합니다. 중요한 변경 전 백업해두거나, 동일 상태의 VM을 다수 복제할 때 기준 이미지로 활용합니다.",
     "Button · Dialog",
     "POST /api/iaas/instances/:id/snapshots (계획)",
     "VM/Image", "UI 반영"),
    ("VM", "/iaas/instances/[id]", "IAAS.VM.008", "VM 콘솔 접속",
     "원격 데스크톱처럼 VM 화면에 직접 접속할 수 있는 noVNC 콘솔을 브라우저 안에서 띄웁니다. 네트워크 문제로 SSH가 안 될 때 마지막 수단의 접속 경로 역할을 합니다.",
     "iframe placeholder",
     "WS /api/iaas/instances/:id/console (계획)",
     "VM", "향후 구현"),
    ("VM", "/iaas/instances/[id]", "IAAS.VM.009", "GPU 패스스루 표시",
     "이 VM이 어떤 AI 가속기(NVIDIA A100, Furiosa NPU 등)를 몇 장 점유하고 있는지 명시적으로 보여 줍니다. 어떤 워크로드가 어떤 가속기를 쓰는지 매핑이 한눈에 보이므로, 자원 회수·재배치 결정을 신속하게 내릴 수 있습니다.",
     "Card · Badge",
     "GET /api/infrastructure/servers/:id (acceleratorIds)",
     "Accelerator", "구현 완료"),
    ("VM", "/iaas/instances/[id]", "IAAS.VM.010", "VM 삭제",
     "사용하지 않는 VM을 제거합니다. 연결된 볼륨이나 네트워크 인터페이스가 있을 때는 안전을 위해 정리 절차를 먼저 안내하여 실수로 인한 데이터 손실을 방지합니다.",
     "Button (destructive)",
     "DELETE /api/iaas/instances/:id (계획)",
     "VM", "UI 반영"),

    # 이미지
    ("IMG", "/iaas/images (이미지·Flavor)", "IAAS.IMG.001", "이미지 목록 조회",
     "VM 생성 시 기준이 되는 디스크 이미지 카탈로그입니다. Ubuntu+CUDA, RHEL+Furiosa SDK, Rocky Linux+Rebellions Runtime, vLLM Serving 등 AI 워크로드용으로 사전 패키징된 이미지들을 한 화면에서 비교·선택할 수 있습니다.",
     "Table · Tabs (이미지/Flavor)",
     "GET /api/iaas/images",
     "Image", "구현 완료"),
    ("IMG", "/iaas/images", "IAAS.IMG.002", "Glance 카탈로그 동기화",
     "OpenStack의 이미지 저장소(Glance)에서 최신 이미지 목록을 다시 가져옵니다. 운영팀이 새 이미지를 등록했을 때 CMP에 즉시 반영하기 위한 수동 새로고침 버튼 역할입니다.",
     "Button (outline)",
     "POST /api/iaas/images/sync (계획)",
     "Image", "UI 반영"),
    ("IMG", "/iaas/images", "IAAS.IMG.003", "이미지 업로드",
     "외부에서 만든 QCOW2·VMDK·RAW 디스크 이미지 파일을 카탈로그에 등록합니다. 사용자 정의 운영체제나 특수 환경 이미지를 플랫폼에 들여올 때 사용합니다.",
     "Dialog · File input · Form",
     "POST /api/iaas/images (계획)",
     "Image", "UI 반영"),
    ("IMG", "/iaas/images", "IAAS.IMG.004", "이미지 상세 Sheet",
     "이미지 한 개를 클릭하면 오른쪽 패널에 운영체제·런타임·지원 가속기·파일 크기·SHA256 해시·생성자 같은 메타데이터가 표시됩니다. VM을 만들기 전에 이미지의 정확한 사양을 확인할 수 있는 검증 단계입니다.",
     "Sheet · Detail rows",
     "GET /api/iaas/images/:id (계획)",
     "Image", "구현 완료"),
    ("IMG", "/iaas/images", "IAAS.IMG.005", "이미지로부터 VM 생성",
     "선택한 이미지를 기반으로 새 VM을 만드는 위저드를 호출합니다. 'CUDA 이미지로 GPU VM 만들기' 같은 일상 작업을 카탈로그 → 생성 흐름으로 매끄럽게 연결합니다.",
     "Button · ResourceRequestWizard",
     "POST /api/iaas/instances (계획)",
     "VM/Image", "UI 반영"),
    ("IMG", "/iaas/images", "IAAS.IMG.006", "이미지 다운로드",
     "이미지 파일을 로컬 디스크로 내려받습니다. 백업, 외부 환경으로의 이전, 또는 별도 검증을 위해 사용합니다.",
     "Button (outline)",
     "GET /api/iaas/images/:id/download (계획)",
     "Image", "UI 반영"),
    ("IMG", "/iaas/images", "IAAS.IMG.007", "이미지 삭제",
     "더 이상 필요 없는 이미지를 카탈로그에서 제거합니다. 사용 중인 VM이 있는지 확인하는 안전 검사를 거친 후 삭제됩니다.",
     "Button (destructive)",
     "DELETE /api/iaas/images/:id (계획)",
     "Image", "UI 반영"),
    ("IMG", "/iaas/images (Flavor 탭)", "IAAS.IMG.008", "Flavor 목록",
     "VM 사양 템플릿(CPU 코어 수·RAM 크기·GPU 개수의 조합)을 미리 정의해 둔 카탈로그입니다. 사용자는 매번 사양을 입력하는 대신 'm1.xlarge.gpu1' 같은 사전 정의된 사양을 선택만 하면 되어 일관성과 편의성이 모두 확보됩니다.",
     "Table",
     "/api/iaas/flavors (fixture)",
     "Flavor", "구현 완료"),
    ("IMG", "/iaas/images (Flavor 탭)", "IAAS.IMG.009", "Flavor 생성",
     "새로운 VM 사양 템플릿을 정의합니다. 표준 카탈로그에 없는 특수 워크로드용 사양 조합이 필요할 때 관리자가 추가할 수 있습니다.",
     "Dialog · Form",
     "POST /api/iaas/flavors (계획)",
     "Flavor", "UI 반영"),

    # 네트워크
    ("NET", "/iaas/networks (네트워크)", "IAAS.NET.001", "네트워크 목록",
     "관리망(서버 관리용), 서비스망(앱 트래픽용), 데이터망(스토리지·백업용)으로 분류된 모든 네트워크를 VLAN과 CIDR 정보와 함께 표 형태로 표시합니다. 어떤 네트워크에 어떤 종류의 트래픽이 흐르는지 한눈에 파악할 수 있습니다.",
     "Table · Badge (class)",
     "GET /api/iaas/networks",
     "Network", "구현 완료"),
    ("NET", "/iaas/networks", "IAAS.NET.002", "네트워크 상세 Sheet — 서브넷·포트·보안·라우터·FIP 5탭",
     "네트워크 하나를 클릭하면 오른쪽 패널이 열리며 서브넷, 포트, 보안 그룹, 라우터, Floating IP 정보를 5개 탭으로 분리해 보여 줍니다. 네트워크 구성 전체를 한 화면에서 파악할 수 있어 트러블슈팅 시간이 크게 단축됩니다.",
     "Sheet · Tabs × 5",
     "GET /api/iaas/networks/:id (계획)",
     "Network", "구현 완료"),
    ("NET", "/iaas/networks", "IAAS.NET.003", "서브넷 추가",
     "네트워크 내에 새 서브넷을 정의합니다. CIDR 범위, 게이트웨이 주소, DHCP 활성화 여부 등을 입력해 추가합니다.",
     "Button · Dialog",
     "POST /api/iaas/networks/:id/subnets (계획)",
     "Subnet", "UI 반영"),
    ("NET", "/iaas/networks", "IAAS.NET.004", "Floating IP 할당",
     "내부에 있는 VM이 외부에서 접속 가능하도록 공인 IP(Floating IP)를 할당합니다. 웹 서비스 공개, 외부 API 호출 등 외부 노출이 필요한 시나리오의 표준 경로입니다.",
     "Button · Dialog",
     "POST /api/iaas/floatingips (계획)",
     "FloatingIP", "UI 반영"),
    ("NET", "/iaas/networks", "IAAS.NET.005", "보안 그룹 규칙 편집",
     "방화벽 역할을 하는 보안 그룹의 인바운드·아웃바운드 트래픽 규칙을 추가하거나 삭제합니다. SSH(22번 포트)나 HTTPS(443번 포트)만 허용하는 등의 정책을 GUI에서 손쉽게 관리할 수 있습니다.",
     "Button · Dialog",
     "PATCH /api/iaas/security-groups/:id (계획)",
     "SecurityGroup", "UI 반영"),
    ("NET", "/iaas/networks", "IAAS.NET.006", "네트워크 삭제",
     "네트워크를 제거합니다. 사용 중인 포트가 있을 때는 안전을 위해 거부되며, 연결된 자원을 먼저 정리해야 합니다.",
     "Button (destructive)",
     "DELETE /api/iaas/networks/:id (계획)",
     "Network", "UI 반영"),
    ("NET", "/iaas/networks", "IAAS.NET.007", "CIDR/IP 클립보드 복사",
     "Subnet CIDR이나 Floating IP를 마우스 클릭 한 번으로 클립보드에 복사합니다. 운영자가 수동으로 IP를 옮겨 적던 작업을 한 번에 정확히 처리할 수 있게 합니다.",
     "Button (icon) · clipboard API",
     "(client only)",
     "Network", "구현 완료"),

    # 프로젝트
    ("PRJ", "/iaas/projects (프로젝트)", "IAAS.PRJ.001", "프로젝트 목록",
     "ETRI, 오케스트로, KETI, 경희대 등 플랫폼을 사용하는 기관·팀별로 격리된 작업 공간(테넌트)을 카드 형태로 표시합니다. 각 카드는 프로젝트명, 소유 기관, 멤버 수, 생성일을 한눈에 보여 줍니다.",
     "Card grid · Avatar",
     "GET /api/iaas/projects",
     "Tenant", "구현 완료"),
    ("PRJ", "/iaas/projects", "IAAS.PRJ.002", "프로젝트 상세 — 개요/쿼터/멤버/자원 4탭",
     "프로젝트 카드를 클릭하면 오른쪽에 상세 패널이 열립니다. 개요(기본 정보), 쿼터(자원 한도), 멤버(소속 사용자), 자원(보유 VM·볼륨 등)을 4개 탭으로 분리해 정보 과부하 없이 조회할 수 있습니다.",
     "Sheet · Tabs × 4",
     "GET /api/iaas/projects/:id (계획)",
     "Tenant", "구현 완료"),
    ("PRJ", "/iaas/projects", "IAAS.PRJ.003", "프로젝트 생성 다이얼로그",
     "새로운 기관이나 팀에 작업 공간을 부여할 때 사용합니다. 프로젝트명·소유 기관·담당자 연락처를 입력하면 OpenStack 테넌트가 생성되고 초기 쿼터가 할당됩니다.",
     "Dialog · Form (react-hook-form + zod)",
     "POST /api/iaas/projects (계획)",
     "Tenant", "UI 반영"),
    ("PRJ", "/iaas/projects", "IAAS.PRJ.004", "쿼터 변경 요청",
     "프로젝트가 현재 할당받은 자원 한도(vCPU·RAM·GPU)를 늘려 달라고 요청합니다. 요청은 관리자 승인 워크플로를 거쳐 반영되어 부서 간 자원 분배의 형평성을 유지하면서도 신속한 확장을 가능하게 합니다.",
     "Button · Dialog",
     "POST /api/iaas/projects/:id/quota-request (계획)",
     "Tenant/Quota", "UI 반영"),
    ("PRJ", "/iaas/projects", "IAAS.PRJ.005", "멤버 초대",
     "이메일로 사용자를 프로젝트에 초대합니다. 역할(관리자·일반 사용자 등)을 함께 지정해 권한을 차등 부여할 수 있습니다.",
     "Button · Dialog",
     "POST /api/iaas/projects/:id/members (계획)",
     "Member", "UI 반영"),
    ("PRJ", "/iaas/projects", "IAAS.PRJ.006", "멤버 제거",
     "프로젝트에서 사용자를 제외합니다. 퇴사자 정리, 외부 협력자 종료 등의 시나리오에서 사용됩니다.",
     "Button (icon)",
     "DELETE /api/iaas/projects/:id/members/:userId (계획)",
     "Member", "UI 반영"),
    ("PRJ", "/iaas/projects", "IAAS.PRJ.007", "프로젝트 편집",
     "프로젝트명·설명·담당자 연락처 등을 수정합니다. 조직 변경이나 담당자 교체 시 활용합니다.",
     "Button · Dialog",
     "PATCH /api/iaas/projects/:id (계획)",
     "Tenant", "UI 반영"),
    ("PRJ", "/iaas/projects", "IAAS.PRJ.008", "프로젝트 활동 로그",
     "이 프로젝트 안에서 누가 언제 무엇을 했는지의 감사 로그를 IAM 감사 페이지에서 필터링된 상태로 조회합니다. 보안 사고 추적과 감사 대응에 활용됩니다.",
     "Button → /iam/audit?projectId=",
     "GET /api/iam/audit?projectId= (계획)",
     "Audit", "UI 반영"),
    ("PRJ", "/iaas/projects", "IAAS.PRJ.009", "프로젝트 삭제",
     "프로젝트를 영구 제거합니다. 안전을 위해 소속된 VM·볼륨·네트워크를 먼저 모두 정리해야만 삭제 가능하며, 단계적 절차를 따르도록 강제해 데이터 손실을 방지합니다.",
     "Button (destructive)",
     "DELETE /api/iaas/projects/:id (계획)",
     "Tenant", "UI 반영"),

    # 프로바이더 (센터/존/풀)
    ("PRV", "/iaas/providers (프로바이더)", "IAAS.PRV.001", "센터 목록",
     "물리 데이터센터(대전 본센터·판교 클라우드센터)를 카드로 표시합니다. 위치·코드·설립일 등 각 센터의 기본 정보를 한눈에 확인할 수 있습니다.",
     "Card grid",
     "GET /api/iaas/centers",
     "Center", "구현 완료"),
    ("PRV", "/iaas/providers", "IAAS.PRV.002", "센터 추가",
     "새로운 물리 데이터센터를 등록합니다. 센터명·위치 코드·주소 등을 입력해 등록합니다.",
     "Dialog · Form",
     "POST /api/iaas/centers (계획)",
     "Center", "UI 반영"),
    ("PRV", "/iaas/providers", "IAAS.PRV.003", "센터 상세 — 존·네트워크 2탭",
     "센터 카드를 클릭하면 그 안에 있는 가용 영역(Zone) 목록과 센터 전체의 네트워크 구성을 2개 탭으로 분리해 조회할 수 있습니다.",
     "Sheet · Tabs × 2",
     "GET /api/iaas/centers/:id (계획)",
     "Center", "구현 완료"),
    ("PRV", "/iaas/providers", "IAAS.PRV.004", "존 추가",
     "센터 내에 새 가용 영역을 추가합니다. 가용 영역은 같은 센터 안에서도 전원·냉방·네트워크가 독립된 구역으로, 장애 격리 단위로 활용됩니다.",
     "Button (outline)",
     "POST /api/iaas/zones (계획)",
     "Zone", "UI 반영"),
    ("PRV", "/iaas/providers", "IAAS.PRV.005", "자원풀(OpenStack 클러스터) 목록",
     "각 가용 영역에 배치된 OpenStack 클러스터를 카드로 표시합니다. Keystone 엔드포인트 주소·버전·노드 수·가속기 보유량을 한 화면에서 확인합니다.",
     "Card grid",
     "GET /api/iaas/pools",
     "Pool", "구현 완료"),
    ("PRV", "/iaas/providers", "IAAS.PRV.006", "자원풀 등록",
     "새로운 OpenStack 클러스터를 CMP에 연결합니다. Keystone 엔드포인트·자격증명·지역(Region) 정보를 입력하면 CMP가 해당 클러스터의 자원을 즉시 인식하기 시작합니다.",
     "Dialog · Form",
     "POST /api/iaas/pools (계획)",
     "Pool", "UI 반영"),
    ("PRV", "/iaas/providers", "IAAS.PRV.007", "자원풀 상세 — 노드·서비스·헬스 3탭",
     "풀 카드를 클릭하면 그 풀에 속한 물리 노드, OpenStack 코어 서비스(Nova·Neutron·Glance·Cinder), 종합 헬스 상태를 3개 탭으로 분리해 조회합니다. 풀 단위 운영 가시성을 단일 화면으로 제공합니다.",
     "Sheet · Tabs × 3",
     "GET /api/iaas/pools/:id (계획)",
     "Pool", "구현 완료"),
    ("PRV", "/iaas/providers", "IAAS.PRV.008", "풀 연결 테스트",
     "Keystone 인증과 주요 서비스 엔드포인트의 응답 가능 여부를 즉시 테스트합니다. 풀 등록 직후나 장애 의심 시 1~2초 안에 연결 상태를 확인할 수 있습니다.",
     "Button (outline)",
     "POST /api/iaas/pools/:id/test (계획)",
     "Pool", "UI 반영"),
    ("PRV", "/iaas/providers", "IAAS.PRV.009", "풀 동기화",
     "풀의 자원 상태와 메타데이터를 OpenStack에서 다시 가져와 CMP DB를 갱신합니다. 외부에서 일어난 변경이 CMP에 자동 반영되지 않을 때 수동으로 동기화 트리거를 거는 안전판입니다.",
     "Button (outline)",
     "POST /api/iaas/pools/:id/sync (계획)",
     "Pool", "UI 반영"),
    ("PRV", "/iaas/providers", "IAAS.PRV.010", "종합 헬스체크",
     "풀에 속한 모든 OpenStack 서비스(Nova·Neutron·Glance·Cinder·Keystone 등)의 상태를 일괄 점검합니다. 어디가 문제인지 한 번에 진단되어 장애 대응 시간이 크게 단축됩니다.",
     "Button",
     "POST /api/iaas/pools/:id/healthcheck (계획)",
     "Pool", "UI 반영"),
    ("PRV", "/iaas/providers", "IAAS.PRV.011", "풀 제거",
     "OpenStack 클러스터를 CMP 등록에서 해제합니다. 연결된 자원이 있을 때는 정리 후에만 가능하며, 운영 중인 환경을 실수로 끊는 것을 방지합니다.",
     "Button (destructive)",
     "DELETE /api/iaas/pools/:id (계획)",
     "Pool", "UI 반영"),

    # 볼륨
    ("VOL", "/iaas/volumes (볼륨)", "IAAS.VOL.001", "볼륨 목록",
     "VM에 연결되거나 단독으로 존재하는 모든 영구 디스크(블록 스토리지)를 표 형태로 조회합니다. 이름·크기·종류(SSD/HDD/NVMe)·연결된 VM·상태를 한눈에 확인할 수 있습니다.",
     "Table",
     "GET /api/iaas/volumes",
     "Volume", "구현 완료"),
    ("VOL", "/iaas/volumes", "IAAS.VOL.002", "볼륨 생성",
     "새 볼륨을 만듭니다. 빈 볼륨으로 시작·이미지에서 부팅 가능한 볼륨 생성·기존 스냅샷에서 복원의 3가지 방식을 지원하여 다양한 시나리오를 한 다이얼로그에서 처리합니다.",
     "Dialog · Select · Form",
     "POST /api/iaas/volumes (계획)",
     "Volume", "UI 반영"),
    ("VOL", "/iaas/volumes", "IAAS.VOL.003", "볼륨 상세 Sheet — 정보·스냅샷·액션·메타 4탭",
     "볼륨 카드를 클릭하면 정보(기본 메타), 스냅샷 목록, 액션 그리드, YAML 메타데이터를 4개 탭으로 분리해 조회합니다. 볼륨 한 개에 대한 모든 정보를 단일 화면에서 다룹니다.",
     "Sheet · Tabs × 4",
     "GET /api/iaas/volumes/:id (계획)",
     "Volume", "구현 완료"),
    ("VOL", "/iaas/volumes", "IAAS.VOL.004", "볼륨 크기 확장",
     "사용 중인 볼륨의 용량을 더 크게 늘립니다. 용량 부족 상황에서 무중단으로 확장할 수 있으나, 데이터 안전을 위해 축소는 불가능합니다.",
     "Button · Dialog",
     "PATCH /api/iaas/volumes/:id (계획)",
     "Volume", "UI 반영"),
    ("VOL", "/iaas/volumes", "IAAS.VOL.005", "볼륨 스냅샷 생성",
     "현재 시점의 볼륨 상태를 스냅샷으로 즉시 저장합니다. 중요 작업 전 백업하거나, 동일 상태의 볼륨을 다수 복제할 때 기준으로 활용됩니다.",
     "Button",
     "POST /api/iaas/volumes/:id/snapshots (계획)",
     "Snapshot", "UI 반영"),
    ("VOL", "/iaas/volumes", "IAAS.VOL.006", "스냅샷으로부터 복원",
     "이전에 만든 스냅샷을 새 볼륨으로 복원합니다. 데이터 손실 사고가 발생했을 때 신속한 복구 경로 역할을 합니다.",
     "Button (ghost)",
     "POST /api/iaas/volumes (from snapshot, 계획)",
     "Volume/Snapshot", "UI 반영"),
    ("VOL", "/iaas/volumes", "IAAS.VOL.007", "VM에 볼륨 연결",
     "available 상태의 볼륨을 선택한 VM에 attach합니다. 추가 디스크가 필요한 VM에 동적으로 스토리지를 붙일 수 있습니다.",
     "Button (outline)",
     "POST /api/iaas/volumes/:id/attach (계획)",
     "Volume/VM", "UI 반영"),
    ("VOL", "/iaas/volumes", "IAAS.VOL.008", "VM에서 볼륨 분리",
     "in-use 상태의 볼륨을 VM에서 detach합니다. 다른 VM으로 옮기거나 단독 보관할 때 사용합니다.",
     "Button (outline)",
     "POST /api/iaas/volumes/:id/detach (계획)",
     "Volume/VM", "UI 반영"),
    ("VOL", "/iaas/volumes", "IAAS.VOL.009", "볼륨을 이미지로 변환",
     "특정 볼륨의 현재 상태를 Glance 이미지로 만들어 카탈로그에 등록합니다. 잘 설정된 디스크를 표준 'Golden Image'로 화하는 패턴을 한 클릭으로 처리합니다.",
     "Button (outline)",
     "POST /api/iaas/volumes/:id/upload-image (계획)",
     "Volume/Image", "UI 반영"),
    ("VOL", "/iaas/volumes", "IAAS.VOL.010", "볼륨 삭제",
     "볼륨을 영구 제거합니다. VM에 연결된(in-use) 상태일 때는 안전을 위해 거부되며, 분리한 후에만 삭제 가능합니다.",
     "Button (destructive)",
     "DELETE /api/iaas/volumes/:id (계획)",
     "Volume", "UI 반영"),
    ("VOL", "/iaas/volumes", "IAAS.VOL.011", "CSV 내보내기",
     "전체 볼륨 목록을 CSV 파일로 다운로드합니다. 외부 자산 관리 시스템과 연계하거나 운영 리포트 작성 시 활용합니다.",
     "Button (outline)",
     "(client only)",
     "Volume", "UI 반영"),
]

# ─────────────────────────────────────────────────────────────
# 데이터: PaaS 기능
# ─────────────────────────────────────────────────────────────
PAAS_FUNCS = [
    # PaaS 개요
    ("OVR", "/paas (개요)", "PAAS.OVR.001", "클러스터 분포 표시",
     "openKcloud는 관리 클러스터(mgmt)와 서비스 클러스터(svc)의 2-tier 구조를 갖습니다. 관리 클러스터는 플랫폼 자체를 운영하기 위한 것이고, 서비스 클러스터는 사용자 워크로드용입니다. 이 화면은 두 종류의 클러스터를 카드로 분리해 표시하므로, 어떤 클러스터가 어떤 역할을 하는지 한눈에 구분됩니다.",
     "Card grid · Badge",
     "GET /api/v2/clusters (mgmt + svc)",
     "Cluster", "구현 완료"),
    ("OVR", "/paas (개요)", "PAAS.OVR.002", "워크로드 상태 분포",
     "전체 워크로드를 Running·Pending·Failed 상태별로 카운트해 카드로 요약합니다. 비정상 워크로드의 수치를 즉시 확인할 수 있어 운영자가 빠르게 대응 우선순위를 정할 수 있습니다.",
     "StatCard × N",
     "GET /api/paas/workloads",
     "Workload", "구현 완료"),
    ("OVR", "/paas (개요)", "PAAS.OVR.003", "리소스 사용량 요약",
     "전체 클러스터의 CPU·메모리·GPU 사용률을 진행률 바로 요약합니다. 클러스터 단위의 자원 포화도를 직관적으로 파악해 노드 증설이나 부하 재분배 시점을 가늠합니다.",
     "Progress bar",
     "GET /api/paas/clusters",
     "Stats", "구현 완료"),

    # 클러스터
    ("CLU", "/paas/clusters", "PAAS.CLU.001", "관리 클러스터 카드",
     "openKcloud 플랫폼 자체를 운영하는 관리용 K8s 클러스터(mgmt-prod-dj)를 표시합니다. 컨트롤 플레인 노드 수·워커 노드 수·K8s 버전·CNI 종류 등 운영 기준 정보를 한 카드에 모아 보여 줍니다.",
     "MgmtClusterCard",
     "GET /api/v2/clusters (kind=mgmt)",
     "Cluster", "구현 완료"),
    ("CLU", "/paas/clusters", "PAAS.CLU.002", "서비스 클러스터 카드",
     "프로젝트별 서비스용 K8s 클러스터(svc-ai-prod·svc-edge-staging)를 표시합니다. OpenStack Magnum이 자동 프로비저닝한 클러스터로, 프로젝트가 과금·격리·자원할당의 단위가 됩니다.",
     "SvcClusterCard",
     "GET /api/v2/clusters (kind=svc)",
     "Cluster", "구현 완료"),
    ("CLU", "/paas/clusters", "PAAS.CLU.003", "클러스터 상태 표시",
     "각 클러스터의 마스터/워커 노드 수·K8s 버전·CNI(Calico·Cilium 등 네트워크 플러그인)·전반적인 상태를 배지와 통계 행으로 요약합니다. 운영 상황 점검의 단일 진실 소스입니다.",
     "Badge · Stat row",
     "GET /api/paas/clusters",
     "Cluster", "구현 완료"),
    ("CLU", "/paas/clusters", "PAAS.CLU.004", "클러스터 자원 사용량",
     "CPU·메모리·스토리지 사용률을 진행률 바와 HealthRing 시각화로 표시합니다. 클러스터별 부하 분포를 즉각 비교할 수 있어 자원 이동·증설 의사결정의 근거가 됩니다.",
     "Progress · HealthRing",
     "GET /api/paas/clusters/:id",
     "Cluster", "구현 완료"),
    ("CLU", "/paas/clusters", "PAAS.CLU.005", "노드 리스트 표시",
     "각 클러스터에 소속된 노드 수와 노드 ID 목록을 보여 줍니다. 노드를 클릭하면 물리 인프라 페이지로 이동해 그 노드의 상세 자원 정보를 확인할 수 있어 PaaS↔IaaS 계층을 자연스럽게 넘나들 수 있습니다.",
     "Card · Link",
     "GET /api/paas/clusters/:id",
     "Node", "구현 완료"),
    ("CLU", "/paas/clusters", "PAAS.CLU.006", "클러스터 생성 위저드",
     "Magnum 기반으로 새 서비스 클러스터를 만드는 단계별 위저드입니다. 노드 수·K8s 버전·CNI 선택 등을 입력해 자동 프로비저닝을 트리거할 수 있도록 설계됐으며, 차차년도에 구체 UI가 완성될 예정입니다.",
     "(Wizard placeholder)",
     "POST /api/v2/clusters (계획)",
     "Cluster", "향후 구현"),
    ("CLU", "/paas/clusters", "PAAS.CLU.007", "kubeconfig 다운로드",
     "kubectl 명령어로 클러스터에 접속할 수 있는 자격증명 파일을 클러스터별로 다운로드합니다. 개발자가 자신의 로컬 PC에서 kubectl로 클러스터를 다루기 위한 표준 경로 역할을 합니다.",
     "Button (계획)",
     "GET /api/paas/clusters/:id/kubeconfig (계획)",
     "Cluster", "향후 구현"),

    # 워크로드
    ("WL", "/paas/workloads (워크로드)", "PAAS.WL.001", "워크로드 목록",
     "Kubernetes 위에서 실행 중인 모든 워크로드(Deployment·StatefulSet·DaemonSet)를 하나의 통합 목록으로 표시합니다. 종류별 색상 배지로 구분되어 15개 이상의 샘플 데이터에서도 원하는 워크로드를 즉시 식별할 수 있습니다.",
     "Table · Badge (kind)",
     "GET /api/paas/workloads?clusterId=&kind=",
     "Workload", "구현 완료"),
    ("WL", "/paas/workloads", "PAAS.WL.002", "워크로드 검색·필터",
     "클러스터·네임스페이스·워크로드 종류로 필터링하고 이름으로 검색합니다. 수백 개의 워크로드 중에서 특정 서비스만 빠르게 추리는 일상 운영의 핵심 도구입니다.",
     "Select × 3 · Input",
     "GET /api/paas/workloads",
     "Workload", "구현 완료"),
    ("WL", "/paas/workloads/[id]", "PAAS.WL.003", "워크로드 상세",
     "워크로드 한 개를 선택하면 Pod 상태, 자원 사용량, 이벤트 타임라인, 배포 히스토리를 모두 한 화면에서 볼 수 있습니다. 장애 분석과 배포 검증의 출발점이 되는 단일 통합 뷰입니다.",
     "Tabs · Recharts · Timeline",
     "GET /api/paas/workloads/:id",
     "Workload", "구현 완료"),
    ("WL", "/paas/workloads", "PAAS.WL.004", "레플리카 스케일",
     "워크로드의 Pod 복제 수를 조정합니다. 트래픽 증가에 대비해 미리 늘리거나 야간에 줄여 비용을 절감하는 등 운영 패턴에 맞게 조절합니다. HPA(자동 스케일러)와의 충돌이 감지되면 경고를 표시해 의도하지 않은 결과를 방지합니다.",
     "DropdownMenu · Dialog · Input",
     "PATCH /api/paas/workloads/:id/scale (계획)",
     "Workload", "UI 반영"),
    ("WL", "/paas/workloads", "PAAS.WL.005", "매니페스트 편집",
     "워크로드의 YAML 매니페스트를 GUI 편집기에서 직접 수정합니다. kubectl edit과 동일한 효과를 브라우저에서 빠르게 처리할 수 있어 터미널 전환 없이 운영이 가능합니다.",
     "DropdownMenu · YAML editor",
     "PATCH /api/paas/workloads/:id (계획)",
     "Workload", "UI 반영"),
    ("WL", "/paas/workloads", "PAAS.WL.006", "롤아웃 재시작",
     "kubectl rollout restart에 해당하는 동작으로, 워크로드의 모든 Pod를 순차적으로 재생성합니다. ConfigMap 변경 후 환경변수 갱신 적용, 메모리 누수에 대한 임시 대응 등 일상 운영에서 자주 사용됩니다.",
     "DropdownMenu item",
     "POST /api/paas/workloads/:id/rollout/restart (계획)",
     "Workload", "UI 반영"),
    ("WL", "/paas/workloads", "PAAS.WL.007", "이전 버전 롤백",
     "최근 배포에서 문제가 발견됐을 때 이전 버전(revision 5 → 4 형태)으로 즉시 되돌립니다. 배포 사고 시 신속한 복구 경로 — 사용자가 영향을 받기 전에 되돌릴 수 있는 안전판입니다.",
     "DropdownMenu item",
     "POST /api/paas/workloads/:id/rollout/undo (계획)",
     "Workload", "UI 반영"),
    ("WL", "/paas/workloads", "PAAS.WL.008", "Pod 셸 진입",
     "kubectl exec에 해당하는 동작으로, 컨테이너 안에서 직접 셸 명령을 실행할 수 있습니다. 로그 확인·임시 디버깅·파일 점검 등 운영자가 컨테이너 내부를 살펴봐야 할 때 활용합니다.",
     "DropdownMenu item · (Terminal placeholder)",
     "WS /api/paas/pods/:id/exec (계획)",
     "Pod", "UI 반영"),
    ("WL", "/paas/workloads", "PAAS.WL.009", "워크로드 삭제",
     "워크로드를 제거합니다. finalizer가 실행되면서 연결된 자원을 정리한 후 안전하게 삭제되어 리소스 누수를 방지합니다.",
     "DropdownMenu item (rose)",
     "DELETE /api/paas/workloads/:id (계획)",
     "Workload", "UI 반영"),

    # 서비스
    ("SVC", "/paas/services (Service/Ingress)", "PAAS.SVC.001", "서비스 목록",
     "Kubernetes Service 리소스를 ClusterIP(내부 통신용)·NodePort(노드 포트로 노출)·LoadBalancer(클라우드 LB로 외부 노출) 종류별로 분류해 표시합니다. 어떤 트래픽 노출 방식이 어떤 워크로드에 적용됐는지 한 화면에서 파악할 수 있습니다.",
     "Table · Badge (type)",
     "GET /api/paas/services",
     "Service", "구현 완료"),
    ("SVC", "/paas/services", "PAAS.SVC.002", "서비스 상세 Sheet — 포트·엔드포인트·셀렉터·YAML 4탭",
     "서비스 한 개를 클릭하면 포트 매핑·실제 엔드포인트(연결된 Pod IP)·셀렉터 규칙·전체 YAML을 네 탭으로 분리해 조회합니다. 라우팅 문제 트러블슈팅의 출발점이 되는 단일 통합 뷰입니다.",
     "Sheet · Tabs × 4",
     "GET /api/paas/services/:id (계획)",
     "Service", "구현 완료"),
    ("SVC", "/paas/services", "PAAS.SVC.003", "서비스 생성 다이얼로그",
     "새로운 Service를 만듭니다. 이름·종류·셀렉터·포트 매핑을 입력해 워크로드를 외부 또는 클러스터 내부에 노출시킵니다.",
     "Dialog · Form",
     "POST /api/paas/services (계획)",
     "Service", "UI 반영"),
    ("SVC", "/paas/services", "PAAS.SVC.004", "YAML 편집",
     "서비스 매니페스트를 직접 편집합니다. 셀렉터 변경·포트 추가·어노테이션 수정 등을 GUI에서 빠르게 처리할 수 있어 운영자 편의가 큽니다.",
     "Button · YAML editor",
     "PATCH /api/paas/services/:id (계획)",
     "Service", "UI 반영"),
    ("SVC", "/paas/services", "PAAS.SVC.005", "외부 IP 접속",
     "LoadBalancer 타입 서비스의 외부 IP를 새 브라우저 탭에서 즉시 엽니다. 배포된 서비스가 외부에서 실제로 보이는지 클릭 한 번으로 검증할 수 있어 배포 직후 확인 단계가 매우 간결해집니다.",
     "Button (outline)",
     "(client only)",
     "Service", "구현 완료"),
    ("SVC", "/paas/services", "PAAS.SVC.006", "kubectl port-forward",
     "로컬 포트와 Pod 포트를 연결하는 명령어를 자동 생성합니다. 외부에 노출하지 않고도 개발자가 자신의 PC에서 클러스터 내 서비스를 테스트할 수 있는 안전한 검증 경로를 제공합니다.",
     "Button",
     "POST /api/paas/services/:id/port-forward (계획)",
     "Service", "UI 반영"),
    ("SVC", "/paas/services", "PAAS.SVC.007", "서비스 삭제",
     "Service 리소스를 제거합니다. 워크로드 자체는 계속 실행되지만 외부 노출 경로가 닫히므로, 부분적 접근 차단 시나리오에 활용됩니다.",
     "Button (destructive)",
     "DELETE /api/paas/services/:id (계획)",
     "Service", "UI 반영"),
    ("SVC", "/paas/services", "PAAS.SVC.008", "YAML 다운로드",
     "전체 서비스의 매니페스트를 YAML 파일로 일괄 다운로드합니다. GitOps 워크플로의 출발점이나 다른 클러스터로의 마이그레이션에 활용할 수 있습니다.",
     "Button (outline)",
     "GET /api/paas/services?export=yaml (계획)",
     "Service", "UI 반영"),

    # 스토리지
    ("STO", "/paas/storage (PVC/PV/StorageClass)", "PAAS.STO.001", "PVC 목록",
     "워크로드가 영구 데이터를 저장하기 위해 요청한 스토리지 청구(PersistentVolumeClaim)들의 목록입니다. 네임스페이스·이름·StorageClass·크기·바인딩 상태를 표로 확인할 수 있습니다.",
     "Tabs · Table",
     "GET /api/paas/pvcs (계획)",
     "PVC", "구현 완료"),
    ("STO", "/paas/storage", "PAAS.STO.002", "PV 목록",
     "실제로 프로비저닝된 영구 볼륨(PersistentVolume)을 표시합니다. 용량·바인딩 상태·Reclaim 정책(삭제 시 데이터를 어떻게 처리할지)을 확인할 수 있어 스토리지 자원 흐름을 파악할 수 있습니다.",
     "Table",
     "GET /api/paas/pvs (계획)",
     "PV", "구현 완료"),
    ("STO", "/paas/storage", "PAAS.STO.003", "StorageClass 목록",
     "사용 가능한 스토리지 종류 카탈로그입니다. 어떤 Provisioner(예: Ceph RBD·NFS)가 어떤 파라미터로 동작하는지, 어느 클래스가 기본값인지 한 화면에서 확인합니다.",
     "Table · Badge (default)",
     "GET /api/paas/storage-classes (계획)",
     "StorageClass", "구현 완료"),
    ("STO", "/paas/storage", "PAAS.STO.004", "PVC 생성",
     "워크로드용 새 PVC를 만듭니다. 이름·StorageClass·접근 모드(RWO: 단일 노드 읽기쓰기 / ROX: 다중 노드 읽기 / RWX: 다중 노드 읽기쓰기)·크기를 입력해 신청하면 K8s가 적합한 PV를 자동 바인딩해 줍니다.",
     "Dialog · Form",
     "POST /api/paas/pvcs (계획)",
     "PVC", "UI 반영"),
    ("STO", "/paas/storage", "PAAS.STO.005", "PVC 상세 Sheet — 정보·이벤트·사용 Pod·YAML 4탭",
     "PVC 한 개를 클릭하면 기본 정보·바인딩 과정의 이벤트 로그·이 PVC를 사용하는 Pod 목록·전체 매니페스트를 네 탭으로 조회합니다. 스토리지 트러블슈팅의 시작점입니다.",
     "Sheet · Tabs × 4",
     "GET /api/paas/pvcs/:id (계획)",
     "PVC", "구현 완료"),
    ("STO", "/paas/storage", "PAAS.STO.006", "PVC 크기 확장",
     "운영 중인 워크로드의 영구 디스크 용량을 늘립니다. AllowVolumeExpansion=true로 설정된 StorageClass에서만 가능한 무중단 확장 기능으로, 데이터를 그대로 유지하면서 용량만 키울 수 있습니다.",
     "Button · Dialog",
     "PATCH /api/paas/pvcs/:id (계획)",
     "PVC", "UI 반영"),
    ("STO", "/paas/storage", "PAAS.STO.007", "PVC 스냅샷 생성",
     "VolumeSnapshot CRD를 생성해 현재 시점의 데이터 상태를 저장합니다. 정기 백업·데이터 복제·검증 환경 구축 등 다양한 시나리오에서 활용됩니다.",
     "Button · Dialog",
     "POST /api/paas/volume-snapshots (계획)",
     "Snapshot", "UI 반영"),
    ("STO", "/paas/storage", "PAAS.STO.008", "PVC 삭제",
     "PVC를 제거합니다. StorageClass의 ReclaimPolicy(Retain·Delete·Recycle 중 하나)에 따라 연결된 PV와 실제 데이터의 운명이 결정되므로 정책에 대한 이해가 필요합니다.",
     "Button (destructive)",
     "DELETE /api/paas/pvcs/:id (계획)",
     "PVC", "UI 반영"),
    ("STO", "/paas/storage", "PAAS.STO.009", "PV YAML 보기",
     "특정 PV의 전체 YAML 매니페스트를 토스트로 펼쳐 볼 수 있습니다. PV가 실제로 어떻게 구성되었는지 즉석에서 확인할 때 사용합니다.",
     "Button (icon)",
     "GET /api/paas/pvs/:id (계획)",
     "PV", "UI 반영"),
    ("STO", "/paas/storage", "PAAS.STO.010", "StorageClass 상세",
     "StorageClass의 Provisioner·파라미터·Reclaim Policy를 상세 패널에서 조회합니다. 어떤 스토리지 옵션이 어떻게 동작하는지에 대한 단일 진실 소스 역할을 합니다.",
     "Sheet",
     "GET /api/paas/storage-classes/:id (계획)",
     "StorageClass", "구현 완료"),
    ("STO", "/paas/storage", "PAAS.STO.011", "StorageClass 기본으로 설정",
     "is-default-class 어노테이션을 토글해 기본 StorageClass를 변경합니다. PVC가 StorageClass를 명시하지 않을 때 자동으로 적용될 기본값을 정하는 운영 설정입니다.",
     "Button",
     "PATCH /api/paas/storage-classes/:id (계획)",
     "StorageClass", "UI 반영"),
    ("STO", "/paas/storage", "PAAS.STO.012", "StorageClass 편집/삭제",
     "StorageClass의 설정을 변경하거나 사용하지 않는 클래스를 제거합니다. 사용 중인 PVC가 있을 때는 데이터 무결성을 위해 삭제가 거부되어 운영자가 실수로 자원을 끊는 것을 방지합니다.",
     "Button × 2",
     "PATCH/DELETE /api/paas/storage-classes/:id (계획)",
     "StorageClass", "UI 반영"),
    ("STO", "/paas/storage", "PAAS.STO.013", "PVC CSV 다운로드",
     "전체 PVC 목록을 CSV로 내보냅니다. 스토리지 사용 현황 보고서나 외부 자산 시스템 연동에 활용됩니다.",
     "Button (outline)",
     "(client only)",
     "PVC", "UI 반영"),

    # ConfigMap / Secret
    ("CFG", "/paas/config (ConfigMap·Secret)", "PAAS.CFG.001", "ConfigMap 목록",
     "워크로드에 외부 주입되는 설정값을 모아둔 ConfigMap을 표 형태로 표시합니다. 네임스페이스·이름·키 개수·최근 변경 시각을 한눈에 확인할 수 있어 설정 관리가 체계적으로 이루어집니다.",
     "Tabs · Table",
     "GET /api/paas/configmaps (계획)",
     "ConfigMap", "구현 완료"),
    ("CFG", "/paas/config", "PAAS.CFG.002", "Secret 목록 (마스킹)",
     "API 키·DB 비밀번호·TLS 인증서 등 민감 정보를 담은 Secret을 표시합니다. 값은 기본적으로 마스킹 처리되어 화면에 노출되지 않으므로, 다른 사람이 옆에서 보더라도 비밀이 새지 않습니다.",
     "Tabs · Table",
     "GET /api/paas/secrets (계획)",
     "Secret", "구현 완료"),
    ("CFG", "/paas/config", "PAAS.CFG.003", "ConfigMap 생성 다이얼로그",
     "새 ConfigMap을 만듭니다. 이름·네임스페이스·key/value 쌍을 입력해 워크로드에서 환경변수나 마운트 파일로 사용할 수 있는 설정을 정의합니다.",
     "Dialog · Form",
     "POST /api/paas/configmaps (계획)",
     "ConfigMap", "UI 반영"),
    ("CFG", "/paas/config", "PAAS.CFG.004", "Secret 생성 다이얼로그",
     "새 Secret을 만듭니다. 종류(Opaque·TLS·Docker registry 등)를 선택하고 데이터를 입력하면 base64 인코딩이 자동 처리되어 사용자가 수동으로 인코딩할 필요가 없습니다.",
     "Dialog · Form · Select",
     "POST /api/paas/secrets (계획)",
     "Secret", "UI 반영"),
    ("CFG", "/paas/config", "PAAS.CFG.005", "ConfigMap 상세 Sheet — Data·정보·사용 Pod·YAML 4탭",
     "ConfigMap 한 개를 클릭하면 key별 값·메타 정보·이 ConfigMap을 마운트한 Pod 목록·전체 YAML을 네 탭으로 조회합니다. 설정 변경이 어디에 영향을 미치는지 사전에 파악할 수 있습니다.",
     "Sheet · Tabs × 4",
     "GET /api/paas/configmaps/:id (계획)",
     "ConfigMap", "구현 완료"),
    ("CFG", "/paas/config", "PAAS.CFG.006", "ConfigMap key 편집",
     "특정 key의 값을 편집기에서 수정합니다. JSON·YAML·properties 등 다양한 포맷의 설정을 GUI에서 안전하게 갱신할 수 있어 운영자가 직접 매니페스트를 다룰 필요가 줄어듭니다.",
     "Button · Dialog",
     "PATCH /api/paas/configmaps/:id (계획)",
     "ConfigMap", "UI 반영"),
    ("CFG", "/paas/config", "PAAS.CFG.007", "Pod 재시작 (변경 적용)",
     "ConfigMap을 변경한 뒤 환경변수로 주입된 값을 새로 읽도록 연결된 Pod를 재시작합니다. (파일로 마운트된 값은 자동으로 갱신되지만, 환경변수는 Pod 재시작이 필요한 K8s의 특성을 반영한 운영 도구입니다.)",
     "Button",
     "POST /api/paas/configmaps/:id/restart-consumers (계획)",
     "ConfigMap/Pod", "UI 반영"),
    ("CFG", "/paas/config", "PAAS.CFG.008", "Secret 상세 — 값 reveal/mask",
     "Secret 상세 패널에서 Eye 아이콘을 클릭하면 마스킹된 값을 일시적으로 노출할 수 있고, 다시 클릭하면 즉시 마스킹됩니다. 운영자가 정확한 값을 확인해야 할 때만 잠깐 보고 바로 가릴 수 있어 보안 위험을 최소화합니다.",
     "Sheet · Toggle",
     "GET /api/paas/secrets/:id (계획)",
     "Secret", "구현 완료"),
    ("CFG", "/paas/config", "PAAS.CFG.009", "Secret 값 클립보드 복사",
     "마스킹 상태에서도 값을 클립보드로 안전하게 복사할 수 있습니다. 화면에는 보이지 않지만 다른 시스템에 붙여넣어야 할 때 사용하는 일상 작업으로, 복사 시 토스트로 사용자에게 확실히 알립니다.",
     "Button (icon) · clipboard API",
     "(client only)",
     "Secret", "구현 완료"),
    ("CFG", "/paas/config", "PAAS.CFG.010", "Secret 로테이션 (Vault 연동)",
     "HashiCorp Vault에서 새 값을 가져와 Secret을 갱신합니다. 비밀번호·키의 주기적 교체 정책을 한 번의 클릭으로 수행할 수 있어 보안 컴플라이언스 대응이 손쉬워집니다.",
     "Button",
     "POST /api/paas/secrets/:id/rotate (계획)",
     "Secret", "UI 반영"),
    ("CFG", "/paas/config", "PAAS.CFG.011", "Secret 편집",
     "Secret 값을 직접 수정합니다. 새 인증서 적용·환경 변경 등의 시나리오에서 사용합니다.",
     "Button · Dialog",
     "PATCH /api/paas/secrets/:id (계획)",
     "Secret", "UI 반영"),
    ("CFG", "/paas/config", "PAAS.CFG.012", "ConfigMap YAML 다운로드",
     "전체 ConfigMap의 매니페스트를 YAML로 일괄 다운로드합니다. 백업·GitOps 적용·다른 환경 복제 등에 활용할 수 있습니다.",
     "Button (outline)",
     "GET /api/paas/configmaps?export=yaml (계획)",
     "ConfigMap", "UI 반영"),
    ("CFG", "/paas/config", "PAAS.CFG.013", "Secret 백업",
     "모든 Secret을 안전하게 백업합니다. 보안 사고나 데이터 손실 대비용 안전망 역할을 하며, 관리자 권한자만 수행 가능하도록 제한되어 있습니다.",
     "Button (outline)",
     "GET /api/paas/secrets/backup (계획)",
     "Secret", "UI 반영"),
]

# ─────────────────────────────────────────────────────────────
# 시트 작성
# ─────────────────────────────────────────────────────────────

COLS = ["분류", "화면 코드", "화면명", "기능 ID", "기능명", "설명", "UI 요소", "API 경로", "관련 자원", "상태"]
COL_WIDTHS = [8, 10, 28, 16, 32, 85, 32, 48, 14, 12]


# ─────────────────────────────────────────────────────────────
# 설명 톤 변환: 평어체(~한다) + dry + 강조 부사 제거
# ─────────────────────────────────────────────────────────────
_TONE_RULES = [
    # 긴 패턴 우선 (짧은 패턴이 먼저 매치되어 잘못 변환되는 것 방지)
    ("보여 줍니다", "보여준다"),
    ("보여줍니다", "보여준다"),
    ("갖습니다", "갖는다"),
    ("받습니다", "받는다"),
    ("줍니다", "준다"),
    ("옵니다", "온다"),
    ("드립니다", "드린다"),
    ("있습니다", "있다"),
    ("없습니다", "없다"),
    ("필요합니다", "필요하다"),
    ("가능합니다", "가능하다"),
    ("부족합니다", "부족하다"),
    ("간결합니다", "간결하다"),
    ("적합합니다", "적합하다"),
    ("동일합니다", "동일하다"),
    ("같습니다", "같다"),
    ("우수합니다", "우수하다"),
    ("좋습니다", "좋다"),
    ("효과적입니다", "효과적이다"),
    # ~됩니다
    ("됩니다", "된다"),
    # 부정 종결
    ("않습니다", "않는다"),
    # 특수 어간 변형 (ㅂ니다 → ㄴ다/는다)
    ("펼쳐집니다", "펼쳐진다"),
    ("열립니다", "열린다"),
    ("닫힙니다", "닫힌다"),
    ("옮깁니다", "옮긴다"),
    ("띄웁니다", "띄운다"),
    ("만듭니다", "만든다"),
    ("내려받습니다", "내려받는다"),
    ("내보냅니다", "내보낸다"),
    ("노출시킵니다", "노출시킨다"),
    ("되돌립니다", "되돌린다"),
    ("말합니다", "말한다"),
    ("다룹니다", "다룬다"),
    ("늘립니다", "늘린다"),
    ("엽니다", "연다"),
    ("이루어집니다", "이루어진다"),
    ("줄어듭니다", "줄어든다"),
    ("알립니다", "알린다"),
    ("손쉬워집니다", "손쉬워진다"),
    ("간결해집니다", "간결해진다"),
    ("쉬워집니다", "쉬워진다"),
    ("어려워집니다", "어려워진다"),
    # 형용사 종결
    ("빠릅니다", "빠르다"),
    ("느립니다", "느리다"),
    ("적습니다", "적다"),
    ("많습니다", "많다"),
    ("큽니다", "크다"),
    ("작습니다", "작다"),
    ("쉽습니다", "쉽다"),
    ("어렵습니다", "어렵다"),
    # ~합니다 (마지막)
    ("합니다", "한다"),
    # 명사 + 입니다 → 이다
    ("입니다", "이다"),
    # 부사·강조 표현 제거
    ("즉시 ", ""),
    ("즉각 ", ""),
    ("한눈에 ", ""),
    ("매우 빠르게 ", ""),
    ("매우 빠른 ", ""),
    ("매우 ", ""),
    ("훨씬 ", ""),
    ("크게 ", ""),
    ("신속한 ", ""),
    ("신속하게 ", ""),
    ("빠르게 ", ""),
    ("곧바로 ", ""),
    ("직관적으로 ", ""),
    ("매끄럽게 ", ""),
    ("자연스럽게 ", ""),
    ("정확하게 ", ""),
    ("일관적으로 ", ""),
]


def to_dry(text: str) -> str:
    if not text:
        return text
    out = text
    for old, new in _TONE_RULES:
        out = out.replace(old, new)
    return out


def setup_columns(ws):
    for i, w in enumerate(COL_WIDTHS, start=1):
        ws.column_dimensions[get_column_letter(i)].width = w


def write_header(ws, row=3, title=None):
    if title:
        ws.cell(row=1, column=1, value=title).font = Font(
            name=FONT_NAME, size=14, bold=True, color="1B3A8A"
        )
    for ci, label in enumerate(COLS, start=1):
        c = ws.cell(row=row, column=ci, value=label)
        c.fill = HDR_FILL
        c.font = HDR_FONT
        c.alignment = CENTER
        c.border = BORDER
    ws.row_dimensions[row].height = 28


def write_row(ws, row, category, item):
    screen_code, screen, fid, name, desc, ui, api, res, status = item
    desc = to_dry(desc)  # 평어체 + dry 변환
    vals = [category, screen_code, screen, fid, name, desc, ui, api, res, status]
    for ci, val in enumerate(vals, start=1):
        c = ws.cell(row=row, column=ci, value=val)
        c.border = BORDER
        c.alignment = WRAP
        c.font = CELL_FONT
        if ci == 1:  # 분류
            c.fill = CATEGORY_FILLS.get(category, PatternFill())
            c.font = Font(name=FONT_NAME, size=10, bold=True)
            c.alignment = CENTER
        elif ci == 2:  # 화면 코드
            c.font = Font(name="Consolas", size=9, color="64748B")
            c.alignment = CENTER
        elif ci == 4:  # 기능 ID
            c.font = ID_FONT
            c.alignment = Alignment(wrap_text=False, vertical="top", horizontal="left")
        elif ci == 8:  # API
            c.font = Font(name="Consolas", size=9, color="334155")
        elif ci == 10:  # 상태
            c.alignment = CENTER
            c.fill = STATUS_FILLS.get(status, PatternFill())
            c.font = Font(name=FONT_NAME, size=9, bold=True)
    # 행 높이 — 설명이 길어진 만큼 충분히 늘려 가독성 확보 (폭 85 컬럼에서 약 50자/줄 기준)
    desc_lines = max(1, len(str(desc)) // 50)
    ws.row_dimensions[row].height = max(34, min(220, 18 + desc_lines * 18))


def add_section_sheet(wb, sheet_name, title, items_with_category):
    ws = wb.create_sheet(title=sheet_name[:31])
    setup_columns(ws)
    write_header(ws, row=3, title=title)
    for i, (cat, item) in enumerate(items_with_category, start=4):
        write_row(ws, i, cat, item)
    last_row = 3 + len(items_with_category)
    ws.auto_filter.ref = f"A3:{get_column_letter(len(COLS))}{last_row}"
    ws.freeze_panes = "A4"


# ─────────────────────────────────────────────────────────────
# 표지·범례·집계
# ─────────────────────────────────────────────────────────────
def add_cover(wb):
    ws = wb.create_sheet(title="표지·범례·집계", index=0)
    for ci, w in enumerate([22, 14, 14, 14, 14], start=1):
        ws.column_dimensions[get_column_letter(ci)].width = w

    ws["A1"] = "openKcloud CMP — IaaS / PaaS 기능 명세서"
    ws["A1"].font = Font(name=FONT_NAME, size=18, bold=True, color="1B3A8A")
    ws["A2"] = "현재 mockup(v1.0) 기준 · OpenStack(IaaS) + Kubernetes(PaaS) 통합 관리"
    ws["A2"].font = Font(name=FONT_NAME, size=12, bold=True, color="475569")
    ws["A3"] = "작성: 오케스트로(주) · 2026.06.04 · 대상 저장소: github.com/openkcloud/kcloud-mnt"
    ws["A3"].font = Font(name=FONT_NAME, size=10, color="64748B")

    # 범례
    ws["A5"] = "상태 범례"
    ws["A5"].font = Font(name=FONT_NAME, size=12, bold=True)

    legends = [
        ("구현 완료", "데이터 조회·표시·시각화가 mockup에서 실제로 동작", "DCFCE7"),
        ("UI 반영", "버튼·다이얼로그·폼이 보이고 toast로 시연 가능. 실제 백엔드 연동은 향후",  "DBEAFE"),
        ("향후 구현", "명세상 자리는 마련되어 있으나 위저드·터미널 등 구체 UI 미반영", "FEF3C7"),
    ]
    for i, (label, desc, color) in enumerate(legends, start=6):
        c = ws.cell(row=i, column=1, value=label)
        c.fill = PatternFill("solid", fgColor=color)
        c.font = Font(name=FONT_NAME, size=10, bold=True)
        c.alignment = CENTER
        c.border = BORDER
        d = ws.cell(row=i, column=2, value=desc)
        d.alignment = WRAP
        d.font = CELL_FONT
        ws.merge_cells(start_row=i, start_column=2, end_row=i, end_column=5)
        d.border = BORDER
        ws.row_dimensions[i].height = 22

    # 카테고리별 집계
    ws["A10"] = "카테고리별 기능 집계"
    ws["A10"].font = Font(name=FONT_NAME, size=12, bold=True)

    headers = ["화면 그룹", "총 건수", "구현 완료", "UI 반영", "향후 구현"]
    for ci, label in enumerate(headers, start=1):
        c = ws.cell(row=11, column=ci, value=label)
        c.fill = HDR_FILL
        c.font = HDR_FONT
        c.alignment = CENTER
        c.border = BORDER
    ws.row_dimensions[11].height = 26

    # 화면 그룹별 함수
    GROUPS = [
        ("IaaS · 개요", [f for f in IAAS_FUNCS if f[0] == "OVR"]),
        ("IaaS · VM 인스턴스", [f for f in IAAS_FUNCS if f[0] == "VM"]),
        ("IaaS · 이미지·Flavor", [f for f in IAAS_FUNCS if f[0] == "IMG"]),
        ("IaaS · 네트워크", [f for f in IAAS_FUNCS if f[0] == "NET"]),
        ("IaaS · 프로젝트", [f for f in IAAS_FUNCS if f[0] == "PRJ"]),
        ("IaaS · 프로바이더", [f for f in IAAS_FUNCS if f[0] == "PRV"]),
        ("IaaS · 볼륨", [f for f in IAAS_FUNCS if f[0] == "VOL"]),
        ("PaaS · 개요", [f for f in PAAS_FUNCS if f[0] == "OVR"]),
        ("PaaS · 클러스터", [f for f in PAAS_FUNCS if f[0] == "CLU"]),
        ("PaaS · 워크로드", [f for f in PAAS_FUNCS if f[0] == "WL"]),
        ("PaaS · 서비스", [f for f in PAAS_FUNCS if f[0] == "SVC"]),
        ("PaaS · 스토리지", [f for f in PAAS_FUNCS if f[0] == "STO"]),
        ("PaaS · Config·Secret", [f for f in PAAS_FUNCS if f[0] == "CFG"]),
    ]

    row = 12
    tot = {"all": 0, "done": 0, "ui": 0, "fut": 0}
    for name, items in GROUPS:
        done = sum(1 for x in items if x[-1] == "구현 완료")
        ui_only = sum(1 for x in items if x[-1] == "UI 반영")
        fut = sum(1 for x in items if x[-1] == "향후 구현")
        ws.cell(row=row, column=1, value=name).font = CELL_FONT
        ws.cell(row=row, column=2, value=len(items)).font = CELL_FONT
        ws.cell(row=row, column=3, value=done).font = CELL_FONT
        ws.cell(row=row, column=4, value=ui_only).font = CELL_FONT
        ws.cell(row=row, column=5, value=fut).font = CELL_FONT
        for ci in range(1, 6):
            cell = ws.cell(row=row, column=ci)
            cell.border = BORDER
            cell.alignment = CENTER if ci > 1 else Alignment(
                wrap_text=True, vertical="center", horizontal="left"
            )
        tot["all"] += len(items)
        tot["done"] += done
        tot["ui"] += ui_only
        tot["fut"] += fut
        row += 1

    # 합계 — 리터럴 값 (Windows 환경에서 LibreOffice 수식 재계산 없이도 즉시 표시)
    sum_row = row
    ws.cell(row=sum_row, column=1, value="합계")
    ws.cell(row=sum_row, column=2, value=tot["all"])
    ws.cell(row=sum_row, column=3, value=tot["done"])
    ws.cell(row=sum_row, column=4, value=tot["ui"])
    ws.cell(row=sum_row, column=5, value=tot["fut"])
    for ci in range(1, 6):
        cell = ws.cell(row=sum_row, column=ci)
        cell.fill = PatternFill("solid", fgColor="F1F5F9")
        cell.border = BORDER
        cell.font = Font(name=FONT_NAME, size=10, bold=True)
        cell.alignment = CENTER if ci > 1 else Alignment(
            wrap_text=True, vertical="center", horizontal="left"
        )

    # 반영률
    ws.cell(row=sum_row + 2, column=1,
            value=f"전체 반영률 (구현+UI): {(tot['done'] + tot['ui']) / tot['all'] * 100:.1f}%").font = Font(
        name=FONT_NAME, size=11, bold=True, color="1B3A8A"
    )
    ws.cell(row=sum_row + 3, column=1,
            value=f"구현 완료율: {tot['done'] / tot['all'] * 100:.1f}%").font = Font(
        name=FONT_NAME, size=10, color="047857"
    )

    # 안내 노트
    ws.cell(row=sum_row + 5, column=1, value="시트 안내").font = Font(name=FONT_NAME, size=12, bold=True)
    notes = [
        "• 전체 기능 — IaaS·PaaS 전체 기능을 한 시트에서 필터·검색",
        "• IaaS 기능 명세 — OpenStack 자원(센터/풀/VM/이미지/네트워크/볼륨/프로젝트) 기능만",
        "• PaaS 기능 명세 — Kubernetes 자원(클러스터/워크로드/서비스/스토리지/Config) 기능만",
        "• 모든 시트에 자동 필터·첫 행 고정 적용 — 화면 코드별, 상태별, 자원 유형별 빠르게 조회 가능",
    ]
    for i, note in enumerate(notes, start=sum_row + 6):
        c = ws.cell(row=i, column=1, value=note)
        c.font = CELL_FONT
        c.alignment = WRAP
        ws.merge_cells(start_row=i, start_column=1, end_row=i, end_column=5)


# ─────────────────────────────────────────────────────────────
# main
# ─────────────────────────────────────────────────────────────
def main():
    wb = Workbook()
    wb.remove(wb.active)

    add_cover(wb)

    # 전체 통합 시트
    all_items = [("IaaS", f) for f in IAAS_FUNCS] + [("PaaS", f) for f in PAAS_FUNCS]
    add_section_sheet(wb, "전체 기능", "IaaS · PaaS 통합 기능 명세 (필터·검색용)", all_items)

    # IaaS 전용 시트
    iaas_items = [("IaaS", f) for f in IAAS_FUNCS]
    add_section_sheet(wb, "IaaS 기능 명세", "IaaS 기능 명세 — OpenStack 기반", iaas_items)

    # PaaS 전용 시트
    paas_items = [("PaaS", f) for f in PAAS_FUNCS]
    add_section_sheet(wb, "PaaS 기능 명세", "PaaS 기능 명세 — Kubernetes 기반", paas_items)

    out_path = os.path.join(os.path.dirname(os.path.abspath(__file__)),
                            "IaaS_PaaS_기능명세서.xlsx")
    wb.save(out_path)


if __name__ == "__main__":
    main()
