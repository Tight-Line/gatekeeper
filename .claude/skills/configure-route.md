# Configure Route Skill

This skill walks users through configuring a new webhook route for gatekeeper.

## Usage

Invoke with `/configure-route` or ask "I want to configure a webhook for [provider]"

## Instructions

When the user wants to configure a new webhook route, guide them through these steps interactively. Ask one question at a time and wait for their response before proceeding.

### Step 1: Identify the Provider

Ask which webhook provider they want to configure. Offer these options:

- **Slack** - Slack Events API, slash commands, interactive components
- **GitHub** - Repository webhooks, organization webhooks
- **Shopify** - Store webhooks (orders, products, customers)
- **Google Calendar** - Calendar push notifications
- **Generic HMAC** - Any provider using HMAC signatures
- **API Key** - Providers using simple header token authentication
- **Other** - Help them determine the best approach

### Step 2: External Hostname

Ask: "What hostname will [provider] send webhooks to?"

Example: `slack-webhooks.example.com` or `webhooks.mycompany.com`

This is the public DNS name that will receive webhook traffic.

### Step 3: Delivery Mode

Ask: "How should webhooks be delivered to your internal service?"

**Option A: Direct forwarding**
- Gatekeeperd forwards directly to your backend
- Requires a firewall rule allowing traffic from gatekeeperd to your internal service
- Lower latency, simpler setup if firewall access is available

**Option B: Relay client**
- A relay client inside your network polls gatekeeperd for webhooks
- No inbound firewall rules needed
- Only requires outbound HTTPS from your network to gatekeeperd

### Step 4: Internal Destination

Ask: "What is the internal URL where webhooks should be delivered?"

For direct mode: This is the full URL gatekeeperd will forward to.
For relay mode: This is the URL the relay client will forward to locally.

Suggest provider-specific defaults:
- Slack: `http://your-app:8080/webhooks/slack` or `/slack/events`
- GitHub: `http://your-app:8080/webhooks/github` or `/github/events`
- Shopify: `http://your-app:8080/webhooks/shopify`
- Google Calendar: `http://your-app:8080/webhooks/gcal` or `/calendar/notifications`

### Step 5: Generate Configuration

Based on their answers, generate the complete configuration.

#### For Direct Mode

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
    destination: {destination-url}
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
    relay_token: "${RELAY_TOKEN_{PROVIDER}}"
```

```yaml
# Add to gatekeeper-relay.yaml

channels:
  - name: {provider}-webhooks
    token: "${RELAY_TOKEN_{PROVIDER}}"
    destination: "{local-destination}"
```

### Step 6: Provider-Specific Setup Instructions

After generating the configuration, provide setup instructions specific to the provider.

#### Slack

1. Go to https://api.slack.com/apps and select your app
2. Navigate to "Event Subscriptions" (or "Interactivity & Shortcuts" for interactive components)
3. Set the Request URL to: `https://{hostname}{path}`
4. Copy the "Signing Secret" from "Basic Information"
5. Set the environment variable: `export SLACK_SIGNING_SECRET="your-signing-secret"`

Configuration uses:
```yaml
verifiers:
  slack:
    type: slack
    signing_secret: "${SLACK_SIGNING_SECRET}"
    max_timestamp_age: 5m  # Replay attack protection
```

Recommended IP allowlist: `aws` (Slack runs on AWS EC2)
```yaml
ip_allowlists:
  aws:
    fetch_url: "https://ip-ranges.amazonaws.com/ip-ranges.json"
    fetch_jq: '.prefixes[] | select(.service=="EC2") | .ip_prefix'
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

#### Generic HMAC

Ask the user for:
- Header name containing the signature
- Hash algorithm (SHA256 or SHA512)
- Encoding (hex or base64)
- Whether there's a signature prefix (e.g., "sha256=")

Configuration uses:
```yaml
verifiers:
  custom:
    type: hmac
    secret: "${WEBHOOK_SECRET}"
    header: "X-Signature"        # Header containing signature
    algorithm: sha256            # sha256 or sha512
    encoding: hex                # hex or base64
    prefix: "sha256="            # Optional prefix to strip
```

#### API Key

Ask the user for:
- Header name containing the token

Configuration uses:
```yaml
verifiers:
  custom:
    type: api_key
    header: "X-Webhook-Token"
    token: "${WEBHOOK_TOKEN}"
```

### Step 7: Environment Variables Summary

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
- After generating config, offer to help with additional routes
