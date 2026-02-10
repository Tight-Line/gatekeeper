# Refactor: Move Challenge Handlers into Verifiers

## Summary

Extract hardcoded Slack URL verification and MS Graph validation handling from `handler.go` into an optional `ChallengeHandler` interface that verifiers implement. This moves platform-specific logic where it belongs and makes the handler generic.

## Problem

Currently `internal/proxy/handler.go` has two hardcoded platform-specific functions:
- `handleSlackURLVerification()` (lines 333-362) - checks `verifierTypes[name] == "slack"`
- `handleMicrosoftGraphValidation()` (lines 381-405) - checks `verifierTypes[name] == "json_field"`

This violates separation of concerns: platform-specific challenge/handshake logic lives in the generic proxy handler instead of with the verifiers that understand those platforms.

## Solution

Add an optional `ChallengeHandler` interface to the verifier package. Verifiers that need challenge handling implement it.

### Interface Design

Add to `internal/verifier/verifier.go`:

```go
// ChallengeResult represents the result of a challenge handler check.
type ChallengeResult struct {
    // Response is the body to write back to the caller.
    Response []byte
    // ContentType is the Content-Type header for the response.
    ContentType string
    // Handled is true if this was a challenge request that should not be forwarded.
    Handled bool
}

// ChallengeHandler is an optional interface that verifiers can implement
// to handle provider-specific challenge/validation requests.
//
// Providers like Slack and Microsoft Graph require the webhook endpoint to
// respond to challenge requests during subscription setup. These challenges
// may need to run before or after signature verification depending on the
// provider's protocol.
type ChallengeHandler interface {
    // HandleChallenge checks if the request is a challenge/validation request
    // and returns the appropriate response if so.
    //
    // If Handled is true, the caller should write Response with ContentType
    // and HTTP 200, then return without forwarding the request.
    HandleChallenge(r *http.Request, payload []byte) ChallengeResult

    // SkipVerify returns true if HandleChallenge should run BEFORE Verify().
    // This is needed for providers like Microsoft Graph where the validation
    // request has an empty body that would fail verification.
    //
    // Return false if HandleChallenge should run AFTER Verify() succeeds.
    // This is needed for providers like Slack where the challenge must be
    // cryptographically signed.
    SkipVerify() bool
}
```

### Key Design Decisions

1. **Two maps in handler** - `preChallengeHandlers` and `postChallengeHandlers` populated at startup based on `SkipVerify()`
2. **Remove `verifierTypes` map** - no longer needed; challenge handlers detected via interface assertion
3. **Slack: SkipVerify() = false** - challenge requests have valid signatures, must verify first
4. **JSONField: SkipVerify() = true** - MS Graph validation has empty body, must skip verify

---

## Implementation Details

### 1. `internal/verifier/verifier.go`

Add `ChallengeResult` struct and `ChallengeHandler` interface after the existing `Verifier` interface (see above).

### 2. `internal/verifier/slack.go`

Add import `"encoding/json"` and implement:

```go
// slackURLVerification represents Slack's URL verification challenge request
type slackURLVerification struct {
    Type      string `json:"type"`
    Challenge string `json:"challenge"`
}

// HandleChallenge checks if this is a Slack URL verification challenge
// and returns the challenge value if so.
//
// Slack sends this during app setup to verify the endpoint is reachable.
// The challenge must be signed, so this runs AFTER Verify() succeeds.
func (v *SlackVerifier) HandleChallenge(r *http.Request, payload []byte) ChallengeResult {
    var req slackURLVerification
    if err := json.Unmarshal(payload, &req); err != nil {
        return ChallengeResult{}
    }

    if req.Type != "url_verification" || req.Challenge == "" {
        return ChallengeResult{}
    }

    return ChallengeResult{
        Response:    []byte(req.Challenge),
        ContentType: "text/plain",
        Handled:     true,
    }
}

// SkipVerify returns false because Slack challenges must be verified first.
// The challenge request is signed like any other Slack request.
func (v *SlackVerifier) SkipVerify() bool {
    return false
}
```

### 3. `internal/verifier/jsonfield.go`

Implement:

