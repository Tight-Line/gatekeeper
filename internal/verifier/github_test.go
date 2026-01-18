package verifier

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGitHubVerifier_Verify(t *testing.T) {
	secret := "test-webhook-secret"
	verifier := NewGitHubVerifier(secret)

	tests := []struct {
		name      string
		setup     func() (*http.Request, []byte)
		wantErr   bool
		errString string
	}{
		{
			name: "valid signature",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"action":"opened","pull_request":{}}`)
				signature := computeGitHubSignature(secret, body)

				req := httptest.NewRequest(http.MethodPost, "/github/webhook", strings.NewReader(string(body)))
				req.Header.Set("X-Hub-Signature-256", signature)
				return req, body
			},
			wantErr: false,
		},
		{
			name: "missing signature header",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"action":"opened"}`)
				req := httptest.NewRequest(http.MethodPost, "/github/webhook", strings.NewReader(string(body)))
				return req, body
			},
			wantErr:   true,
			errString: "signature header is empty",
		},
		{
			name: "invalid signature",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"action":"opened"}`)
				req := httptest.NewRequest(http.MethodPost, "/github/webhook", strings.NewReader(string(body)))
				req.Header.Set("X-Hub-Signature-256", "sha256=invalid")
				return req, body
			},
			wantErr:   true,
			errString: "signature does not match",
		},
		{
			name: "missing sha256 prefix",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"action":"opened"}`)
				req := httptest.NewRequest(http.MethodPost, "/github/webhook", strings.NewReader(string(body)))
				req.Header.Set("X-Hub-Signature-256", "noprefixhere")
				return req, body
			},
			wantErr:   true,
			errString: "signature missing sha256= prefix",
		},
		{
			name: "tampered body",
			setup: func() (*http.Request, []byte) {
				originalBody := []byte(`{"action":"opened"}`)
				tamperedBody := []byte(`{"action":"closed"}`)
				signature := computeGitHubSignature(secret, originalBody)

				req := httptest.NewRequest(http.MethodPost, "/github/webhook", strings.NewReader(string(tamperedBody)))
				req.Header.Set("X-Hub-Signature-256", signature)
				return req, tamperedBody
			},
			wantErr:   true,
			errString: "signature does not match",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, body := tt.setup()
			err := verifier.Verify(req, body)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				} else if tt.errString != "" && !strings.Contains(err.Error(), tt.errString) {
					t.Errorf("expected error containing %q, got %q", tt.errString, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestGitHubVerifier_Type(t *testing.T) {
	v := NewGitHubVerifier("secret")
	if v.Type() != "github" {
		t.Errorf("expected type 'github', got %q", v.Type())
	}
}

// computeGitHubSignature computes a valid GitHub signature for testing
func computeGitHubSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
