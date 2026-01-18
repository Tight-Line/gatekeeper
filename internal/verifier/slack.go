package verifier

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

const (
	slackSignatureHeader  = "X-Slack-Signature"
	slackTimestampHeader  = "X-Slack-Request-Timestamp"
	slackSignatureVersion = "v0"
)

// SlackVerifier verifies Slack webhook signatures
// See: https://api.slack.com/authentication/verifying-requests-from-slack
type SlackVerifier struct {
	signingSecret   string
	maxTimestampAge time.Duration
}

// NewSlackVerifier creates a new Slack verifier
func NewSlackVerifier(signingSecret string, maxTimestampAge time.Duration) *SlackVerifier {
	if maxTimestampAge == 0 {
		maxTimestampAge = 5 * time.Minute // Slack's recommended default
	}
	return &SlackVerifier{
		signingSecret:   signingSecret,
		maxTimestampAge: maxTimestampAge,
	}
}

// Verify checks the Slack signature
func (v *SlackVerifier) Verify(r *http.Request, payload []byte) error {
	// Get timestamp header
	timestampStr := r.Header.Get(slackTimestampHeader)
	if timestampStr == "" {
		return fmt.Errorf("%w: %s header missing", ErrTimestampInvalid, slackTimestampHeader)
	}

	// Parse and validate timestamp
	ts, err := strconv.ParseInt(timestampStr, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: cannot parse timestamp", ErrTimestampInvalid)
	}

	// Check timestamp age for replay attack protection
	requestTime := time.Unix(ts, 0)
	age := time.Since(requestTime)
	if age < 0 {
		age = -age // Handle clock skew in either direction
	}
	if age > v.maxTimestampAge {
		return fmt.Errorf("%w: timestamp is %v old, max allowed is %v", ErrTimestampExpired, age, v.maxTimestampAge)
	}

	// Get signature header
	signature := r.Header.Get(slackSignatureHeader)
	if signature == "" {
		return fmt.Errorf("%w: %s header missing", ErrSignatureEmpty, slackSignatureHeader)
	}

	// Construct the base string: v0:{timestamp}:{body}
	baseString := fmt.Sprintf("%s:%s:%s", slackSignatureVersion, timestampStr, string(payload))

	// Compute HMAC-SHA256
	mac := hmac.New(sha256.New, []byte(v.signingSecret))
	mac.Write([]byte(baseString))
	expectedSig := slackSignatureVersion + "=" + hex.EncodeToString(mac.Sum(nil))

	// Constant-time comparison
	if !hmac.Equal([]byte(expectedSig), []byte(signature)) {
		return ErrSignatureMismatch
	}

	return nil
}

// Type returns the verifier type
func (v *SlackVerifier) Type() string {
	return "slack"
}
