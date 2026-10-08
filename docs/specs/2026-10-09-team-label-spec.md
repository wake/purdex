# Team label — a short name next to the team name (spec + plan)

Date: 2026-10-09. Coordinator: purdex-1f (`mlab/_9vyvs0`). Display: interface lead purdex-88-b8 (`mlab/_tn9uwa`). Builds on team name (`docs/specs/2026-10-09-team-name-spec.md`, #2016／#2023, shipped 608／610).

## 1. User decisions (do not reopen)

| # | Decision (2026-10-09) |
|---|---|
| L0 | 「team 名稱分兩種：標籤短名：五字內，正常名稱：（顯示在右邊 panel）」 |
| L1 | 「英文可以長一點，總之是中文 5 個字寬左右」 |
| L2 | 「不要用 A線：資 這種，乾脆就 A 線 / 資源線 / 資源派工」——短名是一個有意義的短稱呼，不是把長名截斷。 |

Read as: a team has a **label** (short, for the tab group label at the front of the team's tabs) and a **name** (the existing `team_name`, shown in the right-hand team panel). The label is limited by **display width**: about five Chinese characters, so English may be longer.

## 2. Decisions

- **D-L1 · Width rule.** A label, after trimming white space, is at most **10 display columns**: East Asian wide characters and emoji count 2, other characters 1, combining marks and zero-width characters 0 — exactly `runeWidth` in `cmd/pdx/cellwidth.go`. It must also pass the name rule (`peers.ValidateTitle`: valid UTF-8, printable). Examples: 「租約」 4 columns; 「A線派工」 6; 「資源租約派工」 12 → refused; `lease-p1` 8; `resource-lease` 14 → refused.
- **D-L2 · Shared width code.** Move `runeWidth`／`cellWidth`／`cutWidth` from `cmd/pdx/cellwidth.go` into an internal package both the daemon and the CLI import (e.g. `internal/textwidth`), unchanged; `cmd/pdx` keeps calling it. The App mirrors the same table in TypeScript, with a shared fixture file of strings and widths that both test suites read (`testdata/textwidth/cases.json`), so the two cannot drift.
- **D-L3 · Optional on the wire; never a mechanical cut (L2).** The label is optional on the wire (older CLIs), but the skill asks every lead for a meaningful one (D-L9). When a team is approved with no label (none requested, none set by the approver), the daemon takes **only the part of the name before its first separator** — `：` `:` `／` `/` `｜` `|` `－` `-` `—`, surrounding white space trimmed — **if that part is non-empty and passes D-L1**; otherwise the label is `""` and the interface line decides what to show. It **never cuts a name in the middle**. Examples: 「A 線：資源租約＋派工回報」 → 「A 線」; 「介面線」 (no separator, fits) → 「介面線」; 「資源租約與派工回報」 (no separator, 18 columns) → `""`; 「：開頭」 (empty before the separator) → `""`.
- **D-L4 · Request and approve** (same shape as `team_name`, D-N3): `pdx lead request --label "<短名>"` → request `team_label`; `LeadPayload.team_label` always present (`""` = none requested); decide `grant.team_label` **absent** → the requested label, **present** → that value after D-L1 (`""` = derive per D-L3). Unattended approval uses the requested label (or the derived one).
- **D-L5 · Stored and served.** `teams.team_label TEXT NOT NULL DEFAULT ''` holds the **final** label (explicit or derived) — added with `ensureColumn` (team.db is live). `grant_json` records `team_label` as approved (explicit or `""`). `Team.team_label` (GET /api/team) and `TeamRoster.team_label` always present = the final label. Teams created before this version read `""`.
- **D-L6 · Idempotency.** The label is part of the payload: same request id with a different label → `id_conflict`. The hash leaves an empty label out (as `hashPayload` already does for an empty name), so a retry from an older CLI across the upgrade still matches.
- **D-L7 · Capability** `team.label.v1`, appended last.
- **D-L8 · CLI display.** `pdx team`'s first line becomes `team: <name> ［<label>］` (the label part only when it differs from the name; nothing when both are empty). The approval JSON printed by `pdx lead request` carries `team_label`.
- **D-L9 · Skill.** The `pdx lead request` line gains `--label "<短名>"`; one sentence: "Always give `--label`: a short, meaningful name of your own for the tab group label, about five Chinese characters (10 display columns), e.g. `A 線`, `資源線`, `資源派工` — a summary, never the name cut short."
- **D-L10 · App (data and the dialog only).** The approval dialog gets a second field 「標籤（短名）」 under 「Team 名稱」, shown only when the payload has `team_label`; prefilled with the requested label; while it is empty, its placeholder shows the label D-L3 would take from the (edited) name, or 「（無）」 when it would take nothing; validated by D-L1 (width counter shown, approve disabled over 10); the grant carries `team_label` only when the field was shown. The roster store parses `team_label` (`''` when absent). Where the label and the name appear is the interface line's.

## 3. Plan

Constraints: TDD; mutation gates; ≤ 800 lines or ≤ 20 files; team.db is live, every schema change is a migration; tests that format or compare times or widths run the same in any time zone.

**TL-1 · daemon + CLI + skill** (deploy: daemon + CLI + `pdx setup`)
1. Move the width code to `internal/textwidth` (pure move first, byte-identical bodies, its own commit) + `testdata/textwidth/cases.json` + a test reading it.
2. `team.NormaliseTeamLabel(s) (string, error)` and `team.DeriveTeamLabel(name) string` in `internal/team`, next to `NormaliseTeamName`. Table tests incl. 10／11 columns, CJK, emoji, combining marks, every D-L3 example, each separator, a fitting first part vs. a too-wide one.
3. Wire: `CreateApprovalRequest.TeamLabel` (omitempty), `LeadPayload.TeamLabel` (always), `Grant.TeamLabel *string`, `Team.TeamLabel`, `TeamRoster.TeamLabel`; JSON shape tests.
4. Storage: `ensureColumn(teams, team_label, TEXT NOT NULL DEFAULT '')`, `teamCols`／`teamDest`／insert; migration test from the live schema (read mlab's `team.db` schema with `sqlite3 -readonly`).
5. Create (validate, 400 naming the field; hash omits an empty label), decide (absent／present／`""` → derive; invalid → 400, approval stays pending), unattended (requested or derived), roster, `GET /api/team`.
6. Capability `team.label.v1`; `pdx lead request --label`; `pdx team` first line; skill line + embed pin.
Mutation gates: absent `grant.team_label` treated as `""`; width check counting runes instead of columns; derive cutting a too-wide first part to 10 columns instead of returning `""`; hash including an empty label.

**TL-2 · App** (deploy: main checkout fast-forward)
1. `spa/src/lib/textwidth.ts` mirroring D-L1, tested against `testdata/textwidth/cases.json`.
2. Types／parsers: `LeadPayload.team_label?`, `Grant.team_label?`, `TeamRoster.team_label`.
3. Dialog field per D-L10, with tests: hidden without the payload key; prefill; placeholder follows the edited name; 11 columns disables approve; clearing sends `""`; older payload → no `team_label` in the grant.
Mutation gates: always sending `team_label`; width by `.length`.

**Review.** One codex plan review of this file (with the team name spec), then per PR R1 + attacker as usual. After TL-1 merges, tell the interface lead the sha and the wire names.
