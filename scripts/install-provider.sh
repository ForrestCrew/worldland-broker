#!/bin/bash
#
# Worldland Provider SDK Installer
#
# Usage:
#   curl -sSL https://get.worldland.io/provider | sudo bash -s -- \
#     --master-url=https://master.worldland.io \
#     --token=<bootstrap-token> \
#     --wallet=0x1234...
#
# Or download and run manually:
#   chmod +x install-provider.sh
#   sudo ./install-provider.sh --wallet=0x1234... --token=...
#

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Default values
MASTER_URL="${MASTER_URL:-https://master.worldland.io}"
REDIS_ADDR=""
WALLET_ADDR=""
TOKEN=""
PROVIDER_ID=""
MINING_GPU=1
MINING_ENABLED=true
AUTO_JOIN=true
VERBOSE=false

# SDK binary info
SDK_VERSION="1.0.0"
SDK_BINARY_NAME="worldland-provider-sdk"
SDK_DOWNLOAD_URL="https://github.com/worldland/provider-sdk/releases/download/v${SDK_VERSION}"
INSTALL_DIR="/usr/local/bin"

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --master-url=*)
            MASTER_URL="${1#*=}"
            shift
            ;;
        --redis=*)
            REDIS_ADDR="${1#*=}"
            shift
            ;;
        --wallet=*)
            WALLET_ADDR="${1#*=}"
            shift
            ;;
        --token=*)
            TOKEN="${1#*=}"
            shift
            ;;
        --provider-id=*)
            PROVIDER_ID="${1#*=}"
            shift
            ;;
        --mining-gpu=*)
            MINING_GPU="${1#*=}"
            shift
            ;;
        --no-mining)
            MINING_ENABLED=false
            shift
            ;;
        --no-auto-join)
            AUTO_JOIN=false
            shift
            ;;
        --verbose)
            VERBOSE=true
            shift
            ;;
        --help)
            echo "Worldland Provider SDK Installer"
            echo ""
            echo "Usage:"
            echo "  curl -sSL https://get.worldland.io/provider | sudo bash -s -- [OPTIONS]"
            echo ""
            echo "Required Options:"
            echo "  --wallet=ADDR        Worldland wallet address for rewards"
            echo "  --token=TOKEN        Bootstrap token for cluster join"
            echo ""
            echo "Optional Options:"
            echo "  --master-url=URL     Master cluster URL (default: https://master.worldland.io)"
            echo "  --redis=ADDR         Redis address (default: derived from master URL)"
            echo "  --provider-id=ID     Provider ID (auto-generated if empty)"
            echo "  --mining-gpu=N       Initial GPUs for mining (default: 1)"
            echo "  --no-mining          Disable mining"
            echo "  --no-auto-join       Don't auto-execute kubeadm join"
            echo "  --verbose            Enable verbose output"
            echo ""
            exit 0
            ;;
        *)
            echo -e "${RED}Unknown option: $1${NC}"
            exit 1
            ;;
    esac
done

# Functions
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[✓]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[!]${NC} $1"
}

log_error() {
    echo -e "${RED}[✗]${NC} $1"
}

check_root() {
    if [[ $EUID -ne 0 ]]; then
        log_error "This script must be run as root (use sudo)"
        exit 1
    fi
}

check_os() {
    if [[ ! -f /etc/os-release ]]; then
        log_error "Cannot detect OS. This script requires Linux."
        exit 1
    fi
    
    . /etc/os-release
    
    case "$ID" in
        ubuntu|debian)
            log_success "Detected OS: $PRETTY_NAME"
            ;;
        *)
            log_warning "Unsupported OS: $PRETTY_NAME. Proceeding anyway..."
            ;;
    esac
}

check_nvidia() {
    if ! command -v nvidia-smi &> /dev/null; then
        log_error "NVIDIA drivers not found. Please install NVIDIA drivers first."
        echo ""
        echo "Installation guide:"
        echo "  Ubuntu: sudo apt install nvidia-driver-535"
        echo "  Then reboot and run this script again."
        exit 1
    fi
    
    GPU_COUNT=$(nvidia-smi --query-gpu=name --format=csv,noheader | wc -l)
    GPU_NAME=$(nvidia-smi --query-gpu=name --format=csv,noheader | head -1)
    
    log_success "Detected ${GPU_COUNT} GPU(s): ${GPU_NAME}"
}

detect_arch() {
    ARCH=$(uname -m)
    case "$ARCH" in
        x86_64)
            ARCH="amd64"
            ;;
        aarch64)
            ARCH="arm64"
            ;;
        *)
            log_error "Unsupported architecture: $ARCH"
            exit 1
            ;;
    esac
    log_success "Architecture: $ARCH"
}

