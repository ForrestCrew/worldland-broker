package services

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// K8sJoinService handles kubeadm join token generation and node K8s integration
type K8sJoinService struct {
	masterIP   string
	masterPort int
	enabled    bool

	// Track which nodes have successfully joined K8s
	joinedNodes   map[string]time.Time
	joinedNodesMu sync.RWMutex

	// Pending join requests (nodeID -> timestamp of request)
	pendingJoins   map[string]time.Time
	pendingJoinsMu sync.RWMutex
}

// K8sJoinConfig holds configuration for K8s join service
type K8sJoinConfig struct {
	MasterIP   string
	MasterPort int
	Enabled    bool
}

// JoinInfo contains the kubeadm join information to send to nodes
type JoinInfo struct {
	JoinToken   string `json:"join_token"`
	JoinCommand string `json:"join_command"`
	MasterIP    string `json:"master_ip"`
	MasterPort  int    `json:"master_port"`
	CAHash      string `json:"ca_hash"`
}

// NewK8sJoinService creates a new K8s join service
func NewK8sJoinService(cfg *K8sJoinConfig) *K8sJoinService {
	if cfg == nil {
		cfg = &K8sJoinConfig{
			Enabled: false,
		}
	}

	return &K8sJoinService{
		masterIP:     cfg.MasterIP,
		masterPort:   cfg.MasterPort,
		enabled:      cfg.Enabled,
		joinedNodes:  make(map[string]time.Time),
		pendingJoins: make(map[string]time.Time),
	}
}

// IsEnabled returns whether K8s join service is enabled
func (s *K8sJoinService) IsEnabled() bool {
	return s.enabled
}

// GenerateJoinToken generates a kubeadm join token
// Returns the token, CA hash, and full join command
func (s *K8sJoinService) GenerateJoinToken(ctx context.Context) (*JoinInfo, error) {
	if !s.enabled {
		return nil, fmt.Errorf("K8s join service is not enabled")
	}

	// Execute kubeadm token create
	cmd := exec.CommandContext(ctx, "kubeadm", "token", "create", "--print-join-command")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to create token: %w, output: %s", err, string(output))
	}

	// Parse the join command to extract token and hash
	joinCmd := strings.TrimSpace(string(output))
	parts := strings.Fields(joinCmd)

	var token, caHash string
	for i, part := range parts {
		if part == "--token" && i+1 < len(parts) {
			token = parts[i+1]
		}
		if part == "--discovery-token-ca-cert-hash" && i+1 < len(parts) {
			caHash = parts[i+1]
		}
	}

	if token == "" || caHash == "" {
		return nil, fmt.Errorf("failed to parse join command: %s", joinCmd)
	}

	// Construct the full join command
	fullJoinCommand := fmt.Sprintf(
		"sudo kubeadm join %s:%d --token %s --discovery-token-ca-cert-hash %s",
		s.masterIP, s.masterPort, token, caHash,
	)

	slog.Info("Generated K8s join token",
		"master", fmt.Sprintf("%s:%d", s.masterIP, s.masterPort),
		"token_prefix", token[:8]+"...",
	)

	return &JoinInfo{
		JoinToken:   token,
		JoinCommand: fullJoinCommand,
		MasterIP:    s.masterIP,
		MasterPort:  s.masterPort,
		CAHash:      caHash,
	}, nil
}

// RequestJoin marks a node as requesting to join K8s cluster
func (s *K8sJoinService) RequestJoin(nodeID string) {
	s.pendingJoinsMu.Lock()
	defer s.pendingJoinsMu.Unlock()
	s.pendingJoins[nodeID] = time.Now()
}

// MarkNodeJoined marks a node as successfully joined the K8s cluster
func (s *K8sJoinService) MarkNodeJoined(nodeID string) {
	s.joinedNodesMu.Lock()
	s.joinedNodes[nodeID] = time.Now()
	s.joinedNodesMu.Unlock()

	s.pendingJoinsMu.Lock()
	delete(s.pendingJoins, nodeID)
	s.pendingJoinsMu.Unlock()

	slog.Info("Node marked as joined K8s cluster", "nodeID", nodeID)
}

