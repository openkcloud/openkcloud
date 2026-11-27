# Mac Mini 로컬 환경 이관 가이드

> Windows(`C:\Users\YUN\projects\openkcloud-cmp`)에서 개발하던 프로젝트를
> Mac Mini 로컬로 옮기는 절차. GitHub 저장소가 이관 매개체다.

---

## 📋 사전 준비 (Mac Mini에 최초 1회 설치)

### 1. Homebrew (없다면)
```sh
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
```

### 2. Node.js (v20 이상)
```sh
brew install node
# 또는 nvm으로 특정 버전 관리
brew install nvm
mkdir -p ~/.nvm
echo 'export NVM_DIR="$HOME/.nvm"' >> ~/.zshrc
echo '[ -s "/opt/homebrew/opt/nvm/nvm.sh" ] && \. "/opt/homebrew/opt/nvm/nvm.sh"' >> ~/.zshrc
source ~/.zshrc
nvm install 20 && nvm use 20
```

### 3. Git
```sh
brew install git
git config --global user.name "YUN Hoyoung"
git config --global user.email "hy.yun@okestro.com"
```

### 4. GitHub 인증 (SSH 권장)
```sh
# SSH 키 생성 (없다면)
ssh-keygen -t ed25519 -C "hy.yun@okestro.com"

# 공개키를 GitHub에 등록: https://github.com/settings/keys
cat ~/.ssh/id_ed25519.pub | pbcopy   # 클립보드에 복사됨
```
공개키를 GitHub 웹에서 붙여넣고 저장.

---

## 🚚 프로젝트 clone

```sh
# 원하는 위치로 이동 (예: ~/projects)
mkdir -p ~/projects && cd ~/projects

# SSH clone (권장)
git clone git@github.com:openkcloud/kcloud-mnt.git openkcloud-cmp

# 또는 HTTPS clone
git clone https://github.com/openkcloud/kcloud-mnt.git openkcloud-cmp

cd openkcloud-cmp

# 의존성 설치
npm install
```

---

## ▶️ 실행

### 개발 서버
```sh
npm run dev
# http://localhost:3000 (포트 사용 중이면 3001)
```

### 로그인 계정
Keycloak(OIDC PKCE) 로그인 — `http://localhost:3000` 으로 열면 Keycloak 로그인 화면으로 넘어간다.
- 접속 PC hosts 에 `172.30.1.200 keycloak.dev.okestro` 등록, MNT-CORE(8000)·MNT-IAAS(8001) 실행
- `localhost` 로만 연다(다른 PC 에서 IP 로 열면 PKCE 가 막힌다). 3001 로 띄우면 CORE·IAAS `CORS_ORIGINS` 에 추가
- 비밀번호는 개발 Keycloak 관리자에게. Netlify 목업 배포(`npm run build`)는 Keycloak 에 닿지 않아 로그인할 수 없다

| 역할 | 개발 계정 |
|---|---|
| 관리자 | `admin` |
| 유저 | `gy.yang` |

### 프로덕션 빌드
```sh
npm run build
# → out/ 폴더에 정적 파일 생성
```

### Netlify 배포 (원클릭)
```sh
# 실행 권한 부여 (최초 1회)
chmod +x deploy.sh

# 실행
./deploy.sh
```
첫 실행 시 브라우저로 Netlify 로그인 1회. 이후 실행은 같은 URL에 자동 덮어쓰기.

Windows용 `deploy.bat`은 사용 안 함 (macOS는 `deploy.sh` 사용).

---

## 📁 Google Drive 문서 접근

원본 요구사항·피드백 PDF는 Google Drive에 있음:

```
Google Drive/내 드라이브/정부R&D과제(수행)/★16-2. AI반도체 클라우드 플랫폼 구축 및 최적화 기술 개발/
  10. 목업자료/ETRI 피드백 자료/
```

### Mac에서 Google Drive Desktop 설치
1. https://www.google.com/drive/download/ 에서 다운로드
2. hy.yun@okestro.com 계정으로 로그인
3. 스트리밍 모드 선택 (권장 — 로컬 디스크 절약)
4. 접근 경로: `~/Library/CloudStorage/GoogleDrive-hy.yun@okestro.com/내 드라이브/...`
   또는 Finder 사이드바의 **Google Drive**

---

## 🔄 Windows·Mac 간 동기화

두 컴퓨터에서 번갈아 작업할 때:

### Mac에서 작업 시작 전
```sh
cd ~/projects/openkcloud-cmp
git pull                # Windows에서 push한 내용 가져오기
npm install             # package.json 변경 있을 시
```

### Mac에서 작업 마친 후
```sh
git add -A
git commit -m "변경 내용 메시지"
git push
```

### Windows에서 다시 이어서 하려면
```powershell
cd C:\Users\YUN\projects\openkcloud-cmp
git pull
npm install
```

**주의**: `node_modules/`, `.next/`, `out/`은 `.gitignore` 처리되어 있어 각 컴퓨터에서 재생성 필요.

---

## 🩹 트러블슈팅

### `npm install` 실패
```sh
# 캐시 정리 후 재시도
rm -rf node_modules package-lock.json
npm install
```

### 포트 3000이 이미 사용 중
```sh
# 다른 프로세스 확인
lsof -i :3000
# 종료 후 재시도, 또는 3001에서 실행됨
```

### `deploy.sh` 실행 권한 에러
```sh
chmod +x deploy.sh
./deploy.sh
```

### GitHub push에 인증 오류
- SSH 사용 중이면: `ssh -T git@github.com`으로 연결 테스트
- HTTPS 사용 중이면: GitHub PAT(Personal Access Token) 발급 → `git config --global credential.helper osxkeychain`

### CRLF/LF 줄바꿈 경고
`.gitattributes`가 없다면 Mac에서 다음 실행:
```sh
git config --global core.autocrlf input
```

---

## 🗂️ 프로젝트 구조 (참고)

```
openkcloud-cmp/
├── src/
│   ├── app/            # Next.js App Router (40+ 페이지)
│   ├── components/     # 공용 UI 컴포넌트
│   ├── lib/            # 유틸·auth·nav·types
│   └── mocks/          # MSW handlers + fixtures
├── docs/               # 요구사항·기능명세 xlsx/docx + 생성 스크립트
├── public/             # 정적 자산 (fonts, mockServiceWorker.js)
├── deploy.sh           # macOS/Linux 배포 스크립트 ← Mac에서 사용
├── deploy.bat          # Windows 배포 스크립트
├── README.md           # 프로젝트 소개 + 피드백 매트릭스
└── MAC_SETUP.md        # 이 파일
```

---

## 🔗 링크
- **저장소**: https://github.com/openkcloud/kcloud-mnt
- **Netlify 대시보드**: https://app.netlify.com
- **연락**: hy.yun@okestro.com
