package verifier

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Test string constants to avoid duplication (SonarCloud S1192).
const (
	testContentTypeJSON   = "application/json"
	testHeaderContentType = "Content-Type"
	errFmtTokenMissing    = "expected ErrTokenMissing, got %v"
	errFmtUnexpected      = "unexpected error: %v"
	errFmtTokenInvalid    = "expected ErrTokenInvalid, got %v"
	testEmailClaim        = "expected@example.com"
	msgExpectedKid1       = "expected key kid1 in result"
	testUnreachableServer = "http://127.0.0.1:1"
	errExpectedError      = "expected error, got nil"
	testIssuer            = "https://issuer.example.com"
	testUnreachableJWKS   = "http://127.0.0.1:1/jwks"
	errMsgUnrecognizedFmt = "unrecognized format"
	errMsgInvalidPEM      = "invalid PEM"
)

// requireErrIs is a test helper that asserts err matches target.
func requireErrIs(t *testing.T, err, target error, fmtStr string) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Errorf(fmtStr, err)
	}
}

// requireNoErr is a test helper that fails if err is non-nil.
func requireNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Errorf(errFmtUnexpected, err)
	}
}

// requireError is a test helper that fails if err is nil.
func requireError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Error(errExpectedError)
	}
}

// requireErrorContains is a test helper that asserts err is non-nil and contains substr.
func requireErrorContains(t *testing.T, err error, substr, context string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), substr) {
		t.Errorf("expected %s error, got %v", context, err)
	}
}

// makeTestJWT builds a signed JWT for testing.
func makeTestJWT(key *rsa.PrivateKey, kid string, claims map[string]interface{}) string {
	header := map[string]interface{}{
		"alg": "RS256",
		"kid": kid,
		"typ": "JWT",
	}
	headerJSON, _ := json.Marshal(header)
	payloadJSON, _ := json.Marshal(claims)

	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signingInput := headerB64 + "." + payloadB64

	h := sha256.Sum256([]byte(signingInput))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h[:]) // NOSONAR - RS256 mandates PKCS1v15; PSS is a different algorithm (PS256)
	sigB64 := base64.RawURLEncoding.EncodeToString(sig)

	return signingInput + "." + sigB64
}

// makeJWKSHandler returns an httptest handler that serves a standard JWK Set.
func makeJWKSHandler(kid string, pub *rsa.PublicKey) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nB64 := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
		eBytes := big.NewInt(int64(pub.E)).Bytes()
		eB64 := base64.RawURLEncoding.EncodeToString(eBytes)

		body, _ := json.Marshal(map[string]interface{}{
			"keys": []map[string]interface{}{
				{
					"kty": "RSA",
					"kid": kid,
					"n":   nB64,
					"e":   eB64,
				},
			},
		})
		w.Header().Set(testHeaderContentType, testContentTypeJSON)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}
}

// makeSelfSignedCert creates a self-signed RSA certificate and returns the PEM block.
func makeSelfSignedCert(t *testing.T, key *rsa.PrivateKey) string {
	t.Helper()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating test certificate: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}))
}

// makeJSONHandler returns a handler that serves the given JSON body.
func makeJSONHandler(body []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(testHeaderContentType, testContentTypeJSON)
		_, _ = w.Write(body)
	}
}

func TestExtractBearerToken(t *testing.T) {
	t.Run("missing Authorization header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
		_, err := extractBearerToken(req)
		requireErrIs(t, err, ErrTokenMissing, errFmtTokenMissing)
	})

	t.Run("no Bearer prefix", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
		req.Header.Set("Authorization", "Basic abc123")
		_, err := extractBearerToken(req)
		requireErrIs(t, err, ErrTokenMissing, errFmtTokenMissing)
	})

	t.Run("Bearer with empty token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
		req.Header.Set("Authorization", "Bearer ")
		_, err := extractBearerToken(req)
		requireErrIs(t, err, ErrTokenMissing, errFmtTokenMissing)
	})

	t.Run("valid Bearer token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
		req.Header.Set("Authorization", "Bearer mytoken123")
		tok, err := extractBearerToken(req)
		requireNoErr(t, err)
		if tok != "mytoken123" {
			t.Errorf("expected %q, got %q", "mytoken123", tok)
		}
	})
}

