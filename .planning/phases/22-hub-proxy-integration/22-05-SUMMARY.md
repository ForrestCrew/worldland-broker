---
phase: 22-hub-proxy-integration
plan: 05
subsystem: sessions
tags: [kubernetes, k8s, pod-lifecycle, session-workers, state-sync]

# Dependency graph
requires:
  - phase: 22-01
    provides: TenantOrchestrator for namespace management
  - phase: 22-02
    provides: K8s types and helpers (GPUJobSpec, labels)
  - phase: 22-03
    provides: JobManager for Pod creation/deletion
  - phase: 22-04
    provides: PodWatcher with StateChangeHandler interface
provides:
  - K8sStateHandler implementing StateChangeHandler for Hub integration
  - ConfirmationWorker with K8s Pod creation on blockchain confirmation
  - ExpirationWorker with K8s Pod deletion on session expiration
  - Optional K8s integration via WithK8s() methods (backward compatible)
affects: [22-06-wiring, future-pod-management, session-lifecycle]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - K8sStateHandler pattern for bridging K8s events to Hub state machine
    - Optional K8s integration via WithK8s() builder methods
    - Backward compatibility pattern (K8s nil-checks, Node API primary)
    - Blockchain-first state transitions (K8s handles infrastructure-only failures)

key-files:
  created:
    - internal/sessions/k8s_handler.go
  modified:
    - internal/sessions/confirmation_worker.go
    - internal/sessions/expiration_worker.go

key-decisions:
  - "K8s integration is optional via WithK8s() methods for backward compatibility"
  - "Blockchain confirmation takes precedence over K8s Pod state"
  - "Node API remains primary provisioning path, K8s is additive"
  - "K8s failures are non-fatal and logged (best effort)"
  - "SSH password generated but not yet stored (TODO for future)"

patterns-established:
  - "StateChangeHandler implementation: K8sStateHandler bridges PodWatcher to SessionManager"
  - "Optional integration pattern: WithK8s() methods on workers"
  - "Idempotent state handling: check current state before transitions"
  - "Graceful degradation: K8s failures don't fail session operations"

# Metrics
duration: 5min
completed: 2026-02-02
---

# Phase 22 Plan 05: Hub-K8s Worker Integration Summary

**K8s Pod lifecycle integrated into ConfirmationWorker and ExpirationWorker with K8sStateHandler bridging PodWatcher events to Hub state machine**

## Performance

- **Duration:** 5 min
- **Started:** 2026-02-02T12:51:45Z
- **Completed:** 2026-02-02T12:56:33Z
- **Tasks:** 3
- **Files modified:** 3

## Accomplishments
- K8sStateHandler bridges PodWatcher events to SessionManager state transitions
- ConfirmationWorker creates K8s Pods after blockchain transaction verification
- ExpirationWorker deletes K8s Pods on session expiration
- Backward compatible - workers function without K8s configuration

## Task Commits

Each task was committed atomically:

1. **Task 1: Create K8sStateHandler** - `977711b` (feat)
2. **Task 2: Modify ConfirmationWorker for K8s Pod creation** - `140977e` (feat)
3. **Task 3: Modify ExpirationWorker for K8s Pod deletion** - `27781ae` (feat)

## Files Created/Modified

### Created
- `internal/sessions/k8s_handler.go` - K8sStateHandler implementing StateChangeHandler interface
  - OnPodRunning: idempotent, respects PENDING (blockchain confirmation precedence)
  - OnPodFailed: transitions RUNNING/PENDING to FAILED with reason
  - OnPodSucceeded: transitions to STOPPED on normal completion
  - OnPodDeleted: handles unexpected deletions and orphan pods

### Modified
- `internal/sessions/confirmation_worker.go` - Integrated K8s Pod creation
  - Added JobManager, TenantOrchestrator, defaultImage fields
  - WithK8s() method for optional configuration
  - Creates Pod after txHash verification succeeds
  - Ensures tenant namespace exists before Pod creation
  - Configures GPUJobSpec with node info (provider, GPU type, resources)
  - Non-fatal K8s failures (Node API remains primary)

