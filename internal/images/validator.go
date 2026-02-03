// Package images provides Docker image format validation for user-provided custom images.
// It validates image references before passing them to K8s for Pod creation.
package images

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// MaxImageLength is the maximum allowed length for a Docker image reference
const MaxImageLength = 500

// Common validation errors
var (
	ErrEmptyImage       = errors.New("image reference is empty")
	ErrImageTooLong     = errors.New("image reference is too long (max 500 characters)")
	ErrInvalidFormat    = errors.New("invalid Docker image format")
)

// Docker image reference regex pattern
// Supports: registry/repo:tag, registry/repo@digest, repo:tag, repo
// Based on Docker distribution reference specification
var (
	// Name component: lowercase letters, digits, and separators (., _, -, __...)
	// First char must be alphanumeric
	nameComponent = `[a-z0-9]+(?:[._-][a-z0-9]+)*`

	// Registry/hostname: alphanumeric with optional port
	registry = `[a-zA-Z0-9][-a-zA-Z0-9.]*(?::[0-9]+)?`

	// Tag: alphanumeric with .-_
	tag = `[a-zA-Z0-9][-a-zA-Z0-9._]*`

	// Digest: sha256:64 hex chars
	digest = `sha256:[a-f0-9]{64}`

	// Full pattern:
	// Optional registry with /
	// One or more name components separated by /
	// Optional tag (:tag) or digest (@sha256:...)
	imagePattern = regexp.MustCompile(
		`^` +
			// Optional registry (hostname with optional port) followed by /
			`(?:` + registry + `/)?` +
			// Repository path: one or more name components separated by /
			nameComponent + `(?:/` + nameComponent + `)*` +
			// Optional tag or digest
			`(?::` + tag + `|@` + digest + `)?` +
			`$`,
	)

	// UUID pattern for preset image IDs
	uuidPattern = regexp.MustCompile(
		`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`,
	)
)

// ImageValidator validates Docker image references
type ImageValidator struct{}

// NewValidator creates a new ImageValidator
func NewValidator() *ImageValidator {
	return &ImageValidator{}
}

// ValidateFormat validates a Docker image reference string.
// It returns nil if the format is valid, or an error describing the issue.
//
// Valid formats:
//   - Standard library images: ubuntu, alpine
//   - Official images: pytorch/pytorch:2.6.0-cuda12.6-cudnn9-devel
//   - Private registries: gcr.io/my-project/custom:v1
//   - With digest: ubuntu@sha256:abc123... (64 hex chars)
//   - No tag (defaults to :latest): nginx
//
// Invalid formats:
//   - Empty string
//   - Too long (>500 chars)
//   - Invalid characters: spaces, !, <, >, ;, etc.
//   - Missing repository: :latest
//   - Malformed: image::tag, /repo, repo/, repo//image
func (v *ImageValidator) ValidateFormat(image string) error {
	// Check empty
	if image == "" {
		return ErrEmptyImage
	}

	// Check length
	if len(image) > MaxImageLength {
		return ErrImageTooLong
	}

	// Check for obviously invalid characters
	if strings.ContainsAny(image, " !<>;\"'`$&|\\{}[]()") {
		return fmt.Errorf("%w: contains invalid characters", ErrInvalidFormat)
	}

	// Check for structural issues
	if strings.HasPrefix(image, "/") {
		return fmt.Errorf("%w: cannot start with /", ErrInvalidFormat)
	}
	if strings.HasSuffix(image, "/") {
		return fmt.Errorf("%w: cannot end with /", ErrInvalidFormat)
	}
	if strings.Contains(image, "//") {
		return fmt.Errorf("%w: contains //", ErrInvalidFormat)
	}
	if strings.HasPrefix(image, ":") {
		return fmt.Errorf("%w: missing repository name", ErrInvalidFormat)
	}
	if strings.Contains(image, "::") {
		return fmt.Errorf("%w: contains ::", ErrInvalidFormat)
	}

	// Validate digest format if present
	if strings.Contains(image, "@") {
		parts := strings.SplitN(image, "@", 2)
		if len(parts) == 2 {
			digestPart := parts[1]
			// Must be sha256 with exactly 64 hex chars
			if !strings.HasPrefix(digestPart, "sha256:") {
				return fmt.Errorf("%w: digest must use sha256 algorithm", ErrInvalidFormat)
			}
			hash := strings.TrimPrefix(digestPart, "sha256:")
			if len(hash) != 64 {
				return fmt.Errorf("%w: sha256 digest must be exactly 64 hex characters", ErrInvalidFormat)
			}
			// Validate hex chars
			for _, c := range hash {
				if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
					return fmt.Errorf("%w: digest contains non-hex character", ErrInvalidFormat)
				}
			}
		}
	}

	// Final regex validation for overall format
	if !imagePattern.MatchString(strings.ToLower(image)) {
		return fmt.Errorf("%w: does not match Docker image reference pattern", ErrInvalidFormat)
	}

	return nil
}

// ValidateImageFormat is a convenience function for one-off validation.
// It creates a new validator and validates the image format.
func ValidateImageFormat(image string) error {
	return NewValidator().ValidateFormat(image)
}

// IsPresetID checks if the string looks like a UUID (preset image ID)
// vs a Docker image reference.
// Preset IDs are UUIDs in the format 8-4-4-4-12 hex chars with dashes.
func IsPresetID(s string) bool {
	return uuidPattern.MatchString(strings.ToLower(s))
}
