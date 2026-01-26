// Package provider provides the orchestrator for handling provider registrations.
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/nubro999/worldland-gpu/internal/messaging"
	"github.com/redis/go-redis/v9"
)

// Orchestrator handles provider registration and management.
type Orchestrator struct {
	nodeManager   *NodeManager
	miningManager *MiningPodManager
	redisClient   *redis.Client
	producer      *messaging.Producer
	consumer      *messaging.Consumer
	repo          ProviderRepository // DB 저장소

	// Provider registry (in-memory cache)
	providers   map[string]*ProviderState
	providersMu sync.RWMutex

	// Configuration
	masterIP   string
	masterPort int

	// Shutdown
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// ProviderState tracks the current state of a provider.
type ProviderState struct {
	ProviderID    string
	WalletAddr    string
	NodeName      string
	Status        RegistrationStatus
	Spec          SystemSpec       //시스템의 정보를 담고 있는 구조체
	Capacity      ProviderCapacity //provider.ProviderCapacity는 시스템의 정보를 바탕으로 provider.ProviderCapacity를 생성하는 함수
	LastHeartbeat time.Time
	RegisteredAt  time.Time
	JoinedAt      time.Time
}

// OrchestratorConfig holds configuration for the Orchestrator.
type OrchestratorConfig struct {
	MasterIP   string
	MasterPort int
}

// NewOrchestrator creates a new Orchestrator.
// repo는 nil일 수 있음 (DB 연결 실패 시에도 동작 가능)
func NewOrchestrator(nodeManager *NodeManager, redisClient *redis.Client, repo ProviderRepository, cfg *OrchestratorConfig) (*Orchestrator, error) {
	producer := messaging.NewProducer(redisClient)

	consumer, err := messaging.NewConsumer(redisClient, &messaging.ConsumerConfig{
		Stream:        StreamNames.Registration,
		Group:         "orchestrator-group",
		Consumer:      "orchestrator-1",
		BlockDuration: 5 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create consumer: %w", err)
	}

	// MiningPodManager 생성 (nodeManager의 clientset 사용)
	var miningManager *MiningPodManager
	if nodeManager != nil && nodeManager.clientset != nil {
		miningManager = NewMiningPodManager(nodeManager.clientset)
	}

	return &Orchestrator{
		nodeManager:   nodeManager,
		miningManager: miningManager,
		redisClient:   redisClient,
		producer:      producer,
		consumer:      consumer,
		repo:          repo,
		providers:     make(map[string]*ProviderState),
		masterIP:      cfg.MasterIP,
		masterPort:    cfg.MasterPort,
		stopCh:        make(chan struct{}),
	}, nil
}

// Start starts the orchestrator workers.
func (o *Orchestrator) Start(ctx context.Context) {
	// DB에서 기존 Provider 로드
	if err := o.loadProvidersFromDB(ctx); err != nil {
		slog.Warn("Failed to load providers from DB", "error", err)
	}

	// 기존 Mining Pod 상태 복구
	if err := o.RecoverMiningStates(ctx); err != nil {
		slog.Warn("Failed to recover mining states", "error", err)
	}

	// GPU Job 할당 상태 복구 (서버 재시작 시 K8s 실제 상태와 동기화)
	if err := o.RecoverJobAllocations(ctx); err != nil {
		slog.Warn("Failed to recover job allocations", "error", err)
	}

	o.wg.Add(5)

	// Registration handler
	go o.registrationWorker(ctx)

	// Heartbeat monitor
	go o.heartbeatMonitor(ctx)

	// Mining pod monitor
	go o.miningMonitor(ctx)

	// GPU Job pod watcher (실시간 Pod 삭제/변경 감지)
	go o.podWatcher(ctx)

	// Job 만료 모니터 (만료된 Job 자동 삭제)
	go o.jobExpirationMonitor(ctx)

	slog.Info("Orchestrator started")
}

// loadProvidersFromDB loads existing providers from database into memory.
func (o *Orchestrator) loadProvidersFromDB(ctx context.Context) error {
	if o.repo == nil {
		return nil // DB 없이 동작
	}

	providers, err := o.repo.ListAll(ctx)
	if err != nil {
		return err
	}

	o.providersMu.Lock()
	defer o.providersMu.Unlock()

	for _, p := range providers {
		o.providers[p.ProviderID] = p
	}

	slog.Info("Loaded providers from DB", "count", len(providers))
	return nil
}

// Stop stops the orchestrator.
func (o *Orchestrator) Stop() {
	close(o.stopCh)
	o.wg.Wait()
	slog.Info("Orchestrator stopped")
}

// registrationWorker processes provider registration messages.
func (o *Orchestrator) registrationWorker(ctx context.Context) {
	defer o.wg.Done() //종료 시 카운터 감소

	for {
		select {
		case <-o.stopCh:
			return
		case <-ctx.Done():
			return
		default:
		}

		messages, err := o.consumer.ReadMessages(ctx, 10)
		if err != nil {
			slog.Error("Failed to read registration messages", "error", err)
			time.Sleep(1 * time.Second)
			continue
		}

		for _, msg := range messages {
			if err := o.handleRegistration(ctx, msg); err != nil {
				slog.Error("Failed to handle registration", "error", err, "messageID", msg.ID)
			}
			// Acknowledge message
			if err := o.consumer.Ack(ctx, msg.ID); err != nil {
				slog.Error("Failed to ack message", "error", err, "messageID", msg.ID)
			}
		}
	}
}

// handleRegistration processes a single registration request.
func (o *Orchestrator) handleRegistration(ctx context.Context, msg messaging.Message) error {
	var req RegistrationRequest
	if err := msg.Unmarshal(&req); err != nil {
		return fmt.Errorf("failed to unmarshal registration request: %w", err)
	}

	slog.Info("Processing provider registration",
		"providerID", req.ProviderID,
		"hostname", req.Spec.Hostname,
		"gpuCount", req.Spec.TotalGPUs,
	)

	// Check if provider already registered
	o.providersMu.RLock()
	existingProvider, exists := o.providers[req.ProviderID]
	o.providersMu.RUnlock()

	var response RegistrationResponse

	if exists && existingProvider.Status == StatusJoined {
		// Provider already joined, update capacity
		response = RegistrationResponse{
			Success:  true,
			Status:   StatusJoined,
			Message:  "Provider already registered and joined",
			NodeName: existingProvider.NodeName,
		}
	} else {
		// New provider - try to generate join token
		joinToken, caHash, err := o.generateJoinToken(ctx)
		if err != nil {
			slog.Warn("Failed to generate join token (node may already be joined)", "error", err)

			// 토큰 생성 실패해도 Provider 저장 (이미 Join된 노드일 수 있음)
			providerState := &ProviderState{
				ProviderID:    req.ProviderID,
				WalletAddr:    req.WalletAddr,
				Status:        StatusApproved, // 일단 승인 상태로 저장
				Spec:          req.Spec,
				Capacity:      req.Capacity,
				RegisteredAt:  time.Now(),
				LastHeartbeat: time.Now(),
			}

			o.providersMu.Lock()
			o.providers[req.ProviderID] = providerState
			o.providersMu.Unlock()

			// DB에 저장
			if o.repo != nil {
				if err := o.repo.Create(ctx, providerState); err != nil {
					slog.Error("Failed to save provider to DB", "error", err, "providerID", req.ProviderID)
				}
			}

			response = RegistrationResponse{
				Success: true,
				Status:  StatusApproved,
				Message: "Registration approved (join token unavailable - node may already be in cluster)",
			}
		} else {
			joinCommand := fmt.Sprintf(
				"kubeadm join %s:%d --token %s --discovery-token-ca-cert-hash %s",
				o.masterIP, o.masterPort, joinToken, caHash,
			)

			response = RegistrationResponse{
				Success:     true,
				Status:      StatusApproved,
				Message:     "Registration approved. Please execute the join command.",
				JoinToken:   joinToken,
				JoinCommand: joinCommand,
				MasterIP:    o.masterIP,
				MasterPort:  o.masterPort,
				CAHash:      caHash,
			}

			// Store provider state
			providerState := &ProviderState{
				ProviderID:   req.ProviderID,
				WalletAddr:   req.WalletAddr,
				Status:       StatusApproved,
				Spec:         req.Spec,
				Capacity:     req.Capacity,
				RegisteredAt: time.Now(),
			}

			o.providersMu.Lock()
			o.providers[req.ProviderID] = providerState
			o.providersMu.Unlock()

			// DB에 저장
			if o.repo != nil {
				if err := o.repo.Create(ctx, providerState); err != nil {
					slog.Error("Failed to save provider to DB", "error", err, "providerID", req.ProviderID)
				}
			}
		}
	}

	// Send response via Redis (provider agent will be listening)
	responseStream := fmt.Sprintf("provider:response:%s", req.ProviderID)
	if _, err := o.producer.Publish(ctx, responseStream, response); err != nil {
		slog.Error("Failed to publish response", "error", err)
	}

	// Deploy Mining Pod if MiningConfig is provided
	if req.MiningConfig != nil && response.Success && o.miningManager != nil {
		go func() {
			deployCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			slog.Info("Deploying mining pod for provider",
				"providerID", req.ProviderID,
				"gpuCount", req.MiningConfig.GPUCount,
				"wallet", req.MiningConfig.WalletAddress,
			)

			if err := o.DeployMiningForProvider(deployCtx, req.ProviderID, req.MiningConfig); err != nil {
				slog.Error("Failed to deploy mining pod",
					"providerID", req.ProviderID,
					"error", err,
				)
			} else {
				slog.Info("Mining pod deployed successfully", "providerID", req.ProviderID)
			}
		}()
	}

	return nil
}

// generateJoinToken generates a kubeadm join token.
func (o *Orchestrator) generateJoinToken(ctx context.Context) (token string, caHash string, err error) {
	// Execute kubeadm token create
	cmd := exec.CommandContext(ctx, "kubeadm", "token", "create", "--print-join-command")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("failed to create token: %w, output: %s", err, string(output))
	}

	// Parse the join command to extract token and hash
	joinCmd := strings.TrimSpace(string(output))
	parts := strings.Fields(joinCmd)

	for i, part := range parts {
		if part == "--token" && i+1 < len(parts) {
			token = parts[i+1]
		}
		if part == "--discovery-token-ca-cert-hash" && i+1 < len(parts) {
			caHash = parts[i+1]
		}
	}

	if token == "" || caHash == "" {
		return "", "", fmt.Errorf("failed to parse join command: %s", joinCmd)
	}

	return token, caHash, nil
}

// heartbeatMonitor monitors provider heartbeats and updates node status.
func (o *Orchestrator) heartbeatMonitor(ctx context.Context) {
	defer o.wg.Done()

	heartbeatConsumer, err := messaging.NewConsumer(o.redisClient, &messaging.ConsumerConfig{
		Stream:        StreamNames.Heartbeat,
		Group:         "orchestrator-group",
		Consumer:      "orchestrator-1",
		BlockDuration: 5 * time.Second,
	})
	if err != nil {
		slog.Error("Failed to create heartbeat consumer", "error", err)
		return
	}

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-o.stopCh:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			o.checkStaleProviders(ctx)
		default:
		}

		messages, err := heartbeatConsumer.ReadMessages(ctx, 10)
		if err != nil {
			slog.Error("Failed to read heartbeat messages", "error", err)
			time.Sleep(1 * time.Second)
			continue
		}

		for _, msg := range messages {
			o.handleHeartbeat(ctx, msg)
			_ = heartbeatConsumer.Ack(ctx, msg.ID)
		}
	}
}