// parseJWTExpectInvalid is a helper that calls parseJWT and asserts ErrTokenInvalid.
func parseJWTExpectInvalid(t *testing.T, token string) {
	t.Helper()
	_, _, _, _, err := parseJWT(token)
	requireErrIs(t, err, ErrTokenInvalid, errFmtTokenInvalid)
}

func TestParseJWT(t *testing.T) {
	validHeader := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`))
	validPayload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"1234"}`))
	validSig := base64.RawURLEncoding.EncodeToString([]byte("fakesig"))

	t.Run("wrong number of parts", func(t *testing.T) {
		parseJWTExpectInvalid(t, "only.two")
	})

	t.Run("invalid base64 in header", func(t *testing.T) {
		parseJWTExpectInvalid(t, "!!!.payload.sig")
	})

	t.Run("invalid base64 in payload", func(t *testing.T) {
		parseJWTExpectInvalid(t, validHeader+".!!!.sig")
	})

	t.Run("invalid base64 in signature", func(t *testing.T) {
		parseJWTExpectInvalid(t, validHeader+"."+validPayload+".!!!")
	})

	t.Run("invalid JSON in header", func(t *testing.T) {
		badHeader := base64.RawURLEncoding.EncodeToString([]byte(`not-json`))
		parseJWTExpectInvalid(t, badHeader+"."+validPayload+"."+validSig)
	})

	t.Run("invalid JSON in payload", func(t *testing.T) {
		badPayload := base64.RawURLEncoding.EncodeToString([]byte(`not-json`))
		parseJWTExpectInvalid(t, validHeader+"."+badPayload+"."+validSig)
	})

	t.Run("valid JWT", func(t *testing.T) {
		header, payload, signingInput, sig, err := parseJWT(validHeader + "." + validPayload + "." + validSig)
		if err != nil {
			t.Fatalf(errFmtUnexpected, err)
		}
		if header["alg"] != "RS256" {
			t.Errorf("unexpected header alg: %v", header["alg"])
		}
		if payload["sub"] != "1234" {
			t.Errorf("unexpected payload sub: %v", payload["sub"])
		}
		if signingInput != validHeader+"."+validPayload {
			t.Errorf("unexpected signingInput: %s", signingInput)
		}
		if string(sig) != "fakesig" {
			t.Errorf("unexpected sig: %v", sig)
		}
	})
}

func TestOIDCVerifier_Type(t *testing.T) {
	v := NewOIDCVerifier("https://example.com", "myapp", "", nil)
	if v.Type() != "oidc" {
		t.Errorf("expected %q, got %q", "oidc", v.Type())
	}
}

// oidcVerifyFixture holds common test fixtures for TestOIDCVerifier_Verify subtests.
type oidcVerifyFixture struct {
	key         *rsa.PrivateKey
	kid         string
	issuer      string
	audience    string
	validClaims func() map[string]interface{}
	newVerifier func(requiredClaims map[string]string) *OIDCVerifier
	makeRequest func(token string) *http.Request
}

func newOIDCVerifyFixture(t *testing.T) *oidcVerifyFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}

	const (
		issuer   = "https://auth.example.com"
		audience = "myapp"
		kid      = "key1"
	)

	jwksServer := httptest.NewServer(makeJWKSHandler(kid, &key.PublicKey))
	t.Cleanup(jwksServer.Close)

	f := &oidcVerifyFixture{
		key:      key,
		kid:      kid,
		issuer:   issuer,
		audience: audience,
	}
	f.validClaims = func() map[string]interface{} {
		return map[string]interface{}{
			"iss": issuer,
			"aud": audience,
			"exp": float64(time.Now().Add(time.Hour).Unix()),
			"sub": "user123",
		}
	}
	f.newVerifier = func(requiredClaims map[string]string) *OIDCVerifier {
		return NewOIDCVerifier(issuer, audience, jwksServer.URL, requiredClaims)
	}
	f.makeRequest = func(token string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		return req
	}
	return f
}

func testVerifyValidToken(t *testing.T, f *oidcVerifyFixture) {
	t.Helper()
	v := f.newVerifier(nil)
	token := makeTestJWT(f.key, f.kid, f.validClaims())
	requireNoErr(t, v.Verify(f.makeRequest(token), nil))
}