```go
// HandleChallenge checks if this is a Microsoft Graph subscription validation
// request and returns the validation token if so.
//
// Microsoft Graph sends a validation request when creating or renewing subscriptions.
// The request has an empty body with validationToken in the query string.
// Since the body is empty, this MUST run BEFORE Verify().
func (v *JSONFieldVerifier) HandleChallenge(r *http.Request, payload []byte) ChallengeResult {
    validationToken := r.URL.Query().Get("validationToken")
    if validationToken == "" {
        return ChallengeResult{}
    }

    return ChallengeResult{
        Response:    []byte(validationToken),
        ContentType: "text/plain",
        Handled:     true,
    }
}

// SkipVerify returns true because Microsoft Graph validation requests
// have empty bodies that would fail JSON field verification.
func (v *JSONFieldVerifier) SkipVerify() bool {
    return true
}
```

### 4. `internal/proxy/handler.go`

**4a. Update Handler struct** (around line 47-62):

Remove:
```go
verifierTypes map[string]string // verifier name -> type (e.g., "slack", "github")
```

Add:
```go
preChallengeHandlers  map[string]verifier.ChallengeHandler // runs before Verify()
postChallengeHandlers map[string]verifier.ChallengeHandler // runs after Verify()
```

**4b. Update NewHandler** (around line 65-111):

Remove initialization of `verifierTypes` and the line `h.verifierTypes[name] = vc.Type`.

Add initialization:
```go
preChallengeHandlers:  make(map[string]verifier.ChallengeHandler),
postChallengeHandlers: make(map[string]verifier.ChallengeHandler),
```

After verifier construction loop, add:
```go
// Check if verifier implements ChallengeHandler
if ch, ok := v.(verifier.ChallengeHandler); ok {
    if ch.SkipVerify() {
        h.preChallengeHandlers[name] = ch
    } else {
        h.postChallengeHandlers[name] = ch
    }
}
```

**4c. Add generic challenge handling method:**

```go
// handleChallenge checks if the verifier has a ChallengeHandler and if this
// request is a challenge that should be handled directly.
// Returns true if the challenge was handled (caller should return).
func (h *Handler) handleChallenge(w http.ResponseWriter, r *http.Request, ctx *requestContext, ch verifier.ChallengeHandler) bool {
    result := ch.HandleChallenge(r, ctx.body)
    if !result.Handled {
        return false
    }

    h.logger.Info("handling challenge request",
        "hostname", ctx.hostname,
        "path", r.URL.Path,
        "verifier", ctx.route.Verifier,
    )

    w.Header().Set("Content-Type", result.ContentType)
    w.WriteHeader(http.StatusOK)
    _, _ = w.Write(result.Response)

    metrics.RecordRequest(ctx.hostname, ctx.route.Path, "200", time.Since(ctx.start).Seconds())
    return true
}
```

**4d. Update ServeHTTP** (around line 196-210):

Replace the MS Graph and Slack specific calls:

```go
// Handle pre-verification challenges (e.g., MS Graph validation with empty body)
if ctx.route.Verifier != "" {
    if ch, ok := h.preChallengeHandlers[ctx.route.Verifier]; ok {
        if h.handleChallenge(w, r, ctx, ch) {
            return
        }
    }
}

if !h.verifyRequest(w, r, ctx) {
    return
}

// Handle post-verification challenges (e.g., Slack URL verification with signed body)
if ctx.route.Verifier != "" {
    if ch, ok := h.postChallengeHandlers[ctx.route.Verifier]; ok {
        if h.handleChallenge(w, r, ctx, ch) {
            return
        }
    }
}
```

**4e. Delete old code:**

- Delete `handleSlackURLVerification()` function (lines 326-362)
- Delete `handleMicrosoftGraphValidation()` function (lines 364-405)
- Delete `slackURLVerification` struct (lines 320-324)

---

## New Tests

### `internal/verifier/slack_test.go`

