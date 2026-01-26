# ==============================================================================
# 변수 설정
# ==============================================================================
APP_NAME := k8s-proxy-server
MAIN_FILE := cmd/server/main.go
IMAGE_NAME := k8s-proxy-server
IMAGE_TAG := latest

# ==============================================================================
# 로컬 개발 명령어
# ==============================================================================
.PHONY: run run-dev build clean

# 1. 로컬에서 바로 실행 (Go Run)
run:
	@echo "Running $(APP_NAME)..."
	go run $(MAIN_FILE)

# 2. 개발 모드 실행 (.env 로드 + Orchestrator 활성화)
run-dev:
	@echo "Running $(APP_NAME) in dev mode..."
	@if [ -f .env ]; then \
		export $$(grep -v '^#' .env | xargs); \
	fi; \
	KUBECONFIG=~/.kube/config \
	ENABLE_ORCHESTRATOR=true \
	DEBUG_MODE=true \
	go run $(MAIN_FILE)

# 3. 빌드하고 실행 파일 만들기
build:
	@echo "Building..."
	go build -o bin/$(APP_NAME) $(MAIN_FILE)

# 4. 정리
clean:
	rm -rf bin/

# ==============================================================================
# Provider Agent 명령어
# ==============================================================================
.PHONY: build-agent run-agent

# Provider Agent 빌드
build-agent:
	@echo "Building Provider Agent..."
	go build -o bin/provider-agent cmd/provider-agent/main.go

# Provider Agent 실행
run-agent:
	@echo "Running Provider Agent..."
	go run cmd/provider-agent/main.go --redis=$(REDIS_ADDR) --wallet=$(WALLET_ADDR)

# ==============================================================================
# Provider SDK 명령어
# ==============================================================================
.PHONY: build-sdk build-sdk-linux run-sdk

# Provider SDK 빌드 (현재 OS)
build-sdk:
	@echo "Building Provider SDK..."
	go build -o bin/worldland-provider-sdk cmd/provider-sdk/main.go

# Provider SDK 빌드 (Linux AMD64 - GCP/EC2용)
build-sdk-linux:
	@echo "Building Provider SDK for Linux AMD64..."
	GOOS=linux GOARCH=amd64 go build -o bin/worldland-provider-sdk-linux-amd64 cmd/provider-sdk/main.go
	@echo "Building Provider SDK for Linux ARM64..."
	GOOS=linux GOARCH=arm64 go build -o bin/worldland-provider-sdk-linux-arm64 cmd/provider-sdk/main.go

# Provider SDK 실행 (테스트용)
run-sdk:
	@echo "Running Provider SDK..."
	go run cmd/provider-sdk/main.go --wallet=$(WALLET_ADDR) --token=$(TOKEN) --master-url=$(MASTER_URL)

# ==============================================================================
# Docker 명령어
# ==============================================================================
.PHONY: docker-build docker-push docker-run

# Docker 이미지 빌드
docker-build:
	@echo "Building Docker image..."
	docker build -t $(IMAGE_NAME):$(IMAGE_TAG) -f deploy/docker/Dockerfile .

# Docker 이미지 푸시 (레지스트리 주소 필요)
docker-push:
	@echo "Pushing Docker image..."
	docker push $(IMAGE_NAME):$(IMAGE_TAG)

# Docker로 로컬 실행
docker-run:
	@echo "Running in Docker..."
	docker run --rm -p 8080:8080 --env-file .env $(IMAGE_NAME):$(IMAGE_TAG)

# ==============================================================================
# Kubernetes 명령어
# ==============================================================================
.PHONY: k8s-deploy k8s-delete k8s-logs

# K8s 배포
k8s-deploy:
	@echo "Deploying to Kubernetes..."
	kubectl apply -f deploy/k8s/

# K8s 삭제
k8s-delete:
	@echo "Deleting from Kubernetes..."
	kubectl delete -f deploy/k8s/

# K8s 로그 확인
k8s-logs:
	kubectl logs -l app=proxy-server -f
