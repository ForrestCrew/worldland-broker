package http

// ErrorCode represents standardized API error codes
type ErrorCode string

const (
	// Authentication errors (AUTH_xxx)
	ErrCodeUnauthorized     ErrorCode = "AUTH_UNAUTHORIZED"
	ErrCodeInvalidToken     ErrorCode = "AUTH_INVALID_TOKEN"
	ErrCodeTokenExpired     ErrorCode = "AUTH_TOKEN_EXPIRED"
	ErrCodeInvalidSignature ErrorCode = "AUTH_INVALID_SIGNATURE"
	ErrCodeInvalidNonce     ErrorCode = "AUTH_INVALID_NONCE"
	ErrCodeSessionExpired   ErrorCode = "AUTH_SESSION_EXPIRED"

	// Validation errors (VAL_xxx)
	ErrCodeValidation   ErrorCode = "VAL_INVALID_INPUT"
	ErrCodeMissingField ErrorCode = "VAL_MISSING_FIELD"
	ErrCodeInvalidFormat ErrorCode = "VAL_INVALID_FORMAT"

	// Resource errors (RES_xxx)
	ErrCodeNotFound      ErrorCode = "RES_NOT_FOUND"
	ErrCodeAlreadyExists ErrorCode = "RES_ALREADY_EXISTS"
	ErrCodeConflict      ErrorCode = "RES_CONFLICT"

	// Provider errors (PROV_xxx)
	ErrCodeProviderNotFound ErrorCode = "PROV_NOT_FOUND"
	ErrCodeNodeNotFound     ErrorCode = "PROV_NODE_NOT_FOUND"
	ErrCodeNodeNotAvailable ErrorCode = "PROV_NODE_NOT_AVAILABLE"

	// Rental errors (RENT_xxx)
	ErrCodeSessionNotFound     ErrorCode = "RENT_SESSION_NOT_FOUND"
	ErrCodeInvalidSessionState ErrorCode = "RENT_INVALID_STATE"
	ErrCodeInsufficientBalance ErrorCode = "RENT_INSUFFICIENT_BALANCE"

	// Server errors (SRV_xxx)
	ErrCodeInternal           ErrorCode = "SRV_INTERNAL_ERROR"
	ErrCodeServiceUnavailable ErrorCode = "SRV_UNAVAILABLE"
	ErrCodeTimeout            ErrorCode = "SRV_TIMEOUT"
)

// APIError represents a structured API error
type APIError struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
	Details any       `json:"details,omitempty"`
}

// Error implements the error interface
func (e *APIError) Error() string {
	return e.Message
}

// NewAPIError creates a new API error
func NewAPIError(code ErrorCode, message string) *APIError {
	return &APIError{
		Code:    code,
		Message: message,
	}
}

// NewAPIErrorWithDetails creates a new API error with additional details
func NewAPIErrorWithDetails(code ErrorCode, message string, details any) *APIError {
	return &APIError{
		Code:    code,
		Message: message,
		Details: details,
	}
}
