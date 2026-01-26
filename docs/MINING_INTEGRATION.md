# Provider + Worldland Mining 통합 설계 문서

## 1. 개요

### 1.1 목표

Provider(데이터센터)가 GPU를 사용자에게 렌탈하면서, 동시에 Worldland 블록체인 채굴 노드로 동작하도록 한다.

### 1.2 핵심 개념

- **Provider** = 데이터센터 = Worldland 블록체인 노드
- **Worker Node** = GPU가 장착된 EC2 인스턴스
- **Mining Pod** = Worldland 채굴 컨테이너 (각 Provider당 1개)
- **Rental Pod** = 사용자에게 제공되는 GPU 컨테이너

### 1.3 리소스 분배 모델

```
Worker Node GPU 총량: 8개
├── Mining Pod (Worldland 채굴): 2개 (동적 조절 가능)
├── Rental Pods (사용자 렌탈): 5개 (현재 사용 중)
└── Available (대기 중): 1개
```

---

## 2. 아키텍처

### 2.1 시스템 구성도

```
┌─────────────────────────────────────────────────────────────────┐
│                        k8s-proxy-server                         │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │                     Orchestrator                         │   │
│  │  ├── Provider 상태 관리                                  │   │
│  │  ├── Mining Pod 배포/관리                                │   │
│  │  ├── GPU 할당량 조절 (채굴 ↔ 렌탈)                       │   │
│  │  └── ResourceQuota 업데이트                              │   │
│  └─────────────────────────────────────────────────────────┘   │
│                              │                                   │
│                              ▼                                   │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │                    Kubernetes API                        │   │
│  └─────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
                               │
           ┌───────────────────┴───────────────────┐
           ▼                                       ▼
┌─────────────────────────┐           ┌─────────────────────────┐
│    Worker Node #1       │           │    Worker Node #2       │
│  ┌───────────────────┐  │           │  ┌───────────────────┐  │
│  │  Mining Pod       │  │           │  │  Mining Pod       │  │
│  │  (worldland-xxx)  │  │           │  │  (worldland-yyy)  │  │
│  │  GPU: 2           │  │           │  │  GPU: 1           │  │
│  └───────────────────┘  │           │  └───────────────────┘  │
│  ┌───────────────────┐  │           │  ┌───────────────────┐  │
│  │  Rental Pods      │  │           │  │  Rental Pods      │  │
│  │  gpu-user1-xxx    │  │           │  │  gpu-user3-zzz    │  │
│  │  gpu-user2-yyy    │  │           │  │                   │  │
│  └───────────────────┘  │           │  └───────────────────┘  │
│                         │           │                         │
│  Provider Agent         │           │  Provider Agent         │
│  (상태 보고)             │           │  (상태 보고)             │
└─────────────────────────┘           └─────────────────────────┘
```

### 2.2 네임스페이스 구조

```
Kubernetes Cluster
├── default                    # 시스템 Pod
├── worldland-mining           # 모든 Mining Pod (Provider별)
│   ├── mining-provider-xxx
│   └── mining-provider-yyy
├── tenant-user1               # 사용자별 namespace
│   └── gpu-user1-xxx
├── tenant-user2
│   └── gpu-user2-yyy
└── ...
```

---

## 3. 데이터 모델

### 3.1 ProviderCapacity 구조체

```go
type ProviderCapacity struct {
    // === 총 보유량 (불변) ===
    TotalGPUs        map[string]int `json:"total_gpus"`         // {"Tesla T4": 8}
    TotalCPUCores    int            `json:"total_cpu_cores"`    // 32
    TotalMemoryMB    int64          `json:"total_memory_mb"`    // 128000
    TotalDiskGB      int64          `json:"total_disk_gb"`      // 500

    // === 채굴용 예약 (Worldland Mining) ===
    MiningGPUs       map[string]int `json:"mining_gpus"`        // {"Tesla T4": 2}
    MiningCPUCores   int            `json:"mining_cpu_cores"`   // 4
    MiningMemoryMB   int64          `json:"mining_memory_mb"`   // 8000
    MiningPodName    string         `json:"mining_pod_name"`    // "mining-provider-xxx"
    MiningStatus     string         `json:"mining_status"`      // "running" | "stopped" | "pending"

    // === 렌탈 중 (사용자에게 할당됨) ===
    InUseGPUs        map[string]int `json:"in_use_gpus"`        // {"Tesla T4": 5}
    InUseCPUCores    int            `json:"in_use_cpu_cores"`   // 20
    InUseMemoryMB    int64          `json:"in_use_memory_mb"`   // 80000

    // === 렌탈 가능 (대기 중) ===
    AvailableGPUs      map[string]int `json:"available_gpus"`      // {"Tesla T4": 1}
    AvailableCPUCores  int            `json:"available_cpu_cores"` // 8
    AvailableMemoryMB  int64          `json:"available_memory_mb"` // 40000

    // === 가격 정책 ===
    GPUPricesPerHour    map[string]float64 `json:"gpu_prices_per_hour"`
    CPUPricePerHour     float64            `json:"cpu_price_per_hour"`
    MemoryPricePerGBHr  float64            `json:"memory_price_per_gb_hr"`
}
```

