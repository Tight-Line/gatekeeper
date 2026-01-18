package verifier

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHMACVerifier_Verify(t *testing.T) {
	secret := "test-hmac-secret"

	tests := []struct {
		name      string
		hash      string
		encoding  string
		setup     func(v *HMACVerifier) (*http.Request, []byte)
		wantErr   bool
		errString string
	}{
		{
			name:     "valid sha256 hex signature",
			hash:     "SHA256",
			encoding: "hex",
			setup: func(v *HMACVerifier) (*http.Request, []byte) {
				body := []byte(`{"test":"data"}`)
				mac := hmac.New(sha256.New, []byte(secret))
				mac.Write(body)
				signature := hex.EncodeToString(mac.Sum(nil))

				req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(body)))
				req.Header.Set("X-Signature", signature)
				return req, body
			},
			wantErr: false,
		},
		{
			name:     "valid sha256 base64 signature",
			hash:     "SHA256",
			encoding: "base64",
			setup: func(v *HMACVerifier) (*http.Request, []byte) {
				body := []byte(`{"test":"data"}`)
				mac := hmac.New(sha256.New, []byte(secret))
				mac.Write(body)
				signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))

				req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(body)))
				req.Header.Set("X-Signature", signature)
				return req, body
			},
			wantErr: false,
		},
		{
			name:     "valid sha512 hex signature",
			hash:     "SHA512",
			encoding: "hex",
			setup: func(v *HMACVerifier) (*http.Request, []byte) {
				body := []byte(`{"test":"data"}`)
				mac := hmac.New(sha512.New, []byte(secret))
				mac.Write(body)
				signature := hex.EncodeToString(mac.Sum(nil))

				req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(body)))
				req.Header.Set("X-Signature", signature)
				return req, body
			},
			wantErr: false,
		},
		{
			name:     "missing header",
			hash:     "SHA256",
			encoding: "hex",
			setup: func(v *HMACVerifier) (*http.Request, []byte) {
				body := []byte(`{"test":"data"}`)
				req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(body)))
				return req, body
			},
			wantErr:   true,
			errString: "signature header is empty",
		},
		{
			name:     "invalid hex",
			hash:     "SHA256",
			encoding: "hex",
			setup: func(v *HMACVerifier) (*http.Request, []byte) {
				body := []byte(`{"test":"data"}`)
				req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(body)))
				req.Header.Set("X-Signature", "not-valid-hex-zzz")
				return req, body
			},
			wantErr:   true,
			errString: "cannot decode signature hex",
		},
		{
			name:     "wrong signature",
			hash:     "SHA256",
			encoding: "hex",
			setup: func(v *HMACVerifier) (*http.Request, []byte) {
				body := []byte(`{"test":"data"}`)
				req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(body)))
				req.Header.Set("X-Signature", "abcd1234")
				return req, body
			},
			wantErr:   true,
			errString: "signature does not match",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verifier, err := NewHMACVerifier("X-Signature", secret, tt.hash, tt.encoding)
			if err != nil {
				t.Fatalf("failed to create verifier: %v", err)
			}

			req, body := tt.setup(verifier)
			err = verifier.Verify(req, body)

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

func TestNewHMACVerifier_InvalidConfig(t *testing.T) {
	_, err := NewHMACVerifier("X-Sig", "secret", "MD5", "hex")
	if err == nil {
		t.Error("expected error for unsupported hash algorithm")
	}

	_, err = NewHMACVerifier("X-Sig", "secret", "SHA256", "binary")
	if err == nil {
		t.Error("expected error for unsupported encoding")
	}
}

func TestHMACVerifier_Type(t *testing.T) {
	v, _ := NewHMACVerifier("X-Sig", "secret", "SHA256", "hex")
	if v.Type() != "hmac" {
		t.Errorf("expected type 'hmac', got %q", v.Type())
	}
}

func TestHMACVerifier_InvalidBase64(t *testing.T) {
	v, _ := NewHMACVerifier("X-Sig", "secret", "SHA256", "base64")
	body := []byte(`{"test":"data"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(body)))
	req.Header.Set("X-Sig", "not-valid-base64!!!")

	err := v.Verify(req, body)
	if err == nil || !strings.Contains(err.Error(), "cannot decode signature base64") {
		t.Errorf("expected base64 decode error, got %v", err)
	}
}
