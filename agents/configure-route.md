---
name: configure-route
description: Interactively configure a new webhook route for gatekeeper
---

# Configure Route Skill

This skill walks users through configuring a new webhook route for gatekeeper.

## Usage

- **Claude Code**: `/configure-route`
- **Any AI assistant**: "I want to configure a webhook for [provider]"

## Instructions

When the user wants to configure a new webhook route, guide them through these steps interactively. Ask one question at a time and wait for their response before proceeding.

### Step 1: Identify the Provider

Ask which webhook provider they want to configure. Offer these options:

- **Slack** - Slack Events API, slash commands, interactive components
- **GitHub** - Repository webhooks, organization webhooks
- **GitLab** - Repository/project webhooks, group webhooks
- **Shopify** - Store webhooks (orders, products, customers)
- **SendGrid** - Event Webhook (email delivery/engagement events)
- **Google Calendar** - Calendar push notifications (X-Goog-Channel-Token header)
- **Google Chat** - Chat app webhooks (OIDC JWT bearer token)
- **Microsoft Graph** - Outlook Calendar, OneDrive change notifications (token in JSON body)
- **Azure Event Grid (AAD)** - Azure Event Grid with Azure Active Directory authentication
- **Generic HMAC** - Any provider using HMAC signatures (configurable algorithm/encoding)
- **API Key (Header)** - Providers using simple header token authentication
- **Query Parameter Token** - Providers that send a token in the URL query string
- **Header Query Parameter** - Providers that encode key=value pairs inside a header
- **Other** - Help them determine the best approach

### Step 2: External Hostname

Ask: "What hostname will [provider] send webhooks to?"

Example: `slack-webhooks.example.com` or `webhooks.mycompany.com`

This is the public DNS name that will receive webhook traffic.

### Step 3: Path

Ask: "What path should this route match?"

Default suggestion: `/` (matches all paths)

Explain: Routes use segment-aware prefix matching. A route with path `/hooks` matches `/hooks` and `/hooks/github` but NOT `/hookshot`.

### Step 4: Delivery Mode

Ask: "How should webhooks be delivered to your internal service?"

**Option A: Direct forwarding**
- Gatekeeperd forwards directly to your backend
- Requires a firewall rule allowing traffic from gatekeeperd to your internal service
- Lower latency, simpler setup if firewall access is available

**Option B: Relay client**
- A relay client inside your network polls gatekeeperd for webhooks
- No inbound firewall rules needed
- Only requires outbound HTTPS from your network to gatekeeperd

### Step 5: Internal Destination

Ask: "What is the internal URL where webhooks should be delivered?"

For direct mode: This is the full URL gatekeeperd will forward to.
For relay mode: This is the URL the relay client will forward to locally.

Suggest provider-specific defaults:
- Slack: `http://your-app:8080/webhooks/slack` or `/slack/events`
- GitHub: `http://your-app:8080/webhooks/github` or `/github/events`
- GitLab: `http://your-app:8080/webhooks/gitlab` or `/gitlab/events`
- Shopify: `http://your-app:8080/webhooks/shopify`
- Google Calendar: `http://your-app:8080/webhooks/gcal` or `/calendar/notifications`
- Microsoft Graph: `http://your-app:8080/webhooks/graph` or `/graph/notifications`

### Step 6: Additional Options

Ask about optional configuration:

**IP Allowlist**: "Do you want to restrict requests by source IP?"
- Recommend allowlists for the provider (aws for Slack, github for GitHub, google for Google Calendar, etc.)
- Explain: IP allowlists add defense-in-depth but are optional since signature verification is the primary authentication

**Preserve Host Header**: "Should the original Host header be passed to your backend?"
- Default: No (destination hostname is used)
- Enable if: Backend needs to see the original public hostname

**Rate Limiting**: "Do you want to rate limit this route?"
- Protects against abuse with token bucket algorithm
- Configure total RPS (across all IPs) and per-IP RPS
- Returns HTTP 429 with Retry-After header when exceeded
- Reference a named rate limiter or use the global default

**Payload Validation**: "Do you want to validate the payload structure with JSON Schema?"
- Optional defense-in-depth against malformed payloads
- Pre-built schemas available for common providers in `schemas/` directory

### Step 7: Generate Configuration

Based on their answers, generate the complete configuration.

#### For Direct Mode

