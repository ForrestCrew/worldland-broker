# 실제 배포 환경 테스트 가이드

> **마지막 검증**: 2026-01-15 (GCP asia-northeast3)

## 1. 개요

이 문서는 k8s-proxy-server를 실제 Kubernetes 클러스터 환경에서 테스트하기 위한 체크리스트와 가이드입니다.

---

## 2. 사전 요구사항

### 2.1 인프라

| 구성 요소      | 요구사항                   | 상태 (GCP)   |
| -------------- | -------------------------- | ------------ |
| Master Node    | Ubuntu 22.04+, K8s 1.28+   | ✅ 검증 완료 |
| Worker Node(s) | GPU 장착 (NVIDIA Tesla T4) | ✅ 검증 완료 |
| Redis          | 7.x (Docker on Master)     | ✅ 검증 완료 |
| PostgreSQL     | 15 (Docker on Master)      | ✅ 검증 완료 |

### 2.2 네트워크

| 항목              | 포트        | 설명                       | GCP 방화벽 |
| ----------------- | ----------- | -------------------------- | ---------- |
| K8s API Server    | 6443        | Master ↔ Worker 통신       | ✅         |
| Redis             | 6379        | Provider Agent ↔ k8s-proxy | ✅         |
| k8s-proxy HTTP    | 8080        | API 서비스                 | ✅         |
| SSH (Rental Pods) | 30000-32767 | NodePort 범위              | ✅         |

---

## 3. 필수 구현/설정 체크리스트

### 3.1 Mining Docker 이미지 🔴

```bash
# 현재: worldland/miner:v1.0 이미지가 없음
# 옵션 1: 실제 Worldland 채굴 이미지 빌드
# 옵션 2: 테스트용 더미 이미지 사용

# 테스트용 더미 이미지 예시
cat > Dockerfile.miner-test << 'EOF'
FROM nvidia/cuda:12.0.0-base-ubuntu22.04
RUN apt-get update && apt-get install -y curl python3
COPY mining_client.py /app/
ENV PROVIDER_ID=""
ENV K8S_PROXY_URL="http://k8s-proxy-svc.default:8080"
ENV WALLET_ADDRESS=""
ENV POOL_URL=""
CMD ["sh", "-c", "echo 'Mining simulation started' && sleep infinity"]
EOF

docker build -f Dockerfile.miner-test -t worldland/miner:v1.0 .
```

### 3.2 RBAC 권한 설정 🔴

k8s-proxy-server가 Mining Pod를 관리하려면 추가 권한이 필요합니다.

```yaml
# deploy/k8s/rbac.yaml에 추가
rules:
  # 기존 권한...

  # Mining namespace 관리
  - apiGroups: [""]
    resources: ["namespaces"]
    verbs: ["get", "list", "create", "delete"]

  # Mining Pod 관리 (worldland-mining namespace)
  - apiGroups: [""]
    resources: ["pods", "services"]
    verbs: ["get", "list", "watch", "create", "update", "delete"]
    resourceNames: []
```

### 3.3 ConfigMap 설정 🔴

```yaml
# deploy/k8s/configmap.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: k8s-proxy-config
data:
  PORT: "8080"
  DEBUG_MODE: "true"

  # Redis 연결
  REDIS_HOST: "redis-svc.default"
  REDIS_PORT: "6379"

  # Master Node 정보 (Provider Agent용)
  MASTER_IP: "172.31.0.100" # 실제 Master IP로 변경
  MASTER_PORT: "6443"

  # PostgreSQL (선택)
  # DB_HOST: "your-postgres-host"
  # DB_PORT: "5432"
  # DB_NAME: "worldland"
```

### 3.4 Secret 설정 🔴

```yaml
# deploy/k8s/secret.yaml
apiVersion: v1
kind: Secret
metadata:
  name: k8s-proxy-secret
type: Opaque
stringData:
  GOOGLE_CLIENT_ID: "your-google-client-id"
  GOOGLE_CLIENT_SECRET: "your-google-client-secret"
  JWT_SECRET: "your-jwt-secret-key"
  # DB_PASSWORD: "your-db-password"
```

### 3.5 NVIDIA Device Plugin 확인 🔴

```bash
# Worker Node에서 GPU 확인
kubectl get nodes -o json | jq '.items[].status.allocatable["nvidia.com/gpu"]'

# Device Plugin 설치 확인
kubectl get pods -n kube-system | grep nvidia

# 설치 안되어 있으면 설치
kubectl apply -f https://raw.githubusercontent.com/NVIDIA/k8s-device-plugin/main/nvidia-device-plugin.yml
```

