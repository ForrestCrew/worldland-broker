package remote

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/worldland/worldland-hub/internal/adapters/mtls"
)

// JobManager manages GPU session container lifecycle on remote nodes via mTLS.
// This replaces k8s.JobManager for external providers that run Docker directly.
type JobManager struct {
	mtlsServer *mtls.Server
	logger     *slog.Logger

	// SSH credentials stored in-memory (backed by DB via session repo)
	sshCreds   map[string]*SSHConnectionInfo // sessionID -> SSH info
	sshCredsMu sync.RWMutex

	// Pending command responses (command ack channel)
	pendingCmds   map[string]chan *mtls.CommandAck
	pendingCmdsMu sync.Mutex

	// Mining status from SDK node heartbeats (nodeID -> mining status)
	miningStatus   map[string]*NodeMiningStatus
	miningStatusMu sync.RWMutex
}

// NewJobManager creates a new remote JobManager
func NewJobManager(mtlsServer *mtls.Server, logger *slog.Logger) *JobManager {
	return &JobManager{
		mtlsServer:   mtlsServer,
		logger:       logger,
		sshCreds:     make(map[string]*SSHConnectionInfo),
		pendingCmds:  make(map[string]chan *mtls.CommandAck),
		miningStatus: make(map[string]*NodeMiningStatus),
	}
}

// GenerateSSHPassword generates a cryptographically secure random password
func GenerateSSHPassword() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 16)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		b[i] = charset[n.Int64()]
	}
	return string(b)
}