// IsNodeJoined checks if a node has joined the K8s cluster
func (s *K8sJoinService) IsNodeJoined(nodeID string) bool {
	s.joinedNodesMu.RLock()
	defer s.joinedNodesMu.RUnlock()
	_, exists := s.joinedNodes[nodeID]
	return exists
}

// PendingJoinTimeout is how long a pending join request is considered valid
// After this timeout, a new join command can be sent
const PendingJoinTimeout = 2 * time.Minute

// IsPendingJoin checks if a node has a recent pending join request
// Returns false if the pending request has expired (allows retry)
func (s *K8sJoinService) IsPendingJoin(nodeID string) bool {
	s.pendingJoinsMu.RLock()
	defer s.pendingJoinsMu.RUnlock()
	requestTime, exists := s.pendingJoins[nodeID]
	if !exists {
		return false
	}
	// Check if request has expired
	if time.Since(requestTime) > PendingJoinTimeout {
		return false
	}
	return true
}

// GetJoinedNodes returns all nodes that have joined K8s
func (s *K8sJoinService) GetJoinedNodes() map[string]time.Time {
	s.joinedNodesMu.RLock()
	defer s.joinedNodesMu.RUnlock()

	result := make(map[string]time.Time, len(s.joinedNodes))
	for k, v := range s.joinedNodes {
		result[k] = v
	}
	return result
}

// CheckNodeInCluster checks if a node exists in the K8s cluster using kubectl
func (s *K8sJoinService) CheckNodeInCluster(ctx context.Context, nodeName string) (bool, error) {
	if !s.enabled {
		return false, nil
	}

	cmd := exec.CommandContext(ctx, "kubectl", "get", "node", nodeName, "--no-headers")
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Node not found is not an error condition
		if strings.Contains(string(output), "not found") {
			return false, nil
		}
		return false, fmt.Errorf("failed to check node: %w, output: %s", err, string(output))
	}

	// Node exists if command succeeded
	return len(strings.TrimSpace(string(output))) > 0, nil
}

// sanitizeLabelValue converts a string to a valid K8s label value
// K8s labels must be 63 chars or less, alphanumeric with dashes, underscores, dots
// Must start and end with alphanumeric character
func sanitizeLabelValue(value string) string {
	// Replace spaces with dashes
	result := strings.ReplaceAll(value, " ", "-")
	// Convert to lowercase
	result = strings.ToLower(result)
	// Keep only allowed characters: alphanumeric, -, _, .
	var sanitized strings.Builder
	for _, ch := range result {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' || ch == '.' {
			sanitized.WriteRune(ch)
		}
	}
	result = sanitized.String()
	// Trim leading/trailing non-alphanumeric
	result = strings.Trim(result, "-_.")
	// Limit to 63 characters
	if len(result) > 63 {
		result = result[:63]
	}
	return result
}

// LabelNodeForRental adds rental-related labels to a K8s node
func (s *K8sJoinService) LabelNodeForRental(ctx context.Context, nodeName, providerID, gpuModel string) error {
	if !s.enabled {
		return nil
	}

	labels := []string{
		fmt.Sprintf("worldland.io/provider-id=%s", providerID),
		"worldland.io/rental-status=available",
	}
	if gpuModel != "" {
		// Sanitize GPU model to be a valid K8s label value
		sanitizedGPU := sanitizeLabelValue(gpuModel)
		labels = append(labels, fmt.Sprintf("worldland.io/gpu-model=%s", sanitizedGPU))
	}

	// Build kubectl args: label node <nodename> <label1> <label2> ... --overwrite
	args := append([]string{"label", "node", nodeName}, labels...)
	args = append(args, "--overwrite")
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to label node: %w, output: %s", err, string(output))
	}

	slog.Info("Node labeled for rental",
		"nodeName", nodeName,
		"providerID", providerID,
		"gpuModel", gpuModel,
	)

	return nil
}
