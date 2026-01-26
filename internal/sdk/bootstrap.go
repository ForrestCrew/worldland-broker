package sdk

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nubro999/worldland-gpu/internal/agent"
	"github.com/nubro999/worldland-gpu/internal/messaging"
	"github.com/nubro999/worldland-gpu/internal/provider"
)

// Bootstrap handles the Kubernetes node bootstrap process.
type Bootstrap struct {
	config     *Config
	logger     *slog.Logger
	spec       *provider.SystemSpec
	providerID string
	nodeName   string
}

// NewBootstrap creates a new Bootstrap instance.
func NewBootstrap(config *Config) *Bootstrap {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	return &Bootstrap{
		config: config,
		logger: logger,
	}
}

// Run executes the full bootstrap process.
func (b *Bootstrap) Run(ctx context.Context) error {
	fmt.Println("\n[5/6] Joining Kubernetes cluster...")

	// Step 1: Scan system
	if err := b.scanSystem(); err != nil {
		return fmt.Errorf("system scan failed: %w", err)
	}

	// Step 2: Generate or use provided provider ID
	b.generateProviderID()

	// Step 3: Join Kubernetes cluster
	if err := b.joinCluster(ctx); err != nil {
		return fmt.Errorf("cluster join failed: %w", err)
	}

	// Step 4: Label the node
	if err := b.labelNode(ctx); err != nil {
		b.logger.Warn("Failed to label node", "error", err)
		// Non-fatal, continue
	}

	fmt.Printf("  ✓ Successfully joined cluster as node '%s'\n", b.nodeName)

	// Step 5: Register with orchestrator
	fmt.Println("\n[6/6] Registering as Worldland provider...")
	if err := b.registerProvider(ctx); err != nil {
		return fmt.Errorf("provider registration failed: %w", err)
	}

	return nil
}

func (b *Bootstrap) scanSystem() error {
	fmt.Println("  Scanning system hardware...")

	scanner := agent.NewSystemScanner()
	spec, err := scanner.Scan()
	if err != nil {
		return err
	}

	b.spec = spec

	fmt.Printf("  ✓ GPU: %d detected\n", spec.TotalGPUs)
	for _, gpu := range spec.GPUs {
		fmt.Printf("    - %s (%d MB)\n", gpu.Name, gpu.MemoryMB)
	}
	fmt.Printf("  ✓ CPU: %d cores\n", spec.CPUCores)
	fmt.Printf("  ✓ Memory: %d MB\n", spec.TotalMemoryMB)

	return nil
}

func (b *Bootstrap) generateProviderID() {
	if b.config.ProviderID != "" {
		b.providerID = b.config.ProviderID
	} else {
		b.providerID = "provider-" + uuid.New().String()[:8]
	}
	fmt.Printf("  ✓ Provider ID: %s\n", b.providerID)
}

func (b *Bootstrap) joinCluster(ctx context.Context) error {
	// Check if already joined
	if b.isNodeJoined() {
		b.nodeName = b.spec.Hostname
		fmt.Println("  ✓ Already joined to a cluster")
		return nil
	}

	// Build join command from token
	joinCmd := b.buildJoinCommand()

	fmt.Println("  Executing kubeadm join...")

	if b.config.Verbose {
		fmt.Printf("  Command: %s\n", joinCmd)
	}

	cmd := exec.CommandContext(ctx, "bash", "-c", joinCmd)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kubeadm join failed: %w", err)
	}

	b.nodeName = b.spec.Hostname
	return nil
}

func (b *Bootstrap) isNodeJoined() bool {
	// Check if kubelet is running and connected
	cmd := exec.Command("kubectl", "get", "nodes", "-o", "name")
	output, err := cmd.Output()
	if err != nil {
		return false
	}

	return strings.Contains(string(output), b.spec.Hostname)
}

func (b *Bootstrap) buildJoinCommand() string {
	// Parse token which should be in format: token.caCertHash@masterIP:masterPort
	// Or if a full join command is provided, use it directly
	token := b.config.Token

	if strings.HasPrefix(token, "kubeadm join") {
		return token
	}

	// Extract master host (remove protocol and port)
	masterHost := strings.TrimPrefix(b.config.MasterURL, "https://")
	masterHost = strings.TrimPrefix(masterHost, "http://")

	// Remove port if present (e.g., "15.164.220.132:8080" -> "15.164.220.132")
	if idx := strings.LastIndex(masterHost, ":"); idx > 0 {
		masterHost = masterHost[:idx]
	}
	// Remove trailing slash
	masterHost = strings.TrimSuffix(masterHost, "/")

	// Build join command
	// Assuming token format: bootstrapToken::caCertHash
	parts := strings.Split(token, "::")
	if len(parts) == 2 {
		return fmt.Sprintf("kubeadm join %s:6443 --token %s --discovery-token-ca-cert-hash sha256:%s",
			masterHost, parts[0], parts[1])
	}

	// Simple token format - assume master will provide full join command via Redis
	return fmt.Sprintf("kubeadm join %s:6443 --token %s --discovery-token-unsafe-skip-ca-verification",
		masterHost, token)
}

func (b *Bootstrap) labelNode(ctx context.Context) error {
	labels := map[string]string{
		"worldland.io/provider-id":         b.providerID,
		"worldland.io/rental-type":         "gpu",
		"worldland.io/blockchain-provider": "true",
	}

	// Add GPU model label
	if len(b.spec.GPUs) > 0 {
		gpuModel := strings.ReplaceAll(b.spec.GPUs[0].Name, " ", "-")
		labels["worldland.io/gpu-model"] = gpuModel
	}

	for key, value := range labels {
		cmd := exec.CommandContext(ctx, "kubectl", "label", "node", b.nodeName,
			fmt.Sprintf("%s=%s", key, value), "--overwrite")
		if err := cmd.Run(); err != nil {
			b.logger.Warn("Failed to apply label", "key", key, "error", err)
		}
	}

	return nil
}

