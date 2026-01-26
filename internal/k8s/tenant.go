// Package k8s provides tenant management for multi-tenant GPU isolation.
package k8s

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

// TenantManager handles tenant namespace lifecycle and resource isolation.
type TenantManager struct {
	clientset kubernetes.Interface
}

// TenantConfig holds configuration for creating a tenant environment.
type TenantConfig struct {
	UserID        string
	GPUCount      int
	CPURequest    string // e.g., "4"
	MemoryRequest string // e.g., "16Gi"
	JupyterPort   int32  // Jupyter notebook port (default: 8888)
	MinIOService  string // MinIO service name for egress (e.g., "minio-svc.minio")
	PostgresPort  int32  // PostgreSQL port for egress (default: 5432)
}

// DefaultTenantConfig returns a default configuration.
func DefaultTenantConfig(userID string, gpuCount int) *TenantConfig {
	return &TenantConfig{
		UserID:        userID,
		GPUCount:      gpuCount,
		CPURequest:    "4",
		MemoryRequest: "16Gi",
		JupyterPort:   8888,
		MinIOService:  "minio-svc.minio",
		PostgresPort:  5432,
	}
}

// NewTenantManager creates a new TenantManager with the given clientset.
func NewTenantManager(clientset kubernetes.Interface) *TenantManager {
	return &TenantManager{clientset: clientset}
}

// GetNamespaceName returns the namespace name for a user.
func GetNamespaceName(userID string) string {
	return fmt.Sprintf("tenant-%s", userID)
}

// TenantExists checks if a tenant namespace already exists.
func (tm *TenantManager) TenantExists(ctx context.Context, userID string) (bool, error) {
	nsName := GetNamespaceName(userID)
	_, err := tm.clientset.CoreV1().Namespaces().Get(ctx, nsName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to check namespace existence: %w", err)
	}
	return true, nil
}

// CreateTenantEnvironment creates an isolated namespace with quota and network policies.
func (tm *TenantManager) CreateTenantEnvironment(ctx context.Context, cfg *TenantConfig) error {
	nsName := GetNamespaceName(cfg.UserID)

	// 1. Create Namespace
	if err := tm.createNamespace(ctx, nsName, cfg.UserID); err != nil {
		return fmt.Errorf("failed to create namespace: %w", err)
	}

	// 2. Create ResourceQuota
	if err := tm.createResourceQuota(ctx, nsName, cfg); err != nil {
		// Cleanup namespace on failure
		_ = tm.deleteNamespace(ctx, nsName)
		return fmt.Errorf("failed to create resource quota: %w", err)
	}

	// 3. Create Network Policies
	if err := tm.createNetworkPolicies(ctx, nsName, cfg); err != nil {
		// Cleanup on failure
		_ = tm.deleteNamespace(ctx, nsName)
		return fmt.Errorf("failed to create network policies: %w", err)
	}

	return nil
}

// DeleteTenantEnvironment removes the tenant namespace and all resources within it.
func (tm *TenantManager) DeleteTenantEnvironment(ctx context.Context, userID string) error {
	nsName := GetNamespaceName(userID)
	return tm.deleteNamespace(ctx, nsName)
}

// createNamespace creates a namespace for the tenant.
func (tm *TenantManager) createNamespace(ctx context.Context, nsName, userID string) error {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: nsName,
			Labels: map[string]string{
				"tenant":        userID,
				"managed-by":    "web3-ai-platform",
				"resource-type": "tenant-namespace",
			},
		},
	}

	_, err := tm.clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// deleteNamespace deletes the tenant namespace.
func (tm *TenantManager) deleteNamespace(ctx context.Context, nsName string) error {
	err := tm.clientset.CoreV1().Namespaces().Delete(ctx, nsName, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return err
	}
	return nil
}

