# Tunnel connectivity and recovery

Approved scope: fix the recovery assessment of 2026-10-29, then review using
`go-best-practices`, `go-design-patterns`, and `chao-go-sync`; after review and
verification, commit and push the next stable patch tag.

Initial baseline: `72c9c39` / `v0.10.8`. Before release, integrated
`842de30` / `v0.10.9` (automatic cloudflared 2026.10.6 update);
this fix will use the next available patch tag.

- [x] Separate lifecycle from connectivity in runtime snapshots, Web API, MCP,
  and the bilingual UI. Expose active/expected connections and observation
  times. Backend polling failure makes observations stale, not disconnected.
- [x] Keep readiness monitoring when auto-restart is off. Validate readiness
  responses and connector identity. Distinguish confirmed zero connections from
  probe errors. Preserve startup grace and consecutive-failure hysteresis.
- [x] Preserve upstream `auto` protocol selection instead of forcing QUIC.
- [x] Keep jittered recovery attempts through long outages: after ten fast
  attempts, retry every 5–6 minutes. Reset only after confirmed sustained
  connectivity or a new explicit start. Stop must cancel timers and invalidate
  old runs. A stopped or previous run must not update a newer run's health.
- [x] Verify unhealthy→healthy, transient failures, malformed/wrong-instance
  responses, startup, stop/restart races, slow retries, and API/UI consistency.
- [x] Run the three requested skill reviews, resolve actionable findings, then
  run `go test -race ./...`, `go vet ./...`, the frontend behavior tests, and
  a CGO-disabled release build.
- [ ] Update release notes/documentation, commit, atomically push main and the
  next stable patch tag, and confirm the release workflow starts successfully.

This change keeps cloudflared embedded. A blocked upstream shutdown is reported
as an unrecoverable stopping condition; forcibly killing an arbitrary goroutine
or terminating the whole application is outside this scope. Edge readiness
reports tunnel connections, not the health of an origin application.

UI refinement approved during implementation: use the original PNG artwork from
`react-animals`, with green for healthy, yellow for warning/recovery, red for
errors, and gray for unknown. Show a peeking animal by default and reveal the
whole animal on hover, keyboard focus, or touch; no visible health label. Keep
accessible status descriptions and honor reduced motion.

The user confirmed native HTML/CSS/JavaScript and original PNG assets. Assets
are vendored into `web/dist`, embedded in the Go binary, and served locally. No
React or npm dependency is required at runtime.

Verification: full `go test -race ./...` and `go vet ./...` passed with
cloudflared 2026.10.6; 9 frontend behavior tests, 19 release automation tests,
actionlint, embedded PNG HTTP checks, and a CGO-disabled release build passed.
Independent Go best-practices/design-patterns and concurrency reviews passed.
Independent UI review found two state/aggregate issues; both were fixed and
re-reviewed successfully. Component browser checks covered light/dark themes,
390px viewport, and tap expansion. No real WAN outage injection was performed.
