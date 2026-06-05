# S3 WebDAV Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add multi-mount S3 sync from the existing S3 WebDAV file browser.

**Architecture:** Keep sync in `internal/s3dav.Service` so it reuses the existing mount validation and filesystem abstraction. Expose an async job API under `/api/s3/files/sync`, then add a modal workflow in `web/dist/js/app-s3.js` that polls job progress.

**Tech Stack:** Go, `afero`, standard `net/http`, vanilla HTML/CSS/JS, TOML locale files.

---

### Task 1: Backend Sync Contract

**Files:**
- Modify: `internal/s3dav/types.go`
- Modify: `internal/server/server.go`

- [ ] Add `SyncRequest`, `SyncResponse`, `SyncTargetResult`, and `SyncJobResponse`.
- [ ] Register `POST /api/s3/files/sync` and `GET /api/s3/files/sync/{job_id}` before the `/api/s3/files/` object route.
- [ ] Decode JSON, call `s.s3Svc.StartSync`, and return the initial job state.
- [ ] Add a job status handler that calls `s.s3Svc.SyncJob`.

### Task 2: Sync Service

**Files:**
- Modify: `internal/s3dav/service.go`
- Modify: `internal/s3dav/fs.go`
- Test: `internal/s3dav/service_test.go`

- [ ] Add tests for single file sync, skip existing target, overwrite existing target, recursive folder sync, root sync, invalid target mount, and job progress state.
- [ ] Implement `Service.Sync` for synchronous execution.
- [ ] Implement `Service.StartSync` and `Service.SyncJob` for UI progress polling.
- [ ] Use existing `Filesystem`, `listFiles`, `openFile`, and `writeFile`.
- [ ] Preserve relative paths from source to destination.
- [ ] Treat `/` as the recursive visible mount root.

### Task 3: Frontend Sync Flow

**Files:**
- Modify: `web/dist/index.html`
- Modify: `web/dist/js/app-s3.js`
- Modify: `web/dist/style.css`
- Modify: `locales/en.toml`
- Modify: `locales/zh.toml`
- Modify: `locales/ja.toml`

- [ ] Add the sync modal markup.
- [ ] Add toolbar and row sync icon buttons.
- [ ] Render a two-pane source/target bucket tree with a fixed left-to-right direction indicator.
- [ ] Default the source tree to the active mount and the current folder or row path.
- [ ] Default the target tree to the first other enabled and ready mount.
- [ ] Let users select a target directory and show the computed final target path.
- [ ] Render conflict options.
- [ ] Confirm overwrite before submitting.
- [ ] Submit to `/api/s3/files/sync`, poll `/api/s3/files/sync/{job_id}`, show current copy progress and per-target results, and refresh the active file list.

### Task 4: Verification

**Files:**
- Test: Go packages and frontend JS.

- [ ] Run `gofmt` on touched Go files.
- [ ] Run `node --check web/dist/js/app-s3.js`.
- [ ] Run `go test -count=1 ./internal/s3dav ./internal/server`.
- [ ] Run `go test -count=1 ./...`.
- [ ] Run `go build ./...`.
- [ ] Run `git diff --check`.
