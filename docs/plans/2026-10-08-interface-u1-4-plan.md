# Interface language U1-4 — conversation model, transcript normalizer, golden fixtures — plan

Spec: `docs/specs/2026-10-08-interface-u1-spec.md` §1–§5, **§3 M-U1-7** (transcript format, measured for this plan), **§8 first bullet and §8.1** (the U1-4 contract, added by this plan's PR), §10. Design: `docs/pages/interface-language.html` **[D §13]** capabilities, **[D §14]** conversation model and G1–G7, [D §5] deck grammar (what the model must be able to draw), [D §16] fixtures as one of the three deliverables to iOS. Identity: `docs/specs/2026-10-06-conversation-entity-spec.md` §4.1 / D9.

Format as the U1-1 / U1-2 plans: contracts, rules, named tests and mutation gates; implementers write the code (TDD: each named test red first). Parallel to U1-2 (no shared files: U1-4 adds new packages and touches nothing in `internal/module/agent`, `internal/lights`, `internal/core/events.go` or the mod).

## Measurements (2026-10-08)

- **Census of real transcripts** (read-only Python over mlab `~/.claude/projects/**/*.jsonl` modified in the last 14 / 30 days; prints key names, enum values, counts and 28-character shape prefixes with digits masked, never content): 1,460 files (300 main, 1,160 subagent), 491,362 rows, CC 2.1.263–2.1.294. Findings are §3 M-U1-7. Scripts: the session scratchpad `census/census{,2,3,4,5}.py` (not committed: they read private data; rerun them to re-measure).
- **iOS samples** (`wake/purdex-ios` `docs/samples/2026-10-07-cc-2.1.292/`, CC 2.1.292): row-by-row structure checked for the queue, interrupt, refusal, AskUserQuestion, bash-mode and background-Bash cases; matches the census.
- **Existing code**: `GET /api/sessions/{code}/transcript` (`internal/module/agent/transcript_handler.go:51`) serves raw lines with byte-offset cursors (`tail`, `after`, `transcript_id`, 2 MiB per response, 8 MiB per line); path resolution in `transcript_path.go:98` (hook `transcript_path` first, as entity spec §4.1 requires). No Go type models a transcript row beyond the title / first-prompt scanner `internal/conversations`. No transcript golden fixtures exist in Go; `cmd/pdx/plugin/prompts_test.go:13` has the repo's only `-update` flag.
- **iOS parser** (`purdex-ios` `App/Sources/Chat/Transcript.swift`, `ChatGrouping.swift`, `SendQueue.swift`): ported rule by rule into §8.1 (kind table T:19-29, summary T:176-189, status T:32-37 + stale T:67-76, slash T:163-174, compact / interrupt notices T:101-107, ≥3 fold G:25). It reads neither `queue-operation`, `promptSource`, `toolDenialKind` nor `toolUseResult`; §8.1 uses those structured fields first and keeps the iOS string matches as the fallback for older rows.

## Reuse: Nexen `prelude` vs an own parser (spec §8 asks for this evaluation)

Facts: `prelude` lives in the external module `lab.protype.tw/wake/nexen@v0.20.0` (`prelude/`, 2.6 k lines of code + 5.6 k of tests), not in `internal/module/nex`. Its line classifier `classify` and helpers are **unexported**; the only entry point is `ReadPage(Request{Path, End, Before, Limit, NewDeriver})`, which reads a file by path, pages newest-first by byte offset and returns stream-json-shaped items (`user` / `assistant` envelopes, `prelude.note`, `prelude.compaction`). Tool status (`ok | error | denied`) is not in `prelude` but in `nexen/execution/toolevents.go` behind an unexported deriver. `internal/module/nex/imports_test.go` only limits what `internal/module/nex` imports, so a new package could import `nexen/prelude` without tripping it.

| | A. Import `nexen/prelude` | B. Ask Nexen to export a line classifier | **C. Own parser in Purdex, porting prelude's rules (recommended)** |
|---|---|---|---|
| What we get | user / assistant envelopes, notes, compaction; caps; newest-first pages | A plus a per-line call | exactly the model §8.1 needs |
| What we still write | turns, outcome, 7 kinds, status, denied, diff, sources, ids, incremental feed — and a second decode of every envelope | same as A | everything (≈1.5 k lines + tests) |
| Mismatch | file-path paging (no incremental feed for U1-6 increments); omits `turn_duration`, API errors, `thinkingDurationMs`, the `queued` / `scheduled` sources; models `interrupted` results as `error` where [D §5] wants a grey "denied" | same output shape | none |
| Coupling | Purdex's conversation model follows Nexen's release cadence; a Nexen bump can change Purdex output | an upstream change and release before U1-4 can start | none; Nexen keeps owning the execution prelude |
| U4 (execution underlay) | — | — | U4 feeds Nexen events / mod events into the same `convmodel`; the CC transcript normalizer is reused for the resume target's history |

Recommendation **C**. The overlap is the line-classification table (origin gate before tags, slash rewrite, `queued_command` attachments, `<persisted-output>` unwrap, empty-thinking rows, `<synthetic>` rows); §8.1 adopts those rules verbatim and the implementer ports them with a comment citing the prelude source (`prelude/classify.go:286-365`, `:411-512`, `:516-545`). No import, no `imports_test.go` change.

## PRs

| PR | Content | Depends on | Est. lines |
|---|---|---|---|
| **U1-4-0** (this) | plan + spec §3 M-U1-7, §8.1 (docs only) | — | — |
| **U1-4a** | `internal/convmodel`: types, enums, JSON (item union, round trip), `Validate`, transcript capabilities, import-boundary test | — | ~500 |
| **U1-4b** | `internal/convmodel/ccnorm` core: row decode, incremental feed, turns, outcome, user sources, agent text, thinking, system items, title, model / effort, stats | 4a | ~800 (splits into **b-1** decode + feed + turns + outcome ~450 and **b-2** sources + system + title ~400 if it grows) |
| **U1-4c** | `ccnorm` steps: pairing, kinds, summaries, status / denial, input / output caps, image placeholders, diff, command, subagent link + `NormalizeSubagent` | 4b | ~750 |
| **U1-4d** | golden fixtures v1: scrubber, recorded cases, MANIFEST, golden test with `-update`, privacy guard, README for the Apps | 4c | code ~350 + fixture data (see "Fixture size" — needs a lead ruling) |

A chain (each builds on the previous package state); 4d's recording can start while 4c is in review. **Release gate**: none — U1-4 changes no runtime path (no API until U1-6). It merges without a deploy.

Common rules:
- Go only: `make lint` clean; `go test ./internal/convmodel/...`; `go test -race` for `./internal/convmodel/ccnorm` only (the one stateful package). No SPA change, so no vitest.
- Each task one commit; parallel subagents in one worktree commit with `git commit --only <files>`.
- Resource rules (mlab 16 GB, verbatim into every brief): (1) only affected tests during development; (2) the full vitest only before merge, `--maxWorkers=3`, after asking the lead — not needed here; (3) `go test -race` only the affected package, one at a time.
- No transcript content from `~/.claude/projects` enters the repo except through the 4d scrubber and its guard test.

---

## U1-4a — `internal/convmodel`

### Task A-1 — types and JSON

Files: `internal/convmodel/{model.go, item.go, json.go, capabilities.go}` + tests.

- Types exactly as spec §8.1 "Wire form". Enums as string types with constants: `Outcome`, `ItemType` (`user`, `agent_text`, `thinking`, `step`, `system`), `Source` (`user`, `queued`, `peer`, `slash`, `bash`, `task`, `scheduled`), `StepKind` (7), `StepStatus` (4), `SystemKind` (6), `Keep` (`head`, `tail`).
- `Item` is a struct with `Type` and one non-nil pointer per type (`User`, `AgentText`, `Thinking`, `Step`, `System`); `MarshalJSON` writes the flat object `{"type": …, <the variant's fields>}`; `UnmarshalJSON` reads it back (the Apps' decode path, and the fixture round trip). A nil variant or two non-nil variants is a marshal error.
- Times: `int64` ms. Optional scalars as pointers or `omitempty`; `ended_at` pointer (0 is a valid time in tests).
- Offsets for U1-6: `Turn.Offset`, `Item` offset kept in an unexported side field or `json:"-"`; never on the wire.

Tests: `TestItemJSON_RoundTripEveryType`, `TestItemJSON_FlatShapeHasTypeDiscriminator` (golden string for one item of each type), `TestItemJSON_MarshalRejectsZeroOrTwoVariants`, `TestConversationJSON_OmitsEmptyOptionals` (no `client_msg_id`, `streaming`, `input_partial`, `children`, `usage.context` keys when empty), `TestConversationJSON_OffsetsNotOnWire`, `TestTimesAreIntegerMillis`.
Mutation gates: drop `omitempty` on `client_msg_id` → `OmitsEmptyOptionals` red; marshal the variant under a nested key → `FlatShape` red.

### Task A-2 — `Validate` and capabilities

- `func (c *Conversation) Validate() error`: every enum value known; ids non-empty and unique within the conversation; `Turn.Index` = position; `started_at ≤ ended_at`; a `running` turn only last; `step.denial` only with `denied`; `output.total_bytes ≥ len(text)`; `truncated` ⇔ text shorter than total. Used by the normalizer tests and later by the API (debug builds).
- `TranscriptCapabilities()` returns the §8.1 object: `source: "transcript"`, `text_streaming: "message"`, `thinking: "duration"`, `subagent: "partial"`, every other field `none` with `reasons[field] = "not_wired"`. Field set = [D §13] exactly (U1-8 adds values, not fields).

Tests: `TestValidate_AcceptsWellFormed`, one `TestValidate_Rejects<Case>` per rule, `TestTranscriptCapabilities_FailClosed` (every field present; every non-transcript field `none` + reason).
Mutation gate: drop the uniqueness check → `TestValidate_RejectsDuplicateID` red.

### Task A-3 — import boundary

`internal/convmodel/imports_test.go` (pattern of `internal/module/nex/imports_test.go`): non-test files of `convmodel` import only the standard library; `ccnorm` only the standard library and `convmodel`. Test: `TestImportBoundary`. Mutation gate: add an `internal/...` import → red.

---

## U1-4b — `ccnorm` core

Files: `internal/convmodel/ccnorm/{row.go, normalizer.go, turns.go, user.go, system.go}` + tests. Inputs in tests are inline JSONL built by small helpers (`userRow(...)`, `assistantText(...)`, `turnDuration(...)`), shaped after M-U1-7 and the iOS samples.

### API

```go
type Options struct {
    SessionID string // fills key.session_id
}
type Ref struct{ TurnID, ItemID string } // ItemID "" = the turn itself
type Stats struct{ Lines, BadJSON, Skipped map[string]int }

func New(o Options) *Normalizer
func (n *Normalizer) Feed(offset int64, line []byte) []Ref // one complete line, no '\n'
func (n *Normalizer) Conversation(live bool) convmodel.Conversation // deep copy
func (n *Normalizer) Stats() Stats
```

Not safe for concurrent use (the U1-6 caller serializes per transcript). `Feed` never panics on any byte input (fuzz test).

### Rules

Spec §8.1 "Rows", "Turns", "Outcome", "User message source", "Agent text and thinking", "System items", "Text caps", "Incremental". Notes for the implementer:
- Decode with a `rawLine` of named top-level keys and `json.RawMessage` values (as `prelude/classify.go:62`); message content decoded lazily; image `source.data` is measured (`base64.StdEncoding.DecodedLen` of the trimmed length) and never copied.
- Rows of one assistant message arrive as several lines; each line is handled on its own (one block per line, M-U1-7), so no buffering by `message.id` is needed; a multi-block row (older versions) yields `<uuid>#<n>` ids.
- Outcome is recomputed for the last turn on every `Conversation(live)` call (it depends on `live`); earlier turns are final once the next turn opens.
- `Ref`s: a new turn → `{turn, ""}`; a new or changed item → `{turn, item}`; a turn whose outcome / `ended_at` changed → `{turn, ""}`.

### Named tests

Turns and outcome: `TestTurns_OpenAtPromptRowWithTurnPosition`, `TestTurns_OpenAtPromptRowWithoutTurnPosition` (pre-2.1.284 shape), `TestTurns_LocalCommandIsATurn`, `TestTurns_BashModeIsOneTurn` (`<bash-input>` + `<bash-stdout>` prompt row → one turn, `user{source: bash}` + `system{command_output}`), `TestTurns_AbsorbedQueuedPromptStaysInRunningTurn`, `TestTurns_CompactBoundaryBetweenTurnsJoinsPrevious`, `TestOutcome_DoneByTurnDuration`, `TestOutcome_InterruptedByMarkerWithoutTurnDuration` (f2b lines 80–84), `TestOutcome_RefusalWithTurnDurationIsInterrupted` (f3 lines 69–79: refusal marker wins over `turn_duration`), `TestOutcome_FailedByApiError` (synthetic row → `error{kind: rate_limit}`, no agent_text), `TestOutcome_OpenNonLastTurnIsInterrupted`, `TestOutcome_LastOpenTurnRunningWhenLive`, `TestOutcome_LastOpenTurnDoneWhenNotLive`.
Sources: `TestSource_Typed`, `TestSource_SuggestionAcceptedIsUser`, `TestSource_QueuedPromptRow` (f2b line 67), `TestSource_QueuedAttachmentFromHuman`, `TestSource_PeerMetaRowStripsWrapper`, `TestSource_PeerQueuedCommand`, `TestSource_TaskNotificationUsesSummary`, `TestSource_Scheduled`, `TestSource_SlashCommandRewritten` (`<command-name>/login</command-name><command-args>abc</command-args>` → `/login abc`), `TestSource_OriginGateBeforeTags` (a `peer` row whose text starts with `<command-name>` is `peer`, not `slash` — prelude's codex R2 ATK2-2 case), `TestSource_UnknownOriginSkippedAndCounted`, `TestUser_ImageBlocksBecomePlaceholders` (bytes = decoded size; the base64 is not in the output), `TestUser_TextCappedAt64KiB`.
Text, thinking, system: `TestAgentText_OneItemPerBlock`, `TestAgentText_SyntheticRowNeverText`, `TestThinking_EmptyTextWithDuration`, `TestThinking_EmptyWithoutDurationDropped`, `TestSystem_InterruptedReplacesMarker`, `TestSystem_Compacted`, `TestSystem_HandoffOnEntrypointChange`, `TestSystem_ModelChangedBetweenTurns`, `TestSystem_ModelChangedIgnoresSynthetic`, `TestSystem_LocalCommandOutput`.
Rows and metadata: `TestRows_SidechainSkipped`, `TestRows_MetaSkippedExceptPeer`, `TestRows_CompactSummarySkipped`, `TestRows_BadJSONCounted`, `TestTitle_CustomOverAi`, `TestUsage_ModelAndEffortFromLastMainRow`.
Ids and incremental: `TestIDs_RowUUIDs`, `TestIDs_MultiBlockRowSuffix`, `TestIDs_DerivedSystemIDs`, `TestIDs_StableAcrossRenormalize`, `TestFeed_ChunkInvariance` (property: the three iOS samples and a generated 2,000-row transcript fed whole vs. line by line vs. in random whole-line splits → identical `Conversation`), `TestFeed_RefsReportNewAndChanged`, `FuzzFeed` (no panic; `Validate()` passes on the result).
Mutation gates: drop the interrupt-marker check → `InterruptedByMarker…` and `RefusalWithTurnDuration…` red; open a turn on the `queued_command` attachment → `AbsorbedQueuedPrompt…` red; read tags before the origin → `OriginGateBeforeTags` red; keep image base64 → `ImageBlocksBecomePlaceholders` red; buffer per `message.id` and emit at the end → `ChunkInvariance` stays green but `RefsReportNewAndChanged` red.

---

## U1-4c — `ccnorm` steps

Files: `internal/convmodel/ccnorm/{steps.go, kinds.go, output.go, diff.go, subagent.go}` + tests.

Rules: spec §8.1 "Steps". Notes:
- Pairing by `tool_use_id` across turns (a background Bash result can arrive late); a result arriving for a step already marked `denied{interrupted}` (stale) replaces that status.
- Output text: string content as is; a list joins its `text` blocks with `\n` and adds `[image]` per image block; `<persisted-output>` unwrapped (prelude `unwrapPersisted`, one layer). `total_lines` = number of `\n`-separated lines, a trailing `\n` not counting as a further line; `total_bytes` = UTF-8 length; the cut never splits a UTF-8 sequence; a single line longer than the cap is cut by bytes.
- `structuredPatch` hunks are `{oldStart, oldLines, newStart, newLines, lines}` in CC; renamed to snake_case.
- `NormalizeSubagent(r io.Reader, agentID string) ([]convmodel.Item, Stats)` — the same row rules with sidechain rows accepted (every row of a subagent file is sidechain); the subagent's first prompt row is its brief and becomes a `user{source: task}`… **open: see decision D7**.

### Named tests

`TestKind_Table` (every name in the §8.1 table, plus `mcp__x__y` → other), `TestSummary_Table` (one per row of the summary rule, incl. MultiEdit basename, mcp `server · tool`, first-line only, fallback first string in key order), `TestStatus_RunningWhileTurnRunning`, `TestStatus_StaleBecomesDeniedInterrupted`, `TestStatus_DenialKindPassedThrough` (each of the four values), `TestStatus_UserRejectedByTextFallback`, `TestStatus_IsErrorWithoutDenialIsFailed` (`Exit code 1`, `<tool_use_error>`), `TestStatus_LateResultReplacesStale`, `TestDuration_FromRowTimes`, `TestInput_StringValuesCapped`, `TestInput_WholeCapped`, `TestOutput_ExecuteKeepsTail`, `TestOutput_ReadKeepsHead`, `TestOutput_TotalsCountWholeText`, `TestOutput_CutOnLineBoundary`, `TestOutput_NoSplitUTF8`, `TestOutput_ImagePlaceholder`, `TestOutput_PersistedOutputUnwrapped`, `TestDiff_FromStructuredPatchExact`, `TestDiff_FromInputWhenDenied` (`exact: false`), `TestDiff_MultiEditConcatenated`, `TestDiff_HunkLinesCapped`, `TestCommand_ExitCodeParsed`, `TestCommand_BackgroundTaskID`, `TestSubagent_LinkFromToolUseResult`, `TestNormalizeSubagent_ItemsFromSidechainFile`, `TestSteps_DoesNotRetainImageBase64` (a 5 MB image row → output under 1 KiB).
Mutation gates: map `toolDenialKind: interrupted` to failed → `DenialKindPassedThrough` red; keep head for execute → `ExecuteKeepsTail` red; count lines of the kept text instead of the whole → `TotalsCountWholeText` red; trust `added`/`removed` from input when a patch exists → `FromStructuredPatchExact` red.

---

## U1-4d — golden fixtures v1

Files: `testdata/conversation/v1/{MANIFEST.json, README.md, cc-transcript/<case>/{input.jsonl, expected.json, README.md}}`, `internal/convmodel/ccnorm/golden_test.go`, `internal/convmodel/ccnorm/fixtureguard_test.go`, `internal/convmodel/ccnorm/cmd/scrubfixture/main.go` (a `go run` tool, not built into `pdx`).

### Scrubber

Reads a raw transcript, writes a fixture input: keeps the row types and fields the normalizer reads (§8.1 "Rows") and drops every other row; replaces `cwd` with `/work/fixture`, `sessionId` / `session_id` with a fixed uuid, `gitBranch` with `main`, paths under the home directory or `/private/tmp` with `/work/…`; drops `attachment` rows except `queued_command`; truncates base64 image data to a tiny valid PNG while keeping the declared size in a sidecar field the normalizer ignores (so the placeholder `bytes` stays realistic) — **or** keeps a tiny image and accepts a small `bytes` (implementer picks, documented in the case README). Content text of iOS samples stays (it was written for this purpose and is in a repo already); recorded cases use throwaway prompts only.

### Cases (each one directory)

From the iOS samples (CC 2.1.292): `ios-c2-send-keys`, `ios-f2b-queue-interrupt`, `ios-f3-ask-permission`.
Recorded on mlab in a throwaway tmux session (`claude --model sonnet` in a scratch git repo under the scratchpad, prompts written for the fixture, `--permission-mode default` where a refusal is needed): `edit-write-multiedit` (diffs), `read-grep-glob-webfetch`, `bash-fail-and-background` (exit code, background task + task-notification turn), `subagent` (Agent tool; the subagent file under `subagents/`), `image-result` (Read of a PNG), `large-output` (a command printing 5,000 lines), `slash-and-local-command` (`/model`, a skill command), `compact` (`/compact`), `peer-message` (one `pdx msg send` into the throwaway session), `queued-mid-turn` (a prompt absorbed by a running turn), `api-error` — not provokable on demand: a hand-written case from the M-U1-7 shape, marked synthetic in its README. Each recording is one session; total spend is small (sonnet, short prompts).

### Tests

- `TestGolden` — for each MANIFEST case: feed `input.jsonl`, `Conversation(live)` with the case's `live`, compare with `expected.json` byte-for-byte after canonical encoding; `go test ./internal/convmodel/ccnorm -run TestGolden -update` rewrites `expected.json` and the MANIFEST sha256s. `Validate()` must pass for every case.
- `TestManifest_Sha256Match` — the MANIFEST hashes equal the files (what the Apps pin).
- `TestFixtures_NoPrivateData` — no fixture file contains `/Users/`, `/private/tmp/claude-`, `100.64.`, an e-mail address, `Bearer `, `sk-`, `ghp_`, or a 32+ char hex/base64 token outside image data and uuids.
- `TestFixtures_CoverEveryEnum` — across all expected files, every `Source`, `StepKind`, `StepStatus`, `SystemKind`, `Outcome` value appears at least once (`resumed` exempt until M-U1-4-a).
Mutation gates: change one expected field → `TestGolden` red; put a `/Users/` path in an input → `NoPrivateData` red.

### Fixture size

The four iOS-derived and ~11 recorded inputs are ~150–400 lines each after scrubbing; `expected.json` pretty-printed is larger. 4d's **code** stays well under 800 lines, but data pushes the diff past it. Proposal: fixture data does not count toward the 800-line limit (it is reviewed by the guard tests, `Validate`, and a skim of each case README), and codex reviews 4d's code with the data paths excluded. **Needs the lead's ruling (D8).**

---

## G1–G7 ([D §14]) — what U1-4 prepares

| Gap | U1-4 | Later |
|---|---|---|
| G1 writes (send / interrupt / steer / answer) | nothing | U1-9 |
| G2 `client_msg_id` | field on `user` items (omitted when empty) | U1-9 fills it (mod path: the daemon's own submit; no-mod path: the daemon matches the typed text — iOS `SendQueue.reconcile` rules move into the daemon) |
| G3 pane → conversation key | `key` as three fields, no string form invented | U1-8 (aligned with entity spec §4.1 / D9) |
| G4 snapshot paging / caps / truncation | stable `Turn.index` + ids, byte offsets kept for cursors, per-item caps, image placeholders, `truncated` flags | U1-6 (turn limit, `before` / `after`, snapshot-level marker) |
| G5 list summary | nothing | U1-7 |
| G6 push | nothing | U2 |
| G7 catch-up after reconnect | deterministic ids, chunk-invariant feed, `Ref`s per feed | U1-6 (`after` cursor, `approval.closed` replay) |

U1-5 merge hook: mod items will carry the same ids (row `uuid` from `session.append`, `tool_use_id` from `tool.call`), so a mod item and its later transcript row are the same item; U1-5 decides which source wins per field.

## Measurements during implementation

- **M-U1-4-a restart marker** (4b): resume a throwaway session (`claude --resume <sid>`) and restart one; record which rows appear at the restart (`session_context`, `environment`, …). Confirmed → `resumed` from the transcript (spec §8.1 updated in 4b); not reliable → `resumed` stays mod-only (U1-5) and the fixture coverage test exempts it.
- **M-U1-4-b `/compact` and local commands** (4d recording): confirm the `compact_boundary` / `isCompactSummary` / `local_command` shapes against M-U1-7 (one compaction seen in the census).

## Decisions for the lead (behavior rules beyond [D §14]; spec §8.1 already written this way, change on ruling)

| # | Question | Proposal |
|---|---|---|
| D1 | `UserMessage.source` has four values in [D §14]; the transcript has bash mode, task notifications and scheduled wake-ups that open turns | add `bash`, `task`, `scheduled` (spec §8.1 table); `coordinator` / `plugin` / `auto-continuation` origins are skipped like Nexen prelude does |
| D2 | `System.kind` lacks a place for local-command / bash-mode output | add `command_output` |
| D3 | API errors | `Turn.error {kind, message}` + `outcome: failed`; no `agent_text` for the synthetic row (no new System kind) |
| D4 | A step whose turn ended without a result (Esc, killed process) | `denied` with `denial: "interrupted"` (the iOS "stale" rule; grey "已拒絕" per [D §5]); Nexen's toolevents calls this `error` — we diverge on purpose. `denial` passes `toolDenialKind` through so a client can later say 已中斷 vs 已拒絕 without a schema change |
| D5 | Turn outcome when a refusal ends the turn (`turn_duration` present) | `interrupted` (the marker wins) |
| D6 | Kinds for tools [D §5] does not list | `Monitor` → `execute`; `ToolSearch`, `Skill`, `AskUserQuestion`, `ExitPlanMode`, `mcp__*` → `other` |
| D7 | Subagent children | U1-4 provides `NormalizeSubagent`; its brief (first prompt) is a `user{source: task}` item; whether the snapshot inlines `children` or loads them on expand is U1-6's call |
| D8 | Fixture data vs the 800-line limit | data exempt; code counted (above) |
| D9 | Caps | text 64 KiB; step input strings 4 KiB / whole 16 KiB; step output 16 KiB (execute tail, others head); diff 400 hunk lines |
| D10 | Times | integer ms since the epoch (same as the mod wire `at`); iOS parses ISO 8601 today |
| D11 | Additions to [D §14] fields | `Turn.index`, `Turn.error`, `user.truncated` / `images`, `agent_text.truncated`, `step.denial` / `input_truncated` / `output.keep` / `output.images` / `diff.exact` / `diff.truncated` / `command.exit_code` / `command.background_task_id` / `subagent`; `key` as an object |

## Fixture format — draft for iOS (`air26/_0g6g1e`), via the lead

```
testdata/conversation/v1/
  MANIFEST.json      {"version": 1, "cases": [{"name": "ios-f2b-queue-interrupt",
                       "source": "cc-transcript", "cc_version": "2.1.292",
                       "description": "…", "input": "cc-transcript/ios-f2b-queue-interrupt/input.jsonl",
                       "expected": "cc-transcript/ios-f2b-queue-interrupt/expected.json",
                       "sha256": {"input": "…", "expected": "…"}}]}
  README.md          how to consume: decode expected.json; pin by commit + sha256
  cc-transcript/<case>/input.jsonl     scrubbed CC transcript (daemon test input only)
  cc-transcript/<case>/expected.json   {"live": false, "conversation": { …wire form, spec §8.1… }}
  cc-transcript/<case>/README.md       recorded where / how, what it covers
  mod-events/<case>/…                  reserved for U1-5 (mod event stream + expected)
```

`expected.json` example (abridged from `ios-f2b-queue-interrupt`):

```json
{ "live": false,
  "conversation": {
    "key": {"host_id": "", "provider": "claude", "session_id": "00000000-0000-4000-8000-000000000001"},
    "provider": "claude", "title": "…",
    "capabilities": {"source": "transcript", "text_streaming": "message", "thinking": "duration", "…": "…"},
    "usage": {"model": "claude-opus-5-5", "effort": "xhigh"},
    "turns": [
      { "id": "33b186ec-…", "index": 3, "started_at": 1791409527000, "ended_at": 1791409552000, "outcome": "done",
        "items": [
          {"type": "user", "id": "33b186ec-…", "at": 1791409527000, "source": "user", "text": "請寫一篇 1500 字的短篇故事，主題是燈塔"},
          {"type": "thinking", "id": "905082d8-…", "at": 1791409528000},
          {"type": "agent_text", "id": "658d5327-…", "at": 1791409529000, "markdown": "# 最後一盞燈\n\n…"} ] },
      { "id": "b873764d-…", "index": 4, "outcome": "done", "items": [
          {"type": "user", "id": "b873764d-…", "source": "queued", "text": "排隊訊息測試二：請回覆 QUEUED2"}, … ] },
      { "id": "a9e236ce-…", "index": 5, "outcome": "interrupted", "items": [
          {"type": "user", "source": "user", "text": "請寫一篇 1500 字的短篇故事，主題是風箏"},
          {"type": "thinking", …}, {"type": "agent_text", …},
          {"type": "system", "id": "5277d194-…", "kind": "interrupted"} ] } ] } }
```

Questions for iOS: (1) integer-ms times OK (it parses ISO 8601 today)? (2) flat items with a `type` discriminator OK for Swift `Codable`? (3) pinning by commit + MANIFEST sha256 and copying the directory with a script — or a git submodule? (4) is a model-level fixture enough for U2, or does iOS also want a render-structure fixture (fold groups, labels) — proposal: that is a U2 / U3 addition (`render.json` per case), not U1-4.
