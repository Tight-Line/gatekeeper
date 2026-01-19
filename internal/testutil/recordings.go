// Package testutil provides utilities for testing webhook verification,
// including a VCR-like system for recording and replaying webhook payloads.
package testutil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const slackTimestampHeader = "X-Slack-Request-Timestamp"

// Recording represents a captured webhook request for testing.
type Recording struct {
	Metadata RecordingMetadata `json:"metadata"`
	Request  RecordingRequest  `json:"request"`

	// SigningSecret is the secret used to sign this recording.
	// For test recordings, use a known test value.
	SigningSecret string `json:"signing_secret,omitempty"`

	// Token is the API key/token for api_key verifier recordings.
	Token string `json:"token,omitempty"`
}

// RecordingMetadata contains information about the recording.
type RecordingMetadata struct {
	Provider    string `json:"provider"`
	EventType   string `json:"event_type"`
	RecordedAt  string `json:"recorded_at,omitempty"`
	Description string `json:"description"`
}

// RecordingRequest represents the HTTP request portion of a recording.
type RecordingRequest struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Body    json.RawMessage   `json:"body"`
}

// LoadRecording loads a recording from the testdata directory.
// The path is constructed as: testdata/recordings/{provider}/{eventType}/{name}.json
func LoadRecording(provider, eventType, name string) (*Recording, error) {
	// Find testdata directory by walking up from current directory
	testdataPath, err := findTestdataDir()
	if err != nil {
		return nil, err
	}

	path := filepath.Join(testdataPath, "recordings", provider, eventType, name+".json")
	return LoadRecordingFromPath(path)
}

// LoadRecordingFromPath loads a recording from an absolute path.
func LoadRecordingFromPath(path string) (*Recording, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading recording file: %w", err)
	}

	var rec Recording
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("parsing recording: %w", err)
	}

	return &rec, nil
}

// getwd is a variable for os.Getwd, allowing tests to override it.
var getwd = os.Getwd

// findTestdataDir walks up the directory tree to find testdata/recordings.
func findTestdataDir() (string, error) {
	dir, err := getwd()
	if err != nil {
		return "", err
	}

	for {
		candidate := filepath.Join(dir, "testdata")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("testdata directory not found")
		}
		dir = parent
	}
}

// Clone returns a deep copy of the recording.
func (r *Recording) Clone() *Recording {
	clone := &Recording{
		Metadata: RecordingMetadata{
			Provider:    r.Metadata.Provider,
			EventType:   r.Metadata.EventType,
			RecordedAt:  r.Metadata.RecordedAt,
			Description: r.Metadata.Description,
		},
		Request: RecordingRequest{
			Method:  r.Request.Method,
			Path:    r.Request.Path,
			Headers: make(map[string]string),
			Body:    make(json.RawMessage, len(r.Request.Body)),
		},
		SigningSecret: r.SigningSecret,
		Token:         r.Token,
	}

	maps.Copy(clone.Request.Headers, r.Request.Headers)
	copy(clone.Request.Body, r.Request.Body)

	return clone
}

// WithHeader returns a copy with the specified header set or replaced.
func (r *Recording) WithHeader(key, value string) *Recording {
	clone := r.Clone()
	clone.Request.Headers[key] = value
	return clone
}

// WithoutHeader returns a copy with the specified header removed.
func (r *Recording) WithoutHeader(key string) *Recording {
	clone := r.Clone()
	delete(clone.Request.Headers, key)
	return clone
}

// WithBody returns a copy with the body replaced.
func (r *Recording) WithBody(body any) *Recording {
	clone := r.Clone()
	data, err := json.Marshal(body)
	if err != nil {
		panic(fmt.Sprintf("WithBody: failed to marshal body: %v", err))
	}
	clone.Request.Body = data
	return clone
}

// WithBodyField returns a copy with a specific field in the JSON body modified.
// The field path uses dot notation (e.g., "event.type" or "data.id").
func (r *Recording) WithBodyField(fieldPath string, value any) *Recording {
	clone := r.Clone()

	var body map[string]any
	if err := json.Unmarshal(clone.Request.Body, &body); err != nil {
		panic(fmt.Sprintf("WithBodyField: failed to parse body as object: %v", err))
	}

	setNestedField(body, fieldPath, value)

	data, err := json.Marshal(body)
	if err != nil {
		panic(fmt.Sprintf("WithBodyField: failed to marshal body: %v", err))
	}
	clone.Request.Body = data

	return clone
}

