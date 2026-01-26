package verifier

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGitLabVerifier_Verify(t *testing.T) {
	token := "my-gitlab-secret-token"
	verifier := NewGitLabVerifier(token)

	tests := []verifierTestCase{
		{
			name: "valid token",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"event_type":"push"}`)
				req := httptest.NewRequest(http.MethodPost, "/gitlab/webhook", strings.NewReader(string(body)))
				req.Header.Set("X-Gitlab-Token", token)
				return req, body
			},
			wantErr: false,
		},
		{
			name: "missing header",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"event_type":"push"}`)
				req := httptest.NewRequest(http.MethodPost, "/gitlab/webhook", strings.NewReader(string(body)))
				return req, body
			},
			wantErr:   true,
			errString: "signature header is empty",
		},
		{
			name: "wrong token",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"event_type":"push"}`)
				req := httptest.NewRequest(http.MethodPost, "/gitlab/webhook", strings.NewReader(string(body)))
				req.Header.Set("X-Gitlab-Token", "wrong-token")
				return req, body
			},
			wantErr:   true,
			errString: "token does not match",
		},
		{
			name: "empty token in header",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"event_type":"push"}`)
				req := httptest.NewRequest(http.MethodPost, "/gitlab/webhook", strings.NewReader(string(body)))
				req.Header.Set("X-Gitlab-Token", "")
				return req, body
			},
			wantErr:   true,
			errString: "signature header is empty",
		},
	}

	runVerifierTests(t, verifier, tests)
}

func TestGitLabVerifier_Type(t *testing.T) {
	v := NewGitLabVerifier("secret")
	assertVerifierType(t, v, "gitlab")
}
