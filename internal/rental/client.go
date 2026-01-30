package rental

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// NodeClientInterface defines the interface for Hub-to-Node communication
type NodeClientInterface interface {
	StartRental(ctx context.Context, nodeURL string, req StartRentalRequest) (*StartRentalResponse, error)
	StopRental(ctx context.Context, nodeURL string, req StopRentalRequest) (*StopRentalResponse, error)
}

var (
	ErrNodeUnreachable     = errors.New("node unreachable")
	ErrNodeStartFailed     = errors.New("node failed to start rental")
	ErrNodeStopFailed      = errors.New("node failed to stop rental")
	ErrInvalidNodeResponse = errors.New("invalid response from node")
)

// StartRentalRequest sent to Node
type StartRentalRequest struct {
	SessionID    string `json:"sessionId"`
	GPUDeviceID  string `json:"gpuDeviceId"`
	Image        string `json:"image,omitempty"`
	SSHPublicKey string `json:"sshPublicKey"`
	MemoryBytes  int64  `json:"memoryBytes,omitempty"`
	CPUCount     int64  `json:"cpuCount,omitempty"`
}

// StartRentalResponse from Node
type StartRentalResponse struct {
	SessionID  string `json:"sessionId"`
	SSHHost    string `json:"sshHost"`
	SSHPort    int    `json:"sshPort"`
	SSHUser    string `json:"sshUser"`
	SSHCommand string `json:"sshCommand"`
}

// StopRentalRequest sent to Node
type StopRentalRequest struct {
	SessionID string `json:"sessionId"`
}

// StopRentalResponse from Node
type StopRentalResponse struct {
	SessionID string `json:"sessionId"`
	Message   string `json:"message"`
}

// NodeErrorResponse for error cases
type NodeErrorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// NodeClient communicates with Provider Nodes via mTLS
type NodeClient struct {
	httpClient *http.Client
	timeout    time.Duration
}

// NodeClientConfig for creating NodeClient
type NodeClientConfig struct {
	TLSConfig *tls.Config // mTLS configuration
	Timeout   time.Duration
}

// NewNodeClient creates a client for Node communication
func NewNodeClient(cfg NodeClientConfig) *NodeClient {
	if cfg.Timeout == 0 {
		cfg.Timeout = 2 * time.Minute // Container start can take time
	}

	transport := &http.Transport{
		TLSClientConfig: cfg.TLSConfig,
	}

	return &NodeClient{
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   cfg.Timeout,
		},
		timeout: cfg.Timeout,
	}
}

// StartRental calls Node's /rentals/start endpoint
func (c *NodeClient) StartRental(ctx context.Context, nodeURL string, req StartRentalRequest) (*StartRentalResponse, error) {
	url := fmt.Sprintf("%s/rentals/start", nodeURL)

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNodeUnreachable, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp NodeErrorResponse
		if err := json.Unmarshal(respBody, &errResp); err == nil {
			return nil, fmt.Errorf("%w: %s (code: %s)", ErrNodeStartFailed, errResp.Error, errResp.Code)
		}
		return nil, fmt.Errorf("%w: status %d", ErrNodeStartFailed, resp.StatusCode)
	}

	var result StartRentalResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidNodeResponse, err)
	}

	return &result, nil
}

// StopRental calls Node's /rentals/stop endpoint
func (c *NodeClient) StopRental(ctx context.Context, nodeURL string, req StopRentalRequest) (*StopRentalResponse, error) {
	url := fmt.Sprintf("%s/rentals/stop", nodeURL)

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNodeUnreachable, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp NodeErrorResponse
		if err := json.Unmarshal(respBody, &errResp); err == nil {
			return nil, fmt.Errorf("%w: %s (code: %s)", ErrNodeStopFailed, errResp.Error, errResp.Code)
		}
		return nil, fmt.Errorf("%w: status %d", ErrNodeStopFailed, resp.StatusCode)
	}

	var result StopRentalResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidNodeResponse, err)
	}

	return &result, nil
}
