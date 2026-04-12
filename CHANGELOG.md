# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased] — feat/leonglobal-hardening

### Added
- `gcp-bridge/bootstrap.sh` — automated VM setup script for GCP deployment
  (Docker, Caddy, GCP Secret Manager integration, docker-compose).
- `gcp-bridge/DEPLOY-RUNBOOK.md` — 10-step deployment guide for
  `bridge.leon-global.com`.
- `whatsapp-bridge/middleware.go` — bearer auth middleware (`BRIDGE_API_TOKEN`),
  HMAC payload signing (`WEBHOOK_SECRET`), webhook retry with exponential backoff.
- `whatsapp-bridge/Dockerfile` — multi-stage build (golang:1.24-alpine → alpine:3.20)
  with FFmpeg, SQLite, and tzdata.

### Changed
- `webhook.go` — outbound webhooks now signed with HMAC-SHA256 when
  `WEBHOOK_SECRET` is set. Adds `X-Signature: sha256=<hex>` header.
- `webhook.go` — replaced `http.DefaultClient` (no timeout) with a dedicated
  `webhookClient` with 10-second timeout.
- `main.go` — HTTP server binds to `127.0.0.1:<port>` instead of `0.0.0.0`
  (Caddy reverse proxy handles external traffic).
- `main.go` — message content replaced with `len(content)` in log output (privacy).
- `main.go` — `media_path` validated with `filepath.Clean` + `..` segment
  rejection before `os.ReadFile` (path traversal fix).

### Fixed
- `middleware.go` — aligned env var names: `BRIDGE_AUTH_TOKEN` → `BRIDGE_API_TOKEN`,
  `WEBHOOK_SIGNING_SECRET` → `WEBHOOK_SECRET` to match `main.go`, `webhook.go`,
  and deployment scripts.

### Security
- Bearer token authentication on all `/api/*` endpoints except `/api/health`.
- Path traversal prevention on media file access.
- HMAC-SHA256 webhook signing for downstream verification.
- Server bound to localhost only — external access via Caddy with TLS.

---

## 0.1.0 (2026-03-02)

### Added

**Go Bridge:**

- `/api/typing` endpoint - Send typing indicators to chats
- `/api/health` endpoint - Check WhatsApp connection status
- Webhook system for incoming messages (`webhook.go`)
  - Configurable via `WEBHOOK_URL` environment variable
  - Includes quoted message info (reply context)
  - `FORWARD_SELF` option to include self-sent messages
- Auto-download of media files when messages arrive
- Quoted message extraction (reply-to context: ID, sender, content)
- Connection status checks before API operations
- HTTP server timeouts for stability (read: 30s, write: 60s, idle: 120s)

**Python MCP Server:**

- `get_contact` tool - Resolve phone number to contact name
- `sender_display` field in messages - Shows "Name (phone)" format
- `sender_phone` field - Extracted phone number from JID
- Environment variable configuration (`WHATSAPP_DB_PATH`, `WHATSAPP_API_URL`)
- Test suite with pytest (8 tests)
- `sort_by` parameter for `list_messages` ("newest" or "oldest")
- Increased default limits (20 → 50 messages/chats)
- Maximum limits (500 messages, 200 chats)

**Infrastructure:**

- CI/CD pipeline with GitHub Actions (lint, build, test)
- Release Please workflow for automated release PRs/tags/changelog (`.github/workflows/release-please.yml`)
- Manual fallback release workflow for artifact re-publish (`.github/workflows/release.yml`)
- Version consistency check across `pyproject.toml`, `server.json`, and release tags
- Comprehensive documentation (README, CLAUDE.md)
- Maintainer release playbook (`docs/RELEASING.md`)
- Environment variable examples (`.env.example`)

### Fixed

- Go compilation errors from whatsmeow API changes (added `context.Background()`)
- Media filename consistency - now uses message timestamp instead of download time
- Startup migration now consolidates legacy `@lid` chat/message rows into mapped phone JIDs (`@s.whatsapp.net`) to prevent split chat history
- golangci.yml configuration (removed deprecated linters)
- Python linting issues (185 errors fixed)
- Trailing whitespace in SQL queries

### Changed

- Upgraded whatsmeow to latest version (v0.0.0-20260107124630)
- Upgraded Go linting pipeline to golangci-lint v2 (CI action + v2 config schema)
- Modernized Python type hints (`Optional[str]` → `str | None`)
- Improved docstrings with date format examples
- Media download uses message timestamp for consistent filenames
- Refactored `send_message` to use unified `recipient` parameter
- Adopted Release Please for automated versioning/changelog ([`#15`](https://github.com/verygoodplugins/whatsapp-mcp/issues/15)) ([6d45958](https://github.com/verygoodplugins/whatsapp-mcp/commit/6d45958139effa3079ff27a9708d400f89ba9ddf))

### Removed

- Unused `package.json` from Go bridge (was for wrangler, not needed)
- Compiled binary from git tracking

---

## Fork History

This project is a fork of [lharries/whatsapp-mcp](https://github.com/lharries/whatsapp-mcp), originally created by [Luke Harries](https://github.com/lharries) in 2024.

The original repository was last updated in April 2025. This fork by [Very Good Plugins](https://verygoodplugins.com) continues active development with bug fixes, new features, and MCP registry publication.
