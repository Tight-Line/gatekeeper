package verifier

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// sendgridFixture holds a P-256 keypair and the encoded public key forms
// used to construct verifiers across the test cases.
type sendgridFixture struct {
	priv       *ecdsa.PrivateKey
	pemKey     string
	base64Key  string
	wrongPriv  *ecdsa.PrivateKey
	derPubKey  []byte
	wrongCurve *ecdsa.PrivateKey
}

func newSendGridFixture(t *testing.T) *sendgridFixture {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	wrong, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate wrong key: %v", err)
	}
	wrongCurve, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatalf("generate p384 key: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("marshal pkix: %v", err)
	}
	pemBlock := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	return &sendgridFixture{
		priv:       priv,
		wrongPriv:  wrong,
		wrongCurve: wrongCurve,
		pemKey:     string(pemBlock),
		base64Key:  base64.StdEncoding.EncodeToString(der),
		derPubKey:  der,
	}
}

func signSendGridPayload(t *testing.T, priv *ecdsa.PrivateKey, timestamp string, payload []byte) string {
	t.Helper()
	h := sha256.New()
	h.Write([]byte(timestamp))
	h.Write(payload)
	sig, err := ecdsa.SignASN1(rand.Reader, priv, h.Sum(nil))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return base64.StdEncoding.EncodeToString(sig)
}

func TestSendGridVerifier_Verify(t *testing.T) {
	fx := newSendGridFixture(t)
	v, err := NewSendGridVerifier(fx.pemKey, 5*time.Minute)
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}

	tests := []verifierTestCase{
		{
			name: "valid signature with PEM key",
			setup: func() (*http.Request, []byte) {
				body := []byte(`[{"event":"delivered","email":"test@example.com"}]`)
				ts := strconv.FormatInt(time.Now().Unix(), 10)
				sig := signSendGridPayload(t, fx.priv, ts, body)
				req := httptest.NewRequest(http.MethodPost, "/sendgrid", strings.NewReader(string(body)))
				req.Header.Set(sendgridSignatureHeader, sig)
				req.Header.Set(sendgridTimestampHeader, ts)
				return req, body
			},
		},
		{
			name: "missing timestamp header",
			setup: func() (*http.Request, []byte) {
				body := []byte(`[]`)
				req := httptest.NewRequest(http.MethodPost, "/sendgrid", strings.NewReader(string(body)))
				req.Header.Set(sendgridSignatureHeader, "ignored")
				return req, body
			},
			wantErr:   true,
			errString: "header missing",
		},
		{
			name: "unparseable timestamp",
			setup: func() (*http.Request, []byte) {
				body := []byte(`[]`)
				req := httptest.NewRequest(http.MethodPost, "/sendgrid", strings.NewReader(string(body)))
				req.Header.Set(sendgridTimestampHeader, "not-a-number")
				req.Header.Set(sendgridSignatureHeader, "ignored")
				return req, body
			},
			wantErr:   true,
			errString: "cannot parse timestamp",
		},
		{
			name: "expired timestamp",
			setup: func() (*http.Request, []byte) {
				body := []byte(`[]`)
				ts := strconv.FormatInt(time.Now().Add(-1*time.Hour).Unix(), 10)
				sig := signSendGridPayload(t, fx.priv, ts, body)
				req := httptest.NewRequest(http.MethodPost, "/sendgrid", strings.NewReader(string(body)))
				req.Header.Set(sendgridTimestampHeader, ts)
				req.Header.Set(sendgridSignatureHeader, sig)
				return req, body
			},
			wantErr:   true,
			errString: "timestamp is",
		},
		{
			name: "future timestamp tolerated within skew",
			setup: func() (*http.Request, []byte) {
				body := []byte(`[]`)
				ts := strconv.FormatInt(time.Now().Add(30*time.Second).Unix(), 10)
				sig := signSendGridPayload(t, fx.priv, ts, body)
				req := httptest.NewRequest(http.MethodPost, "/sendgrid", strings.NewReader(string(body)))
				req.Header.Set(sendgridTimestampHeader, ts)
				req.Header.Set(sendgridSignatureHeader, sig)
				return req, body
			},
		},
		{
			name: "missing signature header",
			setup: func() (*http.Request, []byte) {
				body := []byte(`[]`)
				ts := strconv.FormatInt(time.Now().Unix(), 10)
				req := httptest.NewRequest(http.MethodPost, "/sendgrid", strings.NewReader(string(body)))
				req.Header.Set(sendgridTimestampHeader, ts)
				return req, body
			},
			wantErr:   true,
			errString: "signature header is empty",
		},
		{
			name: "invalid base64 signature",
			setup: func() (*http.Request, []byte) {
				body := []byte(`[]`)
				ts := strconv.FormatInt(time.Now().Unix(), 10)
				req := httptest.NewRequest(http.MethodPost, "/sendgrid", strings.NewReader(string(body)))
				req.Header.Set(sendgridTimestampHeader, ts)
				req.Header.Set(sendgridSignatureHeader, "!!!not-base64!!!")
				return req, body
			},
			wantErr:   true,
			errString: "cannot decode signature base64",
		},
		{
			name: "signature from wrong key",
			setup: func() (*http.Request, []byte) {
				body := []byte(`[]`)
				ts := strconv.FormatInt(time.Now().Unix(), 10)
				sig := signSendGridPayload(t, fx.wrongPriv, ts, body)
				req := httptest.NewRequest(http.MethodPost, "/sendgrid", strings.NewReader(string(body)))
				req.Header.Set(sendgridTimestampHeader, ts)
				req.Header.Set(sendgridSignatureHeader, sig)
				return req, body
			},
			wantErr:   true,
			errString: "signature does not match",
		},
		{
			name: "tampered payload",
			setup: func() (*http.Request, []byte) {
				original := []byte(`[{"event":"delivered"}]`)
				tampered := []byte(`[{"event":"bounce"}]`)
				ts := strconv.FormatInt(time.Now().Unix(), 10)
				sig := signSendGridPayload(t, fx.priv, ts, original)
				req := httptest.NewRequest(http.MethodPost, "/sendgrid", strings.NewReader(string(tampered)))
				req.Header.Set(sendgridTimestampHeader, ts)
				req.Header.Set(sendgridSignatureHeader, sig)
				return req, tampered
			},
			wantErr:   true,
			errString: "signature does not match",
		},
	}

	runVerifierTests(t, v, tests)
}

