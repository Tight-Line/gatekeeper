package verifier

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

const (
	sendgridSignatureHeader = "X-Twilio-Email-Event-Webhook-Signature"
	sendgridTimestampHeader = "X-Twilio-Email-Event-Webhook-Timestamp"
)

// SendGridVerifier verifies SendGrid Event Webhook signatures.
//
// SendGrid signs Event Webhook deliveries using ECDSA over the NIST P-256 curve.
// The signed content is timestamp + raw payload (concatenated bytes), hashed
// with SHA-256. The signature header carries the base64-encoded ASN.1 DER
// signature, and the timestamp is delivered in a sibling header.
//
// See: https://www.twilio.com/docs/sendgrid/for-developers/tracking-events/getting-started-event-webhook-security-features
type SendGridVerifier struct {
	publicKey       *ecdsa.PublicKey
	maxTimestampAge time.Duration
}

// NewSendGridVerifier creates a new SendGrid verifier from a public key.
//
// The publicKey argument accepts either a PEM-encoded ECDSA public key
// (SubjectPublicKeyInfo) or the base64-encoded DER form that SendGrid
// displays in its dashboard when signed webhooks are enabled.
//
// maxTimestampAge enables replay protection. Zero disables the check.
func NewSendGridVerifier(publicKey string, maxTimestampAge time.Duration) (*SendGridVerifier, error) {
	pub, err := parseSendGridPublicKey(publicKey)
	if err != nil {
		return nil, err
	}
	return &SendGridVerifier{
		publicKey:       pub,
		maxTimestampAge: maxTimestampAge,
	}, nil
}

// Verify checks the SendGrid ECDSA signature against the request payload.
func (v *SendGridVerifier) Verify(r *http.Request, payload []byte) error {
	timestampStr := r.Header.Get(sendgridTimestampHeader)
	if timestampStr == "" {
		return fmt.Errorf("%w: %s header missing", ErrTimestampInvalid, sendgridTimestampHeader)
	}

	if v.maxTimestampAge > 0 {
		ts, err := strconv.ParseInt(timestampStr, 10, 64)
		if err != nil {
			return fmt.Errorf("%w: cannot parse timestamp", ErrTimestampInvalid)
		}
		age := time.Since(time.Unix(ts, 0))
		if age < 0 {
			age = -age
		}
		if age > v.maxTimestampAge {
			return fmt.Errorf("%w: timestamp is %v old, max allowed is %v", ErrTimestampExpired, age, v.maxTimestampAge)
		}
	}

	signature := r.Header.Get(sendgridSignatureHeader)
	if signature == "" {
		return fmt.Errorf("%w: %s header missing", ErrSignatureEmpty, sendgridSignatureHeader)
	}

	sigBytes, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return fmt.Errorf("%w: cannot decode signature base64", ErrSignatureMismatch)
	}

	h := sha256.New()
	h.Write([]byte(timestampStr))
	h.Write(payload)
	digest := h.Sum(nil)

	if !ecdsa.VerifyASN1(v.publicKey, digest, sigBytes) {
		return ErrSignatureMismatch
	}
	return nil
}

// Type returns the verifier type name.
func (v *SendGridVerifier) Type() string {
	return "sendgrid"
}

// parseSendGridPublicKey accepts either a PEM-encoded ECDSA public key or
// the base64-encoded DER (SubjectPublicKeyInfo) that SendGrid displays.
func parseSendGridPublicKey(key string) (*ecdsa.PublicKey, error) {
	if key == "" {
		return nil, errors.New("sendgrid public key is empty")
	}

	var der []byte
	if block, _ := pem.Decode([]byte(key)); block != nil {
		der = block.Bytes
	} else {
		decoded, err := base64.StdEncoding.DecodeString(key)
		if err != nil {
			return nil, fmt.Errorf("sendgrid public key: not valid PEM or base64 DER: %w", err)
		}
		der = decoded
	}

	pub, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("sendgrid public key: parse failed: %w", err)
	}
	ecPub, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("sendgrid public key: expected ECDSA key, got %T", pub)
	}
	if ecPub.Curve != elliptic.P256() {
		return nil, fmt.Errorf("sendgrid public key: expected P-256 curve, got %s", ecPub.Curve.Params().Name)
	}
	return ecPub, nil
}
