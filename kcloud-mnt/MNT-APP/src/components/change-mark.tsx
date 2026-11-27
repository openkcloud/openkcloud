"use client";
import * as React from "react";

/**
 * ChangeMark — 목업(mockup-v2) 리뷰용 "변경표시" 컴포넌트의 실(實) 포털용 no-op 대체.
 * 목업에서는 변경 요구사항 코드 배지/점선 테두리를 그렸지만, 운영 포털에서는
 * 주석을 그리지 않고 자식만 그대로 렌더한다.
 */
export function ChangeMark({
  children,
}: {
  ids?: string[];
  children?: React.ReactNode;
  className?: string;
  inline?: boolean;
}) {
  return <>{children}</>;
}
