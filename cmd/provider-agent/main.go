// Provider Agent - runs on provider nodes to register with the cluster.
//
// This agent:
// 1. Scans system hardware (CPU, GPU, Memory)
// 2. Sends registration to Orchestrator via Redis Streams
// 3. Receives join command and executes kubeadm join
// 4. Sends periodic heartbeats
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/nubro999/worldland-gpu/internal/agent"     //시스템스캔
	"github.com/nubro999/worldland-gpu/internal/messaging" //redis연결
	"github.com/nubro999/worldland-gpu/internal/provider"  //프로바이더
)

var (
	redisAddr    = flag.String("redis", "localhost:6379", "Redis server address") // 플래그이름, 기본값, 설명
	providerID   = flag.String("provider-id", "", "Provider ID (defaults to generated UUID)")
	walletAddr   = flag.String("wallet", "", "WorldLand wallet address")
	gpuCount     = flag.Int("gpu-count", 0, "Number of GPUs to share (0 = all)")
	cpuCores     = flag.Int("cpu-cores", 0, "Number of CPU cores to share (0 = auto)")
	memoryMB     = flag.Int64("memory-mb", 0, "Memory to share in MB (0 = auto)")
	autoJoin     = flag.Bool("auto-join", false, "Automatically execute kubeadm join")
	heartbeatSec = flag.Int("heartbeat", 30, "Heartbeat interval in seconds")

	// Mining flags
	enableMining   = flag.Bool("enable-mining", true, "Enable Worldland mining on registration")
	miningGPUCount = flag.Int("mining-gpu", 1, "Number of GPUs to reserve for mining")
	miningCPU      = flag.Int("mining-cpu", 2, "CPU cores for mining container")
	miningMemory   = flag.Int64("mining-memory", 4096, "Memory (MB) for mining container")
	miningImage    = flag.String("mining-image", "mingeyom/worldland-mio:latest", "Worldland node image")
	miningPool     = flag.String("mining-pool", "stratum+tcp://pool.worldland.io:3333", "Mining pool URL")
) //flag 인자 정의 : 옵션 정의

func main() {
	flag.Parse() // 플래그 인자 정의

	// Setup logging
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))) // 로깅 설정

	// Generate provider ID if not provided
	if *providerID == "" {
		*providerID = "provider-" + uuid.New().String()[:8] // UUID 생성
	}

	slog.Info("Provider Agent starting", "providerID", *providerID)

	// Parse Redis address
	redisHost, redisPort := parseAddr(*redisAddr)

	// Connect to Redis
	redisClient, err := messaging.GetClient(&messaging.Config{ //redis 연결
		Host: redisHost,
		Port: redisPort,
	})
	if err != nil {
		slog.Error("Failed to connect to Redis", "error", err)
		os.Exit(1)
	}
	slog.Info("Connected to Redis", "addr", *redisAddr)

	// Scan system
	scanner := agent.NewSystemScanner()
	spec, err := scanner.Scan()
	if err != nil {
		slog.Error("Failed to scan system", "error", err)
		os.Exit(1)
	}

	fmt.Println(agent.FormatSpec(spec))

	// Prepare capacity
	capacity := prepareCapacity(spec)

	// Create registration request
	req := provider.RegistrationRequest{
		ProviderID:   *providerID,
		WalletAddr:   *walletAddr,
		Spec:         *spec,
		Capacity:     capacity,
		AgentVersion: "1.0.0",
		Timestamp:    time.Now(),
	}

	// Add mining configuration if enabled
	if *enableMining && *walletAddr != "" {
		req.MiningConfig = &provider.MiningConfig{
			Image:         *miningImage,
			GPUCount:      *miningGPUCount,
			CPUCores:      *miningCPU,
			MemoryMB:      *miningMemory,
			WalletAddress: *walletAddr,
			NetworkID:     10396, // Worldland chain ID
			P2PPort:       30303,
			RPCEnabled:    true,
			RPCPort:       8545,
		}
		slog.Info("Mining enabled",
			"gpuCount", *miningGPUCount,
			"wallet", *walletAddr,
		)
	} else if *enableMining && *walletAddr == "" {
		slog.Warn("Mining enabled but wallet address not provided. Mining will be disabled.")
	}

	// Send registration
	ctx, cancel := context.WithCancel(context.Background()) //컨텍스트 생성 go best practice, ctx는 go routine을 위한 컨텍스트, cancel은 컨텍스트를 취소하는 함수
	defer cancel()

	producer := messaging.NewProducer(redisClient) //프로듀서는 메시지를 보내는 역할
	msgID, err := producer.Publish(ctx, provider.StreamNames.Registration, req)
	if err != nil {
		slog.Error("Failed to send registration", "error", err)
		os.Exit(1)
	}
	slog.Info("Registration sent", "messageID", msgID)

	// Wait for response
	responseStream := fmt.Sprintf("provider:response:%s", *providerID)
	responseConsumer, err := messaging.NewConsumer(redisClient, &messaging.ConsumerConfig{
		Stream:        responseStream,
		Group:         "agent-group",
		Consumer:      "agent-1",
		BlockDuration: 30 * time.Second,
	}) //컨슈머는 메시지를 받는 역할
	if err != nil {
		slog.Error("Failed to create response consumer", "error", err)
		os.Exit(1)
	}

	slog.Info("Waiting for response from orchestrator...")

	// Wait for registration response
	var response provider.RegistrationResponse
	waitCtx, waitCancel := context.WithTimeout(ctx, 60*time.Second)
	defer waitCancel()

	for {
		messages, err := responseConsumer.ReadMessages(waitCtx, 1)
		if err != nil {
			slog.Error("Failed to read response", "error", err)
			os.Exit(1)
		}

		if len(messages) > 0 {
			if err := messages[0].Unmarshal(&response); err != nil { //unmarshal은 메시지를 구조체로 변환하는 역할
				slog.Error("Failed to unmarshal response", "error", err)
				os.Exit(1)
			}
			_ = responseConsumer.Ack(ctx, messages[0].ID) //Ack는 메시지를 처리한 것으로 표시하는 역할
			break
		}

		select {
		case <-waitCtx.Done():
			slog.Error("Timeout waiting for response")
			os.Exit(1)
		default:
		}
	}

	slog.Info("Received response",
		"success", response.Success,
		"status", response.Status,
		"message", response.Message,
	)

	if !response.Success {
		slog.Error("Registration rejected", "message", response.Message)
		os.Exit(1)
	}

	// Handle approved status
	if response.Status == provider.StatusApproved && response.JoinCommand != "" {
		fmt.Println("\n=== Join Command ===")
		fmt.Println(response.JoinCommand)
		fmt.Println()

		if *autoJoin {
			slog.Info("Executing kubeadm join...")
			if err := executeJoinCommand(response.JoinCommand); err != nil {
				slog.Error("Failed to join cluster", "error", err)
				os.Exit(1)
			}
			slog.Info("Successfully joined cluster!")
		} else {
			fmt.Println("Run the above command with sudo to join the cluster.")
			fmt.Println("Or restart this agent with --auto-join flag.")
		}
	}

	// Start heartbeat loop
	nodeName := spec.Hostname
	if response.NodeName != "" {
		nodeName = response.NodeName
	}

	go heartbeatLoop(ctx, producer, *providerID, nodeName, time.Duration(*heartbeatSec)*time.Second)

	// Wait for interrupt
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	slog.Info("Agent shutting down...")
	cancel()
}

