package verifier

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestQueryParamVerifier_Verify(t *testing.T) {
	name := "token"
	token := "my-secret-token"
	verifier := NewQueryParamVerifier(name, token)

	tests := []verifierTestCase{
		{
			name: "valid token",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"event":"sync"}`)
				req := httptest.NewRequest(http.MethodPost, "/webhook?token=my-secret-token", strings.NewReader(string(body)))
				return req, body
			},
			wantErr: false,
		},
		{
			name: "valid token with other params",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"event":"sync"}`)
				req := httptest.NewRequest(http.MethodPost, "/webhook?foo=bar&token=my-secret-token&baz=qux", strings.NewReader(string(body)))
				return req, body
			},
			wantErr: false,
		},
		{
			name: "missing query parameter",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"event":"sync"}`)
				req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(body)))
				return req, body
			},
			wantErr:   true,
			errString: "signature header is empty",
		},
		{
			name: "wrong token",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"event":"sync"}`)
				req := httptest.NewRequest(http.MethodPost, "/webhook?token=wrong-token", strings.NewReader(string(body)))
				return req, body
			},
			wantErr:   true,
			errString: "token does not match",
		},
		{
			name: "empty token in query",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"event":"sync"}`)
				req := httptest.NewRequest(http.MethodPost, "/webhook?token=", strings.NewReader(string(body)))
				return req, body
			},
			wantErr:   true,
			errString: "signature header is empty",
		},
		{
			name: "different parameter name present",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"event":"sync"}`)
				req := httptest.NewRequest(http.MethodPost, "/webhook?secret=my-secret-token", strings.NewReader(string(body)))
				return req, body
			},
			wantErr:   true,
			errString: "signature header is empty",
		},
	}

	runVerifierTests(t, verifier, tests)
}

func TestQueryParamVerifier_Type(t *testing.T) {
	v := NewQueryParamVerifier("token", "secret")
	assertVerifierType(t, v, "query_param")
}
