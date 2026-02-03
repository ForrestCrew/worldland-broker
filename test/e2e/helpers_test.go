//go:build e2e
// +build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/e2e-framework/klient"
)

// Config holds E2E test configuration from environment variables
type Config struct {
	HubURL       string
	AuthDisabled bool
	HubTimeout   time.Duration
}

// DefaultConfig returns the default E2E test configuration
func DefaultConfig() Config {
	hubURL := os.Getenv("HUB_URL")
	if hubURL == "" {
		hubURL = "http://localhost:8080"
	}

	authDisabled := os.Getenv("AUTH_DISABLED") == "true"

	return Config{
		HubURL:       hubURL,
		AuthDisabled: authDisabled,
		HubTimeout:   30 * time.Second,
	}
}

// HubClient is an HTTP client for the Hub API used in E2E tests
type HubClient struct {
	baseURL    string
	httpClient *http.Client
	authToken  string
}

// NewHubClient creates a new Hub API client
func NewHubClient(baseURL string) *HubClient {
	return &HubClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// SetAuthToken sets the JWT auth token for authenticated requests
func (c *HubClient) SetAuthToken(token string) {
	c.authToken = token
}

// ListImages fetches the list of preset images from the Hub API
// GET /api/v1/images (public endpoint)
func (c *HubClient) ListImages(ctx context.Context) ([]map[string]interface{}, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/images", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Images       []map[string]interface{} `json:"images"`
		DefaultImage string                   `json:"defaultImage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return result.Images, nil
}

// CreateSession creates a new rental session
// POST /api/v1/rentals (requires auth)
func (c *HubClient) CreateSession(ctx context.Context, nodeID, pricePerSecond, dockerImage string) (map[string]interface{}, error) {
	body := map[string]interface{}{
		"nodeId":         nodeID,
		"pricePerSecond": pricePerSecond,
	}
	if dockerImage != "" {
		body["image"] = dockerImage
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/rentals", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return result, nil
}

// GetSession fetches session details
// GET /api/v1/rentals/:id (requires auth)
func (c *HubClient) GetSession(ctx context.Context, sessionID string) (map[string]interface{}, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/rentals/"+sessionID, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return result, nil
}

// ConfirmSession confirms a rental session with txHash
// POST /api/v1/rentals/:id/confirm (requires auth)
func (c *HubClient) ConfirmSession(ctx context.Context, sessionID, txHash string) (map[string]interface{}, error) {
	body := map[string]interface{}{
		"txHash": txHash,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/rentals/"+sessionID+"/confirm", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Accept 200, 202 (Accepted - verification pending)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return result, nil
}

// TerminateSession terminates a rental session
// POST /api/v1/rentals/:id/terminate (requires auth)
func (c *HubClient) TerminateSession(ctx context.Context, sessionID string) (map[string]interface{}, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/rentals/"+sessionID+"/terminate", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return result, nil
}

// GetSettlement fetches settlement details for a session
// GET /api/v1/rentals/:id/settlement (requires auth)
func (c *HubClient) GetSettlement(ctx context.Context, sessionID string) (map[string]interface{}, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/rentals/"+sessionID+"/settlement", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return result, nil
}

// ListNodes returns all registered nodes from Hub
// GET /api/v1/nodes (public endpoint)
func (c *HubClient) ListNodes(ctx context.Context) ([]map[string]interface{}, error) {
	resp, err := c.doRequest(ctx, "GET", "/api/v1/nodes", nil)
	if err != nil {
		return nil, err
	}

	// Handle both array and object with "nodes" field
	if nodes, ok := resp["nodes"].([]interface{}); ok {
		result := make([]map[string]interface{}, 0, len(nodes))
		for _, n := range nodes {
			if node, ok := n.(map[string]interface{}); ok {
				result = append(result, node)
			}
		}
		return result, nil
	}

	// If response is directly an array
	if respArray, ok := resp["data"].([]interface{}); ok {
		result := make([]map[string]interface{}, 0, len(respArray))
		for _, n := range respArray {
			if node, ok := n.(map[string]interface{}); ok {
				result = append(result, node)
			}
		}
		return result, nil
	}

	return nil, fmt.Errorf("unexpected response format: %+v", resp)
}

// DiscoverNodes returns available nodes matching filter criteria
// GET /api/v1/nodes/available (public endpoint)
func (c *HubClient) DiscoverNodes(ctx context.Context, filter map[string]interface{}) ([]map[string]interface{}, error) {
	// Build query params from filter if provided
	endpoint := "/api/v1/nodes/available"
	if filter != nil {
		// Could add query params here for filtering
		// For now, just use basic endpoint
	}

	resp, err := c.doRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		// Fall back to listing all nodes if discover endpoint doesn't exist
		return c.ListNodes(ctx)
	}

	if nodes, ok := resp["nodes"].([]interface{}); ok {
		result := make([]map[string]interface{}, 0, len(nodes))
		for _, n := range nodes {
			if node, ok := n.(map[string]interface{}); ok {
				result = append(result, node)
			}
		}
		return result, nil
	}

	// Try data field
	if nodes, ok := resp["data"].([]interface{}); ok {
		result := make([]map[string]interface{}, 0, len(nodes))
		for _, n := range nodes {
			if node, ok := n.(map[string]interface{}); ok {
				result = append(result, node)
			}
		}
		return result, nil
	}

	return nil, fmt.Errorf("unexpected response format: %+v", resp)
}

// ListSessions returns all sessions from Hub
// GET /api/v1/sessions (requires auth)
func (c *HubClient) ListSessions(ctx context.Context) ([]map[string]interface{}, error) {
	resp, err := c.doRequest(ctx, "GET", "/api/v1/sessions", nil)
	if err != nil {
		return nil, err
	}

	if sessions, ok := resp["sessions"].([]interface{}); ok {
		result := make([]map[string]interface{}, 0, len(sessions))
		for _, s := range sessions {
			if session, ok := s.(map[string]interface{}); ok {
				result = append(result, session)
			}
		}
		return result, nil
	}

	if sessions, ok := resp["data"].([]interface{}); ok {
		result := make([]map[string]interface{}, 0, len(sessions))
		for _, s := range sessions {
			if session, ok := s.(map[string]interface{}); ok {
				result = append(result, session)
			}
		}
		return result, nil
	}

	return nil, fmt.Errorf("unexpected response format: %+v", resp)
}

// doRequest is a helper for making HTTP requests with common handling
func (c *HubClient) doRequest(ctx context.Context, method, path string, body map[string]interface{}) (map[string]interface{}, error) {
	var reqBody io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request: %w", err)
		}
		reqBody = bytes.NewReader(jsonBody)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return result, nil
}

// Health checks if the Hub server is reachable
// GET /health
func (c *HubClient) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	return nil
}

// isHubAvailable checks if the Hub server is reachable
func isHubAvailable(ctx context.Context, client *HubClient) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return client.Health(ctx) == nil
}

// SetupTestAuth creates a SIWE token for the test user and sets it on the HubClient.
// This allows E2E tests to run with auth enabled.
// If AUTH_DISABLED=true on Hub, this can be skipped.
func SetupTestAuth(ctx context.Context, client *HubClient, userAddress string) error {
	// Implementation should:
	// 1. Generate SIWE message for userAddress
	// 2. Sign with test wallet private key (hardhat account #1)
	// 3. POST to /api/v1/auth/login to get JWT
	// 4. Set JWT on client for subsequent requests
	//
	// For Phase 24 E2E, recommend running Hub with AUTH_DISABLED=true
	// to isolate K8s flow testing from auth concerns.
	return nil // Stub - actual implementation requires eth signer
}

// ========================================
// Kubernetes Helpers
// ========================================

// WaitForPodReady waits for a pod to be ready
func WaitForPodReady(ctx context.Context, client klient.Client, namespace, podName string, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		pod := &corev1.Pod{}
		err := client.Resources(namespace).Get(ctx, podName, namespace, pod)
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil // Pod doesn't exist yet, keep polling
			}
			return false, err // Real error
		}

		// Check if pod is ready
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
				return true, nil
			}
		}

		// Check if pod failed
		if pod.Status.Phase == corev1.PodFailed {
			return false, fmt.Errorf("pod %s/%s failed", namespace, podName)
		}

		return false, nil
	})
}

// WaitForPodPhase waits for a pod to reach a specific phase
func WaitForPodPhase(ctx context.Context, client klient.Client, namespace, podName string, phase corev1.PodPhase, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		pod := &corev1.Pod{}
		err := client.Resources(namespace).Get(ctx, podName, namespace, pod)
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil // Pod doesn't exist yet, keep polling
			}
			return false, err // Real error
		}

		return pod.Status.Phase == phase, nil
	})
}

// WaitForServiceEndpoints waits for a service to have endpoints
func WaitForServiceEndpoints(ctx context.Context, client klient.Client, namespace, serviceName string, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		endpoints := &corev1.Endpoints{}
		err := client.Resources(namespace).Get(ctx, serviceName, namespace, endpoints)
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}

		// Check if endpoints have addresses
		for _, subset := range endpoints.Subsets {
			if len(subset.Addresses) > 0 {
				return true, nil
			}
		}

		return false, nil
	})
}

// CreateTestPod creates a test pod for E2E testing
func CreateTestPod(ctx context.Context, client klient.Client, namespace, name, image string) (*corev1.Pod, error) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app":                          name,
				"app.kubernetes.io/managed-by": "e2e-test",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:    "main",
					Image:   image,
					Command: []string{"sleep", "infinity"},
				},
			},
			RestartPolicy: corev1.RestartPolicyNever,
		},
	}

	if err := client.Resources(namespace).Create(ctx, pod); err != nil {
		return nil, fmt.Errorf("failed to create pod: %w", err)
	}

	return pod, nil
}

// DeleteTestPod deletes a test pod
func DeleteTestPod(ctx context.Context, client klient.Client, namespace, name string) error {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	if err := client.Resources(namespace).Delete(ctx, pod); err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("failed to delete pod: %w", err)
		}
	}

	return nil
}

// ========================================
// Hardhat Node Helpers
// ========================================

// HardhatClient wraps HTTP calls to Hardhat node
type HardhatClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewHardhatClient creates a new HardhatClient
func NewHardhatClient(baseURL string) *HardhatClient {
	return &HardhatClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// JSONRPCRequest represents a JSON-RPC request
type JSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
	ID      int         `json:"id"`
}

// JSONRPCResponse represents a JSON-RPC response
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

// JSONRPCError represents a JSON-RPC error
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Call performs a JSON-RPC call
func (c *HardhatClient) Call(ctx context.Context, method string, params interface{}, result interface{}) error {
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      1,
	}

	jsonBody, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	var rpcResp JSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	if rpcResp.Error != nil {
		return fmt.Errorf("RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	if result != nil {
		if err := json.Unmarshal(rpcResp.Result, result); err != nil {
			return fmt.Errorf("failed to unmarshal result: %w", err)
		}
	}

	return nil
}

// GetBlockNumber gets the current block number
func (c *HardhatClient) GetBlockNumber(ctx context.Context) (uint64, error) {
	var result string
	if err := c.Call(ctx, "eth_blockNumber", []interface{}{}, &result); err != nil {
		return 0, err
	}

	// Parse hex string to uint64
	var blockNum uint64
	fmt.Sscanf(result, "0x%x", &blockNum)
	return blockNum, nil
}

// MineBlock mines a new block (Hardhat specific)
func (c *HardhatClient) MineBlock(ctx context.Context) error {
	return c.Call(ctx, "evm_mine", []interface{}{}, nil)
}

// IsHealthy checks if the Hardhat node is running
func (c *HardhatClient) IsHealthy(ctx context.Context) bool {
	_, err := c.GetBlockNumber(ctx)
	return err == nil
}

// WaitForHardhat waits for Hardhat node to be ready
func WaitForHardhat(ctx context.Context, url string, timeout time.Duration) error {
	client := NewHardhatClient(url)
	return wait.PollUntilContextTimeout(ctx, time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		return client.IsHealthy(ctx), nil
	})
}

// WaitForHub waits for Hub server to be ready
func WaitForHub(ctx context.Context, url string, timeout time.Duration) error {
	client := NewHubClient(url)
	return wait.PollUntilContextTimeout(ctx, time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		err := client.Health(ctx)
		return err == nil, nil
	})
}
