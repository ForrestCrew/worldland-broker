//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// DiagnosticsCollector collects debugging information on test failures
type DiagnosticsCollector struct {
	client     kubernetes.Interface
	outputDir  string
	testName   string
	collectAll bool
}

// NewDiagnosticsCollector creates a new diagnostics collector
func NewDiagnosticsCollector(client kubernetes.Interface, cfg E2EConfig, testName string) *DiagnosticsCollector {
	return &DiagnosticsCollector{
		client:     client,
		outputDir:  cfg.DiagnosticsDir,
		testName:   testName,
		collectAll: cfg.CollectDiagnostics,
	}
}

// CollectOnFailure collects diagnostics if the test failed
func (d *DiagnosticsCollector) CollectOnFailure(t *testing.T, namespace string, sessionID string) {
	if !d.collectAll {
		return
	}

	if !t.Failed() {
		return
	}

	t.Logf("Test failed - collecting diagnostics to %s", d.outputDir)

	// Create timestamped directory for this test run
	timestamp := time.Now().UTC().Format("2006-01-02T15-04-05-000Z")
	testDir := filepath.Join(d.outputDir, fmt.Sprintf("%s-%s", d.testName, timestamp))
	if err := os.MkdirAll(testDir, 0755); err != nil {
		t.Errorf("Failed to create diagnostics directory: %v", err)
		return
	}

	ctx := context.Background()

	// Collect pod logs
	d.collectPodLogs(t, ctx, testDir, namespace, sessionID)

	// Collect pod events
	d.collectPodEvents(t, ctx, testDir, namespace, sessionID)

	// Collect pod descriptions
	d.collectPodDescriptions(t, ctx, testDir, namespace, sessionID)

	// Collect namespace events
	d.collectNamespaceEvents(t, ctx, testDir, namespace)

	t.Logf("Diagnostics collected in: %s", testDir)
}

// collectPodLogs collects logs from all pods matching the session
func (d *DiagnosticsCollector) collectPodLogs(t *testing.T, ctx context.Context, dir, namespace, sessionID string) {
	pods, err := d.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("worldland.io/session-id=%s", sessionID),
	})
	if err != nil {
		t.Logf("Warning: Failed to list pods for logs: %v", err)
		return
	}

	for _, pod := range pods.Items {
		logFile := filepath.Join(dir, fmt.Sprintf("pod-%s.log", pod.Name))

		// Get logs with timestamps
		opts := &corev1.PodLogOptions{
			Timestamps: true,
		}
		req := d.client.CoreV1().Pods(namespace).GetLogs(pod.Name, opts)
		logs, err := req.DoRaw(ctx)
		if err != nil {
			t.Logf("Warning: Failed to get logs for pod %s: %v", pod.Name, err)
			continue
		}

		if err := os.WriteFile(logFile, logs, 0644); err != nil {
			t.Logf("Warning: Failed to write log file %s: %v", logFile, err)
		} else {
			t.Logf("Collected logs: %s", logFile)
		}
	}
}

// collectPodEvents collects events related to pods in the session
func (d *DiagnosticsCollector) collectPodEvents(t *testing.T, ctx context.Context, dir, namespace, sessionID string) {
	pods, err := d.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("worldland.io/session-id=%s", sessionID),
	})
	if err != nil {
		t.Logf("Warning: Failed to list pods for events: %v", err)
		return
	}

	for _, pod := range pods.Items {
		eventsFile := filepath.Join(dir, fmt.Sprintf("pod-%s-events.txt", pod.Name))

		events, err := d.client.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
			FieldSelector: fmt.Sprintf("involvedObject.name=%s", pod.Name),
		})
		if err != nil {
			t.Logf("Warning: Failed to get events for pod %s: %v", pod.Name, err)
			continue
		}

		var content string
		for _, event := range events.Items {
			content += fmt.Sprintf("[%s] %s: %s\n  Reason: %s\n  Message: %s\n\n",
				event.LastTimestamp.Format(time.RFC3339),
				event.Type,
				event.InvolvedObject.Name,
				event.Reason,
				event.Message,
			)
		}

		if err := os.WriteFile(eventsFile, []byte(content), 0644); err != nil {
			t.Logf("Warning: Failed to write events file %s: %v", eventsFile, err)
		} else {
			t.Logf("Collected events: %s", eventsFile)
		}
	}
}

