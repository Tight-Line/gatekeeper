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

	tests := []struct {
		name      string
		setup     func() (*http.Request, []byte)
		wantErr   bool
		errString string
	}{
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

func TestAPIKeyVerifier_Type(t *testing.T) {
	v := NewAPIKeyVerifier("X-API-Key", "secret")
	if v.Type() != "api_key" {
		t.Errorf("expected type 'api_key', got %q", v.Type())
	}
}
