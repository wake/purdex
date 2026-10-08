# Team name (spec)

Date: 2026-10-09. Coordinator: purdex-1f (`mlab/_9vyvs0`). Display: interface lead purdex-88-b8 (`mlab/_b84f5i`). Status: user decision final (§1); derived decisions agreed with the interface lead (§3).

## 1. User decision (do not reopen)

| # | Decision (2026-10-09, relayed by the interface lead with a screenshot of a Chrome tab group's name label) |
|---|---|
| N0 | 「team 應該要可以命名一個 team name，讓 lead 申請時一起交出來＋審核者可改，如果有 team name/team title，應該前面就可以放看看了」 |

Read as three rules: **N1** the lead gives the name with `pdx lead request`; **N2** whoever approves can change it in the approval dialog; **N3** the name is shown at the front of the team's group (tab group label, sidebar, team panel). N3 is the interface line's work; this spec only delivers the data.

## 2. Facts (main @ 9ac4da34)

- `pdx lead request` (`cmd/pdx/lead.go:81-124`) sends `CreateApprovalRequest{reason, max_members, roots, wait_s}` (`internal/team/wire.go:105-114`); the daemon normalises it into `LeadPayload{reason, max_members, roots}` (`wire.go:69-73`, `internal/module/team/handler.go:120-165`). The payload bytes are in `requestHash` (`handler.go:90-92,167`): the same request id with a different payload is `id_conflict`.
- Approve: `DecideRequest.Grant *Grant` (`wire.go:117-122`); nil → the payload's values, an edit falls back field by field (`handler.go:394-411`, `leadGrantOf` `unattended.go:48-54`). The team row is written in the approving transaction (`approve` `unattended.go:19-45` → `CloseLeadApproved`, `team_store.go:~286-345`), the grant as JSON in `teams.grant_json` (`team_store.go:34-44`). Unattended approval uses the same `leadGrantOf` (`unattended.go:105-117`).
- Columns are added with `ensureColumn` (`migrate.go:29-50`, example `migrateUsage` `:64-85`, called from `store.go:95`).
- `Team` (`internal/team/wire_team.go:141-151`) is served by `GET /api/team`; `TeamRoster` (`internal/team/wire_roster.go:43-49`) is built in `internal/module/team/roster.go:80` from `ListLiveTeamsWithLeadUsage` (`team_store_members.go:85`).
- The title rule is `peers.ValidateTitle` (`internal/peers/ref.go:63-83`): 1..64 bytes, valid UTF-8, every rune `unicode.IsPrint` (no control characters: a title is printed into terminal tables). `internal/peers` does not depend on `internal/team`.
- Capabilities: `internal/core/info_handler.go:45-50`, pinned by `TestHandleInfo_Capabilities` (`info_handler_test.go:401-409`).
- App dialog: `spa/src/components/ApprovalDialogHost.tsx` (lead fields `~304-340`, grant built in `decide` `:185-188`); payload parser `leadPayloadOf` (`spa/src/lib/team/types.ts:222-229`); roster parser `spa/src/lib/team/roster.ts`.
- Skill: `cmd/pdx/plugin/purdex/skills/pdx-team/SKILL.md:13` (the `pdx lead request` line), section pinned by `cmd/pdx/plugin/embed_test.go:512-520`.

## 3. Decisions

- **D-N1 · Optional on the wire, asked for by the skill.** A request without a name is valid (older CLIs, a lead that forgot). The skill tells every lead to give one.
- **D-N2 · One rule for a name.** Trim leading/trailing white space; the empty result means "no name"; anything else must pass `peers.ValidateTitle` (≤ 64 bytes, valid UTF-8, printable). CLI, daemon and App all apply it; the daemon is the authority (400 `bad_request` naming the field).
- **D-N3 · Approve.** `grant.team_name` **absent** → the requested name (an older App never wipes it); **present** → that value after D-N2, `""` clears it. Self relay and other kinds ignore it.
- **D-N4 · Unattended approval** keeps the requested name (88 (4)).
- **D-N5 · Storage.** `teams.team_name TEXT NOT NULL DEFAULT ''` is the team's **current** name; `grant_json` keeps `team_name` as the **approved** name, a record. They are equal in v1. **No rename after approval in v1** — pending, the interface lead asks the user; the column makes a later rename an additive change.
- **D-N6 · Wire (told to the interface lead 2026-10-09; iOS and the prototype align to it).**
  - request `team_name` (optional);
  - `LeadPayload.team_name` — **always present**, `""` = none (its presence tells a client the daemon knows names);
  - `Grant.team_name` — in a decide body optional (D-N3); in a served grant always present for approvals decided by this version;
  - `Team.team_name` (top level, `GET /api/team`) and `TeamRoster.team_name` — always present, `""` = none.
- **D-N7 · Capability** `team.name.v1`, appended last.
- **D-N8 · Idempotency.** The name is part of the payload, so a retry with the same id and a different name is `id_conflict` — no new code.
- **D-N9 · CLI display.** `pdx lead request` prints the grant JSON (name included). `pdx team` prints one line `team: <name>` above the table when the name is not empty; `--json` carries it as-is.
- **D-N10 · App (data only).** The approval dialog shows an editable name field for a lead request **when the payload has `team_name`** (a daemon that knows names), prefilled with the requested name; it sends `grant.team_name` only when the field was shown. The roster store parses `team_name`. Where the name is displayed is the interface line's (N3).
- **D-N11 · Logs.** The decision log line names the team (`%q`) when it has a name.

## 4. Phases (each PR ≤ 800 lines or ≤ 20 files)

| Phase | Content | Deploy |
|---|---|---|
| **TN-1** | wire, `teams.team_name` column, create / decide / unattended, `GET /api/team`, roster, capability, `pdx lead request --name`, `pdx team` line, skill | daemon + CLI + `pdx setup` (skill) |
| **TN-2** | App approval dialog field, payload / grant / roster types and parsers | SPA (main checkout fast-forward) |

TN-2 can merge before or after TN-1: it keys on the payload's `team_name`, so against an older daemon it shows nothing new.

## 5. Coordination

- The interface lead displays the name (tab group label, sidebar, team panel) and adds the same field to the iOS approval card; it asked for the wire names and the merge time (§3 D-N6 answered 2026-10-09).
- No mod change. The skill change ships with `pdx setup`.
