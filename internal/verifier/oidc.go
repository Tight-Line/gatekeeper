package verifier

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	oidcKeyCacheTTL     = time.Hour
	oidcDiscoverySuffix = "/.well-known/openid-configuration"
)

// OIDCVerifier verifies JWT bearer tokens from an OIDC provider.
// It supports RS256 signed tokens with automatic JWKS key caching.
type OIDCVerifier struct {
	issuer         string
	audience       string
	jwksURI        string
	requiredClaims map[string]string
	httpClient     *http.Client
	mu             sync.RWMutex
	keys           map[string]*rsa.PublicKey
	keysExpiry     time.Time
}

// NewOIDCVerifier creates a new OIDCVerifier.
// If jwksURI is empty, it will be auto-discovered from the issuer's
// /.well-known/openid-configuration endpoint.
func NewOIDCVerifier(issuer, audience, jwksURI string, requiredClaims map[string]string) *OIDCVerifier {
	return &OIDCVerifier{
		issuer:         issuer,
		audience:       audience,
		jwksURI:        jwksURI,
		requiredClaims: requiredClaims,
		httpClient:     &http.Client{Timeout: 10 * time.Second},
		keys:           make(map[string]*rsa.PublicKey),
	}
}

// Verify implements the Verifier interface. It extracts and validates the JWT
// bearer token from the Authorization header.
func (v *OIDCVerifier) Verify(r *http.Request, _ []byte) error {
	token, err := extractBearerToken(r)
	if err != nil {
		return err
	}
	return v.verifyToken(token)
}

// Type returns the verifier type name.
func (v *OIDCVerifier) Type() string { return "oidc" }

// verifyToken parses and validates a JWT token string.
func (v *OIDCVerifier) verifyToken(token string) error {
	header, payload, signingInput, sig, err := parseJWT(token)
	if err != nil {
		return err
	}

	// Only RS256 is supported
	alg, _ := header["alg"].(string)
	if alg != "RS256" {
		return fmt.Errorf("unsupported algorithm: %s", alg)
	}

	kid, _ := header["kid"].(string)

	pub, err := v.getKey(kid)
	if err != nil {
		return err
	}

	if err := verifyRSASHA256(pub, signingInput, sig); err != nil {
		return ErrSignatureMismatch
	}

	return v.validateClaims(payload)
}

// validateClaims checks the standard JWT claims and any required custom claims.
func (v *OIDCVerifier) validateClaims(payload map[string]interface{}) error {
	// Check expiry
	if exp, ok := payload["exp"]; ok {
		if expVal, ok := exp.(float64); ok {
			if time.Now().Unix() > int64(expVal) {
				return ErrTokenExpired
			}
		}
	}

	// Check issuer
	iss, _ := payload["iss"].(string)
	if iss != v.issuer {
		return fmt.Errorf("issuer mismatch: expected %q, got %q", v.issuer, iss)
	}

	// Check audience
	if !audienceMatches(payload["aud"], v.audience) {
		return fmt.Errorf("audience mismatch: %q not in token audiences", v.audience)
	}

	// Check required claims
	for k, want := range v.requiredClaims {
		got, _ := payload[k].(string)
		if got != want {
			return ErrClaimMismatch
		}
	}

	return nil
}

// audienceMatches checks whether the expected audience appears in the token's aud claim.
// Per RFC 7519, aud can be a string or an array of strings.
func audienceMatches(aud interface{}, expected string) bool {
	switch v := aud.(type) {
	case string:
		return v == expected
	case []interface{}:
		for _, a := range v {
			if s, ok := a.(string); ok && s == expected {
				return true
			}
		}
	}
	return false
}

// getKey retrieves the RSA public key for the given kid, refreshing if necessary.
func (v *OIDCVerifier) getKey(kid string) (*rsa.PublicKey, error) {
	// Fast path: key exists and cache is valid
	v.mu.RLock()
	if time.Now().Before(v.keysExpiry) {
		if pub, ok := v.keys[kid]; ok {
			v.mu.RUnlock()
			return pub, nil
		}
	}
	v.mu.RUnlock()

	// Refresh and retry
	if err := v.refreshKeys(); err != nil {
		return nil, err
	}

	v.mu.RLock()
	defer v.mu.RUnlock()
	pub, ok := v.keys[kid]
	if !ok {
		return nil, fmt.Errorf("unknown key ID: %q", kid)
	}
	return pub, nil
}

// refreshKeys fetches and caches JWKS keys under a write lock.
// Uses a double-check pattern to avoid redundant HTTP calls from concurrent goroutines.
func (v *OIDCVerifier) refreshKeys() error {
	v.mu.Lock()
	defer v.mu.Unlock()

	// Double-check: another goroutine may have refreshed while we waited for the lock
	if time.Now().Before(v.keysExpiry) {
		return nil
	}

	jwksURI, err := v.resolveJWKSURI()
	if err != nil {
		return err
	}

	keys, err := fetchKeys(v.httpClient, jwksURI)
	if err != nil {
		return err
	}

	v.keys = keys
	v.keysExpiry = time.Now().Add(oidcKeyCacheTTL)
	return nil
}

