package images

import (
	"strings"
	"testing"
)

func TestImageValidator_ValidateFormat(t *testing.T) {
	validator := NewValidator()

	tests := []struct {
		name      string
		image     string
		wantErr   bool
		errSubstr string
	}{
		// Valid formats - standard library images
		{
			name:    "standard library image ubuntu",
			image:   "ubuntu",
			wantErr: false,
		},
		{
			name:    "standard library image alpine",
			image:   "alpine",
			wantErr: false,
		},
		{
			name:    "standard library image nginx",
			image:   "nginx",
			wantErr: false,
		},

		// Valid formats - with tags
		{
			name:    "ubuntu with version tag",
			image:   "ubuntu:22.04",
			wantErr: false,
		},
		{
			name:    "alpine with latest tag",
			image:   "alpine:latest",
			wantErr: false,
		},

		// Valid formats - official images with org
		{
			name:    "pytorch official image",
			image:   "pytorch/pytorch:2.6.0-cuda12.6-cudnn9-devel",
			wantErr: false,
		},
		{
			name:    "tensorflow with gpu tag",
			image:   "tensorflow/tensorflow:latest-gpu",
			wantErr: false,
		},
		{
			name:    "nvidia cuda image",
			image:   "nvidia/cuda:12.6.0-devel-ubuntu22.04",
			wantErr: false,
		},

		// Valid formats - private registries
		{
			name:    "gcr.io registry",
			image:   "gcr.io/my-project/custom:v1",
			wantErr: false,
		},
		{
			name:    "docker.io registry",
			image:   "docker.io/library/nginx:1.25",
			wantErr: false,
		},
		{
			name:    "quay.io registry",
			image:   "quay.io/prometheus/prometheus:v2.48.0",
			wantErr: false,
		},
		{
			name:    "ghcr.io registry",
			image:   "ghcr.io/owner/repo:tag",
			wantErr: false,
		},
		{
			name:    "private registry with port",
			image:   "registry.example.com:5000/myapp:v1.0.0",
			wantErr: false,
		},

		// Valid formats - with digest
		{
			name:    "image with sha256 digest",
			image:   "ubuntu@sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
			wantErr: false,
		},
		{
			name:    "registry image with digest",
			image:   "gcr.io/project/image@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			wantErr: false,
		},

		// Valid formats - complex tags
		{
			name:    "image with hyphen and underscore in tag",
			image:   "myrepo/myimage:v1.0.0-beta_1",
			wantErr: false,
		},
		{
			name:    "image with dots in tag",
			image:   "library/python:3.11.6-slim",
			wantErr: false,
		},

		// Invalid formats - empty
		{
			name:      "empty string",
			image:     "",
			wantErr:   true,
			errSubstr: "empty",
		},

		// Invalid formats - too long
		{
			name:      "exceeds max length",
			image:     strings.Repeat("a", 501),
			wantErr:   true,
			errSubstr: "too long",
		},
		{
			name:    "at max length 500 chars",
			image:   strings.Repeat("a", 500),
			wantErr: false,
		},

		// Invalid formats - invalid characters
		{
			name:      "contains exclamation mark",
			image:     "invalid image!",
			wantErr:   true,
			errSubstr: "invalid",
		},
		{
			name:      "contains space",
			image:     "path/with spaces/image",
			wantErr:   true,
			errSubstr: "invalid",
		},
		{
			name:      "contains angle brackets",
			image:     "image<script>",
			wantErr:   true,
			errSubstr: "invalid",
		},
		{
			name:      "contains semicolon",
			image:     "image;rm -rf /",
			wantErr:   true,
			errSubstr: "invalid",
		},

		// Invalid formats - malformed references
		{
			name:      "missing repository (tag only)",
			image:     ":latest",
			wantErr:   true,
			errSubstr: "invalid",
		},
		{
			name:      "double colon",
			image:     "image::tag",
			wantErr:   true,
			errSubstr: "invalid",
		},
		{
			name:      "trailing slash",
			image:     "repo/image/",
			wantErr:   true,
			errSubstr: "invalid",
		},
		{
			name:      "leading slash",
			image:     "/repo/image",
			wantErr:   true,
			errSubstr: "invalid",
		},
		{
			name:      "double slash",
			image:     "repo//image",
			wantErr:   true,
			errSubstr: "invalid",
		},

		// Invalid formats - malformed digests
		{
			name:      "digest with wrong length",
			image:     "ubuntu@sha256:abc123",
			wantErr:   true,
			errSubstr: "invalid",
		},
		{
			name:      "digest with invalid algorithm",
			image:     "ubuntu@md5:abcdef0123456789abcdef0123456789",
			wantErr:   true,
			errSubstr: "invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidateFormat(tt.image)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateFormat(%q) error = %v, wantErr %v", tt.image, err, tt.wantErr)
				return
			}
			if tt.wantErr && tt.errSubstr != "" {
				if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tt.errSubstr)) {
					t.Errorf("ValidateFormat(%q) error = %v, want error containing %q", tt.image, err, tt.errSubstr)
				}
			}
		})
	}
}

func TestValidateImageFormat_ConvenienceFunction(t *testing.T) {
	// Test the package-level convenience function
	err := ValidateImageFormat("ubuntu:22.04")
	if err != nil {
		t.Errorf("ValidateImageFormat(valid) = %v, want nil", err)
	}

	err = ValidateImageFormat("")
	if err == nil {
		t.Error("ValidateImageFormat(empty) = nil, want error")
	}
}

func TestIsPresetID(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect bool
	}{
		{
			name:   "valid lowercase UUID",
			input:  "550e8400-e29b-41d4-a716-446655440000",
			expect: true,
		},
		{
			name:   "valid uppercase UUID",
			input:  "550E8400-E29B-41D4-A716-446655440000",
			expect: true,
		},
		{
			name:   "valid mixed case UUID",
			input:  "550e8400-E29B-41d4-A716-446655440000",
			expect: true,
		},
		{
			name:   "docker image - not UUID",
			input:  "ubuntu:22.04",
			expect: false,
		},
		{
			name:   "registry image - not UUID",
			input:  "gcr.io/project/image:v1",
			expect: false,
		},
		{
			name:   "empty string - not UUID",
			input:  "",
			expect: false,
		},
		{
			name:   "UUID without dashes - not valid",
			input:  "550e8400e29b41d4a716446655440000",
			expect: false,
		},
		{
			name:   "UUID with wrong segment lengths",
			input:  "550e840-0e29b-41d4-a716-446655440000",
			expect: false,
		},
		{
			name:   "short string - not UUID",
			input:  "abc-123",
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsPresetID(tt.input)
			if got != tt.expect {
				t.Errorf("IsPresetID(%q) = %v, want %v", tt.input, got, tt.expect)
			}
		})
	}
}
