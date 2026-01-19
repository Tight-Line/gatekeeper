package verifier

import "net/http"

// NoopVerifier is a verifier that always succeeds
// Use this for testing or when verification is handled elsewhere
type NoopVerifier struct{}

// NewNoopVerifier creates a new noop verifier
func NewNoopVerifier() *NoopVerifier {
	return &NoopVerifier{}
}

// Verify always returns nil (success)
func (v *NoopVerifier) Verify(_ *http.Request, _ []byte) error {
	return nil
}

// Type returns the verifier type
func (v *NoopVerifier) Type() string {
	return "noop"
}