// collectPodDescriptions collects detailed pod descriptions
func (d *DiagnosticsCollector) collectPodDescriptions(t *testing.T, ctx context.Context, dir, namespace, sessionID string) {
	pods, err := d.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("worldland.io/session-id=%s", sessionID),
	})
	if err != nil {
		t.Logf("Warning: Failed to list pods for descriptions: %v", err)
		return
	}

	for _, pod := range pods.Items {
		descFile := filepath.Join(dir, fmt.Sprintf("pod-%s-description.txt", pod.Name))

		var content string
		content += fmt.Sprintf("Name: %s\n", pod.Name)
		content += fmt.Sprintf("Namespace: %s\n", pod.Namespace)
		content += fmt.Sprintf("Phase: %s\n", pod.Status.Phase)
		content += fmt.Sprintf("Node: %s\n", pod.Spec.NodeName)
		content += fmt.Sprintf("Created: %s\n\n", pod.CreationTimestamp.Format(time.RFC3339))

		content += "Labels:\n"
		for k, v := range pod.Labels {
			content += fmt.Sprintf("  %s: %s\n", k, v)
		}
		content += "\n"

		content += "Container Statuses:\n"
		for _, cs := range pod.Status.ContainerStatuses {
			content += fmt.Sprintf("  %s:\n", cs.Name)
			content += fmt.Sprintf("    Ready: %v\n", cs.Ready)
			content += fmt.Sprintf("    RestartCount: %d\n", cs.RestartCount)
			if cs.State.Waiting != nil {
				content += fmt.Sprintf("    State: Waiting (%s: %s)\n", cs.State.Waiting.Reason, cs.State.Waiting.Message)
			} else if cs.State.Running != nil {
				content += fmt.Sprintf("    State: Running (started: %s)\n", cs.State.Running.StartedAt.Format(time.RFC3339))
			} else if cs.State.Terminated != nil {
				content += fmt.Sprintf("    State: Terminated (reason: %s, exit code: %d)\n",
					cs.State.Terminated.Reason, cs.State.Terminated.ExitCode)
			}
		}
		content += "\n"

		content += "Conditions:\n"
		for _, cond := range pod.Status.Conditions {
			content += fmt.Sprintf("  %s: %s (reason: %s)\n", cond.Type, cond.Status, cond.Reason)
		}

		if err := os.WriteFile(descFile, []byte(content), 0644); err != nil {
			t.Logf("Warning: Failed to write description file %s: %v", descFile, err)
		} else {
			t.Logf("Collected description: %s", descFile)
		}
	}
}

// collectNamespaceEvents collects all events in the namespace
func (d *DiagnosticsCollector) collectNamespaceEvents(t *testing.T, ctx context.Context, dir, namespace string) {
	eventsFile := filepath.Join(dir, "namespace-events.txt")

	events, err := d.client.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Logf("Warning: Failed to get namespace events: %v", err)
		return
	}

	var content string
	content += fmt.Sprintf("Namespace: %s\n", namespace)
	content += fmt.Sprintf("Total Events: %d\n\n", len(events.Items))

	for _, event := range events.Items {
		content += fmt.Sprintf("[%s] %s: %s/%s\n",
			event.LastTimestamp.Format(time.RFC3339),
			event.Type,
			event.InvolvedObject.Kind,
			event.InvolvedObject.Name,
		)
		content += fmt.Sprintf("  Reason: %s\n", event.Reason)
		content += fmt.Sprintf("  Message: %s\n\n", event.Message)
	}

	if err := os.WriteFile(eventsFile, []byte(content), 0644); err != nil {
		t.Logf("Warning: Failed to write namespace events file: %v", err)
	} else {
		t.Logf("Collected namespace events: %s", eventsFile)
	}
}
