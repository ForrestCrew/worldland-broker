# k8s-proxy-server 아키텍처 개요

## 1. 시스템 소개

**k8s-proxy-server**는 Worldland GPU 렌탈 플랫폼의 핵심 백엔드 서버입니다.

### 주요 기능

- **GPU Job 관리**: 사용자에게 GPU 컨테이너 제공 (SSH 접속 가능)
- **Provider 관리**: 데이터센터(Provider) 등록 및 리소스 오케스트레이션
- **Mining 통합**: Provider가 동시에 Worldland 블록체인 채굴 노드로 동작
- **Tenant 격리**: 사용자별 Kubernetes namespace로 멀티테넌시 지원
- **인증**: Google OAuth + JWT 기반 인증

---

## 2. 시스템 아키텍처

```
                                    ┌──────────────────────────┐
                                    │      Frontend (React)     │
                                    │   http://localhost:3000   │
                                    └────────────┬─────────────┘
                                                 │
                                                 ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                           k8s-proxy-server                                   │
│                         (API Gateway + Orchestrator)                         │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────┐ │
│  │  Auth API   │  │   Job API   │  │Provider API │  │     Mining API      │ │
│  └─────────────┘  └─────────────┘  └─────────────┘  └─────────────────────┘ │
│                                                                              │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                        Orchestrator                                  │    │
│  │  - Provider 상태 관리         - Resource Allocation                  │    │
│  │  - Mining Pod 관리            - Heartbeat 모니터링                   │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
└────────────┬────────────────────────────────────────────────────────────────┘
             │
     ┌───────┴────────┐
     ▼                ▼
┌─────────┐    ┌─────────────────────────────────────────────────────┐
│  Redis  │    │              Kubernetes Cluster                      │
│ Streams │    │  ┌───────────────────────────────────────────────┐  │
└─────────┘    │  │              Master Node                       │  │
               │  │   k8s-proxy-server (Deployment)                │  │
               │  └───────────────────────────────────────────────┘  │
               │                                                      │
               │  ┌───────────────────────────────────────────────┐  │
               │  │         Worker Node (GPU)                      │  │
               │  │  ┌─────────────┐  ┌─────────────────────────┐ │  │
               │  │  │ Mining Pod  │  │     User Rental Pods    │ │  │
               │  │  │(worldland)  │  │  (tenant-xxx namespace) │ │  │
               │  │  └─────────────┘  └─────────────────────────┘ │  │
               │  │                                                │  │
               │  │  Provider Agent (로컬 데몬)                    │  │
               │  └───────────────────────────────────────────────┘  │
               └──────────────────────────────────────────────────────┘
```

---

## 3. 핵심 컴포넌트

### 3.1 Job Manager (`internal/job/manager.go`)

- GPU 컨테이너 Pod 생성/삭제
- SSH 접속을 위한 NodePort Service 생성
- Tenant 격리 (사용자별 namespace)

### 3.2 Provider Orchestrator (`internal/provider/orchestrator.go`)

- Provider 등록/상태 관리
- 리소스 할당 (GPU, CPU, Memory)
- Mining Pod 자동 배포
- Heartbeat 모니터링

### 3.3 Mining Manager (`internal/provider/mining_manager.go`)

- Worldland 채굴 Pod 배포/관리
- GPU 동적 할당/반환
- Pod 상태 모니터링

### 3.4 Tenant Manager (`internal/k8s/tenant.go`)

- 사용자별 Kubernetes namespace 관리
- ResourceQuota 설정
- 권한 격리

---

## 4. API 엔드포인트

### 4.1 인증 API (`/api/v1/auth`)

| 엔드포인트      | 메서드 | 설명                |
| --------------- | ------ | ------------------- |
| `/auth/google`  | POST   | Google OAuth 로그인 |
| `/auth/refresh` | POST   | JWT 토큰 갱신       |
| `/auth/logout`  | POST   | 로그아웃            |

### 4.2 Job API (`/api/v1/jobs`)

| 엔드포인트  | 메서드 | 인증 | 설명              |
| ----------- | ------ | ---- | ----------------- |
| `/jobs`     | POST   | 필요 | GPU 컨테이너 생성 |
| `/jobs`     | GET    | 필요 | 내 Job 목록       |
| `/jobs/:id` | GET    | 필요 | Job 상태 조회     |
| `/jobs/:id` | DELETE | 필요 | Job 삭제          |

### 4.3 Provider API (`/api/v1/providers`)

| 엔드포인트          | 메서드 | 설명          |
| ------------------- | ------ | ------------- |
| `/providers`        | GET    | Provider 목록 |
| `/providers/search` | GET    | Provider 검색 |
| `/providers/:id`    | GET    | Provider 상세 |

### 4.4 Mining API (`/api/v1/providers/:id/mining`)

| 엔드포인트         | 메서드 | 설명               |
| ------------------ | ------ | ------------------ |
| `/mining`          | GET    | 채굴 상태 조회     |
| `/mining/allocate` | POST   | 채굴 GPU 할당      |
| `/mining/release`  | POST   | 채굴 GPU 반환      |
| `/mining/start`    | POST   | 채굴 시작          |
| `/mining/stop`     | POST   | 채굴 중지          |
| `/mining/metrics`  | GET    | 전체 채굴 메트릭스 |

