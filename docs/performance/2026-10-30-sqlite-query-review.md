# SQLite query latency review — 2026-10-30

## Scope and limits

Reviewed the current cfui SQLite schema, query paths, and browser bootstrap.
No production database, request trace, or slow-query log was available. The
measurements below use a disposable database created by the current Go application,
then populated and queried with Python SQLite 3.54.0. They locate a reproducible
scaling problem; they do not establish the cause of a reported production delay.
No application schema or runtime database settings were changed in this review.

## Confirmed query scaling issue

`internal/cfoauth/store.go`, `ListValidationReportArchives`, orders by `saved_at`
and applies a limit (default 20, maximum 50). The generated table has only the
unique `report_id` index; there is no index on `saved_at`.

Equivalent query and actual baseline plan:

```sql
EXPLAIN QUERY PLAN
SELECT * FROM oauth_validation_reports ORDER BY saved_at DESC LIMIT 20;
-- SCAN oauth_validation_reports
-- USE TEMP B-TREE FOR ORDER BY
```

A candidate index was applied **only to the disposable database**:

```sql
CREATE INDEX probe_saved_at ON oauth_validation_reports(saved_at);
-- SCAN oauth_validation_reports USING INDEX probe_saved_at
```

Test data: 5,000 records, 16,473-byte JSON body per record. Forty-five sequential
queries per scenario on the same connection; no concurrent writes, no cold-cache
control, no HTTP/Ent mapping or JSON parsing included. Percentiles are sorted-sample
nearest ranks for p95 and p99; p50 is the median.

| Scenario | p50 | p95 | p99 |
| --- | ---: | ---: | ---: |
| Current schema | 2.487 ms | 2.710 ms | 2.978 ms |
| Candidate `saved_at` index | 0.061 ms | 0.065 ms | 0.154 ms |

This supports adding an Ent index in a separate change, regenerating schema code,
and testing upgrades from an existing database. The small absolute baseline
latency in this fixture cannot explain a seconds-long first-page delay by itself.

The list also fetches `report_body` and parses it to derive requested/granted scope
counts and fingerprints in `validationReportArchiveSummary`. A projection that
simply drops the body would break these fields. If larger reports become costly,
measure parsing separately and consider persisting those derived summary fields
with a migration/backfill before narrowing the list projection.

## Other paths checked

- `config.Manager.Get` reads an in-memory snapshot under `RLock`; normal config
  reads do not reload SQLite on every browser poll.
- Local auth reads settings by the singleton primary key and sessions by their
  token primary key. Expiry cleanup has an `expires_at` index. No missing index
  was identified for those predicates.
- OAuth identity lookup by `session_id` uses its unique index. `current = true`
  lookup lacks a dedicated index, but identity counts and real timings are needed
  before treating this small table as a bottleneck.
- `persist.OpenClient` applies schema creation during client initialization.
  OAuth creates its client lazily with `sync.Once`; first-request initialization
  must be distinguished from steady-state query latency.
- SQLite DSN enables WAL, shared cache, and a 10-second busy timeout. A stalled
  write or pool/lock wait can therefore dominate a cheap query. This review did
  not reproduce contention, and does not recommend blindly changing these settings.

## Relationship to first-page loading

Bootstrap sequentially waits for locale, authentication, version, features,
configuration and tunnel status, then starts optional services. Rendering the raw
HTML before that sequence completed caused the visible uninitialized page. The
loading curtain now covers that sequence and reveals the local workspace after
configuration and first status arrive, before optional remote feature loading.
This presentation fix does not make SQL faster or prove SQL caused the delay.

## Production evidence needed next

For a reproducible slow request, correlate a request ID and endpoint with:

1. End-to-end request time and individual database operations (without SQL argument
   values: tokens, passwords and report content must stay out of logs).
2. Connection-pool `DB.Stats()` deltas (`WaitCount`, `WaitDuration`, `InUse`) and
   SQLite busy/locked errors, separated from execution and row/JSON processing.
3. Actual row counts, report body sizes and `EXPLAIN QUERY PLAN` on a safe copy.
4. p50/p95/p99 under representative concurrent reads and writes, distinguishing
   cold initialization from steady state.

Optimize the measured dominant component: index/order work, payload parsing,
connection wait, write-lock contention, disk I/O, or upstream HTTP. Keep the
measurement boundary explicit when comparing results.
