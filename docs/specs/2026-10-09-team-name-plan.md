# Team name (plan)

> **Source:** spec `docs/specs/2026-10-09-team-name-spec.md` (N0–N3, D-N1…D-N11). Coordinator purdex-1f (`mlab/_9vyvs0`). Line numbers were re-checked against main @ 7dbcaae5 (9ac4da34 + the alpha.607 bump) when this plan was filed; re-check each before editing.

## Global constraints

- TDD: every task starts with a failing test. Each task is its own commit.
- Resource rules: run only the affected tests; `go test -race` only on the affected package, one at a time; full vitest only once before TN-2's merge, `--maxWorkers=3`, after asking the coordinator for the slot.
- No bump, no deploy (the coordinator does both). TN-1 ships with daemon + CLI + `pdx setup` (skill).
- The wire names in spec D-N6 are already promised to the interface lead and iOS: do not rename them.

## Shared contract

```go
// internal/team/wire.go
type CreateApprovalRequest struct { …; TeamName string `json:"team_name,omitempty"` }  // optional (D-N1)
type LeadPayload struct { …; TeamName string `json:"team_name"` }                     // always present, "" = none
type Grant struct { …; TeamName *string `json:"team_name,omitempty"` }                 // decide: nil = keep the requested name (D-N3);
                                                                                         // served: always set by this version
// internal/team/wire_team.go
type Team struct { …; TeamName string `json:"team_name"` }                            // current name (teams.team_name), always present
// internal/team/wire_roster.go
type TeamRoster struct { …; TeamName string `json:"team_name"` }                      // always present

// internal/team (new, e.g. wire_team_name.go)
// NormaliseTeamName trims white space; "" stays ""; anything else must pass
// peers.ValidateTitle. The error wraps ErrTeamNameInvalid.
func NormaliseTeamName(s string) (string, error)
```

`internal/peers` does not import `internal/team` (checked with `go list -deps`), so `internal/team` may import it; re-run the check after adding the import.

## TN-1 · daemon + CLI + skill

**Task 1 — the rule and the wire.**
Files: `internal/team/wire.go`, `wire_team.go`, `wire_roster.go`, new `internal/team/wire_team_name.go` (+ `_test`), `wire_test.go`, `wire_team_test.go`, `wire_roster_test.go`.
Tests: `TestNormaliseTeamName` table — `""`, `"   "` → `""`; `"  build  "` → `"build"`; 64 ASCII bytes ok, 65 fail; 21 CJK characters (63 bytes) ok, 22 (66) fail; `"a\x07b"`, `"a\nb"`, `"a\tb"` fail; invalid UTF-8 fail; the error wraps `ErrTeamNameInvalid`. JSON shapes: `LeadPayload` with an empty name still prints `"team_name":""`; `Grant` with nil `TeamName` prints no key, with `""` prints `"team_name":""`; `Team` and `TeamRoster` always print `team_name`.

