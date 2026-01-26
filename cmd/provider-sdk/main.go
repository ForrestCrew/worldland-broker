// Worldland Provider SDK - Main entry point
//
// This SDK provides a single-command solution for GPU providers to join
// the Worldland network and start earning from both mining and rentals.
//
// Usage:
//
//	worldland-provider-sdk \
//	  --master-url=https://master.worldland.io \
//	  --token=<bootstrap-token> \
//	  --wallet=0x1234...
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/nubro999/worldland-gpu/internal/sdk"
)

var (
	// Connection flags
	masterURL = flag.String("master-url", "http://15.164.220.132:8080", "Master cluster URL")
	token     = flag.String("token", "", "Bootstrap token for cluster join")
	redisAddr = flag.String("redis", "", "Redis address (defaults to master:6379)")

	// Provider flags
	providerID = flag.String("provider-id", "", "Provider ID (auto-generated if empty)")
	wallet     = flag.String("wallet", "", "Worldland wallet address for rewards")

	// Mining flags
	enableMining = flag.Bool("enable-mining", true, "Enable Worldland mining")
	miningGPU    = flag.Int("mining-gpu", 1, "Initial GPUs for mining")
	miningCPU    = flag.Int("mining-cpu", 2, "CPU cores for mining")
	miningMemory = flag.Int64("mining-memory", 4096, "Memory (MB) for mining")
	miningImage  = flag.String("mining-image", "mingeyom/worldland-mio:latest", "Worldland node image")

	// Full Node flags
	networkID = flag.Int("network-id", 10396, "Worldland network ID")
	p2pPort   = flag.Int("p2p-port", 30303, "P2P listen port")
	rpcPort   = flag.Int("rpc-port", 8545, "RPC port")

	// Behavior flags
	autoJoin  = flag.Bool("auto-join", true, "Auto-execute kubeadm join")
	autoScale = flag.Bool("auto-scale", false, "Auto-adjust mining GPUs based on demand")
	heartbeat = flag.Int("heartbeat", 30, "Heartbeat interval in seconds")

	// Mode flags
	skipInstall  = flag.Bool("skip-install", false, "Skip dependency installation")
	skipValidate = flag.Bool("skip-validate", false, "Skip system validation")
	daemonOnly   = flag.Bool("daemon-only", false, "Only run daemon (already installed)")
	verbose      = flag.Bool("verbose", false, "Enable verbose output")
	version      = flag.Bool("version", false, "Print version and exit")
)

const sdkVersion = "1.0.0"

func main() {
	flag.Parse()

	if *version {
		fmt.Printf("Worldland Provider SDK v%s\n", sdkVersion)
		os.Exit(0)
	}

	// Validate required flags
	if *wallet == "" {
		fmt.Println("Error: --wallet is required")
		flag.Usage()
		os.Exit(1)
	}

	if *token == "" && !*daemonOnly {
		fmt.Println("Error: --token is required for initial setup")
		flag.Usage()
		os.Exit(1)
	}

	// Build config
	config := buildConfig()

	// Print banner
	printBanner()

	ctx := context.Background()

	// Run the appropriate mode
	if *daemonOnly {
		runDaemonOnly(ctx, config)
	} else {
		runFullSetup(ctx, config)
	}
}

func buildConfig() *sdk.Config {
	config := sdk.DefaultConfig()

	config.MasterURL = *masterURL
	config.Token = *token
	config.ProviderID = *providerID
	config.WalletAddr = *wallet

	// Redis address defaults to master host
	if *redisAddr != "" {
		config.RedisAddr = *redisAddr
	} else {
		// Extract host from master URL and use Redis port
		config.RedisAddr = extractHost(*masterURL) + ":6379"
	}

	// Mining config
	config.EnableMining = *enableMining
	config.MiningGPUCount = *miningGPU
	config.MiningCPUCores = *miningCPU
	config.MiningMemoryMB = *miningMemory
	config.MiningImage = *miningImage

	// Full Node config
	config.NetworkID = *networkID
	config.P2PPort = *p2pPort
	config.RPCPort = *rpcPort
	config.RPCEnabled = true

	// Behavior
	config.AutoJoin = *autoJoin
	config.AutoScale = *autoScale
	config.HeartbeatInterval = time.Duration(*heartbeat) * time.Second
	config.Verbose = *verbose

	return config
}

