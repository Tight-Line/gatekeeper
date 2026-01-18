package testutil

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecording_Clone(t *testing.T) {
	original := &Recording{
		Metadata: RecordingMetadata{
			Provider:    "slack",
			EventType:   "event_callback",
			Description: "test recording",
		},
		Request: RecordingRequest{
			Method:  "POST",
			Path:    "/webhooks/slack",
			Headers: map[string]string{"X-Header": "value"},
			Body:    json.RawMessage(`{"key": "value"}`),
		},
		SigningSecret: "secret",
	}

	clone := original.Clone()

	// Verify deep copy
	clone.Metadata.Provider = "github"
	clone.Request.Headers["X-Header"] = "modified"
	clone.Request.Body = json.RawMessage(`{"modified": true}`)

	if original.Metadata.Provider != "slack" {
		t.Error("original metadata was modified")
	}
	if original.Request.Headers["X-Header"] != "value" {
		t.Error("original headers were modified")
	}
	if string(original.Request.Body) != `{"key": "value"}` {
		t.Error("original body was modified")
	}
}

func TestRecording_WithHeader(t *testing.T) {
	rec := &Recording{
		Request: RecordingRequest{
			Headers: map[string]string{"Existing": "value"},
		},
	}

	modified := rec.WithHeader("New-Header", "new-value")

	if modified.Request.Headers["New-Header"] != "new-value" {
		t.Error("header was not set")
	}
	if _, exists := rec.Request.Headers["New-Header"]; exists {
		t.Error("original was modified")
	}
}

func TestRecording_WithoutHeader(t *testing.T) {
	rec := &Recording{
		Request: RecordingRequest{
			Headers: map[string]string{"Remove-Me": "value", "Keep": "value"},
		},
	}

	modified := rec.WithoutHeader("Remove-Me")

	if _, exists := modified.Request.Headers["Remove-Me"]; exists {
		t.Error("header was not removed")
	}
	if modified.Request.Headers["Keep"] != "value" {
		t.Error("other header was removed")
	}
	if rec.Request.Headers["Remove-Me"] != "value" {
		t.Error("original was modified")
	}
}

func TestRecording_WithBody(t *testing.T) {
	rec := &Recording{
		Request: RecordingRequest{
			Body: json.RawMessage(`{"original": true}`),
		},
	}

	modified := rec.WithBody(map[string]string{"new": "body"})

	var body map[string]string
	if err := json.Unmarshal(modified.Request.Body, &body); err != nil {
		t.Fatalf("failed to parse body: %v", err)
	}
	if body["new"] != "body" {
		t.Error("body was not replaced")
	}
}

func TestRecording_WithBodyField(t *testing.T) {
	rec := &Recording{
		Request: RecordingRequest{
			Body: json.RawMessage(`{"event": {"type": "message", "user": "U123"}}`),
		},
	}

	modified := rec.WithBodyField("event.type", "reaction_added")

	var body map[string]any
	if err := json.Unmarshal(modified.Request.Body, &body); err != nil {
		t.Fatalf("failed to parse body: %v", err)
	}

	event := body["event"].(map[string]any)
	if event["type"] != "reaction_added" {
		t.Errorf("field was not modified: got %v", event["type"])
	}
	if event["user"] != "U123" {
		t.Error("other fields were modified")
	}
}

func TestRecording_WithBodyField_CreatesIntermediateMaps(t *testing.T) {
	rec := &Recording{
		Request: RecordingRequest{
			Body: json.RawMessage(`{}`),
		},
	}

	modified := rec.WithBodyField("deeply.nested.field", "value")

	var body map[string]any
	if err := json.Unmarshal(modified.Request.Body, &body); err != nil {
		t.Fatalf("failed to parse body: %v", err)
	}

	deeply := body["deeply"].(map[string]any)
	nested := deeply["nested"].(map[string]any)
	if nested["field"] != "value" {
		t.Error("nested field was not created")
	}
}

