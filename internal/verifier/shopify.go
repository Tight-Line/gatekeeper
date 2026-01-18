package verifier

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
)

const (
	shopifySignatureHeader = "X-Shopify-Hmac-SHA256"
)

// ShopifyVerifier verifies Shopify webhook signatures
// See: https://shopify.dev/docs/apps/webhooks/configuration/https#step-5-verify-the-webhook
type ShopifyVerifier struct {
	secret string
}

// NewShopifyVerifier creates a new Shopify verifier
func NewShopifyVerifier(secret string) *ShopifyVerifier {
	return &ShopifyVerifier{
		secret: secret,
	}
}

// Verify checks the Shopify signature
func (v *ShopifyVerifier) Verify(r *http.Request, payload []byte) error {
	signature := r.Header.Get(shopifySignatureHeader)
	if signature == "" {
		return fmt.Errorf("%w: %s header missing", ErrSignatureEmpty, shopifySignatureHeader)
	}

	// Decode the base64 signature
	sigBytes, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return fmt.Errorf("%w: cannot decode signature base64", ErrSignatureMismatch)
	}

	// Compute expected signature
	mac := hmac.New(sha256.New, []byte(v.secret))
	mac.Write(payload)
	expectedSig := mac.Sum(nil)

	// Constant-time comparison
	if !hmac.Equal(sigBytes, expectedSig) {
		return ErrSignatureMismatch
	}

	return nil
}

// Type returns the verifier type
func (v *ShopifyVerifier) Type() string {
	return "shopify"
}