func extractHost(url string) string {
	// Remove protocol
	host := url
	for _, prefix := range []string{"https://", "http://"} {
		if len(host) > len(prefix) && host[:len(prefix)] == prefix {
			host = host[len(prefix):]
			break
		}
	}
	// Remove port if present
	for i := len(host) - 1; i >= 0; i-- {
		if host[i] == ':' {
			host = host[:i]
			break
		}
		if host[i] == '/' {
			host = host[:i]
			break
		}
	}
	return host
}

func printBanner() {
	fmt.Println("====================================================")
	fmt.Printf("  Worldland Provider SDK v%s\n", sdkVersion)
	fmt.Println("====================================================")
	fmt.Println()
}

func runFullSetup(ctx context.Context, config *sdk.Config) {
	var err error

	// Step 1: Validate system
	if !*skipValidate {
		fmt.Println("[1/6] Validating system requirements...")
		validator := sdk.NewValidator(config)
		result, err := validator.ValidateAll(ctx)
		if err != nil {
			fmt.Printf("  ✗ Validation error: %v\n", err)
			os.Exit(1)
		}

		sdk.PrintValidationResult(result)

		if !result.Valid {
			fmt.Println("\n✗ System validation failed. Please fix the errors above and try again.")
			os.Exit(1)
		}
	} else {
		fmt.Println("[1/6] Skipping system validation...")
	}

	// Step 2-4: Install dependencies
	if !*skipInstall {
		fmt.Println("\n[2/6] Installing containerd...")
		fmt.Println("[3/6] Installing Kubernetes components...")
		fmt.Println("[4/6] Installing NVIDIA Container Toolkit...")

		installer := sdk.NewInstaller(config)

		// Run preflight checks
		if err := installer.PreflightChecks(ctx); err != nil {
			fmt.Printf("  ✗ Preflight checks failed: %v\n", err)
			os.Exit(1)
		}

		// Install all dependencies
		if err := installer.InstallAll(ctx); err != nil {
			fmt.Printf("\n✗ Installation failed: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Println("[2-4/6] Skipping dependency installation...")
	}

	// Step 5-6: Bootstrap and register
	bootstrap := sdk.NewBootstrap(config)
	if err = bootstrap.Run(ctx); err != nil {
		fmt.Printf("\n✗ Bootstrap failed: %v\n", err)
		os.Exit(1)
	}

	// Start daemon
	daemon := sdk.NewDaemon(
		config,
		bootstrap.GetProviderID(),
		bootstrap.GetNodeName(),
		bootstrap.GetSpec(),
	)

	if err = daemon.Run(ctx); err != nil {
		fmt.Printf("\n✗ Daemon error: %v\n", err)
		os.Exit(1)
	}
}

func runDaemonOnly(ctx context.Context, config *sdk.Config) {
	fmt.Println("Running in daemon-only mode...")

	// For daemon-only mode, we need provider ID and node name
	if config.ProviderID == "" {
		fmt.Println("Error: --provider-id is required in daemon-only mode")
		os.Exit(1)
	}

	// Scan system to get spec
	// We'll use a minimal scan just for specs

	daemon := sdk.NewDaemon(
		config,
		config.ProviderID,
		"",  // Node name will be fetched
		nil, // Spec will be fetched
	)

	if err := daemon.Run(ctx); err != nil {
		fmt.Printf("\n✗ Daemon error: %v\n", err)
		os.Exit(1)
	}
}
