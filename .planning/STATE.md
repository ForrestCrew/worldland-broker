# Worldland Hub - Project State

## Current Position

**Phase:** 22-hub-proxy-integration (Phase 22 of 19)
**Plan:** 05 of ?? complete
**Status:** In progress
**Last activity:** 2026-02-02 - Completed 22-05-PLAN.md (Hub-K8s Worker Integration)

**Progress:** [████████████████████] Phase 22 in progress

## Phase Summary

### Phase 22: Hub-Proxy Integration (IN PROGRESS)

Completed plans:
- **22-01:** Tenant isolation (namespaces, quotas, network policies)
- **22-02:** K8s types and helpers (labels, pod naming)
- **22-03:** JobManager with CreateGPUSession and SSH password injection
- **22-03b:** JobManager delete/query operations
- **22-04:** PodWatcher with Informer-based state synchronization
- **22-05:** Hub-K8s Worker Integration (JUST COMPLETED)

Phase 22 delivered so far:
- TenantOrchestrator: namespace management with resource quotas
- GPUJobSpec and SSHConnectionInfo types
- JobManager: CreateGPUSession, DeleteGPUSession, GetSSHConnectionInfo
- SSH password generation and K8s Secret management
- Pod deletion (graceful 30s and immediate 0s)
- SSH connection info retrieval (host, port, password)
- Pod listing for orphan cleanup
- PodWatcher: Informer-based real-time Pod state monitoring
- StateChangeHandler interface for decoupled state management
- Cache utilities: IsCacheSynced, GetPodFromCache, ListPodsFromCache
- K8sStateHandler: bridges PodWatcher events to SessionManager
- ConfirmationWorker: creates K8s Pods after blockchain confirmation
- ExpirationWorker: deletes K8s Pods on session expiration
- Optional K8s integration via WithK8s() builder methods
- Complete unit test coverage with fake clientset and mock handlers

### Phase 14: ADR-001 Backend Implementation (COMPLETE)

Completed plans:
- **14-01:** Database schema and repository methods for tx_hash confirmation
- **14-02:** SetTxHash handler for linking pending sessions to transactions
- **14-03:** Confirmation API (POST /confirm) and ConfirmationWorker for background verification
- **14-04:** TTL cleanup with soft delete and tx_hash protection

Phase 14 delivered:
- Migration 007: deleted_at column + partial unique index on tx_hash
- RentalSession domain model with DeletedAt field
- Repository methods: GetByTxHash, SetTxHash, SoftDeletePendingBefore, ListPendingWithTxHash
- POST /api/v1/rentals/:id/confirm endpoint with idempotency
- GET /api/v1/rentals/:id endpoint for status polling
- ConfirmationWorker for background verification (10s interval)
- Updated TimeoutEnforcer with soft delete behavior
- PendingTimeout increased to 10 minutes
- CheckInterval reduced to 1 minute
- Sessions with tx_hash preserved during TTL cleanup

## Accumulated Context

### Key Decisions