// handleHeartbeat processes a heartbeat message.
func (o *Orchestrator) handleHeartbeat(ctx context.Context, msg messaging.Message) {
	var hb HeartbeatMessage
	if err := msg.Unmarshal(&hb); err != nil {
		slog.Error("Failed to unmarshal heartbeat", "error", err)
		return
	}

	o.providersMu.Lock()
	if provider, exists := o.providers[hb.ProviderID]; exists {
		provider.LastHeartbeat = time.Now()
		provider.Status = hb.Status
		provider.NodeName = hb.NodeName
	}
	o.providersMu.Unlock()

	// DB에 heartbeat 업데이트
	if o.repo != nil {
		if err := o.repo.UpdateHeartbeat(ctx, hb.ProviderID, hb.Status); err != nil {
			slog.Debug("Failed to update heartbeat in DB", "error", err, "providerID", hb.ProviderID)
		}
	}

	// Update node labels based on status
	if hb.NodeName != "" {
		labels := map[string]string{
			"worldland.io/active-jobs":    fmt.Sprintf("%d", hb.ActiveJobs),
			"worldland.io/last-heartbeat": time.Now().UTC().Format(time.RFC3339),
		}
		if err := o.nodeManager.SetNodeLabels(ctx, hb.NodeName, labels); err != nil {
			slog.Error("Failed to update node labels", "error", err, "node", hb.NodeName)
		}

		// Check and update GPU taint
		if err := o.nodeManager.CheckAndUpdateGPUTaint(ctx, hb.NodeName); err != nil {
			slog.Error("Failed to update GPU taint", "error", err, "node", hb.NodeName)
		}
	}
}

