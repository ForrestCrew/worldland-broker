#!/bin/bash

# k8s-proxy-server 실행 스크립트
# Usage: ./run.sh

# .env 파일이 있으면 로드
if [ -f .env ]; then
    export $(grep -v '^#' .env | xargs)
fi

# 기본값 설정
export PROXY_PORT=${PROXY_PORT:-8080}
export DEBUG_MODE=${DEBUG_MODE:-true}
export ENABLE_ORCHESTRATOR=${ENABLE_ORCHESTRATOR:-true}

# PostgreSQL 설정 (비워두면 DB 없이 동작)
export POSTGRES_HOST=${POSTGRES_HOST:-localhost}
export POSTGRES_PORT=${POSTGRES_PORT:-5432}
export POSTGRES_DB=${POSTGRES_DB:-worldland}
export POSTGRES_USER=${POSTGRES_USER:-worldland}
export POSTGRES_PASSWORD=${POSTGRES_PASSWORD:-}
export POSTGRES_SSL_MODE=${POSTGRES_SSL_MODE:-disable}

# Redis 설정
export REDIS_HOST=${REDIS_HOST:-localhost}
export REDIS_PORT=${REDIS_PORT:-6379}

echo "=================================="
echo "  k8s-proxy-server 시작"
echo "=================================="
echo "Port: $PROXY_PORT"
echo "Orchestrator: $ENABLE_ORCHESTRATOR"
echo "Redis: $REDIS_HOST:$REDIS_PORT"
echo "PostgreSQL: $POSTGRES_HOST:$POSTGRES_PORT/$POSTGRES_DB"
echo "=================================="

go run ./cmd/server
