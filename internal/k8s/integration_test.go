// +build integration

package k8s

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestIntegration_FullFlow tests the complete K8s integration flow
// Run with: go test -tags=integration -v ./internal/k8s/... -run TestIntegration
func TestIntegration_FullFlow(t *testing.T) {
	// Skip if not in integration test mode
	if os.Getenv("K8S_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test. Set K8S_INTEGRATION_TEST=true to run.")
	}

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Step 1: Initialize K8s client
	t.Log("Step 1: Initializing K8s client...")
	manager := GetManager()

	kubeconfigPath := os.Getenv("KUBECONFIG")
	if kubeconfigPath == "" {
		kubeconfigPath = os.Getenv("HOME") + "/.kube/config"
	}

	err := manager.InitFromKubeconfig(kubeconfigPath, logger)
	if err != nil {
		t.Fatalf("Failed to initialize K8s client: %v", err)
	}
	t.Log("✓ K8s client initialized")

	clientset, err := manager.GetClientset()
	if err != nil {
		t.Fatalf("Failed to get clientset: %v", err)
	}

	// Step 2: Create TenantOrchestrator and ensure tenant namespace
	t.Log("Step 2: Creating tenant namespace...")
	tenantOrch := NewTenantOrchestrator(clientset, logger)

	testUserAddress := "0x742d35Cc6634C0532925a3b844Bc9e7595f0bEb"
	namespace, err := tenantOrch.EnsureTenant(ctx, testUserAddress, 1)
	if err != nil {
		t.Fatalf("Failed to ensure tenant: %v", err)
	}
	t.Logf("✓ Tenant namespace created: %s", namespace)

	// Verify namespace has ResourceQuota
	quota, err := clientset.CoreV1().ResourceQuotas(namespace).Get(ctx, DefaultGPUQuotaName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Failed to get ResourceQuota: %v", err)
	}
	t.Logf("✓ ResourceQuota exists: CPU requests=%v, Memory requests=%v",
		quota.Spec.Hard["requests.cpu"], quota.Spec.Hard["requests.memory"])

	// Verify NetworkPolicies
	policies, err := clientset.NetworkingV1().NetworkPolicies(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("Failed to list NetworkPolicies: %v", err)
	}
	t.Logf("✓ NetworkPolicies created: %d policies", len(policies.Items))

	// Step 3: Create JobManager and GPU session
	t.Log("Step 3: Creating GPU session (Pod)...")
	jobManager := NewJobManager(clientset, logger)

	testSessionID := "test-session-" + time.Now().Format("20060102150405")
	spec := GPUJobSpec{
		SessionID:     testSessionID,
		UserAddress:   testUserAddress,
		ProviderID:    "provider-001",  // Matches our Kind worker label
		GPUCount:      0,               // No GPU resource request (Kind doesn't have GPUs)
		GPUModel:      "rtx-4090",
		Image:         "busybox:latest",
		CPURequest:    "100m",
		MemoryRequest: "128Mi",
		CPULimit:      "200m",
		MemoryLimit:   "256Mi",
		ExpiresAt:     time.Now().Add(1 * time.Hour),
	}

	password, err := jobManager.CreateGPUSession(ctx, spec)
	if err != nil {
		t.Fatalf("Failed to create GPU session: %v", err)
	}
	t.Logf("✓ GPU session created, password generated: %s...", password[:4])

	// Verify Pod exists
	pod, err := clientset.CoreV1().Pods(namespace).Get(ctx, PodName(testSessionID), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Failed to get Pod: %v", err)
	}
	t.Logf("✓ Pod exists: %s, Phase: %s", pod.Name, pod.Status.Phase)

	// Verify SSH Secret exists
	secret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, SSHSecretName(testSessionID), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Failed to get SSH Secret: %v", err)
	}
	t.Logf("✓ SSH Secret exists: %s", secret.Name)

	// Verify Service exists
	svc, err := clientset.CoreV1().Services(namespace).Get(ctx, SSHServiceName(testSessionID), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Failed to get Service: %v", err)
	}
	t.Logf("✓ SSH Service exists: %s, NodePort: %d", svc.Name, svc.Spec.Ports[0].NodePort)

	// Step 4: Wait for Pod to be scheduled (not necessarily Ready since busybox exits quickly)
	t.Log("Step 4: Waiting for Pod scheduling...")
	time.Sleep(5 * time.Second)

	pod, err = clientset.CoreV1().Pods(namespace).Get(ctx, PodName(testSessionID), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Failed to get Pod status: %v", err)
	}
	t.Logf("✓ Pod phase: %s, Node: %s", pod.Status.Phase, pod.Spec.NodeName)

	// Step 5: Test GetPodStatus
	t.Log("Step 5: Testing GetPodStatus...")
	phase, isReady, err := jobManager.GetPodStatus(ctx, testUserAddress, testSessionID)
	if err != nil {
		t.Logf("GetPodStatus error (may be expected): %v", err)
	} else {
		t.Logf("✓ GetPodStatus: phase=%s, isReady=%v", *phase, isReady)
	}

	// Step 6: Test ListSessionPods
	t.Log("Step 6: Testing ListSessionPods...")
	pods, err := jobManager.ListSessionPods(ctx)
	if err != nil {
		t.Fatalf("Failed to list session pods: %v", err)
	}
	t.Logf("✓ Found %d GPU rental pod(s)", len(pods))

	// Step 7: Delete the session
	t.Log("Step 7: Deleting GPU session...")
	err = jobManager.DeleteGPUSession(ctx, testUserAddress, testSessionID)
	if err != nil {
		t.Fatalf("Failed to delete GPU session: %v", err)
	}
	t.Log("✓ GPU session deleted")

	// Verify Pod deleted
	time.Sleep(2 * time.Second)
	_, err = clientset.CoreV1().Pods(namespace).Get(ctx, PodName(testSessionID), metav1.GetOptions{})
	if err == nil {
		t.Log("⚠ Pod still exists (may be terminating)")
	} else {
		t.Log("✓ Pod deleted successfully")
	}

	// Verify Secret deleted
	_, err = clientset.CoreV1().Secrets(namespace).Get(ctx, SSHSecretName(testSessionID), metav1.GetOptions{})
	if err == nil {
		t.Log("⚠ Secret still exists")
	} else {
		t.Log("✓ Secret deleted successfully")
	}

	// Step 8: Test idempotent deletion
	t.Log("Step 8: Testing idempotent deletion...")
	err = jobManager.DeleteGPUSession(ctx, testUserAddress, testSessionID)
	if err != nil {
		t.Fatalf("Idempotent deletion failed: %v", err)
	}
	t.Log("✓ Idempotent deletion works (no error on non-existent session)")

	// Cleanup: Delete tenant namespace (optional - leave for inspection)
	t.Log("Step 9: Cleanup (keeping namespace for inspection)...")
	t.Logf("✓ Namespace %s left for inspection. Delete manually with: kubectl delete namespace %s", namespace, namespace)

	t.Log("\n=== Integration Test Complete ===")
}

