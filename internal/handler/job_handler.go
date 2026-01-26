// Package handler provides HTTP handlers for job operations.
package handler

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nubro999/worldland-gpu/internal/job"
	"github.com/nubro999/worldland-gpu/internal/provider"
)

// JobHandler handles GPU job HTTP requests.
type JobHandler struct {
	manager      *job.JobManager
	orchestrator *provider.Orchestrator
	nodeManager  *provider.NodeManager
}

// NewJobHandler creates a new JobHandler.
func NewJobHandler(manager *job.JobManager) *JobHandler {
	return &JobHandler{manager: manager}
}

// SetOrchestrator sets the orchestrator for provider validation.
func (h *JobHandler) SetOrchestrator(orchestrator *provider.Orchestrator) {
	h.orchestrator = orchestrator
}

// SetNodeManager sets the node manager for real-time GPU verification.
func (h *JobHandler) SetNodeManager(nm *provider.NodeManager) {
	h.nodeManager = nm
}

// CreateJobRequest is the request body for creating a GPU job.
type CreateJobRequest struct {
	ProviderID  string `json:"provider_id"` // 특정 Provider 지정 (선택적)
	GPUType     string `json:"gpu_type"`    // 원하는 GPU 타입 (예: "Tesla T4", "RTX 4090")
	JobName     string `json:"job_name"`
	GPUCount    int    `json:"gpu_count"`
	Image       string `json:"image"`
	CPUCores    string `json:"cpu_cores"`
	MemoryGB    string `json:"memory_gb"`
	StorageGB   string `json:"storage_gb"`
	SSHPassword string `json:"ssh_password" binding:"required"`
	Duration    int    `json:"duration_hours"`
}

