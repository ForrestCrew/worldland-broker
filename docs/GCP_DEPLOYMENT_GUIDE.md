# GCP Kubernetes 클러스터 배포 가이드

> **마지막 업데이트**: 2026-01-15
> **검증 환경**: GCP asia-northeast3, Ubuntu/Debian, Tesla T4 x4

## 1. 개요

이 가이드는 GCP에서 Worldland GPU 렌탈 플랫폼을 위한 Kubernetes 클러스터를 구축하는 방법을 설명합니다.

### 아키텍처

```
┌─────────────────────────────────────────────────────────────────┐
│                        GCP VPC                                  │
│  ┌─────────────────────┐      ┌─────────────────────────────┐  │
│  │  worldland-master   │      │    worldland-server         │  │
│  │  (Control Plane)    │      │    (GPU Worker)             │  │
│  │  ─────────────────  │      │    ─────────────────────    │  │
│  │  • K8s Master       │◄────►│    • K8s Worker             │  │
│  │  • Redis            │      │    • Tesla T4 x4            │  │
│  │  • PostgreSQL       │      │    • NVIDIA Container TK    │  │
│  │  • CNI (Flannel)    │      │    • Mining Pod             │  │
│  │                     │      │    • GPU Job Pods           │  │
│  └─────────────────────┘      └─────────────────────────────┘  │
│        Internal IP:                   Internal IP:              │
│        10.178.0.13                    10.178.0.10               │
│        Public IP:                     Public IP:                │
│        34.64.255.101                  34.64.249.63              │
└─────────────────────────────────────────────────────────────────┘
```

---

## 2. 사전 준비

### 2.1 GCP 프로젝트 설정

```bash
# 프로젝트 설정
gcloud config set project YOUR_PROJECT_ID

# 필요한 API 활성화
gcloud services enable compute.googleapis.com
```

### 2.2 방화벽 규칙 생성

```bash
# 모든 트래픽 허용 규칙 (개발용)
gcloud compute firewall-rules create worldland-mvp \
  --allow=all \
  --network=default \
  --source-ranges=0.0.0.0/0 \
  --target-tags=gpuserver

# 또는 필요한 포트만 오픈 (프로덕션용)
gcloud compute firewall-rules create worldland-k8s \
  --allow=tcp:6443,tcp:2379-2380,tcp:10250-10252 \
  --network=default \
  --target-tags=k8s-master

gcloud compute firewall-rules create worldland-nodeports \
  --allow=tcp:30000-32767 \
  --network=default \
  --target-tags=gpuserver
```

---

## 3. Master 노드 설정

### 3.1 VM 생성

```bash
gcloud compute instances create worldland-master \
  --zone=asia-northeast3-a \
  --machine-type=e2-medium \
  --image-family=ubuntu-2204-lts \
  --image-project=ubuntu-os-cloud \
  --boot-disk-size=50GB \
  --tags=k8s-master,gpuserver
```

### 3.2 SSH 접속 및 기본 설정

```bash
gcloud compute ssh worldland-master --zone=asia-northeast3-a

# swap 비활성화
sudo swapoff -a
sudo sed -i '/swap/d' /etc/fstab

# 커널 모듈 로드
sudo modprobe overlay
sudo modprobe br_netfilter

cat <<EOF | sudo tee /etc/modules-load.d/containerd.conf
overlay
br_netfilter
EOF

# sysctl 설정
cat <<EOF | sudo tee /etc/sysctl.d/99-kubernetes-cri.conf
net.bridge.bridge-nf-call-iptables  = 1
net.bridge.bridge-nf-call-ip6tables = 1
net.ipv4.ip_forward                 = 1
EOF

sudo sysctl --system
```

### 3.3 containerd 설치

```bash
sudo apt-get update
sudo apt-get install -y containerd

# containerd 설정
sudo mkdir -p /etc/containerd
containerd config default | sudo tee /etc/containerd/config.toml

# SystemdCgroup 활성화
sudo sed -i 's/SystemdCgroup = false/SystemdCgroup = true/' /etc/containerd/config.toml

sudo systemctl restart containerd
sudo systemctl enable containerd
```

### 3.4 Kubernetes 설치

```bash
# 의존성 설치
sudo apt-get install -y apt-transport-https ca-certificates curl gpg

# GPG 키 추가
sudo mkdir -p /etc/apt/keyrings
curl -fsSL https://pkgs.k8s.io/core:/stable:/v1.28/deb/Release.key | \
  sudo gpg --dearmor -o /etc/apt/keyrings/kubernetes-apt-keyring.gpg

# 리포지토리 추가
echo 'deb [signed-by=/etc/apt/keyrings/kubernetes-apt-keyring.gpg] https://pkgs.k8s.io/core:/stable:/v1.28/deb/ /' | \
  sudo tee /etc/apt/sources.list.d/kubernetes.list

# 설치
sudo apt-get update
sudo apt-get install -y kubelet kubeadm kubectl
sudo apt-mark hold kubelet kubeadm kubectl
```

### 3.5 kubeadm init (중요!)

