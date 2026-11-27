/**
 * 포털 형상(scope) 최소 스텁.
 * mockup-v2 의 lib/scope(2차년도/최종 형상 기능 토글) 설계를 아직 도입하지 않아,
 * 현재는 "모든 기능 표시(always visible)"로 둔다. 추후 형상 구분이 필요하면 교체한다.
 */
export function useScope<T>(selector: (s: { visible: Record<string, boolean> }) => T): T {
  return selector({ visible: {} });
}

export function isFeatureVisible(_visible: unknown, _id: string): boolean {
  return true;
}

export function useFeatureVisible(_id: string): boolean {
  return true;
}