func TestSendGridVerifier_VerifyWithBase64Key(t *testing.T) {
	fx := newSendGridFixture(t)
	v, err := NewSendGridVerifier(fx.base64Key, 0)
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}

	body := []byte(`[{"event":"open"}]`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sig := signSendGridPayload(t, fx.priv, ts, body)
	req := httptest.NewRequest(http.MethodPost, "/sendgrid", strings.NewReader(string(body)))
	req.Header.Set(sendgridTimestampHeader, ts)
	req.Header.Set(sendgridSignatureHeader, sig)

	if err := v.Verify(req, body); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestSendGridVerifier_VerifyZeroMaxAgeSkipsExpiryCheck(t *testing.T) {
	fx := newSendGridFixture(t)
	v, err := NewSendGridVerifier(fx.pemKey, 0)
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}

	body := []byte(`[]`)
	// Ancient timestamp, but max_timestamp_age=0 disables the check
	ts := strconv.FormatInt(time.Now().Add(-100*time.Hour).Unix(), 10)
	sig := signSendGridPayload(t, fx.priv, ts, body)
	req := httptest.NewRequest(http.MethodPost, "/sendgrid", strings.NewReader(string(body)))
	req.Header.Set(sendgridTimestampHeader, ts)
	req.Header.Set(sendgridSignatureHeader, sig)

	if err := v.Verify(req, body); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestNewSendGridVerifier_KeyErrors(t *testing.T) {
	fx := newSendGridFixture(t)

	// RSA SPKI to trigger "expected ECDSA key" branch
	rsaPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa: %v", err)
	}
	rsaDER, err := x509.MarshalPKIXPublicKey(&rsaPriv.PublicKey)
	if err != nil {
		t.Fatalf("marshal rsa: %v", err)
	}
	rsaB64 := base64.StdEncoding.EncodeToString(rsaDER)

	wrongCurveDER, err := x509.MarshalPKIXPublicKey(&fx.wrongCurve.PublicKey)
	if err != nil {
		t.Fatalf("marshal p384: %v", err)
	}
	wrongCurveB64 := base64.StdEncoding.EncodeToString(wrongCurveDER)

	cases := []struct {
		name    string
		key     string
		wantErr string
	}{
		{"empty", "", "public key is empty"},
		{"not base64 or pem", "!!! not valid !!!", "not valid PEM or base64 DER"},
		{"base64 but not DER", base64.StdEncoding.EncodeToString([]byte("garbage")), "parse failed"},
		{"rsa key", rsaB64, "expected ECDSA key"},
		{"wrong curve", wrongCurveB64, "expected P-256 curve"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewSendGridVerifier(tc.key, 0)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("expected error containing %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

func TestSendGridVerifier_Type(t *testing.T) {
	fx := newSendGridFixture(t)
	v, err := NewSendGridVerifier(fx.pemKey, 0)
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	assertVerifierType(t, v, "sendgrid")
}
