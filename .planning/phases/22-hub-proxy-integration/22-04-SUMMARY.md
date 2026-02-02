---
phase: 22-hub-proxy-integration
plan: 22-04
subsystem: k8s
tags: [k8s, informer, pod-watcher, state-sync, golang]
requires: [22-01]
provides:
  - PodWatcher for real-time Pod state monitoring
  - StateChangeHandler interface for decoupled state management
  - Informer-based cache for efficient Pod lookups
affects: [22-05, 22-06]
tech-stack:
  added:
    - k8s.io/client-go informers
    - SharedInformerFactory with label selectors
  patterns:
    - Informer-based state synchronization
    - Level-driven event handling (not edge-driven)
    - Cache-first lookups to reduce API load
key-files:
  created:
    - internal/k8s/watcher.go
    - internal/k8s/watcher_test.go
  modified: []
decisions:
  - decision: Use SharedInformerFactory with label selector (worldland.io/gpu-rental=true)
    rationale: Efficiently watches only GPU rental Pods, reduces memory/network overhead
    alternatives: Watch all Pods and filter in code (wasteful)
  - decision: Level-driven state handling (check current Pod phase, not transitions)
    rationale: Resilient to missed events, correct after restarts/reconnects
    alternatives: Edge-driven transitions (fragile with missed events)
  - decision: Running+Ready condition required for OnPodRunning callback
    rationale: Ready probe ensures SSH service is actually available
    alternatives: Trigger on Running alone (may call before service ready)
  - decision: Graceful skip for Pods without session-id label
    rationale: Allows informer to coexist with other GPU workloads in cluster
    alternatives: Error/warn on missing label (noisy, restrictive)
duration: 309s
completed: 2026-02-02
---

# Phase 22 Plan 04: PodWatcher with Informer-Based State Sync Summary

Informer-based Pod state watcher for real-time K8s-to-Hub synchronization

## What Was Built

Created PodWatcher component that watches K8s Pod lifecycle events and triggers Hub session state updates through a decoupled StateChangeHandler interface.

### Key Components

1. **StateChangeHandler Interface** (`internal/k8s/watcher.go`)
   - `OnPodRunning(sessionID)` - Called when Pod becomes Ready (Running + readiness probe passed)
   - `OnPodFailed(sessionID, reason)` - Called when Pod fails (phase=Failed)
   - `OnPodSucceeded(sessionID)` - Called when Pod completes (phase=Succeeded)
   - `OnPodDeleted(sessionID)` - Called when Pod is deleted
   - Decouples PodWatcher from SessionManager (clean architecture)

2. **PodWatcher** (`internal/k8s/watcher.go`)
   - Uses SharedInformerFactory with label selector `worldland.io/gpu-rental=true`
   - Watches only GPU rental Pods (not all cluster Pods)
   - Cache-synced before processing events (WaitForCacheSync)
   - Level-driven event handling (resilient to missed events)
   - 30-second default resync period (configurable via WithResyncPeriod)

3. **Event Handling Logic**
   - **Pending:** Log only, no state change (wait for Running)
   - **Running + Ready:** Trigger `OnPodRunning` (readiness probe passed)
   - **Running + Not Ready:** Log only, wait for readiness
   - **Failed:** Trigger `OnPodFailed` with extracted reason
   - **Succeeded:** Trigger `OnPodSucceeded`
   - **Delete:** Trigger `OnPodDeleted` with tombstone handling

4. **Cache Utilities** (`internal/k8s/watcher.go`)
   - `IsCacheSynced()` - Health check / readiness probe support
   - `GetPodFromCache(namespace, name)` - Fast lookup without API call
   - `ListPodsFromCache()` - Startup reconciliation support
   - `WithResyncPeriod(period)` - Builder method for configuration

5. **Failure Reason Extraction** (`extractPodFailureReason`)
   - Priority: Pod.Status.Message > Pod.Status.Reason > ContainerStatus.Terminated > InitContainerStatus.Terminated
   - Returns "Unknown" if no reason found
   - Extracts both reason and message from terminated containers

6. **Comprehensive Tests** (`internal/k8s/watcher_test.go`)
   - Mock StateChangeHandler for isolated testing
   - Tests for all Pod phases (Running/Failed/Succeeded/Delete)
   - Running+Ready vs Running+NotReady distinction tested
   - Missing session-id label handled gracefully
   - Failure reason extraction tested with multiple sources

## Code Quality

