# Bounded Collection Recovery Implementation Plan

> **For agentic workers:** Execute directly in this session, task by task. The user approved the design and inline implementation; do not dispatch subagents.

**Goal:** After an exhausted transient daily collection failure, allow at most two auditable recovery rounds on the same Shanghai date.

**Architecture:** Keep the original per-task three-attempt budget and durable queue. Add parent/round metadata, idempotent planning and transactional guards at claim time. All public runtimes use the same policy; notification and private workers retain their existing boundaries.

**Tech Stack:** Go 1.26.8, pgx, PostgreSQL 18.6, Docker, PowerShell, existing Vue/Playwright regression harness.

## Task 1: Prove the missing behavior in an isolated database

Files: create `backend/tests/integration/collection_recovery_test.go`.

- [x] Add a fixture using existing `newStoreFixture`, disable unrelated sources, call `Plan` at 08:00, then actually claim/finish a network failure three times at 08:00/08:01/08:05.
- [x] Assert no new task at 08:34:59; concurrently call `Plan` at 08:35 and require exactly one new task. Claim the new task, exhaust it and require round two only two hours later. Exhaust round two and require no fourth task, with the original failure untouched.
- [x] Add table cases for all five transient codes, non-transient/restricted/unknown exhaustion, insufficient attempts, 22:00, cross-date expiry, paused sources, manual/CLI recovery, newer restrictions and invalid parent metadata. Use deterministic supplied times, never sleeps to advance scheduling.
- [x] Add source-runtime, disabled-automation, content-version and notification-isolation checks. Existing fixtures and PostgreSQL migrations are used; no production source HTTP requests.
- [x] Run against a separately named temporary PostgreSQL container with a database ending `_test`:

```powershell
go test ./tests/integration -run '^TestCollectionRecovery' -count=1 -v
```

Expected initial failure: recovery count remains one after the 30-minute boundary. Preserve this output as TDD evidence.

## Task 2: Queue policy and migration

Files: create `backend/migrations/0010_collection_recovery.sql`, create `backend/internal/task/recovery.go`, modify `backend/internal/task/schedule.go`.

- [x] Add `recovery_parent_id uuid REFERENCES scheduled_collections(id)` and `recovery_round smallint NOT NULL DEFAULT 0`. Enforce rounds 0–2, parent absent iff round zero and parent different from self. No old row, attempt or error is reset.
- [x] Share the existing transient error classification between `Finish` and recovery planning:

```go
var retryableCollectionCodes = []string{
    "network_error", "timeout", "timeout_or_cancelled", "source_busy", "interrupted",
}
```

- [x] Extend `Plan` after ordinary daily planning. Before 22:00, insert eligible recovery rows with a unique `recovery:YYYY-MM-DD:source:round` key and `ON CONFLICT DO NOTHING` (key and parent uniqueness). Parent must be today's expected daily/round-one key, failed, attempts three, transient, with the 30-minute/two-hour delay elapsed. Skip disabled, already refreshed, blocked or currently pending/running sources.
- [x] Before claim selection, cancel recoveries whose day/parent/key no longer matches, sources subsequently refreshed or blocked, retaining the parent history. An expired running lease may be cancelled, but an unexpired live lease is never rewritten by this cleanup.
- [x] Exclude recovery candidates while another task for the same source is pending/running. Recheck a selected recovery against locked current source metadata before taking its domain lease. Keep runtime filters and global/domain collection locks unchanged.
- [x] Run gofmt and the targeted integration tests. Read failures, fix behavior and repeat until all pass.

## Task 3: Review and full isolated acceptance

Files: update `docs/operations/s4-notifications.md`, `README.md`; create `docs/verification/2026-10-19-collection-recovery.md`.

- [x] Perform inline Go quality and behavior review using go-feature-change-review and go-code-review. Trace Plan → row → Claim → CollectOne → Finish, including manual/CLI completion and the native source view. Correct confirmed issues and rerun affected checks.
- [x] Run the existing isolated harness with canonical web dependencies:

```powershell
./scripts/verify-s5-chaitin.ps1 -HttpPort 18085 -WebProjectRoot 'D:/Users/zcn20/Documents/ChatGPT/求职助手计算机专业版' -PnpmStore 'D:/Users/zcn20/Documents/ChatGPT/求职助手计算机专业版/.cache/pnpm-store' -UseSystemProxy
```

Require image build, go vet, real PostgreSQL/Linux `-race` all-package tests with no skipped tests, all `TestCollectionRecovery*` markers, five desktop/mobile browser regressions, migrations, native process start/stop and clean API/worker stop. Inspect cleanup logs; remove only newly created test resources.

## Task 4: Local release and evidence

- [x] Commit the tested implementation, fast-forward the canonical branch without altering existing user changes. Back up the local database and record tasks, job/version hashes, private counts and `.env` bytes before release.
- [x] Tag the verified runtime image as `job-assistant-local:latest`, run the existing local startup with `-SkipBuild`, verify all ten migrations, readiness, expected runtime source exclusions, native worker health and `NOTIFICATIONS_SEND_ENABLED=false`.
- [x] Confirm today's successfully refreshed sources do not create recovery work, old nine failures remain, and existing job/version/private data and configuration stay identical. Check desktop/mobile jobs and notification administration without triggering collection or real messages; remove verification sessions.
- [x] Record actual commands/results, any corrected test assertions and pending limits. Long outage root cause, real cloud deployment and seven-day stability remain unverified. Commit records and remove the clean worktree normally.