### 3.2 리소스 관계식

```
Total = Mining + InUse + Available
Available = Total - Mining - InUse
```

### 3.3 MiningConfig 구조체

```go
type MiningConfig struct {
    // 채굴 Pod 설정
    Image           string            `json:"image"`            // "worldland/miner:latest"
    GPUCount        int               `json:"gpu_count"`        // 2
    CPUCores        int               `json:"cpu_cores"`        // 4
    MemoryMB        int64             `json:"memory_mb"`        // 8000

    // 블록체인 설정
    WalletAddress   string            `json:"wallet_address"`   // 채굴 보상 지갑
    PoolURL         string            `json:"pool_url"`         // 마이닝 풀 주소
    ExtraArgs       []string          `json:"extra_args"`       // 추가 인자

    // 환경 변수
    EnvVars         map[string]string `json:"env_vars"`
}
```

---

## 4. API 설계

### 4.1 Provider 등록 API (확장)

**POST /api/v1/providers/register**

```json
// Request
{
  "wallet_address": "0x1234...",
  "capacity": {
    "total_gpus": {"Tesla T4": 8},
    "total_cpu_cores": 32,
    "total_memory_mb": 128000
  },
  "mining_config": {
    "image": "worldland/miner:v1.0",
    "gpu_count": 1,
    "cpu_cores": 2,
    "memory_mb": 4000,
    "wallet_address": "0x1234...",
    "pool_url": "stratum+tcp://pool.worldland.io:3333"
  }
}

// Response
{
  "provider_id": "provider-abc123",
  "status": "approved",
  "mining_pod_name": "mining-provider-abc123",
  "capacity": {
    "total_gpus": {"Tesla T4": 8},
    "mining_gpus": {"Tesla T4": 1},
    "available_gpus": {"Tesla T4": 7}
  }
}
```

### 4.2 채굴 GPU 할당 API

**POST /api/v1/providers/:id/mining/allocate**

채굴용 GPU를 증가시킨다. (렌탈 가용량 감소)

```json
// Request
{
  "gpu_count": 2,            // 추가로 필요한 GPU 개수
  "reason": "high_difficulty" // 선택적: 할당 이유
}

// Response - 성공
{
  "success": true,
  "mining_gpus": {"Tesla T4": 3},      // 기존 1 + 추가 2
  "available_gpus": {"Tesla T4": 5},   // 기존 7 - 추가 2
  "message": "Mining GPU allocation successful"
}

// Response - 실패 (가용 GPU 부족)
{
  "success": false,
  "error": "insufficient_resources",
  "message": "Not enough available GPUs. Available: 1, Requested: 2",
  "suggestion": "Reduce rental jobs first or wait for job completion"
}
```

### 4.3 채굴 GPU 반환 API

**POST /api/v1/providers/:id/mining/release**

채굴용 GPU를 감소시킨다. (렌탈 가용량 증가)

```json
// Request
{
  "gpu_count": 2  // 반환할 GPU 개수
}

// Response
{
  "success": true,
  "mining_gpus": {"Tesla T4": 1},
  "available_gpus": {"Tesla T4": 7},
  "message": "Mining GPU released successfully"
}
```

### 4.4 채굴 상태 조회 API

**GET /api/v1/providers/:id/mining**

```json
// Response
{
  "provider_id": "provider-abc123",
  "mining_status": "running",
  "mining_pod_name": "mining-provider-abc123",
  "resources": {
    "gpu_count": 2,
    "cpu_cores": 4,
    "memory_mb": 8000
  },
  "metrics": {
    "hashrate": "125.5 MH/s",
    "gpu_utilization": [95, 92],
    "temperature": [65, 68],
    "power_usage": [180, 175]
  },
  "wallet_address": "0x1234...",
  "uptime_seconds": 86400
}
```

### 4.5 채굴 설정 업데이트 API

**PUT /api/v1/providers/:id/mining**

```json
// Request
{
  "image": "worldland/miner:v1.1",  // 이미지 업데이트
  "pool_url": "stratum+tcp://pool2.worldland.io:3333",
  "extra_args": ["--intensity", "high"]
}

// Response
{
  "success": true,
  "message": "Mining pod will be restarted with new configuration"
}
```

