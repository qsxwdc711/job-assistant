# Requested Company Import Implementation Plan

> **For agentic workers:** Execute the approved design in this session. Source research is delegated by independent target groups under web-access; implementation is coordinated in the named worktree with exclusive file ownership. Do not create user-owned chats.

**Goal:** Publish verified computer-related campus, internship and explicit entry-level roles from the user's 219 targets, keeping per-target evidence and honest pending/restricted states.

**Architecture:** Extend the existing fixed source registry and Moka reader; add a reusable public Feishu recruitment reader and separately validated platform adapters. Keep shared collection leases, qualification rules, atomic publication, permission checks, versioning, mock notifications and bounded daily recovery unchanged. Coverage is tracked by target IDs, with parent-company and brand relationships reviewed before source reuse.

**Tech Stack:** Go 1.26.8, pgx/PostgreSQL 18.6, existing Vue frontend, Docker, PowerShell, Playwright/Edge for anonymous source observations and isolated UI checks.

## Task 1: Coverage and live contracts

Files: `docs/research/targets/2026-10-22-*.json`, raw public responses under ignored `.cache/research/bulk-20261022/`.

- [x] Preserve all 219 names and original groups, with unique target IDs. User confirmed scope and reviewed the design before business changes.
- [x] Check a clean isolated worktree and baseline `go test ./internal/ingest ./internal/catalog ./internal/task -count=1`; passed 40.153/0.772/0.593 seconds.
- [x] Account for every target with verified official links where observed and explicit pending/restricted states elsewhere; distinguish live details from discovered entrances. Reuse Shokz; do not label all Tencent/Huawei roles as T1/car BU.
- [x] Capture real Moka list/detail cases, including missing optional list fields and Bosch custom experience requirements. For each selected tenant retain org ID, site ID, mode, public path and endpoint; slug and org ID may differ.
- [x] Capture Feishu anonymous page initialization, public CSRF and actual list/detail requests. Keep tokens only in ignored ephemeral research material and sanitize fixtures.

## Task 2: Moka batch

Files: `backend/internal/ingest/moka.go`, new `moka_sources.go`, `moka_document.go`, targeted tests and sanitized fixtures; parent-owned source migration and dispatch.

- [x] Add failing tests using actual cases: optional `commitment` absent from list must not veto valid detailed internship; a present conflicting value must fail. Org, ID, title, hire mode and open state remain mandatory identity checks.
- [x] Add failing qualification tests for structured custom fields. Preserve public field labels/values in campaign evidence and requirements; hard work experience must remain distinct from preference/project experience.
- [x] Add verified tenant configurations only; do not enumerate guessed site IDs. Permit exact approved branded hosts only when their contract is observed.
- [x] Run gofmt and all Moka tests, then complete each source twice using the same production reader without publishing, saving document/identity hashes and exclusions. Correct real schema or scope problems before enabling a source.

## Task 3: Public Feishu reader

Files: new `backend/internal/ingest/feishu.go`, `feishu_document.go`, `feishu_sources.go`, tests/fixtures; shared Reader fields/trusted-host registry and Collect dispatch are parent-owned.

- [x] Add tests for code-0 responses, strict `job_post_list`/`count`, stable pagination, duplicate IDs, short pages, null response, 401/403/429 and late detail failures. Start with `bambulab-careers` contract actually observed at `/campus/position/list`.
- [x] Read only whitelisted public origins, with an isolated anonymous cookie jar and the page's public CSRF initialization. No applicant/login/private APIs. Space reads; reject access challenges and never publish partial results.
- [x] Verify list/detail identity and recruiting type/subject. Classify actual role duties; retain minimum degree, hard versus preferred experience, student/week/month requirements and country/city evidence. Reject ambiguous social roles, nontechnical duties and hard experience over the allowed scope.
- [x] Produce official detail/apply URLs for each source's observed route. Ignore transport/tracking timestamps in content identity. Add only independently authorized tenants to the registry.
- [x] Run focused tests, then repeat complete real anonymous reads per tenant. Record explicit failures instead of relaxing identity/completeness checks to increase counts.

## Task 4: Additional platforms

- [x] Workday implementation, if its source contract is verified, lives in exclusive `workday*.go` and fixture files; use public CXS list and canApply/posted details, China/global locations as actually disclosed, explicit Intern/New College Graduate types rather than substring matches. Shared registrations/migration remain parent-owned.
- [x] Other ATS or static official pages require the same source-specific evidence. For restricted/login/challenge sources stop and record the exact limitation; never manufacture a job or treat an entrance as an applied-for job record.

## Task 5: Database and regression acceptance

Files: new source migration after 0010; `backend/tests/integration/requested_sources_test.go`; source configuration/CLI changes as needed.

- [x] For every enabled new source, require `ingest.SupportsSource(code)` and the proper company; assert prior mappings and the original ten sources remain.
- [x] In a dedicated PostgreSQL database, publish real sanitized fixtures, publish a repeat, fail a later attempt, check stable versions/old jobs and source isolation. Verify ordinary-user denial and paused source behavior.
- [x] Run gofmt, full go vet and Linux `go test -race -v ./... -count=1 -timeout=360s`, no skipped DB tests. Verify all new platform/source test markers; keep prior collection recovery, permissions and notification tests.
- [x] Build runtime image and execute isolated migrations, readiness and normal shutdown. Run frontend types/build and desktop/mobile jobs, filtering, details, official links and notification regressions against independent synthetic accounts.

## Task 6: Release and target reconciliation

- [x] Back up the local database and `.env` and capture existing source/job/version/private-data fingerprints. Commit implementation, fast-forward the canonical checkout without staging existing user research changes.
- [x] Deploy the tested runtime image, migrate and start the established Docker/native workers. Publish only sources whose real complete reads and acceptance passed, using normal collection mechanisms, never direct job-table edits.
- [x] Verify actual database counts by source, no unexpected old-job mutation, no private data/configuration changes, and `NOTIFICATIONS_SEND_ENABLED=false`. Check desktop/mobile; clean test sessions/resources.
- [x] Reconcile every one of the 219 targets to actual published source/count or explicit remaining status and evidence. Record stage results, remaining blockers and long-term/cloud validation separately. A partial batch is not completion of the whole request.

Current batch completed on 2026-10-25: 17 new sources published 663 roles (155 internships) through the daily queue. Across all 219 requested targets, 18 have published roles including existing Shokz; 201 retain explicit remaining states. Runtime uses loopback port 18080 because Windows reserves 8080; real Feishu sending remains disabled. See the phase acceptance and coverage reconciliation for evidence and unfinished work.
