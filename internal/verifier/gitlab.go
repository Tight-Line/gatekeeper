package verifier

import (
	"crypto/subtle"
	"fmt"
	"net/http"
)

// GitLabVerifier verifies requests using the X-Gitlab-Token header.
// GitLab webhooks use simple token comparison (not HMAC).
type GitLabVerifier struct {
	token string
}

// NewGitLabVerifier creates a new GitLab webhook verifier
func NewGitLabVerifier(token string) *GitLabVerifier {
	return &GitLabVerifier{
		token: token,
	}
}

// Verify checks that the X-Gitlab-Token header matches the expected token
func (v *GitLabVerifier) Verify(r *http.Request, _ []byte) error {
	value := r.Header.Get("X-Gitlab-Token")
	if value == "" {
		return fmt.Errorf("%w: X-Gitlab-Token header missing", ErrSignatureEmpty)
	}

	// Constant-time comparison to prevent timing attacks
	if subtle.ConstantTimeCompare([]byte(value), []byte(v.token)) != 1 {
		return ErrTokenMismatch
	}

	return nil
}

// Type returns the verifier type
func (v *GitLabVerifier) Type() string {
	return "gitlab"
}