---

## 4. 배포 순서

### Step 1: Docker 이미지 빌드 & 푸시

```bash
cd k8s-proxy-server

# k8s-proxy-server 이미지 빌드
docker build -t your-registry/k8s-proxy-server:latest .
docker push your-registry/k8s-proxy-server:latest

# Provider Agent 빌드
docker build -f Dockerfile.provider-agent -t your-registry/provider-agent:latest .
docker push your-registry/provider-agent:latest
```

### Step 2: Kubernetes 리소스 배포

```bash
# 1. Namespace 생성
kubectl apply -f deploy/k8s/namespace.yaml

# 2. RBAC 설정
kubectl apply -f deploy/k8s/rbac.yaml

# 3. ConfigMap & Secret
kubectl apply -f deploy/k8s/configmap.yaml
kubectl apply -f deploy/k8s/secret.yaml

# 4. Deployment & Service
kubectl apply -f deploy/k8s/deployment.yaml
kubectl apply -f deploy/k8s/service.yaml

# 5. 상태 확인
kubectl get pods -n default | grep k8s-proxy
kubectl logs -f deployment/k8s-proxy-server
```

### Step 3: Redis 배포 (필요시)

```bash
# 간단한 Redis 배포
kubectl apply -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: redis
spec:
  replicas: 1
  selector:
    matchLabels:
      app: redis
  template:
    metadata:
      labels:
        app: redis
    spec:
      containers:
      - name: redis
        image: redis:7-alpine
        ports:
        - containerPort: 6379
---
apiVersion: v1
kind: Service
metadata:
  name: redis-svc
spec:
  selector:
    app: redis
  ports:
  - port: 6379
    targetPort: 6379
EOF
```

### Step 4: Provider Agent 실행 (Worker Node에서)

```bash
# EC2 Worker Node에 SSH 접속 후
./provider-agent \
  --redis redis-svc.default:6379 \
  --wallet 0xYourWalletAddress \
  --enable-mining \
  --mining-gpu 1 \
  --mining-pool stratum+tcp://pool.worldland.io:3333
```

---

## 5. 테스트 시나리오

### 5.1 기본 기능 테스트

```bash
# k8s-proxy 서비스 URL
PROXY_URL="http://<MASTER_IP>:30080"

# 1. Health Check
curl $PROXY_URL/health

# 2. Provider 목록 확인
curl $PROXY_URL/api/v1/providers

# 3. Mining Metrics 확인
curl $PROXY_URL/api/v1/mining/metrics
```

### 5.2 Tenant Isolation 테스트

```bash
# 1. Alice로 Job 생성
curl -X POST $PROXY_URL/api/v1/jobs \
  -H "Content-Type: application/json" \
  -H "X-User-ID: alice" \
  -d '{
    "gpu_type": "Tesla T4",
    "gpu_count": 1,
    "ssh_password": "alice123"
  }'

# 2. Bob으로 Job 목록 조회 (Alice의 Job이 보이면 안됨)
curl $PROXY_URL/api/v1/jobs -H "X-User-ID: bob"

# 3. Namespace 확인
kubectl get ns | grep tenant
kubectl get pods -n tenant-alice
```

### 5.3 Mining 테스트

```bash
# 1. Provider 등록 확인 (자동 Mining Pod 배포)
kubectl get pods -n worldland-mining

# 2. Mining 상태 확인
curl $PROXY_URL/api/v1/providers/<PROVIDER_ID>/mining

# 3. GPU 추가 할당
curl -X POST $PROXY_URL/api/v1/providers/<PROVIDER_ID>/mining/allocate \
  -H "Content-Type: application/json" \
  -d '{"gpu_count": 1}'

# 4. GPU 반환
curl -X POST $PROXY_URL/api/v1/providers/<PROVIDER_ID>/mining/release \
  -H "Content-Type: application/json" \
  -d '{"gpu_count": 1}'
```

### 5.4 리소스 할당 테스트

```bash
# 1. 대량 GPU 요청 (가용량 초과 테스트)
curl -X POST $PROXY_URL/api/v1/jobs \
  -H "X-User-ID: test" \
  -d '{"gpu_count": 100}'  # 실패해야 함

# 2. Provider 리소스 확인
curl $PROXY_URL/api/v1/providers/<PROVIDER_ID>
```

