# 🚀 Worldland GPU Rental - 사용자 가이드

이 문서는 Worldland GPU Rental 플랫폼을 사용하는 **두 가지 유형의 사용자**를 위한 종합 가이드입니다.

---

## 📋 목차

1. [사용자 유형 개요](#사용자-유형-개요)
2. [User (GPU 대여자) 가이드](#user-gpu-대여자-가이드)
3. [Provider (GPU 제공자) 가이드](#provider-gpu-제공자-가이드)
4. [API Reference](#api-reference)
5. [FAQ](#faq)

---

## 사용자 유형 개요

| 유형         | 역할                              | 주요 기능                         |
| ------------ | --------------------------------- | --------------------------------- |
| **User**     | GPU 컨테이너를 대여하여 사용      | Job 생성, SSH 접속, 모니터링      |
| **Provider** | GPU 서버를 플랫폼에 등록하여 대여 | 노드 등록, 리소스 제공, 수익 창출 |

```
┌─────────────────────────────────────────────────────────────┐
│                     Worldland Platform                       │
│                                                              │
│    ┌──────────────┐                  ┌──────────────┐       │
│    │    User      │   GPU Rental     │   Provider   │       │
│    │  (대여자)    │ ◄────────────────► │  (제공자)    │       │
│    │              │                  │              │       │
│    │ • Job 생성   │                  │ • GPU 등록   │       │
│    │ • SSH 접속   │                  │ • 수익 창출  │       │
│    │ • 모니터링   │                  │ • 마이닝     │       │
│    └──────────────┘                  └──────────────┘       │
└─────────────────────────────────────────────────────────────┘
```

---

# User (GPU 대여자) 가이드

GPU 컨테이너를 대여하여 머신러닝, 딥러닝, 렌더링 등의 작업을 수행하려는 사용자를 위한 가이드입니다.

## 🎯 빠른 시작

### Step 1: 로그인

1. [https://worldland.io](https://worldland.io) 접속
2. **"Login"** 클릭
3. Google 계정으로 로그인 (또는 Dev Login 사용)

```
📌 지원하는 로그인 방법:
• Google OAuth 2.0
• Dev Login (테스트 환경에서만)
```

### Step 2: GPU Job 생성

1. 로그인 후 **"New Job"** 버튼 클릭
2. GPU 타입 선택 (예: Tesla T4, RTX 4090)
3. 리소스 설정:
   - CPU Cores: 2 ~ 16 cores
   - Memory: 8 ~ 64 GB
   - Storage: 20 ~ 200 GB
4. 환경 템플릿 선택:
   - PyTorch
   - TensorFlow
   - Ubuntu
   - CUDA Base
5. SSH 비밀번호 설정 (6자 이상)
6. **"Create Job"** 클릭

### Step 3: SSH 접속

Job이 **Running** 상태가 되면 SSH로 접속할 수 있습니다.

```bash
# SSH 접속 명령어 (Job 상세에서 확인)
ssh root@<HOST_IP> -p <PORT>

# 예시
ssh root@34.64.100.50 -p 32001
```

---

## 📱 웹 인터페이스 사용법

### 대시보드 (`/dashboard`)

로그인 후 메인 대시보드에서 전체 현황을 확인할 수 있습니다.

### Job 목록 페이지 (`/jobs`)

| 항목          | 설명                                                      |
| ------------- | --------------------------------------------------------- |
| **Job ID**    | 고유 식별자                                               |
| **Status**    | `Creating` → `Pending` → `Running` → `Succeeded`/`Failed` |
| **GPU**       | 할당된 GPU 타입 및 개수                                   |
| **Resources** | CPU, Memory, Storage                                      |
| **SSH Info**  | 접속 정보 (Running 상태에서만)                            |
| **Price**     | 시간당 요금                                               |

#### Job 상태 설명

| 상태        | 색상      | 설명                    |
| ----------- | --------- | ----------------------- |
| `Creating`  | 🔵 파란색 | Job 생성 중             |
| `Pending`   | 🟡 노란색 | Pod 스케줄링 대기 중    |
| `Running`   | 🟢 녹색   | 실행 중 (SSH 접속 가능) |
| `Succeeded` | ⚪ 회색   | 정상 종료               |
| `Failed`    | 🔴 빨간색 | 오류로 종료             |

### Job 생성 페이지 (`/jobs/create`)

GPU Job을 생성하는 페이지입니다.

**입력 항목:**

| 필드         | 필수 | 기본값  | 설명                         |
| ------------ | ---- | ------- | ---------------------------- |
| GPU Type     | ✅   | -       | 사용할 GPU 타입 선택         |
| GPU Count    | ❌   | 1       | GPU 개수 (1~8)               |
| CPU Cores    | ❌   | 4       | CPU 코어 수                  |
| Memory       | ❌   | 16 GB   | 메모리 용량                  |
| Storage      | ❌   | 50 GB   | 디스크 용량                  |
| Template     | ❌   | PyTorch | Docker 이미지 템플릿         |
| Duration     | ❌   | 1 hour  | 사용 시간                    |
| SSH Password | ✅   | -       | SSH 접속 비밀번호 (6자 이상) |

**실시간 GPU 가용성:**

- 🟢 **Live**: 클러스터에서 실시간 조회
- 🟡 **Cached**: 캐시된 데이터 (클러스터 오프라인)

---

## 🔧 API 사용법 (프로그래밍 방식)

### 인증

모든 API 요청에는 JWT 토큰이 필요합니다.

```bash
# Google OAuth 로그인 후 토큰 받기
curl -X POST https://api.worldland.io/api/v1/auth/google \
  -H "Content-Type: application/json" \
  -d '{"id_token": "YOUR_GOOGLE_ID_TOKEN"}'

# 응답
{
  "token": "eyJhbGc...",
  "user": {
    "id": "user-123",
    "email": "user@example.com",
    "name": "User Name"
  }
}
```

### Job 생성

```bash
curl -X POST https://api.worldland.io/api/v1/jobs \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "gpu_type": "Tesla T4",
    "gpu_count": 1,
    "cpu_cores": "4",
    "memory_gb": "16",
    "storage_gb": "50",
    "ssh_password": "mypassword123",
    "duration_hours": 2
  }'
```

**응답:**

```json
{
  "job_id": "job-abc123",
  "status": "creating",
  "gpu_model": "Tesla T4",
  "cpu_cores": "4",
  "memory_gb": "16Gi",
  "ssh_host": "34.64.100.50",
  "ssh_port": 32001,
  "ssh_user": "root",
  "price_per_hour": 0.5,
  "message": "Job creation initiated"
}
```

### Job 조회

```bash
# 모든 Job 조회
curl -X GET https://api.worldland.io/api/v1/jobs \
  -H "Authorization: Bearer YOUR_JWT_TOKEN"

# 특정 Job 조회
curl -X GET https://api.worldland.io/api/v1/jobs/job-abc123 \
  -H "Authorization: Bearer YOUR_JWT_TOKEN"
```

### Job 삭제

```bash
curl -X DELETE https://api.worldland.io/api/v1/jobs/job-abc123 \
  -H "Authorization: Bearer YOUR_JWT_TOKEN"
```

### Provider 조회

```bash
# 모든 Provider 조회
curl -X GET https://api.worldland.io/api/v1/providers

# GPU 가용성 조회 (실시간)
curl -X GET https://api.worldland.io/api/v1/providers/gpu-availability

# 특정 GPU 타입 필터
curl -X GET "https://api.worldland.io/api/v1/providers/gpu-availability?gpu_type=Tesla%20T4"
```

---

## 💡 사용 팁

### 1. 적절한 리소스 선택

| 작업 유형   | 권장 GPU     | 권장 메모리 | 권장 스토리지 |
| ----------- | ------------ | ----------- | ------------- |
| 소규모 학습 | Tesla T4 × 1 | 16 GB       | 50 GB         |
| 중규모 학습 | RTX 4090 × 1 | 32 GB       | 100 GB        |
| 대규모 학습 | RTX 4090 × 4 | 64 GB       | 200 GB        |
| 추론/서빙   | Tesla T4 × 1 | 8 GB        | 20 GB         |

### 2. OOMKilled 대응

메모리 부족으로 컨테이너가 종료된 경우:

```json
{
  "status": "Failed",
  "failure_reason": "OOMKilled",
  "suggestion": {
    "action": "increase_memory",
    "recommended_memory": "32Gi",
    "message": "메모리가 부족하여 컨테이너가 종료되었습니다. 32Gi 이상으로 새 Job을 생성해주세요."
  }
}
```

**해결 방법:** 권장 메모리 이상으로 새 Job 생성

### 3. 장기 작업 시

- `screen` 또는 `tmux` 사용하여 세션 유지
- 중요 데이터는 정기적으로 외부로 백업
- `nohup` 명령어로 백그라운드 실행

```bash
# tmux 세션 생성
tmux new -s training

# 학습 스크립트 실행
python train.py

# 세션 분리 (Ctrl+B, D)
# 세션 재접속
tmux attach -t training
```

---

## ⚠️ 주의사항

1. **SSH 비밀번호 보안**: 안전한 비밀번호를 사용하세요
2. **리소스 정확성**: 요청한 리소스는 정확히 할당됩니다 (Guaranteed QoS)
3. **비용 확인**: Job 생성 전 예상 비용을 확인하세요
4. **데이터 백업**: Job 종료 시 데이터가 삭제됩니다

---

# Provider (GPU 제공자) 가이드

GPU 서버를 보유하고 있으며, 이를 플랫폼에 등록하여 수익을 창출하려는 사용자를 위한 가이드입니다.

## 🎯 빠른 시작

### 사전 요구사항

| 항목        | 요구사항                                    |
| ----------- | ------------------------------------------- |
| **OS**      | Ubuntu 20.04+ / Debian 11+                  |
| **GPU**     | NVIDIA GPU (Compute Capability 3.5+)        |
| **Driver**  | NVIDIA Driver 470+                          |
| **Network** | Public IP, 6443/10250/30000-32767 포트 오픈 |
| **CPU**     | 4+ cores                                    |
| **Memory**  | 8GB+                                        |
| **Disk**    | 50GB+                                       |

### Step 1: GPU 인스턴스 준비

```bash
# GCP 예시
gcloud compute instances create gpu-worker-1 \
  --zone=us-central1-a \
  --machine-type=n1-standard-8 \
  --accelerator=type=nvidia-tesla-t4,count=4 \
  --image-family=ubuntu-2204-lts \
  --image-project=ubuntu-os-cloud \
  --boot-disk-size=100GB

# AWS 예시
aws ec2 run-instances \
  --instance-type g4dn.xlarge \
  --image-id ami-0abcdef1234567890
```

### Step 2: NVIDIA 드라이버 설치

```bash
sudo apt update
sudo apt install -y nvidia-driver-535
sudo reboot

# 설치 확인
nvidia-smi
```

### Step 3: Provider SDK 설치

```bash
# 단일 명령으로 설치
curl -sSL https://get.worldland.io/provider | sudo bash -s -- \
  --master-url=https://master.worldland.io \
  --token=<bootstrap-token> \
  --wallet=0x1234abcd...
```

**설치 옵션:**

| 옵션              | 필수 | 설명                        |
| ----------------- | ---- | --------------------------- |
| `--master-url`    | ✅   | Master Orchestrator URL     |
| `--wallet`        | ✅   | 보상 지갑 주소              |
| `--token`         | ✅   | Bootstrap 토큰              |
| `--enable-mining` | ❌   | 마이닝 자동 시작            |
| `--mining-gpu`    | ❌   | 마이닝용 GPU 개수 (기본: 1) |
| `--verbose`       | ❌   | 상세 로그 출력              |

---

## 🔧 SDK가 자동으로 수행하는 작업

1. **시스템 설정**

   - DNS 설정
   - 커널 모듈 로드 (overlay, br_netfilter)
   - sysctl 네트워크 설정
   - swap 비활성화

2. **containerd 설치**

   - containerd 설치 및 SystemdCgroup 활성화

3. **CNI 플러그인 설치**

   - CNI 플러그인 v1.4.0 설치
   - Flannel 호환 설정

4. **Kubernetes 컴포넌트 설치**

   - kubeadm, kubelet, kubectl 설치

5. **NVIDIA Container Toolkit 설치**

   - nvidia-container-toolkit 설치
   - containerd 기본 런타임 설정

6. **클러스터 참여**

   - kubeadm join 실행
   - Node 라벨 설정

7. **Provider 등록**

   - 시스템 스펙 스캔
   - Orchestrator에 등록

8. **Mining 시작** (옵션)
   - Mining Pod 배포
   - Worldland 노드 실행

---

## 📊 상태 확인

### CLI 명령어

```bash
# Provider 상태 확인
worldland-provider status

# 출력 예시:
# [Provider Status]
#   Provider ID: provider-abc123
#   Node Name: gpu-worker-001
#   Status: available
#   Uptime: 3d 12h 45m
#   Healthy: true
#
# [Resources]
#   Total GPUs: 4
#   Mining: 1
#   Rented: 2
#   Available: 1
```

### Mining 관리

```bash
# GPU 할당량 변경
worldland-provider mining set-gpu 2

# Mining 중지/시작
worldland-provider mining stop
worldland-provider mining start

# 상태 확인
worldland-provider mining status

# 로그 확인
worldland-provider logs --mining
worldland-provider logs -f  # 실시간
```

---

## 💰 수익 구조

| 수익 유형  | 설명                | 예상 수익             |
| ---------- | ------------------- | --------------------- |
| **Mining** | Worldland 블록 보상 | 변동 (난이도에 따름)  |
| **Rental** | GPU 대여 수익       | $0.50/GPU/시간 (기본) |

### 수익 확인

```bash
# 대시보드
https://dashboard.worldland.io/providers/<your-provider-id>

# CLI (예정)
worldland-provider earnings
```

---

## 🔒 보안 권장사항

1. **방화벽 설정**: 필요한 포트만 오픈
2. **SSH 키 인증**: 비밀번호 인증 비활성화
3. **정기 업데이트**: `apt update && apt upgrade`
4. **모니터링**: 리소스 사용량 모니터링 설정

```bash
# 필수 포트만 허용
sudo ufw allow 6443/tcp   # K8s API
sudo ufw allow 10250/tcp  # Kubelet
sudo ufw allow 30000:32767/tcp  # NodePort
sudo ufw enable
```

---

## 🔧 트러블슈팅

### NVIDIA 드라이버 미인식

```bash
# 드라이버 재설치
sudo apt purge nvidia-*
sudo apt install nvidia-driver-535
sudo reboot
```

### Master 연결 실패

```bash
# 네트워크 확인
curl -v https://master.worldland.io/health

# 방화벽 확인
sudo ufw allow 6443/tcp
```

### kubeadm join 실패

```bash
# 기존 설정 초기화
sudo kubeadm reset -f
sudo rm -rf /etc/cni/net.d
sudo iptables -F && sudo iptables -t nat -F

# 다시 시도
worldland-provider-sdk --wallet=... --token=...
```

---

# API Reference

## 인증 API

| 엔드포인트             | 메서드 | 인증 | 설명                |
| ---------------------- | ------ | ---- | ------------------- |
| `/api/v1/auth/google`  | POST   | ❌   | Google OAuth 로그인 |
| `/api/v1/auth/refresh` | POST   | ✅   | 토큰 갱신           |
| `/api/v1/auth/logout`  | POST   | ✅   | 로그아웃            |

## Job API

| 엔드포인트         | 메서드 | 인증 | 설명          |
| ------------------ | ------ | ---- | ------------- |
| `/api/v1/jobs`     | POST   | ✅   | Job 생성      |
| `/api/v1/jobs`     | GET    | ✅   | 내 Job 목록   |
| `/api/v1/jobs/:id` | GET    | ✅   | Job 상세 조회 |
| `/api/v1/jobs/:id` | DELETE | ✅   | Job 삭제      |

## Provider API

| 엔드포인트                           | 메서드 | 인증 | 설명              |
| ------------------------------------ | ------ | ---- | ----------------- |
| `/api/v1/providers`                  | GET    | ❌   | Provider 목록     |
| `/api/v1/providers/search`           | GET    | ❌   | Provider 검색     |
| `/api/v1/providers/:id`              | GET    | ❌   | Provider 상세     |
| `/api/v1/providers/gpu-availability` | GET    | ❌   | 실시간 GPU 가용성 |

## Mining API

| 엔드포인트                              | 메서드 | 인증 | 설명      |
| --------------------------------------- | ------ | ---- | --------- |
| `/api/v1/providers/:id/mining`          | GET    | ✅   | 채굴 상태 |
| `/api/v1/providers/:id/mining/allocate` | POST   | ✅   | GPU 할당  |
| `/api/v1/providers/:id/mining/release`  | POST   | ✅   | GPU 반환  |
| `/api/v1/providers/:id/mining/start`    | POST   | ✅   | 채굴 시작 |
| `/api/v1/providers/:id/mining/stop`     | POST   | ✅   | 채굴 중지 |

---

# FAQ

## User 관련

**Q: Job이 계속 Pending 상태입니다.**
A: GPU 리소스가 부족할 수 있습니다. 다른 GPU 타입을 선택하거나 잠시 후 다시 시도하세요.

**Q: SSH 접속이 안됩니다.**
A: Job이 Running 상태인지 확인하고, 올바른 호스트/포트를 사용하세요.

**Q: 데이터를 영구 저장할 수 있나요?**
A: 현재는 Job 종료 시 데이터가 삭제됩니다. 중요 데이터는 외부로 백업하세요.

## Provider 관련

**Q: Mining과 Rental을 동시에 할 수 있나요?**
A: 네! GPU를 Mining과 Rental용으로 분할 할당합니다.

**Q: Rental 요청이 많으면 Mining GPU도 사용되나요?**
A: `--auto-scale` 옵션을 켜면 자동으로 조정됩니다.

**Q: 여러 노드를 운영할 수 있나요?**
A: 네! 각 노드에서 SDK를 실행하면 됩니다. 같은 지갑 주소를 사용하세요.

---

## 📞 지원

- **GitHub Issues**: https://github.com/worldland/provider-sdk/issues
- **Discord**: https://discord.gg/worldland
- **Email**: support@worldland.io

---

_마지막 업데이트: 2026-01-15_