```yaml
# Add to gatekeeperd.yaml

verifiers:
  {verifier-name}:
    {provider-specific-config}

# Optional: Add validator if requested
validators:
  {validator-name}:
    type: json_schema
    schema_file: "schemas/{provider}/{event_type}.json"

routes:
  - hostname: {hostname}
    path: {path}
    ip_allowlist: {recommended-allowlist}  # if applicable
    verifier: {verifier-name}
    validator: {validator-name}            # if applicable
    rate_limiter: {limiter-name}           # if applicable
    destination: {destination-url}
    preserve_host: {true/false}            # if enabled
```

#### For Relay Mode

Generate both server and client configs:

```yaml
# Add to gatekeeperd.yaml

verifiers:
  {verifier-name}:
    {provider-specific-config}

routes:
  - hostname: {hostname}
    path: {path}
    ip_allowlist: {recommended-allowlist}  # if applicable
    verifier: {verifier-name}
    rate_limiter: {limiter-name}           # if applicable
    relay_token: "${RELAY_TOKEN_{PROVIDER}}"
```

```yaml
# Add to gatekeeper-relay.yaml

channels:
  - name: {provider}-webhooks
    token: "${RELAY_TOKEN_{PROVIDER}}"
    destination: "{local-destination}"
    workers: 1  # Increase for high-volume webhooks
    # preserve_path: false  # Set if destination is the exact URL; omit to append the original webhook path
```

**Ask:** "Should the original webhook path be appended to the destination URL?"

By default the relay appends the incoming webhook path to the destination (e.g., destination `http://svc:8080/app` + webhook path `/hook` → `http://svc:8080/app/hook`). Add `preserve_path: false` when the destination is already the complete URL and path-appending would break routing.

### Step 8: Provider-Specific Setup Instructions

After generating the configuration, provide setup instructions specific to the provider.

#### Slack

1. Go to https://api.slack.com/apps and select your app
2. Navigate to "Event Subscriptions" (or "Interactivity & Shortcuts" for interactive components)
3. Set the Request URL to: `https://{hostname}{path}`
4. Copy the "Signing Secret" from "Basic Information"
5. Set the environment variable: `export SLACK_SIGNING_SECRET="your-signing-secret"`

**Note**: Gatekeeper automatically handles Slack URL verification challenges. When Slack sends a `url_verification` request during webhook setup, gatekeeper responds immediately with the challenge - your backend does not need to handle this.

Configuration uses:
```yaml
verifiers:
  slack:
    type: slack
    signing_secret: "${SLACK_SIGNING_SECRET}"
    max_timestamp_age: 5m  # Replay attack protection (default: 5m)
```

Recommended IP allowlist: `aws` (Slack runs on AWS)
```yaml
ip_allowlists:
  aws:
    fetch_url: "https://ip-ranges.amazonaws.com/ip-ranges.json"
    fetch_jq: ".prefixes[].ip_prefix"
    refresh_interval: 24h
```

#### GitHub

1. Go to your repository or organization settings
2. Navigate to "Webhooks" and click "Add webhook"
3. Set Payload URL to: `https://{hostname}{path}`
4. Set Content type to: `application/json`
5. Generate a secret and enter it in the "Secret" field
6. Set the environment variable: `export GITHUB_WEBHOOK_SECRET="your-secret"`

Configuration uses:
```yaml
verifiers:
  github:
    type: github
    secret: "${GITHUB_WEBHOOK_SECRET}"
```

Recommended IP allowlist: GitHub publishes their IP ranges at https://api.github.com/meta
```yaml
ip_allowlists:
  github:
    fetch_url: "https://api.github.com/meta"
    fetch_jq: ".hooks[]"
    refresh_interval: 24h
```

#### GitLab

1. Go to your GitLab project/group settings
2. Navigate to Settings > Webhooks
3. Click "Add new webhook"
4. Set the URL to: `https://{hostname}{path}`
5. Enter a secret token in the "Secret token" field
6. Select the events you want to trigger the webhook
7. Set the environment variable: `export GITLAB_WEBHOOK_TOKEN="your-secret-token"`

Configuration uses:
```yaml
verifiers:
  gitlab:
    type: gitlab
    token: "${GITLAB_WEBHOOK_TOKEN}"
```

Recommended IP allowlist (GitLab.com):
```yaml
ip_allowlists:
  gitlab:
    cidrs:
      - "34.74.90.64/28"
      - "34.74.226.0/24"
```

