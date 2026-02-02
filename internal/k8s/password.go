package k8s

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// GenerateSSHPassword generates a cryptographically secure random password
// Uses 16 alphanumeric characters for sufficient entropy and easy typing
func GenerateSSHPassword() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 16)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		b[i] = charset[n.Int64()]
	}
	return string(b)
}

// SSHSecretName generates a K8s Secret name from session ID
func SSHSecretName(sessionID string) string {
	return fmt.Sprintf("ssh-creds-%s", sessionID)
}

// createSSHSecret creates a K8s Secret containing SSH password
func createSSHSecret(ctx context.Context, clientset kubernetes.Interface, namespace, sessionID, password string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SSHSecretName(sessionID),
			Namespace: namespace,
			Labels: map[string]string{
				LabelSessionID: sessionID,
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"password": password,
		},
	}

	_, err := clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	return err
}

// GetSSHPassword retrieves SSH password from K8s Secret
func GetSSHPassword(ctx context.Context, clientset kubernetes.Interface, namespace, sessionID string) (string, error) {
	secret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, SSHSecretName(sessionID), metav1.GetOptions{})
	if err != nil {
		return "", err
	}

	password, ok := secret.Data["password"]
	if !ok {
		return "", fmt.Errorf("password not found in secret")
	}

	return string(password), nil
}

// deleteSSHSecret deletes SSH Secret (idempotent - ignores NotFound)
func deleteSSHSecret(ctx context.Context, clientset kubernetes.Interface, namespace, sessionID string) error {
	err := clientset.CoreV1().Secrets(namespace).Delete(ctx, SSHSecretName(sessionID), metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}
