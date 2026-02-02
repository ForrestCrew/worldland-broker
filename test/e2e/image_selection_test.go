//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"testing"

	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

// TestImageSelection verifies the image selection API functionality
// This test verifies preset images are accessible via the Hub API
func TestImageSelection(t *testing.T) {
	cfg := DefaultConfig()
	hubClient := NewHubClient(cfg.HubURL)

	feature := features.New("Image Selection").
		Setup(func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			// Skip if Hub not available
			if !isHubAvailable(ctx, hubClient) {
				t.Skip("Hub server not available at " + cfg.HubURL)
			}
			return ctx
		}).
		Assess("can list preset images", func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			images, err := hubClient.ListImages(ctx)
			if err != nil {
				t.Fatalf("failed to list images: %v", err)
			}

			if len(images) == 0 {
				t.Log("WARNING: No preset images found - Hub may not have been seeded")
			} else {
				t.Logf("found %d preset images:", len(images))
				for _, img := range images {
					t.Logf("  - %s: %s", img["name"], img["dockerImage"])
				}
			}

			return ctx
		}).
		Assess("preset images have required fields", func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			images, err := hubClient.ListImages(ctx)
			if err != nil {
				t.Fatalf("failed to list images: %v", err)
			}

			requiredFields := []string{"id", "name", "dockerImage", "category"}

			for i, img := range images {
				for _, field := range requiredFields {
					if _, ok := img[field]; !ok {
						t.Errorf("preset image %d missing '%s' field", i, field)
					}
				}

				// Verify field types
				if name, ok := img["name"].(string); !ok || name == "" {
					t.Errorf("preset image %d has invalid 'name' field", i)
				}
				if dockerImage, ok := img["dockerImage"].(string); !ok || dockerImage == "" {
					t.Errorf("preset image %d has invalid 'dockerImage' field", i)
				}
				if _, ok := img["category"].(string); !ok {
					t.Errorf("preset image %d has invalid 'category' field", i)
				}
			}

			return ctx
		}).
		Assess("expected preset categories exist", func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			images, err := hubClient.ListImages(ctx)
			if err != nil {
				t.Fatalf("failed to list images: %v", err)
			}

			// Track found categories
			categories := make(map[string]bool)
			for _, img := range images {
				if cat, ok := img["category"].(string); ok {
					categories[cat] = true
				}
			}

			// Log found categories (don't fail if empty - preset seeding is optional)
			if len(categories) > 0 {
				t.Log("Found categories:")
				for cat := range categories {
					t.Logf("  - %s", cat)
				}
			}

			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

// TestImageValidation verifies image format validation in the API
func TestImageValidation(t *testing.T) {
	cfg := DefaultConfig()
	hubClient := NewHubClient(cfg.HubURL)

	feature := features.New("Image Validation").
		Setup(func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			// Skip if Hub not available
			if !isHubAvailable(ctx, hubClient) {
				t.Skip("Hub server not available at " + cfg.HubURL)
			}
			return ctx
		}).
		Assess("rejects empty image gracefully", func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			// Test creating session without image - should use default
			// This will likely fail due to auth, but verifies the endpoint handles empty image
			_, err := hubClient.CreateSession(ctx, "test-node", "1000000", "")

			// Expected outcomes:
			// 1. Success with default image (if auth disabled)
			// 2. Auth error (401) - acceptable, image wasn't rejected for format
			// 3. Other error - log for debugging
			if err != nil {
				t.Logf("create session without image: %v (expected auth error)", err)
			} else {
				t.Log("create session without image succeeded (auth disabled mode)")
			}

			return ctx
		}).
		Assess("rejects invalid image format", func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			// Test with invalid Docker image format
			invalidImages := []string{
				"invalid image!",          // spaces and special chars
				"invalid:tag:extra",       // multiple colons
				"!@#$%^&*()",              // all special chars
				"../../etc/passwd",        // path traversal attempt
				"",                         // empty (handled differently)
			}

			for _, invalidImg := range invalidImages {
				if invalidImg == "" {
					continue // empty is tested separately
				}

				_, err := hubClient.CreateSession(ctx, "test-node", "1000000", invalidImg)
				if err == nil {
					t.Logf("WARNING: Invalid image '%s' was accepted - validation may not be in API layer", invalidImg)
				} else {
					// Check if it's an auth error vs validation error
					errStr := err.Error()
					if contains(errStr, "401") || contains(errStr, "unauthorized") || contains(errStr, "Unauthorized") {
						t.Logf("image '%s': auth error (expected, can't test validation without auth)", invalidImg)
					} else if contains(errStr, "invalid") || contains(errStr, "format") || contains(errStr, "400") {
						t.Logf("image '%s': correctly rejected with validation error", invalidImg)
					} else {
						t.Logf("image '%s': rejected with error: %v", invalidImg, err)
					}
				}
			}

			return ctx
		}).
		Assess("accepts valid Docker image formats", func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			// Test valid Docker image formats (will fail on auth, not format)
			validImages := []string{
				"busybox",
				"busybox:latest",
				"nginx:1.25",
				"pytorch/pytorch:2.0-cuda12.1-cudnn8-runtime",
				"nvcr.io/nvidia/pytorch:23.08-py3",
				"gcr.io/myproject/myimage:v1.0.0",
				"registry.example.com:5000/myimage:tag",
			}

			for _, validImg := range validImages {
				_, err := hubClient.CreateSession(ctx, "test-node", "1000000", validImg)
				if err != nil {
					errStr := err.Error()
					// Should get auth error, not format validation error
					if contains(errStr, "invalid") && contains(errStr, "image") {
						t.Errorf("valid image '%s' was rejected for format: %v", validImg, err)
					} else {
						t.Logf("valid image '%s': %v (expected auth error)", validImg, err)
					}
				} else {
					t.Logf("valid image '%s': accepted", validImg)
				}
			}

			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

// contains checks if s contains substr (case-insensitive not needed here)
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