// resolveJWKSURI returns the configured JWKS URI or discovers it from the issuer.
func (v *OIDCVerifier) resolveJWKSURI() (string, error) {
	if v.jwksURI != "" {
		return v.jwksURI, nil
	}

	// OIDC discovery
	discoveryURL := strings.TrimRight(v.issuer, "/") + oidcDiscoverySuffix
	resp, err := v.httpClient.Get(discoveryURL)
	if err != nil {
		return "", fmt.Errorf("oidc discovery request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oidc discovery returned status %d", resp.StatusCode)
	}

	var doc struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return "", fmt.Errorf("oidc discovery: invalid JSON: %w", err)
	}
	if doc.JWKSURI == "" {
		return "", errors.New("oidc discovery: missing jwks_uri")
	}
	return doc.JWKSURI, nil
}

// fetchKeys fetches and parses JWKS keys from the given URI.
// Supports both standard JWK Set format and Google X.509 certificate map format.
func fetchKeys(client *http.Client, jwksURI string) (map[string]*rsa.PublicKey, error) {
	resp, err := client.Get(jwksURI)
	if err != nil {
		return nil, fmt.Errorf("fetching JWKS: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("JWKS endpoint returned status %d", resp.StatusCode)
	}

	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("JWKS: invalid JSON: %w", err)
	}

	// Try standard JWK Set format first: {"keys": [...]}
	var jwks struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.Unmarshal(raw, &jwks); err == nil && len(jwks.Keys) > 0 {
		return keysFromJWKS(jwks.Keys)
	}

	// Try Google X.509 cert map format: {"kid": "-----BEGIN CERTIFICATE-----..."}
	var certMap map[string]string
	if err := json.Unmarshal(raw, &certMap); err == nil && len(certMap) > 0 {
		// Check if values look like PEM certificates
		for _, v := range certMap {
			if strings.HasPrefix(v, "-----BEGIN ") {
				return keysFromCertMap(certMap)
			}
			break
		}
	}

	return nil, errors.New("unrecognized JWKS format")
}

// keysFromJWKS parses RSA public keys from a JWK Set key array.
// Non-RSA keys are silently skipped.
func keysFromJWKS(keys []json.RawMessage) (map[string]*rsa.PublicKey, error) {
	result := make(map[string]*rsa.PublicKey)
	for _, raw := range keys {
		var jwk struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		}
		if err := json.Unmarshal(raw, &jwk); err != nil {
			continue
		}
		if jwk.Kty != "RSA" {
			continue
		}
		pub, err := rsaPublicKeyFromJWK(jwk.N, jwk.E)
		if err != nil {
			return nil, fmt.Errorf("parsing JWK key %q: %w", jwk.Kid, err)
		}
		result[jwk.Kid] = pub
	}
	return result, nil
}

// keysFromCertMap parses RSA public keys from a Google-style X.509 certificate map.
func keysFromCertMap(certMap map[string]string) (map[string]*rsa.PublicKey, error) {
	result := make(map[string]*rsa.PublicKey)
	for kid, pemStr := range certMap {
		block, _ := pem.Decode([]byte(pemStr))
		if block == nil {
			return nil, fmt.Errorf("key %q: invalid PEM block", kid)
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("key %q: parsing certificate: %w", kid, err)
		}
		pub, ok := cert.PublicKey.(*rsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("key %q: certificate public key is not RSA", kid)
		}
		result[kid] = pub
	}
	return result, nil
}

// rsaPublicKeyFromJWK constructs an RSA public key from base64url-encoded n and e values.
func rsaPublicKeyFromJWK(nB64, eB64 string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nB64)
	if err != nil {
		return nil, fmt.Errorf("invalid modulus: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eB64)
	if err != nil {
		return nil, fmt.Errorf("invalid exponent: %w", err)
	}

	n := new(big.Int).SetBytes(nBytes)
	e := new(big.Int).SetBytes(eBytes)

	return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
}

// parseJWT splits a compact JWT into its components and decodes each part.
func parseJWT(token string) (header, payload map[string]interface{}, signingInput string, sig []byte, err error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, nil, "", nil, fmt.Errorf("%w: expected 3 parts, got %d", ErrTokenInvalid, len(parts))
	}

	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, nil, "", nil, fmt.Errorf("%w: invalid header encoding", ErrTokenInvalid)
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, nil, "", nil, fmt.Errorf("%w: invalid payload encoding", ErrTokenInvalid)
	}

	sig, err = base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, nil, "", nil, fmt.Errorf("%w: invalid signature encoding", ErrTokenInvalid)
	}

	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, nil, "", nil, fmt.Errorf("%w: invalid header JSON", ErrTokenInvalid)
	}

	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return nil, nil, "", nil, fmt.Errorf("%w: invalid payload JSON", ErrTokenInvalid)
	}

	signingInput = parts[0] + "." + parts[1]
	return header, payload, signingInput, sig, nil
}

// verifyRSASHA256 verifies an RS256 JWT signature.
func verifyRSASHA256(pub *rsa.PublicKey, signingInput string, sig []byte) error {
	h := sha256.Sum256([]byte(signingInput))
	return rsa.VerifyPKCS1v15(pub, crypto.SHA256, h[:], sig) // NOSONAR - RS256 mandates PKCS#1 v1.5; PSS is a different algorithm (PS256)
}

// extractBearerToken extracts the bearer token from the Authorization header.
func extractBearerToken(r *http.Request) (string, error) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return "", ErrTokenMissing
	}
	if !strings.HasPrefix(auth, "Bearer ") {
		return "", ErrTokenMissing
	}
	token := strings.TrimPrefix(auth, "Bearer ")
	if token == "" {
		return "", ErrTokenMissing
	}
	return token, nil
}
