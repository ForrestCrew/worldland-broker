package k8s

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestGenerateSSHPassword(t *testing.T) {
	t.Run("generates 16 character password", func(t *testing.T) {
		password := GenerateSSHPassword()
		if len(password) != 16 {
			t.Errorf("expected password length 16, got %d", len(password))
		}
	})

	t.Run("generates different passwords", func(t *testing.T) {
		password1 := GenerateSSHPassword()
		password2 := GenerateSSHPassword()
		if password1 == password2 {
			t.Error("expected different passwords, got same")
		}
	})

	t.Run("generates only alphanumeric characters", func(t *testing.T) {
		const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
		password := GenerateSSHPassword()
		for _, c := range password {
			found := false
			for _, valid := range charset {
				if c == valid {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("password contains invalid character: %c", c)
			}
		}
	})
}

func TestCreateGPUSession(t *testing.T) {
	ctx := context.Background()
	clientset := fake.NewSimpleClientset()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	jm := NewJobManager(clientset, logger)

	spec := GPUJobSpec{
		SessionID:     "test-session-1",
		UserAddress:   "0x1234567890123456789012345678901234567890",
		ProviderID:    "provider-001",
		GPUCount:      0,
		GPUModel:      "RTX 4090",
		Image:         "nvidia/cuda:12.0-runtime",
		CPURequest:    "4",
		MemoryRequest: "16Gi",
		CPULimit:      "8",
		MemoryLimit:   "32Gi",
		ExpiresAt:     time.Now().Add(1 * time.Hour),
	}

	password, err := jm.CreateGPUSession(ctx, spec)
	if err != nil {
		t.Fatalf("CreateGPUSession failed: %v", err)
	}

	// Verify password returned
	if password == "" {
		t.Error("expected non-empty password")
	}

	namespace := TenantNamespace(spec.UserAddress)
	podName := PodName(spec.SessionID)

	// Verify Secret created
	secret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, SSHSecretName(spec.SessionID), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get secret: %v", err)
	}

	// Check both Data and StringData (fake clientset might not convert StringData)
	secretPassword := ""
	if len(secret.Data) > 0 && secret.Data["password"] != nil {
		secretPassword = string(secret.Data["password"])
	} else if len(secret.StringData) > 0 {
		secretPassword = secret.StringData["password"]
	}

	if secretPassword != password {
		t.Errorf("secret password mismatch: expected %s, got %s", password, secretPassword)
	}

	// Verify Secret labels
	if secret.Labels[LabelSessionID] != spec.SessionID {
		t.Errorf("secret label session-id mismatch: expected %s, got %s", spec.SessionID, secret.Labels[LabelSessionID])
	}

	// Verify Pod created
	pod, err := clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get pod: %v", err)
	}

	// Verify Pod labels
	if pod.Labels[LabelSessionID] != spec.SessionID {
		t.Errorf("pod label session-id mismatch: expected %s, got %s", spec.SessionID, pod.Labels[LabelSessionID])
	}
	if pod.Labels[LabelProviderID] != spec.ProviderID {
		t.Errorf("pod label provider-id mismatch: expected %s, got %s", spec.ProviderID, pod.Labels[LabelProviderID])
	}
	if pod.Labels[LabelGPURental] != "true" {
		t.Errorf("pod label gpu-rental mismatch: expected true, got %s", pod.Labels[LabelGPURental])
	}

	// Verify Pod annotations
	if pod.Annotations[AnnotationUserAddress] != spec.UserAddress {
		t.Errorf("pod annotation user-address mismatch: expected %s, got %s", spec.UserAddress, pod.Annotations[AnnotationUserAddress])
	}
	if pod.Annotations[AnnotationGPUModel] != spec.GPUModel {
		t.Errorf("pod annotation gpu-model mismatch: expected %s, got %s", spec.GPUModel, pod.Annotations[AnnotationGPUModel])
	}

	// Verify RestartPolicy
	if pod.Spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("pod restart policy mismatch: expected Never, got %s", pod.Spec.RestartPolicy)
	}

	// Verify NodeSelector
	if pod.Spec.NodeSelector[LabelProviderID] != spec.ProviderID {
		t.Errorf("pod nodeSelector provider-id mismatch: expected %s, got %s", spec.ProviderID, pod.Spec.NodeSelector[LabelProviderID])
	}

	// Verify SSH_PASSWORD env var
	envFound := false
	for _, env := range pod.Spec.Containers[0].Env {
		if env.Name == "SSH_PASSWORD" {
			envFound = true
			if env.ValueFrom == nil || env.ValueFrom.SecretKeyRef == nil {
				t.Error("SSH_PASSWORD env var not referencing Secret")
			} else {
				if env.ValueFrom.SecretKeyRef.Name != SSHSecretName(spec.SessionID) {
					t.Errorf("SSH_PASSWORD secret name mismatch: expected %s, got %s", SSHSecretName(spec.SessionID), env.ValueFrom.SecretKeyRef.Name)
				}
				if env.ValueFrom.SecretKeyRef.Key != "password" {
					t.Errorf("SSH_PASSWORD secret key mismatch: expected password, got %s", env.ValueFrom.SecretKeyRef.Key)
				}
			}
			break
		}
	}
	if !envFound {
		t.Error("SSH_PASSWORD env var not found in pod")
	}

	// Verify TCP readiness probe
	probe := pod.Spec.Containers[0].ReadinessProbe
	if probe == nil {
		t.Fatal("readiness probe not found")
	}
	if probe.TCPSocket == nil {
		t.Fatal("readiness probe is not TCP socket")
	}
	if probe.TCPSocket.Port.IntVal != 22 {
		t.Errorf("readiness probe port mismatch: expected 22, got %d", probe.TCPSocket.Port.IntVal)
	}

	// Verify Service created
	svcName := SSHServiceName(spec.SessionID)
	svc, err := clientset.CoreV1().Services(namespace).Get(ctx, svcName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get service: %v", err)
	}

	// Verify Service type
	if svc.Spec.Type != corev1.ServiceTypeNodePort {
		t.Errorf("service type mismatch: expected NodePort, got %s", svc.Spec.Type)
	}

	// Verify Service selector
	if svc.Spec.Selector[LabelSessionID] != spec.SessionID {
		t.Errorf("service selector session-id mismatch: expected %s, got %s", spec.SessionID, svc.Spec.Selector[LabelSessionID])
	}
}

