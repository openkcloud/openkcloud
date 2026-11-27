// 배포 버전 — 정적 export 때 out/version.json 으로 만들어진다. 열어 둔 탭이 새 배포를 알아채는 데 쓴다(version-watcher).
export const dynamic = "force-static";

export function GET() {
  return Response.json({ buildId: process.env.NEXT_PUBLIC_BUILD_ID });
}
