# Conversation entity — P3a handoff to Nexen (v0.17)

**From:** `mlab/purdex-19` (Purdex implementer). **To:** `mlab/_lsjkj6` (nexen-61). **Coordinator:** `mlab/_0le0d2`.
**Authority:** Purdex spec `docs/specs/2026-10-06-conversation-entity-spec.md` §8.2 (the coordinator's full text, 2026-10-06) plus the decisions below. This file is the Nexen-side scope, a suggested task split and the contract sections to touch. Nexen's own flow applies in the nexen repo: its spec / plan / codex rounds, merge, then a bump PR and the `v0.17.0` tag.

Line numbers refer to nexen `main` at `dda7c42` (v0.16.1). They come from a read-only survey on 2026-10-06; re-check them before editing.

## 0. Decisions that bind this work

| # | Decision | Status |
|---|---|---|
| S1 | `start_idle` is an **explicit field** behind a capability, never "an empty brief means no turn". An old daemon ignores unknown fields and would run the turn anyway. | spec §8.1 |
| S2 | `start_idle` creates the row **directly in `idle`** with no turn and no advance, and sets `activity_since = created_at`. | spec §8.2 |
| S3 | `transcript_path` is set at creation (`transcriptPath(provider, canonical cwd, resume_session_id)`, empty when not computable). `session_id` stays empty until the first launch. | spec §8.2 |
| S4 | The prelude boundary is measured at delegate (`LastLineEnd`, same 2 s bound) and carried on `execution.delegated` as `transcript_prelude_bytes`, next to `start_idle: true` and `brief: ""`. | spec §8.2 |
| S5 | `GET /v1/executions?session_id=` matches `resume_session_id = S` OR `session_id = S` OR any turn with `session_id = S`. Three `CREATE INDEX IF NOT EXISTS`, no schema bump. Capability `list.session_filter`. `store.ListOptions` gains `SessionID`, because Purdex embeds Nexen and calls `store.List` directly. | spec §8.2 |
| S6 | No transcript-to-now endpoint. | spec §10 / §11 |
| **D19** | `Service.Prelude` resolves the boundary in the order **delegated measurement → first `execution.running` (turn_idx 1) measurement → legacy scan**. Turn-1 launch keeps measuring, with no special case. If the delegate-time measurement failed (timeout or unreadable), turn 1 fills it in without falling back to the legacy full-file scan. Contract wording: "the boundary is the first successful measurement (delegated before turn 1); once set it never moves." | **approved by the coordinator 2026-10-06** (supersedes the order written in spec §8.2's first draft) |
| **D20** | Every prelude item gains an integer **`offset`**: the start byte of the transcript line it came from. A segment marker uses the start of the line it belongs to. It goes in the contract and the capability (`transcript_prelude.item_offset: true`). `pos` stays opaque, and clients never parse it. Purdex's §10.3 attribution compares `offset` with each stint's boundary. | **approved by the coordinator 2026-10-06** |

## 1. Suggested task split (one commit per task, TDD)

### A1 — store: create an idle row

- `store/execution.go:62-90` `NewExecution`: add `TranscriptPath string`.
- `create()` (:479-517) writes neither `transcript_path` nor `activity_since` today (INSERT at :505-512). Add a `CreateIdle(ctx, in)` wrapper next to `CreateQueued` (:467) and `CreateRejected` (:475). It writes `state = idle`, `transcript_path = nullIfEmpty(in.TranscriptPath)` and `activity_since = ts`, the same `ts` as `created_at`.
- Checked already: schema.sql has no triggers. `legalTransitions` (:53-57) governs `Transition` only. `insertTurnRow` (turn.go:309-314) and `ClaimTurn` (:421-424) accept `idle`. `TurnCount` is a subquery (:229), so zero turns is fine.
- Tests: `store` package. A created idle row reads back `idle`, `turn_count 0`, the transcript path and `activity_since == created_at`. Then `CreateTurn` + `ClaimTurn` succeed on it.

### A2 — service: the `start_idle` branch of `Delegate`

- `execution/service.go:55-96` `Request`: add `StartIdle bool`. Update the `Result` doc (:98-107), because the state can now be `idle`.
- **Library-level precondition check**, in step 0 next to `ValidateBrief` (:515) and `checkAttachments` (:535): if `StartIdle` is set and (`ResumeSessionID == ""` or `Provider != "claude"` or `Brief != ""` or attachments are present), return a sentinel `ErrStartIdleConflict` that wraps the offending field name. No row is created. Purdex embeds Nexen and calls `Delegate` directly, so the API check alone is not enough.
- Keep steps 1–3.5 and the credential decision (:544-628) **unchanged**: cwd allowlist, profile, the `resumeTargetPresent` stat (:602), account selection. Their rejections still produce `rejected` rows (admission.go:107-135).
- Then, when `StartIdle` is set:
  - measure `b := measureBounded(s.preludeLastLineEnd, path, s.preludeCaptureWait)` (launch.go:711-734, 2 s bound `preludeCaptureTimeout` :694), with `path = s.transcriptPath(req.Provider, resolved.Canonical, req.ResumeSessionID)` (addressing.go:78-89);
  - `CreateIdle` with `TranscriptPath: path`;
  - skip `CreateTurn` (:658-668), the attachments key, the `turnErr` path (:705-708) and `advance` (:718-737);
  - emit `execution.delegated` with the usual keys plus `start_idle: true`, `brief: ""` and, only when measured, `transcript_prelude_bytes: b`;
  - return `Result{ID, State: StateIdle, EffectiveProfile}`.
- Tests (`execution_test`, helpers `newTestServiceWithStore` service_test.go:729-760, `plantTranscript` resume_session_test.go:34):
  - state `idle`, no turns, no provider start (fakeProvider records nothing);
  - delegated payload keys incl. `start_idle`, `brief:""`, `transcript_prelude_bytes == size of the planted transcript`;
  - summary `transcript_path` set, `session_id` empty;
  - each precondition violation → `ErrStartIdleConflict` naming the field, with no row;
  - a missing resume transcript → `rejected` exactly as today;
  - **first `Send`** (lease required) creates turn idx 1 with `input_kind = message`, launches `--resume <resume_session_id>`, and then behaves as turn 1 today;
  - a missing transcript at the first send → `failed` / `turn_failed_to_launch` (matrix :614).

### A3 — prelude boundary order: delegated first (D19)

- `launch.go:756-771` `measurePreludeEnd` is **unchanged**: turn 1 keeps measuring.
- `execution/prelude.go:175-203` `preludeEnd`: resolve in the order:
  1. the `execution.delegated` measurement (`transcript_prelude_bytes`). Reuse the existing delegated fetch (:193-201), and add a parser: `capturedPreludeEnd` (:214-230) requires `turn_idx`, which the delegated payload lacks.
  2. the first `execution.running` with `turn_idx == 1` (today's step, :186).
  3. the legacy scan (:202).

  The cache logic stays.
- Tests (`prelude_capture_test.go`, `prelude_service_test.go`):
  - a `start_idle` row whose transcript grows between delegate and the first send: `GET /prelude` `total_bytes` equals the **delegated** value both before and after the first send, even though turn 1's `execution.running` carries a larger one;
  - a delegated measurement that failed (simulate the timeout via `SetPreludeCaptureForTest`, export_test.go:203): the boundary is the turn-1 measurement after the first send, and the legacy scan (`SetScanLegacyBoundaryForTest` :194) is not called;
  - neither measurement exists: the legacy path, as today;
  - non-`start_idle` executions: unchanged, because their delegated payload has no measurement.

### A4 — API: wire, 400, capability

- `api/handlers.go:37-60` `delegateRequestWire`: add `StartIdle bool \`json:"start_idle"\``.
- In `handleDelegate` (:118-206), right after the resume-id validation (:149-152) and before any attachment decoding: return a 400 `start_idle_conflict` with a `field` key. Model the writer on `writeAttachmentError` (api/attachments.go:190-196). Map `ErrStartIdleConflict` in `writeDelegateError` (:906-934) as well.
- Add `codeStartIdleConflict` to the code block (:800-843).
- `api/capabilities.go`: `delegateCapabilities` (:147-155) gains `StartIdle bool \`json:"start_idle"\``, set true at :557-560.
- The delegate response `state` can be `idle`. Check `writeDelegateResult` and anything that documents queued / running / rejected.
- Tests (api, `postDelegate` resume_session_test.go:111, `newTestServer` server_test.go:400): the 400 body has `code` and `field` for each violation, with no row (pattern :129-149); 200 `state: idle`; the capability is declared and honoured, copying the `TestCapabilities_DeclaredResumeSessionIDIsHonored` pattern (capabilities_test.go:637-663).

### A5 — list by session (S5)

- `store/execution.go:940-946` `ListOptions`: add `SessionID string`. In `List` (:965-1020), after the label clause (:988-992), add `AND (resume_session_id = ? OR session_id = ? OR id IN (SELECT execution_id FROM turns WHERE session_id = ?))`. The `IN` form lets SQLite use all three indexes; a correlated `EXISTS` with `OR` does not.
- `store/schema.sql`: three `CREATE INDEX IF NOT EXISTS`, on `executions(resume_session_id)`, `executions(session_id)` and `turns(session_id)`. Precedent: `events_by_execution_kind` (schema.sql:119-126) is additive and not version-gated. `currentSchemaVersion` stays 6 (store.go:76).
- `api/handlers.go:521-627` `handleListExecutions`: parse `session_id` after `label.*` (:559-577). Validate and lowercase with `store.ValidateResumeSessionID` (:360) / `NormalizeResumeSessionID` (:395), and return 400 `malformed_parameter` via `writeMalformedParameter(w, "session_id", err)` (:869-875). A present but empty `session_id` → 400, matching the prelude endpoint's present-but-empty rule.
- Capabilities: add a `List listCapabilities \`json:"list"\`` object with `SessionFilter bool \`json:"session_filter"\`` to `capabilitiesResponse` (:388-484) and set it in the literal (:509-574).
- Tests:
  - store (query_test.go): match by each of the three sources; no duplicates when two sources match; combined with archived / state / labels; the indexes exist in `sqlite_master` (store_test.go:387 pattern);
  - api: 400 on a malformed value; lowercase normalisation; the capability.

### A6 — prelude item `offset` (D20)

- Each prelude item gains `offset` (int64, the start byte of the source transcript line). Derived N2 items and segment markers carry the offset of their source line. See the `prelude` package `page.go` / `classify.go` and the API view in `api/prelude.go:34-46`.
- Capability `transcript_prelude.item_offset: true` (capabilities.go:349-366).
- Tests: on the golden fixture, `offset` equals the line start byte for every kind, and it is monotonic within a page.

### A7 — contract docs (same PR as the code they describe, per capability-matrix :1782)

**`docs/contract/capability-matrix.md`:**

| Section | Lines | Change |
|---|---|---|
| §0 response shape | 34-131 | `delegate.start_idle` (101-104), new `list` object, `transcript_prelude.item_offset` |
| `delegate` explainer | 162-168 | |
| §1 verb table | 285-306 | delegate 287, list 288: `start_idle`, `session_id` |
| §1.5 list filters | 380-388 | |
| §1.8 resume | 581-629 | point 5 (599-601) and the "transcript gone before launch → failed" row (614), which for `start_idle` happens at the first `send` |
| §1.10 titles | 767 | titles can appear before turn 1 |
| §1.11 prelude | 876 | boundary order (904-915, 929-943), the delegated measurement, D19, `offset` |
| error codes | 1088-1129 | `start_idle_conflict`; `malformed_parameter` for `session_id` |
| §2 honesty rows | #5 (1143), #48 (1192), #65 (1209) | #5: `idle` may mean "never ran a turn". #48: for `start_idle` the resume guarantee is paid at the first send. #65: legacy scan not used for `start_idle`. Append new rows after #72 (1216) if needed. |
| §3 events | `execution.delegated` 1230, `execution.running` 1232 | new keys |

**`docs/contract/consumer-guide.md`:** §4 table (214-235), §6.3 the delegate → attach → send flow (515-531), §9.7 (1141-1265), and the change log (292-319).

### Release

A separate bump PR touching CHANGELOG.md only: `chore: bump v0.17.0 — start_idle, list by session`. Then an annotated tag `v0.17.0` on the bump PR's merge commit, as v0.16.1 was done (dda7c42). The CHANGELOG Deploy section states: no schema bump, indexes added on open, no DB delete.

## 2. What Purdex does with it (P3b-1, for context)

- **Pin v0.17.0.** Handoff and `worker-rebuild` delegate with `StartIdle: true`, with no brief, after the existing owner lock. The Go pin is the capability check: the daemon embeds Nexen.
- **`keep_session:false` after a handoff.** Today the tmux session is killed only when the delegate answered `running`. It must also accept `idle` from a `start_idle` delegate.
- **Owner scans** (`liveWorkersFor`) switch to `store.ListOptions{SessionID: S}`.
- The SPA's handoff confirm text drops "continues headless" wording.
- **P3b-2** (§10) uses `list.session_filter`, `GET /prelude` `total_bytes` per stint, item `offset`, and `/events` per earlier stint.

## 3. Hand-back

When `v0.17.0` is tagged, message `mlab/_oecdo4` (purdex-19) with the tag, the CHANGELOG entry and any contract decision taken during review that differs from this file.