func TestCreateGPUSession_WithGPU(t *testing.T) {
	ctx := context.Background()
	clientset := fake.NewSimpleClientset()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	jm := NewJobManager(clientset, logger)

	spec := GPUJobSpec{
		SessionID:     "test-session-gpu",
		UserAddress:   "0x1234567890123456789012345678901234567890",
		ProviderID:    "provider-001",
		GPUCount:      2,
		GPUModel:      "A100",
		Image:         "nvidia/cuda:12.0-runtime",
		CPURequest:    "8",
		MemoryRequest: "32Gi",
		CPULimit:      "16",
		MemoryLimit:   "64Gi",
		ExpiresAt:     time.Now().Add(1 * time.Hour),
	}

	_, err := jm.CreateGPUSession(ctx, spec)
	if err != nil {
		t.Fatalf("CreateGPUSession failed: %v", err)
	}

	namespace := TenantNamespace(spec.UserAddress)
	podName := PodName(spec.SessionID)

	pod, err := clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get pod: %v", err)
	}

	// Verify GPU resource request
	gpuRequest := pod.Spec.Containers[0].Resources.Requests["nvidia.com/gpu"]
	if gpuRequest.IsZero() {
		t.Error("GPU request not set")
	}
	gpuRequestValue := gpuRequest.Value()
	if gpuRequestValue != 2 {
		t.Errorf("GPU request mismatch: expected 2, got %d", gpuRequestValue)
	}

	// Verify GPU resource limit
	gpuLimit := pod.Spec.Containers[0].Resources.Limits["nvidia.com/gpu"]
	if gpuLimit.IsZero() {
		t.Error("GPU limit not set")
	}
	gpuLimitValue := gpuLimit.Value()
	if gpuLimitValue != 2 {
		t.Errorf("GPU limit mismatch: expected 2, got %d", gpuLimitValue)
	}
}

func TestGetSSHPassword(t *testing.T) {
	ctx := context.Background()
	clientset := fake.NewSimpleClientset()

	namespace := "test-namespace"
	sessionID := "test-session-1"
	expectedPassword := "testPassword123"

	// Create Secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SSHSecretName(sessionID),
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"password": []byte(expectedPassword),
		},
	}

	_, err := clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create secret: %v", err)
	}

	// Retrieve password
	password, err := GetSSHPassword(ctx, clientset, namespace, sessionID)
	if err != nil {
		t.Fatalf("GetSSHPassword failed: %v", err)
	}

	if password != expectedPassword {
		t.Errorf("password mismatch: expected %s, got %s", expectedPassword, password)
	}
}

func TestGetSSHPassword_NotFound(t *testing.T) {
	ctx := context.Background()
	clientset := fake.NewSimpleClientset()

	namespace := "test-namespace"
	sessionID := "non-existent-session"

	// Try to retrieve password from non-existent secret
	_, err := GetSSHPassword(ctx, clientset, namespace, sessionID)
	if err == nil {
		t.Error("expected error for non-existent secret, got nil")
	}
}
