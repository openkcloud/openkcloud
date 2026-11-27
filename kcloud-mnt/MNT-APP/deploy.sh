#!/usr/bin/env bash
# openKcloud CMP — Netlify deploy (macOS · Linux)
# Windows에서는 deploy.bat 사용

set -e
cd "$(dirname "$0")"

echo ""
echo "===================================="
echo " openKcloud CMP - Netlify deploy"
echo "===================================="
echo ""

if [ ! -f "out/index.html" ]; then
  echo "[INFO] out/ 폴더가 없음. 먼저 빌드 진행..."
  echo ""
  npm run build
  if [ $? -ne 0 ]; then
    echo ""
    echo "[ERROR] 빌드 실패. 위 에러 메시지를 확인하기."
    exit 1
  fi
fi

echo ""
echo "out/ 폴더를 Netlify에 업로드한다..."
echo "- 처음 실행 시 브라우저가 열려 GitHub/이메일 로그인 1회 필요"
echo "- 이후에는 같은 URL에 자동 덮어쓰기"
echo ""

npx --yes netlify-cli deploy --dir=out --prod

echo ""
echo "===================================="
echo " 완료. 위 'Website URL'이 배포 주소이다."
echo "===================================="
echo ""