func testVerifyErrorCases(t *testing.T, f *oidcVerifyFixture) {
	t.Helper()

	t.Run("missing Authorization header", func(t *testing.T) {
		v := f.newVerifier(nil)
		requireErrIs(t, v.Verify(f.makeRequest(""), nil), ErrTokenMissing, errFmtTokenMissing)
	})

	t.Run("invalid JWT format", func(t *testing.T) {
		v := f.newVerifier(nil)
		requireErrIs(t, v.Verify(f.makeRequest("notavalidjwt"), nil), ErrTokenInvalid, errFmtTokenInvalid)
	})

	t.Run("non-RS256 algorithm", func(t *testing.T) {
		v := f.newVerifier(nil)
		headerJSON, _ := json.Marshal(map[string]interface{}{"alg": "HS256", "kid": f.kid})
		payloadJSON, _ := json.Marshal(f.validClaims())
		h64 := base64.RawURLEncoding.EncodeToString(headerJSON)
		p64 := base64.RawURLEncoding.EncodeToString(payloadJSON)
		token := h64 + "." + p64 + ".fakesig"
		requireErrorContains(t, v.Verify(f.makeRequest(token), nil), "unsupported algorithm", "unsupported algorithm")
	})

	t.Run("expired token", func(t *testing.T) {
		v := f.newVerifier(nil)
		claims := f.validClaims()
		claims["exp"] = float64(time.Now().Add(-time.Hour).Unix())
		token := makeTestJWT(f.key, f.kid, claims)
		requireErrIs(t, v.Verify(f.makeRequest(token), nil), ErrTokenExpired, "expected ErrTokenExpired, got %v")
	})

	t.Run("wrong issuer", func(t *testing.T) {
		v := f.newVerifier(nil)
		claims := f.validClaims()
		claims["iss"] = "https://wrong.example.com"
		token := makeTestJWT(f.key, f.kid, claims)
		requireErrorContains(t, v.Verify(f.makeRequest(token), nil), "issuer", "issuer")
	})
}

func testVerifyAudienceCases(t *testing.T, f *oidcVerifyFixture) {
	t.Helper()

	t.Run("wrong audience string", func(t *testing.T) {
		v := f.newVerifier(nil)
		claims := f.validClaims()
		claims["aud"] = "wrongapp"
		token := makeTestJWT(f.key, f.kid, claims)
		requireErrorContains(t, v.Verify(f.makeRequest(token), nil), "audience", "audience")
	})

	t.Run("wrong audience array", func(t *testing.T) {
		v := f.newVerifier(nil)
		claims := f.validClaims()
		claims["aud"] = []interface{}{"wrongapp", "otherapp"}
		token := makeTestJWT(f.key, f.kid, claims)
		requireErrorContains(t, v.Verify(f.makeRequest(token), nil), "audience", "audience")
	})

	t.Run("correct audience as array", func(t *testing.T) {
		v := f.newVerifier(nil)
		claims := f.validClaims()
		claims["aud"] = []interface{}{f.audience, "other"}
		token := makeTestJWT(f.key, f.kid, claims)
		requireNoErr(t, v.Verify(f.makeRequest(token), nil))
	})
}

func testVerifySignatureAndKeyCases(t *testing.T, f *oidcVerifyFixture) {
	t.Helper()

	t.Run("bad signature", func(t *testing.T) {
		v := f.newVerifier(nil)
		token := makeTestJWT(f.key, f.kid, f.validClaims())
		parts := strings.Split(token, ".")
		parts[2] = base64.RawURLEncoding.EncodeToString([]byte("badsignature"))
		tampered := strings.Join(parts, ".")
		requireErrIs(t, v.Verify(f.makeRequest(tampered), nil), ErrSignatureMismatch, "expected ErrSignatureMismatch, got %v")
	})

	t.Run("unknown kid not in JWKS", func(t *testing.T) {
		v := f.newVerifier(nil)
		token := makeTestJWT(f.key, "unknownkid", f.validClaims())
		requireErrorContains(t, v.Verify(f.makeRequest(token), nil), "unknown key ID", "unknown key ID")
	})

	t.Run("unknown kid triggers refresh and succeeds", func(t *testing.T) {
		v := f.newVerifier(nil)
		token := makeTestJWT(f.key, f.kid, f.validClaims())
		requireNoErr(t, v.Verify(f.makeRequest(token), nil))
	})
}