- `internal/sessions/expiration_worker.go` - Integrated K8s Pod deletion
  - Added JobManager field
  - WithK8s() method for optional configuration
  - Deletes Pod BEFORE Node API call on expiration
  - Idempotent deletion (ignores NotFound errors)
  - K8s failures logged but don't fail operations

## Decisions Made

1. **Blockchain confirmation takes precedence over K8s Pod state**
   - Rationale: Blockchain is source of truth for session state transitions. K8s only handles infrastructure failures (Pod crash, eviction).
   - Impact: K8sStateHandler skips state transition when Pod is Running but session is still PENDING (awaiting blockchain confirmation).

2. **Node API remains primary provisioning path**
   - Rationale: Phase 22 is additive integration. Existing Node API flow must continue working.
   - Impact: ConfirmationWorker calls both K8s Pod creation AND Node API. K8s failures are non-fatal.

3. **Optional K8s integration via WithK8s() methods**
   - Rationale: Backward compatibility - Hub can run without K8s configured.
   - Impact: Workers have nil checks for JobManager/TenantOrchestrator. WithK8s() is optional builder method.

4. **SSH password not yet stored in session**
   - Rationale: Session domain model doesn't have SSH password field. Future enhancement needed.
   - Impact: Password is generated and logged but not persisted. Marked as TODO.

5. **Default GPU count is 1**
   - Rationale: Session domain model doesn't have GPUCount field.
   - Impact: All K8s Pods created with 1 GPU. Future enhancement for multi-GPU support.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Used node.GPUType instead of node.GPUModel**
- **Found during:** Task 2 (ConfirmationWorker modification)
- **Issue:** Plan specified `node.GPUModel` but Node struct has `GPUType` field
- **Fix:** Changed to `node.GPUType` in GPUJobSpec construction
- **Files modified:** internal/sessions/confirmation_worker.go
- **Verification:** `go build ./internal/sessions/...` compiles successfully
- **Committed in:** 140977e (Task 2 commit)

**2. [Rule 2 - Missing Critical] Added WithK8s() builder methods**
- **Found during:** Task 2 and 3 (Worker modifications)
- **Issue:** Plan didn't specify how to configure optional K8s components
- **Fix:** Added WithK8s() builder methods to both workers for clean optional configuration
- **Files modified:** internal/sessions/confirmation_worker.go, internal/sessions/expiration_worker.go
- **Verification:** Follows Go builder pattern, allows nil-checks in worker logic
- **Committed in:** 140977e, 27781ae (Task commits)

**3. [Rule 2 - Missing Critical] Set default image in constructor**
- **Found during:** Task 2 (ConfirmationWorker modification)
- **Issue:** Plan specified using K8sConfig.DefaultImage but config package doesn't have K8sConfig yet
- **Fix:** Set defaultImage to "ubuntu:22.04" in constructor, WithK8s() can override
- **Files modified:** internal/sessions/confirmation_worker.go
- **Verification:** Provides sensible default, configurable later
- **Committed in:** 140977e (Task 2 commit)

---

**Total deviations:** 3 auto-fixed (1 bug, 2 missing critical)
**Impact on plan:** All fixes necessary for compilation and backward compatibility. No scope creep.

## Issues Encountered

None - all tasks executed as planned with minor field name corrections.

## Next Phase Readiness

**Ready for:**
- Phase 22-06: Wire PodWatcher to Hub lifecycle (main.go integration)
- K8sStateHandler ready to receive PodWatcher events
- Workers ready for K8s components via WithK8s() configuration

**Blockers/Concerns:**
- SSH password storage: Session domain model needs SSHPassword field (future enhancement)
- GPU count: Session domain model needs GPUCount field for multi-GPU support
- K8s config: Config package needs K8sConfig struct for default image configuration

**Technical debt:**
- TODO in ConfirmationWorker: Store SSH password in session after Pod creation
- Hardcoded GPU count = 1 (needs multi-GPU support)
- Hardcoded resource requests (4 CPU, 16Gi memory) - should be configurable

---
*Phase: 22-hub-proxy-integration*
*Completed: 2026-02-02*
