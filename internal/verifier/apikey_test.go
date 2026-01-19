package verifier

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIKeyVerifier_Verify(t *testing.T) {
	header := "X-Goog-Channel-Token"
	token := "my-secret-channel-token"
	verifier := NewAPIKeyVerifier(header, token)

	tests := []verifierTestCase{
		{
			name: "valid token",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"resourceState":"sync"}`)
				req := httptest.NewRequest(http.MethodPost, "/calendar/notify", strings.NewReader(string(body)))
				req.Header.Set("X-Goog-Channel-Token", token)
				return req, body
			},
			wantErr: false,
		},
		{
			name: "missing header",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"resourceState":"sync"}`)
				req := httptest.NewRequest(http.MethodPost, "/calendar/notify", strings.NewReader(string(body)))
				return req, body
			},
			wantErr:   true,
			errString: "signature header is empty",
		},
		{
			name: "wrong token",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"resourceState":"sync"}`)
				req := httptest.NewRequest(http.MethodPost, "/calendar/notify", strings.NewReader(string(body)))
				req.Header.Set("X-Goog-Channel-Token", "wrong-token")
				return req, body
			},
			wantErr:   true,
			errString: "token does not match",
		},
		{
			name: "empty token in header",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"resourceState":"sync"}`)
				req := httptest.NewRequest(http.MethodPost, "/calendar/notify", strings.NewReader(string(body)))
				req.Header.Set("X-Goog-Channel-Token", "")
				return req, body
			},
			wantErr:   true,
			errString: "signature header is empty",
		},
	}

	runVerifierTests(t, verifier, tests)
}

func TestAPIKeyVerifier_Type(t *testing.T) {
	v := NewAPIKeyVerifier("X-API-Key", "secret")
	assertVerifierType(t, v, "api_key")
}