Note: Self-hosted GitLab instances will have different IP addresses. Check your instance's outbound IP or skip IP allowlisting.

#### Shopify

1. Go to your Shopify admin panel
2. Navigate to Settings > Notifications > Webhooks
3. Click "Create webhook"
4. Select the event and set the URL to: `https://{hostname}{path}`
5. Note the webhook signing secret shown after creation
6. Set the environment variable: `export SHOPIFY_WEBHOOK_SECRET="your-secret"`

Configuration uses:
```yaml
verifiers:
  shopify:
    type: shopify
    secret: "${SHOPIFY_WEBHOOK_SECRET}"
```

#### SendGrid Event Webhook

SendGrid signs Event Webhook deliveries with ECDSA P-256. The signed content is `timestamp + raw_body`, the signature header carries the base64-encoded ASN.1 DER signature, and the timestamp travels in a sibling header.

1. In the SendGrid app, enable signature verification under **Settings -> Mail Settings -> Event Webhook -> Signed Event Webhook Requests**
2. Copy the displayed public verification key (base64-encoded DER) or convert it to PEM
3. Set the environment variable: `export SENDGRID_WEBHOOK_PUBLIC_KEY="MFkwEwYHKoZIzj0CAQYI..."`
4. Point the Event Webhook URL at: `https://{hostname}{path}`

Configuration uses:
```yaml
verifiers:
  sendgrid:
    type: sendgrid
    public_key: "${SENDGRID_WEBHOOK_PUBLIC_KEY}"
    max_timestamp_age: 5m  # optional replay-protection window; omit/0 disables
```

IP allowlist: SendGrid does **not** publish stable IP ranges for Event Webhook traffic, so omit `ip_allowlist` and rely on signature verification.

#### Google Calendar

1. Use the Google Calendar API to create a watch request
2. Set the `address` to: `https://{hostname}{path}`
3. Set a `token` value in your watch request (this is your shared secret)
4. Set the environment variable: `export GCAL_CHANNEL_TOKEN="your-token"`

Configuration uses:
```yaml
verifiers:
  gcal:
    type: api_key
    header: "X-Goog-Channel-Token"
    token: "${GCAL_CHANNEL_TOKEN}"
```

Recommended IP allowlist:
```yaml
ip_allowlists:
  google:
    fetch_url: "https://www.gstatic.com/ipranges/goog.json"
    fetch_jq: ".prefixes[].ipv4Prefix"
    refresh_interval: 24h
```

#### Microsoft Graph (Outlook Calendar, OneDrive, etc.)

Microsoft Graph change notifications embed the verification token in the JSON body, not a header.

1. When creating a subscription, set the `clientState` property to include your verification token
2. Use a JSON structure like: `{"tpVerificationToken":"your-secret","routing":"data"}`
3. Set the environment variable: `export MS_GRAPH_CLIENT_STATE="your-secret"`
4. Set the notification URL to: `https://{hostname}{path}`

Configuration uses:
```yaml
verifiers:
  ms-graph:
    type: json_field
    path: "value.0.clientState.tpVerificationToken"  # Path to token in JSON body
    token: "${MS_GRAPH_CLIENT_STATE}"
```

Path syntax:
- Uses dot notation for nested fields
- Array indices are numbers (e.g., `value.0` for first element)
- Auto-parses JSON strings (if `clientState` is `{"tpVerificationToken":"x"}`, the path extracts `x`)

**Note:** Gatekeeper automatically handles Microsoft Graph subscription validation. When creating or renewing a subscription, Graph sends a POST with `validationToken` as a query parameter and an empty body. Gatekeeper detects this on `json_field` verifier routes and responds immediately with the token value, allowing subscription setup without backend involvement.

Recommended IP allowlist:
```yaml
ip_allowlists:
  microsoft-graph:
    cidrs:
      - "20.20.32.0/19"
      - "20.190.128.0/18"
      - "20.231.128.0/19"
      - "40.126.0.0/18"
```

#### Google Chat

Google Chat signs webhook requests with an RS256 JWT bearer token. There are two audience modes; ask the user which one their Chat app is configured to use.

**App URL audience mode** (recommended for new apps):

1. In the Google Cloud Console, configure the Chat app with **Authentication Audience: HTTP endpoint URL**
2. Set the environment variables:
   - `export GCP_PROJECT_NUMBER="your-project-number"`
   - The audience is the exact webhook URL: `https://{hostname}{path}`

