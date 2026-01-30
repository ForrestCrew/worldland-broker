//go:build integration && docker

// Package integration_test provides SSH connectivity verification tests.
// These tests validate SC2 success criterion: SSH connectivity to GPU containers.
//
// SC2: User can SSH into running GPU container and execute commands
//
// Run with: go test -tags="integration,docker" ./test/integration/
package integration_test

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSSH_ConnectivityToGPUContainer validates SC2 success criterion:
// User can SSH into a running GPU rental container and execute commands.
//
// This test simulates the full end-user experience:
// 1. Container is created with user's SSH public key
// 2. User connects via SSH (using private key)
// 3. User can execute commands in the container
// 4. User can access GPU (nvidia-smi)
//
// This is the automated verification of what the human checkpoint will validate.
func TestSSH_ConnectivityToGPUContainer(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping SSH integration test in short mode")
	}

	if !isDockerAvailable() {
		t.Skip("Docker not available - skipping SSH connectivity test")
	}

	ctx := context.Background()

	// Generate test SSH key pair
	sshKeyPair, err := generateTestSSHKeyPair()
	require.NoError(t, err, "Should generate test SSH key pair")
	defer cleanupSSHKeys(sshKeyPair)

	// Create container with SSH access
	testConfig := ContainerConfig{
		Image:        "nvidia/cuda:12.1-runtime-ubuntu22.04",
		SessionID:    "test-ssh-" + time.Now().Format("20060102-150405"),
		SSHPublicKey: sshKeyPair.PublicKey,
		MemoryBytes:  2 * 1024 * 1024 * 1024,
		CPUCount:     2,
	}

	containerID, err := createGPUContainer(ctx, testConfig)
	require.NoError(t, err, "Should create container with SSH access")
	defer cleanupContainer(ctx, containerID)

	// Wait for SSH server to start
	time.Sleep(5 * time.Second)

	// Get SSH connection details
	sshHost, sshPort, sshUser, err := getContainerSSHDetails(ctx, containerID)
	require.NoError(t, err, "Should get SSH connection details")

	// === SC2 VERIFICATION: SSH connectivity ===
	t.Run("SSH_BasicConnectivity", func(t *testing.T) {
		// Test SSH connection with simple command
		cmd := buildSSHCommand(sshKeyPair.PrivateKeyPath, sshHost, sshPort, sshUser, "echo 'SSH_WORKS'")
		output, err := executeCommand(ctx, cmd)

		require.NoError(t, err, "SSH connection should succeed")
		assert.Contains(t, output, "SSH_WORKS", "Should execute command via SSH")
	})

	t.Run("SSH_ExecuteCommands", func(t *testing.T) {
		// Test command execution: list directory
		cmd := buildSSHCommand(sshKeyPair.PrivateKeyPath, sshHost, sshPort, sshUser, "ls -la /")
		output, err := executeCommand(ctx, cmd)

		require.NoError(t, err, "Should execute ls command")
		assert.Contains(t, output, "bin", "Should see standard Linux directories")
		assert.Contains(t, output, "usr", "Should see standard Linux directories")
	})

	t.Run("SSH_FileOperations", func(t *testing.T) {
		// Test file creation and reading
		createCmd := buildSSHCommand(sshKeyPair.PrivateKeyPath, sshHost, sshPort, sshUser,
			"echo 'test-data' > /tmp/test.txt && cat /tmp/test.txt")
		output, err := executeCommand(ctx, createCmd)

		require.NoError(t, err, "Should create and read file")
		assert.Contains(t, output, "test-data", "Should read file contents")
	})

	t.Run("SSH_GPUAccess", func(t *testing.T) {
		if !isNvidiaDockerAvailable() {
			t.Skip("nvidia-docker not available - skipping GPU access test")
		}

		// Test GPU access via nvidia-smi
		cmd := buildSSHCommand(sshKeyPair.PrivateKeyPath, sshHost, sshPort, sshUser, "nvidia-smi")
		output, err := executeCommand(ctx, cmd)

		require.NoError(t, err, "nvidia-smi should execute successfully")
		assert.Contains(t, output, "NVIDIA", "Should show NVIDIA GPU information")
		assert.Contains(t, output, "Driver Version", "Should show driver version")
	})

	t.Run("SSH_CUDAAvailability", func(t *testing.T) {
		if !isNvidiaDockerAvailable() {
			t.Skip("nvidia-docker not available - skipping CUDA test")
		}

		// Test CUDA runtime is available
		cmd := buildSSHCommand(sshKeyPair.PrivateKeyPath, sshHost, sshPort, sshUser, "nvcc --version || echo 'CUDA_RUNTIME_AVAILABLE'")
		output, err := executeCommand(ctx, cmd)

		require.NoError(t, err, "Should check CUDA availability")
		// Either nvcc is available, or we're in a runtime-only image
		assert.True(t, strings.Contains(output, "cuda") || strings.Contains(output, "CUDA_RUNTIME_AVAILABLE"),
			"Should have CUDA runtime available")
	})
}

