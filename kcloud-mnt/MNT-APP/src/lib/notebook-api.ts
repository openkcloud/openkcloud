/* ════════════════════════════════════════════════════════════════════
 * Notebook API — Kubeflow Notebook Controller 연동 인터페이스 (MNT-AINB 경유)
 * ────────────────────────────────────────────────────────────────────
 * 노트북·파이프라인·워크스페이스 생성과 노트북 접속·시작·정지·삭제.
 * UI 계층은 이 인터페이스에만 의존합니다.
 * ──────────────────────────────────────────────────────────────────── */

import { useQuery } from "@tanstack/react-query";
import { ainbFetch } from "@/lib/ainb-api";

/** Kubeflow Notebook CRD에 매핑되는 응답 모델 */
export interface NotebookResource {
  /** 네임스페이스 (Kubeflow Profile 매핑) */
  namespace: string;
  /** Notebook CRD name */
  name: string;
  /** Pod 상태 */
  status: "Running" | "Starting" | "Stopped" | "Error";
  /** 컨테이너 이미지 */
  image: string;
  /** 리소스 요청 (K8s ResourceRequirements) */
  resources: {
    cpu: string;
    memory: string;
    /** vendor.com/gpu 또는 vendor.com/npu 등 */
    accelerator?: { kind: string; count: number };
  };
  /** PVC 마운트 정보 */
  workspaceVolume: {
    pvcName: string;
    sizeGb: number;
    mountPath: string;
  };
  /**
   * ⭐ iframe src 로 사용될 URL — MNT-AINB 의 IDE 프록시 경로:
   *   https://{포털 주소}/notebook/{namespace}/{name}/
   * 프록시는 로그인 때 발급한 세션 쿠키(okc_session)로 인증한다.
   */
  connectUrl: string;
  /** 연결 모드 — 실제 Kubeflow가 설치되면 "live", 그렇지 않으면 "mock" */
  connectMode: "live" | "mock";
  /** Kubeflow OAuth2 Proxy가 set-cookie로 발급한 토큰 만료 시각 */
  jwtExpiresAt?: string;
}

/** 노트북 생성 (POST /api/ainb/notebooks) */
export async function createNotebook(body: {
  name: string;
  namespace: string;
  image: string;
  cpu: string;
  memoryGi: number;
  acceleratorKind?: string;
  acceleratorCount: number;
  storageGb: number;
}): Promise<{ id: string }> {
  const res = await ainbFetch(`/notebooks`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  }, 20000);
  if (!res.ok) throw new Error(`${res.status} ${await res.text()}`);
  return res.json();
}

/** Pipeline YAML 업로드 (POST /api/ainb/pipelines/upload) */
export async function uploadPipeline(body: {
  name: string;
  description?: string;
  yaml: string;
}): Promise<{ pipeline_id?: string }> {
  const res = await ainbFetch(`/pipelines/upload`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`${res.status} ${await res.text()}`);
  return res.json();
}

/** Profile 생성 (POST /api/ainb/profiles) */
export async function createProfile(body: {
  name: string;
  ownerEmail: string;
  cpu: number;
  memoryGi: number;
  acceleratorCount: number;
  storageGi: number;
}): Promise<{ name: string }> {
  const res = await ainbFetch(`/profiles`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  }, 20000);
  if (!res.ok) throw new Error(`${res.status} ${await res.text()}`);
  return res.json();
}

/** 노트북 상세 조회 + Code Server 연결 URL 발급 */
async function fetchNotebookConnect(id: string): Promise<NotebookResource> {
  const res = await ainbFetch(`/notebooks/${id}/connect`);
  if (!res.ok) throw new Error(`Notebook connect failed: ${res.status}`);
  return res.json();
}

/** React Query hook */
export function useNotebookConnect(id: string) {
  return useQuery({
    queryKey: ["notebook-connect", id],
    queryFn: () => fetchNotebookConnect(id),
    enabled: !!id,
    refetchOnWindowFocus: false,
  });
}

/** 노트북 시작·정지·삭제 */
export async function notebookAction(
  id: string,
  action: "start" | "stop" | "restart" | "delete",
  opts?: { keepVolume?: boolean },
): Promise<void> {
  const method = action === "delete" ? "DELETE" : "POST";
  const path =
    action === "delete"
      ? `/notebooks/${id}?keepVolume=${opts?.keepVolume ? "true" : "false"}`
      : `/notebooks/${id}/${action}`;
  const res = await ainbFetch(path, { method });
  if (!res.ok) throw new Error(`Notebook ${action} failed: ${res.status}`);
}

/**
 * ════════════════════════════════════════════════════════════════════
 * 구현 참고 — 백엔드(MNT-AINB) / Kubeflow / K8s
 * ════════════════════════════════════════════════════════════════════
 *
 * 1️⃣ MNT-AINB (FastAPI) — 게이트웨이 경로 /api/ainb
 *     POST  /api/ainb/notebooks                  # Notebook CRD 생성
 *     GET   /api/ainb/notebooks                  # 목록
 *     GET   /api/ainb/notebooks/{id}             # 상세
 *     GET   /api/ainb/notebooks/{id}/connect     # connectUrl 발급
 *     POST  /api/ainb/notebooks/{id}/start       # 시작 (annotation: kubeflow-resource-stopped=false)
 *     POST  /api/ainb/notebooks/{id}/stop        # 정지 (annotation: kubeflow-resource-stopped=true)
 *     DELETE /api/ainb/notebooks/{id}            # 삭제
 *
 * 2️⃣ Kubeflow Notebook Controller 설치
 *     $ kubectl apply -k "github.com/kubeflow/kubeflow/components/notebook-controller/config/overlays/standalone?ref=v1.10.0"
 *
 * 3️⃣ Notebook CRD 예시
 *     apiVersion: kubeflow.org/v1
 *     kind: Notebook
 *     metadata:
 *       name: research-llama-finetune
 *       namespace: ai-research            # = 워크스페이스(Kubeflow Profile) = 프로젝트 1:1
 *     spec:
 *       template:
 *         spec:
 *           containers:
 *             - name: notebook
 *               image: kubeflow/codeserver-python:v1.10.0-cuda12.4
 *               resources:
 *                 requests:
 *                   cpu: "16"
 *                   memory: "64Gi"
 *                   rebellions.ai/atom: "2"
 *                 limits:
 *                   cpu: "16"
 *                   memory: "64Gi"
 *                   rebellions.ai/atom: "2"
 *               volumeMounts:
 *                 - name: workspace
 *                   mountPath: /home/kubeflow
 *           volumes:
 *             - name: workspace
 *               persistentVolumeClaim:
 *                 claimName: research-llama-vol
 *
 * 4️⃣ IDE iframe 라우팅 — 게이트웨이가 /notebook/* 을 MNT-AINB 프록시로 넘긴다
 *     (프록시가 세션 쿠키 okc_session 을 검증한 뒤 노트북 서비스로 전달)
 *
 * 5️⃣ Code Server 컨테이너에 RBLN/CUDA SDK 미리 설치된 이미지 사용
 *     - openkcloud/codeserver-rbln:1.4.0
 *     - openkcloud/codeserver-cuda:12.4
 *     - openkcloud/codeserver-pim:0.7
 * ════════════════════════════════════════════════════════════════════
 */
