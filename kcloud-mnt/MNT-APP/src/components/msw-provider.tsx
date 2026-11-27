"use client";
import * as React from "react";

const MSW_ENABLED = process.env.NEXT_PUBLIC_MSW_ENABLED !== "false";
const INIT_TIMEOUT_MS = 6000;

export function MSWProvider({ children }: { children: React.ReactNode }) {
  const [ready, setReady] = React.useState(!MSW_ENABLED);

  React.useEffect(() => {
    if (!MSW_ENABLED) return;
    if (typeof window === "undefined") return;

    let cancelled = false;

    // 타임아웃 — Service Worker 등록이 멈춰도 앱은 진입 (fail-open)
    const timeoutId = window.setTimeout(() => {
      if (!cancelled) {
        // eslint-disable-next-line no-console
        console.warn("[MSW] init timeout — rendering app without mocks");
        setReady(true);
      }
    }, INIT_TIMEOUT_MS);

    (async () => {
      try {
        const { worker } = await import("@/mocks/browser");
        await worker.start({
          onUnhandledRequest: "bypass",
          serviceWorker: { url: "/mockServiceWorker.js" },
        });
        if (!cancelled) {
          window.clearTimeout(timeoutId);
          setReady(true);
        }
      } catch (e) {
        // eslint-disable-next-line no-console
        console.error("[MSW] init failed:", e);
        if (!cancelled) {
          window.clearTimeout(timeoutId);
          setReady(true); // fail-open — 앱은 어쨌든 렌더링
        }
      }
    })();

    return () => {
      cancelled = true;
      window.clearTimeout(timeoutId);
    };
  }, []);

  if (!ready) {
    return (
      <div className="flex h-screen w-full items-center justify-center bg-background">
        <div className="flex flex-col items-center gap-3">
          <div className="size-8 animate-spin rounded-full border-2 border-primary border-t-transparent" />
          <p className="text-sm text-muted-foreground">
            openKcloud 준비 중…
          </p>
        </div>
      </div>
    );
  }

  return <>{children}</>;
}
