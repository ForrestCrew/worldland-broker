---
phase: 24-base-image-selection
plan: 03
subsystem: session-management
tags: [docker-image, session-creation, k8s-integration, rental-flow]
dependency-graph:
  requires: ["24-01", "24-02"]
  provides: ["Image selection wired through rental flow"]
  affects: ["future-image-presets", "k8s-pod-creation"]
tech-stack:
  added: []
  patterns:
    - "Optional dependency injection with builder methods"
    - "Image resolution with fallback chain"
key-files:
  created: []
  modified:
    - worldland-hub/internal/sessions/manager.go
    - worldland-hub/internal/adapters/postgres/rental_repo.go
    - worldland-hub/internal/adapters/http/rental_handler.go
    - worldland-hub/internal/sessions/confirmation_worker.go
decisions:
  - id: IMG-FLOW-01
    title: "Image resolution in SessionManager"
    choice: "Resolve image at session creation time"
    rationale: "Validates upfront, stores resolved docker_image string"
  - id: IMG-FLOW-02
    title: "Image parameter flow"
    choice: "Empty string defaults to domain.DefaultImage"
    rationale: "Backward compatible, UUID resolves preset, custom URL validated"
metrics:
  duration: ~15 minutes
  completed: 2026-02-03
---

# Phase 24 Plan 03: Image Selection Flow Summary

Image selection wired from HTTP API through SessionManager to K8s Pod and Node API.

## Objective Achieved

Users can now specify a base image when creating rental sessions:
- Preset ID (UUID): Resolved via ImageRepository to docker_image
- Custom URL: Validated by ImageValidator
- Empty: Uses domain.DefaultImage ("nvidia/cuda:12.1-runtime-ubuntu22.04")

## Commits

| Commit | Description |
|--------|-------------|
| 92f45f3 | Extend SessionManager.CreateSession with dockerImage parameter |
| 8bc98e3 | Add docker_image persistence to rental session repository |
| 207080a | Add image selection to HTTP rental handler |
| 450ddb4 | Wire session.DockerImage to K8s Pod and Node API |

## Key Changes

### Task 1: SessionManager.CreateSession
- Added `dockerImage string` parameter to CreateSession
- Added `resolveDockerImage` helper with fallback chain:
  1. Empty -> domain.DefaultImage
  2. UUID -> ImageRepository.GetByID
  3. Custom URL -> ImageValidator.ValidateFormat
- Added `WithImageRepository` and `WithImageValidator` builder methods
- Added `ErrInvalidImage` and `ErrImageNotFound` error types

### Task 2: Repository Persistence
- Updated INSERT to include `docker_image` column
- Updated all SELECT queries to include `docker_image`
- Updated `scanSession` and `collectSessions` to scan DockerImage field

### Task 3: HTTP Handler
- Added `Image` field to CreateSessionRequest (optional)
- Added `WithImageRepository` builder method
- Added error handling for ErrInvalidImage and ErrImageNotFound (Korean messages)
- Added `ListImages` handler for GET /api/v1/images endpoint
- Added `ImageInfo` and `ListImagesResponse` types
- Support for category filter query parameter

### Task 4: ConfirmationWorker
- Extract `containerImage` from session.DockerImage with fallback to defaultImage
- Pass `containerImage` to GPUJobSpec.Image for K8s Pod
- Pass `containerImage` to Node API StartRentalRequest.Image
- Added image field to logging

## API Changes

### POST /api/v1/rentals (CreateSession)
New optional field:
```json
{
  "nodeId": "required",
  "pricePerSecond": "required",
  "image": "optional - UUID for preset or docker image URL"
}
```

### GET /api/v1/images (ListImages)
New endpoint:
```json
{
  "images": [
    {
      "id": "uuid",
      "name": "PyTorch 2.6 CUDA 12.6",
      "dockerImage": "pytorch/pytorch:2.6.0-cuda12.6-cudnn9-devel",
      "category": "pytorch",
      "gpuRequired": true,
      "description": "..."
    }
  ],
  "defaultImage": "nvidia/cuda:12.1-runtime-ubuntu22.04"
}
```

Query parameters:
- `category` (optional): Filter by category (pytorch, tensorflow, cuda)

## Deviations from Plan

None - plan executed exactly as written.

## Success Criteria Verification

- [x] CreateSession handler accepts optional image parameter
- [x] Preset IDs resolved to docker_image via ImageRepository
- [x] Custom image URLs validated by ImageValidator
- [x] Session stores docker_image in database
- [x] ConfirmationWorker uses session.DockerImage for K8s Pod
- [x] GET /api/v1/images endpoint returns preset list
- [x] All existing tests pass

## Files Modified

| File | Changes |
|------|---------|
| internal/sessions/manager.go | +76 lines (CreateSession params, resolveDockerImage, builder methods, error types) |
| internal/adapters/postgres/rental_repo.go | +16 lines (docker_image in queries and scans) |
| internal/adapters/http/rental_handler.go | +93 lines (Image field, error handling, ListImages handler) |
| internal/sessions/confirmation_worker.go | +12 lines (containerImage extraction and usage) |
| internal/sessions/manager_test.go | +1 line (empty string param) |
| test/integration/rental_flow_test.go | +5 instances (empty string param) |
| test/integration/session_flow_test.go | +6 instances (empty string param) |

## Next Phase Readiness

Phase 24 (Base Image Selection) is now complete:
- 24-01: Database schema and domain model
- 24-02: ImageValidator for format validation
- 24-03: Image selection wired through rental flow (this plan)

The image selection feature is fully functional. Future enhancements could include:
- Admin API for managing preset images
- Image pull verification before session creation
- Per-provider image allowlists
