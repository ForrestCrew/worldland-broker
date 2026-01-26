package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// APIClient is a client for the k8s-proxy-server API.
type APIClient struct {
	baseURL    string
	httpClient *http.Client
	providerID string
}

// NewAPIClient creates a new APIClient.
func NewAPIClient(baseURL, providerID string) *APIClient {
	return &APIClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		providerID: providerID,
	}
}

// MiningStatusResponse represents the mining status response.
type MiningStatusResponse struct {
	ProviderID    string                `json:"provider_id"`
	MiningStatus  string                `json:"mining_status"`
	MiningPodName string                `json:"mining_pod_name"`
	Resources     MiningResourcesStatus `json:"resources"`
	Metrics       *MiningMetricsStatus  `json:"metrics,omitempty"`
	WalletAddress string                `json:"wallet_address"`
	UptimeSeconds int64                 `json:"uptime_seconds"`
}

// MiningResourcesStatus represents mining resource allocation.
type MiningResourcesStatus struct {
	GPUCount int   `json:"gpu_count"`
	CPUCores int   `json:"cpu_cores"`
	MemoryMB int64 `json:"memory_mb"`
}

// MiningMetricsStatus represents mining performance metrics.
type MiningMetricsStatus struct {
	Hashrate       string    `json:"hashrate,omitempty"`
	GPUUtilization []float64 `json:"gpu_utilization,omitempty"`
	Temperature    []int     `json:"temperature,omitempty"`
	PowerUsage     []int     `json:"power_usage,omitempty"`
}

// AllocateMiningGPURequest represents a request to allocate mining GPUs.
type AllocateMiningGPURequest struct {
	GPUCount int    `json:"gpu_count"`
	GPUType  string `json:"gpu_type,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// ReleaseMiningGPURequest represents a request to release mining GPUs.
type ReleaseMiningGPURequest struct {
	GPUCount int    `json:"gpu_count"`
	GPUType  string `json:"gpu_type,omitempty"`
}

// StartMiningRequest represents a request to start mining.
type StartMiningRequest struct {
	Image         string            `json:"image,omitempty"`
	GPUCount      int               `json:"gpu_count"`
	CPUCores      int               `json:"cpu_cores"`
	MemoryMB      int64             `json:"memory_mb"`
	WalletAddress string            `json:"wallet_address"`
	ExtraArgs     []string          `json:"extra_args,omitempty"`
	EnvVars       map[string]string `json:"env_vars,omitempty"`

	// Full Node settings
	Bootnodes   []string `json:"bootnodes,omitempty"`
	NetworkID   int      `json:"network_id,omitempty"`
	NodeDataDir string   `json:"node_data_dir,omitempty"`
	P2PPort     int      `json:"p2p_port,omitempty"`
	RPCEnabled  bool     `json:"rpc_enabled,omitempty"`
	RPCPort     int      `json:"rpc_port,omitempty"`
}

// APIResponse represents a generic API response.
type APIResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message,omitempty"`
	Error   string      `json:"error,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

// GetMiningStatus retrieves the current mining status.
func (c *APIClient) GetMiningStatus(ctx context.Context) (*MiningStatusResponse, error) {
	url := fmt.Sprintf("%s/api/v1/providers/%s/mining", c.baseURL, c.providerID)

	resp, err := c.doRequest(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp)
	}

	var status MiningStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &status, nil
}

// AllocateMiningGPU allocates additional GPUs for mining.
func (c *APIClient) AllocateMiningGPU(ctx context.Context, gpuCount int, reason string) error {
	url := fmt.Sprintf("%s/api/v1/providers/%s/mining/allocate", c.baseURL, c.providerID)

	req := AllocateMiningGPURequest{
		GPUCount: gpuCount,
		Reason:   reason,
	}

	resp, err := c.doRequest(ctx, "POST", url, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.parseError(resp)
	}

	return nil
}

// ReleaseMiningGPU releases GPUs from mining.
func (c *APIClient) ReleaseMiningGPU(ctx context.Context, gpuCount int) error {
	url := fmt.Sprintf("%s/api/v1/providers/%s/mining/release", c.baseURL, c.providerID)

	req := ReleaseMiningGPURequest{
		GPUCount: gpuCount,
	}

	resp, err := c.doRequest(ctx, "POST", url, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.parseError(resp)
	}

	return nil
}

// StartMining starts the mining pod.
func (c *APIClient) StartMining(ctx context.Context, req *StartMiningRequest) error {
	url := fmt.Sprintf("%s/api/v1/providers/%s/mining/start", c.baseURL, c.providerID)

	resp, err := c.doRequest(ctx, "POST", url, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.parseError(resp)
	}

	return nil
}

// StopMining stops the mining pod.
func (c *APIClient) StopMining(ctx context.Context) error {
	url := fmt.Sprintf("%s/api/v1/providers/%s/mining/stop", c.baseURL, c.providerID)

	resp, err := c.doRequest(ctx, "POST", url, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.parseError(resp)
	}

	return nil
}

// HealthCheck checks if the API server is healthy.
func (c *APIClient) HealthCheck(ctx context.Context) error {
	url := fmt.Sprintf("%s/health", c.baseURL)

	resp, err := c.doRequest(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check failed: status %d", resp.StatusCode)
	}

	return nil
}

func (c *APIClient) doRequest(ctx context.Context, method, url string, body interface{}) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(jsonBody)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Provider-ID", c.providerID)

	return c.httpClient.Do(req)
}

func (c *APIClient) parseError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)

	var apiResp APIResponse
	if err := json.Unmarshal(body, &apiResp); err == nil {
		if apiResp.Error != "" {
			return fmt.Errorf("%s: %s", apiResp.Error, apiResp.Message)
		}
		if apiResp.Message != "" {
			return fmt.Errorf("%s", apiResp.Message)
		}
	}

	return fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(body))
}
