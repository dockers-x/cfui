# OAuth Refactor And Relay Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the Cloudflare OAuth redirect URL stable for all users, then split the OAuth frontend and localization into clearer modules without changing frameworks.

**Architecture:** Cloudflare receives one fixed Worker redirect URI. cfui encodes the current instance callback target into OAuth state, the Worker decodes it and returns the OAuth response to that cfui instance, and cfui verifies the same state from SQLite before exchanging the code. The frontend remains plain JavaScript served from `web/dist/js`, and i18n remains TOML served by `/api/i18n/{lang}` with added directory merge support.

**Tech Stack:** Go, SQLite/ent, Cloudflare OAuth + Workers, plain JavaScript, TOML locale files.

---

### Task 1: Fixed Worker Redirect URI

**Files:**
- Modify: `internal/cfoauth/service.go`
- Modify: `internal/cfoauth/config.go`
- Modify: `internal/server/oauth_handlers.go`
- Modify: `docs/cloudflare-oauth-worker.js`
- Modify: `README.md`
- Test: `internal/cfoauth/service_test.go`
- Test: `internal/server/oauth_handlers_test.go`

- [x] Encode the per-instance cfui callback URL into OAuth `state`.
- [x] Keep the token exchange `redirect_uri` equal to the fixed Worker callback URL.
- [x] Change the Worker to decode the callback URL from `state`.
- [x] Update docs and setup guide to say Cloudflare OAuth App redirect URI is the fixed Worker URL only.
- [x] Verify with Go OAuth tests; LAN runtime update follows after commit.

### Task 2: OAuth Frontend Module Split

**Files:**
- Create: `web/dist/js/app-oauth-data.js`
- Create: `web/dist/js/app-oauth-setup.js`
- Modify: `web/dist/js/app-oauth.js`
- Modify: `web/dist/index.html`

- [x] Move OAuth resource/scope/static option definitions into `app-oauth-data.js`.
- [x] Load `app-oauth-data.js` before `app-oauth.js`.
- [x] Move OAuth relay callback and setup-guide UI rendering into `app-oauth-setup.js`.
- [x] Load `app-oauth-setup.js` before `app-oauth.js`.
- [x] Keep all existing global function wiring intact.
- [x] Verify every JS file with `node --check`.

### Task 3: Directory-Based i18n Merge

**Files:**
- Modify: `internal/server/server.go`
- Create: `locales/en/oauth.toml`
- Create: `locales/zh/oauth.toml`
- Create: `locales/ja/oauth.toml`
- Modify: `locales/en.toml`
- Modify: `locales/zh.toml`
- Modify: `locales/ja.toml`

- [x] Make `/api/i18n/{lang}` load the legacy file plus every `locales/{lang}/*.toml`.
- [x] Move OAuth keys into `locales/{lang}/oauth.toml`.
- [x] Add a key parity check across English, Chinese, and Japanese.

### Task 4: Verification And LAN Update

**Files:**
- No new source files.

- [x] Run JS syntax checks.
- [x] Run Go tests for OAuth/server/i18n touched packages.
- [x] Run `make test`.
- [ ] Commit and push the branch.
- [ ] Rebuild and restart the LAN instance at `http://10.10.68.168:14333/cloudflare`.
