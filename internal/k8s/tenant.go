package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

// Resource quota constants (ADR-K8S-004)
const (
	// DefaultGPUQuotaName is the name of the ResourceQuota in tenant namespaces
	DefaultGPUQuotaName = "gpu-quota"

	// CPU and memory per GPU (ADR-K8S-004)
	CPUPerGPU           = 4
	CPULimitPerGPU      = 8
	MemoryGiPerGPU      = 16
	MemoryLimitGiPerGPU = 32
	MaxPodsPerTenant    = 10
)

// TenantOrchestrator manages tenant namespaces with resource quotas and network policies
type TenantOrchestrator struct {
	clientset kubernetes.Interface
	logger    *slog.Logger
}

// NewTenantOrchestrator creates a new TenantOrchestrator
func NewTenantOrchestrator(clientset kubernetes.Interface, logger *slog.Logger) *TenantOrchestrator {
	return &TenantOrchestrator{
		clientset: clientset,
		logger:    logger,
	}
}

// EnsureTenant creates a tenant namespace with resource quotas and network policies.
// If the namespace already exists, returns the namespace name (idempotent).
func (t *TenantOrchestrator) EnsureTenant(ctx context.Context, userAddress string, gpuCount int) (string, error) {
	namespace := TenantNamespace(userAddress)

	// Check if namespace exists
	_, err := t.clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err == nil {
		// Namespace exists - idempotent success
		t.logger.Info("tenant namespace already exists",
			"namespace", namespace,
			"userAddress", userAddress,
		)
		return namespace, nil
	}

	if !apierrors.IsNotFound(err) {
		return "", fmt.Errorf("failed to check namespace existence: %w", err)
	}

	// Create namespace
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
			Labels: map[string]string{
				"worldland.io/tenant":       "true",
				"worldland.io/user-address": userAddress,
			},
		},
	}

	_, err = t.clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			// Race condition - another goroutine created it
			t.logger.Info("namespace created by another process",
				"namespace", namespace,
			)
			return namespace, nil
		}
		return "", fmt.Errorf("failed to create namespace: %w", err)
	}

	t.logger.Info("created tenant namespace",
		"namespace", namespace,
		"userAddress", userAddress,
	)

	// Create ResourceQuota
	if err := t.createResourceQuota(ctx, namespace, gpuCount); err != nil {
		return "", fmt.Errorf("failed to create resource quota: %w", err)
	}

	// Create NetworkPolicies
	if err := t.createNetworkPolicies(ctx, namespace); err != nil {
		return "", fmt.Errorf("failed to create network policies: %w", err)
	}

	return namespace, nil
}

// DeleteTenant deletes a tenant namespace with all resources (cascading delete).
// Ignores NotFound errors (idempotent).
func (t *TenantOrchestrator) DeleteTenant(ctx context.Context, userAddress string) error {
	namespace := TenantNamespace(userAddress)

	deletePolicy := metav1.DeletePropagationForeground
	err := t.clientset.CoreV1().Namespaces().Delete(ctx, namespace, metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	})

	if err != nil {
		if apierrors.IsNotFound(err) {
			// Namespace doesn't exist - idempotent success
			t.logger.Info("tenant namespace does not exist (already deleted)",
				"namespace", namespace,
				"userAddress", userAddress,
			)
			return nil
		}
		return fmt.Errorf("failed to delete namespace: %w", err)
	}

	t.logger.Info("deleted tenant namespace",
		"namespace", namespace,
		"userAddress", userAddress,
	)
	return nil
}

// TenantExists checks if a tenant namespace exists
func (t *TenantOrchestrator) TenantExists(ctx context.Context, userAddress string) (bool, error) {
	namespace := TenantNamespace(userAddress)

	_, err := t.clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to check namespace existence: %w", err)
	}

	return true, nil
}

// GetTenantQuotaUsage returns the ResourceQuota status for a tenant namespace.
// For future monitoring in Phase 23.
func (t *TenantOrchestrator) GetTenantQuotaUsage(ctx context.Context, userAddress string) (*corev1.ResourceQuotaStatus, error) {
	namespace := TenantNamespace(userAddress)

	quota, err := t.clientset.CoreV1().ResourceQuotas(namespace).Get(ctx, DefaultGPUQuotaName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("resource quota not found in namespace %s", namespace)
		}
		return nil, fmt.Errorf("failed to get resource quota: %w", err)
	}

	return &quota.Status, nil
}

// UpdateTenantQuota updates the ResourceQuota hard limits for a tenant namespace.
// For dynamic quota adjustment.
func (t *TenantOrchestrator) UpdateTenantQuota(ctx context.Context, userAddress string, gpuCount int) error {
	namespace := TenantNamespace(userAddress)

	// Get existing ResourceQuota
	quota, err := t.clientset.CoreV1().ResourceQuotas(namespace).Get(ctx, DefaultGPUQuotaName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("resource quota not found in namespace %s", namespace)
		}
		return fmt.Errorf("failed to get resource quota: %w", err)
	}

	// Update hard limits
	quota.Spec.Hard = corev1.ResourceList{
		"requests.nvidia.com/gpu": resource.MustParse(strconv.Itoa(gpuCount)),
		"limits.nvidia.com/gpu":   resource.MustParse(strconv.Itoa(gpuCount)),
		"requests.cpu":            resource.MustParse(strconv.Itoa(CPUPerGPU * gpuCount)),
		"limits.cpu":              resource.MustParse(strconv.Itoa(CPULimitPerGPU * gpuCount)),
		"requests.memory":         resource.MustParse(fmt.Sprintf("%dGi", MemoryGiPerGPU*gpuCount)),
		"limits.memory":           resource.MustParse(fmt.Sprintf("%dGi", MemoryLimitGiPerGPU*gpuCount)),
		corev1.ResourcePods:       resource.MustParse(strconv.Itoa(MaxPodsPerTenant)),
	}

	_, err = t.clientset.CoreV1().ResourceQuotas(namespace).Update(ctx, quota, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update resource quota: %w", err)
	}

	t.logger.Info("updated resource quota",
		"namespace", namespace,
		"gpuCount", gpuCount,
		"cpuRequest", CPUPerGPU*gpuCount,
		"memoryRequest", fmt.Sprintf("%dGi", MemoryGiPerGPU*gpuCount),
	)

	return nil
}