func TestRecording_InvalidateSignature(t *testing.T) {
	tests := []struct {
		name   string
		header string
	}{
		{"slack", "X-Slack-Signature"},
		{"github", "X-Hub-Signature-256"},
		{"shopify", "X-Shopify-Hmac-SHA256"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &Recording{
				Request: RecordingRequest{
					Headers: map[string]string{tt.header: "valid-signature"},
				},
			}

			modified := rec.InvalidateSignature()

			if modified.Request.Headers[tt.header] != "invalid-signature-for-testing" {
				t.Error("signature was not invalidated")
			}
			if rec.Request.Headers[tt.header] != "valid-signature" {
				t.Error("original was modified")
			}
		})
	}
}

func TestRecording_ExpireTimestamp(t *testing.T) {
	rec := &Recording{
		Request: RecordingRequest{
			Headers: map[string]string{"X-Slack-Request-Timestamp": "1234567890"},
		},
	}

	modified := rec.ExpireTimestamp(10 * 60 * 1000000000) // 10 minutes in nanoseconds

	// Just verify the timestamp changed
	if modified.Request.Headers["X-Slack-Request-Timestamp"] == "1234567890" {
		t.Error("timestamp was not modified")
	}
}

func TestRecording_ToHTTPRequest(t *testing.T) {
	rec := &Recording{
		Request: RecordingRequest{
			Method: "POST",
			Path:   "/webhooks/test",
			Headers: map[string]string{
				"Content-Type": "application/json",
				"X-Custom":     "header",
			},
			Body: json.RawMessage(`{"test": "data"}`),
		},
	}

	req, body := rec.ToHTTPRequest()

	if req.Method != "POST" {
		t.Errorf("method: got %q, want POST", req.Method)
	}
	if req.URL.Path != "/webhooks/test" {
		t.Errorf("path: got %q, want /webhooks/test", req.URL.Path)
	}
	if req.Header.Get("Content-Type") != "application/json" {
		t.Error("Content-Type header not set")
	}
	if req.Header.Get("X-Custom") != "header" {
		t.Error("custom header not set")
	}
	if string(body) != `{"test": "data"}` {
		t.Errorf("body: got %q", string(body))
	}
}

func TestRecording_SaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test-recording.json")

	original := &Recording{
		Metadata: RecordingMetadata{
			Provider:    "test",
			EventType:   "test_event",
			Description: "test description",
		},
		Request: RecordingRequest{
			Method:  "POST",
			Path:    "/test",
			Headers: map[string]string{"X-Test": "value"},
			Body:    json.RawMessage(`{"key": "value"}`),
		},
		SigningSecret: "test-secret",
	}

	if err := original.Save(path); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("file was not created")
	}

	// Load it back
	loaded, err := LoadRecordingFromPath(path)
	if err != nil {
		t.Fatalf("LoadRecordingFromPath failed: %v", err)
	}

	if loaded.Metadata.Provider != original.Metadata.Provider {
		t.Error("provider mismatch")
	}
	if loaded.SigningSecret != original.SigningSecret {
		t.Error("signing secret mismatch")
	}
	// Compare bodies as parsed JSON since formatting may differ
	var origBody, loadedBody map[string]any
	if err := json.Unmarshal(original.Request.Body, &origBody); err != nil {
		t.Fatalf("failed to parse original body: %v", err)
	}
	if err := json.Unmarshal(loaded.Request.Body, &loadedBody); err != nil {
		t.Fatalf("failed to parse loaded body: %v", err)
	}
	if origBody["key"] != loadedBody["key"] {
		t.Errorf("body mismatch: original=%v, loaded=%v", origBody, loadedBody)
	}
}

func TestRecording_BodyMap(t *testing.T) {
	rec := &Recording{
		Request: RecordingRequest{
			Body: json.RawMessage(`{"key": "value", "nested": {"inner": 123}}`),
		},
	}

	m := rec.BodyMap()

	if m["key"] != "value" {
		t.Error("key not found")
	}
	nested := m["nested"].(map[string]any)
	if nested["inner"] != float64(123) {
		t.Error("nested value not found")
	}
}

