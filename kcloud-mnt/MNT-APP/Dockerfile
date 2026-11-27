# syntax=docker/dockerfile:1
# MNT-APP — Next.js 정적 export(out/)를 nginx 로 서빙.
# 빌드: docker build -t harbor.io:7443/mnt/mnt-app:TAG .
# build:gateway = MSW off + same-origin 상대경로(/api/*) — 게이트웨이 라우팅 전제.

# ── build stage ──
FROM node:20-alpine AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
RUN npm run build:gateway

# ── runtime stage ──
FROM nginx:1.27-alpine AS runtime
COPY --from=build /app/out /usr/share/nginx/html
COPY deploy/nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 8080
CMD ["nginx", "-g", "daemon off;"]
