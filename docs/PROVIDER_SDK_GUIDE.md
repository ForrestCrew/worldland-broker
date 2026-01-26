# Provider SDK 설치 가이드

## 개요

Worldland Provider SDK는 GPU 보유자가 단일 명령으로 Worldland 네트워크에 참여할 수 있게 해주는 도구입니다.

## 빠른 시작 (GCP/AWS/Azure)

### 1. GPU 인스턴스 준비

```bash
# GCP 예시: Tesla T4 GPU가 장착된 인스턴스 생성
gcloud compute instances create gpu-worker-1 \
  --zone=us-central1-a \
  --machine-type=n1-standard-8 \
  --accelerator=type=nvidia-tesla-t4,count=4 \
  --image-family=ubuntu-2204-lts \
  --image-project=ubuntu-os-cloud \
  --boot-disk-size=100GB \
  --maintenance-policy=TERMINATE
```

### 2. NVIDIA 드라이버 설치 (필수)

```bash
# SSH 접속 후
sudo apt update
sudo apt install -y nvidia-driver-535
sudo reboot
```

### 3. Provider SDK 설치 (단일 명령!)

```bash
curl -sSL https://get.worldland.io/provider | sudo bash -s -- \
  --master-url=https://master.worldland.io \
  --token=<bootstrap-token> \
  --wallet=0x1234abcd...
```

끝! 🎉

---

## 상세 가이드

### 사전 요구사항

| 항목    | 요구사항                                    |
| ------- | ------------------------------------------- |
| OS      | Ubuntu 20.04+ / Debian 11+                  |
| GPU     | NVIDIA GPU (Compute Capability 3.5+)        |
| Driver  | NVIDIA Driver 470+                          |
| Network | Public IP, 6443/10250/30000-32767 포트 오픈 |
| CPU     | 4+ cores                                    |
| Memory  | 8GB+                                        |
| Disk    | 50GB+                                       |

### 설치 옵션

```bash
# 기본 설치
./worldland-provider-sdk \
  --wallet=0x... \
  --master-url=http://MASTER_IP:8080

# 전체 옵션
./worldland-provider-sdk \
  --master-url=http://34.64.255.101:8080 \  # Master Orchestrator URL
  --wallet=0x1234... \                       # 보상 지갑 주소 (필수)
  --enable-mining \                          # Mining 자동 시작 (선택)
  --mining-gpu=1 \                           # 채굴용 GPU 개수 (기본: 1)
  --network-id=10396 \                       # Worldland 체인 ID
  --verbose                                  # 상세 로그
```

### Bootstrap Token 발급

Master 노드에서:

```bash
# 새 토큰 생성
kubeadm token create --print-join-command

# 출력 예시:
# kubeadm join 10.178.0.13:6443 --token abcdef.0123456789abcdef \
#   --discovery-token-ca-cert-hash sha256:1234...
```

---

## SDK가 자동으로 수행하는 작업 (v2.0)

### Step 1: 시스템 설정 (신규!)

- `/run/systemd/resolve/resolv.conf` 자동 생성 (DNS 설정)
- 커널 모듈 로드 (overlay, br_netfilter)
- sysctl 네트워크 설정
- swap 비활성화

### Step 2: containerd 설치

- containerd 설치 및 설정
- SystemdCgroup 활성화

### Step 3: CNI 플러그인 설치 (신규!)

- CNI 플러그인 v1.4.0 자동 다운로드
- `/opt/cni/bin/`에 설치
- Flannel 호환 설정

### Step 4: Kubernetes 컴포넌트 설치

- kubeadm, kubelet, kubectl 설치
- apt-mark hold로 버전 고정

### Step 5: NVIDIA Container Toolkit 설치 (개선!)

- nvidia-container-toolkit 설치
- containerd 기본 런타임으로 설정 (`--set-as-default`)
- containerd conf.d imports 자동 설정
- containerd/kubelet 재시작

### Step 6: 클러스터 참여

- Redis를 통해 Orchestrator에서 join 명령 수신
- kubeadm join 실행
- Node 라벨 설정 (`worldland.io/rental-type=gpu`)
- GPU 리소스 등록

