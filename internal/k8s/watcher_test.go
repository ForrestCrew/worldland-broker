package k8s

import (
	"context"
	"log/slog"
	"os"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// mockStateHandler implements StateChangeHandler for testing
type mockStateHandler struct {
	runningCalls   []string
	failedCalls    []failedCall
	succeededCalls []string
	deletedCalls   []string
}

type failedCall struct {
	sessionID string
	reason    string
}

func (m *mockStateHandler) OnPodRunning(ctx context.Context, sessionID string) error {
	m.runningCalls = append(m.runningCalls, sessionID)
	return nil
}

func (m *mockStateHandler) OnPodFailed(ctx context.Context, sessionID string, reason string) error {
	m.failedCalls = append(m.failedCalls, failedCall{sessionID: sessionID, reason: reason})
	return nil
}

func (m *mockStateHandler) OnPodSucceeded(ctx context.Context, sessionID string) error {
	m.succeededCalls = append(m.succeededCalls, sessionID)
	return nil
}

func (m *mockStateHandler) OnPodDeleted(ctx context.Context, sessionID string) error {
	m.deletedCalls = append(m.deletedCalls, sessionID)
	return nil
}

func TestHandlePodEvent_Running(t *testing.T) {
	handler := &mockStateHandler{}
	watcher := &PodWatcher{
		stateHandler: handler,
		logger:       slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test-ns",
			Labels: map[string]string{
				LabelSessionID: "session-123",
			},
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

	watcher.handlePodEvent(context.Background(), pod, "update")

	if len(handler.runningCalls) != 1 {
		t.Errorf("expected 1 OnPodRunning call, got %d", len(handler.runningCalls))
	}
	if handler.runningCalls[0] != "session-123" {
		t.Errorf("expected session-123, got %s", handler.runningCalls[0])
	}
}

func TestHandlePodEvent_RunningNotReady(t *testing.T) {
	handler := &mockStateHandler{}
	watcher := &PodWatcher{
		stateHandler: handler,
		logger:       slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test-ns",
			Labels: map[string]string{
				LabelSessionID: "session-123",
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodReady,
					Status: corev1.ConditionFalse,
				},
			},
		},
	}

	watcher.handlePodEvent(context.Background(), pod, "update")

	if len(handler.runningCalls) != 0 {
		t.Errorf("expected 0 OnPodRunning calls, got %d", len(handler.runningCalls))
	}
}

func TestHandlePodEvent_Failed(t *testing.T) {
	handler := &mockStateHandler{}
	watcher := &PodWatcher{
		stateHandler: handler,
		logger:       slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test-ns",
			Labels: map[string]string{
				LabelSessionID: "session-123",
			},
		},
		Status: corev1.PodStatus{
			Phase:   corev1.PodFailed,
			Reason:  "ImagePullBackOff",
			Message: "Failed to pull image",
		},
	}

	watcher.handlePodEvent(context.Background(), pod, "update")

	if len(handler.failedCalls) != 1 {
		t.Errorf("expected 1 OnPodFailed call, got %d", len(handler.failedCalls))
	}
	if handler.failedCalls[0].sessionID != "session-123" {
		t.Errorf("expected session-123, got %s", handler.failedCalls[0].sessionID)
	}
	// Should prioritize Message over Reason
	if handler.failedCalls[0].reason != "Failed to pull image" {
		t.Errorf("expected 'Failed to pull image', got %s", handler.failedCalls[0].reason)
	}
}

func TestHandlePodEvent_Succeeded(t *testing.T) {
	handler := &mockStateHandler{}
	watcher := &PodWatcher{
		stateHandler: handler,
		logger:       slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test-ns",
			Labels: map[string]string{
				LabelSessionID: "session-123",
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodSucceeded,
		},
	}

	watcher.handlePodEvent(context.Background(), pod, "update")

	if len(handler.succeededCalls) != 1 {
		t.Errorf("expected 1 OnPodSucceeded call, got %d", len(handler.succeededCalls))
	}
	if handler.succeededCalls[0] != "session-123" {
		t.Errorf("expected session-123, got %s", handler.succeededCalls[0])
	}
}

func TestHandlePodDelete(t *testing.T) {
	handler := &mockStateHandler{}
	watcher := &PodWatcher{
		stateHandler: handler,
		logger:       slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test-ns",
			Labels: map[string]string{
				LabelSessionID: "session-123",
			},
		},
	}

	watcher.handlePodDelete(context.Background(), pod)

	if len(handler.deletedCalls) != 1 {
		t.Errorf("expected 1 OnPodDeleted call, got %d", len(handler.deletedCalls))
	}
	if handler.deletedCalls[0] != "session-123" {
		t.Errorf("expected session-123, got %s", handler.deletedCalls[0])
	}
}

func TestHandlePodEvent_MissingSessionLabel(t *testing.T) {
	handler := &mockStateHandler{}
	watcher := &PodWatcher{
		stateHandler: handler,
		logger:       slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test-ns",
			Labels:    map[string]string{
				// No session-id label
			},
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

	watcher.handlePodEvent(context.Background(), pod, "update")

	// Should not call any handlers
	if len(handler.runningCalls) != 0 {
		t.Errorf("expected 0 calls for pod without session label, got %d", len(handler.runningCalls))
	}
}

func TestExtractPodFailureReason(t *testing.T) {
	tests := []struct {
		name     string
		pod      *corev1.Pod
		expected string
	}{
		{
			name: "pod status message",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Message: "Pod failed due to resource constraints",
				},
			},
			expected: "Pod failed due to resource constraints",
		},
		{
			name: "pod status reason",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Reason: "Evicted",
				},
			},
			expected: "Evicted",
		},
		{
			name: "container terminated reason",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{
							State: corev1.ContainerState{
								Terminated: &corev1.ContainerStateTerminated{
									Reason:  "OOMKilled",
									Message: "Container killed due to OOM",
								},
							},
						},
					},
				},
			},
			expected: "OOMKilled",
		},
		{
			name: "container terminated message only",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{
							State: corev1.ContainerState{
								Terminated: &corev1.ContainerStateTerminated{
									Message: "Custom error message",
								},
							},
						},
					},
				},
			},
			expected: "Custom error message",
		},
		{
			name: "init container terminated",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					InitContainerStatuses: []corev1.ContainerStatus{
						{
							State: corev1.ContainerState{
								Terminated: &corev1.ContainerStateTerminated{
									Reason: "InitContainerFailed",
								},
							},
						},
					},
				},
			},
			expected: "InitContainerFailed",
		},
		{
			name: "no reason found",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{},
			},
			expected: "Unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractPodFailureReason(tt.pod)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestPodWatcher_IsPodReady(t *testing.T) {
	tests := []struct {
		name     string
		pod      *corev1.Pod
		expected bool
	}{
		{
			name: "pod ready",
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
			name: "pod not ready",
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
			name: "no ready condition",
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
			result := isPodReady(tt.pod)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}
