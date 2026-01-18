package verifier

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
)

const (
	githubSignatureHeader = "X-Hub-Signature-256"
	githubSignaturePrefix = "sha256="
)

// GitHubVerifier verifies GitHub webhook signatures
// See: https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries
type GitHubVerifier struct {
	secret string
}

// NewGitHubVerifier creates a new GitHub verifier
func NewGitHubVerifier(secret string) *GitHubVerifier {
	return &GitHubVerifier{
		secret: secret,
	}
}

// Verify checks the GitHub signature
func (v *GitHubVerifier) Verify(r *http.Request, payload []byte) error {
	signature := r.Header.Get(githubSignatureHeader)
	if signature == "" {
		return fmt.Errorf("%w: %s header missing", ErrSignatureEmpty, githubSignatureHeader)
	}

	// Remove the sha256= prefix
	if !strings.HasPrefix(signature, githubSignaturePrefix) {
		return fmt.Errorf("%w: signature missing %s prefix", ErrSignatureMismatch, githubSignaturePrefix)
	}
	sigHex := strings.TrimPrefix(signature, githubSignaturePrefix)

	// Decode the hex signature
	sigBytes, err := hex.DecodeString(sigHex)
	if err != nil {
		return fmt.Errorf("%w: cannot decode signature hex", ErrSignatureMismatch)
	}

	// Compute expected signature
	mac := hmac.New(sha256.New, []byte(v.secret))
	mac.Write(payload)
	expectedSig := mac.Sum(nil)

	// Constant-time comparison
	if !hmac.Equal(sigBytes, expectedSig) {
		return ErrSignatureMismatch
	}

	return nil
}

// Type returns the verifier type
func (v *GitHubVerifier) Type() string {
	return "github"
}