// CreateGPUSession sends a start_rental command to the remote node via mTLS.
// The node will create a Docker container and return SSH connection info.
// Returns the generated SSH password.
func (m *JobManager) CreateGPUSession(ctx context.Context, spec GPUJobSpec) (string, error) {
	password := GenerateSSHPassword()

	// Parse resource specs
	cpuCount := 4
	if n, err := strconv.Atoi(spec.CPURequest); err == nil {
		cpuCount = n
	}
	memoryMB := 16384 // 16GB default
	if spec.MemoryRequest != "" {
		memoryMB = parseMemoryToMB(spec.MemoryRequest)
	}

	payload := StartRentalPayload{
		SessionID:   spec.SessionID,
		Image:       spec.Image,
		GPUDeviceID: spec.GPUDeviceID,
		GPUCount:    spec.GPUCount,
		CPUCount:    cpuCount,
		MemoryMB:    memoryMB,
	}

	payloadMap := map[string]interface{}{
		"session_id":    payload.SessionID,
		"image":         payload.Image,
		"gpu_device_id": payload.GPUDeviceID,
		"gpu_count":     payload.GPUCount,
		"cpu_count":     payload.CPUCount,
		"memory_mb":     payload.MemoryMB,
		"ssh_password":  password,
	}

	cmdID := fmt.Sprintf("start-rental-%s", uuid.New().String())
	cmd := mtls.Command{
		ID:      cmdID,
		Type:    "start_rental",
		Payload: payloadMap,
	}

	// Register pending command for response tracking
	ackCh := make(chan *mtls.CommandAck, 1)
	m.pendingCmdsMu.Lock()
	m.pendingCmds[cmdID] = ackCh
	m.pendingCmdsMu.Unlock()
	defer func() {
		m.pendingCmdsMu.Lock()
		delete(m.pendingCmds, cmdID)
		m.pendingCmdsMu.Unlock()
	}()

	// Send command to node
	if err := m.mtlsServer.SendCommand(spec.NodeID, cmd); err != nil {
		return "", fmt.Errorf("failed to send start_rental to node %s: %w", spec.NodeID, err)
	}

	m.logger.Info("sent start_rental command",
		"sessionId", spec.SessionID,
		"nodeId", spec.NodeID,
		"image", spec.Image,
		"gpuCount", spec.GPUCount,
	)

	// Wait for acknowledgment with timeout
	select {
	case ack := <-ackCh:
		if ack.Status != "ok" {
			return "", fmt.Errorf("node rejected start_rental: %s", ack.Error)
		}
		// Extract SSH connection info from ack payload
		if ack.Payload != nil {
			sshUser := "ubuntu"
			if user, ok := ack.Payload["ssh_user"].(string); ok && user != "" {
				sshUser = user
			}
			sshInfo := &SSHConnectionInfo{
				Password: password,
				User:     sshUser,
			}
			if host, ok := ack.Payload["ssh_host"].(string); ok {
				sshInfo.Host = host
			}
			if port, ok := ack.Payload["ssh_port"].(float64); ok {
				sshInfo.Port = int32(port)
			}
			m.storeSSHCreds(spec.SessionID, sshInfo)
		}
		return password, nil

	case <-time.After(2 * time.Minute):
		// Store password with host info from node's API endpoint (fallback for slow image pulls)
		m.storeSSHCreds(spec.SessionID, &SSHConnectionInfo{
			Host:     spec.NodeHost,
			Port:     30000, // default SSH port for rental containers
			Password: password,
			User:     "ubuntu",
		})
		m.logger.Warn("start_rental command timed out waiting for ack, password stored with fallback host",
			"sessionId", spec.SessionID,
			"nodeId", spec.NodeID,
			"host", spec.NodeHost,
		)
		return password, nil

	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// DeleteGPUSession sends a stop_rental command to the remote node via mTLS.
func (m *JobManager) DeleteGPUSession(ctx context.Context, nodeID, sessionID string) error {
	payloadMap := map[string]interface{}{
		"session_id": sessionID,
	}

	cmd := mtls.Command{
		ID:      fmt.Sprintf("stop-rental-%s", uuid.New().String()),
		Type:    "stop_rental",
		Payload: payloadMap,
	}

	if err := m.mtlsServer.SendCommand(nodeID, cmd); err != nil {
		// Node might be disconnected - log but don't fail
		m.logger.Warn("failed to send stop_rental command",
			"sessionId", sessionID,
			"nodeId", nodeID,
			"error", err,
		)
		// Clean up local state anyway
		m.deleteSSHCreds(sessionID)
		return nil
	}

	m.logger.Info("sent stop_rental command",
		"sessionId", sessionID,
		"nodeId", nodeID,
	)

	// Clean up local SSH credentials
	m.deleteSSHCreds(sessionID)
	return nil
}

// GetSSHConnectionInfo retrieves SSH connection details for a session
func (m *JobManager) GetSSHConnectionInfo(_ context.Context, sessionID string) (*SSHConnectionInfo, error) {
	m.sshCredsMu.RLock()
	defer m.sshCredsMu.RUnlock()

	info, ok := m.sshCreds[sessionID]
	if !ok {
		return nil, fmt.Errorf("SSH credentials not found for session %s", sessionID)
	}
	return info, nil
}

// HandleCommandAck processes command acknowledgments from nodes.
// Called by the mTLS OnMessage handler in main.go.
func (m *JobManager) HandleCommandAck(nodeID string, ack *mtls.CommandAck) {
	m.pendingCmdsMu.Lock()
	ch, ok := m.pendingCmds[ack.CommandID]
	m.pendingCmdsMu.Unlock()

	if ok {
		// Send ack to waiting goroutine (non-blocking)
		select {
		case ch <- ack:
		default:
		}
		return
	}

	// Late ack (after timeout) - update SSH info if this was a start_rental response
	if strings.HasPrefix(ack.CommandID, "start-rental-") && ack.Status == "ok" && ack.Payload != nil {
		if sessionID, ok := ack.Payload["session_id"].(string); ok && sessionID != "" {
			host, _ := ack.Payload["ssh_host"].(string)
			port, _ := ack.Payload["ssh_port"].(float64)
			if host != "" && port > 0 {
				m.UpdateSSHInfo(sessionID, host, int32(port))
				m.logger.Info("late ack: updated SSH info after timeout",
					"sessionId", sessionID, "host", host, "port", int32(port))
			}
		}
	}

	// Handle state updates from node (container_state_update messages)
	if ack.Payload != nil {
		if stateStr, ok := ack.Payload["container_state"].(string); ok {
			sessionID, _ := ack.Payload["session_id"].(string)
			if sessionID != "" {
				m.handleContainerStateUpdate(nodeID, sessionID, stateStr, ack.Payload)
			}
		}
	}
}

// UpdateSSHInfo updates SSH connection info for a session (called when node reports container ready)
func (m *JobManager) UpdateSSHInfo(sessionID string, host string, port int32) {
	m.sshCredsMu.Lock()
	defer m.sshCredsMu.Unlock()

	if info, ok := m.sshCreds[sessionID]; ok {
		info.Host = host
		info.Port = port
	}
}

// handleContainerStateUpdate processes container state updates from nodes
func (m *JobManager) handleContainerStateUpdate(nodeID, sessionID, state string, payload map[string]interface{}) {
	m.logger.Info("container state update",
		"nodeId", nodeID,
		"sessionId", sessionID,
		"state", state,
	)

	// Update SSH info if container became running
	if state == "running" {
		if host, ok := payload["ssh_host"].(string); ok {
			if port, ok := payload["ssh_port"].(float64); ok {
				m.UpdateSSHInfo(sessionID, host, int32(port))
			}
		}
	}
}

// storeSSHCreds stores SSH credentials for a session
func (m *JobManager) storeSSHCreds(sessionID string, info *SSHConnectionInfo) {
	m.sshCredsMu.Lock()
	defer m.sshCredsMu.Unlock()
	m.sshCreds[sessionID] = info
}

// deleteSSHCreds removes SSH credentials for a session
func (m *JobManager) deleteSSHCreds(sessionID string) {
	m.sshCredsMu.Lock()
	defer m.sshCredsMu.Unlock()
	delete(m.sshCreds, sessionID)
}

// parseMemoryToMB converts K8s memory spec strings to megabytes
func parseMemoryToMB(spec string) int {
	if len(spec) < 2 {
		return 16384
	}
	suffix := spec[len(spec)-2:]
	numStr := spec[:len(spec)-2]
	num, err := strconv.Atoi(numStr)
	if err != nil {
		return 16384
	}
	switch suffix {
	case "Gi":
		return num * 1024
	case "Mi":
		return num
	default:
		return 16384
	}
}

// HandleNodeMessage processes raw messages from nodes (for mTLS OnMessage callback).
// Detects message type: heartbeat or CommandAck.
func (m *JobManager) HandleNodeMessage(nodeID string, msg []byte) {
	// Try to detect message type
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(msg, &raw); err != nil {
		m.logger.Error("failed to parse node message", "nodeID", nodeID, "error", err)
		return
	}

	// Check if this is a heartbeat message
	if typeRaw, ok := raw["type"]; ok {
		var msgType string
		if err := json.Unmarshal(typeRaw, &msgType); err == nil && msgType == "heartbeat" {
			m.handleHeartbeat(nodeID, raw)
			return
		}
	}

	// Otherwise treat as CommandAck
	var ack mtls.CommandAck
	if err := json.Unmarshal(msg, &ack); err != nil {
		m.logger.Error("failed to parse command ack", "nodeID", nodeID, "error", err)
		return
	}
	m.HandleCommandAck(nodeID, &ack)
}

// handleHeartbeat processes heartbeat messages from SDK nodes
func (m *JobManager) handleHeartbeat(nodeID string, raw map[string]json.RawMessage) {
	payloadRaw, ok := raw["payload"]
	if !ok {
		m.logger.Warn("heartbeat missing payload", "nodeID", nodeID)
		return
	}
	m.logger.Info("heartbeat received", "nodeID", nodeID)

	var payload struct {
		Mining *struct {
			State       string `json:"state"`
			ContainerID string `json:"container_id"`
			GPUCount    int    `json:"gpu_count"`
			StartedAt   string `json:"started_at,omitempty"`
		} `json:"mining,omitempty"`
	}
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		m.logger.Debug("failed to parse heartbeat payload", "nodeID", nodeID, "error", err)
		return
	}

	if payload.Mining != nil {
		status := &NodeMiningStatus{
			State:       payload.Mining.State,
			ContainerID: payload.Mining.ContainerID,
			GPUCount:    payload.Mining.GPUCount,
			LastSeen:    time.Now(),
		}
		if payload.Mining.StartedAt != "" {
			if t, err := time.Parse(time.RFC3339, payload.Mining.StartedAt); err == nil {
				status.StartedAt = &t
			}
		}

		m.miningStatusMu.Lock()
		m.miningStatus[nodeID] = status
		m.miningStatusMu.Unlock()

		m.logger.Info("mining status updated from heartbeat",
			"nodeID", nodeID,
			"state", status.State,
			"gpuCount", status.GPUCount,
		)
	}
}

// GetNodeMiningStatus returns mining status for a specific node
func (m *JobManager) GetNodeMiningStatus(nodeID string) *NodeMiningStatus {
	m.miningStatusMu.RLock()
	defer m.miningStatusMu.RUnlock()
	return m.miningStatus[nodeID]
}

// GetAllMiningStatus returns mining status for all SDK nodes
func (m *JobManager) GetAllMiningStatus() map[string]*NodeMiningStatus {
	m.miningStatusMu.RLock()
	defer m.miningStatusMu.RUnlock()
	result := make(map[string]*NodeMiningStatus, len(m.miningStatus))
	for k, v := range m.miningStatus {
		result[k] = v
	}
	return result
}
