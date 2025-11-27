.PHONY: help install test lint format clean build run docker-build docker-run k8s-dev k8s-prod k8s-delete

# Variables
IMAGE_NAME := kcloud-infra
IMAGE_TAG := latest
REGISTRY := ghcr.io/yourusername
PYTHON := python3
PIP := pip3

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

install: ## Install Python dependencies
	$(PIP) install --upgrade pip
	$(PIP) install -r requirements.txt

install-dev: install ## Install development dependencies
	$(PIP) install pytest pytest-cov black pylint flake8

test: ## Run tests
	$(PYTHON) -m pytest tests/ -v

test-cov: ## Run tests with coverage
	$(PYTHON) -m pytest tests/ -v --cov=src --cov-report=html --cov-report=term

lint: ## Run linting
	$(PYTHON) -m pylint src/ --disable=C,R
	$(PYTHON) -m flake8 src/ --max-line-length=120

format: ## Format code with black
	$(PYTHON) -m black src/ tests/

format-check: ## Check code formatting
	$(PYTHON) -m black --check src/ tests/

clean: ## Clean temporary files
	find . -type d -name "__pycache__" -exec rm -rf {} + 2>/dev/null || true
	find . -type f -name "*.pyc" -delete
	find . -type f -name "*.pyo" -delete
	find . -type d -name "*.egg-info" -exec rm -rf {} + 2>/dev/null || true
	rm -rf .pytest_cache .coverage htmlcov/

build: clean ## Build the application
	$(PYTHON) -m compileall src/

run: ## Run the application locally
	$(PYTHON) -m uvicorn src.main:app --host 0.0.0.0 --port 8006 --reload

docker-build: ## Build Docker image
	docker build -t $(IMAGE_NAME):$(IMAGE_TAG) .

docker-run: ## Run Docker container
	docker run -d \
		--name $(IMAGE_NAME) \
		--env-file .env \
		-p 8006:8006 \
		$(IMAGE_NAME):$(IMAGE_TAG)

docker-push: ## Push Docker image to registry
	docker tag $(IMAGE_NAME):$(IMAGE_TAG) $(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)
	docker push $(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)

docker-stop: ## Stop Docker container
	docker stop $(IMAGE_NAME) || true
	docker rm $(IMAGE_NAME) || true

k8s-validate: ## Validate Kubernetes manifests
	kubectl kustomize k8s/base
	kubectl kustomize k8s/overlays/development
	kubectl kustomize k8s/overlays/production

k8s-dev: ## Deploy to development environment
	kubectl apply -k k8s/overlays/development

k8s-dev-dry: ## Dry run deployment to development
	kubectl apply -k k8s/overlays/development --dry-run=client

k8s-prod: ## Deploy to production environment
	kubectl apply -k k8s/overlays/production

k8s-prod-dry: ## Dry run deployment to production
	kubectl apply -k k8s/overlays/production --dry-run=client

k8s-delete-dev: ## Delete development deployment
	kubectl delete -k k8s/overlays/development

k8s-delete-prod: ## Delete production deployment
	kubectl delete -k k8s/overlays/production

k8s-logs-dev: ## Show logs from development pods
	kubectl logs -n kcloud-dev -l app=kcloud-infra -f

k8s-logs-prod: ## Show logs from production pods
	kubectl logs -n kcloud-prod -l app=kcloud-infra -f

k8s-status-dev: ## Check development deployment status
	kubectl get all -n kcloud-dev

k8s-status-prod: ## Check production deployment status
	kubectl get all -n kcloud-prod

setup-env: ## Create .env file from example
	cp .env.example .env
	@echo "Please edit .env with your configuration"

all: install lint test build ## Run all checks and build
