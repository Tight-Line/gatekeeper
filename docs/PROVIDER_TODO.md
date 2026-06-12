# Provider TODO

Webhook providers we want to support in the future. Contributions welcome.

## Already Implemented

| Provider | Category | Verifier Type |
|----------|----------|---------------|
| Slack | Communication | `slack` |
| GitHub | DevOps | `github` |
| GitLab | DevOps | `gitlab` |
| Shopify | E-commerce | `shopify` |
| Google Calendar | Productivity | `api_key` |
| Google Chat | Communication | `oidc` |
| Azure Event Grid (AAD) | Cloud | `oidc` |
| Microsoft Graph | Productivity | `json_field` |
| SendGrid | Email | `sendgrid` |
| Generic HMAC | Any | `hmac` |
| Generic API Key | Any | `api_key` |
| Generic Query Param | Any | `query_param` |
| Generic Header Query Param | Any | `header_query_param` |

## Priority 1: High Demand

Well-documented APIs with straightforward signature schemes.

| Provider | Category | Signature Method | Docs |
|----------|----------|------------------|------|
| Stripe | Payments | HMAC-SHA256 with timestamp | [link](https://stripe.com/docs/webhooks/signatures) |
| Twilio | Communication | HMAC-SHA1 of URL + params | [link](https://www.twilio.com/docs/usage/webhooks/webhooks-security) |
| PagerDuty | Ops | HMAC-SHA256 | [link](https://developer.pagerduty.com/docs/webhooks/v3-overview/) |
| Linear | Project Mgmt | HMAC-SHA256 | [link](https://developers.linear.app/docs/graphql/webhooks) |
| Discord | Communication | Ed25519 signature | [link](https://discord.com/developers/docs/interactions/receiving-and-responding) |

## Priority 2: Common

Widely used services with moderately complex verification.

| Provider | Category | Signature Method | Docs |
|----------|----------|------------------|------|
| Bitbucket | DevOps | HMAC-SHA256 or SHA512 | [link](https://support.atlassian.com/bitbucket-cloud/docs/manage-webhooks/) |
| Jira | Project Mgmt | Asymmetric (JWT-like) | [link](https://developer.atlassian.com/cloud/jira/platform/webhooks/) |
| Microsoft Teams | Communication | HMAC-SHA256 | [link](https://learn.microsoft.com/en-us/microsoftteams/platform/webhooks-and-connectors/how-to/add-outgoing-webhook) |
| Zendesk | Support | HMAC-SHA256 | [link](https://developer.zendesk.com/documentation/webhooks/verifying/) |
| HubSpot | CRM | HMAC-SHA256 v1/v2 | [link](https://developers.hubspot.com/docs/api/webhooks) |
| Datadog | Monitoring | HMAC-SHA256 | [link](https://docs.datadoghq.com/integrations/webhooks/) |
| CircleCI | CI/CD | Shared secret header | [link](https://circleci.com/docs/webhooks/) |
| Calendly | Scheduling | HMAC-SHA256 | [link](https://developer.calendly.com/api-docs/docs/webhook-signatures) |
| Typeform | Forms | HMAC-SHA256 | [link](https://www.typeform.com/developers/webhooks/secure-your-webhooks/) |

## Priority 3: Niche or Complex

Less common or requiring complex verification schemes.

| Provider | Category | Signature Method | Notes |
|----------|----------|------------------|-------|
| PayPal | Payments | Certificate-based | Requires fetching PayPal certs |
| Salesforce | CRM | Org-specific | Complex org validation |
| AWS SNS | Cloud | Certificate-based | X.509 signature verification |
| Azure Event Grid | Cloud | SAS token or AAD (`oidc`) | AAD mode supported via oidc verifier; SAS token not yet implemented |
| Okta | Identity | HMAC-SHA256 | [link](https://developer.okta.com/docs/concepts/event-hooks/) |
| Auth0 | Identity | HMAC-SHA256 | [link](https://auth0.com/docs/customize/hooks) |
| Zoom | Communication | HMAC-SHA256 | [link](https://developers.zoom.us/docs/api/rest/webhook-reference/) |
| Asana | Project Mgmt | HMAC-SHA256 | [link](https://developers.asana.com/docs/webhooks) |
| Square | Payments | HMAC-SHA256 | [link](https://developer.squareup.com/docs/webhooks/step3validate) |
| Notion | Productivity | None | IP allowlist only |
| Airtable | Productivity | HMAC-SHA256 | [link](https://airtable.com/developers/web/api/webhooks-overview) |
| New Relic | Monitoring | Custom headers | Basic auth or custom |
| Monday.com | Project Mgmt | HMAC-SHA256 | [link](https://developer.monday.com/api-reference/docs/webhooks) |

## Community Requested

Add providers here that users have requested.

| Provider | Category | Requested By | Notes |
|----------|----------|--------------|-------|
| | | | |

## Contributing a New Provider

See AGENTS.md for the workflow to add a new verifier:

1. Create `internal/verifier/{provider}.go` implementing the `Verifier` interface
2. Create `internal/verifier/{provider}_test.go` with table-driven tests
3. Add the verifier type to `internal/config/config.go` validation
4. Wire it up in `internal/proxy/handler.go`
5. Add example configuration to `config/example.yaml`
6. Update this file to move the provider to "Already Implemented"
