package verifier

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestShopifyVerifier_Verify(t *testing.T) {
	secret := "test-shopify-secret"
	verifier := NewShopifyVerifier(secret)

	tests := []struct {
		name      string
		setup     func() (*http.Request, []byte)
		wantErr   bool
		errString string
	}{
		{
			name: "valid signature",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"id":123,"topic":"orders/create"}`)
				signature := computeShopifySignature(secret, body)

				req := httptest.NewRequest(http.MethodPost, "/shopify/webhook", strings.NewReader(string(body)))
				req.Header.Set("X-Shopify-Hmac-SHA256", signature)
				return req, body
			},
			wantErr: false,
		},
		{
			name: "missing signature header",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"id":123}`)
				req := httptest.NewRequest(http.MethodPost, "/shopify/webhook", strings.NewReader(string(body)))
				return req, body
			},
			wantErr:   true,
			errString: "signature header is empty",
		},
		{
			name: "invalid base64",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"id":123}`)
				req := httptest.NewRequest(http.MethodPost, "/shopify/webhook", strings.NewReader(string(body)))
				req.Header.Set("X-Shopify-Hmac-SHA256", "not-valid-base64!!!")
				return req, body
			},
			wantErr:   true,
			errString: "cannot decode signature base64",
		},
		{
			name: "invalid signature",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"id":123}`)
				req := httptest.NewRequest(http.MethodPost, "/shopify/webhook", strings.NewReader(string(body)))
				req.Header.Set("X-Shopify-Hmac-SHA256", base64.StdEncoding.EncodeToString([]byte("wrong")))
				return req, body
			},
			wantErr:   true,
			errString: "signature does not match",
		},
		{
			name: "tampered body",
			setup: func() (*http.Request, []byte) {
				originalBody := []byte(`{"id":123}`)
				tamperedBody := []byte(`{"id":456}`)
				signature := computeShopifySignature(secret, originalBody)

				req := httptest.NewRequest(http.MethodPost, "/shopify/webhook", strings.NewReader(string(tamperedBody)))
				req.Header.Set("X-Shopify-Hmac-SHA256", signature)
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

func TestShopifyVerifier_Type(t *testing.T) {
	v := NewShopifyVerifier("secret")
	if v.Type() != "shopify" {
		t.Errorf("expected type 'shopify', got %q", v.Type())
	}
}

// computeShopifySignature computes a valid Shopify signature for testing
func computeShopifySignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
