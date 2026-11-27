"use client";
import * as React from "react";

/**
 * FeatureGate — 형상 게이팅 최소 스텁. 현재는 항상 자식을 렌더한다([[scope]] 스텁과 짝).
 * 추후 2차년도/최종 형상 구분 도입 시 mockup-v2 의 feature-gate 로 교체.
 */
export function FeatureGate({
  children,
}: {
  id?: string;
  fallback?: "note" | "none";
  className?: string;
  children?: React.ReactNode;
}) {
  return <>{children}</>;
}
