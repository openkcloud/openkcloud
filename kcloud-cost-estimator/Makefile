# Collector Module Makefile
.PHONY: install test run docker-build docker-run clean lint format k8s-deploy k8s-delete k8s-status

# Python 가상환경 설정
VENV_DIR = venv
PYTHON = $(VENV_DIR)/bin/python
PIP = $(VENV_DIR)/bin/pip

# Docker 설정
IMAGE_NAME = kcloud-cost-estimator
TAG = latest

# Kubernetes 설정
K8S_NAMESPACE = kcloud-system
K8S_DEPLOYMENT_DIR = deployment

# 가상환경 생성 및 의존성 설치
install: $(VENV_DIR)/bin/activate
$(VENV_DIR)/bin/activate: requirements.txt
	python3 -m venv $(VENV_DIR)
	$(PIP) install --upgrade pip
	$(PIP) install -r requirements.txt
	touch $(VENV_DIR)/bin/activate

# 개발 의존성 설치
install-dev: install
	$(PIP) install -e .
	$(PIP) install pytest pytest-cov pytest-asyncio black flake8 mypy

# 테스트 실행
test: install-dev
	$(PYTHON) -m pytest demo/ -v --cov=src --cov-report=html

# 로컬 서버 실행
run: install
	$(PYTHON) -m uvicorn src.main:app --host 0.0.0.0 --port 8001 --reload

# 백그라운드 워커 실행 (Celery)
run-worker: install
	$(PYTHON) -m celery worker -A src.worker:celery_app --loglevel=info

# 코드 포맷팅
format: install-dev
	$(PYTHON) -m black src/ demo/
	$(PYTHON) -m isort src/ demo/

# 린트 검사
lint: install-dev
	$(PYTHON) -m flake8 src/ demo/
	$(PYTHON) -m mypy src/

# Docker 이미지 빌드
docker-build:
	docker build -t $(IMAGE_NAME):$(TAG) .

# Docker 컨테이너 실행
docker-run:
	docker run -d \
		--name kcloud-collector \
		-p 8001:8001 \
		-e KEPLER_PROMETHEUS_URL=http://prometheus:9090 \
		-e REDIS_URL=redis://localhost:6379 \
		-e INFLUXDB_URL=http://influxdb:8086 \
		$(IMAGE_NAME):$(TAG)

# Docker 컨테이너 중지 및 제거
docker-clean:
	docker stop kcloud-collector || true
	docker rm kcloud-collector || true

# Health Check
health-check:
	curl -f http://localhost:8001/health || exit 1

# 정리
clean:
	rm -rf $(VENV_DIR)
	rm -rf __pycache__
	rm -rf .pytest_cache
	rm -rf htmlcov
	find . -name "*.pyc" -delete
	find . -name "*.pyo" -delete

# 로그 확인
logs:
	docker logs -f kcloud-collector

# 개발 모드 전체 실행
dev-up: install
	# Prometheus와 Redis가 실행 중이어야 함
	$(PYTHON) -m uvicorn src.main:app --host 0.0.0.0 --port 8001 --reload &
	$(PYTHON) -m celery worker -A src.worker:celery_app --loglevel=info &

# 개발 환경 정리
dev-down:
	pkill -f uvicorn || true
	pkill -f celery || true

# ========================================
# Kubernetes Deployment
# ========================================

# K8s 전체 배포
k8s-deploy:
	kubectl apply -f $(K8S_DEPLOYMENT_DIR)/namespace.yaml
	kubectl apply -f $(K8S_DEPLOYMENT_DIR)/rbac.yaml
	kubectl apply -f $(K8S_DEPLOYMENT_DIR)/configmap.yaml
	kubectl apply -f $(K8S_DEPLOYMENT_DIR)/deployment.yaml
	kubectl apply -f $(K8S_DEPLOYMENT_DIR)/service.yaml
	kubectl apply -f $(K8S_DEPLOYMENT_DIR)/hpa.yaml

# K8s 전체 삭제
k8s-delete:
	kubectl delete -f $(K8S_DEPLOYMENT_DIR)/hpa.yaml --ignore-not-found=true
	kubectl delete -f $(K8S_DEPLOYMENT_DIR)/service.yaml --ignore-not-found=true
	kubectl delete -f $(K8S_DEPLOYMENT_DIR)/deployment.yaml --ignore-not-found=true
	kubectl delete -f $(K8S_DEPLOYMENT_DIR)/configmap.yaml --ignore-not-found=true
	kubectl delete -f $(K8S_DEPLOYMENT_DIR)/rbac.yaml --ignore-not-found=true
	kubectl delete -f $(K8S_DEPLOYMENT_DIR)/namespace.yaml --ignore-not-found=true

# K8s 상태 확인
k8s-status:
	@echo "=== Namespace ==="
	kubectl get namespace $(K8S_NAMESPACE)
	@echo "\n=== Deployments ==="
	kubectl get deployments -n $(K8S_NAMESPACE)
	@echo "\n=== Pods ==="
	kubectl get pods -n $(K8S_NAMESPACE)
	@echo "\n=== Services ==="
	kubectl get services -n $(K8S_NAMESPACE)
	@echo "\n=== HPA ==="
	kubectl get hpa -n $(K8S_NAMESPACE)

# K8s 로그 확인
k8s-logs:
	kubectl logs -n $(K8S_NAMESPACE) -l app=kcloud-cost-estimator --tail=100 -f

# K8s 포트 포워딩
k8s-port-forward:
	kubectl port-forward -n $(K8S_NAMESPACE) svc/kcloud-cost-estimator 8001:8001

# K8s 재시작
k8s-restart:
	kubectl rollout restart deployment/kcloud-cost-estimator -n $(K8S_NAMESPACE)
	kubectl rollout status deployment/kcloud-cost-estimator -n $(K8S_NAMESPACE)

# Docker 이미지 빌드 후 K8s에 배포
k8s-build-deploy: docker-build k8s-restart