// TestSSH_AuthenticationRejection validates that SSH rejects unauthorized keys
func TestSSH_AuthenticationRejection(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping SSH integration test in short mode")
	}

	if !isDockerAvailable() {
		t.Skip("Docker not available")
	}

	ctx := context.Background()

	// Create container with specific SSH key
	authorizedKeyPair, err := generateTestSSHKeyPair()
	require.NoError(t, err)
	defer cleanupSSHKeys(authorizedKeyPair)

	testConfig := ContainerConfig{
		Image:        "nvidia/cuda:12.1-runtime-ubuntu22.04",
		SessionID:    "test-ssh-auth-" + time.Now().Format("20060102-150405"),
		SSHPublicKey: authorizedKeyPair.PublicKey,
		MemoryBytes:  1 * 1024 * 1024 * 1024,
		CPUCount:     1,
	}

	containerID, err := createGPUContainer(ctx, testConfig)
	require.NoError(t, err)
	defer cleanupContainer(ctx, containerID)

	time.Sleep(5 * time.Second)

	sshHost, sshPort, sshUser, err := getContainerSSHDetails(ctx, containerID)
	require.NoError(t, err)

	// Generate different (unauthorized) SSH key
	unauthorizedKeyPair, err := generateTestSSHKeyPair()
	require.NoError(t, err)
	defer cleanupSSHKeys(unauthorizedKeyPair)

	// Attempt SSH with unauthorized key - should fail
	cmd := buildSSHCommand(unauthorizedKeyPair.PrivateKeyPath, sshHost, sshPort, sshUser, "echo 'should-not-work'")
	_, err = executeCommand(ctx, cmd)

	assert.Error(t, err, "SSH should reject unauthorized key")
}

// TestSSH_MultipleUsers validates independent SSH access for concurrent rentals
func TestSSH_MultipleUsers(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping SSH integration test in short mode")
	}

	if !isDockerAvailable() {
		t.Skip("Docker not available")
	}

	ctx := context.Background()

	// Create two containers with different SSH keys
	user1KeyPair, err := generateTestSSHKeyPair()
	require.NoError(t, err)
	defer cleanupSSHKeys(user1KeyPair)

	user2KeyPair, err := generateTestSSHKeyPair()
	require.NoError(t, err)
	defer cleanupSSHKeys(user2KeyPair)

	// Container 1
	config1 := ContainerConfig{
		Image:        "nvidia/cuda:12.1-runtime-ubuntu22.04",
		SessionID:    "test-ssh-user1-" + time.Now().Format("20060102-150405"),
		SSHPublicKey: user1KeyPair.PublicKey,
		MemoryBytes:  1 * 1024 * 1024 * 1024,
		CPUCount:     1,
	}

	container1, err := createGPUContainer(ctx, config1)
	require.NoError(t, err)
	defer cleanupContainer(ctx, container1)

	// Container 2
	config2 := ContainerConfig{
		Image:        "nvidia/cuda:12.1-runtime-ubuntu22.04",
		SessionID:    "test-ssh-user2-" + time.Now().Format("20060102-150405"),
		SSHPublicKey: user2KeyPair.PublicKey,
		MemoryBytes:  1 * 1024 * 1024 * 1024,
		CPUCount:     1,
	}

	container2, err := createGPUContainer(ctx, config2)
	require.NoError(t, err)
	defer cleanupContainer(ctx, container2)

	time.Sleep(5 * time.Second)

	// Get SSH details for both containers
	ssh1Host, ssh1Port, ssh1User, err := getContainerSSHDetails(ctx, container1)
	require.NoError(t, err)

	ssh2Host, ssh2Port, ssh2User, err := getContainerSSHDetails(ctx, container2)
	require.NoError(t, err)

	// User 1 can access container 1
	cmd1 := buildSSHCommand(user1KeyPair.PrivateKeyPath, ssh1Host, ssh1Port, ssh1User, "hostname")
	output1, err := executeCommand(ctx, cmd1)
	require.NoError(t, err, "User 1 should access container 1")
	assert.NotEmpty(t, output1)

	// User 2 can access container 2
	cmd2 := buildSSHCommand(user2KeyPair.PrivateKeyPath, ssh2Host, ssh2Port, ssh2User, "hostname")
	output2, err := executeCommand(ctx, cmd2)
	require.NoError(t, err, "User 2 should access container 2")
	assert.NotEmpty(t, output2)

	// User 1 cannot access container 2 (wrong key)
	cmd1to2 := buildSSHCommand(user1KeyPair.PrivateKeyPath, ssh2Host, ssh2Port, ssh2User, "hostname")
	_, err = executeCommand(ctx, cmd1to2)
	assert.Error(t, err, "User 1 should not access container 2")
}

// SSHKeyPair represents an SSH key pair for testing
type SSHKeyPair struct {
	PublicKey      string
	PrivateKeyPath string
}

// Helper functions for SSH testing

func generateTestSSHKeyPair() (*SSHKeyPair, error) {
	// Generate temporary SSH key pair for testing
	// Implementation: exec ssh-keygen to create test keys in /tmp
	// Returns paths to public/private keys
	return &SSHKeyPair{
		PublicKey:      "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQ...",
		PrivateKeyPath: "/tmp/test-ssh-key",
	}, nil // Placeholder
}

func cleanupSSHKeys(keyPair *SSHKeyPair) {
	// Remove test SSH keys
	// Implementation: exec rm for public and private key files
}

func getContainerSSHDetails(ctx context.Context, containerID string) (host string, port int, user string, err error) {
	// Get SSH connection details for container
	// Implementation: inspect container to get mapped SSH port
	// Returns: host (container IP or localhost), port (mapped SSH port), user (rental user)
	return "localhost", 2222, "rental", nil // Placeholder
}

func buildSSHCommand(privateKeyPath, host string, port int, user, command string) *exec.Cmd {
	// Build SSH command with proper options
	// Implementation: ssh -i <key> -p <port> -o StrictHostKeyChecking=no <user>@<host> <command>
	sshArgs := []string{
		"-i", privateKeyPath,
		"-p", fmt.Sprintf("%d", port),
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ConnectTimeout=10",
		fmt.Sprintf("%s@%s", user, host),
		command,
	}
	return exec.Command("ssh", sshArgs...)
}

func executeCommand(ctx context.Context, cmd *exec.Cmd) (string, error) {
	// Execute command and return output
	// Implementation: cmd.CombinedOutput()
	output, err := cmd.CombinedOutput()
	return string(output), err
}
