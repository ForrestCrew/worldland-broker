//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/pkg/env"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/envfuncs"
	"sigs.k8s.io/e2e-framework/support"
	"sigs.k8s.io/e2e-framework/support/kind"
)

var (
	testEnv         env.Environment
	kindClusterName = "worldland-e2e"
)

func TestMain(m *testing.M) {
	// Check for existing cluster mode
	useExistingCluster := os.Getenv("USE_EXISTING_CLUSTER") == "true"
	existingClusterName := os.Getenv("CLUSTER_NAME")
	if existingClusterName == "" {
		existingClusterName = "worldland-dev"
	}

	// Parse flags
	cfg, err := envconf.NewFromFlags()
	if err != nil {
		fmt.Printf("failed to create env config: %v\n", err)
		os.Exit(1)
	}

	testEnv = env.NewWithConfig(cfg)

	if useExistingCluster {
		// Use existing Kind cluster (for local development)
		testEnv.Setup(
			useExistingKindCluster(existingClusterName),
			setupTestNamespace(),
		)
		testEnv.Finish(
			cleanupTestNamespace(),
		)
	} else {
		// Create ephemeral Kind cluster (for CI)
		testEnv.Setup(
			envfuncs.CreateCluster(kindProvider(), kindClusterName),
			setupTestNamespace(),
		)
		testEnv.Finish(
			cleanupTestNamespace(),
			envfuncs.DestroyCluster(kindClusterName),
		)
	}

	// Run tests
	os.Exit(testEnv.Run(m))
}

// kindProvider returns the Kind cluster provider for e2e-framework
func kindProvider() support.E2EClusterProvider {
	return kind.NewProvider()
}

// useExistingKindCluster returns a setup function that connects to an existing Kind cluster
func useExistingKindCluster(clusterName string) env.Func {
	return func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
		// Get kubeconfig path
		kubeconfigPath := os.Getenv("KUBECONFIG")
		if kubeconfigPath == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return ctx, fmt.Errorf("failed to get home dir: %w", err)
			}
			kubeconfigPath = fmt.Sprintf("%s/.kube/config", home)
		}

		// Set the kubeconfig path in config
		cfg.WithKubeconfigFile(kubeconfigPath)

		fmt.Printf("Using existing Kind cluster: %s\n", clusterName)
		fmt.Printf("Kubeconfig: %s\n", kubeconfigPath)

		return ctx, nil
	}
}

// testNamespace is the namespace used for E2E tests
const testNamespace = "e2e-test"

// setupTestNamespace creates the test namespace
func setupTestNamespace() env.Func {
	return func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
		client, err := cfg.NewClient()
		if err != nil {
			return ctx, fmt.Errorf("failed to create client: %w", err)
		}

		// Create namespace
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: testNamespace,
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "e2e-test",
				},
			},
		}
		if err := client.Resources().Create(ctx, ns); err != nil {
			// Ignore if already exists
			fmt.Printf("Namespace %s may already exist: %v\n", testNamespace, err)
		}

		// Store namespace in context for tests
		cfg.WithNamespace(testNamespace)

		fmt.Printf("Test namespace: %s\n", testNamespace)
		return ctx, nil
	}
}

// cleanupTestNamespace deletes the test namespace
func cleanupTestNamespace() env.Func {
	return func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
		client, err := cfg.NewClient()
		if err != nil {
			return ctx, fmt.Errorf("failed to create client: %w", err)
		}

		// Delete namespace
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: testNamespace,
			},
		}
		if err := client.Resources().Delete(ctx, ns); err != nil {
			fmt.Printf("Failed to delete namespace %s: %v\n", testNamespace, err)
		}

		return ctx, nil
	}
}
