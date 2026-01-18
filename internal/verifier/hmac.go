package verifier

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash"
	"net/http"
)

// HMACVerifier verifies webhook signatures using configurable HMAC
type HMACVerifier struct {
	header   string
	secret   string
	hashFunc func() hash.Hash
	encoding string // "hex" or "base64"
}

// NewHMACVerifier creates a new generic HMAC verifier
func NewHMACVerifier(header, secret, hashAlgo, encoding string) (*HMACVerifier, error) {
	var hashFunc func() hash.Hash
	switch hashAlgo {
	case "SHA256", "sha256":
		hashFunc = sha256.New
	case "SHA512", "sha512":
		hashFunc = sha512.New
	default:
		return nil, fmt.Errorf("unsupported hash algorithm: %s", hashAlgo)
	}

	if encoding != "hex" && encoding != "base64" {
		return nil, fmt.Errorf("unsupported encoding: %s (must be hex or base64)", encoding)
	}

	return &HMACVerifier{
		header:   header,
		secret:   secret,
		hashFunc: hashFunc,
		encoding: encoding,
	}, nil
}

// Verify checks the HMAC signature
func (v *HMACVerifier) Verify(r *http.Request, payload []byte) error {
	signature := r.Header.Get(v.header)
	if signature == "" {
		return fmt.Errorf("%w: %s header missing", ErrSignatureEmpty, v.header)
	}

	// Decode the signature based on encoding
	var sigBytes []byte
	var err error
	switch v.encoding {
	case "hex":
		sigBytes, err = hex.DecodeString(signature)
		if err != nil {
			return fmt.Errorf("%w: cannot decode signature hex", ErrSignatureMismatch)
		}
	case "base64":
		sigBytes, err = base64.StdEncoding.DecodeString(signature)
		if err != nil {
			return fmt.Errorf("%w: cannot decode signature base64", ErrSignatureMismatch)
		}
	}

	// Compute expected signature
	mac := hmac.New(v.hashFunc, []byte(v.secret))
	mac.Write(payload)
	expectedSig := mac.Sum(nil)

	// Constant-time comparison
	if !hmac.Equal(sigBytes, expectedSig) {
		return ErrSignatureMismatch
	}

	return nil
}

// Type returns the verifier type
func (v *HMACVerifier) Type() string {
	return "hmac"
}
