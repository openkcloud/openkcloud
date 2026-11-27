"use client";
import * as React from "react";
import { onlineManager } from "@tanstack/react-query";
import { LogIn, RotateCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import { loginAgain, useAuth } from "@/lib/auth";

/** 로그인이 끝났을 때(로그아웃·미사용·다른 탭·관리자 강제·다른 곳 로그인·계정 문제) — 원인 안내 + 로그인 버튼 모달(연결 문제는 다시 시도).
 *  닫을 수 없다. 뒤의 화면은 레이아웃이 흐리게 두고(SessionBlur), 데이터 갱신은 멈춘다(다음 로그인은 페이지를 새로 연다). */
export function SessionEnded() {
  const ended = useAuth((s) => s.ended);
  React.useEffect(() => {
    onlineManager.setOnline(false); // 조회·자동 갱신 멈춤 — 토큰 없이 API 를 계속 부르지 않게
  }, []);
  const title = ended?.reason === "denied" ? "로그인할 수 없습니다" : ended?.reason === "error" ? "연결할 수 없습니다" : "로그아웃 안내";

  return (
    <Dialog open>
      <DialogContent
        hideClose
        className="max-w-md"
        onEscapeKeyDown={(e) => e.preventDefault()}
        onInteractOutside={(e) => e.preventDefault()}
      >
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{ended?.message ?? "로그아웃 되었습니다."}</DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button onClick={loginAgain}>
            {ended?.reason === "error" ? (
              <>
                <RotateCw className="size-4" /> 다시 시도
              </>
            ) : (
              <>
                <LogIn className="size-4" /> 로그인
              </>
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** 세션이 끝났을 때 레이아웃의 보던 화면을 흐리게(글자를 읽을 수 없게) 하고 조작을 막는다. */
export function SessionBlur({ ended, children }: { ended: boolean; children: React.ReactNode }) {
  return (
    <>
      <div className={ended ? "pointer-events-none select-none blur-md" : "contents"} inert={ended}>
        {children}
      </div>
      {ended && <SessionEnded />}
    </>
  );
}

/** 로그인 확인 중 표시(가드 공통). */
export function AuthLoading({ dark = false }: { dark?: boolean }) {
  return (
    <div className={`flex h-screen w-full items-center justify-center ${dark ? "bg-slate-950" : "bg-background"}`}>
      <div
        className={`size-8 animate-spin rounded-full border-2 border-t-transparent ${dark ? "border-brand-mint-500" : "border-primary"}`}
      />
    </div>
  );
}
