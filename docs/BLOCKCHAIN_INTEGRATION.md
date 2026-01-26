# GPU Rental Platform - Blockchain Integration

## 아키텍처 개요

```
┌────────────────────────────────────────────────────────────────────────┐
│                           Frontend (React/Next.js)                     │
│   ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐   │
│   │ MetaMask    │  │ WalletConn  │  │ Session Key │  │  Job UI     │   │
│   │ Login       │  │ ect         │  │ Management  │  │             │   │
│   └──────┬──────┘  └──────┬──────┘  └──────┬──────┘  └──────┬──────┘   │
└──────────┼────────────────┼────────────────┼────────────────┼──────────┘
           │                │                │                │
           ▼                ▼                ▼                ▼
┌────────────────────────────────────────────────────────────────────────┐
│                          Go Backend (k8s-proxy-server)                 │
│   ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐   │
│   │ Wallet Auth │  │ Session     │  │ Blockchain  │  │ Job         │   │
│   │ Handler     │  │ Manager     │  │ Client      │  │ Handler     │   │
│   └──────┬──────┘  └──────┬──────┘  └──────┬──────┘  └──────┬──────┘   │
│          │                │                │                │          │
│          ▼                ▼                ▼                ▼          │
│   ┌─────────────────────────────────────────────────────────────────┐  │
│   │                        Middleware Layer                         │  │
│   │  SessionKeyAuth │ WalletOrSessionKey │ QuotaCheck │ CORS        │  │
│   └─────────────────────────────────────────────────────────────────┘  │
└──────────┬────────────────┬────────────────┬────────────────┬──────────┘
           │                │                │                │
           ▼                ▼                ▼                ▼
     ┌──────────┐    ┌──────────┐    ┌──────────────┐  ┌──────────┐
     │  Redis   │    │ Postgres │    │ BSC/Ethereum │  │   K8s    │
     │ (Session)│    │ (Ledger) │    │ (GPUVault)   │  │ (Jobs)   │
     └──────────┘    └──────────┘    └──────────────┘  └──────────┘
```

## 주요 컴포넌트

### 1. Smart Contracts (`/contracts`)

- **GPUVault.sol**: BEP-20 기반 Vault 컨트랙트
  - 예치(Deposit) / 출금(Withdraw)
  - 세션 키 등록/취소
  - 렌탈 시작/종료 및 정산
  - EIP-712 서명 기반 지불
- **MockUSDT.sol**: 테스트용 USDT 토큰

### 2. Blockchain Client (`/internal/blockchain`)

- BSC/Ethereum RPC 연결
- 컨트랙트 상태 조회 (잔액, 세션 키, 렌탈)
- 트랜잭션 릴레이 (렌탈 시작/종료)

### 3. Wallet Authentication (`/internal/wallet`)

- **Verifier**: EIP-712 서명 검증, 로그인 서명 검증
- **SessionManager**: Redis 기반 세션 키 관리, 쿼터 추적

### 4. Handlers (`/internal/handler`)

- **WalletAuthHandler**: 지갑 로그인, 세션 키 CRUD API

### 5. Middleware (`/internal/middleware`)

- **SessionKeyAuthMiddleware**: 세션 키 인증
- **WalletOrSessionKeyMiddleware**: 하이브리드 인증
- **QuotaCheckMiddleware**: 지출 한도 확인

## API 엔드포인트

### 인증

| Method | Endpoint                                  | 설명                    |
| ------ | ----------------------------------------- | ----------------------- |
| GET    | `/api/v1/auth/login-message?wallet=0x...` | 로그인 메시지 생성      |
| POST   | `/api/v1/auth/wallet`                     | 지갑 로그인 (서명 검증) |
| POST   | `/api/v1/auth/session-key`                | 세션 키 등록            |
| GET    | `/api/v1/auth/session-key/:key`           | 세션 키 정보            |
| DELETE | `/api/v1/auth/session-key/:key`           | 세션 키 취소            |
| GET    | `/api/v1/auth/sessions`                   | 내 세션 목록            |
| POST   | `/api/v1/auth/generate-session-key`       | 세션 키 쌍 생성         |

### 인증 헤더

```
# 세션 키 인증
X-Session-Key: 0x...

# 또는
Authorization: SessionKey 0x...

# 개발용 (DebugMode)
X-User-ID: test-user
```

## 환경 변수

```env
# Blockchain
ENABLE_BLOCKCHAIN=true
BLOCKCHAIN_RPC_URL=https://bsc-dataseed.binance.org/
BLOCKCHAIN_CHAIN_ID=56
VAULT_ADDRESS=0x...
BACKEND_PRIVATE_KEY=...

# Redis (세션 저장)
REDIS_HOST=localhost
REDIS_PORT=6379

# JWT
JWT_SECRET=your-secret-key
```

## 사용 흐름

### 1. 최초 로그인 (지갑 팝업 1회)

```
1. Frontend: GET /api/v1/auth/login-message?wallet=0x...
2. Frontend: MetaMask personal_sign(message)
3. Frontend: POST /api/v1/auth/wallet { wallet, message, signature, timestamp }
4. Backend: JWT 토큰 발급
```

### 2. 세션 키 등록 (지갑 팝업 1회)

```
1. Frontend: 세션 키 쌍 생성 (브라우저)
2. Frontend: EIP-712 서명 (RegisterSessionKey 타입)
3. Frontend: POST /api/v1/auth/session-key { mainWallet, sessionKey, spendLimit, duration, signature }
4. Backend: 세션 Redis에 저장
```

### 3. GPU 렌탈 (팝업 없음!)

```
1. Frontend: POST /api/v1/jobs { ... }
   Headers: X-Session-Key: 0x...
2. Backend: 세션 검증 → K8s Job 생성
3. Backend: (선택) 컨트랙트에 렌탈 기록
```

### 4. 정산

```
1. Job 종료 시 Backend가 사용량 계산
2. 컨트랙트 endRental() 호출
3. Provider에게 자동 지급 (5% 수수료 차감)
```

## 보안

- ✅ 세션 키는 출금(Withdraw) 불가 - 컨트랙트 레벨 제한
- ✅ 지출 한도(Spend Limit) 설정 필수
- ✅ 최대 30일 만료 기간
- ✅ 즉시 취소(Revoke) 가능
- ✅ Nonce로 재사용 공격 방지
