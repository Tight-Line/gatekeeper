package verifier

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"hash"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// computeHMACSignature computes an HMAC signature for testing
func computeHMACSignature(secret string, body []byte, hashName, encoding string) string {
	var h func() hash.Hash
	if hashName == "SHA512" {
		h = sha512.New
	} else {
		h = sha256.New
	}

	mac := hmac.New(h, []byte(secret))
	mac.Write(body)

	if encoding == "base64" {
		return base64.StdEncoding.EncodeToString(mac.Sum(nil))
	}
	return hex.EncodeToString(mac.Sum(nil))
}

// makeHMACRequest creates a test request with an optional signature header
func makeHMACRequest(body []byte, signature string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(body)))
	if signature != "" {
		req.Header.Set("X-Signature", signature)
	}
	return req
}

func TestHMACVerifier_Verify(t *testing.T) {
	secret := "test-hmac-secret"
	body := []byte(`{"test":"data"}`)

	testCases := []struct {
		name      string
		hash      string
		encoding  string
		signature string // empty means compute valid signature; "-" means no header
		wantErr   bool
		errString string
	}{
		{"valid sha256 hex", "SHA256", "hex", "", false, ""},
		{"valid sha256 base64", "SHA256", "base64", "", false, ""},
		{"valid sha512 hex", "SHA512", "hex", "", false, ""},
		{"missing header", "SHA256", "hex", "-", true, "signature header is empty"},
		{"invalid hex", "SHA256", "hex", "not-valid-hex-zzz", true, "cannot decode signature hex"},
		{"wrong signature", "SHA256", "hex", "abcd1234", true, "signature does not match"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			verifier, err := NewHMACVerifier("X-Signature", secret, tc.hash, tc.encoding)
			if err != nil {
				t.Fatalf("failed to create verifier: %v", err)
			}

			var sig string
			switch tc.signature {
			case "":
				sig = computeHMACSignature(secret, body, tc.hash, tc.encoding)
			case "-":
				sig = ""
			default:
				sig = tc.signature
			}

			req := makeHMACRequest(body, sig)
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