func (b *Bootstrap) registerProvider(ctx context.Context) error {
	// Parse Redis address
	redisHost, redisPort := parseRedisAddr(b.config.RedisAddr)

	// Connect to Redis
	redisClient, err := messaging.GetClient(&messaging.Config{
		Host: redisHost,
		Port: redisPort,
	})
	if err != nil {
		return fmt.Errorf("failed to connect to Redis: %w", err)
	}

	// Prepare capacity
	capacity := b.prepareCapacity()

	// Create registration request
	req := provider.RegistrationRequest{
		ProviderID:   b.providerID,
		WalletAddr:   b.config.WalletAddr,
		Spec:         *b.spec,
		Capacity:     capacity,
		AgentVersion: "2.0.0-sdk",
		Timestamp:    time.Now(),
	}

	// Add mining configuration
	if b.config.EnableMining && b.config.WalletAddr != "" {
		// Detect public IP for NAT
		publicIP, err := DetectPublicIP(ctx)
		if err != nil {
			b.logger.Warn("Failed to detect public IP", "error", err)
		} else {
			fmt.Printf("  ✓ Public IP detected: %s\n", publicIP)
		}

		req.MiningConfig = &provider.MiningConfig{
			Image:         b.config.MiningImage,
			GPUCount:      b.config.MiningGPUCount,
			CPUCores:      b.config.MiningCPUCores,
			MemoryMB:      b.config.MiningMemoryMB,
			WalletAddress: b.config.WalletAddr,
			Bootnodes:     b.config.Bootnodes,
			NetworkID:     b.config.NetworkID,
			NodeDataDir:   b.config.NodeDataDir,
			P2PPort:       b.config.P2PPort,
			RPCEnabled:    b.config.RPCEnabled,
			RPCPort:       b.config.RPCPort,
			PublicIP:      publicIP, // Auto-detected public IP for NAT
		}
	}

	// Send registration
	producer := messaging.NewProducer(redisClient)
	msgID, err := producer.Publish(ctx, provider.StreamNames.Registration, req)
	if err != nil {
		return fmt.Errorf("failed to send registration: %w", err)
	}

	fmt.Printf("  ✓ Registration sent (ID: %s)\n", msgID)

	// Wait for response
	responseStream := fmt.Sprintf("provider:response:%s", b.providerID)
	responseConsumer, err := messaging.NewConsumer(redisClient, &messaging.ConsumerConfig{
		Stream:        responseStream,
		Group:         "sdk-group",
		Consumer:      "sdk-1",
		BlockDuration: 30 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("failed to create response consumer: %w", err)
	}

	fmt.Println("  Waiting for orchestrator response...")

	// Wait with timeout
	waitCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	var response provider.RegistrationResponse
	for {
		messages, err := responseConsumer.ReadMessages(waitCtx, 1)
		if err != nil {
			return fmt.Errorf("failed to read response: %w", err)
		}

		if len(messages) > 0 {
			if err := messages[0].Unmarshal(&response); err != nil {
				return fmt.Errorf("failed to unmarshal response: %w", err)
			}
			_ = responseConsumer.Ack(ctx, messages[0].ID)
			break
		}

		select {
		case <-waitCtx.Done():
			return fmt.Errorf("timeout waiting for orchestrator response")
		default:
		}
	}

	if !response.Success {
		return fmt.Errorf("registration rejected: %s", response.Message)
	}

	fmt.Printf("  ✓ Provider registered: %s\n", b.providerID)

	if response.NodeName != "" {
		b.nodeName = response.NodeName
	}

	return nil
}

func (b *Bootstrap) prepareCapacity() provider.ProviderCapacity {
	capacity := provider.ProviderCapacity{
		GPUCount:        b.spec.TotalGPUs,
		CPUCores:        int(float64(b.spec.CPUCores) * 0.8),
		MemoryMB:        int64(float64(b.spec.TotalMemoryMB) * 0.8),
		GPUPricePerHour: 0.5,
		CPUPricePerHour: 0.01,
	}

	// Build GPU type maps
	gpuTypes := make(map[string]int)
	for _, gpu := range b.spec.GPUs {
		gpuTypes[gpu.Name]++
	}

	capacity.TotalGPUs = gpuTypes
	capacity.AvailableGPUs = make(map[string]int)
	for gpuType, count := range gpuTypes {
		// Reserve mining GPUs
		available := count
		if b.config.EnableMining && available > 0 {
			miningReserve := b.config.MiningGPUCount
			if miningReserve > available {
				miningReserve = available
			}
			available -= miningReserve
		}
		capacity.AvailableGPUs[gpuType] = available
	}

	capacity.TotalCPUCores = b.spec.CPUCores
	capacity.TotalMemoryMB = b.spec.TotalMemoryMB
	capacity.AvailableCPUCores = capacity.CPUCores
	capacity.AvailableMemoryMB = capacity.MemoryMB

	return capacity
}

// GetProviderID returns the provider ID.
func (b *Bootstrap) GetProviderID() string {
	return b.providerID
}

// GetNodeName returns the node name.
func (b *Bootstrap) GetNodeName() string {
	return b.nodeName
}

// GetSpec returns the system spec.
func (b *Bootstrap) GetSpec() *provider.SystemSpec {
	return b.spec
}

func parseRedisAddr(addr string) (string, int) {
	host := "localhost"
	port := 6379

	if idx := strings.LastIndex(addr, ":"); idx > 0 {
		host = addr[:idx]
		fmt.Sscanf(addr[idx+1:], "%d", &port)
	}

	return host, port
}