### 5.5 OOMKilled 상태 확인 테스트

```bash
# OOMKilled된 Job 상태 확인
curl $PROXY_URL/api/v1/jobs/<JOB_ID>

# 예상 응답 (OOMKilled 발생 시):
# {
#   "job_id": "gpu-job-xxx",
#   "status": "Failed",
#   "failure_reason": "OOMKilled",
#   "failure_message": "Container was killed due to memory limit exceeded (Exit Code: 137)",
#   "suggestion": {
#     "action": "increase_memory",
#     "recommended_memory": "32Gi",
#     "message": "메모리가 부족하여 컨테이너가 종료되었습니다. 32Gi 이상의 메모리로 새 Job을 생성해주세요."
#   }
# }
```

---

## 6. 트러블슈팅

### 6.1 Mining Pod가 Pending 상태

```bash
# 원인 확인
kubectl describe pod -n worldland-mining <POD_NAME>

# 일반적인 원인:
# - GPU 리소스 부족
# - NodeSelector가 맞지 않음
# - Image Pull 실패
```

### 6.2 Provider Agent 등록 실패

```bash
# Redis 연결 확인
redis-cli -h <REDIS_HOST> ping

# k8s-proxy 로그 확인
kubectl logs -f deployment/k8s-proxy-server | grep -i provider
```

### 6.3 Job Pod가 생성되지 않음

```bash
# RBAC 권한 확인
kubectl auth can-i create pods --as=system:serviceaccount:default:k8s-proxy-server

# Tenant namespace 확인
kubectl get ns

# k8s-proxy 로그 확인
kubectl logs -f deployment/k8s-proxy-server | grep -i job
```

### 6.4 Job이 OOMKilled로 종료됨

```bash
# Pod 이벤트 확인
kubectl describe pod <POD_NAME> -n tenant-<USER_ID>

# 해결 방법:
# 1. 더 많은 메모리로 새 Job 생성
# 2. API 응답의 suggestion.recommended_memory 참고
```

---

## 7. 모니터링

### 7.1 주요 로그 확인

```bash
# k8s-proxy-server 로그
kubectl logs -f deployment/k8s-proxy-server

# Mining Pod 로그
kubectl logs -f -n worldland-mining <POD_NAME>

# Provider Agent 로그 (Worker Node에서)
journalctl -u provider-agent -f
```

### 7.2 메트릭스 확인

```bash
# Mining 메트릭스
curl $PROXY_URL/api/v1/mining/metrics

# 응답 예시:
# {
#   "total_mining_providers": 2,
#   "running_mining_pods": 2,
#   "total_mining_gpus": 4,
#   "total_available_gpus": 12,
#   "total_providers": 3
# }
```

---

## 8. 체크리스트 요약

### ✅ 완료됨 (GCP 검증 2026-01-15)

- [x] Mining Docker 이미지 (mingeyom/worldland-mio:latest)
- [x] RBAC 권한 설정
- [x] Redis 연결 (Docker on Master)
- [x] PostgreSQL 연결 (Docker on Master)
- [x] NVIDIA Device Plugin 설치
- [x] k8s-proxy-server 로컬 실행
- [x] GPU Job 생성 및 SSH 접속
- [x] Mining Pod 배포

### 🟡 권장 (추후)

- [ ] k8s-proxy-server K8s 배포 (Master에서 실행)
- [ ] 모니터링 설정 (Prometheus/Grafana)
- [ ] 로그 수집 (ELK/Loki)

### 🟢 선택

- [ ] 프로덕션 Google OAuth 설정
- [ ] TLS/HTTPS 설정 (Ingress + cert-manager)
- [ ] Bootnodes 설정 (Mining 피어 연결)

---

## 9. 변경 이력

| 버전 | 날짜       | 변경 내용                                    |
| ---- | ---------- | -------------------------------------------- |
| 1.0  | 2026-01-12 | 초안 작성                                    |
| 1.1  | 2026-01-12 | OOMKilled 테스트 시나리오 및 트러블슈팅 추가 |
| 2.0  | 2026-01-15 | GCP 배포 완료, 체크리스트 업데이트           |

---

> 📘 **참고**: 상세한 GCP 배포 가이드는 [GCP_DEPLOYMENT_GUIDE.md](./GCP_DEPLOYMENT_GUIDE.md)를 참조하세요.