### 4.6 채굴 중지/시작 API

**POST /api/v1/providers/:id/mining/stop**
**POST /api/v1/providers/:id/mining/start**

```json
// Response
{
  "success": true,
  "mining_status": "stopped", // or "running"
  "released_gpus": { "Tesla T4": 2 }, // stop 시 반환
  "message": "Mining pod stopped, resources released to rental pool"
}
```

---

## 5. 동작 시나리오

### 5.1 Provider 등록 + Mining 시작

```
┌──────────────────┐                    ┌────────────────┐
│  Provider Agent  │                    │  k8s-proxy     │
└────────┬─────────┘                    └────────┬───────┘
         │                                       │
         │ 1. POST /providers/register           │
         │   (capacity + mining_config)          │
         │──────────────────────────────────────>│
         │                                       │
         │                    2. Create Provider │
         │                    3. Deploy Mining Pod
         │                       (in worldland-mining ns)
         │                    4. Update Capacity │
         │                       - mining_gpus = 1
         │                       - available = 7 │
         │                                       │
         │ 5. Response: provider_id + status     │
         │<──────────────────────────────────────│
         │                                       │
```

### 5.2 채굴 강도 증가 요청

```
상황: 블록체인 난이도 상승 → 더 많은 GPU 필요

┌──────────────────┐                    ┌────────────────┐
│ Mining Container │                    │  k8s-proxy     │
│ (Worldland Node) │                    │                │
└────────┬─────────┘                    └────────┬───────┘
         │                                       │
         │ 1. POST /providers/xxx/mining/allocate│
         │   { "gpu_count": 2 }                  │
         │──────────────────────────────────────>│
         │                                       │
         │         2. Check available_gpus >= 2  │
         │         3. Update Mining Pod          │
         │            (GPU limit: 1 → 3)         │
         │         4. Update Capacity            │
         │            mining_gpus: 1 → 3         │
         │            available: 7 → 5           │
         │                                       │
         │ 5. Response: success                  │
         │<──────────────────────────────────────│
         │                                       │
         │ 6. 추가 GPU 사용하여 채굴 강도 증가  │
         │                                       │
```

### 5.3 렌탈 요청 시 가용량 확인

```
상황: 사용자가 GPU 6개 렌탈 요청

┌──────────────────┐                    ┌────────────────┐
│      User        │                    │  k8s-proxy     │
└────────┬─────────┘                    └────────┬───────┘
         │                                       │
         │ 1. POST /jobs                         │
         │   { gpu_count: 6, gpu_type: "T4" }   │
         │──────────────────────────────────────>│
         │                                       │
         │         2. Provider 검색              │
         │            - Provider A: available=5 (부족)
         │            - Provider B: available=7 (OK)
         │         3. Provider B 선택            │
         │         4. Job 생성 (tenant-user ns)  │
         │         5. Update Provider B Capacity │
         │            in_use: 0 → 6              │
         │            available: 7 → 1           │
         │                                       │
         │ 5. Response: job created              │
         │<──────────────────────────────────────│
```

### 5.4 긴급 채굴 GPU 필요 (가용량 부족 시)

```
상황: 채굴에 급히 GPU 필요하지만, 렌탈 중인 Job들이 GPU 점유

옵션 1: 거부 + 대기 안내
  - "가용 GPU 부족. Job 종료 대기 필요."

옵션 2: Preemption (강제 회수) - 정책 기반
  - 특정 조건(가격, 시간 등) 충족 시 Job 종료
  - 사용자에게 알림 + 환불

옵션 3: 다른 Provider에서 Job 마이그레이션
  - 복잡도 높음, 추후 구현

초기 구현: 옵션 1 (거부 + 대기)
```

---

## 6. Mining Pod 스펙

### 6.1 Kubernetes Pod 템플릿

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: mining-provider-abc123
  namespace: worldland-mining
  labels:
    app: worldland-mining
    provider-id: provider-abc123
    worldland.io/mining: "true"
spec:
  nodeSelector:
    kubernetes.io/hostname: ip-172-31-0-110 # Provider 워커 노드

  containers:
    - name: miner
      image: worldland/miner:v1.0
      resources:
        limits:
          nvidia.com/gpu: 2
          cpu: "4"
          memory: 8Gi
        requests:
          nvidia.com/gpu: 2
          cpu: "2"
          memory: 4Gi
      env:
        - name: WALLET_ADDRESS
          value: "0x1234..."
        - name: POOL_URL
          value: "stratum+tcp://pool.worldland.io:3333"
        - name: PROVIDER_ID
          value: "provider-abc123"
        - name: K8S_PROXY_URL
          value: "http://k8s-proxy-svc:8080"

  tolerations:
    - key: "nvidia.com/gpu"
      operator: "Exists"
      effect: "NoSchedule"