func parseAddr(addr string) (string, int) {
	host := "localhost"
	port := 6379

	if idx := len(addr) - 1; idx > 0 {
		for i := idx; i >= 0; i-- {
			if addr[i] == ':' {
				host = addr[:i]
				if p, err := fmt.Sscanf(addr[i+1:], "%d", &port); err == nil && p == 1 {
					return host, port
				}
				break
			}
		}
	}
	return host, port
}

// prepareCapacity는 시스템의 정보를 바탕으로 provider.ProviderCapacity를 생성하는 함수, 이부분이 노드가 터지는 원인일 수 있음
func prepareCapacity(spec *provider.SystemSpec) provider.ProviderCapacity { //provider.SystemSpec은 시스템의 정보를 담고 있는 구조체
	capacity := provider.ProviderCapacity{}

	// GPU
	if *gpuCount > 0 {
		capacity.GPUCount = *gpuCount
	} else {
		capacity.GPUCount = spec.TotalGPUs
	}

	// CPU
	if *cpuCores > 0 {
		capacity.CPUCores = *cpuCores
	} else {
		// Share 80% of CPU cores
		capacity.CPUCores = int(float64(spec.CPUCores) * 0.8)
	}

	// Memory
	if *memoryMB > 0 {
		capacity.MemoryMB = *memoryMB
	} else {
		// Share 80% of memory
		capacity.MemoryMB = int64(float64(spec.TotalMemoryMB) * 0.8)
	}
	//TODO: 자동 경매로직 설정하기 OR 시간당 값 계산 로직 넣기
	// Default pricing (WLC per hour)
	capacity.GPUPricePerHour = 0.5
	capacity.CPUPricePerHour = 0.01

	return capacity
}

func executeJoinCommand(joinCmd string) error { //joinCmd는 kubeadm join 명령어
	cmd := exec.Command("sudo", "sh", "-c", joinCmd)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// TODO: GPU, CPU, Memory 사용량 계산 로직 구현
func heartbeatLoop(ctx context.Context, producer *messaging.Producer, providerID, nodeName string, interval time.Duration) { //heartbeatLoop는 심박수를 보내는 함수
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done(): //ctx가 done되면 종료
			return
		case <-ticker.C:
			hb := provider.HeartbeatMessage{
				ProviderID:  providerID,
				NodeName:    nodeName,
				Status:      provider.StatusAvailable,
				GPUUsage:    getGPUUsage(),
				CPUUsage:    getCPUUsage(),
				MemoryUsage: getMemoryUsage(),
				ActiveJobs:  0, // TODO: Get from kubelet
				Timestamp:   time.Now(),
			}

			if _, err := producer.Publish(ctx, provider.StreamNames.Heartbeat, hb); err != nil {
				slog.Error("Failed to send heartbeat", "error", err)
			} else {
				slog.Debug("Heartbeat sent")
			}
		}
	}
}

func getGPUUsage() []float64 {
	// TODO: Implement nvidia-smi GPU utilization query
	return []float64{}
}

func getCPUUsage() float64 {
	// TODO: Implement CPU usage calculation
	return 0.0
}

func getMemoryUsage() float64 {
	// TODO: Implement memory usage calculation
	return 0.0
}
