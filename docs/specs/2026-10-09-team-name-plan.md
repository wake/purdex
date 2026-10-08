# Team name (plan)

> **Source:** spec `docs/specs/2026-10-09-team-name-spec.md` (N0–N3, D-N1…D-N11). Coordinator purdex-1f (`mlab/_9vyvs0`). Line numbers are main @ 9ac4da34; re-check each before editing.

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
Files: `internal/module/team/migrate.go` (`migrateTeamName`: `ensureColumn(db, "teams", "team_name", "TEXT NOT NULL DEFAULT ''")`), `store.go` (call it after the schema Execs, next to `migrateUsage`), `team_store.go` (`teamCols`, `scanTeam`, the insert in `closeLeadApprovedIn` writes `team_name`).
Tests: a team.db made with the old schema (no column) opens, gains the column, old rows read `""`; opening twice is a no-op; insert → read round trip of the name; `grant_json` of an old row (no `team_name` key) decodes with `TeamName == nil`.

**Task 3 — create.**
Files: `internal/module/team/handler.go` (create path `~120-165`).
Behaviour: lead kind only — `NormaliseTeamName(req.TeamName)`; an error → 400 `bad_request`, detail `team_name: …`, nothing stored; the normalised name goes into `LeadPayload.TeamName` (so into `requestHash`). Other kinds ignore the field.
Tests: a valid name lands in the payload; a name with surrounding spaces is stored trimmed; an invalid name → 400 and no approval row; the same id re-sent with a different name → 409 `id_conflict`; the same id re-sent with the same name → the existing approval (idempotent).

**Task 4 — approve (manual and unattended).**
Files: `internal/module/team/unattended.go` (`leadGrantOf` sets `TeamName` to a copy of the payload's; `leadTeamOf` sets `Team.TeamName` from the grant; `teamNote` adds ` team %q` when the name is not empty), `handler.go` decide (`~394-411`: when `req.Grant != nil && req.Grant.TeamName != nil` → `NormaliseTeamName`, error → 400 `bad_request` and the approval stays pending; ok → `g.TeamName = &name`).
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
Tests: roster lists the name; a team without one lists `""`; `GET /api/team` from the lead and from a member both show `team.team_name`.

**Task 6 — capability.**
Files: `internal/core/info_handler.go:45-50` (append `"team.name.v1"` last), `info_handler_test.go:401-409`.

**Task 7 — CLI.**
Files: `cmd/pdx/lead.go` (flag `--name`, usage line `pdx lead request --reason <text> [--name <team name>] [--max-members N] …`, client-side `NormaliseTeamName` → the same exit code the other request flags use for a bad value, `CreateApprovalRequest.TeamName`, `leadFinish`'s nil-grant fallback copies the payload's name), `cmd/pdx/team_cmd.go` (human view: `team: <name>` on its own line above the table when not empty; `--json` unchanged), tests in `lead_test.go`, `team_cmd_test.go`.
Tests: the request body pins `team_name`; an invalid `--name` exits before any HTTP call; no `--name` → no `team_name` key; `pdx team` prints the line only when there is a name, and prints it through the same sanitising the table cells use.

**Task 8 — skill.**
Files: `cmd/pdx/plugin/purdex/skills/pdx-team/SKILL.md:13`, `cmd/pdx/plugin/embed_test.go` (pin `--name`).
Text: the request line gains `--name "<team name>"`; one sentence after it: "Always give `--name`: a short name for the team's work (at most 64 bytes); it is shown at the front of the team's tab group, and the user may change it when approving." Keep both section headings unchanged (U23 tests find sections by them).

**Acceptance (coordinator, after deploy + `pdx setup`).** Throw-away session: `pdx lead request --reason test --name "驗收 team"` → App dialog shows the field prefilled (after TN-2) or approve via API with `grant.team_name` edited → `pdx team` first line `team: <edited>`, `GET /api/team/roster` shows it; unattended on → a second request's name kept; an older body without `team_name` keeps the requested name.

## TN-2 · App (data only)

**Task 1 — types and parsers.**
Files: `spa/src/lib/team/types.ts` (`LeadPayload.team_name?: string` — set only when the payload has a string `team_name`, so `undefined` means "daemon does not know names"; `Grant.team_name?: string`), `spa/src/lib/team/roster.ts` (`TeamRoster.team_name: string`, `''` when absent or not a string; the guard stays tolerant), tests `types.test.ts` (or the file that covers `leadPayloadOf`), `roster.test.ts`, `useTeamRosterStore.test.ts`.

**Task 2 — the dialog field.**
Files: `spa/src/components/ApprovalDialogHost.tsx` (lead branch `~304-340`: a text input "Team 名稱" above the member limit, shown only when `payload.team_name !== undefined`, prefilled; `nameOk` = trimmed UTF-8 length ≤ 64 bytes (`new TextEncoder().encode(s).length`) and no `\p{Cc}`; `grantOk` includes it; `decide` adds `team_name: trimmed` to the grant only when the field is shown), `spa/src/locales/zh-TW.json`, `en.json` (`approval.dialog.teamName*`: label, placeholder「未命名」, error「最多 64 bytes，不能有控制字元」), tests in `ApprovalDialogHost.test.tsx`.
State: keep the edited name wherever the dialog keeps its edited member limit, so minimize / restore behaves the same (`ApprovalDialogHost.minimize.test.tsx` pattern); add the name to that test.
Tests: field hidden for a payload without `team_name` and the grant has no `team_name` key; shown and prefilled when present (including `""`); editing sends the trimmed value; clearing sends `""`; 65 bytes or a control character disables approve and shows the error; a daemon 400 on decide shows the existing error path; self relay shows no field.
**Mutation gates:** always send `team_name` (an older daemon payload would send `""`); drop the byte-length check.

## Review and merge

Each PR: codex R1 + attacker (`--model gpt-5.6-sol`); critic only for a high or a disagreement. Report to the coordinator before merging. After TN-1 merges, tell the interface lead (`mlab/_b84f5i`) the merge sha; after TN-2 merges, the same.