| Decision | Phase | Rationale | Impact |
|----------|-------|-----------|--------|
| Deferred GPU verification | 04-08 | GPU hardware unavailable in dev; automated tests provide sufficient coverage | GPU validation happens on deployment infrastructure |
| Build tag separation (integration, docker) | 04-08 | Allows Hub tests without GPU hardware | CI can run Hub tests; GPU tests run on actual hardware |
| Manual SIWE message creation | 05-04 | EIP-4361 format is simple; avoid extra dependency | Smaller bundle size |
| Korean error messages | 05-04 | Target audience is Korean users | Consistent UX |
| Session invalidation on disconnect/network change | 05-04 | Security best practice | Prevents stale sessions |
| ENS mainnet config | 05-05 | wagmi type system requires chainId in config chains array | ENS resolution works properly |
| AuthHeader integration | 05-05 | Integrate WalletHeader into AuthHeader (not HeaderNav) since AuthHeader is auth UI | Clean separation of concerns |
| Parameterize CheckpointStore | 09-05 | Allows Hub and Indexer to maintain independent checkpoints | Hub uses 'rental_events', Indexer uses 'indexer_events' |
| Indexed hook naming | 09-06 | Named useIndexedTransactionHistory to differentiate from on-chain useTransactionHistory | Both hooks available for different use cases |
| Soft delete for PENDING timeout | 14-04 | Simpler than TransitionToFailed, preserves audit trail | Sessions get deleted_at timestamp |
| tx_hash protection in TTL | 14-04 | Protects sessions with confirmation in progress (Pitfall #3) | Sessions with tx_hash preserved |
| Graceful vs immediate deletion | 22-03b | Provide both 30s and 0s grace period methods | Hub can choose based on scenario (normal vs force) |
| SSH password fallback | 22-03b | Return empty password on Secret retrieval failure | Caller may have password stored elsewhere |
| Multi-resource cleanup | 22-03b | Delete Pod, Service, AND Secret together | Prevents Secret leakage after session ends |
| Informer with label selector | 22-04 | SharedInformerFactory filters to worldland.io/gpu-rental=true at creation | Reduces memory/network overhead by only watching GPU rental Pods |
| Level-driven state handling | 22-04 | Check current Pod phase instead of tracking transitions | Resilient to missed events, correct after restarts |
| Running+Ready requirement | 22-04 | Require Pod to be Running AND Ready before OnPodRunning | Ensures SSH service is actually available (readiness probe passed) |
| Graceful missing label skip | 22-04 | Skip Pods without session-id label without error | Allows coexistence with other GPU workloads in cluster |
| Blockchain confirmation precedence | 22-05 | Blockchain is source of truth for session state, K8s only handles infrastructure failures | K8sStateHandler skips state transition when Pod Running but session PENDING |
| Optional K8s integration | 22-05 | Hub can run without K8s configured (backward compatibility) | Workers have WithK8s() builder methods and nil checks |
| Node API remains primary | 22-05 | Existing Node API flow must continue working | K8s Pod creation is additive, failures are non-fatal |

### Technical Stack

**Added in Phase 22:**
- TenantOrchestrator for namespace management
- JobManager for Pod lifecycle
- SSH password generation and Secret injection
- Graceful and immediate Pod termination
- Multi-resource cleanup (Pod + Service + Secret)
- PodWatcher with SharedInformerFactory
- StateChangeHandler interface
- Informer-based state synchronization pattern
- Cache-first Pod lookups
- K8sStateHandler for bridging K8s events to Hub state machine
- Optional K8s integration pattern (WithK8s() builder methods)

**Added in Phase 14:**
- SoftDeleter interface for TTL cleanup
- ConfirmationWorker for background verification
- Soft delete pattern for PENDING sessions

**Patterns Established:**
- Graceful termination with configurable grace period
- Idempotent deletion (ignore NotFound errors)
- Multi-resource cleanup patterns
- Pod readiness checking before connection info
- Soft delete instead of hard delete for expired sessions
- tx_hash as confirmation-in-progress marker
- Background worker for blockchain verification
- Partial unique index for idempotency

### Architecture Notes

**ADR-001 Confirmation Flow:**
1. User creates session (PENDING, tx_hash=NULL)
2. User submits tx → SetTxHash links session to transaction
3. ConfirmationWorker verifies tx on-chain
4. If confirmed: PENDING → RUNNING
5. If unconfirmed after timeout: PENDING → soft deleted

**TTL Protection:**
- Sessions WITH tx_hash: preserved (confirmation in progress)
- Sessions WITHOUT tx_hash: soft deleted after 10 minutes
- RUNNING sessions: still use TransitionToFailed for heartbeat timeout

## Blockers & Concerns

### Current Blockers
None

### Deferred Items
1. **GPU hardware verification** - Physical nvidia-smi validation deferred to deployment
2. **Multi-GPU allocation** - Pattern documented, implementation when available

### Technical Debt
- Provider flow test uses nil for rental/balance handlers (acceptable for phase testing)
- Docker/SSH test helpers are placeholders (actual implementation in Node service)

## Files & Structure

### Key Files Created (Phase 14)
```
migrations/007_adr_001_confirmation.sql               # Schema changes
internal/adapters/http/confirmation_handler.go        # ConfirmRental, GetSession handlers
internal/adapters/http/confirmation_handler_test.go   # Handler tests
internal/blockchain/verifier.go                       # TransactionVerifier
internal/blockchain/verifier_test.go                  # Verifier tests
internal/sessions/confirmation_worker.go              # Background verification worker
internal/sessions/confirmation_worker_test.go         # Worker tests
```

### Key Files Modified (Phase 14)
```
internal/domain/rental.go                   # DeletedAt field
internal/domain/interfaces.go               # New repository methods
internal/adapters/postgres/rental_repo.go   # Repository implementation
internal/sessions/timeouts.go               # Soft delete logic
internal/sessions/timeouts_test.go          # Updated tests
cmd/hub/main.go                             # Wire SoftDeleter
```

### Key Files Created (Phase 22)
```
internal/k8s/types.go                       # K8s labels, types, naming helpers
internal/k8s/tenant.go                      # TenantOrchestrator for namespace management
internal/k8s/tenant_test.go                 # Tenant tests
internal/k8s/client.go                      # ClientsetManager singleton
internal/k8s/client_test.go                 # Client tests
internal/k8s/password.go                    # SSH password generation
internal/k8s/job.go                         # JobManager for Pod lifecycle
internal/k8s/job_test.go                    # Job tests
internal/k8s/watcher.go                     # PodWatcher with Informer-based state sync
internal/k8s/watcher_test.go                # Watcher tests
```

## Session Continuity

**Last session:** 2026-02-02T12:56:33Z
**Stopped at:** Completed 22-05-PLAN.md (Hub-K8s Worker Integration)
**Resume file:** None

**Next steps:**
1. Plan 22-06: Wire PodWatcher to Hub lifecycle (main.go integration)
2. Continue Phase 22 remaining plans for full Hub-K8s integration
3. Consider SSH password storage in session domain model

## Test Coverage

**Integration Test Suites:**
- Provider flow (authentication, node registration, certificates)
- Session flow (state transitions, timeouts, cancellation)
- Rental flow (Hub-to-Node orchestration, settlement)
- Container lifecycle (Docker GPU containers)
- SSH connectivity (SC2 automated verification)
- Indexer repository tests (deposit/withdraw, rental history)
- Indexer processor tests (event handling)
- Query repository tests (historical queries)
- Timeout enforcer tests (soft delete behavior)
- K8s JobManager tests (Pod lifecycle, SSH info, deletion) - Phase 22

**Success Criteria Coverage:**
- HUB-01: Provider matching
- HUB-02: Session management
- HUB-03: Blockchain event handling
- SC2: SSH connectivity (automated)
- SC2: GPU verification (deferred to deployment)
- WEB3-01: Wallet connection (RainbowKit)
- WEB3-02: Network validation (NetworkBanner + useNetworkGuard)
- WEB3-03: Header wallet display (WalletHeader + useWalletInfo)
- WEB3-04: Provider SIWE auth
- WEB3-05: User SIWE auth
- IDXR-01: Standalone indexer binary
- IDXR-02: Independent checkpoint management
- IDXR-03: Graceful shutdown
- IDXR-04: Frontend history hooks
- ADR-001: Confirmation flow with tx_hash protection

## Phase 22 Status (IN PROGRESS)

**Completed:**
- 22-01: Tenant isolation with namespaces
- 22-02: K8s types and naming helpers
- 22-03: JobManager CreateGPUSession
- 22-03b: JobManager delete/query operations
- 22-04: PodWatcher with Informer-based state sync
- 22-05: Hub-K8s Worker Integration

**Pending:**
- 22-06: Wire PodWatcher to Hub lifecycle (main.go)
- Additional Phase 22 plans (if any)

## Phase 14 Completion Status

**Completed:**
- 14-01: Database schema and repository methods
- 14-02: SetTxHash handler
- 14-03: ConfirmationWorker
- 14-04: TTL cleanup with soft delete

---
*Last updated: 2026-02-02T12:58:44Z*
*Phase: 22-hub-proxy-integration (IN PROGRESS)*