### Step 7: Provider 등록

- 시스템 스펙 자동 스캔 (GPU, CPU, Memory)
- Public IP 자동 감지
- Orchestrator에 등록

### Step 8: Mining 시작 (옵션)

- Mining Pod 자동 배포 (옵션 활성화 시)
- Worldland 노드 실행

---

## CLI 명령어

### 상태 확인

```bash
worldland-provider status
```

출력:

```
[Provider Status]
  Provider ID: provider-abc123
  Node Name: gcp-gpu-worker-001
  Status: available
  Uptime: 3d 12h 45m
  Healthy: true

[Resources]
  Total GPUs: 4
  Mining: 1
  Rented: 2
  Available: 1

[Mining Status]
  Status: running
  Current GPUs: 1
  Target GPUs: 1
  Auto-Scale: false (min: 1, max: 4)
  Last Update: 2024-01-15T10:30:00Z
```

### Mining 관리

```bash
# GPU 할당량 변경
worldland-provider mining set-gpu 2

# Mining 중지
worldland-provider mining stop

# Mining 시작
worldland-provider mining start

# 상태 확인
worldland-provider mining status
```

### 로그 확인

```bash
# 전체 로그
worldland-provider logs

# Mining Pod 로그
worldland-provider logs --mining

# 실시간 로그
worldland-provider logs -f
```

---

## 시스템 서비스로 등록

```bash
# 서비스 활성화 (부팅 시 자동 시작)
sudo systemctl enable worldland-provider

# 서비스 시작
sudo systemctl start worldland-provider

# 서비스 상태 확인
sudo systemctl status worldland-provider

# 로그 확인
journalctl -u worldland-provider -f
```

---

## 문제 해결

### NVIDIA 드라이버가 감지되지 않음

```bash
# 드라이버 설치 확인
nvidia-smi

# 드라이버 재설치
sudo apt purge nvidia-*
sudo apt install nvidia-driver-535
sudo reboot
```

### Master에 연결할 수 없음

```bash
# 네트워크 확인
curl -v https://master.worldland.io/health

# 방화벽 확인
sudo ufw allow 6443/tcp
sudo ufw allow 10250/tcp
sudo ufw allow 30000:32767/tcp
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

### Heartbeat 전송 실패

```bash
# Redis 연결 확인
redis-cli -h master.worldland.io -p 6379 ping

# Pod 상태 확인
kubectl get pods -n worldland-mining
```

---

## 수익 구조

| 수익 유형 | 설명                | 예상 수익             |
| --------- | ------------------- | --------------------- |
| Mining    | Worldland 블록 보상 | 변동 (난이도에 따름)  |
| Rental    | GPU 대여 수익       | $0.50/GPU/시간 (기본) |

### 수익 확인

```bash
# 대시보드에서 확인
https://dashboard.worldland.io/providers/<your-provider-id>

# CLI로 확인 (예정)
worldland-provider earnings
```

---

## 보안 권장사항

1. **방화벽 설정**: 필요한 포트만 오픈
2. **SSH 키 인증**: 비밀번호 인증 비활성화
3. **정기 업데이트**: `apt update && apt upgrade`
4. **모니터링**: 리소스 사용량 모니터링 설정

---

## FAQ

**Q: Mining과 Rental을 동시에 할 수 있나요?**
A: 네! GPU를 Mining과 Rental용으로 분할 할당합니다.

**Q: Rental 요청이 많으면 Mining GPU도 사용되나요?**
A: `--auto-scale` 옵션을 켜면 자동으로 조정됩니다.

**Q: 여러 노드를 운영할 수 있나요?**
A: 네! 각 노드에서 SDK를 실행하면 됩니다. 같은 지갑 주소를 사용하면 됩니다.

**Q: 서버를 재시작하면 어떻게 되나요?**
A: systemd 서비스로 등록했다면 자동으로 재시작됩니다.

---

## 지원 및 문의

- GitHub Issues: https://github.com/worldland/provider-sdk/issues
- Discord: https://discord.gg/worldland
- Email: support@worldland.io