**Task 2 — storage.**
Files: `internal/module/team/migrate.go` (`migrateTeamName`: `ensureColumn(db, "teams", "team_name", "TEXT NOT NULL DEFAULT ''")`, next to `migrateUsage` `:75`), `store.go` (call it after `migrateUsage`, `:95`), `team_store.go`: append `team_name` to `teamCols` (`:69`) **and** `&t.TeamName` to `teamDest` (`:82-85`; `scanTeam`, `ListLiveTeamsWithLeadUsage` in `team_store_members.go:85` and the member-to-team join at `:162` all scan through it, so none of them needs its own edit); the insert in `closeLeadApprovedIn` (`:341`) is `VALUES (?, ?, ?, ?, ?, ?, ?, 0, '')` with literal `ended_at`/`end_reason`, so with `team_name` last it becomes `VALUES (?, …, ?, 0, '', ?)` and the arg list gains `t.TeamName`. Tests must cover both the join read (a member's team carries the name) and the live list.
Tests: a team.db made with the old schema (no column) opens, gains the column, old rows read `""`; opening twice is a no-op; insert → read round trip of the name; `grant_json` of an old row (no `team_name` key) decodes with `TeamName == nil`.

**Task 3 — create.**
Files: `internal/module/team/handler.go` (create path `:118-170`; payload built `:162`, `requestHash` `:167`, compared `:186-191`).
Behaviour: lead kind only — `NormaliseTeamName(req.TeamName)`; an error → 400 `bad_request`, detail `team_name: …`, nothing stored; the normalised name goes into `LeadPayload.TeamName`. Other kinds ignore the field.
**Hash compatibility (added at filing).** `LeadPayload.team_name` is always present, so a payload hashed as-is changes its bytes for *every* request, named or not. `storedHash` is persisted and `pdx lead request` retries across a daemon restart with the same id, so a request opened before the deploy and retried after it would hit `id_conflict`. The hash therefore takes a payload in which an empty name is omitted (`hashPayload` = the same fields, `team_name,omitempty`), while the stored/served payload keeps the always-present form. Test: a row stored with the pre-change hash (old payload bytes) is answered, not `id_conflict`, when the same request is re-sent with no name; a named request still conflicts with a different name.
Tests: a valid name lands in the payload; a name with surrounding spaces is stored trimmed; an invalid name → 400 and no approval row; the same id re-sent with a different name → 409 `id_conflict`; the same id re-sent with the same name → the existing approval (idempotent).

**Task 4 — approve (manual and unattended).**
Files: `internal/module/team/unattended.go` (`leadGrantOf` `:48` sets `TeamName` to a pointer to a copy of the payload's; `leadTeamOf` `:58` sets `Team.TeamName` from the grant, nil-safe since `Grant.TeamName` is a `*string`; `teamNote` `:65` adds ` team %q` when the name is not empty; `unattendedGrant` goes through `leadGrantOf`, so D-N4 needs no further code, only the test), `handler.go` decide (`:388-411`: when `req.Grant != nil && req.Grant.TeamName != nil` → `NormaliseTeamName`, error → 400 `bad_request` and the approval stays pending; ok → `g.TeamName = &name`).
Tests (decide bodies as **raw JSON**, so key absence is real):
- `{"decision":"approve"}` → team name = requested;
- `{"decision":"approve","grant":{"max_members":2,"roots":[…]}}` (an older App) → requested name kept, max members 2;
- `"grant":{"team_name":"renamed"}` → `renamed` in `teams.team_name`, `grant.team_name` and `GET /api/team`;
- `"grant":{"team_name":""}` → cleared;
- `"grant":{"team_name":"a\u0007"}` → 400, approval still pending, no team;
- unattended on + a request with a name → the team has the requested name; the decision log line contains it.
**Mutation gates** (each must turn a test red, then revert): treat a nil `Grant.TeamName` as `""` (an older App wipes the name); skip `NormaliseTeamName` on decide; drop `team_name` from the `closeLeadApprovedIn` insert.

**Task 5 — roster and `GET /api/team`.**
Files: `internal/module/team/roster.go:80` (`TeamName: t.TeamName`), `team_store_members.go` only if `ListLiveTeamsWithLeadUsage` does not go through `teamCols`.
Tests: roster lists the name; a team without one lists `""`; `GET /api/team` from the lead shows `team.team_name`. (Corrected while implementing: `GET /api/team` is the lead's own view by design — `callerTeam` resolves `LiveTeamByLead` and answers a member 409 `not_lead`, P4-6 — so there is no member test; a member reads the name from the roster.)

**Task 6 — capability.**
Files: `internal/core/info_handler.go:45-50` (append `"team.name.v1"` last), `info_handler_test.go:401-409`.

**Task 7 — CLI.**
Anchors: `leadUsage` `lead.go:26`, flags `:87-88`, rejects `:101-106` (`reject` → exit 2), request built `:167`, `leadFinish` fallback `:288-293`; `runTeamCmd` `team_cmd.go:462`, `--json` return `:489`, table header `:500` (the `team:` line goes between them).
Files: `cmd/pdx/lead.go` (flag `--name`, usage line `pdx lead request --reason <text> [--name <team name>] [--max-members N] …`, client-side `NormaliseTeamName` → the same exit code the other request flags use for a bad value, `CreateApprovalRequest.TeamName`, `leadFinish`'s nil-grant fallback copies the payload's name), `cmd/pdx/team_cmd.go` (human view: `team: <name>` on its own line above the table when not empty; `--json` unchanged), tests in `lead_test.go`, `team_cmd_test.go`.
Tests: the request body pins `team_name`; an invalid `--name` exits before any HTTP call; no `--name` → no `team_name` key; `pdx team` prints the line only when there is a name, and prints it through the same sanitising the table cells use.

**Task 8 — skill.**
Files: `cmd/pdx/plugin/purdex/skills/pdx-team/SKILL.md:13`, `cmd/pdx/plugin/embed_test.go` (pin `--name`).
Text: the request line gains `--name "<team name>"`; one sentence after it: "Always give `--name`: a short name for the team's work (at most 64 bytes); it is shown at the front of the team's tab group, and the user may change it when approving." Keep both section headings unchanged (U23 tests find sections by them).

**Acceptance (coordinator, after deploy + `pdx setup`).** Throw-away session: `pdx lead request --reason test --name "驗收 team"` → App dialog shows the field prefilled (after TN-2) or approve via API with `grant.team_name` edited → `pdx team` first line `team: <edited>`, `GET /api/team/roster` shows it; unattended on → a second request's name kept; an older body without `team_name` keeps the requested name.

## TN-2 · App (data only)

**Task 1 — types and parsers.**
Files: `spa/src/lib/team/types.ts` (`LeadPayload.team_name?: string` — set only when the payload has a string `team_name`, so `undefined` means "daemon does not know names"; `Grant.team_name?: string`), `spa/src/lib/team/roster.ts` (`TeamRoster.team_name: string` `:40`; the guard `isTeamRoster` `:78` stays tolerant, but `parseRosterEvent` returns the parsed objects as-is `:99`, so a daemon that predates names would leave the field `undefined` under a `string` type — normalise in `parseRosterEvent` (`team_name: typeof t.team_name === 'string' ? t.team_name : ''`) and test it), tests `types.test.ts` (or the file that covers `leadPayloadOf`), `roster.test.ts`, `useTeamRosterStore.test.ts`.

**Task 2 — the dialog field.**
Files: `spa/src/components/ApprovalDialogHost.tsx` (lead branch `~304-340`: a text input "Team 名稱" above the member limit, shown only when `payload.team_name !== undefined`, prefilled; `nameOk` = trimmed UTF-8 length ≤ 64 bytes (`new TextEncoder().encode(s).length`) and every character matches `[\p{L}\p{M}\p{N}\p{P}\p{S} ]` (Go's `unicode.IsPrint`: letters, marks, numbers, punctuation, symbols and the ASCII space only — so U+3000, NBSP, zero-width and other `Cf`/`Zl`/`Zp` characters are refused like the daemon refuses them); `grantOk` includes it; `decide` adds `team_name: trimmed` to the grant only when the field is shown), `spa/src/locales/zh-TW.json`, `en.json` (flat snake_case keys like the neighbours `approval.dialog.max_members` / `roots_required` at `zh-TW.json:~2030`: `approval.dialog.team_name`, `team_name_placeholder`「未命名」, `team_name_invalid`「最多 64 bytes，且只能有可顯示字元」; both locales, `locale-completeness.test.ts` checks them), tests in `ApprovalDialogHost.test.tsx`.
State: keep the edited name wherever the dialog keeps its edited member limit, so minimize / restore behaves the same (`ApprovalDialogHost.minimize.test.tsx` pattern); add the name to that test.
Tests: field hidden for a payload without `team_name` and the grant has no `team_name` key; shown and prefilled when present (including `""`); editing sends the trimmed value; clearing sends `""`; 65 bytes, a control character, U+200B (zero-width space) or U+3000 each disable approve and shows the error; a daemon 400 on decide shows the existing error path; self relay shows no field.
**Mutation gates:** always send `team_name` (an older daemon payload would send `""`); drop the byte-length check; narrow the character rule back to `\p{Cc}`.

## Review and merge

Each PR: codex R1 + attacker (`--model gpt-5.6-sol`); critic only for a high or a disagreement. Report to the coordinator before merging. After TN-1 merges, tell the interface lead (`mlab/_b84f5i`) the merge sha; after TN-2 merges, the same.

## Known limits (codex plan review, job task-muzsuqhb-1uh8ot; accepted without code)

- **New CLI, old daemon.** `team_name` is dropped silently by a daemon that predates names (it ignores unknown JSON fields). `pdx` is one binary for daemon and CLI, so the window is "binary replaced, daemon not yet restarted". If such a named request is retried after the restart, the new hash includes the name and the answer is `id_conflict`; the lead re-requests with a new id.
- **Rollback.** Rolling the daemon back (alpha.606 backup) keeps the `team_name` column (old code ignores it) but a decide that carries `grant.team_name` creates an unnamed team. The App shows the field because the pending payload has `team_name`; no per-host capability probe is added for this (the existing probe, `unattended-support.ts`, is unattended-specific).