// createResourceQuota creates GPU/CPU/Memory quota for the tenant.
func (tm *TenantManager) createResourceQuota(ctx context.Context, nsName string, cfg *TenantConfig) error {
	quota := &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gpu-quota",
			Namespace: nsName,
		},
		Spec: corev1.ResourceQuotaSpec{
			Hard: corev1.ResourceList{
				"requests.nvidia.com/gpu": resource.MustParse(fmt.Sprintf("%d", cfg.GPUCount)),
				"limits.nvidia.com/gpu":   resource.MustParse(fmt.Sprintf("%d", cfg.GPUCount)),
				"requests.cpu":            resource.MustParse(cfg.CPURequest),
				"requests.memory":         resource.MustParse(cfg.MemoryRequest),
			},
		},
	}

	_, err := tm.clientset.CoreV1().ResourceQuotas(nsName).Create(ctx, quota, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// createNetworkPolicies creates all required network policies for tenant isolation.
func (tm *TenantManager) createNetworkPolicies(ctx context.Context, nsName string, cfg *TenantConfig) error {
	// Policy 1: Allow internal communication (same namespace)
	if err := tm.createInternalPolicy(ctx, nsName); err != nil {
		return err
	}

	// Policy 2: Ingress whitelist (Jupyter, LoadBalancer)
	if err := tm.createIngressPolicy(ctx, nsName, cfg.JupyterPort); err != nil {
		return err
	}

	// Policy 3: Egress to external services (S3, DB, HTTPS)
	if err := tm.createEgressPolicy(ctx, nsName, cfg); err != nil {
		return err
	}

	return nil
}

// createInternalPolicy allows all pods within the same namespace to communicate.
func (tm *TenantManager) createInternalPolicy(ctx context.Context, nsName string) error {
	policy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "allow-internal",
			Namespace: nsName,
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{}, // All pods
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
			},
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					From: []networkingv1.NetworkPolicyPeer{
						{
							// Allow from same namespace
							PodSelector: &metav1.LabelSelector{},
						},
					},
				},
			},
		},
	}

	_, err := tm.clientset.NetworkingV1().NetworkPolicies(nsName).Create(ctx, policy, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// createIngressPolicy allows ingress from ingress-nginx namespace on specific ports.
func (tm *TenantManager) createIngressPolicy(ctx context.Context, nsName string, jupyterPort int32) error {
	policy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "allow-ingress-whitelist",
			Namespace: nsName,
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{}, // All pods
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
			},
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					From: []networkingv1.NetworkPolicyPeer{
						{
							// Allow from ingress-nginx namespace
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"name": "ingress-nginx",
								},
							},
						},
					},
					Ports: []networkingv1.NetworkPolicyPort{
						{
							Protocol: func() *corev1.Protocol { p := corev1.ProtocolTCP; return &p }(),
							Port:     &intstr.IntOrString{Type: intstr.Int, IntVal: jupyterPort},
						},
					},
				},
			},
		},
	}

	_, err := tm.clientset.NetworkingV1().NetworkPolicies(nsName).Create(ctx, policy, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// createEgressPolicy allows egress to essential external services.
func (tm *TenantManager) createEgressPolicy(ctx context.Context, nsName string, cfg *TenantConfig) error {
	tcpProtocol := corev1.ProtocolTCP

	policy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "allow-egress-essential",
			Namespace: nsName,
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{}, // All pods
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeEgress,
			},
			Egress: []networkingv1.NetworkPolicyEgressRule{
				// Allow DNS resolution (kube-dns)
				{
					Ports: []networkingv1.NetworkPolicyPort{
						{
							Protocol: func() *corev1.Protocol { p := corev1.ProtocolUDP; return &p }(),
							Port:     &intstr.IntOrString{Type: intstr.Int, IntVal: 53},
						},
					},
				},
				// Allow HTTPS (S3/MinIO, PyPI, GitHub, etc.)
				{
					Ports: []networkingv1.NetworkPolicyPort{
						{
							Protocol: &tcpProtocol,
							Port:     &intstr.IntOrString{Type: intstr.Int, IntVal: 443},
						},
					},
				},
				// Allow HTTP (some package registries)
				{
					Ports: []networkingv1.NetworkPolicyPort{
						{
							Protocol: &tcpProtocol,
							Port:     &intstr.IntOrString{Type: intstr.Int, IntVal: 80},
						},
					},
				},
				// Allow PostgreSQL
				{
					Ports: []networkingv1.NetworkPolicyPort{
						{
							Protocol: &tcpProtocol,
							Port:     &intstr.IntOrString{Type: intstr.Int, IntVal: cfg.PostgresPort},
						},
					},
				},
				// Allow MinIO (typically 9000)
				{
					Ports: []networkingv1.NetworkPolicyPort{
						{
							Protocol: &tcpProtocol,
							Port:     &intstr.IntOrString{Type: intstr.Int, IntVal: 9000},
						},
					},
				},
				// Allow communication within the cluster (to access internal services)
				{
					To: []networkingv1.NetworkPolicyPeer{
						{
							NamespaceSelector: &metav1.LabelSelector{}, // All namespaces in cluster
						},
					},
				},
			},
		},
	}

	_, err := tm.clientset.NetworkingV1().NetworkPolicies(nsName).Create(ctx, policy, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// UpdateGPUQuota updates the GPU quota for an existing tenant.
func (tm *TenantManager) UpdateGPUQuota(ctx context.Context, userID string, newGPUCount int) error {
	nsName := GetNamespaceName(userID)

	quota, err := tm.clientset.CoreV1().ResourceQuotas(nsName).Get(ctx, "gpu-quota", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get existing quota: %w", err)
	}

	quota.Spec.Hard["requests.nvidia.com/gpu"] = resource.MustParse(fmt.Sprintf("%d", newGPUCount))
	quota.Spec.Hard["limits.nvidia.com/gpu"] = resource.MustParse(fmt.Sprintf("%d", newGPUCount))

	_, err = tm.clientset.CoreV1().ResourceQuotas(nsName).Update(ctx, quota, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update quota: %w", err)
	}

	return nil
}