func TestRecording_BodyMap_InvalidJSON(t *testing.T) {
	rec := &Recording{
		Request: RecordingRequest{
			Body: json.RawMessage(`not json`),
		},
	}

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for invalid JSON")
		}
	}()

	rec.BodyMap()
}

func TestRecording_BodyString(t *testing.T) {
	rec := &Recording{
		Request: RecordingRequest{
			Body: json.RawMessage(`{"test": "data"}`),
		},
	}

	s := rec.BodyString()
	if s != `{"test": "data"}` {
		t.Errorf("got %q, want %q", s, `{"test": "data"}`)
	}
}

func TestRecording_FutureTimestamp(t *testing.T) {
	rec := &Recording{
		Request: RecordingRequest{
			Headers: map[string]string{"X-Slack-Request-Timestamp": "1234567890"},
		},
	}

	modified := rec.FutureTimestamp(10 * time.Minute)

	// Timestamp should be in the future (greater than current time)
	if modified.Request.Headers["X-Slack-Request-Timestamp"] == "1234567890" {
		t.Error("timestamp was not modified")
	}
	// Original should not be modified
	if rec.Request.Headers["X-Slack-Request-Timestamp"] != "1234567890" {
		t.Error("original was modified")
	}
}

func TestRecording_FutureTimestamp_NoTimestampHeader(t *testing.T) {
	rec := &Recording{
		Request: RecordingRequest{
			Headers: map[string]string{"X-Other": "value"},
		},
	}

	modified := rec.FutureTimestamp(10 * time.Minute)

	// Should not add a timestamp header if one doesn't exist
	if _, exists := modified.Request.Headers["X-Slack-Request-Timestamp"]; exists {
		t.Error("timestamp header should not be added if not present")
	}
}

func TestRecording_WithSigningSecret(t *testing.T) {
	rec := &Recording{
		SigningSecret: "original-secret",
	}

	modified := rec.WithSigningSecret("new-secret")

	if modified.SigningSecret != "new-secret" {
		t.Errorf("got %q, want %q", modified.SigningSecret, "new-secret")
	}
	if rec.SigningSecret != "original-secret" {
		t.Error("original was modified")
	}
}

func TestRecording_WithToken(t *testing.T) {
	rec := &Recording{
		Token: "original-token",
	}

	modified := rec.WithToken("new-token")

	if modified.Token != "new-token" {
		t.Errorf("got %q, want %q", modified.Token, "new-token")
	}
	if rec.Token != "original-token" {
		t.Error("original was modified")
	}
}

func TestLoadRecording(t *testing.T) {
	// This test requires the testdata/recordings directory to exist
	// with actual recording files. We can test with a known recording.
	rec, err := LoadRecording("slack", "event_callback", "message")
	if err != nil {
		t.Skipf("skipping test: %v (testdata/recordings may not exist)", err)
	}

	if rec.Metadata.Provider != "slack" {
		t.Errorf("provider: got %q, want %q", rec.Metadata.Provider, "slack")
	}
}

func TestLoadRecordingFromPath_NotFound(t *testing.T) {
	_, err := LoadRecordingFromPath("/nonexistent/path/recording.json")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestLoadRecordingFromPath_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "invalid.json")

	if err := os.WriteFile(path, []byte("not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadRecordingFromPath(path)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestFindTestdataDir(t *testing.T) {
	// This should find the testdata directory in the project
	dir, err := findTestdataDir()
	if err != nil {
		t.Skipf("skipping test: %v (testdata directory may not exist)", err)
	}

	if !filepath.IsAbs(dir) || !strings.HasSuffix(dir, "testdata") {
		t.Errorf("unexpected directory: %s", dir)
	}
}

func TestRecording_WithBody_Panic(t *testing.T) {
	rec := &Recording{
		Request: RecordingRequest{
			Body: json.RawMessage(`{}`),
		},
	}

	// Create an unmarshallable value (channel cannot be JSON marshaled)
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for unmarshallable body")
		}
	}()

	rec.WithBody(make(chan int))
}

func TestRecording_WithBodyField_InvalidBody(t *testing.T) {
	rec := &Recording{
		Request: RecordingRequest{
			Body: json.RawMessage(`not json`),
		},
	}

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for invalid JSON body")
		}
	}()

	rec.WithBodyField("field", "value")
}

