# Gatekeeper Agent Skills

This directory contains AI skills for interactive gatekeeper configuration. These skills work with any AI assistant that can follow structured instructions.

## Available Skills

| Skill | File | Claude Code |
|-------|------|-------------|
| Configure Route | [`configure-route.md`](configure-route.md) | `/configure-route` |
| Configure Helm | [`configure-helm.md`](configure-helm.md) | `/configure-helm` |

## Usage

**With Claude Code**: Invoke skills with slash commands (`/configure-route`, `/configure-helm`)

**With any AI assistant**: Either provide the skill file as context, or simply ask:
- "I want to configure a webhook for Slack"
- "Help me deploy gatekeeper to Kubernetes"

## Skill Structure

Each skill file contains:

1. **Usage** - How to invoke the skill
2. **Instructions** - Step-by-step flow for Claude to follow
3. **Provider-specific guidance** - Configuration examples and setup instructions
4. **Generated output** - Templates for the configuration files

## Claude Code Integration

These skills are symlinked from `.claude/skills/` for Claude Code slash command support:

```
.claude/skills/configure-route.md -> ../../agents/configure-route.md
.claude/skills/configure-helm.md -> ../../agents/configure-helm.md
```

The canonical versions live here in `agents/` and are tracked in version control.
