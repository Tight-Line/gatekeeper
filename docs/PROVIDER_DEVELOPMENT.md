# Provider Development Guide

This guide explains how to develop a new webhook provider (verifier and/or validator) or improve an existing one by capturing real webhooks from production systems.

## Overview

The workflow is:

1. Deploy gatekeeperd to a public server to receive real webhooks
2. Configure the provider to send webhooks to your gatekeeperd
3. Capture the raw requests (headers + body)
4. Copy recordings to your dev environment
5. Use recordings to develop and test verifiers/validators
6. Iterate until tests pass with real payloads

## Prerequisites

### Local Development Environment

- Go 1.21+ installed
- Clone of the gatekeeper repository
- Text editor or IDE

### Public Server (choose one)

**Option A: Bare Metal / VPS**
- A server with a public IP (DigitalOcean, Linode, AWS EC2, etc.)
- A domain name pointing to that IP
- Ports 80 and 443 open for ACME certificate provisioning

**Option B: Kubernetes**
- A cluster with an ingress controller
- A domain name pointing to your ingress
- TLS termination (via ingress or cert-manager)

### Provider Account

- Access to the webhook provider's dashboard (Slack, GitHub, Shopify, etc.)
- Ability to configure webhook URLs and obtain signing secrets

## Step 1: Deploy Gatekeeperd for Recording

### Create a Recording Configuration

Create a minimal config that accepts webhooks and logs them. Use the `noop` verifier initially to accept all requests:

```yaml
# config-recording.yaml
global:
  acme_email: "you@example.com"
  acme_cache_dir: "/var/cache/gatekeeper/certs"
  metrics_port: 9090
  log_level: debug  # Important: debug level logs request details

verifiers:
  # Use noop initially to accept all requests while recording
  accept-all:
    type: noop

routes:
  - hostname: webhooks.yourdomain.com
    path: /slack
    verifier: accept-all
    destination: http://localhost:8888  # Dummy destination (see below)
```

### Handle the Destination

Gatekeeperd forwards requests to a destination. During recording, you have options:

**Option 1: Drop on the floor (simplest)**

Run a simple server that accepts everything:

```bash
# Using netcat (responds with 200 OK to everything)
while true; do echo -e "HTTP/1.1 200 OK\r\n\r\n" | nc -l 8888; done

# Or using Python
python3 -c "
from http.server import HTTPServer, BaseHTTPRequestHandler
class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        self.send_response(200)
        self.end_headers()
HTTPServer(('', 8888), Handler).serve_forever()
"
```

**Option 2: Echo server (for debugging)**

```bash
# Using Python to log what's received
python3 -c "
from http.server import HTTPServer, BaseHTTPRequestHandler
class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get('Content-Length', 0))
        body = self.rfile.read(length)
        print('Headers:', dict(self.headers))
        print('Body:', body.decode())
        self.send_response(200)
        self.end_headers()
HTTPServer(('', 8888), Handler).serve_forever()
"
```

### Provider-Specific Response Requirements

Some providers require specific responses:

| Provider | Requirement |
|----------|-------------|
| Slack | Must respond to `url_verification` challenge with the challenge value |
| GitHub | Any 2xx response |
| Shopify | Any 2xx response |
| Stripe | Any 2xx response (retries on failure) |

For Slack, your echo server needs to handle the challenge:

```python
import json
from http.server import HTTPServer, BaseHTTPRequestHandler

class SlackHandler(BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get('Content-Length', 0))
        body = self.rfile.read(length)
        data = json.loads(body)

        # Log everything
        print('Headers:', dict(self.headers))
        print('Body:', body.decode())

        # Handle Slack URL verification
        if data.get('type') == 'url_verification':
            self.send_response(200)
            self.send_header('Content-Type', 'text/plain')
            self.end_headers()
            self.wfile.write(data['challenge'].encode())
        else:
            self.send_response(200)
            self.end_headers()

HTTPServer(('', 8888), SlackHandler).serve_forever()
```

### Deploy on Bare Metal

```bash
# Build
go build -o gatekeeperd ./cmd/gatekeeperd

# Copy to server
scp gatekeeperd config-recording.yaml user@server:/opt/gatekeeper/

# On the server, run gatekeeperd
/opt/gatekeeper/gatekeeperd -config /opt/gatekeeper/config-recording.yaml
```

### Deploy on Kubernetes

