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

	testCases := []struct {
		name      string
		hash      string
		encoding  string
		setup     func() (*http.Request, []byte)
		wantErr   bool
		errString string
	}{
		{
			name:     "valid sha256 hex signature",
			hash:     "SHA256",
			encoding: "hex",
			setup: func() (*http.Request, []byte) {
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
			setup: func() (*http.Request, []byte) {
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
			setup: func() (*http.Request, []byte) {
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
			setup: func() (*http.Request, []byte) {
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
			setup: func() (*http.Request, []byte) {
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
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"test":"data"}`)
				req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(body)))
				req.Header.Set("X-Signature", "abcd1234")
				return req, body
			},
			wantErr:   true,
			errString: "signature does not match",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			verifier, err := NewHMACVerifier("X-Signature", secret, tc.hash, tc.encoding)
			if err != nil {
				t.Fatalf("failed to create verifier: %v", err)
			}

			req, body := tc.setup()
			err = verifier.Verify(req, body)
			assertVerifyResult(t, err, tc.wantErr, tc.errString)
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
	assertVerifierType(t, v, "hmac")
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