// CreateJob creates a new GPU job.
// POST /api/v1/jobs
func (h *JobHandler) CreateJob(c *gin.Context) {
	// Get user ID from context (set by auth middleware)
	userID, exists := c.Get("userID")
	if !exists {
		userID = "anonymous" // For testing
	}

	var req CreateJobRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request",
			"details": err.Error(),
		})
		return
	}

	jobReq := &job.GPUJobRequest{
		UserID:      userID.(string),
		ProviderID:  req.ProviderID,
		JobName:     req.JobName,
		GPUCount:    req.GPUCount,
		Image:       req.Image,
		CPUCores:    req.CPUCores,
		MemoryGB:    req.MemoryGB,
		StorageGB:   req.StorageGB,
		SSHPassword: req.SSHPassword,
		Duration:    req.Duration,
	}

	// Provider 검증/검색
	var gpuModel string
	var pricePerHour float64
	var providerState *provider.ProviderState

	if h.orchestrator != nil {
		// 1. provider_id가 직접 지정된 경우
		if req.ProviderID != "" {
			ps, exists := h.orchestrator.GetProviderState(req.ProviderID)
			if !exists {
				c.JSON(http.StatusBadRequest, gin.H{
					"error":   "Provider not found",
					"details": "The specified provider_id does not exist",
				})
				return
			}
			providerState = ps
		} else if req.GPUType != "" {
			// 2. gpu_type으로 검색
			filter := &provider.SearchFilter{
				GPUModel: req.GPUType,
				Limit:    1,
			}
			providers, err := h.orchestrator.Search(c.Request.Context(), filter)
			if err != nil || len(providers) == 0 {
				c.JSON(http.StatusBadRequest, gin.H{
					"error":   "No provider found",
					"details": "No provider with GPU type: " + req.GPUType,
				})
				return
			}
			providerState = providers[0]
			jobReq.ProviderID = providerState.ProviderID
		}

		// Provider 검증 및 정보 설정
		if providerState != nil {
			// 상태 확인
			if providerState.Status != provider.StatusApproved && providerState.Status != provider.StatusJoined && providerState.Status != provider.StatusAvailable {
				c.JSON(http.StatusBadRequest, gin.H{
					"error":   "Provider not available",
					"details": "Provider status: " + string(providerState.Status),
				})
				return
			}
			// GPU 확인 - 실제 클러스터 상태 조회
			var availableGPU int
			var gpuSource string

			// 실제 클러스터에서 GPU 가용성 확인 (NodeManager가 있고 NodeName이 있는 경우)
			if h.nodeManager != nil && providerState.Spec.Hostname != "" {
				actualGPU, err := h.nodeManager.GetNodeAllocatableGPU(c.Request.Context(), providerState.Spec.Hostname)
				if err == nil {
					availableGPU = int(actualGPU)
					gpuSource = "cluster"
				} else {
					// 클러스터 조회 실패 시 fallback to in-memory state
					availableGPU = providerState.Capacity.AvailableGPUCount()
					gpuSource = "cache"
				}
			} else {
				// NodeManager 없거나 NodeName 없으면 in-memory state 사용
				availableGPU = providerState.Capacity.AvailableGPUCount()
				gpuSource = "cache"
			}

			if req.GPUCount > availableGPU {
				c.JSON(http.StatusBadRequest, gin.H{
					"error":     "Insufficient GPU",
					"message":   fmt.Sprintf("Requested: %d, Available: %d (source: %s)", req.GPUCount, availableGPU, gpuSource),
					"available": availableGPU,
					"source":    gpuSource,
				})
				return
			}

			// CPU 확인
			requestedCPU := 0
			if req.CPUCores != "" {
				fmt.Sscanf(req.CPUCores, "%d", &requestedCPU)
			}
			if requestedCPU == 0 {
				requestedCPU = 4 // 기본값
			}
			availableCPU := providerState.Capacity.GetAvailableCPUCores()
			if requestedCPU > availableCPU {
				c.JSON(http.StatusBadRequest, gin.H{
					"error":     "Insufficient CPU",
					"message":   fmt.Sprintf("Requested: %d cores, Available: %d cores", requestedCPU, availableCPU),
					"available": availableCPU,
				})
				return
			}

			// Memory 확인
			requestedMemoryGB := 0
			if req.MemoryGB != "" {
				// "16Gi" 또는 "16" 형식 처리
				memStr := req.MemoryGB
				memStr = strings.TrimSuffix(memStr, "Gi")
				memStr = strings.TrimSuffix(memStr, "G")
				fmt.Sscanf(memStr, "%d", &requestedMemoryGB)
			}
			if requestedMemoryGB == 0 {
				requestedMemoryGB = 16 // 기본값
			}
			availableMemoryGB := int(providerState.Capacity.GetAvailableMemoryMB() / 1024)
			if requestedMemoryGB > availableMemoryGB {
				c.JSON(http.StatusBadRequest, gin.H{
					"error":     "Insufficient Memory",
					"message":   fmt.Sprintf("Requested: %d GB, Available: %d GB", requestedMemoryGB, availableMemoryGB),
					"available": availableMemoryGB,
				})
				return
			}

			// Provider 노드 정보 설정
			jobReq.NodeHostname = providerState.Spec.Hostname
			jobReq.NodePublicIP = providerState.Spec.PublicIP // SSH 접속용 공인 IP

			// GPU 모델 및 가격 정보
			if len(providerState.Spec.GPUs) > 0 {
				gpuModel = providerState.Spec.GPUs[0].Name
			}
			pricePerHour = providerState.Capacity.GPUPricePerHour

			// jobReq에도 설정 (Pod Annotations에 저장용)
			jobReq.GPUModel = gpuModel
			jobReq.PricePerHour = pricePerHour

			// 리소스 체크 로깅
			slog.Info("Resource check passed",
				"provider_id", providerState.ProviderID,
				"hostname", providerState.Spec.Hostname,
				"gpu_type", gpuModel,
				"requested_gpu", req.GPUCount,
				"requested_cpu", req.CPUCores,
				"requested_memory", req.MemoryGB,
				"available_gpu", providerState.Capacity.AvailableGPUCount(),
				"available_cpu", providerState.Capacity.GetAvailableCPUCores(),
				"available_memory_mb", providerState.Capacity.GetAvailableMemoryMB(),
			)
		}
	}

	// === 리소스 할당 (Job 생성 전에 먼저 수행) ===
	var allocation *provider.ResourceAllocation
	if providerState != nil && h.orchestrator != nil {
		cpuCoresInt := 0
		if req.CPUCores != "" {
			fmt.Sscanf(req.CPUCores, "%d", &cpuCoresInt)
		}
		if cpuCoresInt == 0 {
			cpuCoresInt = 4 // 기본값
		}
		memoryMB := int64(0)
		if req.MemoryGB != "" {
			var memGB int
			memStr := strings.TrimSuffix(req.MemoryGB, "Gi")
			memStr = strings.TrimSuffix(memStr, "G")
			fmt.Sscanf(memStr, "%d", &memGB)
			memoryMB = int64(memGB) * 1024
		}
		if memoryMB == 0 {
			memoryMB = 16 * 1024 // 기본값 16GB
		}
		storageGB := int64(0)
		if req.StorageGB != "" {
			var storage int
			storageStr := strings.TrimSuffix(req.StorageGB, "Gi")
			storageStr = strings.TrimSuffix(storageStr, "G")
			fmt.Sscanf(storageStr, "%d", &storage)
			storageGB = int64(storage)
		}
		if storageGB == 0 {
			storageGB = 20 // 기본값 20GB
		}

		gpuCount := req.GPUCount
		if gpuCount <= 0 {
			gpuCount = 1
		}

		allocation = &provider.ResourceAllocation{
			JobID:      "", // Job 생성 후 설정
			ProviderID: providerState.ProviderID,
			GPUType:    gpuModel,
			GPUCount:   gpuCount,
			CPUCores:   cpuCoresInt,
			MemoryMB:   memoryMB,
			StorageGB:  storageGB,
		}

		// 리소스를 먼저 할당 (Job 생성 전)
		if err := h.orchestrator.AllocateResources(providerState.ProviderID, allocation); err != nil {
			c.JSON(http.StatusConflict, gin.H{
				"error":   "Resource allocation failed",
				"details": err.Error(),
			})
			return
		}
		slog.Info("Resources pre-allocated for job",
			"provider_id", providerState.ProviderID,
			"gpu_count", gpuCount,
			"cpu_cores", cpuCoresInt,
			"memory_mb", memoryMB,
		)
	}

	// === Job 생성 ===
	resp, err := h.manager.CreateGPUJob(c.Request.Context(), jobReq)
	if err != nil {
		// Job 생성 실패 시 리소스 롤백
		if allocation != nil && h.orchestrator != nil {
			if releaseErr := h.orchestrator.ReleaseResources(allocation.ProviderID, allocation); releaseErr != nil {
				slog.Error("Failed to rollback resource allocation",
					"provider_id", allocation.ProviderID,
					"error", releaseErr,
				)
			} else {
				slog.Info("Resource allocation rolled back due to job creation failure",
					"provider_id", allocation.ProviderID,
				)
			}
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to create job",
			"details": err.Error(),
		})
		return
	}

	// allocation에 JobID 설정 (추적용 로그)
	if allocation != nil {
		allocation.JobID = resp.JobID
		slog.Info("Job created with allocated resources",
			"job_id", resp.JobID,
			"provider_id", allocation.ProviderID,
		)
	}

	// Provider 정보 추가
	if providerState != nil {
		resp.ProviderID = providerState.ProviderID
	}
	resp.GPUModel = gpuModel
	resp.PricePerHour = pricePerHour

	c.JSON(http.StatusCreated, resp)
}