Use the Helm chart with recording configuration:

```bash
helm install gatekeeper-recorder ./deploy/helm/gatekeeper \
  --set config="$(cat config-recording.yaml)"
```

## Step 2: Configure the Provider

### Obtain Signing Credentials

1. Go to your provider's webhook configuration page
2. Note the signing secret/key (you'll need this for verification)
3. Configure the webhook URL to point to your gatekeeperd

Example for Slack:
1. Go to api.slack.com > Your App > Event Subscriptions
2. Set Request URL to `https://webhooks.yourdomain.com/slack`
3. Copy the "Signing Secret" from Basic Information

### Trigger Test Events

Most providers have a way to send test events:

- **Slack**: Use the app in a test workspace, send messages
- **GitHub**: Go to webhook settings > "Redeliver" on past events, or push to a repo
- **Shopify**: Create test orders in a development store

## Step 3: Capture Webhooks

### View Logs

With `log_level: debug`, gatekeeperd logs request details:

```bash
# On bare metal
journalctl -u gatekeeperd -f

# On Kubernetes
kubectl logs -f deployment/gatekeeper-recorder
```

### Create a Recording File

From the logs, extract the headers and body. Create a recording file:

```json
{
  "metadata": {
    "provider": "slack",
    "event_type": "event_callback",
    "description": "Message posted in channel"
  },
  "request": {
    "method": "POST",
    "path": "/slack",
    "headers": {
      "Content-Type": "application/json",
      "X-Slack-Request-Timestamp": "1234567890",
      "X-Slack-Signature": "v0=abc123..."
    },
    "body": {
      "type": "event_callback",
      "event": {
        "type": "message",
        "text": "Hello world"
      }
    }
  },
  "signing_secret": "your-actual-signing-secret"
}
```

**Important**: Include the real signing secret so the recording can be used to verify the signature algorithm works.

### Capture Script (Optional)

For bulk capture, modify the echo server to save recordings:

```python
import json
import time
from http.server import HTTPServer, BaseHTTPRequestHandler

class RecordingHandler(BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get('Content-Length', 0))
        body = self.rfile.read(length)

        # Save recording
        recording = {
            "metadata": {
                "provider": "unknown",
                "event_type": "unknown",
                "recorded_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
                "description": "Auto-captured webhook"
            },
            "request": {
                "method": "POST",
                "path": self.path,
                "headers": dict(self.headers),
                "body": json.loads(body) if body else {}
            },
            "signing_secret": "REPLACE_WITH_REAL_SECRET"
        }

        filename = f"recording_{int(time.time())}.json"
        with open(filename, 'w') as f:
            json.dump(recording, f, indent=2)
        print(f"Saved: {filename}")

        # Respond
        self.send_response(200)
        self.end_headers()

HTTPServer(('', 8888), RecordingHandler).serve_forever()
```

## Step 4: Copy Recordings to Dev Environment

### From Bare Metal

```bash
scp user@server:/path/to/recording_*.json ./testdata/recordings/slack/event_callback/
```

### From Kubernetes

```bash
# Find the pod
kubectl get pods -l app=gatekeeper-recorder

# Copy files
kubectl cp gatekeeper-recorder-xxx:/path/to/recordings ./testdata/recordings/
```

### Organize Recordings

Place recordings in the standard directory structure:

```
testdata/recordings/
  {provider}/
    {event_type}/
      {descriptive_name}.json
```

Example:
```
testdata/recordings/
  slack/
    event_callback/
      message.json
      reaction_added.json
    url_verification/
      challenge.json
```

## Step 5: Develop the Verifier

### Test with Real Recording

```go
func TestSlackVerifier_RealPayload(t *testing.T) {
    rec, err := testutil.LoadRecording("slack", "event_callback", "message")
    if err != nil {
        t.Fatal(err)
    }

    verifier := NewSlackVerifier(rec.SigningSecret, 5*time.Minute)
    req, body := rec.ToHTTPRequest()

    err = verifier.Verify(req, body)
    if err != nil {
        t.Errorf("verification failed: %v", err)
    }
}
```

### Debug Signature Mismatches

If verification fails, add debug logging to understand the signature computation:

```go
// In your verifier, temporarily add:
fmt.Printf("Timestamp: %s\n", timestamp)
fmt.Printf("Body: %s\n", string(body))
fmt.Printf("Signature base: %s\n", signatureBase)
fmt.Printf("Expected: %s\n", expectedSignature)
fmt.Printf("Received: %s\n", receivedSignature)
```

Common issues:
- Timestamp format differences
- Body encoding (raw vs parsed JSON)
- Signature encoding (hex vs base64)
- Prefix format (e.g., Slack uses `v0=`)

### Generate Failure Test Cases

Use the recording mutation helpers:

```go
func TestSlackVerifier_Failures(t *testing.T) {
    rec, _ := testutil.LoadRecording("slack", "event_callback", "message")
    verifier := NewSlackVerifier(rec.SigningSecret, 5*time.Minute)

    tests := []struct {
        name string
        rec  *testutil.Recording
    }{
        {"invalid signature", rec.InvalidateSignature()},
        {"expired timestamp", rec.ExpireTimestamp(10 * time.Minute)},
        {"wrong secret", rec.WithSigningSecret("wrong-secret")},
        {"missing signature", rec.WithoutHeader("X-Slack-Signature")},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            req, body := tt.rec.ToHTTPRequest()
            err := verifier.Verify(req, body)
            if err == nil {
                t.Error("expected verification to fail")
            }
        })
    }
}
```

## Step 6: Develop the Validator (JSON Schema)

### Generate Schema from Real Payload

Examine real payloads to understand the structure:

```bash
cat testdata/recordings/slack/event_callback/message.json | jq '.request.body'
```

Create a JSON Schema that matches:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "slack/event_callback",
  "title": "Slack Event Callback",
  "type": "object",
  "required": ["type", "event"],
  "properties": {
    "type": {
      "type": "string",
      "const": "event_callback"
    },
    "event": {
      "type": "object",
      "required": ["type"],
      "properties": {
        "type": {"type": "string"},
        "text": {"type": "string"}
      }
    }
  }
}
```

### Test Schema Against Real Payloads

```go
func TestSlackSchema_RealPayload(t *testing.T) {
    rec, _ := testutil.LoadRecording("slack", "event_callback", "message")

    v, err := validator.NewJSONSchemaValidator(validator.JSONSchemaConfig{
        SchemaFile: "../../schemas/slack/event_callback.json",
    })
    if err != nil {
        t.Fatal(err)
    }

    err = v.Validate(rec.Request.Body)
    if err != nil {
        t.Errorf("validation failed: %v", err)
    }
}
```

### Iterate on Schema

If validation fails, the error message tells you what's wrong:

```
validation failed: missing properties: 'team_id'
```

Adjust your schema or make fields optional as needed.

## Step 7: Wire Into Gatekeeperd

Once verifier and validator work with real payloads:

1. Add verifier type to `internal/config/config.go`
2. Wire verifier in `internal/proxy/handler.go`
3. Add schema to `schemas/{provider}/`
4. Update documentation

See [CODING_STANDARDS.md](CODING_STANDARDS.md) for the full checklist.

## Step 8: Test End-to-End

Deploy gatekeeperd with your new verifier (not noop):

```yaml
verifiers:
  my-slack:
    type: slack
    signing_secret: "${SLACK_SIGNING_SECRET}"

validators:
  slack-event:
    type: json_schema
    schema_file: "schemas/slack/event_callback.json"

routes:
  - hostname: webhooks.yourdomain.com
    path: /slack
    verifier: my-slack
    validator: slack-event
    destination: http://your-actual-backend:8080
```

Send real webhooks and verify they're accepted/rejected correctly.

## Troubleshooting

### Webhook Not Reaching Gatekeeperd

- Check DNS resolution: `dig webhooks.yourdomain.com`
- Check TLS certificate: `curl -v https://webhooks.yourdomain.com/health`
- Check firewall rules (ports 80, 443)
- Check provider's webhook delivery logs

### Signature Verification Failing

- Ensure signing secret matches exactly (no trailing whitespace)
- Check timestamp is within acceptable range
- Verify body is raw bytes, not re-serialized JSON
- Compare your signature computation with provider's documentation

### Schema Validation Failing

- Use `jq` to examine real payload structure
- Check for optional fields that might be missing
- Verify field types (string vs number vs null)

## Security Notes

- **Never commit real signing secrets** to the repository
- Use placeholder secrets in test recordings, or ensure recordings use test/development credentials
- Rotate secrets after development if they were exposed
- Keep production secrets in environment variables or secret management systems
