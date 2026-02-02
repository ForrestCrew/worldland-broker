package k8s

import (
	"context"
	"log/slog"
	"os"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestEnsureTenant_CreatesNamespace(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	orchestrator := NewTenantOrchestrator(clientset, logger)

	ctx := context.Background()
	userAddress := "0x742d35Cc6634C0532925a3b844Bc9e7595f0bEb"
	gpuCount := 2

	// Act
	namespace, err := orchestrator.EnsureTenant(ctx, userAddress, gpuCount)

	// Assert
	if err != nil {
		t.Fatalf("EnsureTenant failed: %v", err)
	}

	expectedNamespace := TenantNamespace(userAddress)
	if namespace != expectedNamespace {
		t.Errorf("expected namespace %s, got %s", expectedNamespace, namespace)
	}

	// Verify namespace created
	ns, err := clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("namespace not created: %v", err)
	}

	if ns.Labels["worldland.io/tenant"] != "true" {
		t.Errorf("expected tenant label to be 'true', got %s", ns.Labels["worldland.io/tenant"])
	}

	if ns.Labels["worldland.io/user-address"] != userAddress {
		t.Errorf("expected user-address label to be %s, got %s", userAddress, ns.Labels["worldland.io/user-address"])
	}

	// Verify ResourceQuota created
	quota, err := clientset.CoreV1().ResourceQuotas(namespace).Get(ctx, DefaultGPUQuotaName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("resource quota not created: %v", err)
	}

	gpuRequest := quota.Spec.Hard["requests.nvidia.com/gpu"]
	if gpuRequest.String() != "2" {
		t.Errorf("expected GPU request to be 2, got %s", gpuRequest.String())
	}

	cpuRequest := quota.Spec.Hard["requests.cpu"]
	if cpuRequest.String() != "8" {
		t.Errorf("expected CPU request to be 8, got %s", cpuRequest.String())
	}

	memoryRequest := quota.Spec.Hard["requests.memory"]
	if memoryRequest.String() != "32Gi" {
		t.Errorf("expected memory request to be 32Gi, got %s", memoryRequest.String())
	}

	// Verify 3 NetworkPolicies created
	policies, err := clientset.NetworkingV1().NetworkPolicies(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("failed to list network policies: %v", err)
	}

	if len(policies.Items) != 3 {
		t.Errorf("expected 3 network policies, got %d", len(policies.Items))
	}

	policyNames := make(map[string]bool)
	for _, policy := range policies.Items {
		policyNames[policy.Name] = true
	}

	expectedPolicies := []string{"allow-internal", "allow-dns", "allow-egress"}
	for _, expected := range expectedPolicies {
		if !policyNames[expected] {
			t.Errorf("expected network policy %s not found", expected)
		}
	}
}

func TestEnsureTenant_Idempotent(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	orchestrator := NewTenantOrchestrator(clientset, logger)

	ctx := context.Background()
	userAddress := "0x742d35Cc6634C0532925a3b844Bc9e7595f0bEb"
	gpuCount := 2

	// First call - create tenant
	namespace1, err := orchestrator.EnsureTenant(ctx, userAddress, gpuCount)
	if err != nil {
		t.Fatalf("first EnsureTenant failed: %v", err)
	}

	// Second call - should be idempotent
	namespace2, err := orchestrator.EnsureTenant(ctx, userAddress, gpuCount)
	if err != nil {
		t.Fatalf("second EnsureTenant failed: %v", err)
	}

	// Assert same namespace returned
	if namespace1 != namespace2 {
		t.Errorf("expected same namespace, got %s and %s", namespace1, namespace2)
	}

	// Verify no duplicate resources
	namespaces, err := clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("failed to list namespaces: %v", err)
	}

	count := 0
	for _, ns := range namespaces.Items {
		if ns.Name == namespace1 {
			count++
		}
	}

	if count != 1 {
		t.Errorf("expected 1 namespace, got %d", count)
	}
}