// checkStaleProviders checks for providers that haven't sent heartbeats.
func (o *Orchestrator) checkStaleProviders(ctx context.Context) {
	o.providersMu.Lock()
	defer o.providersMu.Unlock()

	staleThreshold := time.Now().Add(-2 * time.Minute)

	for id, provider := range o.providers {
		if provider.Status == StatusJoined && provider.LastHeartbeat.Before(staleThreshold) {
			slog.Warn("Provider heartbeat stale, marking as offline",
				"providerID", id,
				"lastHeartbeat", provider.LastHeartbeat,
			)
			provider.Status = StatusOffline

			// Update node labels
			if provider.NodeName != "" {
				labels := map[string]string{
					"worldland.io/rental-status": "offline",
				}
				if err := o.nodeManager.SetNodeLabels(ctx, provider.NodeName, labels); err != nil {
					slog.Error("Failed to mark node as offline", "error", err)
				}
			}
		}
	}
}

// OnNodeJoined should be called when a node joins the cluster.
func (o *Orchestrator) OnNodeJoined(ctx context.Context, nodeName string, providerID string) error {
	o.providersMu.Lock()
	provider, exists := o.providers[providerID]
	if !exists {
		o.providersMu.Unlock()
		return fmt.Errorf("provider not found: %s", providerID)
	}
	provider.Status = StatusJoined
	provider.NodeName = nodeName
	provider.JoinedAt = time.Now()
	gpuModel := ""
	if len(provider.Spec.GPUs) > 0 {
		gpuModel = provider.Spec.GPUs[0].Name
	}
	o.providersMu.Unlock()

	// Mark node as available for rental
	if err := o.nodeManager.MarkNodeAsRentalAvailable(ctx, nodeName, providerID, gpuModel); err != nil {
		return fmt.Errorf("failed to mark node as available: %w", err)
	}

	slog.Info("Provider node joined cluster",
		"providerID", providerID,
		"nodeName", nodeName,
	)

	return nil
}

// GetProviderState returns the current state of a provider.
func (o *Orchestrator) GetProviderState(providerID string) (*ProviderState, bool) {
	o.providersMu.RLock()
	defer o.providersMu.RUnlock()
	provider, exists := o.providers[providerID]
	return provider, exists
}

// ListProviders returns all registered providers.
func (o *Orchestrator) ListProviders() []*ProviderState {
	o.providersMu.RLock()
	defer o.providersMu.RUnlock()

	providers := make([]*ProviderState, 0, len(o.providers))
	for _, p := range o.providers {
		providers = append(providers, p)
	}
	return providers
}

// GetAllProvidersJSON returns all providers as JSON (for API responses).
func (o *Orchestrator) GetAllProvidersJSON() ([]byte, error) {
	providers := o.ListProviders()
	return json.Marshal(providers)
}

// Search searches providers by filter criteria.
// Uses DB if available, otherwise falls back to in-memory search.
func (o *Orchestrator) Search(ctx context.Context, filter *SearchFilter) ([]*ProviderState, error) {
	// DB가 있으면 DB에서 검색
	if o.repo != nil {
		return o.repo.Search(ctx, filter)
	}

	// DB 없으면 인메모리에서 검색
	o.providersMu.RLock()
	defer o.providersMu.RUnlock()

	var results []*ProviderState
	for _, p := range o.providers {
		// 상태 필터
		if filter.Status != "" && string(p.Status) != filter.Status {
			continue
		}
		// GPU 모델 필터 (부분 매칭)
		if filter.GPUModel != "" && len(p.Spec.GPUs) > 0 {
			if !strings.Contains(strings.ToLower(p.Spec.GPUs[0].Name), strings.ToLower(filter.GPUModel)) {
				continue
			}
		}
		// 최소 메모리 필터
		if filter.MinMemoryMB > 0 && p.Spec.TotalMemoryMB < filter.MinMemoryMB {
			continue
		}
		// 최소 CPU 코어 필터
		if filter.MinCPUCores > 0 && p.Spec.CPUCores < filter.MinCPUCores {
			continue
		}
		// 최소 디스크 필터
		if filter.MinDiskGB > 0 && p.Spec.AvailableDiskGB < filter.MinDiskGB {
			continue
		}
		// 최대 가격 필터
		if filter.MaxPricePerHour > 0 && p.Capacity.GPUPricePerHour > filter.MaxPricePerHour {
			continue
		}

		results = append(results, p)
	}

	return results, nil
}

