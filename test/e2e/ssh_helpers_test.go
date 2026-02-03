//go:build e2e
// +build e2e

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// SSHClient wraps SSH connection for E2E testing
type SSHClient struct {
	client *ssh.Client
	user   string
	host   string
	port   int
}

// SSHConnectionInfo holds SSH credentials from Hub API
type SSHConnectionInfo struct {
	Host     string
	Port     int
	User     string
	Password string
}

// ConnectSSH establishes SSH connection to a container
func ConnectSSH(ctx context.Context, host string, port int, user, password string) (*SSHClient, error) {
	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // Test environment only
		Timeout:         10 * time.Second,
	}

	addr := fmt.Sprintf("%s:%d", host, port)
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, fmt.Errorf("SSH dial to %s failed: %w", addr, err)
	}

	return &SSHClient{
		client: client,
		user:   user,
		host:   host,
		port:   port,
	}, nil
}

// RunCommand executes a command on the SSH session
func (s *SSHClient) RunCommand(cmd string) (string, error) {
	session, err := s.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("SSH session creation failed: %w", err)
	}
	defer session.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	if err := session.Run(cmd); err != nil {
		return "", fmt.Errorf("SSH command %q failed: %w (stderr: %s)", cmd, err, stderr.String())
	}

	return stdout.String(), nil
}

// Close closes the SSH connection
func (s *SSHClient) Close() error {
	if s.client != nil {
		return s.client.Close()
	}
	return nil
}

// VerifySSHAccess verifies SSH access with retries
func VerifySSHAccess(ctx context.Context, t *testing.T, info SSHConnectionInfo) {
	t.Helper()

	var client *SSHClient
	var err error

	// Retry SSH connection (sshd may take time to start)
	maxRetries := 15
	for i := 0; i < maxRetries; i++ {
		select {
		case <-ctx.Done():
			t.Fatalf("Context cancelled while waiting for SSH: %v", ctx.Err())
		default:
		}

		client, err = ConnectSSH(ctx, info.Host, info.Port, info.User, info.Password)
		if err == nil {
			break
		}

		if i < maxRetries-1 {
			t.Logf("SSH connection attempt %d/%d failed: %v (retrying...)", i+1, maxRetries, err)
			time.Sleep(2 * time.Second)
			continue
		}
	}

	if err != nil {
		t.Fatalf("SSH connection failed after %d retries: %v", maxRetries, err)
	}
	defer client.Close()

	t.Log("SSH connection established")

	// Verify user identity
	output, err := client.RunCommand("whoami")
	if err != nil {
		t.Fatalf("SSH command 'whoami' failed: %v", err)
	}

	actualUser := strings.TrimSpace(output)
	if actualUser != info.User {
		t.Fatalf("SSH returned wrong user: got %q, expected %q", actualUser, info.User)
	}

	// Verify basic container access
	output, err = client.RunCommand("echo 'hello from GPU container'")
	if err != nil {
		t.Fatalf("SSH echo command failed: %v", err)
	}

	if !strings.Contains(output, "hello from GPU container") {
		t.Fatalf("SSH echo returned unexpected output: %s", output)
	}

	t.Logf("SSH access fully verified: %s@%s:%d", info.User, info.Host, info.Port)
}

// WaitForSSHReady waits until SSH connection is available
func WaitForSSHReady(ctx context.Context, host string, port int, user, password string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		client, err := ConnectSSH(ctx, host, port, user, password)
		if err == nil {
			client.Close()
			return nil
		}

		time.Sleep(2 * time.Second)
	}

	return fmt.Errorf("SSH not ready after %v", timeout)
}

// ParseSSHInfoFromSession extracts SSH info from Hub session response
func ParseSSHInfoFromSession(session map[string]interface{}) (SSHConnectionInfo, error) {
	info := SSHConnectionInfo{}

	// Handle both nested and flat response formats
	sshInfo, ok := session["sshConnectionInfo"].(map[string]interface{})
	if !ok {
		// Try flat format
		if host, ok := session["sshHost"].(string); ok {
			info.Host = host
		} else {
			return info, fmt.Errorf("sshHost not found in session")
		}

		if port, ok := session["sshPort"].(float64); ok {
			info.Port = int(port)
		} else {
			return info, fmt.Errorf("sshPort not found in session")
		}

		if password, ok := session["sshPassword"].(string); ok {
			info.Password = password
		} else {
			return info, fmt.Errorf("sshPassword not found in session")
		}

		info.User = "ubuntu" // Default user
		return info, nil
	}

	// Nested format
	if host, ok := sshInfo["host"].(string); ok {
		info.Host = host
	} else {
		return info, fmt.Errorf("host not found in sshConnectionInfo")
	}

	if port, ok := sshInfo["port"].(float64); ok {
		info.Port = int(port)
	} else {
		return info, fmt.Errorf("port not found in sshConnectionInfo")
	}

	if password, ok := sshInfo["password"].(string); ok {
		info.Password = password
	} else {
		return info, fmt.Errorf("password not found in sshConnectionInfo")
	}

	if user, ok := sshInfo["user"].(string); ok {
		info.User = user
	} else {
		info.User = "ubuntu" // Default
	}

	return info, nil
}