func TestDeleteTenant_RemovesNamespace(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	orchestrator := NewTenantOrchestrator(clientset, logger)

	ctx := context.Background()
	userAddress := "0x742d35Cc6634C0532925a3b844Bc9e7595f0bEb"
	gpuCount := 2

	// Create tenant first
	namespace, err := orchestrator.EnsureTenant(ctx, userAddress, gpuCount)
	if err != nil {
		t.Fatalf("EnsureTenant failed: %v", err)
	}

	// Verify namespace exists
	_, err = clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("namespace should exist: %v", err)
	}

	// Delete tenant
	err = orchestrator.DeleteTenant(ctx, userAddress)
	if err != nil {
		t.Fatalf("DeleteTenant failed: %v", err)
	}

	// Verify namespace deleted
	_, err = clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err == nil {
		t.Error("namespace should be deleted")
	}
}

func TestDeleteTenant_NotFoundIgnored(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	orchestrator := NewTenantOrchestrator(clientset, logger)

	ctx := context.Background()
	userAddress := "0x742d35Cc6634C0532925a3b844Bc9e7595f0bEb"

	// Delete non-existent namespace - should not error
	err := orchestrator.DeleteTenant(ctx, userAddress)
	if err != nil {
		t.Fatalf("DeleteTenant should be idempotent for non-existent namespace: %v", err)
	}
}

func TestTenantExists(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	orchestrator := NewTenantOrchestrator(clientset, logger)

	ctx := context.Background()
	userAddress := "0x742d35Cc6634C0532925a3b844Bc9e7595f0bEb"

	// Test non-existent namespace
	exists, err := orchestrator.TenantExists(ctx, userAddress)
	if err != nil {
		t.Fatalf("TenantExists failed: %v", err)
	}
	if exists {
		t.Error("expected namespace to not exist")
	}

	// Create tenant
	_, err = orchestrator.EnsureTenant(ctx, userAddress, 2)
	if err != nil {
		t.Fatalf("EnsureTenant failed: %v", err)
	}

	// Test existing namespace
	exists, err = orchestrator.TenantExists(ctx, userAddress)
	if err != nil {
		t.Fatalf("TenantExists failed: %v", err)
	}
	if !exists {
		t.Error("expected namespace to exist")
	}
}

func TestResourceQuotaCalculation(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	orchestrator := NewTenantOrchestrator(clientset, logger)

	ctx := context.Background()

	tests := []struct {
		name                string
		gpuCount            int
		expectedCPURequest  string
		expectedCPULimit    string
		expectedMemRequest  string
		expectedMemLimit    string
	}{
		{
			name:               "1 GPU",
			gpuCount:           1,
			expectedCPURequest: "4",
			expectedCPULimit:   "8",
			expectedMemRequest: "16Gi",
			expectedMemLimit:   "32Gi",
		},
		{
			name:               "2 GPUs",
			gpuCount:           2,
			expectedCPURequest: "8",
			expectedCPULimit:   "16",
			expectedMemRequest: "32Gi",
			expectedMemLimit:   "64Gi",
		},
		{
			name:               "4 GPUs",
			gpuCount:           4,
			expectedCPURequest: "16",
			expectedCPULimit:   "32",
			expectedMemRequest: "64Gi",
			expectedMemLimit:   "128Gi",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userAddress := "0x" + tt.name // Unique address per test
			namespace, err := orchestrator.EnsureTenant(ctx, userAddress, tt.gpuCount)
			if err != nil {
				t.Fatalf("EnsureTenant failed: %v", err)
			}

			quota, err := clientset.CoreV1().ResourceQuotas(namespace).Get(ctx, DefaultGPUQuotaName, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("failed to get resource quota: %v", err)
			}

			cpuRequest := quota.Spec.Hard["requests.cpu"]
			if cpuRequest.String() != tt.expectedCPURequest {
				t.Errorf("expected CPU request %s, got %s", tt.expectedCPURequest, cpuRequest.String())
			}

			cpuLimit := quota.Spec.Hard["limits.cpu"]
			if cpuLimit.String() != tt.expectedCPULimit {
				t.Errorf("expected CPU limit %s, got %s", tt.expectedCPULimit, cpuLimit.String())
			}

			memRequest := quota.Spec.Hard["requests.memory"]
			if memRequest.String() != tt.expectedMemRequest {
				t.Errorf("expected memory request %s, got %s", tt.expectedMemRequest, memRequest.String())
			}

			memLimit := quota.Spec.Hard["limits.memory"]
			if memLimit.String() != tt.expectedMemLimit {
				t.Errorf("expected memory limit %s, got %s", tt.expectedMemLimit, memLimit.String())
			}
		})
	}
}