Configuration uses:
```yaml
verifiers:
  google-chat:
    type: oidc
    issuer: "https://accounts.google.com"
    audience: "https://{hostname}{path}"
    # jwks_uri omitted: auto-discovered from Google OIDC discovery document
    claims:
      email: "service-${GCP_PROJECT_NUMBER}@gcp-sa-gsuiteaddons.iam.gserviceaccount.com"
```

**Project number audience mode** (legacy):

1. Configure the Chat app with **Authentication Audience: Project number**
2. Set the environment variable: `export GCP_PROJECT_NUMBER="your-project-number"`

Configuration uses:
```yaml
verifiers:
  google-chat:
    type: oidc
    issuer: "chat@system.gserviceaccount.com"
    audience: "${GCP_PROJECT_NUMBER}"
    jwks_uri: "https://www.googleapis.com/service_accounts/v1/metadata/x509/chat@system.gserviceaccount.com"
```

IP allowlist: Google Chat does not publish stable webhook source IPs; rely on JWT signature verification.

#### Azure Event Grid (AAD)

Azure Event Grid can deliver webhooks with Azure Active Directory (AAD) authentication. Gatekeeper verifies the RS256 JWT bearer token issued by your AAD tenant.

1. Configure Event Grid to use AAD authentication, specifying your app registration as the target audience
2. Set the environment variables:
   - `export AZURE_TENANT_ID="your-tenant-id"`
   - `export AZURE_APP_ID_URI="api://your-app-id"` (the audience value you registered)
3. Set the Event Grid subscription endpoint to: `https://{hostname}{path}`

Configuration uses:
```yaml
verifiers:
  azure-eventgrid:
    type: oidc
    issuer: "https://sts.windows.net/${AZURE_TENANT_ID}/"
    audience: "${AZURE_APP_ID_URI}"
    # jwks_uri omitted: auto-discovered from AAD tenant metadata
```

IP allowlist: Azure publishes IP ranges but they are large and change frequently; rely on JWT signature verification.

#### Generic HMAC

Ask the user for:
- Header name containing the signature
- Hash algorithm (SHA256 or SHA512)
- Encoding (hex or base64)

Configuration uses:
```yaml
verifiers:
  custom-hmac:
    type: hmac
    secret: "${WEBHOOK_SECRET}"
    header: "X-Signature"        # Header containing signature
    hash: SHA256                 # SHA256 or SHA512
    encoding: hex                # hex or base64
```

#### API Key (Header)

Ask the user for:
- Header name containing the token

Configuration uses:
```yaml
verifiers:
  custom-apikey:
    type: api_key
    header: "X-Webhook-Token"
    token: "${WEBHOOK_TOKEN}"
```

#### Query Parameter Token

Some providers send a verification token as a URL query parameter (e.g., `?token=secret`).

Ask the user for:
- Query parameter name (e.g., `token`, `verify`, `secret`)

Configuration uses:
```yaml
verifiers:
  url-token:
    type: query_param
    name: "token"           # Query parameter name
    token: "${WEBHOOK_URL_TOKEN}"
```

#### Header Query Parameter

Some providers encode multiple key=value pairs inside a header (e.g., `X-Custom-Header: key1=value1&key2=value2`).

Ask the user for:
- Header name
- Key name within the header

Configuration uses:
```yaml
verifiers:
  header-param:
    type: header_query_param
    header: "X-Goog-Channel-Token"  # Header containing the encoded parameters
    name: "secret"                   # Key name to extract
    token: "${CHANNEL_SECRET}"
```

### Step 9: Environment Variables Summary

At the end, summarize all environment variables that need to be set:

```bash
# Required environment variables for this configuration:
export {VAR_NAME}="your-value-here"

# For relay mode, also set:
export RELAY_TOKEN_{PROVIDER}="generate-a-secure-random-token"
```

Remind them:
- Never commit secrets to version control
- Use Kubernetes Secrets or a secret manager in production
- For relay tokens, generate a secure random value (e.g., `openssl rand -hex 32`)

### Conversation Style

- Be concise and direct
- Ask one question at a time
- Provide sensible defaults where possible
- Explain trade-offs when relevant (e.g., direct vs relay mode)
- Use code blocks for all configuration snippets
- After generating config, offer to help with additional routes or a complete Helm deployment (see `configure-helm` skill)