func testVerifyClaimCases(t *testing.T, f *oidcVerifyFixture) {
	t.Helper()

	t.Run("required claim mismatch", func(t *testing.T) {
		v := f.newVerifier(map[string]string{"email": testEmailClaim})
		claims := f.validClaims()
		claims["email"] = "other@example.com"
		token := makeTestJWT(f.key, f.kid, claims)
		requireErrIs(t, v.Verify(f.makeRequest(token), nil), ErrClaimMismatch, "expected ErrClaimMismatch, got %v")
	})

	t.Run("required claim matches", func(t *testing.T) {
		v := f.newVerifier(map[string]string{"email": testEmailClaim})
		claims := f.validClaims()
		claims["email"] = testEmailClaim
		token := makeTestJWT(f.key, f.kid, claims)
		requireNoErr(t, v.Verify(f.makeRequest(token), nil))
	})
}

func TestOIDCVerifier_Verify(t *testing.T) {
	f := newOIDCVerifyFixture(t)

	t.Run("valid token", func(t *testing.T) {
		testVerifyValidToken(t, f)
	})

	testVerifyErrorCases(t, f)
	testVerifyAudienceCases(t, f)
	testVerifySignatureAndKeyCases(t, f)
	testVerifyClaimCases(t, f)
}

func TestFetchKeys_JWKSFormat(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	server := httptest.NewServer(makeJWKSHandler("kid1", &key.PublicKey))
	defer server.Close()

	client := &http.Client{}
	keys, err := fetchKeys(client, server.URL)
	if err != nil {
		t.Fatalf(errFmtUnexpected, err)
	}
	if _, ok := keys["kid1"]; !ok {
		t.Error(msgExpectedKid1)
	}
}

func TestFetchKeys_CertMapFormat(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	certPEM := makeSelfSignedCert(t, key)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := json.Marshal(map[string]string{"kid1": certPEM})
		w.Header().Set(testHeaderContentType, testContentTypeJSON)
		_, _ = w.Write(body)
	}))
	defer server.Close()

	client := &http.Client{}
	keys, err := fetchKeys(client, server.URL)
	if err != nil {
		t.Fatalf(errFmtUnexpected, err)
	}
	if _, ok := keys["kid1"]; !ok {
		t.Error(msgExpectedKid1)
	}
}