// AllocateResources allocates resources from a provider for a job.
func (o *Orchestrator) AllocateResources(providerID string, allocation *ResourceAllocation) error {
	o.providersMu.Lock()
	defer o.providersMu.Unlock()

	provider, exists := o.providers[providerID]
	if !exists {
		return fmt.Errorf("provider not found: %s", providerID)
	}

	// GPU 타입 결정
	gpuType := allocation.GPUType
	if gpuType == "" {
		if len(provider.Spec.GPUs) > 0 {
			gpuType = provider.Spec.GPUs[0].Name
		} else {
			gpuType = "default"
		}
	}

	// AvailableGPUs 맵 초기화
	if provider.Capacity.AvailableGPUs == nil {
		provider.Capacity.AvailableGPUs = make(map[string]int)
		// 레거시 필드에서 초기화
		if provider.Capacity.GPUCount > 0 {
			provider.Capacity.AvailableGPUs[gpuType] = provider.Capacity.GPUCount
		} else if provider.Spec.TotalGPUs > 0 {
			provider.Capacity.AvailableGPUs[gpuType] = provider.Spec.TotalGPUs
		}
	}

	// InUseGPUs 맵 초기화
	if provider.Capacity.InUseGPUs == nil {
		provider.Capacity.InUseGPUs = make(map[string]int)
	}

	// GPU 가용성 확인 및 차감
	if allocation.GPUCount > 0 {
		available := provider.Capacity.AvailableGPUs[gpuType]
		if available < allocation.GPUCount {
			return fmt.Errorf("insufficient GPU: requested %d, available %d", allocation.GPUCount, available)
		}
		provider.Capacity.AvailableGPUs[gpuType] -= allocation.GPUCount
		provider.Capacity.InUseGPUs[gpuType] += allocation.GPUCount
	}

	// CPU 할당
	if allocation.CPUCores > 0 {
		// TotalCPUCores 초기화 (레거시 필드 또는 Spec에서)
		if provider.Capacity.TotalCPUCores == 0 {
			if provider.Capacity.CPUCores > 0 {
				provider.Capacity.TotalCPUCores = provider.Capacity.CPUCores
			} else if provider.Spec.CPUCores > 0 {
				provider.Capacity.TotalCPUCores = provider.Spec.CPUCores
			}
		}
		// 가용 CPU 초기화 (필요시)
		if provider.Capacity.AvailableCPUCores == 0 && provider.Capacity.TotalCPUCores > 0 {
			provider.Capacity.AvailableCPUCores = provider.Capacity.TotalCPUCores - provider.Capacity.InUseCPUCores - provider.Capacity.MiningCPUCores
		}
		if provider.Capacity.AvailableCPUCores < allocation.CPUCores {
			// GPU 롤백
			provider.Capacity.AvailableGPUs[gpuType] += allocation.GPUCount
			provider.Capacity.InUseGPUs[gpuType] -= allocation.GPUCount
			return fmt.Errorf("insufficient CPU: requested %d, available %d", allocation.CPUCores, provider.Capacity.AvailableCPUCores)
		}

		provider.Capacity.AvailableCPUCores -= allocation.CPUCores
		provider.Capacity.InUseCPUCores += allocation.CPUCores
	}

	// Memory 할당
	if allocation.MemoryMB > 0 {
		// TotalMemoryMB 초기화 (레거시 필드 또는 Spec에서)
		if provider.Capacity.TotalMemoryMB == 0 {
			if provider.Capacity.MemoryMB > 0 {
				provider.Capacity.TotalMemoryMB = int64(provider.Capacity.MemoryMB)
			} else if provider.Spec.TotalMemoryMB > 0 {
				provider.Capacity.TotalMemoryMB = int64(provider.Spec.TotalMemoryMB)
			}
		}
		// 가용 메모리 초기화 (필요시)
		if provider.Capacity.AvailableMemoryMB == 0 && provider.Capacity.TotalMemoryMB > 0 {
			provider.Capacity.AvailableMemoryMB = provider.Capacity.TotalMemoryMB - provider.Capacity.InUseMemoryMB - int64(provider.Capacity.MiningMemoryMB)
		}
		if provider.Capacity.AvailableMemoryMB < allocation.MemoryMB {
			// GPU, CPU 롤백
			provider.Capacity.AvailableGPUs[gpuType] += allocation.GPUCount
			provider.Capacity.InUseGPUs[gpuType] -= allocation.GPUCount
			provider.Capacity.AvailableCPUCores += allocation.CPUCores
			provider.Capacity.InUseCPUCores -= allocation.CPUCores
			return fmt.Errorf("insufficient Memory: requested %d MB, available %d MB", allocation.MemoryMB, provider.Capacity.AvailableMemoryMB)
		}
		provider.Capacity.AvailableMemoryMB -= allocation.MemoryMB
		provider.Capacity.InUseMemoryMB += allocation.MemoryMB
	}

	slog.Info("Resources allocated",
		"provider_id", providerID,
		"job_id", allocation.JobID,
		"gpu_type", gpuType,
		"gpu_count", allocation.GPUCount,
		"cpu_cores", allocation.CPUCores,
		"memory_mb", allocation.MemoryMB,
		"available_gpu", provider.Capacity.AvailableGPUs[gpuType],
	)

	return nil
}

// ReleaseResources returns allocated resources back to the provider.
func (o *Orchestrator) ReleaseResources(providerID string, allocation *ResourceAllocation) error {
	// GPU 타입 결정
	gpuType := allocation.GPUType
	if gpuType == "" {
		gpuType = "default"
	}

	// releaseJobResources 호출 (통일된 리소스 반환 로직)
	o.releaseJobResources(providerID, gpuType, allocation.GPUCount, allocation.CPUCores, allocation.MemoryMB)

	slog.Info("Resources released via ReleaseResources",
		"provider_id", providerID,
		"job_id", allocation.JobID,
		"gpu_count", allocation.GPUCount,
	)

	return nil
}

// ================== Mining Management ==================

// AllocateMiningGPU reserves additional GPUs for mining from the available pool.
// Returns an error if not enough GPUs are available (Option 1: reject + wait).
func (o *Orchestrator) AllocateMiningGPU(ctx context.Context, providerID string, gpuType string, count int) error {
	o.providersMu.Lock()
	defer o.providersMu.Unlock()

	provider, exists := o.providers[providerID]
	if !exists {
		return fmt.Errorf("provider not found: %s", providerID)
	}

	// 가용 GPU 확인
	available := 0
	if provider.Capacity.AvailableGPUs != nil {
		available = provider.Capacity.AvailableGPUs[gpuType]
	}

	if available < count {
		return fmt.Errorf("insufficient available GPUs for mining: requested %d, available %d. Wait for rental jobs to complete", count, available)
	}

	// Initialize maps if nil
	if provider.Capacity.AvailableGPUs == nil {
		provider.Capacity.AvailableGPUs = make(map[string]int)
	}
	if provider.Capacity.MiningGPUs == nil {
		provider.Capacity.MiningGPUs = make(map[string]int)
	}

	// 가용량에서 차감
	provider.Capacity.AvailableGPUs[gpuType] -= count
	// 채굴용에 추가
	provider.Capacity.MiningGPUs[gpuType] += count

	slog.Info("Mining GPU allocated",
		"providerID", providerID,
		"gpuType", gpuType,
		"allocated", count,
		"miningTotal", provider.Capacity.MiningGPUs[gpuType],
		"available", provider.Capacity.AvailableGPUs[gpuType],
	)

	// Mining Pod 업데이트 (GPU 수 변경)
	if o.miningManager != nil {
		miningConfig := &MiningConfig{
			GPUCount: provider.Capacity.MiningGPUCount(),
			CPUCores: provider.Capacity.MiningCPUCores,
			MemoryMB: provider.Capacity.MiningMemoryMB,
		}
		// 비동기로 Pod 업데이트 (에러는 로그만)
		go func() {
			if err := o.miningManager.UpdateMiningPodGPU(ctx, providerID, miningConfig, provider.NodeName); err != nil {
				slog.Error("Failed to update mining pod", "error", err)
			}
		}()
	}

	return nil
}