```bash
# Public IP와 Internal IP 확인
INTERNAL_IP=$(hostname -I | awk '{print $1}')
PUBLIC_IP=$(curl -s ifconfig.me)

# kubeadm 초기화 - 반드시 두 IP 모두 인증서에 포함!
sudo kubeadm init \
  --pod-network-cidr=10.244.0.0/16 \
  --apiserver-advertise-address=$INTERNAL_IP \
  --apiserver-cert-extra-sans=$PUBLIC_IP,$INTERNAL_IP

# kubeconfig 설정
mkdir -p $HOME/.kube
sudo cp -i /etc/kubernetes/admin.conf $HOME/.kube/config
sudo chown $(id -u):$(id -g) $HOME/.kube/config
```

### 3.6 CNI (Flannel) 설치

```bash
kubectl apply -f https://raw.githubusercontent.com/flannel-io/flannel/master/Documentation/kube-flannel.yml
```

### 3.7 Redis 및 PostgreSQL 설치

```bash
# Redis
sudo docker run -d \
  --name redis \
  --restart unless-stopped \
  -p 6379:6379 \
  redis:7-alpine

# PostgreSQL
sudo docker run -d \
  --name postgres \
  --restart unless-stopped \
  -e POSTGRES_USER=worldland \
  -e POSTGRES_PASSWORD=worldland \
  -e POSTGRES_DB=worldland \
  -p 5432:5432 \
  postgres:15-alpine
```

---

## 4. Worker 노드 설정 (GPU)

### 4.1 GPU VM 생성

```bash
gcloud compute instances create worldland-server \
  --zone=asia-northeast3-c \
  --machine-type=n1-standard-32 \
  --accelerator=type=nvidia-tesla-t4,count=4 \
  --image-family=common-gpu \
  --image-project=ml-images \
  --boot-disk-size=50GB \
  --maintenance-policy=TERMINATE \
  --tags=gpuserver
```

### 4.2 SSH 접속 및 기본 설정

```bash
gcloud compute ssh worldland-server --zone=asia-northeast3-c

# swap 비활성화 및 커널 모듈 설정 (Master와 동일)
sudo swapoff -a
sudo modprobe overlay
sudo modprobe br_netfilter
```

### 4.3 resolv.conf 생성 (중요!)

```bash
# Pod sandbox 생성 시 DNS 관련 에러 방지
sudo mkdir -p /run/systemd/resolve
echo "nameserver 8.8.8.8" | sudo tee /run/systemd/resolve/resolv.conf
```

### 4.4 containerd 및 Kubernetes 설치

Master와 동일한 방법으로 설치

### 4.5 CNI 플러그인 설치 (중요!)

```bash
# CNI 플러그인 다운로드 및 설치
CNI_VERSION="v1.4.0"
sudo mkdir -p /opt/cni/bin
curl -L "https://github.com/containernetworking/plugins/releases/download/${CNI_VERSION}/cni-plugins-linux-amd64-${CNI_VERSION}.tgz" | \
  sudo tar -C /opt/cni/bin -xz
```

### 4.6 NVIDIA Container Toolkit 설치 및 설정

```bash
# NVIDIA Container Toolkit 설치
distribution=$(. /etc/os-release;echo $ID$VERSION_ID)
curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | \
  sudo gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
echo "deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://nvidia.github.io/libnvidia-container/stable/deb/\$(ARCH) /" | \
  sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list

sudo apt-get update
sudo apt-get install -y nvidia-container-toolkit

# containerd 설정 (기본 런타임으로 설정)
sudo nvidia-ctk runtime configure --runtime=containerd --set-as-default

# containerd conf.d 디렉토리 import 설정
# nvidia-ctk가 /etc/containerd/conf.d/99-nvidia.toml에 설정을 저장하는 경우
if [ -f /etc/containerd/conf.d/99-nvidia.toml ]; then
  # main config에 imports 디렉티브 추가
  if ! grep -q "conf.d" /etc/containerd/config.toml; then
    sudo sed -i '1i imports = ["/etc/containerd/conf.d/*.toml"]' /etc/containerd/config.toml
  fi
fi

sudo systemctl restart containerd
```

### 4.7 클러스터 참여

```bash
# Master에서 join 명령 생성
# (Master에서 실행)
kubeadm token create --print-join-command

# Worker에서 join 실행
sudo kubeadm join 10.178.0.13:6443 --token <TOKEN> \
  --discovery-token-ca-cert-hash sha256:<HASH>

# kubelet 재시작
sudo systemctl restart kubelet
```

### 4.8 노드 라벨 추가 (Master에서)

```bash
# GPU 렌탈용 라벨 추가
kubectl label node worldland-server worldland.io/rental-type=gpu
```

---

## 5. NVIDIA Device Plugin 배포

Master에서:

```bash
kubectl apply -f https://raw.githubusercontent.com/NVIDIA/k8s-device-plugin/v0.14.0/nvidia-device-plugin.yml

# GPU 확인
kubectl get nodes -o custom-columns="NAME:.metadata.name,GPU:.status.allocatable.nvidia\.com/gpu"
```

예상 출력:

```
NAME               GPU
worldland-master   <none>
worldland-server   4
```

---

## 6. 로컬 k8s-proxy-server 설정