// setNestedField sets a value in a nested map using dot notation.
func setNestedField(m map[string]any, path string, value any) {
	parts := strings.Split(path, ".")

	for i, part := range parts[:len(parts)-1] {
		next, ok := m[part]
		if !ok {
			// Create intermediate maps as needed
			newMap := make(map[string]any)
			m[part] = newMap
			m = newMap
			continue
		}

		nextMap, ok := next.(map[string]any)
		if !ok {
			panic(fmt.Sprintf("setNestedField: path %q: %q is not an object", path, strings.Join(parts[:i+1], ".")))
		}
		m = nextMap
	}

	m[parts[len(parts)-1]] = value
}

// InvalidateSignature returns a copy with a corrupted signature header.
// This is provider-specific and handles common signature header names.
func (r *Recording) InvalidateSignature() *Recording {
	clone := r.Clone()

	// Provider-specific signature headers
	signatureHeaders := []string{
		"X-Slack-Signature",
		"X-Hub-Signature-256",
		"X-Shopify-Hmac-SHA256",
		"X-Signature",
		"X-Webhook-Signature",
	}

	for _, header := range signatureHeaders {
		if _, exists := clone.Request.Headers[header]; exists {
			clone.Request.Headers[header] = "invalid-signature-for-testing"
		}
	}

	return clone
}

// ExpireTimestamp returns a copy with an expired timestamp.
// The age parameter specifies how far in the past to set the timestamp.
// This is provider-specific and handles common timestamp header names.
func (r *Recording) ExpireTimestamp(age time.Duration) *Recording {
	clone := r.Clone()

	expiredTime := time.Now().Add(-age)

	// Slack uses Unix timestamp
	if _, exists := clone.Request.Headers[slackTimestampHeader]; exists {
		clone.Request.Headers[slackTimestampHeader] = fmt.Sprintf("%d", expiredTime.Unix())
	}

	return clone
}

// FutureTimestamp returns a copy with a timestamp in the future.
// This is useful for testing clock skew rejection.
func (r *Recording) FutureTimestamp(offset time.Duration) *Recording {
	clone := r.Clone()

	futureTime := time.Now().Add(offset)

	// Slack uses Unix timestamp
	if _, exists := clone.Request.Headers[slackTimestampHeader]; exists {
		clone.Request.Headers[slackTimestampHeader] = fmt.Sprintf("%d", futureTime.Unix())
	}

	return clone
}

// WithSigningSecret returns a copy with a different signing secret.
// Use this when you need to recompute signatures with a specific secret.
func (r *Recording) WithSigningSecret(secret string) *Recording {
	clone := r.Clone()
	clone.SigningSecret = secret
	return clone
}

// WithToken returns a copy with a different token.
func (r *Recording) WithToken(token string) *Recording {
	clone := r.Clone()
	clone.Token = token
	return clone
}

// ToHTTPRequest converts the recording to an *http.Request and body bytes.
// The request can be passed directly to a verifier's Verify method.
func (r *Recording) ToHTTPRequest() (req *http.Request, body []byte) {
	body = []byte(r.Request.Body)

	req = httptest.NewRequest(r.Request.Method, r.Request.Path, bytes.NewReader(body))

	// Set headers
	for key, value := range r.Request.Headers {
		req.Header.Set(key, value)
	}

	return req, body
}

// BodyString returns the body as a string.
func (r *Recording) BodyString() string {
	return string(r.Request.Body)
}

// BodyMap returns the body as a map, or panics if not a valid JSON object.
func (r *Recording) BodyMap() map[string]any {
	var m map[string]any
	if err := json.Unmarshal(r.Request.Body, &m); err != nil {
		panic(fmt.Sprintf("BodyMap: body is not a valid JSON object: %v", err))
	}
	return m
}

// Save writes the recording to a file at the specified path.
func (r *Recording) Save(path string) error {
	// Recording contains only JSON-serializable types, so MarshalIndent cannot fail.
	data, _ := json.MarshalIndent(r, "", "  ")

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing file: %w", err)
	}

	return nil
}