// ReleaseMiningGPU releases GPUs from mining back to the available pool.
func (o *Orchestrator) ReleaseMiningGPU(ctx context.Context, providerID string, gpuType string, count int) error {
	o.providersMu.Lock()
	defer o.providersMu.Unlock()

	provider, exists := o.providers[providerID]
	if !exists {
		return fmt.Errorf("provider not found: %s", providerID)
	}

	// 채굴용 GPU 확인
	miningGPUs := 0
	if provider.Capacity.MiningGPUs != nil {
		miningGPUs = provider.Capacity.MiningGPUs[gpuType]
	}

	if miningGPUs < count {
		return fmt.Errorf("cannot release more GPUs than allocated for mining: mining %d, requested %d", miningGPUs, count)
	}

	// Initialize maps if nil
	if provider.Capacity.AvailableGPUs == nil {
		provider.Capacity.AvailableGPUs = make(map[string]int)
	}

	// 채굴용에서 차감
	provider.Capacity.MiningGPUs[gpuType] -= count
	// 가용량에 추가
	provider.Capacity.AvailableGPUs[gpuType] += count

	slog.Info("Mining GPU released",
		"providerID", providerID,
		"gpuType", gpuType,
		"released", count,
		"miningTotal", provider.Capacity.MiningGPUs[gpuType],
		"available", provider.Capacity.AvailableGPUs[gpuType],
	)

	// Mining Pod 업데이트 (GPU 수 변경)
	if o.miningManager != nil {
		miningConfig := &MiningConfig{
			GPUCount: provider.Capacity.MiningGPUCount(),
			CPUCores: provider.Capacity.MiningCPUCores,
			MemoryMB: provider.Capacity.MiningMemoryMB,
		}
		go func() {
			if err := o.miningManager.UpdateMiningPodGPU(ctx, providerID, miningConfig, provider.NodeName); err != nil {
				slog.Error("Failed to update mining pod", "error", err)
			}
		}()
	}

	return nil
}

// GetMiningStatus returns the mining status for a provider.
func (o *Orchestrator) GetMiningStatus(ctx context.Context, providerID string) (map[string]interface{}, error) {
	o.providersMu.RLock()
	provider, exists := o.providers[providerID]
	o.providersMu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("provider not found: %s", providerID)
	}

	// Get pod status
	podStatus := "unknown"
	if o.miningManager != nil {
		status, err := o.miningManager.GetMiningPodStatus(ctx, providerID)
		if err == nil {
			podStatus = status
		}
	}

	return map[string]interface{}{
		"provider_id":         providerID,
		"mining_status":       podStatus,
		"mining_pod_name":     provider.Capacity.MiningPodName,
		"mining_gpus":         provider.Capacity.MiningGPUs,
		"mining_gpu_count":    provider.Capacity.MiningGPUCount(),
		"mining_cpu_cores":    provider.Capacity.MiningCPUCores,
		"mining_memory_mb":    provider.Capacity.MiningMemoryMB,
		"available_gpus":      provider.Capacity.AvailableGPUs,
		"available_gpu_count": provider.Capacity.AvailableGPUCount(),
	}, nil
}

// DeployMiningForProvider deploys a mining pod for a provider.
func (o *Orchestrator) DeployMiningForProvider(ctx context.Context, providerID string, config *MiningConfig) error {
	o.providersMu.Lock()
	defer o.providersMu.Unlock()

	provider, exists := o.providers[providerID]
	if !exists {
		return fmt.Errorf("provider not found: %s", providerID)
	}

	if o.miningManager == nil {
		return fmt.Errorf("mining manager not available")
	}

	// GPU 타입 추정 (첫 번째 GPU 타입 사용)
	var gpuType string
	for t := range provider.Capacity.TotalGPUs {
		gpuType = t
		break
	}
	if gpuType == "" && len(provider.Spec.GPUs) > 0 {
		gpuType = provider.Spec.GPUs[0].Name
	}

	// 가용 GPU 확인 - AvailableGPUs가 nil이면 TotalGPUs에서 초기화
	if provider.Capacity.AvailableGPUs == nil {
		provider.Capacity.AvailableGPUs = make(map[string]int)
		// TotalGPUs에서 복사
		if provider.Capacity.TotalGPUs != nil {
			for t, c := range provider.Capacity.TotalGPUs {
				provider.Capacity.AvailableGPUs[t] = c
			}
		} else if gpuType != "" {
			// Spec에서 total_gpus 사용
			provider.Capacity.AvailableGPUs[gpuType] = provider.Spec.TotalGPUs
		}
	}

	available := 0
	if gpuType != "" {
		available = provider.Capacity.AvailableGPUs[gpuType]
	}
	// 아직도 0이면 Spec.TotalGPUs 사용
	if available == 0 && provider.Spec.TotalGPUs > 0 {
		available = provider.Spec.TotalGPUs
		provider.Capacity.AvailableGPUs[gpuType] = available
	}

	if available < config.GPUCount {
		return fmt.Errorf("insufficient GPUs: requested %d, available %d", config.GPUCount, available)
	}

	// Mining Pod 배포
	podName, err := o.miningManager.DeployMiningPod(ctx, providerID, config, provider.NodeName)
	if err != nil {
		return fmt.Errorf("failed to deploy mining pod: %w", err)
	}

	// Capacity 업데이트
	if provider.Capacity.MiningGPUs == nil {
		provider.Capacity.MiningGPUs = make(map[string]int)
	}
	provider.Capacity.MiningGPUs[gpuType] = config.GPUCount
	provider.Capacity.AvailableGPUs[gpuType] -= config.GPUCount
	provider.Capacity.MiningCPUCores = config.CPUCores
	provider.Capacity.MiningMemoryMB = config.MemoryMB
	provider.Capacity.MiningPodName = podName
	provider.Capacity.MiningStatus = "pending"

	slog.Info("Mining pod deployed",
		"providerID", providerID,
		"podName", podName,
		"gpuCount", config.GPUCount,
	)

	return nil
}

