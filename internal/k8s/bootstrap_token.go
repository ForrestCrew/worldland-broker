package k8s

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net/url"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// CreateBootstrapToken creates a K8s bootstrap token via the Secret API in kube-system.
// Returns tokenID (6 chars), tokenSecret (16 chars), and any error.
func CreateBootstrapToken(ctx context.Context, clientset kubernetes.Interface, ttl time.Duration) (string, string, error) {
	tokenID, err := randomAlphaNum(6)
	if err != nil {
		return "", "", fmt.Errorf("generate token ID: %w", err)
	}

	tokenSecret, err := randomAlphaNum(16)
	if err != nil {
		return "", "", fmt.Errorf("generate token secret: %w", err)
	}

	expiration := time.Now().Add(ttl).UTC().Format(time.RFC3339)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bootstrap-token-" + tokenID,
			Namespace: "kube-system",
		},
		Type: corev1.SecretType("bootstrap.kubernetes.io/token"),
		StringData: map[string]string{
			"token-id":                       tokenID,
			"token-secret":                   tokenSecret,
			"usage-bootstrap-authentication": "true",
			"usage-bootstrap-signing":        "true",
			"auth-extra-groups":              "system:bootstrappers:kubeadm:default-node-token",
			"expiration":                     expiration,
		},
	}

	_, err = clientset.CoreV1().Secrets("kube-system").Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		return "", "", fmt.Errorf("create bootstrap token secret: %w", err)
	}

	return tokenID, tokenSecret, nil
}

// ComputeCACertHash computes the sha256 hash of the CA certificate's DER encoding.
// Returns "sha256:<hex>" format used by kubeadm.
func ComputeCACertHash(caCertPEM []byte) (string, error) {
	block, _ := pem.Decode(caCertPEM)
	if block == nil {
		return "", fmt.Errorf("failed to PEM-decode CA certificate")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parse CA certificate: %w", err)
	}

	hash := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return "sha256:" + hex.EncodeToString(hash[:]), nil
}

// GenerateJoinCommand builds the kubeadm join command string.
func GenerateJoinCommand(masterAddr, tokenID, tokenSecret, caHash string) string {
	host := extractHostPort(masterAddr)
	return fmt.Sprintf("sudo kubeadm join %s --token %s.%s --discovery-token-ca-cert-hash %s",
		host, tokenID, tokenSecret, caHash)
}

// extractHostPort strips the URL scheme, keeping host:port.
// "https://10.0.0.1:6443" → "10.0.0.1:6443"
func extractHostPort(addr string) string {
	if strings.Contains(addr, "://") {
		u, err := url.Parse(addr)
		if err == nil {
			return u.Host
		}
	}
	return addr
}

// randomAlphaNum generates a random lowercase alphanumeric string of length n.
func randomAlphaNum(n int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = charset[int(b[i])%len(charset)]
	}
	return string(b), nil
}