// createResourceQuota creates a ResourceQuota in the tenant namespace
func (t *TenantOrchestrator) createResourceQuota(ctx context.Context, namespace string, gpuCount int) error {
	quota := &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name:      DefaultGPUQuotaName,
			Namespace: namespace,
		},
		Spec: corev1.ResourceQuotaSpec{
			Hard: corev1.ResourceList{
				"requests.nvidia.com/gpu": resource.MustParse(strconv.Itoa(gpuCount)),
				"limits.nvidia.com/gpu":   resource.MustParse(strconv.Itoa(gpuCount)),
				"requests.cpu":            resource.MustParse(strconv.Itoa(CPUPerGPU * gpuCount)),
				"limits.cpu":              resource.MustParse(strconv.Itoa(CPULimitPerGPU * gpuCount)),
				"requests.memory":         resource.MustParse(fmt.Sprintf("%dGi", MemoryGiPerGPU*gpuCount)),
				"limits.memory":           resource.MustParse(fmt.Sprintf("%dGi", MemoryLimitGiPerGPU*gpuCount)),
				corev1.ResourcePods:       resource.MustParse(strconv.Itoa(MaxPodsPerTenant)),
			},
		},
	}

	_, err := t.clientset.CoreV1().ResourceQuotas(namespace).Create(ctx, quota, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			t.logger.Info("resource quota already exists", "namespace", namespace)
			return nil
		}
		return fmt.Errorf("failed to create quota: %w", err)
	}

	t.logger.Info("created resource quota",
		"namespace", namespace,
		"gpuCount", gpuCount,
		"cpuRequest", CPUPerGPU*gpuCount,
		"memoryRequest", fmt.Sprintf("%dGi", MemoryGiPerGPU*gpuCount),
	)

	return nil
}

// createNetworkPolicies creates network isolation policies for the tenant namespace
func (t *TenantOrchestrator) createNetworkPolicies(ctx context.Context, namespace string) error {
	// Policy 1: Allow internal traffic (same namespace)
	allowInternal := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "allow-internal",
			Namespace: namespace,
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
			},
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					From: []networkingv1.NetworkPolicyPeer{
						{
							PodSelector: &metav1.LabelSelector{},
						},
					},
				},
			},
		},
	}

	if err := t.createNetworkPolicy(ctx, allowInternal); err != nil {
		return fmt.Errorf("failed to create allow-internal policy: %w", err)
	}

	// Policy 2: Allow DNS (kube-system)
	allowDNS := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "allow-dns",
			Namespace: namespace,
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeEgress,
			},
			Egress: []networkingv1.NetworkPolicyEgressRule{
				{
					To: []networkingv1.NetworkPolicyPeer{
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"kubernetes.io/metadata.name": "kube-system",
								},
							},
						},
					},
					Ports: []networkingv1.NetworkPolicyPort{
						{
							Protocol: func() *corev1.Protocol {
								p := corev1.ProtocolUDP
								return &p
							}(),
							Port: func() *intstr.IntOrString {
								port := intstr.FromInt(53)
								return &port
							}(),
						},
					},
				},
			},
		},
	}

	if err := t.createNetworkPolicy(ctx, allowDNS); err != nil {
		return fmt.Errorf("failed to create allow-dns policy: %w", err)
	}

	// Policy 3: Allow egress (internet access, block cluster internal)
	allowEgress := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "allow-egress",
			Namespace: namespace,
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeEgress,
			},
			Egress: []networkingv1.NetworkPolicyEgressRule{
				{
					To: []networkingv1.NetworkPolicyPeer{
						{
							IPBlock: &networkingv1.IPBlock{
								CIDR: "0.0.0.0/0",
								Except: []string{
									"10.0.0.0/8",      // Block cluster internal
									"172.16.0.0/12",   // Block private ranges
									"192.168.0.0/16",  // Block private ranges
								},
							},
						},
					},
				},
			},
		},
	}

	if err := t.createNetworkPolicy(ctx, allowEgress); err != nil {
		return fmt.Errorf("failed to create allow-egress policy: %w", err)
	}

	t.logger.Info("created network policies", "namespace", namespace)
	return nil
}

// createNetworkPolicy creates a network policy with idempotent handling
func (t *TenantOrchestrator) createNetworkPolicy(ctx context.Context, policy *networkingv1.NetworkPolicy) error {
	_, err := t.clientset.NetworkingV1().NetworkPolicies(policy.Namespace).Create(ctx, policy, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			t.logger.Info("network policy already exists",
				"namespace", policy.Namespace,
				"policy", policy.Name,
			)
			return nil
		}
		return err
	}

	t.logger.Info("created network policy",
		"namespace", policy.Namespace,
		"policy", policy.Name,
	)
	return nil
}
