//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

// TestKindClusterInfrastructure verifies that the Kind cluster infrastructure is working correctly.
// This is an infrastructure smoke test that runs as part of E2E to ensure the test environment is healthy.
func TestKindClusterInfrastructure(t *testing.T) {
	feature := features.New("Kind Cluster Smoke Test").
		Assess("cluster is reachable", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			// Get the client from config
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("failed to create K8s client: %v", err)
			}

			// List nodes to verify cluster connectivity
			nodes := &corev1.NodeList{}
			if err := client.Resources().List(ctx, nodes); err != nil {
				t.Fatalf("failed to list nodes: %v", err)
			}

			if len(nodes.Items) == 0 {
				t.Fatal("no nodes found in cluster")
			}

			t.Logf("cluster has %d node(s):", len(nodes.Items))
			for _, node := range nodes.Items {
				ready := false
				for _, cond := range node.Status.Conditions {
					if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
						ready = true
						break
					}
				}
				t.Logf("  - %s (ready: %v)", node.Name, ready)
			}

			return ctx
		}).
		Assess("can create namespace", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("failed to create K8s client: %v", err)
			}

			// Create a test namespace
			testNS := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "smoke-test-ns",
					Labels: map[string]string{
						"app.kubernetes.io/managed-by": "e2e-smoke-test",
					},
				},
			}

			if err := client.Resources().Create(ctx, testNS); err != nil {
				t.Fatalf("failed to create namespace: %v", err)
			}

			t.Log("created namespace: smoke-test-ns")

			// Clean up
			defer func() {
				if err := client.Resources().Delete(ctx, testNS); err != nil {
					t.Logf("warning: failed to delete namespace: %v", err)
				}
			}()

			// Verify namespace exists
			ns := &corev1.Namespace{}
			if err := client.Resources().Get(ctx, "smoke-test-ns", "", ns); err != nil {
				t.Fatalf("failed to get created namespace: %v", err)
			}

			t.Logf("verified namespace smoke-test-ns exists")
			return ctx
		}).
		Assess("can create pod", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("failed to create K8s client: %v", err)
			}

			// Create a simple test pod in the e2e-test namespace
			namespace := cfg.Namespace()
			if namespace == "" {
				namespace = testNamespace
			}

			podName := "smoke-test-pod"

			// Create pod using helper
			pod, err := CreateTestPod(ctx, client, namespace, podName, "busybox:latest")
			if err != nil {
				t.Fatalf("failed to create pod: %v", err)
			}

			t.Logf("created pod %s/%s", namespace, podName)

			// Wait for pod to be running (or at least scheduled)
			err = WaitForPodPhase(ctx, client, namespace, podName, corev1.PodRunning, 60*time.Second)
			if err != nil {
				// Not fatal - pod might be pending due to resources
				t.Logf("warning: pod did not reach Running phase: %v", err)

				// Check current status
				if err := client.Resources(namespace).Get(ctx, podName, namespace, pod); err == nil {
					t.Logf("pod current phase: %s", pod.Status.Phase)
					for _, cond := range pod.Status.Conditions {
						t.Logf("  condition %s: %s - %s", cond.Type, cond.Status, cond.Reason)
					}
				}
			} else {
				t.Logf("pod %s/%s is running", namespace, podName)
			}

			// Clean up
			if err := DeleteTestPod(ctx, client, namespace, podName); err != nil {
				t.Logf("warning: failed to delete pod: %v", err)
			} else {
				t.Logf("deleted pod %s/%s", namespace, podName)
			}

			return ctx
		}).
		Assess("test namespace has correct labels", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("failed to create K8s client: %v", err)
			}

			namespace := cfg.Namespace()
			if namespace == "" {
				namespace = testNamespace
			}

			// Get the test namespace
			ns := &corev1.Namespace{}
			if err := client.Resources().Get(ctx, namespace, "", ns); err != nil {
				t.Fatalf("failed to get namespace %s: %v", namespace, err)
			}

			// Verify expected label
			if label, ok := ns.Labels["app.kubernetes.io/managed-by"]; !ok || label != "e2e-test" {
				t.Errorf("namespace %s missing expected label app.kubernetes.io/managed-by=e2e-test", namespace)
			} else {
				t.Logf("namespace %s has correct label: %s=%s", namespace, "app.kubernetes.io/managed-by", label)
			}

			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

// TestClusterResources verifies cluster resource availability
func TestClusterResources(t *testing.T) {
	feature := features.New("Cluster Resources").
		Assess("nodes have allocatable resources", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("failed to create K8s client: %v", err)
			}

			nodes := &corev1.NodeList{}
			if err := client.Resources().List(ctx, nodes); err != nil {
				t.Fatalf("failed to list nodes: %v", err)
			}

			for _, node := range nodes.Items {
				cpu := node.Status.Allocatable.Cpu()
				memory := node.Status.Allocatable.Memory()
				pods := node.Status.Allocatable.Pods()

				t.Logf("node %s allocatable:", node.Name)
				t.Logf("  CPU: %s", cpu.String())
				t.Logf("  Memory: %s", memory.String())
				t.Logf("  Pods: %s", pods.String())

				// Basic sanity checks
				if cpu.IsZero() {
					t.Errorf("node %s has zero allocatable CPU", node.Name)
				}
				if memory.IsZero() {
					t.Errorf("node %s has zero allocatable memory", node.Name)
				}
			}

			return ctx
		}).
		Assess("core namespaces exist", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("failed to create K8s client: %v", err)
			}

			coreNamespaces := []string{"kube-system", "kube-public", "default"}

			for _, nsName := range coreNamespaces {
				ns := &corev1.Namespace{}
				if err := client.Resources().Get(ctx, nsName, "", ns); err != nil {
					t.Errorf("core namespace %s not found: %v", nsName, err)
				} else {
					t.Logf("core namespace %s exists", nsName)
				}
			}

			return ctx
		}).
		Assess("kube-system pods are running", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("failed to create K8s client: %v", err)
			}

			pods := &corev1.PodList{}
			if err := client.Resources("kube-system").List(ctx, pods); err != nil {
				t.Fatalf("failed to list kube-system pods: %v", err)
			}

			runningCount := 0
			for _, pod := range pods.Items {
				if pod.Status.Phase == corev1.PodRunning {
					runningCount++
				}
			}

			t.Logf("kube-system: %d/%d pods running", runningCount, len(pods.Items))

			if runningCount == 0 {
				t.Error("no running pods in kube-system")
			}

			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}
