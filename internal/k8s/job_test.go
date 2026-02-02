package k8s

import (
	"context"
	"log/slog"
	"os"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestDeleteGPUSession(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	userAddress := "0x742d35Cc6634C0532925a3b844Bc9e7595f0bEb"
	sessionID := "test-session-123"
	namespace := TenantNamespace(userAddress)

	// Create fake clientset with pod, service, and secret
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      PodName(sessionID),
			Namespace: namespace,
		},
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SSHServiceName(sessionID),
			Namespace: namespace,
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SSHSecretName(sessionID),
			Namespace: namespace,
		},
	}

	clientset := fake.NewSimpleClientset(pod, svc, secret)
	manager := NewJobManager(clientset, logger)

	// Delete session
	err := manager.DeleteGPUSession(ctx, userAddress, sessionID)
	if err != nil {
		t.Fatalf("DeleteGPUSession failed: %v", err)
	}

	// Verify pod deleted
	_, err = clientset.CoreV1().Pods(namespace).Get(ctx, PodName(sessionID), metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Errorf("Expected pod to be deleted, got error: %v", err)
	}

	// Verify service deleted
	_, err = clientset.CoreV1().Services(namespace).Get(ctx, SSHServiceName(sessionID), metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Errorf("Expected service to be deleted, got error: %v", err)
	}

	// Verify secret deleted
	_, err = clientset.CoreV1().Secrets(namespace).Get(ctx, SSHSecretName(sessionID), metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Errorf("Expected secret to be deleted, got error: %v", err)
	}
}

func TestDeleteGPUSession_Idempotent(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	userAddress := "0x742d35Cc6634C0532925a3b844Bc9e7595f0bEb"
	sessionID := "nonexistent-session"

	clientset := fake.NewSimpleClientset()
	manager := NewJobManager(clientset, logger)

	// Delete non-existent session (should not error)
	err := manager.DeleteGPUSession(ctx, userAddress, sessionID)
	if err != nil {
		t.Errorf("DeleteGPUSession should be idempotent, got error: %v", err)
	}
}

func TestDeleteGPUSessionImmediate(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	userAddress := "0x742d35Cc6634C0532925a3b844Bc9e7595f0bEb"
	sessionID := "test-session-456"
	namespace := TenantNamespace(userAddress)

	// Create fake clientset with pod
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      PodName(sessionID),
			Namespace: namespace,
		},
	}

	clientset := fake.NewSimpleClientset(pod)
	manager := NewJobManager(clientset, logger)

	// Delete session immediately
	err := manager.DeleteGPUSessionImmediate(ctx, userAddress, sessionID)
	if err != nil {
		t.Fatalf("DeleteGPUSessionImmediate failed: %v", err)
	}

	// Verify pod deleted
	_, err = clientset.CoreV1().Pods(namespace).Get(ctx, PodName(sessionID), metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Errorf("Expected pod to be deleted, got error: %v", err)
	}
}

func TestGetSSHConnectionInfo_Success(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	userAddress := "0x742d35Cc6634C0532925a3b844Bc9e7595f0bEb"
	sessionID := "test-session-789"
	namespace := TenantNamespace(userAddress)
	password := "testPassword123"
	nodeIP := "192.168.1.100"
	nodePort := int32(30022)

	// Create Pod in Running state with Ready condition
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      PodName(sessionID),
			Namespace: namespace,
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodReady,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}

	// Create Service with NodePort
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SSHServiceName(sessionID),
			Namespace: namespace,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:     "ssh",
					NodePort: nodePort,
				},
			},
		},
	}

	// Create Secret with password
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SSHSecretName(sessionID),
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"password": []byte(password),
		},
	}

	// Create Node with IP
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
		Status: corev1.NodeStatus{
			Addresses: []corev1.NodeAddress{
				{
					Type:    corev1.NodeExternalIP,
					Address: nodeIP,
				},
			},
		},
	}

	clientset := fake.NewSimpleClientset(pod, svc, secret, node)
	manager := NewJobManager(clientset, logger)

	// Get SSH connection info
	connInfo, err := manager.GetSSHConnectionInfo(ctx, userAddress, sessionID)
	if err != nil {
		t.Fatalf("GetSSHConnectionInfo failed: %v", err)
	}

	// Verify connection info
	if connInfo.Host != nodeIP {
		t.Errorf("Expected host %s, got %s", nodeIP, connInfo.Host)
	}
	if connInfo.Port != nodePort {
		t.Errorf("Expected port %d, got %d", nodePort, connInfo.Port)
	}
	if connInfo.Password != password {
		t.Errorf("Expected password %s, got %s", password, connInfo.Password)
	}
}

func TestGetSSHConnectionInfo_PodNotReady(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	userAddress := "0x742d35Cc6634C0532925a3b844Bc9e7595f0bEb"
	sessionID := "test-session-not-ready"
	namespace := TenantNamespace(userAddress)

	// Create Pod in Pending state (not Ready)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      PodName(sessionID),
			Namespace: namespace,
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodReady,
					Status: corev1.ConditionFalse,
				},
			},
		},
	}

	// Create Service
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SSHServiceName(sessionID),
			Namespace: namespace,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:     "ssh",
					NodePort: 30022,
				},
			},
		},
	}

	clientset := fake.NewSimpleClientset(pod, svc)
	manager := NewJobManager(clientset, logger)

	// Get SSH connection info (should fail - pod not ready)
	_, err := manager.GetSSHConnectionInfo(ctx, userAddress, sessionID)
	if err == nil {
		t.Error("Expected error for pod not ready, got nil")
	}
}

func TestIsPodReady(t *testing.T) {
	tests := []struct {
		name     string
		pod      *corev1.Pod
		expected bool
	}{
		{
			name: "Pod with Ready condition True",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.PodReady,
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "Pod with Ready condition False",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.PodReady,
							Status: corev1.ConditionFalse,
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "Pod with no conditions",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsPodReady(tt.pod)
			if result != tt.expected {
				t.Errorf("IsPodReady() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestListSessionPods(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Create pods with gpu-rental label
	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "session-1",
			Namespace: "tenant-a",
			Labels: map[string]string{
				LabelGPURental: "true",
				LabelSessionID: "session-1",
			},
		},
	}

	pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "session-2",
			Namespace: "tenant-b",
			Labels: map[string]string{
				LabelGPURental: "true",
				LabelSessionID: "session-2",
			},
		},
	}

	// Create pod without label (should not be returned)
	pod3 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "other-pod",
			Namespace: "tenant-c",
		},
	}

	clientset := fake.NewSimpleClientset(pod1, pod2, pod3)
	manager := NewJobManager(clientset, logger)

	// List session pods
	pods, err := manager.ListSessionPods(ctx)
	if err != nil {
		t.Fatalf("ListSessionPods failed: %v", err)
	}

	// Verify only labeled pods returned
	if len(pods) != 2 {
		t.Errorf("Expected 2 pods, got %d", len(pods))
	}

	// Verify correct pods returned
	foundPod1 := false
	foundPod2 := false
	for _, pod := range pods {
		if pod.Name == "session-1" {
			foundPod1 = true
		}
		if pod.Name == "session-2" {
			foundPod2 = true
		}
	}

	if !foundPod1 || !foundPod2 {
		t.Error("Expected to find both session-1 and session-2 pods")
	}
}
