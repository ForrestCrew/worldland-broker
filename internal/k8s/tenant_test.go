package k8s

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestGetNamespaceName(t *testing.T) {
	tests := []struct {
		userID   string
		expected string
	}{
		{"user123", "tenant-user123"},
		{"alice", "tenant-alice"},
		{"bob-123", "tenant-bob-123"},
	}

	for _, tt := range tests {
		result := GetNamespaceName(tt.userID)
		if result != tt.expected {
			t.Errorf("GetNamespaceName(%s) = %s; want %s", tt.userID, result, tt.expected)
		}
	}
}

func TestTenantExists(t *testing.T) {
	ctx := context.Background()
	fakeClient := fake.NewSimpleClientset()
	tm := NewTenantManager(fakeClient)

	// Test: Namespace does not exist
	exists, err := tm.TenantExists(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("TenantExists returned error: %v", err)
	}
	if exists {
		t.Error("TenantExists returned true for non-existent namespace")
	}

	// Create a namespace
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "tenant-testuser"},
	}
	_, err = fakeClient.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create test namespace: %v", err)
	}

	// Test: Namespace exists
	exists, err = tm.TenantExists(ctx, "testuser")
	if err != nil {
		t.Fatalf("TenantExists returned error: %v", err)
	}
	if !exists {
		t.Error("TenantExists returned false for existing namespace")
	}
}

func TestCreateTenantEnvironment(t *testing.T) {
	ctx := context.Background()
	fakeClient := fake.NewSimpleClientset()
	tm := NewTenantManager(fakeClient)

	cfg := DefaultTenantConfig("user1", 2)

	// Create tenant environment
	err := tm.CreateTenantEnvironment(ctx, cfg)
	if err != nil {
		t.Fatalf("CreateTenantEnvironment failed: %v", err)
	}

	nsName := GetNamespaceName("user1")

	// Verify namespace was created
	ns, err := fakeClient.CoreV1().Namespaces().Get(ctx, nsName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Namespace not created: %v", err)
	}
	if ns.Labels["tenant"] != "user1" {
		t.Errorf("Namespace label tenant = %s; want user1", ns.Labels["tenant"])
	}

	// Verify resource quota was created
	quota, err := fakeClient.CoreV1().ResourceQuotas(nsName).Get(ctx, "gpu-quota", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("ResourceQuota not created: %v", err)
	}
	gpuRequest := quota.Spec.Hard["requests.nvidia.com/gpu"]
	if gpuRequest.Value() != 2 {
		t.Errorf("GPU quota = %d; want 2", gpuRequest.Value())
	}

	// Verify network policies were created
	policies, err := fakeClient.NetworkingV1().NetworkPolicies(nsName).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("Failed to list network policies: %v", err)
	}
	expectedPolicies := map[string]bool{
		"allow-internal":          false,
		"allow-ingress-whitelist": false,
		"allow-egress-essential":  false,
	}
	for _, p := range policies.Items {
		if _, ok := expectedPolicies[p.Name]; ok {
			expectedPolicies[p.Name] = true
		}
	}
	for name, found := range expectedPolicies {
		if !found {
			t.Errorf("NetworkPolicy %s was not created", name)
		}
	}
}

func TestCreateTenantEnvironment_Idempotent(t *testing.T) {
	ctx := context.Background()
	fakeClient := fake.NewSimpleClientset()
	tm := NewTenantManager(fakeClient)

	cfg := DefaultTenantConfig("user2", 1)

	// Create twice - should not error
	err := tm.CreateTenantEnvironment(ctx, cfg)
	if err != nil {
		t.Fatalf("First CreateTenantEnvironment failed: %v", err)
	}

	err = tm.CreateTenantEnvironment(ctx, cfg)
	if err != nil {
		t.Fatalf("Second CreateTenantEnvironment failed (should be idempotent): %v", err)
	}
}

