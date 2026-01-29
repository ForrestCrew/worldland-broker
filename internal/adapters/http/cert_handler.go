package http

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/worldland/worldland-hub/internal/services"
)

// CertHandler handles certificate-related HTTP requests
type CertHandler struct {
	certService *services.CertService
}

// NewCertHandler creates a new certificate handler
func NewCertHandler(certService *services.CertService) *CertHandler {
	return &CertHandler{certService: certService}
}

// IssueCertificate issues a new mTLS certificate for a node
// POST /api/v1/nodes/:id/certificate
func (h *CertHandler) IssueCertificate(c *gin.Context) {
	providerID := c.GetString("provider_id")
	nodeID := c.Param("id")

	if nodeID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Node ID is required"})
		return
	}

	bundle, err := h.certService.IssueCertificate(c.Request.Context(), nodeID, providerID)
	if err != nil {
		if err.Error() == "not authorized" {
			c.JSON(http.StatusForbidden, gin.H{"error": "Not authorized to issue certificate for this node"})
			return
		}
		if strings.Contains(err.Error(), "node not found") {
			c.JSON(http.StatusNotFound, gin.H{"error": "Node not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"certificate": string(bundle.Certificate),
		"private_key": string(bundle.PrivateKey),
		"expires_at":  bundle.ExpiresAt.Format(time.RFC3339),
		"node_id":     bundle.NodeID,
		"message":     "Certificate issued. Configure node with these credentials.",
	})
}

// GetRootCA returns the CA certificate for node TLS verification
// GET /api/v1/ca/root
func (h *CertHandler) GetRootCA(c *gin.Context) {
	caCert := h.certService.GetRootCA()
	c.Data(http.StatusOK, "application/x-pem-file", caCert)
}