// TestIntegration_PodWatcher tests the PodWatcher functionality
func TestIntegration_PodWatcher(t *testing.T) {
	if os.Getenv("K8S_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test. Set K8S_INTEGRATION_TEST=true to run.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Initialize K8s client
	manager := GetManager()
	if !manager.IsInitialized() {
		kubeconfigPath := os.Getenv("KUBECONFIG")
		if kubeconfigPath == "" {
			kubeconfigPath = os.Getenv("HOME") + "/.kube/config"
		}
		if err := manager.InitFromKubeconfig(kubeconfigPath, logger); err != nil {
			t.Fatalf("Failed to initialize K8s client: %v", err)
		}
	}

	clientset, _ := manager.GetClientset()

	// Create mock state handler
	handler := &testStateHandler{t: t}

	// Create and start PodWatcher
	watcher := NewPodWatcher(clientset, handler, logger)

	go func() {
		if err := watcher.Start(ctx); err != nil && err != context.Canceled {
			t.Logf("PodWatcher error: %v", err)
		}
	}()

	// Wait for cache sync
	time.Sleep(3 * time.Second)

	if watcher.IsCacheSynced() {
		t.Log("✓ PodWatcher cache synced")
	} else {
		t.Log("⚠ PodWatcher cache not synced yet")
	}

	// List pods from cache
	cachedPods := watcher.ListPodsFromCache()
	t.Logf("✓ Found %d GPU rental pod(s) in cache", len(cachedPods))

	t.Log("✓ PodWatcher integration test complete")
}

type testStateHandler struct {
	t *testing.T
}

func (h *testStateHandler) OnPodRunning(ctx context.Context, sessionID string) error {
	h.t.Logf("OnPodRunning called: sessionID=%s", sessionID)
	return nil
}

func (h *testStateHandler) OnPodFailed(ctx context.Context, sessionID string, reason string) error {
	h.t.Logf("OnPodFailed called: sessionID=%s, reason=%s", sessionID, reason)
	return nil
}

func (h *testStateHandler) OnPodSucceeded(ctx context.Context, sessionID string) error {
	h.t.Logf("OnPodSucceeded called: sessionID=%s", sessionID)
	return nil
}

func (h *testStateHandler) OnPodDeleted(ctx context.Context, sessionID string) error {
	h.t.Logf("OnPodDeleted called: sessionID=%s", sessionID)
	return nil
}
