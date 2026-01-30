package rental

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartRental_Success(t *testing.T) {
	// Create test server returning valid response
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/rentals/start", r.URL.Path)
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var req StartRentalRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		require.NoError(t, err)
		assert.Equal(t, "session-123", req.SessionID)
		assert.Equal(t, "GPU-xxx", req.GPUDeviceID)
		assert.Equal(t, "ssh-rsa AAAA...", req.SSHPublicKey)

		resp := StartRentalResponse{
			SessionID:  req.SessionID,
			SSHHost:    "node.example.com",
			SSHPort:    30001,
			SSHUser:    "ubuntu",
			SSHCommand: "ssh -p 30001 ubuntu@node.example.com",
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewNodeClient(NodeClientConfig{
		TLSConfig: server.Client().Transport.(*http.Transport).TLSClientConfig,
	})

	resp, err := client.StartRental(context.Background(), server.URL, StartRentalRequest{
		SessionID:    "session-123",
		GPUDeviceID:  "GPU-xxx",
		SSHPublicKey: "ssh-rsa AAAA...",
	})

	require.NoError(t, err)
	assert.Equal(t, "session-123", resp.SessionID)
	assert.Equal(t, "node.example.com", resp.SSHHost)
	assert.Equal(t, 30001, resp.SSHPort)
	assert.Equal(t, "ubuntu", resp.SSHUser)
	assert.Equal(t, "ssh -p 30001 ubuntu@node.example.com", resp.SSHCommand)
}

func TestStartRental_NodeError_ReturnsError(t *testing.T) {
	// Server returns 500 with error response
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		errResp := NodeErrorResponse{
			Error: "container failed to start",
			Code:  "CONTAINER_ERROR",
		}
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(errResp)
	}))
	defer server.Close()

	client := NewNodeClient(NodeClientConfig{
		TLSConfig: server.Client().Transport.(*http.Transport).TLSClientConfig,
	})

	resp, err := client.StartRental(context.Background(), server.URL, StartRentalRequest{
		SessionID:    "session-123",
		GPUDeviceID:  "GPU-xxx",
		SSHPublicKey: "ssh-rsa AAAA...",
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.ErrorIs(t, err, ErrNodeStartFailed)
	assert.Contains(t, err.Error(), "container failed to start")
	assert.Contains(t, err.Error(), "CONTAINER_ERROR")
}

func TestStartRental_NodeUnreachable_ReturnsError(t *testing.T) {
	// Create client pointing to non-existent server
	client := NewNodeClient(NodeClientConfig{
		Timeout: 1 * time.Second,
	})

	resp, err := client.StartRental(context.Background(), "https://localhost:9999", StartRentalRequest{
		SessionID:    "session-123",
		GPUDeviceID:  "GPU-xxx",
		SSHPublicKey: "ssh-rsa AAAA...",
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.ErrorIs(t, err, ErrNodeUnreachable)
}

func TestStopRental_Success(t *testing.T) {
	// Create test server returning valid stop response
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/rentals/stop", r.URL.Path)
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var req StopRentalRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		require.NoError(t, err)
		assert.Equal(t, "session-123", req.SessionID)

		resp := StopRentalResponse{
			SessionID: req.SessionID,
			Message:   "Rental stopped successfully",
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewNodeClient(NodeClientConfig{
		TLSConfig: server.Client().Transport.(*http.Transport).TLSClientConfig,
	})

	resp, err := client.StopRental(context.Background(), server.URL, StopRentalRequest{
		SessionID: "session-123",
	})

	require.NoError(t, err)
	assert.Equal(t, "session-123", resp.SessionID)
	assert.Equal(t, "Rental stopped successfully", resp.Message)
}

func TestStopRental_NotFound_ReturnsError(t *testing.T) {
	// Server returns 404 - rental not found
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		errResp := NodeErrorResponse{
			Error: "rental not found",
			Code:  "NOT_FOUND",
		}
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(errResp)
	}))
	defer server.Close()

	client := NewNodeClient(NodeClientConfig{
		TLSConfig: server.Client().Transport.(*http.Transport).TLSClientConfig,
	})

	resp, err := client.StopRental(context.Background(), server.URL, StopRentalRequest{
		SessionID: "session-999",
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.ErrorIs(t, err, ErrNodeStopFailed)
	assert.Contains(t, err.Error(), "rental not found")
	assert.Contains(t, err.Error(), "NOT_FOUND")
}

func TestStartRental_ContextCancelled(t *testing.T) {
	// Create a slow server
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second) // Simulate slow response
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(StartRentalResponse{})
	}))
	defer server.Close()

	client := NewNodeClient(NodeClientConfig{
		TLSConfig: server.Client().Transport.(*http.Transport).TLSClientConfig,
		Timeout:   5 * time.Second,
	})

	// Create context that cancels immediately
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	resp, err := client.StartRental(ctx, server.URL, StartRentalRequest{
		SessionID:    "session-123",
		GPUDeviceID:  "GPU-xxx",
		SSHPublicKey: "ssh-rsa AAAA...",
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.ErrorIs(t, err, ErrNodeUnreachable)
}