func TestRecording_WithBodyField_PathNotObject(t *testing.T) {
	rec := &Recording{
		Request: RecordingRequest{
			Body: json.RawMessage(`{"field": "string-not-object"}`),
		},
	}

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic when path traverses non-object")
		}
	}()

	rec.WithBodyField("field.nested", "value")
}

func TestRecording_WithBodyField_UnmarshalableValue(t *testing.T) {
	rec := &Recording{
		Request: RecordingRequest{
			Body: json.RawMessage(`{"field": "value"}`),
		},
	}

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic when value can't be marshaled")
		}
	}()

	// Channel values cannot be JSON marshaled
	rec.WithBodyField("field", make(chan int))
}

func TestRecording_Save_InvalidPath(t *testing.T) {
	rec := &Recording{
		Metadata: RecordingMetadata{Provider: "test"},
	}

	// Try to save to a path that can't be created (null byte in path)
	err := rec.Save("/\x00/invalid")
	if err == nil {
		t.Error("expected error for invalid path")
	}
}

func TestRecording_Save_WriteError(t *testing.T) {
	rec := &Recording{
		Metadata: RecordingMetadata{Provider: "test"},
	}

	// Create a read-only directory
	tmpDir := t.TempDir()
	readOnlyDir := filepath.Join(tmpDir, "readonly")
	if err := os.MkdirAll(readOnlyDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a file where we want to write (to make directory creation succeed but file write fail)
	targetPath := filepath.Join(readOnlyDir, "test.json")

	// Make the directory read-only after creation
	if err := os.Chmod(readOnlyDir, 0o555); err != nil {
		t.Skip("cannot set read-only permissions")
	}
	defer func() { _ = os.Chmod(readOnlyDir, 0o755) }() // Restore for cleanup

	err := rec.Save(targetPath)
	if err == nil {
		t.Error("expected error for read-only directory")
	}
}

func TestRecording_ExpireTimestamp_NoTimestampHeader(t *testing.T) {
	rec := &Recording{
		Request: RecordingRequest{
			Headers: map[string]string{"X-Other": "value"},
		},
	}

	modified := rec.ExpireTimestamp(10 * time.Minute)

	// Should not add a timestamp header if one doesn't exist
	if _, exists := modified.Request.Headers["X-Slack-Request-Timestamp"]; exists {
		t.Error("timestamp header should not be added if not present")
	}
}

func TestLoadRecording_NotFound(t *testing.T) {
	// Test LoadRecording with a provider/event/name that definitely doesn't exist
	_, err := LoadRecording("nonexistent-provider", "nonexistent-event", "nonexistent-name")
	if err == nil {
		t.Error("expected error for nonexistent recording")
	}
}

func TestFindTestdataDir_GetwdError(t *testing.T) {
	// Save original and restore after test
	originalGetwd := getwd
	defer func() { getwd = originalGetwd }()

	// Make getwd return an error
	getwd = func() (string, error) {
		return "", os.ErrPermission
	}

	_, err := findTestdataDir()
	if err == nil {
		t.Error("expected error when getwd fails")
	}
}

func TestFindTestdataDir_NotFound(t *testing.T) {
	// Save original and restore after test
	originalGetwd := getwd
	defer func() { getwd = originalGetwd }()

	// Make getwd return root directory where testdata won't be found
	getwd = func() (string, error) {
		return "/", nil
	}

	_, err := findTestdataDir()
	if err == nil {
		t.Error("expected error when testdata not found")
	}
	if !strings.Contains(err.Error(), "testdata directory not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoadRecording_FindTestdataError(t *testing.T) {
	// Save original and restore after test
	originalGetwd := getwd
	defer func() { getwd = originalGetwd }()

	// Make getwd return an error
	getwd = func() (string, error) {
		return "", os.ErrPermission
	}

	_, err := LoadRecording("slack", "event_callback", "message")
	if err == nil {
		t.Error("expected error when findTestdataDir fails")
	}
}
