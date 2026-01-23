package verifier

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHeaderQueryParamVerifier_Verify(t *testing.T) {
	header := "X-Goog-Channel-Token"
	name := "secret"
	token := "my-secret-token"
	verifier := NewHeaderQueryParamVerifier(header, name, token)

	tests := []verifierTestCase{
		{
			name: "valid token in query string header",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"resourceState":"sync"}`)
				req := httptest.NewRequest(http.MethodPost, "/calendar/notify", strings.NewReader(string(body)))
				req.Header.Set("X-Goog-Channel-Token", "secret=my-secret-token")
				return req, body
			},
			wantErr: false,
		},
		{
			name: "valid token with multiple params",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"resourceState":"sync"}`)
				req := httptest.NewRequest(http.MethodPost, "/calendar/notify", strings.NewReader(string(body)))
				req.Header.Set("X-Goog-Channel-Token", "foo=bar&secret=my-secret-token&baz=qux")
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
			name: "empty header",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"resourceState":"sync"}`)
				req := httptest.NewRequest(http.MethodPost, "/calendar/notify", strings.NewReader(string(body)))
				req.Header.Set("X-Goog-Channel-Token", "")
				return req, body
			},
			wantErr:   true,
			errString: "signature header is empty",
		},
		{
			name: "parameter missing from header",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"resourceState":"sync"}`)
				req := httptest.NewRequest(http.MethodPost, "/calendar/notify", strings.NewReader(string(body)))
				req.Header.Set("X-Goog-Channel-Token", "foo=bar&other=value")
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
				req.Header.Set("X-Goog-Channel-Token", "secret=wrong-token")
				return req, body
			},
			wantErr:   true,
			errString: "token does not match",
		},
		{
			name: "empty parameter value",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"resourceState":"sync"}`)
				req := httptest.NewRequest(http.MethodPost, "/calendar/notify", strings.NewReader(string(body)))
				req.Header.Set("X-Goog-Channel-Token", "secret=")
				return req, body
			},
			wantErr:   true,
			errString: "signature header is empty",
		},
		{
			name: "invalid query string format",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"resourceState":"sync"}`)
				req := httptest.NewRequest(http.MethodPost, "/calendar/notify", strings.NewReader(string(body)))
				req.Header.Set("X-Goog-Channel-Token", "not-a-query-string%")
				return req, body
			},
			wantErr:   true,
			errString: "signature does not match",
		},
		{
			name: "plain value without equals",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"resourceState":"sync"}`)
				req := httptest.NewRequest(http.MethodPost, "/calendar/notify", strings.NewReader(string(body)))
				// Plain value parses as key with empty value, so "secret" param would not match
				req.Header.Set("X-Goog-Channel-Token", "my-secret-token")
				return req, body
			},
			wantErr:   true,
			errString: "signature header is empty",
		},
	}

	runVerifierTests(t, verifier, tests)
}

func TestHeaderQueryParamVerifier_Type(t *testing.T) {
	v := NewHeaderQueryParamVerifier("X-Goog-Channel-Token", "secret", "token123")
	assertVerifierType(t, v, "header_query_param")
}