// StopMiningForProvider stops the mining pod and releases resources.
func (o *Orchestrator) StopMiningForProvider(ctx context.Context, providerID string) error {
	o.providersMu.Lock()
	defer o.providersMu.Unlock()

	provider, exists := o.providers[providerID]
	if !exists {
		return fmt.Errorf("provider not found: %s", providerID)
	}

	if o.miningManager == nil {
		return fmt.Errorf("mining manager not available")
	}

	// Delete mining pod
	if err := o.miningManager.DeleteMiningPod(ctx, providerID); err != nil {
		return fmt.Errorf("failed to delete mining pod: %w", err)
	}

	// Return GPUs to available pool
	for gpuType, count := range provider.Capacity.MiningGPUs {
		if provider.Capacity.AvailableGPUs == nil {
			provider.Capacity.AvailableGPUs = make(map[string]int)
		}
		provider.Capacity.AvailableGPUs[gpuType] += count
	}

	// Clear mining fields
	provider.Capacity.MiningGPUs = nil
	provider.Capacity.MiningCPUCores = 0
	provider.Capacity.MiningMemoryMB = 0
	provider.Capacity.MiningPodName = ""
	provider.Capacity.MiningStatus = "stopped"

	slog.Info("Mining stopped for provider", "providerID", providerID)

	return nil
}

// ================== Mining Monitoring ==================

// miningMonitor periodically checks mining pod status and handles failures.
func (o *Orchestrator) miningMonitor(ctx context.Context) {
	defer o.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	slog.Info("Mining monitor started")

	for {
		select {
		case <-ctx.Done():
			slog.Info("Mining monitor stopping (context cancelled)")
			return
		case <-o.stopCh:
			slog.Info("Mining monitor stopping")
			return
		case <-ticker.C:
			o.syncMiningPodStates(ctx)
		}
	}
}

// syncMiningPodStates synchronizes mining pod states with actual K8s pod status.
func (o *Orchestrator) syncMiningPodStates(ctx context.Context) {
	if o.miningManager == nil {
		return
	}

	o.providersMu.Lock()
	defer o.providersMu.Unlock()

	for providerID, provider := range o.providers {
		// Skip providers without mining
		if provider.Capacity.MiningPodName == "" {
			continue
		}

		// Check pod status
		status, err := o.miningManager.GetMiningPodStatus(ctx, providerID)
		if err != nil {
			slog.Warn("Failed to get mining pod status",
				"providerID", providerID,
				"error", err,
			)
			continue
		}

		oldStatus := provider.Capacity.MiningStatus
		provider.Capacity.MiningStatus = status

		// Handle status changes
		if oldStatus != status {
			slog.Info("Mining pod status changed",
				"providerID", providerID,
				"oldStatus", oldStatus,
				"newStatus", status,
			)
		}

		// Handle failed/stopped pods - return GPUs to available pool
		if (status == "failed" || status == "stopped") && provider.Capacity.MiningGPUCount() > 0 {
			slog.Warn("Mining pod not running, returning GPUs to available pool",
				"providerID", providerID,
				"status", status,
				"gpusToReturn", provider.Capacity.MiningGPUCount(),
			)

			// Return all mining GPUs to available
			for gpuType, count := range provider.Capacity.MiningGPUs {
				if provider.Capacity.AvailableGPUs == nil {
					provider.Capacity.AvailableGPUs = make(map[string]int)
				}
				provider.Capacity.AvailableGPUs[gpuType] += count
			}

			// Clear mining allocation
			provider.Capacity.MiningGPUs = nil
			provider.Capacity.MiningCPUCores = 0
			provider.Capacity.MiningMemoryMB = 0
		}
	}
}

// RecoverMiningStates recovers mining states after restart by checking existing pods.
func (o *Orchestrator) RecoverMiningStates(ctx context.Context) error {
	if o.miningManager == nil {
		return nil
	}

	slog.Info("Recovering mining states...")

	o.providersMu.Lock()
	defer o.providersMu.Unlock()

	for providerID, provider := range o.providers {
		// Check if mining pod exists for this provider
		exists, err := o.miningManager.MiningPodExists(ctx, providerID)
		if err != nil {
			slog.Warn("Failed to check mining pod existence",
				"providerID", providerID,
				"error", err,
			)
			continue
		}

		if exists {
			status, _ := o.miningManager.GetMiningPodStatus(ctx, providerID)
			provider.Capacity.MiningPodName = fmt.Sprintf("mining-%s", providerID)
			provider.Capacity.MiningStatus = status

			slog.Info("Recovered mining pod state",
				"providerID", providerID,
				"status", status,
			)
		}
	}

	return nil
}

// GetMiningMetrics returns aggregated mining metrics across all providers.
func (o *Orchestrator) GetMiningMetrics() map[string]interface{} {
	o.providersMu.RLock()
	defer o.providersMu.RUnlock()

	var (
		totalMiningProviders int
		runningMiningPods    int
		totalMiningGPUs      int
		totalAvailableGPUs   int
	)

	for _, provider := range o.providers {
		if provider.Capacity.MiningPodName != "" {
			totalMiningProviders++

			if provider.Capacity.MiningStatus == "running" {
				runningMiningPods++
			}

			totalMiningGPUs += provider.Capacity.MiningGPUCount()
		}

		totalAvailableGPUs += provider.Capacity.AvailableGPUCount()
	}

	return map[string]interface{}{
		"total_mining_providers": totalMiningProviders,
		"running_mining_pods":    runningMiningPods,
		"total_mining_gpus":      totalMiningGPUs,
		"total_available_gpus":   totalAvailableGPUs,
		"total_providers":        len(o.providers),
	}
}

