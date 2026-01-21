# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed
- Helm chart default image repositories now point to GHCR (ghcr.io/tight-line/*)

### Removed
- Docker Hub workflow (images are published to GHCR only)

## [0.1.2] - 2026-01-21

### Changed
- Helm charts now publish only on version tags, not on every push to main

## [0.1.1] - 2026-01-21

### Added
- Relay client logs "connected to server" on successful connection
- Relay client logs "connection recovered" after recovering from failures
- Minikube testing guide with step-by-step instructions
- Minikube Helm values files for local development

### Fixed
- Docker security: non-root user with cap_net_bind_service
- SonarCloud security issues and code quality warnings
- Release workflow lowercase repository owner for GHCR

## [0.1.0] - 2025-01-18

### Added
- Initial release
- Webhook proxy server (gatekeeperd) with signature verification
- Support for Slack, GitHub, Shopify, and generic HMAC verification
- IP allowlist filtering with static CIDRs and dynamic fetching
- JSON Schema payload validation
- Direct forwarding to backend services
- Relay delivery for private networks
- Relay client (gatekeeper-relay) for polling and forwarding
- ACME TLS certificate management
- Prometheus metrics
- Helm charts for Kubernetes deployment
- Docker images for both components
