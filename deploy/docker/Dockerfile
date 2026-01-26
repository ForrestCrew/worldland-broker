# ===================================
# Stage 1: Build
# ===================================
FROM golang:1.24-alpine AS builder

WORKDIR /app

# 의존성 먼저 복사 (캐시 활용)
COPY go.mod go.sum ./
RUN go mod download

# 소스코드 복사 및 빌드
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /server ./cmd/server

# ===================================
# Stage 2: Production (Distroless)
# ===================================
FROM gcr.io/distroless/static:nonroot

COPY --from=builder /server /server

EXPOSE 8080

USER nonroot:nonroot

ENTRYPOINT ["/server"]