```

### 6.2 Mining Container 내부 로직

```python
# /app/miner_agent.py (Mining Container 내부)

import requests
import os

K8S_PROXY_URL = os.getenv("K8S_PROXY_URL")
PROVIDER_ID = os.getenv("PROVIDER_ID")

def request_more_gpu(count: int, reason: str = ""):
    """채굴 강도를 높이기 위해 GPU 추가 요청"""
    response = requests.post(
        f"{K8S_PROXY_URL}/api/v1/providers/{PROVIDER_ID}/mining/allocate",
        json={"gpu_count": count, "reason": reason}
    )
    return response.json()

def release_gpu(count: int):
    """채굴 강도를 줄이고 GPU 반환"""
    response = requests.post(
        f"{K8S_PROXY_URL}/api/v1/providers/{PROVIDER_ID}/mining/release",
        json={"gpu_count": count}
    )
    return response.json()

# 사용 예시
if difficulty_increased:
    result = request_more_gpu(2, reason="difficulty_spike")
    if result["success"]:
        start_mining_with_more_gpus()
    else:
        log.warning("GPU 추가 할당 실패, 현재 리소스로 계속 채굴")
```

---

## 7. 구현 순서

### Phase 1: 데이터 모델 확장 ✅

1. [x] `ProviderCapacity`에 Mining 관련 필드 추가
2. [x] `MiningConfig` 구조체 정의
3. [x] Provider 등록 API에 mining_config 지원

### Phase 2: Mining Pod 관리 ✅

4. [x] `MiningPodManager` 생성 (배포, 업데이트, 삭제)
5. [x] Mining Pod 템플릿 생성
6. [x] worldland-mining namespace 자동 생성

### Phase 3: Mining API 구현 ✅

7. [x] `POST /providers/:id/mining/allocate` - GPU 추가 할당
8. [x] `POST /providers/:id/mining/release` - GPU 반환
9. [x] `GET /providers/:id/mining` - 상태 조회
10. [x] `POST /providers/:id/mining/stop|start` - 채굴 중지/시작

### Phase 4: Provider Agent 연동 ✅

11. [x] Provider Agent 등록 시 Mining Pod 자동 배포
12. [x] Mining Container → k8s-proxy API 호출 예제 (examples/mining_client.py)

### Phase 5: 모니터링 & 안정화 ✅

13. [x] Mining Pod 상태 모니터링 (miningMonitor 워커)
14. [x] GPU 할당 충돌 처리 (Mutex 사용)
15. [x] 장애 복구 로직 (RecoverMiningStates)

---

## 8. 고려사항

### 8.1 동시성 제어

- 같은 Provider에 대해 동시에 allocate/release 요청 시 → Mutex 사용
- Database 트랜잭션 또는 Optimistic Locking 적용

### 8.2 장애 복구

- Mining Pod 비정상 종료 시 → GPU 자동 반환 (Capacity 업데이트)
- k8s-proxy 재시작 시 → 기존 Mining Pod 상태 복원

### 8.3 가격 정책

- 렌탈 GPU 가격: $0.50/hr (Tesla T4)
- Mining GPU: 무료 (자체 사용)
- Mining 수익과 Rental 수익 비교 → 최적 할당 자동 조절 (추후)

### 8.4 보안

- Mining API는 해당 Provider만 호출 가능 (인증 필요)
- Wallet Address 변경 시 추가 검증

---

## 9. 테스트 시나리오

### 9.1 정상 플로우

```bash
# 1. Provider 등록 + Mining 시작
curl -X POST /api/v1/providers/register -d '{...}'

# 2. Mining 상태 확인
curl /api/v1/providers/provider-xxx/mining

# 3. GPU 추가 할당
curl -X POST /api/v1/providers/provider-xxx/mining/allocate \
  -d '{"gpu_count": 2}'

# 4. GPU 반환
curl -X POST /api/v1/providers/provider-xxx/mining/release \
  -d '{"gpu_count": 1}'
```

### 9.2 예외 상황

- 가용 GPU 부족 상태에서 allocate 요청
- Mining Pod 실행 중 Provider offline
- 동시에 여러 allocate 요청

---

## 10. 변경 이력

| 버전 | 날짜       | 변경 내용                               |
| ---- | ---------- | --------------------------------------- |
| 1.0  | 2026-01-12 | 초안 작성                               |
| 1.1  | 2026-01-12 | Phase 1-5 구현 완료, 모니터링 워커 추가 |