---

## 5. 데이터 흐름

### 5.1 Provider 등록 흐름

```
Provider Agent → Redis Stream → Orchestrator
    ↓
    1. kubeadm join 토큰 발급
    2. Provider 상태 저장
    3. Mining Pod 자동 배포 (MiningConfig 있는 경우)
```

### 5.2 Job 생성 흐름

```
User Request → Auth Middleware → JobHandler → JobManager
    ↓
    1. Provider 검색/선택
    2. 리소스 할당 확인
    3. Tenant namespace 생성 (없으면)
    4. GPU Pod + SSH Service 생성
    5. 리소스 차감
```

### 5.3 Mining GPU 조절 흐름

```
Mining Container → Mining API → Orchestrator
    ↓
    1. 가용 GPU 확인
    2. OK면 할당, 부족하면 거부 (Option 1)
    3. Mining Pod GPU limit 업데이트
    4. Capacity 상태 갱신
```

---

## 6. 리소스 격리 및 관리

### 6.1 EC2 스타일 리소스 할당

사용자가 요청한 리소스를 정확하게 할당하고, 가용량을 사전에 검증합니다.

```
┌─────────────────────────────────────────────────────────────────┐
│                    Job 생성 요청 흐름                            │
├─────────────────────────────────────────────────────────────────┤
│  1. GPU 가용량 확인  → 부족 시 거부                              │
│  2. CPU 가용량 확인  → 부족 시 거부                              │
│  3. Memory 가용량 확인 → 부족 시 거부                            │
│  4. 모두 충족 시 → Pod 생성 (Guaranteed QoS)                    │
└─────────────────────────────────────────────────────────────────┘
```

### 6.2 Guaranteed QoS (Request = Limit)

```yaml
resources:
  requests:
    cpu: "4"
    memory: "16Gi" # 정확히 할당
    nvidia.com/gpu: 1
  limits:
    cpu: "4"
    memory: "16Gi" # 초과 불가
    nvidia.com/gpu: 1
```

**리소스별 격리 수준:**

| 리소스 | 격리 방식          | 초과 시 동작   |
| ------ | ------------------ | -------------- |
| GPU    | 완전 독점          | 불가능         |
| Memory | 하드 제한 (cgroup) | OOMKilled      |
| CPU    | 시분할 보장        | CPU Throttling |

### 6.3 OOM 처리

메모리 초과로 컨테이너가 종료된 경우, 사용자에게 상세 정보와 권장 사항을 제공합니다.

```json
// GET /api/v1/jobs/job-xxx (OOMKilled 발생 시)
{
  "status": "Failed",
  "failure_reason": "OOMKilled",
  "failure_message": "Container was killed due to memory limit exceeded (Exit Code: 137)",
  "suggestion": {
    "action": "increase_memory",
    "recommended_memory": "32Gi",
    "message": "메모리가 부족하여 컨테이너가 종료되었습니다. 32Gi 이상의 메모리로 새 Job을 생성해주세요."
  }
}
```

---

## 7. 디렉토리 구조

```
k8s-proxy-server/
├── cmd/
│   ├── server/              # 메인 서버 엔트리포인트
│   └── provider-agent/      # Provider Agent 엔트리포인트
├── internal/
│   ├── auth/                # Google OAuth, JWT
│   ├── config/              # 환경변수 설정
│   ├── handler/             # API 핸들러
│   │   ├── auth_handler.go
│   │   ├── job_handler.go
│   │   ├── provider_handler.go
│   │   └── mining_handler.go
│   ├── job/                 # Job 관리
│   ├── k8s/                 # K8s 클라이언트, Tenant 관리
│   ├── messaging/           # Redis Streams
│   ├── middleware/          # 인증, CORS, 로깅
│   ├── provider/            # Provider 오케스트레이션
│   │   ├── orchestrator.go
│   │   ├── mining_manager.go
│   │   └── types.go
│   └── server/              # Gin 서버 설정
├── deploy/
│   └── k8s/                 # K8s 매니페스트
├── docs/                    # 문서
│   ├── ARCHITECTURE_OVERVIEW.md
│   ├── API_GATEWAY_ARCHITECTURE.md
│   ├── MINING_INTEGRATION.md
│   └── DEPLOYMENT_TEST_GUIDE.md
└── examples/
    └── mining_client.py     # Mining Container 클라이언트 예제
```

---

## 8. 관련 문서

| 문서                                                         | 설명                             |
| ------------------------------------------------------------ | -------------------------------- |
| [MINING_INTEGRATION.md](./MINING_INTEGRATION.md)             | Mining + Provider 통합 상세 설계 |
| [DEPLOYMENT_TEST_GUIDE.md](./DEPLOYMENT_TEST_GUIDE.md)       | 실제 배포 환경 테스트 가이드     |
| [API_GATEWAY_ARCHITECTURE.md](./API_GATEWAY_ARCHITECTURE.md) | API Gateway 설계 (Redis Streams) |

---

## 9. 변경 이력

| 버전 | 날짜       | 변경 내용                         |
| ---- | ---------- | --------------------------------- |
| 1.0  | 2026-01-12 | 초안 작성 (레거시 문서 통합)      |
| 1.1  | 2026-01-12 | 리소스 격리 및 OOM 처리 섹션 추가 |
