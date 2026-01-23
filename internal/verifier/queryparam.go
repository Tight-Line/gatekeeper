package verifier

import (
	"crypto/subtle"
	"fmt"
	"net/http"
)

// QueryParamVerifier verifies requests by comparing a URL query parameter to a known token
type QueryParamVerifier struct {
	name  string
	token string
}

// NewQueryParamVerifier creates a new query parameter verifier
func NewQueryParamVerifier(name, token string) *QueryParamVerifier {
	return &QueryParamVerifier{
		name:  name,
		token: token,
	}
}

// Verify checks that the query parameter value matches the expected token
func (v *QueryParamVerifier) Verify(r *http.Request, _ []byte) error {
	value := r.URL.Query().Get(v.name)
	if value == "" {
		return fmt.Errorf("%w: %s query parameter missing", ErrSignatureEmpty, v.name)
	}

	// Constant-time comparison to prevent timing attacks
	if subtle.ConstantTimeCompare([]byte(value), []byte(v.token)) != 1 {
		return ErrTokenMismatch
	}

	return nil
}

// Type returns the verifier type
func (v *QueryParamVerifier) Type() string {
	return "query_param"
}
