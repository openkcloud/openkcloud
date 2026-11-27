"use client";
import * as React from "react";
import { usePathname } from "next/navigation";
import { toast } from "sonner";

const CURRENT = process.env.NEXT_PUBLIC_BUILD_ID;
const ENABLED = process.env.NODE_ENV === "production" && !!CURRENT;
const INTERVAL_MS = 60 * 1000;

let notified = false;

async function check() {
  if (notified || document.visibilityState !== "visible") return;
  try {
    const r = await fetch("/version.json", { cache: "no-cache" }); // 바뀌지 않았으면 304
    if (!r.ok) return;
    const { buildId } = (await r.json()) as { buildId?: string };
    // 빌드 ID = 빌드 시각(yyyyMMddHHmmss, next.config) — 더 새로울 때만 알린다.
    // 배포(롤링 업데이트) 중엔 옛 파드가 잠깐 같이 응답해 옛 값이 올 수 있다.
    if (!buildId || buildId <= CURRENT!) return;
    notified = true;
    toast.info("새 버전이 배포되었습니다", {
      id: "new-version", // 창 복귀 때 focus·visibilitychange 가 같이 와도 하나만
      description: "새로고침하면 새 화면을 사용할 수 있습니다.",
      duration: Infinity,
      action: { label: "새로고침", onClick: () => window.location.reload() },
    });
  } catch {
    // 네트워크 오류 — 다음 확인 때 다시
  }
}

/** 새 배포 알림 — 열어 둔 탭이 예전 화면을 계속 쓰지 않게.
 *  화면이 보이는 동안 1분마다, 그리고 탭·창으로 돌아올 때·네트워크가 다시 연결될 때·메뉴를 이동할 때
 *  /version.json(빌드 때 생성, 수십 바이트)을 확인하고, 새 버전이면 새로고침 안내를 띄운다.
 *  입력 중일 수 있어 자동으로 새로고침하지는 않는다. */
export function VersionWatcher() {
  const pathname = usePathname();

  React.useEffect(() => {
    if (!ENABLED) return;
    const timer = setInterval(check, INTERVAL_MS);
    document.addEventListener("visibilitychange", check);
    window.addEventListener("focus", check);
    window.addEventListener("online", check);
    return () => {
      clearInterval(timer);
      document.removeEventListener("visibilitychange", check);
      window.removeEventListener("focus", check);
      window.removeEventListener("online", check);
    };
  }, []);

  // 메뉴 이동 — 미리 받아 둔 화면은 서버 버전 비교 없이 열릴 수 있다
  React.useEffect(() => {
    if (ENABLED) check();
  }, [pathname]);

  return null;
}