func TestDeleteTenantEnvironment(t *testing.T) {
	ctx := context.Background()
	fakeClient := fake.NewSimpleClientset()
	tm := NewTenantManager(fakeClient)

	// Create namespace first
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "tenant-deleteuser"},
	}
	_, _ = fakeClient.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})

	// Delete
	err := tm.DeleteTenantEnvironment(ctx, "deleteuser")
	if err != nil {
		t.Fatalf("DeleteTenantEnvironment failed: %v", err)
	}

	// Verify deletion
	exists, _ := tm.TenantExists(ctx, "deleteuser")
	if exists {
		t.Error("Namespace still exists after deletion")
	}

	// Delete non-existent - should not error
	err = tm.DeleteTenantEnvironment(ctx, "nonexistent")
	if err != nil {
		t.Errorf("DeleteTenantEnvironment on non-existent namespace returned error: %v", err)
	}
}

func TestUpdateGPUQuota(t *testing.T) {
	ctx := context.Background()
	fakeClient := fake.NewSimpleClientset()
	tm := NewTenantManager(fakeClient)

	nsName := "tenant-quotauser"

	// Pre-create namespace and quota
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: nsName},
	}
	_, _ = fakeClient.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})

	quota := &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gpu-quota",
			Namespace: nsName,
		},
		Spec: corev1.ResourceQuotaSpec{
			Hard: corev1.ResourceList{},
		},
	}
	_, _ = fakeClient.CoreV1().ResourceQuotas(nsName).Create(ctx, quota, metav1.CreateOptions{})

	// Update quota
	err := tm.UpdateGPUQuota(ctx, "quotauser", 4)
	if err != nil {
		t.Fatalf("UpdateGPUQuota failed: %v", err)
	}

	// Verify update
	updatedQuota, _ := fakeClient.CoreV1().ResourceQuotas(nsName).Get(ctx, "gpu-quota", metav1.GetOptions{})
	gpuRequest := updatedQuota.Spec.Hard["requests.nvidia.com/gpu"]
	if gpuRequest.Value() != 4 {
		t.Errorf("Updated GPU quota = %d; want 4", gpuRequest.Value())
	}
}

func TestNetworkPolicyInternalCommunication(t *testing.T) {
	ctx := context.Background()
	fakeClient := fake.NewSimpleClientset()
	tm := NewTenantManager(fakeClient)

	cfg := DefaultTenantConfig("netuser", 1)
	_ = tm.CreateTenantEnvironment(ctx, cfg)

	nsName := GetNamespaceName("netuser")

	// Get internal policy
	policy, err := fakeClient.NetworkingV1().NetworkPolicies(nsName).Get(ctx, "allow-internal", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Failed to get allow-internal policy: %v", err)
	}

	// Verify it allows ingress from same namespace
	if len(policy.Spec.Ingress) != 1 {
		t.Errorf("Expected 1 ingress rule, got %d", len(policy.Spec.Ingress))
	}
	if len(policy.Spec.Ingress[0].From) != 1 {
		t.Errorf("Expected 1 from selector, got %d", len(policy.Spec.Ingress[0].From))
	}
	// PodSelector with empty match labels = all pods in same namespace
	if policy.Spec.Ingress[0].From[0].PodSelector == nil {
		t.Error("Expected PodSelector for internal communication")
	}
}

func TestNetworkPolicyEgressPorts(t *testing.T) {
	ctx := context.Background()
	fakeClient := fake.NewSimpleClientset()
	tm := NewTenantManager(fakeClient)

	cfg := DefaultTenantConfig("egressuser", 1)
	_ = tm.CreateTenantEnvironment(ctx, cfg)

	nsName := GetNamespaceName("egressuser")

	// Get egress policy
	policy, err := fakeClient.NetworkingV1().NetworkPolicies(nsName).Get(ctx, "allow-egress-essential", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Failed to get allow-egress-essential policy: %v", err)
	}

	// Verify egress rules exist
	if len(policy.Spec.Egress) == 0 {
		t.Error("Expected egress rules, got none")
	}

	// Check for expected ports (DNS, HTTPS, HTTP, PostgreSQL, MinIO)
	expectedPorts := map[int32]bool{53: false, 443: false, 80: false, 5432: false, 9000: false}
	for _, rule := range policy.Spec.Egress {
		for _, port := range rule.Ports {
			if port.Port != nil {
				expectedPorts[port.Port.IntVal] = true
			}
		}
	}

	for port, found := range expectedPorts {
		if !found {
			t.Errorf("Expected port %d in egress rules", port)
		}
	}
}