// GetJob returns the status of a GPU job.
// GET /api/v1/jobs/:id
func (h *JobHandler) GetJob(c *gin.Context) {
	jobID := c.Param("id")
	if jobID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "job_id is required"})
		return
	}

	userID, exists := c.Get("userID")
	if !exists {
		userID = "anonymous"
	}

	resp, err := h.manager.GetJobStatus(c.Request.Context(), jobID, userID.(string))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "Job not found",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// DeleteJob deletes a GPU job.
// DELETE /api/v1/jobs/:id
func (h *JobHandler) DeleteJob(c *gin.Context) {
	jobID := c.Param("id")
	if jobID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "job_id is required"})
		return
	}

	userID, exists := c.Get("userID")
	if !exists {
		userID = "anonymous"
	}

	// 삭제 전에 Job 정보 조회 (리소스 반환용) - 실제 할당된 리소스 정보 추출
	var allocation *provider.ResourceAllocation

	if h.orchestrator != nil {
		jobResp, err := h.manager.GetJobStatus(c.Request.Context(), jobID, userID.(string))
		if err == nil && jobResp.ProviderID != "" {
			// 실제 할당된 리소스 정보 파싱
			gpuCount := jobResp.GPUCount
			if gpuCount <= 0 {
				gpuCount = 1 // 최소 1 GPU
			}

			cpuCores := 0
			if jobResp.CPUCores != "" {
				cpuStr := strings.TrimSuffix(jobResp.CPUCores, "m") // "2000m" -> "2000"
				fmt.Sscanf(cpuStr, "%d", &cpuCores)
				if cpuCores >= 1000 {
					cpuCores = cpuCores / 1000 // millicores to cores
				}
			}

			memoryMB := int64(0)
			if jobResp.MemoryGB != "" {
				var memValue int
				memStr := jobResp.MemoryGB
				if strings.HasSuffix(memStr, "Gi") {
					memStr = strings.TrimSuffix(memStr, "Gi")
					fmt.Sscanf(memStr, "%d", &memValue)
					memoryMB = int64(memValue) * 1024
				} else if strings.HasSuffix(memStr, "Mi") {
					memStr = strings.TrimSuffix(memStr, "Mi")
					fmt.Sscanf(memStr, "%d", &memValue)
					memoryMB = int64(memValue)
				} else {
					fmt.Sscanf(memStr, "%d", &memValue)
					memoryMB = int64(memValue) * 1024 // assume GB
				}
			}

			allocation = &provider.ResourceAllocation{
				JobID:      jobID,
				ProviderID: jobResp.ProviderID,
				GPUCount:   gpuCount,
				CPUCores:   cpuCores,
				MemoryMB:   memoryMB,
			}

			slog.Info("Job resource info retrieved for deletion",
				"job_id", jobID,
				"provider_id", jobResp.ProviderID,
				"gpu_count", gpuCount,
				"cpu_cores", cpuCores,
				"memory_mb", memoryMB,
			)
		}
	}

	err := h.manager.DeleteJob(c.Request.Context(), jobID, userID.(string))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to delete job",
			"details": err.Error(),
		})
		return
	}

	// 리소스 반환 (정확한 값으로)
	if allocation != nil && h.orchestrator != nil {
		if err := h.orchestrator.ReleaseResources(allocation.ProviderID, allocation); err != nil {
			slog.Error("Failed to release resources",
				"job_id", jobID,
				"provider_id", allocation.ProviderID,
				"error", err,
			)
			c.JSON(http.StatusOK, gin.H{
				"message": "Job deleted, but resource release failed",
				"job_id":  jobID,
				"warning": err.Error(),
			})
			return
		}
		slog.Info("Resources released successfully",
			"job_id", jobID,
			"provider_id", allocation.ProviderID,
			"gpu_count", allocation.GPUCount,
			"cpu_cores", allocation.CPUCores,
			"memory_mb", allocation.MemoryMB,
		)
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Job deleted successfully",
		"job_id":  jobID,
	})
}

// ListJobs lists all GPU jobs for the current user.
// GET /api/v1/jobs
func (h *JobHandler) ListJobs(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		userID = "anonymous"
	}

	jobs, err := h.manager.ListUserJobs(c.Request.Context(), userID.(string))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to list jobs",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"jobs":  jobs,
		"count": len(jobs),
	})
}