```go
func TestSlackVerifier_HandleChallenge(t *testing.T) {
    v := NewSlackVerifier("secret", 5*time.Minute)

    tests := []struct {
        name        string
        body        string
        wantHandled bool
        wantBody    string
    }{
        {
            name:        "url_verification challenge",
            body:        `{"type":"url_verification","challenge":"test-challenge-123"}`,
            wantHandled: true,
            wantBody:    "test-challenge-123",
        },
        {
            name:        "regular event_callback",
            body:        `{"type":"event_callback","event":{"type":"message"}}`,
            wantHandled: false,
        },
        {
            name:        "invalid JSON",
            body:        `not json`,
            wantHandled: false,
        },
        {
            name:        "url_verification with empty challenge",
            body:        `{"type":"url_verification","challenge":""}`,
            wantHandled: false,
        },
    }

    for _, tc := range tests {
        t.Run(tc.name, func(t *testing.T) {
            req := httptest.NewRequest(http.MethodPost, "/webhook", nil)
            result := v.HandleChallenge(req, []byte(tc.body))

            if result.Handled != tc.wantHandled {
                t.Errorf("Handled = %v, want %v", result.Handled, tc.wantHandled)
            }
            if tc.wantHandled {
                if string(result.Response) != tc.wantBody {
                    t.Errorf("Response = %q, want %q", result.Response, tc.wantBody)
                }
                if result.ContentType != "text/plain" {
                    t.Errorf("ContentType = %q, want text/plain", result.ContentType)
                }
            }
        })
    }
}

func TestSlackVerifier_SkipVerify(t *testing.T) {
    v := NewSlackVerifier("secret", 5*time.Minute)
    if v.SkipVerify() {
        t.Error("SkipVerify() = true, want false")
    }
}
```

### `internal/verifier/jsonfield_test.go`

```go
func TestJSONFieldVerifier_HandleChallenge(t *testing.T) {
    v := NewJSONFieldVerifier("value.0.clientState", "test-token")

    tests := []struct {
        name        string
        queryParams string
        wantHandled bool
        wantBody    string
    }{
        {
            name:        "validation token present",
            queryParams: "validationToken=Validation%3ATestToken123",
            wantHandled: true,
            wantBody:    "Validation:TestToken123",
        },
        {
            name:        "no validation token",
            queryParams: "",
            wantHandled: false,
        },
        {
            name:        "other query params only",
            queryParams: "other=value",
            wantHandled: false,
        },
    }

    for _, tc := range tests {
        t.Run(tc.name, func(t *testing.T) {
            url := "/webhook"
            if tc.queryParams != "" {
                url += "?" + tc.queryParams
            }
            req := httptest.NewRequest(http.MethodPost, url, nil)
            result := v.HandleChallenge(req, nil)

            if result.Handled != tc.wantHandled {
                t.Errorf("Handled = %v, want %v", result.Handled, tc.wantHandled)
            }
            if tc.wantHandled && string(result.Response) != tc.wantBody {
                t.Errorf("Response = %q, want %q", result.Response, tc.wantBody)
            }
        })
    }
}

func TestJSONFieldVerifier_SkipVerify(t *testing.T) {
    v := NewJSONFieldVerifier("path", "token")
    if !v.SkipVerify() {
        t.Error("SkipVerify() = false, want true")
    }
}
```

---

## Verification

1. **Existing integration tests** validate external behavior (should pass unchanged):
   ```bash
   go test ./internal/proxy -run TestHandler_SlackURLVerification
   go test ./internal/proxy -run TestHandler_MicrosoftGraphValidation
   ```

2. **New unit tests** validate verifier implementations:
   ```bash
   go test ./internal/verifier -run TestSlackVerifier_HandleChallenge
   go test ./internal/verifier -run TestJSONFieldVerifier_HandleChallenge
   ```

3. **Full test suite**:
   ```bash
   go test ./...
   ```

---

## Benefits

1. **Handler becomes generic** - No more provider-specific type string checks
2. **Easy to extend** - Any verifier can implement ChallengeHandler for new platforms
3. **Clear separation of concerns** - Challenge logic lives with the verifier that understands it
4. **Backward compatible** - Existing verifiers without ChallengeHandler continue to work
5. **Self-documenting** - The interface and SkipVerify() clearly document timing requirements
