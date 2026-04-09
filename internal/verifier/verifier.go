package verifier

import (
	"errors"
	"net/http"
)

// Common errors
var (
	ErrSignatureEmpty    = errors.New("signature header is empty")
	ErrSignatureMismatch = errors.New("signature does not match")
	ErrTimestampInvalid  = errors.New("timestamp is invalid")
	ErrTimestampExpired  = errors.New("timestamp is too old")
	ErrTokenMismatch     = errors.New("token does not match")
	ErrTokenMissing      = errors.New("bearer token missing")
	ErrTokenExpired      = errors.New("token is expired")
	ErrTokenInvalid      = errors.New("token is invalid")
	ErrClaimMismatch     = errors.New("required claim does not match")
)

// Verifier verifies incoming webhook requests
type Verifier interface {
	// Verify checks if the request is authentic
	// The payload is the raw request body
	Verify(r *http.Request, payload []byte) error

	// Type returns the verifier type name
	Type() string
}
