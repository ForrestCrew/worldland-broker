package k8s

import (
	"strings"
	"testing"
)

func TestTenantNamespace(t *testing.T) {
	tests := []struct {
		name    string
		address string
		want    string
	}{
		{
			name:    "generates consistent namespace",
			address: "0x1234567890abcdef1234567890abcdef12345678",
			want:    "tenant-",
		},
		{
			name:    "same address produces same namespace",
			address: "0xABCDEF1234567890ABCDEF1234567890ABCDEF12",
			want:    "tenant-",
		},
		{
			name:    "case-insensitive",
			address: "0xabcdef1234567890abcdef1234567890abcdef12",
			want:    "tenant-",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TenantNamespace(tt.address)

			// Check format: tenant-{16-char-hex}
			if !strings.HasPrefix(got, "tenant-") {
				t.Errorf("TenantNamespace() = %v, want prefix 'tenant-'", got)
			}

			// Check length (tenant- = 7 chars, hash = 16 chars)
			if len(got) != 23 {
				t.Errorf("TenantNamespace() length = %v, want 23", len(got))
			}

			// Check K8s naming rules (lowercase, alphanumeric + dash)
			if got != strings.ToLower(got) {
				t.Errorf("TenantNamespace() = %v, want lowercase", got)
			}

			// Check under 63 character limit
			if len(got) > 63 {
				t.Errorf("TenantNamespace() length = %v, exceeds 63 char limit", len(got))
			}
		})
	}

	// Test consistency: same address produces same namespace
	t.Run("same address produces same namespace", func(t *testing.T) {
		addr := "0x1234567890ABCDEF1234567890ABCDEF12345678"
		ns1 := TenantNamespace(addr)
		ns2 := TenantNamespace(addr)

		if ns1 != ns2 {
			t.Errorf("TenantNamespace() not consistent: %v != %v", ns1, ns2)
		}
	})

	// Test case-insensitivity
	t.Run("case-insensitive hashing", func(t *testing.T) {
		addr1 := "0xABCDEF1234567890ABCDEF1234567890ABCDEF12"
		addr2 := "0xabcdef1234567890abcdef1234567890abcdef12"
		ns1 := TenantNamespace(addr1)
		ns2 := TenantNamespace(addr2)

		if ns1 != ns2 {
			t.Errorf("TenantNamespace() not case-insensitive: %v != %v", ns1, ns2)
		}
	})

	// Test uniqueness: different addresses produce different namespaces
	t.Run("different addresses produce different namespaces", func(t *testing.T) {
		addr1 := "0x1111111111111111111111111111111111111111"
		addr2 := "0x2222222222222222222222222222222222222222"
		ns1 := TenantNamespace(addr1)
		ns2 := TenantNamespace(addr2)

		if ns1 == ns2 {
			t.Errorf("TenantNamespace() not unique: %v == %v", ns1, ns2)
		}
	})
}

func TestPodName(t *testing.T) {
	tests := []struct {
		name      string
		sessionID string
		want      string
	}{
		{
			name:      "generates correct format",
			sessionID: "abc123",
			want:      "session-abc123",
		},
		{
			name:      "handles UUID",
			sessionID: "550e8400-e29b-41d4-a716-446655440000",
			want:      "session-550e8400-e29b-41d4-a716-446655440000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PodName(tt.sessionID)
			if got != tt.want {
				t.Errorf("PodName() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSSHServiceName(t *testing.T) {
	tests := []struct {
		name      string
		sessionID string
		want      string
	}{
		{
			name:      "generates correct format",
			sessionID: "abc123",
			want:      "ssh-abc123",
		},
		{
			name:      "handles UUID",
			sessionID: "550e8400-e29b-41d4-a716-446655440000",
			want:      "ssh-550e8400-e29b-41d4-a716-446655440000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SSHServiceName(tt.sessionID)
			if got != tt.want {
				t.Errorf("SSHServiceName() = %v, want %v", got, tt.want)
			}
		})
	}
}
