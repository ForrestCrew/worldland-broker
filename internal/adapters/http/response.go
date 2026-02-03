package http

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// APIResponse is the standard response wrapper
type APIResponse struct {
	Success   bool   `json:"success"`
	Data      any    `json:"data,omitempty"`
	Error     any    `json:"error,omitempty"`
	Timestamp string `json:"timestamp"`
}

// Success sends a successful JSON response
func Success(c *gin.Context, statusCode int, data any) {
	c.JSON(statusCode, APIResponse{
		Success:   true,
		Data:      data,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}

// SuccessOK sends a 200 OK response with data
func SuccessOK(c *gin.Context, data any) {
	Success(c, http.StatusOK, data)
}

// SuccessCreated sends a 201 Created response with data
func SuccessCreated(c *gin.Context, data any) {
	Success(c, http.StatusCreated, data)
}

// SuccessNoContent sends a 204 No Content response
func SuccessNoContent(c *gin.Context) {
	c.Status(http.StatusNoContent)
}

// Error sends an error JSON response
func Error(c *gin.Context, statusCode int, err *APIError) {
	c.JSON(statusCode, APIResponse{
		Success:   false,
		Error:     err,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}

// ErrorBadRequest sends a 400 Bad Request response
func ErrorBadRequest(c *gin.Context, code ErrorCode, message string) {
	Error(c, http.StatusBadRequest, NewAPIError(code, message))
}

// ErrorUnauthorized sends a 401 Unauthorized response
func ErrorUnauthorized(c *gin.Context, message string) {
	Error(c, http.StatusUnauthorized, NewAPIError(ErrCodeUnauthorized, message))
}

// ErrorForbidden sends a 403 Forbidden response
func ErrorForbidden(c *gin.Context, message string) {
	Error(c, http.StatusForbidden, NewAPIError(ErrCodeUnauthorized, message))
}

// ErrorNotFound sends a 404 Not Found response
func ErrorNotFound(c *gin.Context, code ErrorCode, message string) {
	Error(c, http.StatusNotFound, NewAPIError(code, message))
}

// ErrorConflict sends a 409 Conflict response
func ErrorConflict(c *gin.Context, code ErrorCode, message string) {
	Error(c, http.StatusConflict, NewAPIError(code, message))
}

// ErrorInternal sends a 500 Internal Server Error response
func ErrorInternal(c *gin.Context, message string) {
	Error(c, http.StatusInternalServerError, NewAPIError(ErrCodeInternal, message))
}

// ErrorWithDetails sends an error response with additional details
func ErrorWithDetails(c *gin.Context, statusCode int, code ErrorCode, message string, details any) {
	Error(c, statusCode, NewAPIErrorWithDetails(code, message, details))
}