func TestFetchKeys_Errors(t *testing.T) {
	t.Run("HTTP request error", func(t *testing.T) {
		client := &http.Client{}
		_, err := fetchKeys(client, testUnreachableServer) // nothing listening
		requireError(t, err)
	})

	t.Run("non-200 response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()
		client := &http.Client{}
		_, err := fetchKeys(client, server.URL)
		requireError(t, err)
	})

	t.Run("invalid JSON", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("not json at all"))
		}))
		defer server.Close()
		client := &http.Client{}
		_, err := fetchKeys(client, server.URL)
		requireError(t, err)
	})

	t.Run(errMsgUnrecognizedFmt, func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"foo": 123}`))
		}))
		defer server.Close()
		client := &http.Client{}
		_, err := fetchKeys(client, server.URL)
		requireErrorContains(t, err, "unrecognized", errMsgUnrecognizedFmt)
	})
}

func TestKeysFromJWKS(t *testing.T) {
	t.Run("non-RSA key type skipped", func(t *testing.T) {
		raw := []json.RawMessage{
			json.RawMessage(`{"kty":"EC","kid":"ec1","crv":"P-256"}`),
		}
		keys, err := keysFromJWKS(raw)
		if err != nil {
			t.Fatalf(errFmtUnexpected, err)
		}
		if len(keys) != 0 {
			t.Errorf("expected empty result, got %d keys", len(keys))
		}
	})

	t.Run("invalid modulus", func(t *testing.T) {
		raw := []json.RawMessage{
			json.RawMessage(`{"kty":"RSA","kid":"k1","n":"!!!invalid!!!","e":"AQAB"}`),
		}
		_, err := keysFromJWKS(raw)
		requireError(t, err)
	})

	t.Run("invalid exponent", func(t *testing.T) {
		key, _ := rsa.GenerateKey(rand.Reader, 2048)
		nB64 := base64.RawURLEncoding.EncodeToString(key.N.Bytes())
		raw := []json.RawMessage{
			[]byte(`{"kty":"RSA","kid":"k1","n":"` + nB64 + `","e":"!!!invalid!!!"}`),
		}
		_, err := keysFromJWKS(raw)
		requireError(t, err)
	})

	t.Run("valid key", func(t *testing.T) {
		key, _ := rsa.GenerateKey(rand.Reader, 2048)
		nB64 := base64.RawURLEncoding.EncodeToString(key.N.Bytes())
		eB64 := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())
		raw := []json.RawMessage{
			[]byte(`{"kty":"RSA","kid":"k1","n":"` + nB64 + `","e":"` + eB64 + `"}`),
		}
		keys, err := keysFromJWKS(raw)
		if err != nil {
			t.Fatalf(errFmtUnexpected, err)
		}
		if _, ok := keys["k1"]; !ok {
			t.Error("expected key k1 in result")
		}
	})
}

func TestKeysFromCertMap(t *testing.T) {
	t.Run(errMsgInvalidPEM, func(t *testing.T) {
		certMap := map[string]string{"kid1": "not a PEM block"}
		_, err := keysFromCertMap(certMap)
		requireErrorContains(t, err, errMsgInvalidPEM, errMsgInvalidPEM)
	})

	t.Run("invalid certificate DER", func(t *testing.T) {
		// Valid PEM wrapping but invalid DER content
		block := &pem.Block{Type: "CERTIFICATE", Bytes: []byte("notvalidder")}
		certPEM := string(pem.EncodeToMemory(block))
		certMap := map[string]string{"kid1": certPEM}
		_, err := keysFromCertMap(certMap)
		requireError(t, err)
	})

	t.Run("non-RSA certificate", func(t *testing.T) {
		// Generate an ECDSA key and self-signed cert
		ecKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		template := &x509.Certificate{
			SerialNumber: big.NewInt(1),
			Subject:      pkix.Name{CommonName: "ec-test"},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(time.Hour),
		}
		certDER, _ := x509.CreateCertificate(rand.Reader, template, template, &ecKey.PublicKey, ecKey)
		certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}))
		certMap := map[string]string{"kid1": certPEM}
		_, err := keysFromCertMap(certMap)
		requireErrorContains(t, err, "not RSA", "not RSA")
	})

	t.Run("valid RSA cert", func(t *testing.T) {
		key, _ := rsa.GenerateKey(rand.Reader, 2048)
		certPEM := makeSelfSignedCert(t, key)
		certMap := map[string]string{"kid1": certPEM}
		keys, err := keysFromCertMap(certMap)
		if err != nil {
			t.Fatalf(errFmtUnexpected, err)
		}
		if _, ok := keys["kid1"]; !ok {
			t.Error(msgExpectedKid1)
		}
	})
}

func testResolveJWKSURIExplicit(t *testing.T) {
	t.Helper()
	v := NewOIDCVerifier(testIssuer, "aud", "https://explicit.example.com/jwks", nil)
	uri, err := v.resolveJWKSURI()
	if err != nil {
		t.Fatalf(errFmtUnexpected, err)
	}
	if uri != "https://explicit.example.com/jwks" {
		t.Errorf("expected explicit URI, got %q", uri)
	}
}

func testResolveJWKSURIDiscoverySuccess(t *testing.T) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"jwks_uri": "https://auth.example.com/jwks",
	})
	server := httptest.NewServer(makeJSONHandler(body))
	defer server.Close()

	v := NewOIDCVerifier(server.URL, "aud", "", nil)
	uri, err := v.resolveJWKSURI()
	if err != nil {
		t.Fatalf(errFmtUnexpected, err)
	}
	if uri != "https://auth.example.com/jwks" {
		t.Errorf("expected discovered URI, got %q", uri)
	}
}

func testResolveJWKSURIDiscoveryErrors(t *testing.T) {
	t.Helper()

	t.Run("discovery HTTP error", func(t *testing.T) {
		v := NewOIDCVerifier(testUnreachableServer, "aud", "", nil)
		_, err := v.resolveJWKSURI()
		requireError(t, err)
	})

	t.Run("discovery non-200", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()
		v := NewOIDCVerifier(server.URL, "aud", "", nil)
		_, err := v.resolveJWKSURI()
		requireError(t, err)
	})

	t.Run("discovery invalid JSON", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("not json"))
		}))
		defer server.Close()
		v := NewOIDCVerifier(server.URL, "aud", "", nil)
		_, err := v.resolveJWKSURI()
		requireError(t, err)
	})

	t.Run("discovery missing jwks_uri", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"issuer":"https://example.com"}`))
		}))
		defer server.Close()
		v := NewOIDCVerifier(server.URL, "aud", "", nil)
		_, err := v.resolveJWKSURI()
		requireError(t, err)
	})
}

