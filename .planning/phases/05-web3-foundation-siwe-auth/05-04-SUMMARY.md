---
phase: 05-web3-foundation-siwe-auth
plan: 04
subsystem: frontend-auth
tags: [siwe, authentication, wagmi, react-context]
dependency-graph:
  requires: [05-01, 05-02]
  provides: [siwe-auth-flow, protected-actions, session-management]
  affects: [05-05, 06-provider-dashboard]
tech-stack:
  added: []
  patterns: [react-context-provider, protected-action-hook, siwe-message-format]
key-files:
  created:
    - worldland-front/lib/api.ts
    - worldland-front/contexts/AuthContext.tsx
    - worldland-front/hooks/useWalletAuth.ts
  modified:
    - worldland-front/app/providers/Web3Provider.tsx
decisions:
  - id: manual-siwe-message
    description: Manual SIWE message creation instead of siwe library for smaller bundle
    rationale: EIP-4361 format is simple; avoid extra dependency
  - id: korean-error-messages
    description: All user-facing error messages in Korean
    rationale: Target audience is Korean users; consistent UX
  - id: session-invalidation
    description: Immediate session invalidation on wallet disconnect or network change
    rationale: Security best practice; prevents stale sessions
metrics:
  tasks: 4
  duration: ~4 minutes
  completed: 2026-01-30
---

# Phase 05 Plan 04: SIWE Authentication Flow Summary

JWT-less SIWE authentication with role-specific messages and 7-day session management

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Create API client for Hub communication | 8500f94 | lib/api.ts |
| 2 | Create AuthContext for SIWE session management | 472a010 | contexts/AuthContext.tsx |
| 3 | Create useWalletAuth hook for protected actions | e0574b5 | hooks/useWalletAuth.ts |
| 4 | Integrate AuthProvider into Web3Provider | 6e7e88e | app/providers/Web3Provider.tsx |

## Key Implementation Details

### API Client (lib/api.ts)

- HubApiClient class for Hub backend communication
- Configured with `credentials: 'include'` for CORS with cookies
- Supports both JSON and raw text responses (nonce endpoint)
- ApiError class for standardized error handling
- Endpoints: getNonce, login, logout

### AuthContext (contexts/AuthContext.tsx)

- SIWE authentication flow with role differentiation
- Provider message: "Worldland Provider로 로그인합니다. GPU 노드를 등록하고 임대를 관리할 수 있습니다."
- User message: "Worldland User로 로그인합니다. GPU 자원을 임대하고 사용할 수 있습니다."
- 7-day session duration stored in localStorage
- Immediate session invalidation on:
  - Wallet disconnect
  - Network change
  - Token expiry
- Manual SIWE message creation (no siwe library dependency)
- Korean error messages: "서명이 취소됐어요"

### useWalletAuth Hook (hooks/useWalletAuth.ts)

- executeProtectedAction for SIWE-gated operations
- Prompts for signature on first protected action (not immediately after connect)
- Validates: connection -> network -> authentication
- Korean error messages for all validation failures
- Role-specific authentication support

### Provider Integration (Web3Provider.tsx)

Provider hierarchy:
1. WagmiProvider (outermost)
2. QueryClientProvider
3. RainbowKitProvider
4. AuthProvider (has access to wagmi hooks)
5. children

## Decisions Made

### Manual SIWE Message Creation

Instead of using the `siwe` library, implemented manual message creation following EIP-4361 format. This reduces bundle size and avoids an extra dependency for a relatively simple string format.

### Korean UX

All user-facing messages are in Korean:
- "지갑을 먼저 연결해주세요" - Wallet not connected
- "올바른 네트워크로 전환해주세요" - Wrong network
- "서명이 취소됐어요" - User rejected signature
- "인증이 필요합니다" - Authentication required

### Session Management

- Storage key: `worldland_auth`
- Stored data: token, address, providerId, role, expiry
- 7-day expiry from login time
- Signature failure does NOT disconnect wallet

## Deviations from Plan

None - plan executed exactly as written.

## Verification Results

- [x] API client exists with Hub endpoint methods
- [x] AuthContext manages SIWE session state
- [x] Different messages for provider vs user roles
- [x] 7-day session with immediate invalidation
- [x] useWalletAuth prompts on first protected action
- [x] Korean error messages present
- [x] Frontend builds without errors

## Next Phase Readiness

**Prerequisites for 05-05:**
- [x] SIWE authentication flow implemented
- [x] Hub API client for nonce/login/logout
- [x] Session management with role differentiation
- [x] Protected action hook for gated operations

**Integration points:**
- AuthContext.requestSignature integrates with Hub /api/v1/auth/nonce and /api/v1/auth/login
- useWalletAuth.executeProtectedAction provides SIWE-gated action execution
- hubApi.setToken enables authenticated API calls

---
*Completed: 2026-01-30T16:15:54Z*
*Duration: ~4 minutes*
