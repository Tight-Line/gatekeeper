# Contributing to Gatekeeper

Thanks for your interest in contributing. This document covers the essentials;
the full development workflow is in [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md).

## What we welcome

- **New provider implementations** - See [docs/PROVIDER_TODO.md](docs/PROVIDER_TODO.md) for the wishlist
  and [docs/PROVIDER_DEVELOPMENT.md](docs/PROVIDER_DEVELOPMENT.md) for a step-by-step guide
- **Bug fixes** - If possible, include a test case that fails before your fix and passes after.
  We understand some bugs require a live third-party webhook to trigger; in those cases a
  clear reproduction description, or more ideally, a branch with a failing test case that
  illustrates the issue, is sufficient.
- **Performance improvements** - Include benchmarks showing the improvement
- **New features** - Open an issue first to discuss the design before writing code

## Before you open a PR

1. Read [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) for environment setup and build/test commands
2. Read [docs/CODING_STANDARDS.md](docs/CODING_STANDARDS.md) for code style and testing requirements
3. Run `make test` locally and confirm all tests pass with 100% coverage on changed packages
4. Run `make lint` and resolve any issues
5. Update docs if your change affects configuration, behavior, or the public API

## Pull request checklist

- [ ] Linked to the relevant issue (use `Fixes #NNN` or `Relates to #NNN`)
- [ ] Tests added or updated (where possible)
- [ ] `make test` passes locally
- [ ] `make lint` passes locally
- [ ] Documentation updated if needed

## A note on AI-assisted contributions

We use AI tools in our own development and welcome others who do the same. However,
PRs must demonstrate human understanding of the changes. Include clear motivation
explaining *why* the change is needed, not just what it does. Explain your testing
approach. Low-effort submissions that appear to be unreviewed AI output will be declined.
We value quality over quantity.

## Reporting bugs

Open a [bug report](https://github.com/Tight-Line/gatekeeper/issues/new?template=bug_report.md).

## Suggesting features

Open a [feature request](https://github.com/Tight-Line/gatekeeper/issues/new?template=feature_request.md).

## Security issues

Please do **not** open public issues for security vulnerabilities.
See [SECURITY.md](SECURITY.md) for responsible disclosure instructions.