download_sdk() {
    log_info "Downloading Worldland Provider SDK v${SDK_VERSION}..."
    
    DOWNLOAD_FILE="${SDK_BINARY_NAME}-linux-${ARCH}"
    DOWNLOAD_PATH="${SDK_DOWNLOAD_URL}/${DOWNLOAD_FILE}"
    
    # Try to download from release URL
    if curl -sSL -o /tmp/${SDK_BINARY_NAME} "${DOWNLOAD_PATH}" 2>/dev/null; then
        chmod +x /tmp/${SDK_BINARY_NAME}
        mv /tmp/${SDK_BINARY_NAME} ${INSTALL_DIR}/${SDK_BINARY_NAME}
        log_success "SDK downloaded to ${INSTALL_DIR}/${SDK_BINARY_NAME}"
    else
        log_warning "Could not download pre-built binary. Attempting to build from source..."
        build_from_source
    fi
    
    # Also create symlink for convenience
    ln -sf ${INSTALL_DIR}/${SDK_BINARY_NAME} ${INSTALL_DIR}/worldland-provider
}

build_from_source() {
    log_info "Building SDK from source..."
    
    # Check if Go is installed
    if ! command -v go &> /dev/null; then
        log_info "Installing Go..."
        curl -sSL https://go.dev/dl/go1.21.5.linux-${ARCH}.tar.gz | tar -C /usr/local -xzf -
        export PATH=$PATH:/usr/local/go/bin
    fi
    
    # Clone and build
    TEMP_DIR=$(mktemp -d)
    cd $TEMP_DIR
    
    git clone --depth 1 https://github.com/worldland/provider-sdk.git 2>/dev/null || {
        log_error "Could not clone repository. Please download the SDK manually."
        exit 1
    }
    
    cd provider-sdk
    go build -o ${INSTALL_DIR}/${SDK_BINARY_NAME} ./cmd/provider-sdk/
    
    # Cleanup
    rm -rf $TEMP_DIR
    
    log_success "SDK built and installed"
}

run_sdk() {
    log_info "Starting Provider SDK..."
    
    SDK_ARGS="--master-url=${MASTER_URL}"
    SDK_ARGS="${SDK_ARGS} --wallet=${WALLET_ADDR}"
    SDK_ARGS="${SDK_ARGS} --token=${TOKEN}"
    
    if [[ -n "$REDIS_ADDR" ]]; then
        SDK_ARGS="${SDK_ARGS} --redis=${REDIS_ADDR}"
    fi
    
    if [[ -n "$PROVIDER_ID" ]]; then
        SDK_ARGS="${SDK_ARGS} --provider-id=${PROVIDER_ID}"
    fi
    
    SDK_ARGS="${SDK_ARGS} --mining-gpu=${MINING_GPU}"
    
    if [[ "$MINING_ENABLED" == "false" ]]; then
        SDK_ARGS="${SDK_ARGS} --enable-mining=false"
    fi
    
    if [[ "$AUTO_JOIN" == "true" ]]; then
        SDK_ARGS="${SDK_ARGS} --auto-join"
    fi
    
    if [[ "$VERBOSE" == "true" ]]; then
        SDK_ARGS="${SDK_ARGS} --verbose"
    fi
    
    # Run the SDK
    ${INSTALL_DIR}/${SDK_BINARY_NAME} ${SDK_ARGS}
}

create_systemd_service() {
    log_info "Creating systemd service..."
    
    cat > /etc/systemd/system/worldland-provider.service << EOF
[Unit]
Description=Worldland Provider SDK
After=network.target docker.service containerd.service
Wants=network.target

[Service]
Type=simple
ExecStart=${INSTALL_DIR}/${SDK_BINARY_NAME} --daemon-only --provider-id=\${PROVIDER_ID} --wallet=${WALLET_ADDR}
Restart=always
RestartSec=10
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

    systemctl daemon-reload
    log_success "Systemd service created: worldland-provider.service"
    echo ""
    echo "To enable auto-start on boot:"
    echo "  sudo systemctl enable worldland-provider"
}

# Main execution
main() {
    echo ""
    echo "======================================================"
    echo "  Worldland Provider SDK Installer v${SDK_VERSION}"
    echo "======================================================"
    echo ""
    
    # Validate required arguments
    if [[ -z "$WALLET_ADDR" ]]; then
        log_error "--wallet is required"
        echo "Usage: $0 --wallet=0x... --token=..."
        exit 1
    fi
    
    if [[ -z "$TOKEN" ]]; then
        log_error "--token is required"
        echo "Usage: $0 --wallet=0x... --token=..."
        exit 1
    fi
    
    # Pre-flight checks
    check_root
    check_os
    detect_arch
    check_nvidia
    
    # Download/install SDK
    download_sdk
    
    # Create systemd service (but don't enable)
    create_systemd_service
    
    # Run the SDK
    run_sdk
}

main "$@"