- **Type Safety:** Full type checking with kubernetes.Interface
- **Error Handling:** All handler errors logged with structured logging (slog)
- **Structured Logging:** Pod name, namespace, sessionID, phase logged on every event
- **Graceful Degradation:** Missing session-id label skipped (debug log only)
- **Tombstone Handling:** Delete events handle DeletedFinalStateUnknown
- **Test Coverage:** 9 unit tests covering all event paths

## Technical Decisions

### 1. Informer vs Polling
**Chose:** SharedInformerFactory with label selector
**Why:** Real-time updates, cache-based lookups, watch API efficiency
**Tradeoff:** More complex than polling, requires cache sync handling

### 2. Level-Driven vs Edge-Driven
**Chose:** Level-driven (check current phase, not transitions)
**Why:** Resilient to missed events, correct state after restart
**Example:** If we miss Pending→Running event, next update still sees Running+Ready

### 3. Ready Condition Requirement
**Chose:** Running+Ready required for OnPodRunning
**Why:** Ensures SSH service is actually available (readiness probe = TCP:22 check)
**Tradeoff:** Slightly delayed state transition (waits for probe)

### 4. Label Selector in Informer
**Chose:** Filter at informer creation (WithTweakListOptions)
**Why:** Reduces memory/network usage, only watches relevant Pods
**Alternative:** Watch all Pods, filter in handler (wasteful)

## Integration Points

### Upstream (What This Depends On)
- **Plan 22-01:** LabelGPURental, LabelSessionID constants from `internal/k8s/types.go`
- **K8s Client:** kubernetes.Interface from ClientsetManager

### Downstream (What Depends On This)
- **Plan 22-05:** SessionManager will implement StateChangeHandler interface
- **Plan 22-06:** Hub main.go will wire PodWatcher to SessionManager, start goroutine

## Files Modified

### Created
```
internal/k8s/watcher.go         # 308 lines (PodWatcher + utilities)
internal/k8s/watcher_test.go    # 390 lines (9 unit tests + mock handler)
```

### Modified
None - fully additive implementation

## Performance Characteristics

- **Memory:** O(n) where n = number of GPU rental Pods (not all cluster Pods)
- **Network:** Watch connection (efficient K8s watch API), no polling
- **CPU:** Event-driven callbacks (no busy polling)
- **Startup:** Waits for cache sync before processing events (safe)

## Security Considerations

- **Label Filtering:** Only watches Pods with `worldland.io/gpu-rental=true` (scoped access)
- **No Credentials Exposure:** Does not log Pod secrets or environment variables
- **Graceful Unknown:** Missing session-id label skipped (no crash on unexpected Pods)

## Deviations from Plan

None - plan executed exactly as written.

## Testing Strategy

### Unit Tests (Implemented)
- Mock StateChangeHandler for callback verification
- All Pod phases tested (Pending/Running/Failed/Succeeded)
- Ready condition logic tested
- Missing label handling tested
- Failure reason extraction tested (6 scenarios)

### Integration Tests (Deferred to E2E)
- Full informer lifecycle with real K8s cluster
- Cache sync behavior under load
- Watch reconnection on network failure
- Multi-namespace Pod handling

Deferred because:
1. Requires real K8s cluster or kind/minikube setup
2. Unit tests cover all code paths with mocks
3. E2E tests will validate full Hub-K8s integration

## Next Phase Readiness

### Blockers
None

### Prerequisites for Next Plan (22-05)
1. SessionManager must implement StateChangeHandler interface
2. State transition logic: PENDING→RUNNING, RUNNING→FAILED, RUNNING→COMPLETED
3. Database updates on state changes

### Future Enhancements
1. Metrics/Prometheus integration (event counters, handler latency)
2. Circuit breaker for handler failures (avoid thundering herd on DB outage)
3. Event replay mechanism for cache resync reconciliation
4. Multi-cluster support (federated informers)

## Success Criteria Met

- [x] PodWatcher uses SharedInformerFactory with label selector
- [x] Label selector filters to worldland.io/gpu-rental=true pods only
- [x] Informer cache sync is awaited before handling events
- [x] Running+Ready triggers OnPodRunning callback
- [x] Failed/Succeeded triggers appropriate callback
- [x] Pod delete triggers OnPodDeleted callback
- [x] Missing session-id label is gracefully handled
- [x] Unit tests pass with mock StateChangeHandler

## Commits

| Commit | Type | Description |
|--------|------|-------------|
| 0e5968e | feat | Create PodWatcher with Informer pattern |
| 4fba1f0 | feat | Add PodWatcher cache utilities |
| d33ab90 | test | Add PodWatcher unit tests |

**Total Duration:** 309 seconds (~5 minutes)

---
*Generated: 2026-02-02T12:47:31Z*
*Phase: 22-hub-proxy-integration*
*Plan: 22-04*