// ================== Job Allocation Recovery ==================

// RecoverJobAllocations recovers GPU job allocations from K8s on startup.
// This ensures that after server restart, the in-memory state matches actual K8s pod state.
func (o *Orchestrator) RecoverJobAllocations(ctx context.Context) error {
	if o.nodeManager == nil {
		slog.Warn("NodeManager not available, skipping job allocation recovery")
		return nil
	}

	slog.Info("Recovering job allocations from K8s...")

	// List all GPU job pods
	pods, err := o.nodeManager.ListGPUJobPods(ctx)
	if err != nil {
		return fmt.Errorf("failed to list GPU job pods: %w", err)
	}

	if len(pods) == 0 {
		slog.Info("No existing GPU jobs found")
		return nil
	}

	o.providersMu.Lock()
	defer o.providersMu.Unlock()

	// Track recovered allocations per provider
	recoveredCount := 0
	providerAllocations := make(map[string][]GPUJobPodInfo)

	for _, pod := range pods {
		if pod.ProviderID == "" {
			slog.Debug("Skipping pod without provider ID", "job_id", pod.JobID)
			continue
		}
		providerAllocations[pod.ProviderID] = append(providerAllocations[pod.ProviderID], pod)
	}

	// Apply allocations to each provider
	for providerID, jobs := range providerAllocations {
		provider, exists := o.providers[providerID]
		if !exists {
			slog.Warn("Provider not found for job recovery",
				"provider_id", providerID,
				"job_count", len(jobs),
			)
			continue
		}

		// Sum up resources used by jobs
		totalGPU := 0
		totalCPU := 0
		totalMemoryMB := int64(0)

		for _, job := range jobs {
			totalGPU += job.GPUCount
			totalCPU += job.CPUCores
			totalMemoryMB += job.MemoryMB
			recoveredCount++

			slog.Debug("Recovered job allocation",
				"job_id", job.JobID,
				"provider_id", providerID,
				"gpu", job.GPUCount,
				"cpu", job.CPUCores,
				"memory_mb", job.MemoryMB,
			)
		}

		// Initialize InUse maps if nil
		if provider.Capacity.InUseGPUs == nil {
			provider.Capacity.InUseGPUs = make(map[string]int)
		}

		// Determine GPU type (use first GPU from spec or default)
		gpuType := "default"
		if len(provider.Spec.GPUs) > 0 {
			gpuType = provider.Spec.GPUs[0].Name
		}

		// Update in-use resources
		provider.Capacity.InUseGPUs[gpuType] = totalGPU
		provider.Capacity.InUseCPUCores = totalCPU
		provider.Capacity.InUseMemoryMB = totalMemoryMB

		// Recalculate available resources
		// Available = Total - InUse - Mining
		if provider.Capacity.AvailableGPUs == nil {
			provider.Capacity.AvailableGPUs = make(map[string]int)
		}

		// Get total GPU for this type
		totalForType := 0
		if provider.Capacity.TotalGPUs != nil {
			totalForType = provider.Capacity.TotalGPUs[gpuType]
		}
		if totalForType == 0 {
			totalForType = provider.Spec.TotalGPUs
		}

		// Mining GPUs for this type
		miningForType := 0
		if provider.Capacity.MiningGPUs != nil {
			miningForType = provider.Capacity.MiningGPUs[gpuType]
		}

		// Available = Total - InUse - Mining
		available := totalForType - totalGPU - miningForType
		if available < 0 {
			available = 0
		}
		provider.Capacity.AvailableGPUs[gpuType] = available

		// CPU and Memory available
		if provider.Capacity.TotalCPUCores > 0 {
			provider.Capacity.AvailableCPUCores = provider.Capacity.TotalCPUCores - totalCPU - provider.Capacity.MiningCPUCores
		}
		if provider.Capacity.TotalMemoryMB > 0 {
			provider.Capacity.AvailableMemoryMB = provider.Capacity.TotalMemoryMB - totalMemoryMB - provider.Capacity.MiningMemoryMB
		}

		slog.Info("Provider resource state recovered",
			"provider_id", providerID,
			"jobs_count", len(jobs),
			"in_use_gpu", totalGPU,
			"in_use_cpu", totalCPU,
			"in_use_memory_mb", totalMemoryMB,
			"available_gpu", available,
		)
	}

	slog.Info("Job allocation recovery completed",
		"total_jobs", recoveredCount,
		"providers_affected", len(providerAllocations),
	)

	return nil
}

// ================== Pod Watcher ==================

// podWatcher watches GPU Job pods and handles deletion/failure events.
func (o *Orchestrator) podWatcher(ctx context.Context) {
	defer o.wg.Done()

	if o.nodeManager == nil {
		slog.Warn("NodeManager not available, pod watcher disabled")
		return
	}

	slog.Info("GPU Job pod watcher started")

	for {
		select {
		case <-ctx.Done():
			slog.Info("Pod watcher stopping (context cancelled)")
			return
		case <-o.stopCh:
			slog.Info("Pod watcher stopping")
			return
		default:
		}

		// Start watching
		eventCh, err := o.nodeManager.WatchGPUJobPods(ctx)
		if err != nil {
			slog.Error("Failed to start pod watcher, retrying in 10s", "error", err)
			select {
			case <-ctx.Done():
				return
			case <-o.stopCh:
				return
			case <-time.After(10 * time.Second):
				continue
			}
		}

		// Process events
		for event := range eventCh {
			o.handlePodEvent(ctx, event)
		}

		// Watcher closed, restart after delay
		slog.Warn("Pod watcher connection closed, restarting in 5s")
		select {
		case <-ctx.Done():
			return
		case <-o.stopCh:
			return
		case <-time.After(5 * time.Second):
			continue
		}
	}
}