func TestResolveJWKSURI(t *testing.T) {
	t.Run("explicit jwksURI configured", func(t *testing.T) {
		testResolveJWKSURIExplicit(t)
	})

	t.Run("discovery success", func(t *testing.T) {
		testResolveJWKSURIDiscoverySuccess(t)
	})

	testResolveJWKSURIDiscoveryErrors(t)
}

func TestRefreshKeys_DoubleCheck(t *testing.T) {
	// Set keysExpiry to future and jwksURI to a bad URL.
	// The double-check should fire and return nil without making any HTTP calls.
	v := NewOIDCVerifier(testIssuer, "aud", testUnreachableJWKS, nil)
	v.keysExpiry = time.Now().Add(time.Hour)

	err := v.refreshKeys()
	if err != nil {
		t.Errorf("expected nil (double-check should prevent HTTP call), got %v", err)
	}
}

func TestRSAPublicKeyFromJWK(t *testing.T) {
	t.Run("invalid modulus", func(t *testing.T) {
		_, err := rsaPublicKeyFromJWK("!!!invalid!!!", "AQAB")
		requireError(t, err)
	})

	t.Run("invalid exponent", func(t *testing.T) {
		key, _ := rsa.GenerateKey(rand.Reader, 2048)
		nB64 := base64.RawURLEncoding.EncodeToString(key.N.Bytes())
		_, err := rsaPublicKeyFromJWK(nB64, "!!!invalid!!!")
		requireError(t, err)
	})
}

func TestGetKey_FastPath(t *testing.T) {
	// Test the fast path where cache is valid and key is present
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	const kid = "fastkey"

	v := NewOIDCVerifier(testIssuer, "aud", testUnreachableJWKS, nil)
	v.keys[kid] = &key.PublicKey
	v.keysExpiry = time.Now().Add(time.Hour)

	pub, err := v.getKey(kid)
	if err != nil {
		t.Fatalf(errFmtUnexpected, err)
	}
	if pub != &key.PublicKey {
		t.Error("expected cached public key to be returned")
	}
}

func TestRefreshKeys_ResolveError(t *testing.T) {
	// jwksURI is empty, issuer points to nothing - resolveJWKSURI will fail
	v := NewOIDCVerifier(testUnreachableServer, "aud", "", nil)
	err := v.refreshKeys()
	if err == nil {
		t.Error("expected error from resolveJWKSURI, got nil")
	}
}

func TestRefreshKeys_FetchError(t *testing.T) {
	// jwksURI points to a bad URL - fetchKeys will fail, hitting the return err branch
	v := NewOIDCVerifier(testIssuer, "aud", testUnreachableJWKS, nil)
	err := v.refreshKeys()
	if err == nil {
		t.Error("expected error from fetchKeys, got nil")
	}
}

func TestGetKey_RefreshError(t *testing.T) {
	// refreshKeys will fail; getKey should propagate that error (return nil, err branch)
	v := NewOIDCVerifier(testIssuer, "aud", testUnreachableJWKS, nil)
	_, err := v.getKey("somekid")
	if err == nil {
		t.Error("expected error from getKey when refresh fails, got nil")
	}
}

func TestFetchKeys_CertMapNoBeginPrefix(t *testing.T) {
	// certMap with string values that don't start with "-----BEGIN "
	// This exercises the break path in the cert map detection
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := json.Marshal(map[string]string{"kid1": "not-a-pem-value"})
		w.Header().Set(testHeaderContentType, testContentTypeJSON)
		_, _ = w.Write(body)
	}))
	defer server.Close()

	client := &http.Client{}
	_, err := fetchKeys(client, server.URL)
	requireErrorContains(t, err, "unrecognized", errMsgUnrecognizedFmt)
}

func TestKeysFromJWKS_InvalidJSONSkipped(t *testing.T) {
	// An entry with invalid JSON is skipped (the continue branch)
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	nB64 := base64.RawURLEncoding.EncodeToString(key.N.Bytes())
	eB64 := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())

	raw := []json.RawMessage{
		json.RawMessage(`not valid json at all`),
		[]byte(`{"kty":"RSA","kid":"k1","n":"` + nB64 + `","e":"` + eB64 + `"}`),
	}
	keys, err := keysFromJWKS(raw)
	if err != nil {
		t.Fatalf(errFmtUnexpected, err)
	}
	// The invalid entry is skipped, valid RSA key is parsed
	if _, ok := keys["k1"]; !ok {
		t.Error("expected key k1 in result")
	}
}