func TestNetworkPolicies(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	orchestrator := NewTenantOrchestrator(clientset, logger)

	ctx := context.Background()
	userAddress := "0x742d35Cc6634C0532925a3b844Bc9e7595f0bEb"
	gpuCount := 2

	namespace, err := orchestrator.EnsureTenant(ctx, userAddress, gpuCount)
	if err != nil {
		t.Fatalf("EnsureTenant failed: %v", err)
	}

	// Verify allow-internal policy
	allowInternal, err := clientset.NetworkingV1().NetworkPolicies(namespace).Get(ctx, "allow-internal", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("allow-internal policy not found: %v", err)
	}

	if len(allowInternal.Spec.PolicyTypes) != 1 || allowInternal.Spec.PolicyTypes[0] != networkingv1.PolicyTypeIngress {
		t.Error("allow-internal should have Ingress policy type")
	}

	if len(allowInternal.Spec.Ingress) != 1 {
		t.Errorf("expected 1 ingress rule, got %d", len(allowInternal.Spec.Ingress))
	}

	// Verify allow-dns policy
	allowDNS, err := clientset.NetworkingV1().NetworkPolicies(namespace).Get(ctx, "allow-dns", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("allow-dns policy not found: %v", err)
	}

	if len(allowDNS.Spec.PolicyTypes) != 1 || allowDNS.Spec.PolicyTypes[0] != networkingv1.PolicyTypeEgress {
		t.Error("allow-dns should have Egress policy type")
	}

	if len(allowDNS.Spec.Egress) != 1 {
		t.Errorf("expected 1 egress rule, got %d", len(allowDNS.Spec.Egress))
	}

	// Verify allow-egress policy
	allowEgress, err := clientset.NetworkingV1().NetworkPolicies(namespace).Get(ctx, "allow-egress", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("allow-egress policy not found: %v", err)
	}

	if len(allowEgress.Spec.PolicyTypes) != 1 || allowEgress.Spec.PolicyTypes[0] != networkingv1.PolicyTypeEgress {
		t.Error("allow-egress should have Egress policy type")
	}

	if len(allowEgress.Spec.Egress) != 1 {
		t.Errorf("expected 1 egress rule, got %d", len(allowEgress.Spec.Egress))
	}

	// Verify CIDR blocks in allow-egress
	if allowEgress.Spec.Egress[0].To[0].IPBlock == nil {
		t.Fatal("allow-egress should have IPBlock")
	}

	ipBlock := allowEgress.Spec.Egress[0].To[0].IPBlock
	if ipBlock.CIDR != "0.0.0.0/0" {
		t.Errorf("expected CIDR 0.0.0.0/0, got %s", ipBlock.CIDR)
	}

	expectedExcept := []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}
	if len(ipBlock.Except) != len(expectedExcept) {
		t.Errorf("expected %d except ranges, got %d", len(expectedExcept), len(ipBlock.Except))
	}

	for i, expected := range expectedExcept {
		if ipBlock.Except[i] != expected {
			t.Errorf("expected except[%d] to be %s, got %s", i, expected, ipBlock.Except[i])
		}
	}
}