### 6.1 kubeconfig 가져오기

```bash
# Master에서 kubeconfig 복사
gcloud compute scp worldland-master:~/.kube/config ~/.kube/gcp-config --zone=asia-northeast3-a

# Public IP로 서버 주소 변경
sed -i 's/10.178.0.13/34.64.255.101/' ~/.kube/gcp-config

# 기본 kubeconfig로 설정
cp ~/.kube/gcp-config ~/.kube/config
```

### 6.2 .env 파일 설정

```bash
cd k8s-proxy-server

cat > .env << 'EOF'
PORT=8080
DEBUG_MODE=true

# GCP Master 연결
REDIS_HOST=34.64.255.101
REDIS_PORT=6379
DB_HOST=34.64.255.101
DB_PORT=5432
DB_NAME=worldland
DB_USER=worldland
DB_PASSWORD=worldland

# Orchestrator
ENABLE_ORCHESTRATOR=true
MASTER_IP=10.178.0.13
MASTER_PUBLIC_IP=34.64.255.101
MASTER_PORT=6443
EOF
```

### 6.3 서버 실행

```bash
make run-dev
```

---

## 7. 검증

### 7.1 클러스터 상태 확인

```bash
# 노드 상태
kubectl get nodes

# GPU 확인
kubectl get nodes -o custom-columns="NAME:.metadata.name,GPU:.status.allocatable.nvidia\.com/gpu"

# 시스템 Pod 확인
kubectl get pods -n kube-system
```

### 7.2 Provider 등록 및 Mining

```bash
# Provider 확인
curl http://localhost:8080/api/v1/providers

# Mining 시작
curl -X POST http://localhost:8080/api/v1/providers/<PROVIDER_ID>/mining/start \
  -H "Content-Type: application/json" \
  -d '{"wallet_address": "0x..."}'

# Mining Pod 확인
kubectl get pods -n worldland-mining
```

### 7.3 GPU Job 생성 및 SSH 접속

```bash
# Job 생성
curl -X POST http://localhost:8080/api/v1/jobs \
  -H "Content-Type: application/json" \
  -d '{
    "name": "gpu-test",
    "image": "nvidia/cuda:11.8.0-base-ubuntu22.04",
    "gpu_count": 1,
    "cpu_cores": "2",
    "memory_mb": "4096",
    "ssh_password": "testpass123"
  }'

# SSH 접속 (Worker Public IP + NodePort)
ssh root@34.64.249.63 -p <SSH_PORT>
```

---

## 8. 트러블슈팅

### 8.1 Pod Sandbox 생성 실패

**증상**: `failed to create sandbox: open /run/systemd/resolve/resolv.conf: no such file or directory`

**해결**:

```bash
sudo mkdir -p /run/systemd/resolve
echo "nameserver 8.8.8.8" | sudo tee /run/systemd/resolve/resolv.conf
sudo systemctl restart kubelet
```

### 8.2 CNI 플러그인 없음

**증상**: `network plugin is not ready: cni config uninitialized`

**해결**:

```bash
sudo mkdir -p /opt/cni/bin
curl -L "https://github.com/containernetworking/plugins/releases/download/v1.4.0/cni-plugins-linux-amd64-v1.4.0.tgz" | \
  sudo tar -C /opt/cni/bin -xz
sudo systemctl restart kubelet
```

### 8.3 NVIDIA GPU가 감지되지 않음

**증상**: `nvidia.com/gpu: <none>`

**해결**:

```bash
# NVIDIA Container Toolkit 설정
sudo nvidia-ctk runtime configure --runtime=containerd --set-as-default

# containerd imports 확인
cat /etc/containerd/config.toml | grep imports

# 없으면 추가
echo 'imports = ["/etc/containerd/conf.d/*.toml"]' | sudo tee -a /tmp/header
cat /etc/containerd/config.toml | sudo tee -a /tmp/header
sudo mv /tmp/header /etc/containerd/config.toml

sudo systemctl restart containerd
sudo systemctl restart kubelet

# Device Plugin 재시작
kubectl delete pod -n kube-system -l name=nvidia-device-plugin-ds
```

### 8.4 Job Pod가 Pending 상태

**증상**: `node(s) didn't match Pod's node affinity/selector`

**해결**:

```bash
kubectl label node worldland-server worldland.io/rental-type=gpu
```

---

## 9. 리소스 정리

```bash
# Job 삭제
kubectl delete pod gpu-anonymous-xxx

# Mining 중지
curl -X POST http://localhost:8080/api/v1/providers/<PROVIDER_ID>/mining/stop

# Worker 노드 제거
kubectl drain worldland-server --ignore-daemonsets --delete-emptydir-data
kubectl delete node worldland-server

# VM 삭제
gcloud compute instances delete worldland-server --zone=asia-northeast3-c
gcloud compute instances delete worldland-master --zone=asia-northeast3-a
```

---

## 10. 변경 이력

| 버전 | 날짜       | 변경 내용                                    |
| ---- | ---------- | -------------------------------------------- |
| 1.0  | 2026-01-15 | 초안 작성 - GCP 배포 전체 과정 및 트러블슈팅 |