// handlePodEvent handles a pod watch event.
func (o *Orchestrator) handlePodEvent(ctx context.Context, event PodWatchEvent) {
	info := event.PodInfo

	switch event.Type {
	case "DELETED":
		// Pod 삭제됨 → 리소스 반환
		if info.ProviderID == "" {
			return
		}

		slog.Info("Pod deleted, releasing resources",
			"job_id", info.JobID,
			"provider_id", info.ProviderID,
			"gpu_count", info.GPUCount,
			"cpu_cores", info.CPUCores,
			"memory_mb", info.MemoryMB,
		)

		o.releaseJobResources(info.ProviderID, info.GPUModel, info.GPUCount, info.CPUCores, info.MemoryMB)

	case "MODIFIED":
		// Pod 상태 변경 확인 (Failed, Succeeded → 리소스 반환)
		if event.RawPod == nil {
			return
		}

		phase := event.RawPod.Status.Phase
		if phase == "Failed" || phase == "Succeeded" {
			if info.ProviderID == "" {
				return
			}

			slog.Info("Pod terminated, releasing resources",
				"job_id", info.JobID,
				"provider_id", info.ProviderID,
				"phase", phase,
				"gpu_count", info.GPUCount,
			)

			o.releaseJobResources(info.ProviderID, info.GPUModel, info.GPUCount, info.CPUCores, info.MemoryMB)
		}

	case "ADDED":
		// Pod 생성됨 - 로깅만 (리소스는 CreateJob에서 이미 할당됨)
		slog.Debug("Pod created (watched)",
			"job_id", info.JobID,
			"provider_id", info.ProviderID,
			"node", info.NodeName,
		)
	}
}

// releaseJobResources releases resources back to the provider.
func (o *Orchestrator) releaseJobResources(providerID string, gpuType string, gpuCount, cpuCores int, memoryMB int64) {
	o.providersMu.Lock()
	defer o.providersMu.Unlock()

	provider, exists := o.providers[providerID]
	if !exists {
		slog.Warn("Provider not found for resource release", "provider_id", providerID)
		return
	}

	// GPU 타입이 비어있으면 Provider spec에서 추출
	if gpuType == "" {
		if len(provider.Spec.GPUs) > 0 {
			gpuType = provider.Spec.GPUs[0].Name
		} else {
			gpuType = "default"
		}
	}

	// Release GPU
	if provider.Capacity.AvailableGPUs == nil {
		provider.Capacity.AvailableGPUs = make(map[string]int)
	}
	provider.Capacity.AvailableGPUs[gpuType] += gpuCount

	// Update InUse tracking
	if provider.Capacity.InUseGPUs != nil {
		provider.Capacity.InUseGPUs[gpuType] -= gpuCount
		if provider.Capacity.InUseGPUs[gpuType] < 0 {
			provider.Capacity.InUseGPUs[gpuType] = 0
		}
	}

	// Release CPU
	provider.Capacity.AvailableCPUCores += cpuCores
	provider.Capacity.InUseCPUCores -= cpuCores
	if provider.Capacity.InUseCPUCores < 0 {
		provider.Capacity.InUseCPUCores = 0
	}

	// Release Memory
	provider.Capacity.AvailableMemoryMB += memoryMB
	provider.Capacity.InUseMemoryMB -= memoryMB
	if provider.Capacity.InUseMemoryMB < 0 {
		provider.Capacity.InUseMemoryMB = 0
	}

	slog.Info("Resources released by pod watcher",
		"provider_id", providerID,
		"gpu_released", gpuCount,
		"cpu_released", cpuCores,
		"memory_released_mb", memoryMB,
		"available_gpu", provider.Capacity.AvailableGPUs[gpuType],
	)
}

// ================== Job Expiration Monitor ==================

// jobExpirationMonitor periodically checks for expired jobs and cleans them up.
func (o *Orchestrator) jobExpirationMonitor(ctx context.Context) {
	defer o.wg.Done()

	if o.nodeManager == nil {
		slog.Warn("NodeManager not available, job expiration monitor disabled")
		return
	}

	slog.Info("Job expiration monitor started")

	// Check interval: 1 minute
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	// Run initial check
	o.cleanupExpiredAndFailedJobs(ctx)

	for {
		select {
		case <-ctx.Done():
			slog.Info("Job expiration monitor stopping (context cancelled)")
			return
		case <-o.stopCh:
			slog.Info("Job expiration monitor stopping")
			return
		case <-ticker.C:
			o.cleanupExpiredAndFailedJobs(ctx)
		}
	}
}

// cleanupExpiredAndFailedJobs finds and deletes expired or failed jobs.
func (o *Orchestrator) cleanupExpiredAndFailedJobs(ctx context.Context) {
	pods, err := o.nodeManager.ListAllGPUJobPods(ctx)
	if err != nil {
		slog.Error("Failed to list GPU job pods for cleanup", "error", err)
		return
	}

	if len(pods) == 0 {
		return
	}

	now := time.Now()
	var expiredCount, failedCount int

	for _, pod := range pods {
		shouldDelete := false
		reason := ""

		// Check for expired jobs
		if !pod.ExpiresAt.IsZero() && now.After(pod.ExpiresAt) {
			shouldDelete = true
			reason = "expired"
			expiredCount++
		}

		// Check for failed/succeeded pods
		if pod.Status == "Failed" || pod.Status == "Succeeded" {
			shouldDelete = true
			reason = pod.Status
			failedCount++
		}

		if shouldDelete {
			slog.Info("Cleaning up job",
				"job_id", pod.JobID,
				"reason", reason,
				"provider_id", pod.ProviderID,
				"namespace", pod.Namespace,
			)

			// Release resources first
			if pod.ProviderID != "" && (pod.GPUCount > 0 || pod.CPUCores > 0 || pod.MemoryMB > 0) {
				o.releaseJobResources(pod.ProviderID, pod.GPUModel, pod.GPUCount, pod.CPUCores, pod.MemoryMB)
			}

			// Delete the job
			if err := o.nodeManager.DeleteGPUJob(ctx, pod.JobID, pod.Namespace); err != nil {
				slog.Error("Failed to delete expired/failed job",
					"job_id", pod.JobID,
					"error", err,
				)
			} else {
				slog.Info("Job cleaned up successfully",
					"job_id", pod.JobID,
					"reason", reason,
				)
			}
		}
	}

	if expiredCount > 0 || failedCount > 0 {
		slog.Info("Job cleanup completed",
			"expired_count", expiredCount,
			"failed_count", failedCount,
		)
	}
}
