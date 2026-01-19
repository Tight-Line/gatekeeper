package verifier

import (
	"crypto/subtle"
	"fmt"
	"net/http"
)

// APIKeyVerifier verifies requests by comparing a header value to a known token
// This is used for services like Google Calendar that use channel tokens
type APIKeyVerifier struct {
	header string
	token  string
}

// NewAPIKeyVerifier creates a new API key verifier
func NewAPIKeyVerifier(header, token string) *APIKeyVerifier {
	return &APIKeyVerifier{
		header: header,
		token:  token,
	}
}

// Verify checks that the header value matches the expected token
func (v *APIKeyVerifier) Verify(r *http.Request, _ []byte) error {
	value := r.Header.Get(v.header)
	if value == "" {
		return fmt.Errorf("%w: %s header missing", ErrSignatureEmpty, v.header)
	}

	// Constant-time comparison to prevent timing attacks
	if subtle.ConstantTimeCompare([]byte(value), []byte(v.token)) != 1 {
		return ErrTokenMismatch
	}

	return nil
}

// Type returns the verifier type
func (v *APIKeyVerifier) Type() string {
	return "api_key"
}
