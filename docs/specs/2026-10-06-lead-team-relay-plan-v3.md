# Lead / member / team and context relay — Implementation Plan v3 (P4, P4b, P4c, P6, P7)

> **Status (2026-10-07):** draft, not yet through codex review. Written against origin/main `d57ca13b` (alpha.581; spec U20 merged in PR #1838). Every `file:line` below was read on that commit. P8a-2 is still in flight on its own branch; the one PR here that touches the mod (P6-6) and the one that touches `register.js` again (P7-2) rebase onto it.
> **Source:** spec `docs/specs/2026-10-06-lead-team-relay-spec.md` (U1–U20, M1–M25), the "Coordinator decisions" and "Fix notes" of plan v1 and plan v2 (binding), and the line's memory `kickoff_lead_team_relay.md` (what shipped, review rulings, pitfalls).
> **Relation to v1 and v2:** v1 shipped P0–P3 (alpha.513–527), v2 shipped P2c, P5a, P5b and P8a-1a…1d (alpha.529–579). v3 schedules what is left of spec §12: **P4, P4b, P4c, P6, P7**. P8b is not scheduled and not written here. v3 uses a **compact format**: contracts, rules, tests and mutation gates, but no full code blocks. v2's code blocks went stale after review, so v3 does not repeat that mistake. The implementer writes the code test-first from these contracts.

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development (recommended) or superpowers:executing-plans. Each PR is TDD, one task per commit. Before you start a PR, re-verify its `file:line`s against main, because another line moves fast.

**Goal:** a lead spawns, lists and kills members, on whichever paired host the rule picks for the repo. The lead relays a member when it decides to; the member's mod writes, clears and seeds. A crash mid-relay is reconciled from frames. The lead is told when a member is idle past 70% or was auto-compacted.

---

## Global constraints

- **Size.** Each PR is ≤ 800 lines of diff **and** ≤ 20 files. The sections below are already split. A pure-move PR (P4-0, P6-0) is proved by per-declaration byte comparison, not by diffstat. Pure-move and bump PRs get no codex review (repo CLAUDE.md).
- **Order.** Merge in the order of the PR table. Exceptions: P6-0 and P4c-1 have no upstream dependency and may land early. P6-8 needs only P6-2b.
- **Tests.**
  - Go: `go test ./<pkg>/ -race` and `gofmt -l` (empty output). `make lint` fails on unformatted Go since `d71853ba`.
  - SPA: `cd spa && npx vitest run <path>`, `pnpm run lint`, `npx tsc -p tsconfig.app.json --noEmit`, `pnpm run build`.
  - Mod: `claude plugin validate cmd/pdx/plugin/purdex`, `claude plugin test cmd/pdx/plugin/purdex`, `go test ./cmd/pdx/plugin/`.
  - `go build ./cmd/pdx/` dirties the tracked repo-root `pdx` binary (#1699). Run `git checkout -- pdx` before committing.
- **Commits.** English messages, ending with the attribution line the session's system reminder gives. Parallel subagents in one worktree commit with `git commit --only <files>`.
- **Copy.** Daemon and CLI strings are exactly as the spec quotes them (U16: 接力 / 切換, never 交接). SPA strings go in `spa/src/locales/{en,zh-TW}.json`, pinned by `locale-completeness.test.ts`.
- **Errors on the CLI.** Every `pdx spawn|kill|team|relay` API error prints `pdx <cmd>: <detail> <code>`, with the code as the **last** stderr token (the shape `cmd/pdx/relay.go:131-161` ships). Exit codes follow spec §14. The mapping is pinned per PR.
- **Times** are unix ms. The one exception is `rate_limits.*.resets_at`, which stays in unix seconds as Claude Code sends it (M13).
- **Secrets.** Never print a token. Acceptance scripts write the header into a 0600 file (`scratchpad/accept-*.sh` pattern).
- **Restart-aware CLI.** Use `cmd/pdx/daemonclient` with an attempt timeout of 35 s. A create POST carries a client UUID and `Idempotent()`. A third consecutive hung poll exits 20 (`cmd/pdx/lead.go:201-226`).
- **Platform.** Clients are Purdex.app (Electron, renderer = `spa/`) and the iOS App (another repo). Nothing here is for a plain browser. A new tab-hosted SPA component follows the repo CLAUDE.md checklist.
- **Deploy tags** (each PR names one or more):
  - **daemon:** swap `bin/pdx` and restart the daemon.
  - **CLI:** swap `bin/pdx`, no restart.
  - **setup:** the mod or skill changed, so re-run `pdx setup --agent cc` on each host that has the mod installed (mlab does).
  - **SPA:** fast-forward the main checkout, which reloads through HMR.
  - **none.**
- **Binding deploy asks** (purdex-d3, kept in memory):
  - Before every deploy, ask d3 and check for running workers. If any are running, stop and report.
  - Before every `pdx setup --agent cc`, copy `~/.claude/settings.json` into the scratchpad (chmod 600, never print it). Afterwards, jq-compare every key except `env.CLAUDE_CODE_PLUGIN_DIRS` and report any diff, including `hooks`.
- **Throwaway sessions only.** Acceptance uses a throwaway target, never self or a peer. Afterwards, `~/.config/pdx/hooklocks/` must hold no leftover flag.

## Review focus

1. **Approval → team in one transaction, and a team never ends mid-relay.** → P4-2 (`CloseLeadApproved`, the team-end guard), P4-3 (lead and member move in the `cleared` transaction).
2. **A spawn survives a daemon restart without launching twice.** A start timeout kills only the session this op created (generation-guarded). → P4-5.
3. **Cross-host trust.** Only `principal.HostID == lead_host_id` with a matching `team_id` may kill or relay. `AllowTeam` gates writes. The admin token is refused on the peer team routes. An older daemon (404 **or** plain-text 403) reads as `remote_unsupported`. → P4c-2, P4c-3, P6-7.
4. **The relay lock is checked before the flag is removed.** It never outlives the op: lowered at `cleared` and at every terminal state. → P6-3.
5. **#1735.** An op stuck past `claimed` is ended from frames. A cleared report is accepted only for the op's own process. → P6-4.
6. **The member relay is safe to spoof.** The claim is accepted only for the target session. The control message is consumed only with its marker, from a peer. A mod older than protocol 2 is refused (`relay_unsupported`). → P6-2b, P6-6.

---

## PR table

| PR | Content | Needs | ≈ lines / files | Deploy |
|---|---|---|---|---|
| **P4-0** | Pure move: split `relay_store.go` (#1703) | main | 680 moved / 5 | none |
| **P4-1** | Wire contract of teams, members, spawn and kill, plus the U20 validators | main | 330 / 2 | none |
| **P4-2** | `teams` table; team created in the approve transaction; `already_lead`; team end in the sweeper | P4-0, P4-1 | 560 / 7 | daemon |
| **P4-3** | `team_members` table; `member_cannot_lead`; `relayRole` → lead / member; lead and member moves in the `cleared` transaction; `team_id` on `pdx lead request` stdout | P4-2 | 600 / 9 | daemon + CLI |
| **P4-4** | Host config `team.member_command`; `spawn_ops` table; launch-line builder | P4-1 | 560 / 9 | daemon |
| **P4-5** | Spawn runner; `POST /api/team/spawns`; boot reconciliation of spawns | P4-3, P4-4 | 780 / 7 | daemon |
| **P4-6** | `POST /api/team/kill`, `GET /api/team`; persisted usage on team rows; members marked gone | P4-5 | 560 / 8 | daemon |
| **P4-7** | CLI `pdx spawn|kill|team`; both U20 reminders; the brief; skill | P4-6 | 720 / 6 | CLI + setup |
| **P4b-1** | Parse `rate_limits`; account fingerprint; per-host weekly reading; `GET /api/peers/team/usage` | P4-1 | 600 / 10 | daemon |
| **P4b-2** | Repo inventory (scan, `developable`, canonical key); `team.repo_roots|min_weekly_remaining|preferred_host`; `GET /api/team/repos`, `GET /api/peers/team/repos` | P4-4, P4b-1 | 680 / 10 | daemon |
| **P4b-3** | Peers `HostCaller` (call path X on host Y); `GET /api/team/hosts` | P4b-2 | 400 / 6 | daemon |
| **P4b-4** | Selection rule; `pdx spawn --repo/--host`; the choice line; `no_host_for_repo`; `remote_unsupported` until P4c | P4b-3, P4-7 | 560 / 10 | daemon + CLI + setup |
| **P4c-1** | `PeerHost.AllowTeam`; `pdx peers host allow-team`; Hosts toggle | main | 380 / 12 | daemon + CLI + SPA |
| **P4c-2** | Member host side: `remote_members`; `POST /api/peers/team/spawn|kill|lead-moved|end`; policy; role of a remote member | P4b-4, P4c-1 | 760 / 12 | daemon |
| **P4c-3** | Lead host side: forwarding spawn and kill; `forwarded_ops` with retry; lead-moved and end outbox; remote members in `pdx team` | P4c-2 | 790 / 14 | daemon (both hosts) + CLI |
| **P6-0** | Pure move: split `cmd/pdx/relay.go` (#1730) | main | 560 moved / 3 | none |
| **P6-1** | Daemon notifier: virtual peer, in-process peers sender, auto-reply; lead-handover notice to members | P4-3 | 600 / 8 | daemon |
| **P6-2a** | Member-relay wire; `relay_ops.pid/pane_id`; persisted mod `hello`; `cleared` binding by op pid | P6-1 | 420 / 10 | daemon |
| **P6-2b** | `POST /api/team/relays` and the control message; `POST /api/relay/ops/{id}/claim`; op long-poll | P6-2a, P4-6 | 650 / 6 | daemon |
| **P6-3** | Relay lock: decide answer before flag removal; daemon raises and lowers the flag; shared flock helpers | P6-2b | 620 / 10 | daemon + CLI |
| **P6-4** | Claim (60 s) and whole-op (15 min) timeouts; boot and sweeper reconciliation from frames (#1735); completion and failure notices | P6-3 | 700 / 6 | daemon |
| **P6-5** | CLI `pdx relay <ref> [--wait]` and `pdx relay claim` | P6-0, P6-2b | 520 / 4 | CLI |
| **P6-6** | Mod: control message, claim, write with 協作關係 facts; protocol `VERSION` 2; acceptance recipe | P6-5, P6-4 (P8a-2 if merged) | 560 / 5 | daemon + setup |
| **P6-7** | Cross-host member relay: `POST /api/peers/team/relay`; forwarding; proxied wait; notices across hosts | P6-6, P4c-3 | 650 / 8 | daemon (both hosts) |
| **P6-8** | SPA: restart-confirm line `N 個接力進行中` | P6-2b | 120 / 5 | SPA |
| **P7-1** | Agent-status accessor; the 70% idle notice, armed once and re-armed after a relay or a drop | P6-4 | 620 / 8 | daemon |
| **P7-2** | Member auto-compact report: route, CLI, mod; skill notice texts | P7-1 | 420 / 8 | daemon + CLI + setup |

Total ≈ 14 300 lines across 26 PRs. Spec order P4 → P4b → P4c → P6 → P7 is kept. The local member relay (P6-1…P6-6) depends on no part of P4b or P4c, so it could ship before them (open question 1).

---

## Shared contracts (read once; the PR sections refer to them)

**Exit codes** (spec §14) for the commands added here:

| Code | Cases |
|---|---|
| 0 | spawn done; kill done; team listed; relay accepted; claim accepted; `--wait` ended `done` or hit its bound (op JSON printed) |
| 1 | runtime/API error; spawn `failed` with any reason other than `member_start_timeout`; relay `failed{handoff_incomplete}`; brief not sent after a successful spawn (stdout still carries the member) |
| 2 | usage, including `--model` / `--effort` invalid (U20), both `--brief` and `--brief-file`, an unknown `pdx relay` word |
| 12 | relay op `cancelled` (`--wait`) |
| 13 | `not_lead`, `team_full`, `cwd_outside_grant`, `not_your_member`, `relay_unsupported`, `relay_open`, `bad_transition`, `not_your_op` (new), `no_host_for_repo`, `host_not_allowed`, `remote_unsupported`, `already_lead`, `member_cannot_lead` |
| 14 | `member_start_timeout` (spawn), `remote_unreachable`, relay `failed{member_unresponsive|member_gone}` (`--wait`) |
| 20 / 21 | daemon unreachable through the grace / plain 404 |

**`team.db` tables added in v3** (each `CREATE … IF NOT EXISTS` in `OpenStore`, `internal/module/team/store.go:31-72`; later columns go through one `ensureColumn` helper, P4-6):

| Table | PR | Key | Purpose |
|---|---|---|---|
| `teams` | P4-2 | `id` (= the approving request's id) | one row per approval; `lead_session_id` follows relays |
| `team_members` | P4-3 | `spawn_op` | the lead host's members (local and, from P4c-3, remote) |
| `spawn_ops` | P4-4 | `id` (client UUID) | persisted spawn steps (spec §9.3) |
| `remote_members` | P4c-2 | `spawn_op` | the member host's record of members whose lead is elsewhere (spec §7.4 (c)) |
| `forwarded_ops` | P4c-3 | `id` | the lead host's forwarded spawn/kill/relay and its lead-moved/end outbox |
| `mod_hello` | P6-2a | `session_id` | the persisted `hello` (mod presence survives a restart) |

**`Start` order after v3** (`internal/module/team/module.go:243-263`): MkdirAll relay dir → boot lease grace → start the notifier's virtual peer (P6-1) → `reconcileRelays` (P5a + P6-4) → re-raise relay locks (P6-3) → resume running spawns (P4-5) and forwarded ops (P4c-3) → `OnSubscribe` → sweepers → repo scan (P4b-2, async). `Stop` cancels `stopCtx`, waits for the sweepers **and** the spawn and forward goroutines (`spawnWG`), then closes the virtual peer.

**Notices** (P6-1 sends all of them from the daemon's virtual peer; texts pinned in `internal/module/team/notice.go`). In bracket forms `ref` is the bare 6 characters; elsewhere it is `_xxxxxx`:

| Notice | Text | PR |
|---|---|---|
| control | `[pdx-relay:control] op=<op id>` | P6-2b |
| lead handover (to members) | `[pdx team] 你的 lead 已換手：<new address> [<new ref>]（舊 ref 仍可用）` | P6-1 |
| relay done (to lead) | `[pdx team] <old ref> 已由 <new ref> 接手（接力檔 <path>）` | P6-4 |
| relay failed / cancelled (to lead) | `[pdx team] <ref> 接力失敗（<reason>）；接力檔 <path>` / `[pdx team] <ref> 接力已取消（<reason>）` (plan-defined: spec only says "the lead is told") | P6-4 |
| 70% idle (to lead) | `[pdx team] member <address> [<ref>]「<title>」已用 <N>%，目前閒置。要接力請執行：pdx relay _<ref>` | P7-1 |
| auto-compacted (to lead) | `[pdx team] <ref> 已自動壓縮（lead 未在 70% 時接力）` | P7-2 |
| reply to the daemon | `這是 pdx daemon 的自動通知，不會讀取回覆` | P6-1 |

---

# Phase P4 — teams, spawn, kill, team on this host (spec §6.1 step 4, §6.2, §7.1–§7.3, §8.4, §9.3, §10, U8, U10, U13, U20)

## PR P4-0 — pure move: split `relay_store.go` (#1703)

**Goal.** Remove the SRP debt flagged in plan v2's fix notes (P5a-1a R2 and P5a-3a) before P4-3 adds the team and member moves to the `cleared` transaction. This is a separate PR because a pure move gets no codex review and is proved by bytes.

**Files.**
- Modify `internal/module/team/relay_store.go` (454 lines). It keeps the error vars, `relaySchema`, `relayCols`, `scanRelayOp`, `CreateRelayOp`, `GetRelayOp`, `OpenRelayOpBySession`, `RelayOpByRequest` and `ListActiveRelayOps`.
- Create `relay_store_report.go`: `relayTransitions`, `checkLineage`, `RelayReport`, `ReportResult` and its constants, `ReportRelay`.
- Create `relay_store_lineage.go`: `lineageRow`, `PreviousRefs`, `ChainRoots`, plus the `var _ team.LineageReader` assertion.
- Create `relay_store_prefs.go`: `SetSelfRelayPaused`, `SelfRelayPaused`.
- Create `relay_store_retention.go`: `ListUnprunedRelayOps`, `MarkRelayPruned`.

**Contract.** No identifier, signature, comment or statement changes. Import blocks are recomputed per file.

**Proof.** A throwaway `go/parser` script, outside the repo, maps every top-level declaration (doc comment included, `fset` offsets) of the old file and of the five new files by name. It byte-compares each pair and asserts that the two sets of names are equal. Paste its output into the PR body.

**Tests.** `go test ./internal/module/team/ -race`, `go vet ./internal/module/team/`, `gofmt -l`. No new tests.

**Size.** About 340 lines moved out (≈ 680 diff lines), 5 files.

**Risks.** None at runtime. If the declaration-set comparison is not exact, the PR is not a pure move. Fix it rather than explaining the difference.

## PR P4-1 — wire contract: teams, members, spawn, kill, the U20 validators

**Goal.** Fix the P4 contract once, the way P5a-0 did for relays, so P4-2…P4-7 build on stable types. Spec §7.1–§7.3, U20 (a)(b)(c).

**Files.** Create `internal/team/wire_team.go` and `internal/team/wire_team_test.go`.

**Interfaces.**

```go
const ( // APIError.Error, 409 unless noted
	ErrNotLead = "not_lead"; ErrTeamFull = "team_full"
	ErrCwdOutsideGrant = "cwd_outside_grant"; ErrNotYourMember = "not_your_member"
)
const ( // SpawnOp.Reason when failed
	SpawnReasonStartTimeout = "member_start_timeout" // exit 14; every other reason exit 1
	SpawnReasonCreateFailed = "session_create_failed"; SpawnReasonLaunchFailed = "launch_failed"
	SpawnReasonNameTaken = "tmux_name_taken"; SpawnReasonAbandoned = "abandoned"
)
type MemberState string // "active" | "killed" | "gone"
type SpawnState string  // "running" | "done" | "failed"
const ( // SpawnOp.Step: how far a running spawn got (persisted, spec §9.3)
	StepAccepted = "accepted"; StepSessionCreated = "session_created"
	StepLaunched = "launched"; StepRegistered = "registered"
)
const (
	TeamEndLeadGone = "lead_gone"
	SpawnRegisterS = 20 // §7.2 step 5
	SpawnPollWaitS = 25 // POST /api/team/spawns answers within this
	DefaultMemberCommand = "claude --dangerously-skip-permissions" // §7.2 step 4
	MemberBriefPrefixFmt = "[pdx team] 你是 %s 的 member（team %s）。接力由 lead 決定，不要自己接力。"
	ReminderAtActivation = "已成為 lead。預設模型不固定：spawn member 時請依工作需求用 --model 指定（例：--model sonnet 做機械性修改、--model opus 做設計）。"
	ReminderNoModel = "提醒：沒有指定 --model，member 會用這台主機當下的預設模型。"
)
var Efforts = []string{"low", "medium", "high", "xhigh", "max"} // M25
func ValidModel(s string) bool       // ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}(\[1m\])?$
func ValidEffort(s string) bool      // one of Efforts, exact case
func SpawnTmuxName(opID string) string // "tm-" + first 10 hex digits of the op id with dashes removed

type Team struct {
	ID, HostID, LeadSessionID, LeadRef string // json: id, host_id, lead_session_id, lead_ref
	Grant     Grant  `json:"grant"`
	RequestID string `json:"request_id"`
	CreatedAt int64  `json:"created_at"`
	EndedAt   int64  `json:"ended_at,omitempty"`
	EndReason string `json:"end_reason,omitempty"`
}
type MemberContext struct { // the member's statusline reading (P1), persisted from P4-6
	UsedPercentage *float64 `json:"used_percentage"`; Window int `json:"window"`
	ModelID string `json:"model_id,omitempty"`; Effort string `json:"effort,omitempty"`; At int64 `json:"at"`
}
type Member struct {
	SessionID, Ref, Address, TeamID, HostID string // json: session_id, ref, address, team_id, host_id
	Title string `json:"title,omitempty"`; Cwd string `json:"cwd"`; TmuxSession string `json:"tmux_session"`
	State MemberState `json:"state"`
	Model string `json:"model,omitempty"`; Effort string `json:"effort,omitempty"` // asked at spawn (U20)
	Context *MemberContext `json:"context,omitempty"`
	SpawnOp string `json:"spawn_op"`; CreatedAt int64 `json:"created_at"`
}
type SpawnRequest struct { // POST /api/team/spawns
	ID, OriginInbox string // json: id (UUID v4, idempotency key), origin_inbox
	Cwd, Title, Model, Effort string `json:",omitempty"` // cwd absolute; model/effort optional (U20)
}
type SpawnOp struct {
	ID, TeamID, HostID string; State SpawnState; Step string; Reason string `json:"reason,omitempty"`
	Cwd, Title, Model, Effort, TmuxSession string
	LeadAddress string  `json:"lead_address,omitempty"` // for the brief's first line (§7.2)
	Member      *Member `json:"member,omitempty"`       // done only
	CreatedAt, UpdatedAt int64
}
type KillRequest struct{ OriginInbox, Target string } // POST /api/team/kill
type TeamView struct { Team Team `json:"team"`; Members []Member `json:"members"` } // members never null
```

All JSON keys are snake_case as listed. P4b-4 adds `SpawnRequest.Repo/Host`, `SpawnOp.Choice` and two codes. P4c-3 adds `host_not_allowed` and `remote_unreachable`. Nothing is added ahead of its phase.

**Behaviour rules.**
- `ValidModel` accepts `opus`, `sonnet`, `fable`, `claude-opus-5-5` and `opus[1m]`. It refuses `""`, `a b`, `'x'`, `x;y`, `$(x)`, `-x`, a 65-character name and `opus[2m]`.
- `ValidEffort` is case-sensitive. `High` is refused.
- `SpawnTmuxName` is deterministic. Given a non-UUID it still returns `tm-` plus the first 10 hex-ish characters. Callers pass validated UUID v4s only.

**Tests.**
- `TestWireTeam_LiteralsArePinned`: every code, reason, state, step, limit and both reminder strings.
- `TestWireTeam_JSONShapes`: full and minimal encodings of `Team`, `Member`, `SpawnOp` and `TeamView`. `members:[]` is never `null`, and `used_percentage` nil encodes differently from 0.
- `TestValidModel_Table`, `TestValidEffort_Table`.
- `TestSpawnTmuxName_FromUUID`.

**Mutation gates.**
- Drop `(\[1m\])?` → the `opus[1m]` case goes red.
- Allow a space in the class → `a b` goes red.

**Size.** 330 lines, 2 files.

**Risks.** None.

## PR P4-2 — `teams`: the team is born in the approve transaction; `already_lead`; the team ends with its lead

**Goal.**
- Spec §6.2: "Approval creates the team (§7.1) in the same transaction".
- `409 already_lead` at create.
- §7.1: "It ends when the lead's conversation ends … including a manual /clear".
- This closes plan v1's staged deferral (Coordinator decisions on P2a, codex finding 1).

**Files.**
- Create `internal/module/team/team_store.go`, `team_store_test.go`.
- Modify `internal/module/team/store.go:67-70`: run `teamSchema` after `relaySchema`.
- Modify `internal/module/team/handler.go`: create path `:183-204`, decide path `:358-397`.
- Modify `internal/module/team/sweeper.go:44-53`.
- Create `handler_team_test.go`, `sweeper_team_test.go`.

**Interfaces.**
- Schema:
  ```sql
  CREATE TABLE IF NOT EXISTS teams (id TEXT PRIMARY KEY, host_id TEXT NOT NULL,
    lead_session_id TEXT NOT NULL, lead_ref TEXT NOT NULL, grant_json TEXT NOT NULL,
    request_id TEXT NOT NULL UNIQUE, created_at INTEGER NOT NULL,
    ended_at INTEGER NOT NULL DEFAULT 0, end_reason TEXT NOT NULL DEFAULT '');
  CREATE UNIQUE INDEX IF NOT EXISTS teams_one_live_per_lead ON teams (lead_session_id) WHERE ended_at = 0;
  ```
- Store:
  - `var ErrLeadHasTeam = errors.New(…)`.
  - `func (s *Store) CloseLeadApproved(id string, c Close, t team.Team) (team.Approval, bool, error)`. One transaction: `closeRowIn(tx, id, c, "", 0)`, then, only when it changed one row, `INSERT INTO teams`. A violation of `teams_one_live_per_lead` rolls both back and returns `ErrLeadHasTeam`. The returned row and `won` are what `closeWhere` returns (`store.go:229-239`).
  - `LiveTeamByLead(sessionID) (team.Team, bool, error)`.
  - `ListLiveTeams() ([]team.Team, error)`.
  - `EndTeam(id, reason string, at int64) (bool, error)`, guarded `WHERE id=? AND ended_at=0`.
- HTTP:
  - `POST /api/team/approvals` gains `409 already_lead`.
  - `POST /api/team/approvals/{id}/decide` on an approve of a lead row answers as today, and the team exists when it returns 200. It answers `409 already_lead` (row left open) if the origin already leads a live team.

**Behaviour rules.**
1. **Team id = the approving request's id** (deviation 1). `teams.request_id` holds the same value, so the spec's row shape stands and `pdx lead request` can print the team id without a second call (P4-3). Grant = the closed row's grant (`handler.go:370-382`).
2. **Create order** (under `createMu`, `handler.go:173`): idempotent replay by id, then `request_open`, then `already_lead` (`LiveTeamByLead(origin.SessionID)`). An ended team does not count.
3. **The approve path** runs `m.closeWith(id, func(){ return m.store.CloseLeadApproved(id, close, t) })`. The winner still broadcasts `closed` once (`module.go:323-340`). Deny, cancel, timeout and abandon are unchanged.
4. **Team end** runs in `tick()` inside the `checkLive` block, **before** the `ListOpen` early return at `sweeper.go:54-61`:
   - For each live team whose `LiveSession(lead_session_id)` is false **and** whose lead has no relay op in `claimed|writing|written` (`OpenRelayOpBySession`): `EndTeam(lead_gone)` and one log line.
   - The relay guard is why a relay's own `/clear` does not end the team. The old session id leaves the registry about 0.6 s after `/clear` (measured, P5a-2b fix notes) while the op is still `written`. The `cleared` transaction (P4-3) then moves `lead_session_id`.
   - A registry read error answers "live" (`peers/origin_resolver.go`), so it never ends a team.
5. Members are untouched when a team ends (D4: they become ordinary sessions; P4-3's role reads require a live team).

**Tests.**
- `TestStore_CloseLeadApprovedCreatesTheTeamInOneTx`: approved row plus team row. A second call loses the CAS and makes no second team.
- `TestStore_CloseLeadApprovedRollsBackWhenTheLeadHasATeam`: the row is still `open` and no team is added.
- `TestDecide_ApproveCreatesTheTeamWithTheEditedGrant`.
- `TestDecide_AlreadyLeadLeavesTheRowOpen`.
- `TestCreate_AlreadyLeadIs409_EndedTeamDoesNotCount`.
- `TestTick_EndsTheTeamOfAGoneLead`.
- `TestTick_KeepsTheTeamWhileItsLeadIsRelaying`: op `written` with the session not live → kept. Op terminal → ended on the next liveness tick.
- `TestTick_EndsTeamsEvenWithNoOpenApproval`.

**Mutation gates.**
- Insert the team after the commit, outside the transaction → `…RollsBackWhenTheLeadHasATeam` red (the row closes approved with no team).
- Drop the relay guard → `…WhileItsLeadIsRelaying` red.
- Put the end check after the `ListOpen` early return → `…WithNoOpenApproval` red.

**Size.** 560 lines, 7 files.

**Risks.**
- **Deploy.** An approved lead request now creates a team. A session whose team is live then gets `already_lead` until it exits. Batch P4-2 with P4-3 if possible.

## PR P4-3 — `team_members`; `member_cannot_lead`; roles; moves in `cleared`; team id on stdout

**Goal.** Spec §6.2 `member_cannot_lead`, §8.4 ("lead_session_id moves … the member row's session id moves (same transaction)"), U13 (a member has no switch), the binding d3 ask on `relayRole`, and §6.1 step 4 (team id on stdout).

**Files.**
- Modify `internal/module/team/team_store.go` and its test.
- Modify `internal/module/team/relay_handler.go:33-65` (`relayRole`, `selfRelayState`) and `:69-137` (hello and self use the error).
- Create `relay_role_test.go`.
- Modify `internal/module/team/relay_store_report.go` (the `cleared` branch of `ReportRelay`, after the lineage insert, today `relay_store.go:286-293`) and `relay_store_test.go`.
- Modify `internal/module/team/handler.go` (create).
- Modify `cmd/pdx/lead.go:274-298` and `cmd/pdx/lead_test.go`.

**Interfaces.**
- Schema:
  ```sql
  CREATE TABLE IF NOT EXISTS team_members (spawn_op TEXT PRIMARY KEY, team_id TEXT NOT NULL,
    host_id TEXT NOT NULL, session_id TEXT NOT NULL, ref TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '', cwd TEXT NOT NULL, tmux_session TEXT NOT NULL,
    tmux_id TEXT NOT NULL DEFAULT '', tmux_instance TEXT NOT NULL DEFAULT '',
    pane_id TEXT NOT NULL DEFAULT '', pid INTEGER NOT NULL DEFAULT 0,
    proc_start TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '',
    effort TEXT NOT NULL DEFAULT '', state TEXT NOT NULL,
    created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
  CREATE INDEX IF NOT EXISTS team_members_team ON team_members (team_id, state);
  CREATE UNIQUE INDEX IF NOT EXISTS team_members_one_active ON team_members (session_id) WHERE state = 'active';
  ```
- Store: `InsertMember(memberRow) error` (idempotent on `spawn_op`); `ActiveMemberInLiveTeam(sessionID) (memberRow, team.Team, bool, error)`; `MembersOf(teamID) ([]memberRow, error)`; `SetMemberState(spawnOp, state, at) error`.
- `func (m *Module) relayRole(sessionID string) (string, error)`: `"lead"` when `LiveTeamByLead`, `"member"` when `ActiveMemberInLiveTeam`, else `"none"`. **A store error is an error** (deviation 14): hello, self and begin answer 500, so the mod treats it as unavailable and nothing relays (fail closed; spec §8.7 (d)).
- Inside `ReportRelay`'s `cleared` transaction, after the lineage insert:
  ```sql
  UPDATE teams SET lead_session_id=?, lead_ref=? WHERE lead_session_id=? AND ended_at=0;
  UPDATE team_members SET session_id=?, ref=?, updated_at=? WHERE session_id=? AND state='active';
  ```
- `leadGrantOutput` (`cmd/pdx/lead.go:276-279`) gains `TeamID string \`json:"team_id,omitempty"\`` = `ap.ID` for an approved lead row. stdout is still one JSON line. The U20 activation reminder is **not** here: it ships with `pdx spawn` in P4-7 (U20 (b): "never names a command that does not exist yet").
- `POST /api/team/approvals` gains `409 member_cannot_lead`. `cmd/pdx/lead.go:58-62` already maps it to 13.

**Behaviour rules.**
1. **Roles are read live** on every hello, self and begin (spec §8.7 (a)).
2. **A member's hello** answers `role:"member", self_relay:"off"` (`selfRelayState`, `relay_handler.go:42-45`). Its `begin` and its `self on|off` are `409 member_relay_is_leads`. Its `self status` is 200 with `member:true`.
3. **A lead** reads the `self_lead` switch (`relay_handler.go:50-53`).
4. **A member of an ended team** is `none` (D4).
5. **The moves are in the lineage transaction.** If `checkLineage` or the insert fails, nothing moves (spec §8.4). The title move stays its own meta.db transaction (plan v2 decision).
6. **Report to purdex-d3** when this PR ships: `relayRole` now returns `member`, and the pinned tests below exist (binding ask).

**Tests.**
- `TestRelayHello_MemberAnswersSelfRelayOff`, `TestRelayBegin_MemberIsRefused`, `TestRelaySelf_OnOffInAMemberIsRefusedStatusSaysMember`. These are the binding pair the d3 ask names.
- `TestRelayHello_LeadReadsTheLeadSwitch`: `self_lead=false`, `self_solo=true` → lead off, solo on.
- `TestRelayRole_MemberOfAnEndedTeamIsNone`.
- `TestRelayRole_StoreErrorIs500`.
- `TestCreate_MemberCannotLead`.
- `TestRelayStore_ClearedMovesLeadAndMemberInTheSameTx`.
- `TestRelayStore_ClearedThatFailsLineageMovesNothing`.
- `TestRelayStore_ClearedDoesNotReviveAnEndedTeam`.
- `TestLeadFinish_ApprovedPrintsTeamID`.

**Mutation gates.**
- `relayRole` back to `"none"` → `…MemberAnswersSelfRelayOff` red.
- The UPDATEs moved after the commit → `…FailsLineageMovesNothing` red.
- Drop `ended_at=0` → `…DoesNotReviveAnEndedTeam` red.

**Size.** 600 lines, 9 files.

**Risks.**
- **Spawn race.** The mod's first hello can land before P4-5 stores the member row, because hello is sent at `session.start` and the row is written after registration. That hello answers `none`. This is harmless: `begin` re-reads the role and refuses, and the mod then sets `s.role='member'` (`register.js:380`). P7-2 lets the daemon decide, for the same reason.

## PR P4-4 — host config `team.member_command`; `spawn_ops`; the launch line

**Goal.** Spec §7.2 step 4 (`team.member_command`, `--plugin-dir`), U20 (a) (the daemon appends `--model '<m>'` and `--effort <e>`), §9.3 (spawn steps persisted).

**Files.**
- Create `internal/module/hostconfig/team.go` and `team_test.go`.
- Modify `internal/module/hostconfig/handler.go:48-67` (`emptyFor`, the GET map) and `module.go:35,46` (register the reader, `PUT /api/hostconfig/team`).
- Create `internal/module/team/spawn_store.go` and `spawn_store_test.go`.
- Create `internal/module/team/launch.go` and `launch_test.go`.
- Modify `internal/module/team/store.go` (schema).

**Interfaces.**
- Host config (pattern of `relay.go`):
  - `KeyTeam = "team"`, `TeamSettingsKey = "hostconfig.team-settings"`.
  - `type TeamSettings struct{ MemberCommand string \`json:"member_command"\` }`; `DefaultTeamSettings`.
  - `type TeamSettingsReader interface{ TeamSettings() (TeamSettings, error) }`.
  - `normalizeTeam(raw)`: unknown field → error; `member_command` non-blank, ≤ 512 bytes, no control characters or newline. An explicit `null` is an error, as in relay.
  - GET answers the default for a never-written key.
- Schema:
  ```sql
  CREATE TABLE IF NOT EXISTS spawn_ops (id TEXT PRIMARY KEY, team_id TEXT NOT NULL,
    host_id TEXT NOT NULL, request_hash TEXT NOT NULL, origin_session_id TEXT NOT NULL,
    cwd TEXT NOT NULL, title TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '',
    effort TEXT NOT NULL DEFAULT '', tmux_name TEXT NOT NULL,
    tmux_id TEXT NOT NULL DEFAULT '', tmux_instance TEXT NOT NULL DEFAULT '',
    pane_id TEXT NOT NULL DEFAULT '', step TEXT NOT NULL, state TEXT NOT NULL,
    reason TEXT NOT NULL DEFAULT '', session_id TEXT NOT NULL DEFAULT '',
    launched_at INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
  CREATE INDEX IF NOT EXISTS spawn_ops_running ON spawn_ops (team_id) WHERE state = 'running';
  ```
- Store:
  - `CreateSpawnOp(op spawnRow, hash string) (stored spawnRow, storedHash string, inserted bool, err error)`, with `ON CONFLICT DO NOTHING` (the approvals pattern, `store.go:155-165`).
  - `GetSpawnOp`.
  - `AdvanceSpawnOp(id, fromStep string, upd spawnUpdate) (bool, error)`, a CAS on `step` and `state='running'`.
  - `FailSpawnOp(id, reason string, at int64) (bool, error)`.
  - `ListRunningSpawnOps() ([]spawnRow, error)`.
  - `CountRunningSpawns(teamID string, exceptID string) (int, error)`.
- `func launchLine(memberCommand, pluginDir, model, effort string) (string, error)`:
  - Result: `<member_command> --plugin-dir '<dir>'[ --model '<m>'][ --effort <e>]`, with no trailing newline (the runner adds `\n`).
  - It re-validates with `team.ValidModel` / `ValidEffort`.
  - It uses a local `shellQuote` (`'` → `'\''`), the same rule as `internal/agent/cc/statusline.go:85`, which is unexported.

**Behaviour rules.**
1. `member_command` is the host owner's text and is not quoted. Every value the daemon appends is quoted, except the effort enum.
2. `--plugin-dir` is always appended (spec §7.2). Even with the global install the plugin loads once (M5).

**Tests.**
- `TestNormalizeTeam_DefaultsUnknownFieldNullAndBlank`.
- `TestGetHostConfig_TeamDefault`.
- `TestLaunchLine_QuotesModelAndPluginDir`: `opus[1m]` → `--model 'opus[1m]'`, and a plugin dir with a space or a `'`.
- `TestLaunchLine_NoModelNoEffort`.
- `TestLaunchLine_RefusesBadModelOrEffort`.
- `TestSpawnStore_CreateIsIdempotentByIDAndHash`.
- `TestSpawnStore_AdvanceIsACASOnStep`.
- `TestSpawnStore_CountRunningExcludesSelf`.

**Mutation gates.**
- Unquoted model → `opus[1m]` red.
- `AdvanceSpawnOp` without the step guard → the CAS test red.

**Size.** 560 lines, 9 files.

**Risks.** None. The GET gains a `team` field, which is additive. The SPA ignores unknown host-config fields; it only reads its own keys.

## PR P4-5 — the spawn runner, `POST /api/team/spawns`, boot reconciliation

**Goal.** Spec §7.2 steps 1–6 (minus the CLI and the brief), §9.3 "Spawn ops not finished", §15 "Spawn" (limit, roots, symlink escape, retry after restart, start timeout).

**Files.**
- Create `internal/module/team/spawn.go` (runner), `spawn_handler.go` (route), `spawn_test.go`, `spawn_handler_test.go`, `spawn_fakes_test.go`.
- Modify `internal/module/team/module.go`:
  - `Dependencies` adds `"session"` (`:165`).
  - `Init` resolves the new services (`:170-211`).
  - The route (`:215-234`), the `Start` resume (`:243-263`), `Stop` waits on `spawnWG`.

**Interfaces.**
- Narrow seams, each type-asserted at `Init` on what already exists:
  - `sessionCreator{ SessionExists(string) bool; ValidateCwd(string) error; CreateSession(name, cwd string) (*session.SessionInfo, error) }`, from `session.RegistryKey` (`internal/module/session/provider.go:19-27,65`).
  - `tmuxOps{ ActivePaneMetadata(ctx, name) (tmux.TmuxPaneMetadata, error); SendKeysIfInstanceTarget(id, window, inst string, keys ...string) (bool, error); KillSessionIfInstance(id, inst string) (bool, error) }`, from `c.Tmux` (`internal/tmux/executor.go:51,84-92`).
  - `frameReader{ LiveSessions(ctx, agentType string) ([]agent.TerminalSession, error) }`, from `agent.TerminalSessionsKey` (`terminal_sessions.go:15,47-54`).
  - `hostconfig.TeamSettingsReader` (P4-4).
  - `TitleSetter{ Claim(sessionID, label string, now time.Time) (store.PeerLabel, error) }`, type-asserted on the value `WithTitles` was given (`*store.PeerLabelStore`, `internal/store/peer_label.go:63`).
- `POST /api/team/spawns`, body `team.SpawnRequest` → **200 `team.SpawnOp`**, in state `running|done|failed`. This is create-or-join: the handler waits up to `SpawnPollWaitS` for the op to leave `running` (deviation 13). Errors:
  - 400 `bad_request`: id not UUID v4, invalid model or effort, title fails `ipeers.ValidateTitle`, cwd relative or missing.
  - 400 `origin_unknown`.
  - 409 `not_lead`, `team_full`, `cwd_outside_grant`, `id_conflict`.
  - 503 `not_ready`.
- Seams: `spawnPoll` (250 ms), `spawnBudget` (`SpawnRegisterS`), `spawnSleep`. All injectable.

**Behaviour rules.**
1. **Validation order.** Stopping → 503. Then decode, id, model, effort, title, cwd shape. Then the origin by inbox (a registry error is 503). Everything below runs under `createMu`.
2. **Idempotency.**
   - `hash` = sha256(origin session id, cwd, title, model, effort).
   - Same id and hash → join the existing op. Same id, other hash → 409 `id_conflict`.
   - A join never checks the limits again.
3. **Checks, in order** (all under `createMu`):
   - **Lead.** `LiveTeamByLead(origin)`, else 409 `not_lead`.
   - **Limit.** Live members plus running spawn ops of the team, this one excepted, must be < `grant.max_members`, else 409 `team_full`. A live member is an active row whose session is live, or whose relay is in flight. This is "the limit counts only live members" (spec §13 D4).
   - **cwd.** `filepath.EvalSymlinks(cwd)`, else 400 "cwd does not exist". `ValidateCwd(resolved)`. Under at least one granted root, each root `EvalSymlinks`'d and compared with `filepath.Rel` (no `..`), else 409 `cwd_outside_grant`.
   - Then insert the op (`step accepted`, `tmux_name = SpawnTmuxName(id)`), release `createMu`, and start `go m.runSpawn(id)` under `spawnWG`.
4. **The runner.** Each step is a CAS from the recorded step, and every terminal transition wakes the op's waiters (`addWaiter`/`wake`, `module.go:409-445`).
   - **accepted**
     - Tmux session absent: `CreateSession(tmux_name, cwd)`, then record `tmux_id`, `tmux_instance`, and `pane_id` from `ActivePaneMetadata` → `session_created`. A create error → `failed{session_create_failed}`.
     - Tmux session present but no `tmux_id` recorded: a crash between create and record, so adopt it (the name derives from this op's id).
     - Present on a brand-new op: `failed{tmux_name_taken}`.
   - **session_created**
     - Ensure the plugin tree: if `<data_dir>/cc-plugin/purdex/hooks/register.js` is missing and `agentcc.PluginSource != nil`, call `agentcc.ExtractPlugin(…)` (`internal/agent/cc/plugin.go:35,63`). It is never called on an existing tree (open question 9). A failure is logged.
     - Send `launchLine(...)+"\n"` with `SendKeysIfInstanceTarget(tmux_id, "0", tmux_instance, …)`.
     - Not sent or an error → kill (generation-guarded), then `failed{launch_failed}`.
     - Sent → record `launched_at` → `launched`.
   - **launched**
     - Poll until `launched_at + 20 s`. A **verified** frame on `pane_id` with a session id (`LiveSessions(ctx,"cc")`), **and** `ResolveOriginBySession(sid)` live → `registered` (record `session_id`).
     - Past the budget: `KillSessionIfInstance(tmux_id, tmux_instance)`, then `failed{member_start_timeout}`. The slot frees at once because the op is no longer running.
   - **registered**
     - `InsertMember` (idempotent): ref, pid, `proc_start`, pane, tmux, cwd, title, model and effort as asked, state active.
     - `TitleSetter.Claim(sid, title)` when a title was given. A failure is logged, not fatal.
     - Op `done` with `Member` filled. `LeadAddress` = the origin's `Address`.
5. **Boot.** `Start` lists running ops and runs `runSpawn` for each (spec §9.3):
   - `session_created` or `launched` with the tmux session gone → `failed{abandoned}`.
   - `launched` past its budget → kill, then `member_start_timeout`.
   - A tmux server restart shows up as an instance mismatch: the send is refused (`launch_failed`) and the kill declines. Nothing of a stranger is touched (I3, as take-to-terminal does, `internal/module/nex/take_to_terminal.go:323-337`).
6. **Stop.** The runner observes `stopCtx`. It leaves the op `running` at its recorded step, for the next boot.

**Tests** (fakes: `tmux.FakeExecutor`, plus fakes for the four seams):
- `TestSpawn_LaunchLineHasPluginDirModelEffortAndRegisters`: keys = `claude --dangerously-skip-permissions --plugin-dir '<dd>/cc-plugin/purdex' --model 'opus[1m]' --effort high\n`; member row; title claimed; op done; `lead_address` set.
- `TestSpawn_WithoutModelSendsNoModelFlag`.
- `TestSpawn_Refusals`: `not_lead`; `team_full` counting a running op; `cwd_outside_grant`; a symlink inside a root pointing outside it → `cwd_outside_grant`; bad model, effort or title → 400; missing cwd → 400.
- `TestSpawn_StartTimeoutKillsAndFreesTheSlot`: fake clock; kill with the recorded id and instance; with `max_members=1` the next spawn is accepted.
- `TestSpawn_ReplayJoinsTheSameOp`: two POSTs, one `CreateSession`; another body → 409.
- `TestSpawn_RestartMidOpOpensNothingTwice`: op left at `session_created` → a second `Module` over the same team.db → `Start` → one send, no second create, done.
- `TestSpawn_RestartPastBudgetKillsAndFails`.
- `TestSpawn_TmuxRestartBetweenStepsTouchesNothing`.
- `TestSpawn_StopLeavesTheOpRunning`.

**Mutation gates.**
- Drop running ops from the count → the concurrent `team_full` case red.
- Drop the step CAS → the restart test sees two sends, red.
- Skip `EvalSymlinks` on the cwd → the symlink-escape case red.
- Skip the kill on timeout → the kill assertion red.

**Size.** 780 lines, 7 files. If it runs over, move boot resume and its two tests to P4-6.

**Risks** (see open questions 2 and 9 for the measurements):
- **Trust dialogs.** A member launched into a directory never opened before may stop at Claude Code's folder-trust or bypass-mode dialog before `SessionStart`. The spawn then times out. **Needs measuring**; see open question 2.
- **No Purdex hooks.** A host without the Purdex hooks never produces a frame, so every spawn there times out. The CLI says so in P4-7.

## PR P4-6 — kill, `pdx team`'s route, persisted usage, gone members

**Goal.** Spec §7.3 (`pdx kill`, `pdx team` with context, model and effort; U20 (e)), §8.5 "Persist it for teams only". Persisted usage sits here by plan v2's coordinator decision ("persisted usage on team rows: P4"); spec §12 lists it under P7 (deviation 9).

**Files.**
- Create `internal/module/team/team_handler.go` and its test.
- Create `internal/module/team/migrate.go` (`ensureColumn`) and its test.
- Modify `team_store.go` (usage columns), `sweeper.go` (persist, mark gone), `module.go` (routes).

**Interfaces.**
- `func ensureColumn(db *sql.DB, table, column, decl string) error`, reading `PRAGMA table_info` (pattern `internal/store/frames.go:141`). Used here and by P6-2a, P7-1.
- Columns:
  - `team_members`: `usage_pct REAL`, `usage_window INTEGER NOT NULL DEFAULT 0`, `usage_model TEXT NOT NULL DEFAULT ''`, `usage_effort TEXT NOT NULL DEFAULT ''`, `usage_at INTEGER NOT NULL DEFAULT 0`.
  - `teams`: the same five as `lead_usage_*`.
- `GET /api/team?origin_inbox=<inbox>` → 200 `team.TeamView` | 400 `origin_unknown` | 409 `not_lead` | 503.
- `POST /api/team/kill`, body `team.KillRequest` → 200 `team.Member` (state `killed`) | 409 `not_lead` / `not_your_member` | 503.
- `func (m *Module) matchMember(t team.Team, target string) (memberRow, bool, error)`. Targets:
  - `_xxxxxx`, `xxxxxx`;
  - `<host>/_xxxxxx`, `<host>/<name>`, `<host>/<name> [xxxxxx]`. In P4 the host must be this host (self alias or host id, from `c.Cfg.Peers` under `CfgMu`); P4c-3 extends it to remote hosts.
  - It matches the team's members by current ref, then by lineage (`store.PreviousRefs()[member.session_id]`), then by live registry name.

**Behaviour rules.**
1. **Kill.** `KillSessionIfInstance(tmux_id, tmux_instance)`. Any outcome (killed, already gone, generation moved) sets `state=killed`. An already-killed member answers 200 (idempotent). Worktrees are the lead's (spec §7.3).
2. **Team view.** One row per member of the team, in any state, ordered by `created_at`.
   - `Context` is the live `m.usage.ContextUsage(sid)` when present, else the persisted columns when `usage_at > 0`, else absent. Model and effort come from that reading: blank until the member's first statusline (U20 (e)).
   - `Address` is the live origin's `Address` (`ResolveOriginBySession`), else `<self alias>/_<ref>`.
3. **Sweeper, liveness tick.**
   - Copy each active member's reading, and each live lead's, into its row when the reading's `At` is newer.
   - Mark an active member `gone` when `!LiveSession(sid)` and no relay op of its session is in flight. The same relay guard as P4-2.

**Tests.**
- `TestTeam_ListsMembersWithModelEffortAndContext`.
- `TestTeam_NotLeadIs409`.
- `TestTeam_ServesThePersistedReadingAfterARestart`.
- `TestKill_OnlyTheLeadsOwnMember`: another team's member → `not_your_member`; a non-lead → `not_lead`.
- `TestKill_ByTheOldRefAfterARelay`.
- `TestKill_IsIdempotentAndGenerationGuarded`.
- `TestTick_MarksAGoneMemberButNotOneMidRelay`.
- `TestEnsureColumn_AddsOnceKeepsData`.

**Mutation gates.**
- Drop the `team_id` filter in `matchMember` → cross-team kill red.
- Drop the lineage tier → the old-ref kill red.
- Serve only live readings → the after-restart test red.

**Size.** 560 lines, 8 files.

**Risks.** None beyond P4-5's.

## PR P4-7 — CLI: `pdx spawn`, `pdx kill`, `pdx team`; both U20 reminders; the brief; the skill

**Goal.** Spec §6.1 step 4 (U20 (b)), §7.2 (CLI side, the brief), §7.3, §10 "As a lead" (U20 (d)), §14, §15 U20 cases.

**Files.**
- Create `cmd/pdx/team_cmd.go` and `cmd/pdx/team_cmd_test.go`.
- Modify `cmd/pdx/main.go:45-46` (the commands line) and `:54-93` (dispatch `spawn`, `kill`, `team`).
- Modify `cmd/pdx/lead.go:282-298` (the stderr reminder after the grant) and `cmd/pdx/lead_test.go`.
- Modify `cmd/pdx/plugin/purdex/skills/pdx-team/SKILL.md`.

**Interfaces.**

```
pdx spawn [--cwd <dir>] [--title <t>] [--model <m>] [--effort <e>] [--brief-file <f> | --brief <text>] [--config <path>]
pdx kill <ref> [--config <path>]
pdx team [--json] [--config <path>]
```

**Behaviour rules.**
1. **Grammar errors exit 2 before any config load:**
   - unknown flag; extra arguments;
   - `--model` failing `team.ValidModel`; `--effort` failing `team.ValidEffort`;
   - both briefs given; `--brief-file` unreadable; `--title` failing `ipeers.ValidateTitle`;
   - `kill` without exactly one target.
2. **Inbox.** `CLAUDE_CODE_MESSAGING_SOCKET`; unset → exit 1, as `pdx lead request` does (`lead.go:143-147`). `--cwd` defaults to the process cwd, made absolute (open question 6).
3. **Without `--model`.** stderr gets `team.ReminderNoModel`, then the spawn goes on and the exit code is unchanged (U20 (c)).
4. **The spawn loop.**
   - Mint a UUID v4. `POST /api/team/spawns` with `Idempotent()`.
   - While the op is `running`, POST the same body again (the daemon joins). Three consecutive hung attempts exit 20.
   - `done` → stdout one JSON line `{"ref","address","tmux_session","session_id","host_id","spawn_op"}`.
   - `failed` → `member_start_timeout` exits 14, with the stderr hint `member 沒有在 20 秒內啟動（這台主機需要 Purdex hooks：pdx setup --agent cc）`. Other reasons exit 1.
5. **The brief**, only after `done`.
   - `POST /api/peers/send` through `Once`, never replayed: `{to: member.address, origin_inbox: inbox, text: fmt.Sprintf(MemberBriefPrefixFmt, op.lead_address, op.team_id) + "\n" + brief}`.
   - On failure: stderr `pdx spawn: member 已開啟，但 brief 沒送出：<err>；請用 pdx msg send <address> 手動送`, exit 1, stdout already printed (open question 14).
6. **Kill.** `POST /api/team/kill` → stdout the member JSON, exit 0. 409s exit 13.
7. **Team.** `GET /api/team?origin_inbox=…`. `--json` prints the `TeamView` as is. Otherwise a table, with `-` for unknown:
   ```
   ADDRESS  REF  TITLE  STATE  CTX  MODEL  EFFORT  CWD  TMUX
   ```
   409 `not_lead` exits 13.
8. **`pdx lead request`.** On approval, stdout stays the grant JSON alone. stderr gets `team.ReminderAtActivation` once (U20 (b)).
9. **Skill, "As a lead".** Replace today's wrong `pdx spawn --root <dir> [--repo <name>]` (`SKILL.md:19`) with the synopsis above. Add U20 (d): choose each member's model and effort for its task (`--model sonnet` for mechanical work, `--model opus` for design), because the host default is not fixed, and check `pdx team` to see what each member actually runs. Keep the worktree advice (U10). P4b-4 adds `--repo` / `--host`.

**Tests.**
- `TestSpawnCmd_UsageErrorsExit2`: model with a space, `'`, `;`, `$(`; effort `High` or `ultra`; both briefs; unreadable file.
- `TestSpawnCmd_NoModelPrintsTheReminderAndExits0`.
- `TestSpawnCmd_ModelAndEffortReachTheRequest`.
- `TestSpawnCmd_PostsAgainWhileRunning`.
- `TestSpawnCmd_StartTimeoutExits14`.
- `TestSpawnCmd_RefusalsExit13CodeLast`.
- `TestSpawnCmd_BriefFromTheLeadInboxWithThePrefix`.
- `TestSpawnCmd_BriefFailureExits1KeepsStdout`.
- `TestKillCmd_*`, `TestTeamCmd_TableShowsModelAndEffort`.
- `TestLeadRequest_ApprovedPrintsTheReminderOnStderrOnlyTheGrantOnStdout`.
- `TestDispatch_SpawnKillTeam`.

**Mutation gates.**
- Drop the CLI model check (daemon only) → the exit-2 test red.
- Print the reminder on stdout → the lead test red.

**Acceptance** (mlab, throwaway lead, spec §15 real acceptance 2, first half):
1. Approve a lead request.
2. `pdx spawn --model sonnet --effort low --brief "say hi"` twice.
3. `pdx team` shows both, and `claude-sonnet-…` / `low` after their first turn.
4. `pdx kill` one.
5. Restart the daemon during a third spawn: it finishes once, and `tmux ls` shows one `tm-` session for it.
6. Kill everything and check that no flags are left.

**Size.** 720 lines, 6 files.

**Risks.** The skill text reaches sessions only after `pdx setup --agent cc` (setup tag).

---

# Phase P4b — host selection (spec §7.4 (a)(b), selection rule; U15; M13–M15)

## PR P4b-1 — `rate_limits`, the account, the weekly reading, `GET /api/peers/team/usage`

**Goal.** Spec §7.4 (b): parse `rate_limits` (M13); keep one host-level reading per account (M14); stale after 60 minutes; expose it to paired hosts.

**Files.**
- Modify `internal/module/agent/context_usage.go:38-85` and `context_usage_test.go`.
- Create `internal/team/wire_hosts.go` and its test.
- Create `internal/module/team/usage.go` and `usage_test.go`.
- Modify `internal/module/team/module.go` (route, Init reader).
- Modify `internal/module/peers/policy.go:37-50` and `policy_test.go`.

**Interfaces.**
- Agent module:
  - `type RateWindow struct{ UsedPercentage float64; ResetsAt int64 }`, where `ResetsAt` is unix **seconds**.
  - `type RateLimitReading struct{ FiveHour, SevenDay *RateWindow; At int64 }`.
  - `type RateLimitReader interface{ RateLimits() (RateLimitReading, bool) }`, implemented by `*Module` and type-asserted like `ContextUsageReader`.
  - `statuslineUsage` gains `RateLimits *struct{ FiveHour, SevenDay *struct{ UsedPercentage *float64 \`json:"used_percentage"\`; ResetsAt int64 \`json:"resets_at"\` } }` (`json:"rate_limits"`, `five_hour`, `seven_day`).
- Wire:
  - `type UsageWindow struct{ UsedPercentage float64 \`json:"used_percentage"\`; ResetsAt int64 \`json:"resets_at"\` }`.
  - `type HostUsage struct{ HostID, Alias, AccountFP string; SevenDay, FiveHour *UsageWindow; At int64; State string }`, with `State` `"fresh"|"unknown"`.
  - `const UsageStaleS = 3600`, `const AccountRefreshS = 600`.
- Team: `GET /api/peers/team/usage` → 200 `HostUsage`, for a host principal or the admin token.
- Policy: allows `GET` on the exact path `/api/peers/team/usage`.

**Behaviour rules.**
1. **Parsing.** `rate_limits` is parsed **before** the `context_window == nil` early return (`context_usage.go:56`).
   - A payload whose windows are all absent or have a null percentage leaves the host reading as it was. M13: the first refresh has none.
   - Otherwise it replaces the host-level reading: newest wins, `At` = now.
2. **The account.** Read from `~/.claude.json` `oauthAccount.emailAddress` (seam `claudeJSONPath`) at `Start` and every 10 minutes on the sweeper. It is stored as `AccountFP = hex(sha256(lowercase email))[:16]` and never the email itself (open question 8). Unreadable → `""` and one log line per state change.
3. **Staleness.** `State` is `"unknown"` and both windows are nil when there is no reading or `now - At > 60 min` (spec §7.4 (b)).

**Tests.**
- `TestRecordContextUsage_RateLimitsAbsentOnFirstRefreshPresentFromSecond`: two M13-shaped payloads.
- `TestRateLimits_NullPercentageKeepsThePreviousReading`.
- `TestHostUsage_UnknownAfter60Min`.
- `TestAccountFP_ReadsOnlyOauthAccountNeverTheEmail`.
- `TestHostRoutePolicy_TeamUsageGetOnly`: GET allowed; POST, a different path, `/api/peers/team/usage/x` refused.

**Mutation gates.**
- Parse after the early return → the first test red.
- Expose the raw email → its test red.

**Size.** 600 lines, 10 files.

**Risks.** None.

## PR P4b-2 — repo inventory and the `developable` rule

**Goal.** Spec §7.4 (a): the scan, `developable` (M15), the canonical key, the cache, `GET /api/team/repos` and `GET /api/peers/team/repos`; host config `team.repo_roots`, `team.min_weekly_remaining`, `team.preferred_host`.

**Files.**
- Modify `internal/module/hostconfig/team.go` and its test.
- Create `internal/module/team/repos.go` and `repos_test.go`.
- Modify `internal/team/wire_hosts.go` and its test.
- Modify `internal/module/team/module.go` and `peers/policy.go` (with its test).

**Interfaces.**
- `TeamSettings` gains:
  - `RepoRoots []string` (`repo_roots`, default `["~/Workspace"]`, ≤ 16 entries, absolute or `~`-prefixed);
  - `MinWeeklyRemaining int` (`min_weekly_remaining`, 0–100, default 20);
  - `PreferredHost string` (`preferred_host`, alias pattern or `""`, default `"mlab"`).
- Wire:
  - `type RepoCheckout struct{ Key, Path, Remote string; Developable bool; SparsePatterns []string; MTime int64 }`;
  - `type RepoInventory struct{ HostID, Alias string; ScannedAt int64; Repos []RepoCheckout; Error string }`.
- `func canonicalRepoKey(remote, orgRepoPath string) string`.
- `func scanRepos(ctx context.Context, roots []string, home string, git gitRunner) ([]team.RepoCheckout, error)`, where `type gitRunner func(ctx context.Context, dir string, args ...string) (string, error)`.
- Routes: `GET /api/team/repos[?refresh=1]` (admin) and `GET /api/peers/team/repos` (host or admin) → 200 `RepoInventory`.

**Behaviour rules.**
1. **The scan.** Two levels `{org}/{repo}` under each root, looking for a `.git` directory or file. Deeper paths (`.claude/worktrees/…`) are never visited. Per checkout:
   - `git -C <p> config --get remote.origin.url`;
   - `git -C <p> config --bool core.sparseCheckout`;
   - when true, `git -C <p> sparse-checkout list`.
   Each git call is bounded at 5 s.
2. **Developable** = sparse unset or false, **or** some listed pattern is neither `docs` nor `docs/…`. Never `ls-files` (M15).
3. **Canonical key.** Drop `ssh://`, `git@`, user and port; host lowercase; drop `.git`; keep the org's case. No remote → the path's `org/repo`.
   - `ssh://git@lab.protype.tw:9079/NTSU/istdc.git` → `lab.protype.tw/NTSU/istdc`
   - `git@github.com:wake/purdex.git` → `github.com/wake/purdex`
   - `https://github.com/wake/purdex` → `github.com/wake/purdex`
4. **The cache.** In memory, scanned asynchronously at `Start` and every 10 minutes. `?refresh=1` scans synchronously, single-flight, bounded at 30 s. A scan error keeps the last good list and sets `Error`.

**Tests** (real `git` in `t.TempDir()`):
- `TestScanRepos_PlainCheckoutIsDevelopable`.
- `TestScanRepos_ConeSparseDocsOnlyIsNot`: `git sparse-checkout set docs`.
- `TestScanRepos_SparseDocsPlusSrcIs`.
- `TestScanRepos_DotGitFileCounts`.
- `TestScanRepos_DepthTwoOnly`.
- `TestCanonicalRepoKey_Table`: the three M15 shapes plus `https://user@Host/x/y.git` and no remote.
- `TestReposRoute_RefreshIsSingleFlight`.
- `TestHostRoutePolicy_TeamReposGetOnly`.

**Mutation gates.**
- Use `ls-files` instead → the docs-only case red.
- Lowercase the org → its key case red.

**Size.** 680 lines, 10 files.

**Risks.** None. M15 measured the scan at 0.76 s for 47 checkouts.

## PR P4b-3 — peers `HostCaller`; `GET /api/team/hosts`

**Goal.** Spec §7.4 (a) "inside `GET /api/team/hosts` (all paired hosts, fetched the way `/api/peers` rows are)". Today only `fetchRemote` and `postDeliver` exist (`internal/module/peers/client.go:39`, `send.go:89`); this adds the generic "call path X on host Y" the spec's M16 notes is missing.

**Files.**
- Create `internal/module/peers/host_caller.go` and `host_caller_test.go`.
- Modify `internal/module/peers/module.go:368` (register next to `OriginResolverKey`).
- Create `internal/module/team/hosts.go` and `hosts_test.go`.
- Modify `internal/module/team/module.go`.

**Interfaces.**
- `const HostCallerKey = "peers.host-caller"`.
- `type PeerHostRef struct{ Alias, HostID string; Verified, HasToken bool }`.
- `type HostCaller interface`:
  - `Self() (alias, hostID string)`;
  - `Hosts() []PeerHostRef`, which never carries a token;
  - `Call(ctx context.Context, alias, method, path string, body, out any) (status int, raw []byte, err error)`. It sends `Bearer entry.Token` to `entry.URL+path` only, follows no redirect, and is bounded by `ipeers.InterDaemonTimeout` with a 16 MiB body cap. `ErrHostUnknown` / `ErrHostUnverified` come back before any I/O.
- Wire:
  - `type HostInventory struct{ Alias, HostID string; Self, Reachable, Supported bool; Error string; Repos *RepoInventory; Usage *HostUsage }`;
  - `type HostsResponse struct{ Hosts []HostInventory }`.
- `GET /api/team/hosts` (admin) → 200 `HostsResponse`.

**Behaviour rules.**
1. **Self** is filled from the local cache and usage.
2. **Peers.** For each verified peer host, in parallel with a 5 s total budget, GET both peer routes.
   - **200** → supported.
   - **404, or a 403 whose body is not a JSON `APIError`** → `Supported=false` ("older daemon"; open question 7). An older daemon's `PeerAuth` answers plain-text `forbidden` for an unknown `/api/peers/*` path (`internal/middleware/peer_auth.go:88-90`).
   - **A transport error** → `Reachable=false`.
3. The token is read from the live config per call, never cached.

**Tests.**
- `TestHostCaller_SendsTheTokenOnlyToTheEntryURL`.
- `TestHostCaller_RefusesRedirectsAndUnverified`.
- `TestHosts_ClassifiesOKOlderUnreachable`: 404, plain 403, refused connection.
- `TestHosts_SelfFromLocalCache`.

**Mutation gates.**
- Follow redirects → the redirect test red.
- Treat plain 403 as supported → the classification red.

**Size.** 400 lines, 6 files.

**Risks.** None.

## PR P4b-4 — the selection rule and `pdx spawn --repo / --host`

**Goal.** Spec §7.4 rule steps 1–5 and the stderr line; §7.2 `--repo` / `--host`. Execution is still local only (§7.4 (d)): a rule that picks another host answers `409 remote_unsupported` until P4c-3 (open question 18).

**Files.**
- Create `internal/module/team/choose.go` and `choose_test.go`.
- Modify `internal/module/team/spawn_handler.go` and its test.
- Modify `internal/team/wire_team.go` and its test.
- Modify `cmd/pdx/team_cmd.go` and its test.
- Modify `SKILL.md`.

**Interfaces.**
- `SpawnRequest` gains `Repo, Host string \`json:",omitempty"\``.
- `SpawnOp` gains `Choice *HostChoice \`json:"choice,omitempty"\``, where `type HostChoice struct{ HostAlias, HostID string; Self bool; Cwd, Reason string }`.
- Codes `ErrNoHostForRepo = "no_host_for_repo"`, `ErrRemoteUnsupported = "remote_unsupported"`, both 409 and exit 13.
- `func chooseHost(x string, hosts []team.HostInventory, preferred string, minRemaining int) (team.HostChoice, *team.APIError)`, a pure function.
- `func matchRepo(x string, c team.RepoCheckout) bool`:
  1. the exact canonical key;
  2. a case-insensitive `org/repo` suffix of the key;
  3. a case-insensitive `org/repo` of the path.
- CLI: `pdx spawn … [--repo <key|org/repo>] [--host <alias>]`.

**Behaviour rules** (spec §7.4):
1. **Candidates** are supported, reachable hosts with a developable matching checkout.
2. **None** → 409 `no_host_for_repo`. The detail names the hosts with docs-only matches (`a26 只有 docs`), or says no host has `<x>`.
3. **One** → that host.
4. **Several.** Rank: enough remaining (≥ `min_weekly_remaining`) = 2, `unknown` = 1, below = 0.
   - If **every** candidate is rank 2: `preferred_host` when it is among them, else the most remaining.
   - Otherwise: the best rank, then the most remaining, with the preferred host as the tiebreak, then alias order.
   - If every candidate is below the threshold, the same ordering still picks one. This case is unspecified (open question 17).
5. **`--host <h>`** skips the rule.
   - With `--repo`, `<h>` must be a candidate, else 409 `no_host_for_repo`.
   - An unknown alias is 400.
   - `--host <self>` without `--repo` is a plain local spawn.
6. **The chosen host.**
   - **Self:** `cwd` defaults to the newest-`mtime` matching checkout, unless `--cwd` was given. The grant-root check still applies.
   - **Remote:** 409 `remote_unsupported`, detail `跨主機開 member 在 P4c 才支援；規則選了 <alias>`, until P4c-3 replaces the branch.
7. **The `Reason` line**, printed on stderr by the CLI before the result. The templates are pinned by tests:
   - several candidates: `選擇 <h>：有 <key> 可開發的 checkout（<a>、<b> 只有 docs）；weekly 剩餘 <h1> NN% / <h2> 未知，<同帳號|不同帳號>，<依偏好選 <h>|選剩餘最多的 <h>>`;
   - one candidate: `選擇 <h>：唯一有 <key> 可開發 checkout 的主機（… 只有 docs）`;
   - `--host`: `選擇 <h>：由 --host 指定`.
   "同帳號" means every candidate has the same non-empty `AccountFP`.
8. **Skill.** Add `--repo` / `--host` and the one-line rule.

**Tests** (spec §15 "Host selection"):
- `TestChooseHost_DocsOnlyEverywhereIsNoHostForRepo`.
- `TestChooseHost_PlainCheckoutWins`.
- `TestChooseHost_SameAccountTiePrefersMlab`.
- `TestChooseHost_BelowThresholdLosesToAbove`.
- `TestChooseHost_UnknownRanksBetween`.
- `TestChooseHost_MatchOrder`.
- `TestChooseHost_ReasonTemplates`.
- `TestSpawn_RepoPicksThisHostAndDefaultsCwdToTheNewestCheckout`.
- `TestSpawn_RepoPicksARemoteIsRemoteUnsupported`.
- `TestSpawnCmd_PrintsTheChoiceLineAndMapsTheTwoCodes`.

**Mutation gates.**
- Treat unknown as enough → the between case red.
- Ignore `preferred_host` → the tie case red.

**Size.** 560 lines, 10 files.

**Risks.** M14: mlab and a26 share an account today, so rule 2 always ties. The tests use two account fingerprints.

---

# Phase P4c — cross-host execution (spec §7.4 (c)(d), U15, U20 (f), M16)

## PR P4c-1 — `AllowTeam`: config, CLI, Hosts toggle

**Goal.** Spec §7.4 (c): `PeerHost.AllowTeam` (default false), `pdx peers … allow-team <alias> on|off`, and the toggle 「允許 <alias> 在這台開 member」.

**Files.**
- Modify `internal/config/config.go:46-58`.
- Modify `internal/module/peers/hosts.go` (`hostRow` `:30-60`, `putHostRequest` `:91`, apply at `:546`) and `hosts_test.go`.
- Modify `cmd/pdx/peers.go` (usage `:70-80`, the `host` verbs, `cliPutHostRequest` `:814`) and `peers_test.go`.
- Modify `spa/src/lib/host-api.ts` (`PeerHostRow` `:128-140`, `updatePeerHost` `:474-484`).
- Create `spa/src/components/hosts/peers/PeerAllowTeamToggle.tsx` and its test.
- Modify `spa/src/components/hosts/PeersSection.tsx` (render the toggle in each host row).
- Modify `spa/src/locales/en.json` and `zh-TW.json`.

**Interfaces.**
- `AllowTeam bool \`toml:"allow_team" json:"allow_team"\``.
- `hostRow.AllowTeam` (`allow_team`) and `putHostRequest.AllowTeam *bool` (`allow_team`).
- CLI: `pdx peers host allow-team <alias> on|off [--config <path>]` (deviation 7: the existing grammar is singular `host`).
- SPA: the `updatePeerHost` patch gains `allow_team?: boolean`. Locale key `hosts.peers.allow_team` = 「允許 {alias} 在這台開 member」 / "Allow {alias} to open members on this host".

**Behaviour rules.**
1. **Default false.** An existing `config.toml` without the key reads false.
2. **The toggle** shows the server's value. While a PUT is in flight it is disabled; a failure reverts it and toasts.
3. **Tab-hosted check.** The toggle holds no draft state that must survive a tab switch. The value is re-read from `listPeerHosts` on mount. A remount test asserts it shows the server's value, per the repo CLAUDE.md checklist.

**Tests.**
- `TestPutHost_AllowTeamTogglesAndRowShowsIt`.
- `TestPeersCmd_AllowTeamGrammar`: `on`, `off`, anything else → 2.
- `PeerAllowTeamToggle.test.tsx`: click → PUT `{allow_team:true}`, revert on failure, remount shows the server value.
- Locale completeness.

**Mutation gates.** Drop the field from `hostRow` → the row test red.

**Size.** 380 lines, 12 files.

**Risks.** None.

## PR P4c-2 — the member host side

**Goal.** Spec §7.4 (c): `remote_members`; `POST /api/peers/team/spawn|kill|lead-moved` behind `HostRoutePolicy`; `AllowTeam` on the writes; accept only `principal.HostID == lead_host_id` with a matching `team_id`; spawn idempotent on the op id. Plus `POST /api/peers/team/end` (deviation 5).

**Files.**
- Create `internal/module/team/remote_store.go` and its test.
- Create `internal/module/team/peer_team_handler.go` and its test.
- Modify `internal/module/peers/policy.go` and its test.
- Modify `internal/module/team/spawn.go`: the runner records a remote member at `registered`.
- Modify `relay_handler.go` (role) and `relay_store_report.go` (`cleared` moves remote rows).
- Modify `internal/team/wire_team.go` and its test.

**Interfaces.**
- Schema:
  ```sql
  CREATE TABLE IF NOT EXISTS remote_members (spawn_op TEXT PRIMARY KEY,
    member_session_id TEXT NOT NULL, ref TEXT NOT NULL, team_id TEXT NOT NULL,
    lead_host_id TEXT NOT NULL, lead_host_alias TEXT NOT NULL,
    lead_session_id TEXT NOT NULL, lead_ref TEXT NOT NULL,
    cwd TEXT NOT NULL, title TEXT NOT NULL DEFAULT '', tmux_session TEXT NOT NULL,
    tmux_id TEXT NOT NULL DEFAULT '', tmux_instance TEXT NOT NULL DEFAULT '',
    pane_id TEXT NOT NULL DEFAULT '', pid INTEGER NOT NULL DEFAULT 0,
    model TEXT NOT NULL DEFAULT '', effort TEXT NOT NULL DEFAULT '', state TEXT NOT NULL,
    team_ended_at INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
  CREATE UNIQUE INDEX IF NOT EXISTS remote_members_one_active ON remote_members (member_session_id) WHERE state='active';
  ```
  `spawn_ops` gains, through `ensureColumn`: `lead_host_id TEXT NOT NULL DEFAULT ''`, `lead_host_alias TEXT NOT NULL DEFAULT ''`, `lead_json TEXT NOT NULL DEFAULT ''`.
- Wire:
  - `type RemoteSpawnRequest struct{ ID, TeamID, LeadSessionID, LeadRef, LeadAddress, Cwd, Title, Model, Effort string }`;
  - `type RemoteKillRequest struct{ TeamID, MemberSessionID string }`;
  - `type LeadMovedRequest struct{ TeamID, LeadSessionID, LeadRef string }`, with `lead_ref` added (deviation 5);
  - `type TeamEndRequest struct{ TeamID string }`;
  - codes `ErrHostNotAllowed = "host_not_allowed"` (403, JSON) and `ErrAdminNotAllowed = "admin_not_allowed"` (403).
- Routes, each `POST` and each listed by exact path in `HostRoutePolicy`:
  - `/api/peers/team/spawn` → 200 `SpawnOp`, waiting ≤ 8 s;
  - `/api/peers/team/kill` → 200 `Member`;
  - `/api/peers/team/lead-moved` → 200 `{updated:n}`;
  - `/api/peers/team/end` → 200 `{updated:n}`.

**Behaviour rules.**
1. **Principal binding**, in every handler, in this order. It mirrors `handleDeliver` (`internal/module/peers/deliver.go:142-165`):
   - admin → 403 `admin_not_allowed`;
   - not a host principal, or an empty `HostID` → 403 `host_unverified`;
   - the live config entry for `principal.Alias` must still carry `principal.HostID`, else 403 `host_unverified`.
2. **`AllowTeam`** gates `spawn` and `kill` (spec: "gates spawn, kill and relay"). Off → 403 JSON `host_not_allowed`. `lead-moved` and `end` need only the binding plus existing `remote_members` rows of that `team_id` and lead host.
3. **Spawn on the member host.** Idempotent on `ID` (same hash joins). Neither the grant nor `max_members` travels (spec §7.4 (c)); the lead host checked them.
   - `cwd` must exist and lie under this host's own `team.repo_roots` (deviation 11, open question 5), else 409 `cwd_outside_grant`.
   - The P4-5 runner runs unchanged, with `lead_*` recorded. At `registered` it inserts `remote_members` instead of `team_members`.
   - The handler answers the op's state after ≤ 8 s (`InterDaemonTimeout` is 10 s). The lead host re-posts.
4. **Kill.** Accepted only when a `remote_members` row has `member_session_id`, `team_id`, and `lead_host_id == principal.HostID`; else 409 `not_your_member`. Same kill semantics as P4-6.
5. **`lead-moved`** updates `lead_session_id` and `lead_ref` on matching rows. A stale value is not fatal, because the check is host plus team.
6. **`end`** sets `team_ended_at`.
7. **Role.** `relayRole` adds: an active `remote_members` row with `team_ended_at = 0` → `member`.
8. **`cleared`.** The transaction also updates `remote_members` (`member_session_id`, `ref`).

**Tests** (spec §15 "Cross-host"):
- `TestPeerTeam_HostWithoutAllowTeamGetsHostNotAllowed`.
- `TestPeerTeam_AdminIsRefused`.
- `TestPeerTeam_AliasRebindIsRefused`.
- `TestPeerTeam_KillFromAnotherHostOrTeamIsRefused`.
- `TestPeerTeam_SpawnRetriedWithTheSameIDOpensOneSession`.
- `TestPeerTeam_CwdOutsideRepoRoots`.
- `TestRelayRole_RemoteMemberIsMemberUntilEnd`.
- `TestRelayStore_ClearedMovesARemoteMember`.
- `TestHostRoutePolicy_TeamWritesExactPathsOnly`.

**Mutation gates.**
- Drop the `lead_host_id` check → the cross-host kill red.
- Read `AllowTeam` from the request instead of the config → its test red.

**Size.** 760 lines, 12 files.

**Risks.** None.

## PR P4c-3 — the lead host side: forwarding, the outbox, remote members in `pdx team`

**Goal.** Spec §7.4 (c): forwarding with op-id idempotency and the restart grace; `remote_unsupported`, `host_not_allowed`, `remote_unreachable`; the cross-host brief; `lead-moved` after a lead relay; U20 (f) (model and effort travel); remote model and effort visible in `pdx team` (U20 (e)).

**Files.**
- Create `internal/module/team/forward.go`, `forward_store.go` and tests.
- Modify `spawn_handler.go` (replace P4b-4's remote branch), `team_handler.go` (kill and view for remote members), `relay_report.go` (after `cleared` applied: enqueue `lead-moved`) and `sweeper.go` (on team end: enqueue `end`; pump the outbox).
- Modify `internal/peers/record.go:22-26` and `internal/module/peers/module.go:779-787` (`ContextInfo` gains `model_id`, `effort`) with their tests.
- Modify `cmd/pdx/team_cmd.go` (map the two new codes) and its test.

**Interfaces.**
- Schema:
  ```sql
  CREATE TABLE IF NOT EXISTS forwarded_ops (id TEXT PRIMARY KEY, kind TEXT NOT NULL,
    host_alias TEXT NOT NULL, host_id TEXT NOT NULL, path TEXT NOT NULL,
    body_json TEXT NOT NULL, state TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '',
    result_json TEXT NOT NULL DEFAULT '', attempts INTEGER NOT NULL DEFAULT 0,
    next_at INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
  ```
  `kind` is `spawn`, `kill`, `lead_moved` or `end`; P6-7 adds `relay`. `state` is `forwarding`, `done` or `failed`.
- Codes `ErrRemoteUnreachable = "remote_unreachable"` (exit 14). The lead host maps the member host's `host_not_allowed` to 409 (exit 13).
- `ipeers.ContextInfo` gains `ModelID string \`json:"model_id,omitempty"\``, `Effort string \`json:"effort,omitempty"\``. This is additive: older readers ignore it.

**Behaviour rules.**
1. **Spawn to a remote host.**
   - The lead-host checks are unchanged: lead, `team_full` counting remote active rows, and no root check (the grant does not travel).
   - Persist `forwarded_ops{kind:spawn, state:forwarding}` and the local `spawn_ops` row (`host_id` = remote).
   - Loop `HostCaller.Call(POST /api/peers/team/spawn, RemoteSpawnRequest{… Model, Effort …})` with the same id. A `running` op loops; the total budget is 60 s.
   - `done` → insert `team_members` (`host_id` remote, `ref`, `session_id`, and `address = <entry alias>/_<ref>`, the lead's own alias for that host). The local op is done.
   - `failed` → failed with its reason.
2. **Classification** (spawn and kill):
   - **404, or a plain-text 403** → `remote_unsupported` (open question 7).
   - **JSON 403 `host_not_allowed`** → `host_not_allowed`.
   - **Transport errors and 503** → retried with backoff 0.25 s → 1 s for a 30 s grace (spec §9.1 applied daemon-to-daemon), then `remote_unreachable`.
   - The forwarded op is persisted, so a lead-host restart resumes it in `Start`. A re-POST is idempotent on the member host.
3. **Kill of a remote member.** Forward `RemoteKillRequest`. A 200 sets the local row `killed`.
4. **The outbox.**
   - After a lead relay's `cleared` (P4-3 moved `lead_session_id`), enqueue one `lead_moved` per remote host that has active members of that team.
   - When P4-2's sweeper ends a team, enqueue one `end` per such host.
   - The sweeper pumps the outbox: every 30 s, at most 120 attempts (1 h), then `failed` with a log line. A stale lead id is not fatal (spec §7.4 (c)).
5. **The team view for remote members.**
   - Fetch `/api/peers` from each member host through `HostCaller` (3 s; host principals may GET it). Match by session id, and take `agent.context` including the new `model_id` and `effort`.
   - An unreachable host leaves the columns blank, and the CLI marks the row `(主機無回應)`.
6. **The brief.** It is unchanged in the CLI because it is address-based. Its first line names `LeadAddress` (`<lead self alias>/<name|_ref>`); risk below.

**Tests.**
- Forwarding against two team modules over `httptest`: `TestForward_SpawnDoneRecordsTheRemoteMember`, `TestForward_RetryAfterLeadRestartOpensOneMember`.
- `TestForward_404AndPlain403AreRemoteUnsupported`.
- `TestForward_JSON403IsHostNotAllowed`.
- `TestForward_UnreachableThroughTheGraceIsRemoteUnreachable` (fake clock).
- `TestForward_KillMarksTheRowKilled`.
- `TestOutbox_LeadMovedAfterALeadRelay`.
- `TestOutbox_EndAfterTeamEndIsRetried`.
- `TestTeamView_RemoteMemberShowsModelAndEffort`.
- `TestBuild_ContextCarriesModelAndEffort`.
- CLI: `TestSpawnCmd_HostNotAllowed13RemoteUnreachable14`.

**Mutation gates.**
- Treat a plain 403 as `host_not_allowed` → its test red.
- Mint a new id per retry → the one-member test red.
- No outbox retry → the end test red.

**Acceptance** (needs both daemons at this version; air26's daemon has been behind before):
1. On mlab, allow-team for a26.
2. From a26, a throwaway lead runs `pdx spawn --host mlab --model sonnet --brief …`.
3. `pdx team` on a26 shows the mlab member with its model and effort.
4. `pdx kill` it.
5. Repeat with allow-team off → exit 13 `host_not_allowed`.

**Size.** 790 lines, 14 files. If it runs over, split kill forwarding and the outbox into P4c-4.

**Risks.**
- **Brief address.** If a member host knows the lead host under an alias other than the lead's self alias, the brief's lead address does not resolve from the member host. Replies still route through the message envelope; the member host also matches the host segment against the host id (`ipeers.HostMatches`). Noted, not fixed.

---

# Phase P6 — member relay (spec §6.6 relay lock, §8.1–§8.3, §8.4 member notice, §8.5 notifier, §9.3 relay ops, §9.5, U9, U17 use 2)

## PR P6-0 — pure move: split `cmd/pdx/relay.go` (#1730)

**Goal.** The SRP debt from plan v2's fix notes (P5a-2c), split before P6-5 adds `pdx relay <ref>` and `claim`.

**Files.**
- Modify `cmd/pdx/relay.go` (440 lines). It keeps the usage, constants, `relayRefusalCodes`, `runRelay`, `relayFlags`, `relayClient`, `runRelayCmd`, `relayUsageErr`, `relayPrintJSON` and `relayReportErr`.
- Create `cmd/pdx/relay_wait.go`: `runRelayWait`, `relayFinish`.
- Create `cmd/pdx/relay_subcmds.go`: `runRelayHello`, `runRelayBegin`, `runRelaySelf`, `runRelayReport`, `runRelayOp`.

**Contract, proof, tests.** As P4-0: per-declaration byte comparison, `go test ./cmd/pdx/ -race`, no codex.

**Size.** About 280 lines moved (≈ 560 diff lines), 3 files.

## PR P6-1 — the daemon's notifier; the lead-handover notice

**Goal.** Spec §8.5 "Daemon notices come from the daemon's own virtual peer … A reply to it gets one line back"; §7.4 (c) "notices … go through `POST /api/peers/send` from the member host's virtual peer"; §8.4 "When a lead relays, the daemon also tells each active member". Plan v2's P5a section deferred this notice to P4. It lands here, with the notifier it needs (deviation 10).

**Files.**
- Create `internal/module/peers/sender.go` and `sender_test.go`.
- Modify `internal/module/peers/module.go` (register `SenderKey`).
- Create `internal/module/team/notify.go`, `notice.go` and `notify_test.go`.
- Modify `internal/module/team/module.go` (`Start` / `Stop`, `Init` resolves the sender) and `relay_report.go` (handover after `cleared` applied).

**Interfaces.**
- Peers:
  - `const SenderKey = "peers.sender"`.
  - `type Sender interface{ Send(ctx context.Context, req ipeers.SendRequest) (ipeers.SendResponse, error) }`.
  - `type SendError struct{ Status int; API ipeers.APIError }` for any non-2xx.
  - The implementation runs `m.handleSend` (`internal/module/peers/send.go:197`) in process. The request context carries `middleware.WithPrincipal(ctx, Principal{Kind: PrincipalAdmin})`, because the handler is admin-only (`:202-206`). The response goes to a small buffered `http.ResponseWriter`.
  - This keeps one send path: resolution, local and remote delivery, audit.
- Team:
  - `type notifier struct{ vp *ccuds.VirtualPeer; send peersmod.Sender; logf … }`.
  - `func (m *Module) notify(ctx context.Context, to, text string) error`: a 5 s context, with `OriginInbox = vp.SockPath()`.
  - Seams: `vpOptions` (`SockDir`, `RegistryDir`, `ProcStart` for tests; `ccuds.VirtualPeerOptions`, `internal/peers/ccuds/virtual_peer.go:41-52`).
  - `Name` is `pdx-daemon`.

**Behaviour rules.**
1. **Lifecycle.** `Start` starts the virtual peer before `reconcileRelays` (P6-4 re-sends control messages from there). A start failure is logged: notices are then disabled, and `notify` returns `errNoNotifier`. `Stop` closes it after the sweepers and the spawn goroutines, which removes the registry files (re-exec keeps the pid, `virtual_peer.go:105`).
2. **Auto-reply.**
   - A goroutine reads `vp.Frames()` and parses each line with `ccuds.ParseFrame` (`frame.go:73`).
   - When the frame's `from` is `uds:<sock>`, it writes the auto-reply back with `ccuds.WriteFrame`.
   - At most once per sender socket per 10 minutes.
   - Never to a sender whose registry name is `pdx-daemon`. This guards a reply loop between two daemons.
3. **The handover notice.** After a `cleared` report is **applied** (not on `ReportNoop`), if a live team now has `lead_session_id == op.NewSessionID`:
   - For each active member (local and remote), send `[pdx team] 你的 lead 已換手：<new address> [<new ref>]（舊 ref 仍可用）`.
   - Address: local → `<self alias>/_<ref>`; remote → `<host alias>/_<ref>`.
   - A failure is logged; this is best effort.
4. **Visibility.** `pdx peers` lists `<host>/pdx-daemon` from now on (risk).

**Tests.**
- `TestSender_RunsHandleSendAsAdmin`.
- `TestSender_RefusalIsASendError`.
- `TestNotifier_StartsAndStopsWithTheModule`: temp sock and registry dirs; files gone after `Stop`.
- `TestNotify_UsesTheVirtualPeersInbox`.
- `TestAutoReply_OncePerSenderPerWindow_NeverToPdxDaemon`.
- `TestHandover_SentOnceOnAppliedClearedNotOnResend`.

**Mutation gates.**
- Notify on `ReportNoop` → the once test red.
- Drop the `pdx-daemon` guard → the loop test red.

**Size.** 600 lines, 8 files.

**Risks.**
- **Origin attribution.** **Verify in this PR** that `findOrigin` (`send.go:147`) accepts the virtual peer's row as an origin, the way the peer-proxy helper's virtual peer is accepted (`internal/peers/proxyhelper/helper.go:114`). If it does not, the sender must name the origin by the daemon's pid instead. Stop and report rather than widen `findOrigin` silently.
- **The `pdx peers` row.** If listing the `pdx-daemon` row is unwanted, filter it in the CLI table by name. That is not in this PR.

## PR P6-2a — member-relay wire; op pid and pane; persisted `hello`; the `cleared` binding

**Goal.** Make P6-2b's routes and P6-4's reconciliation possible:
- every op carries its process binding;
- mod presence survives a daemon restart, so `relay_unsupported` is not answered wrongly after every restart;
- `checkClearedTarget` gets the member binding it says P6 brings (`internal/module/team/relay_report.go:199-205`).

**Files.**
- Create `internal/team/wire_relay_member.go`; modify `wire_relay.go:65-82` (`RelayOp`) and the contract test.
- Modify `relay_store.go` (columns, `relayCols`, scan, create; via `ensureColumn`).
- Create `internal/module/team/mod_hello_store.go` and its test.
- Modify `relay_handler.go` (begin fills pid and pane; hello upserts) and `relay_report.go` (binding), with tests.

**Interfaces.**
- `RelayOp` gains `PID int \`json:"pid,omitempty"\`` and `PaneID string \`json:"pane_id,omitempty"\``.
- Columns on `relay_ops`: `pid INTEGER NOT NULL DEFAULT 0`, `pane_id TEXT NOT NULL DEFAULT ''`.
- Wire:
  - `type RelayCreateRequest struct{ ID, OriginInbox, Target string }` (spec §8.2 step 1);
  - `type RelayClaimRequest struct{ SessionID string }`;
  - `type RelayClaimResponse struct{ Op RelayOp; Lead *RelayLead }`;
  - `type RelayLead struct{ Address, Ref, TeamID string }`;
  - codes `ErrRelayUnsupported = "relay_unsupported"` and `ErrNotYourOp = "not_your_op"` (new, 409, exit 13);
  - `const MinMemberRelayModVersion = 2`, `RelayControlPrefix = "[pdx-relay:control] op="`, `RelayClaimTimeoutS = 60`, `RelayOpTimeoutS = 900`.
- Schema `mod_hello(session_id TEXT PRIMARY KEY, mod_version TEXT NOT NULL, agent TEXT NOT NULL, at INTEGER NOT NULL)`. `Init` loads the newest 512 into `modSeen`. `hello` upserts, and evicts the oldest past 512.

**Behaviour rules.**
1. **Self begin** fills `PID = origin.PID` and `PaneID` = the `%N` suffix of `origin.Tmux` (`team.Origin.Tmux`, `wire.go:63`).
2. **`checkClearedTarget`** binds to `op.PID` when it is set, for any kind. A self op from before P6 falls back to its approval row's origin pid, as today. A member op with `pid = 0` is 400 (fail closed).
3. **Persistence** is the only change to `modSeen`. Readers are unchanged (`relay_handler.go:84-95`, `modPresent`).

**Tests.**
- `TestRelayOp_PIDAndPaneRoundTrip` (contract).
- `TestRelayBegin_FillsPIDAndPane`.
- `TestModHello_SurvivesARestart`.
- `TestCleared_MemberOpBindsToItsPID`: other pid → 400; the same pid → 200; pid 0 → 400.

**Mutation gates.**
- Do not load at `Init` → the restart test red.
- Wave a pid-0 member op through → its case red.

**Size.** 420 lines, 10 files.

**Risks.** #1832 (`modPresent` never expires) is unchanged in kind. Persistence lets presence outlive a restart, which matches how long the process lives. Noted in open question 11.

## PR P6-2b — `POST /api/team/relays`, the control message, `claim`, the op long-poll

**Goal.** Spec §8.2 steps 1–3, §8.3 `claim`, §15 "claim is accepted only for the target session", the plan v2 P5a-1a R1 fix note (a creator outside `createMu` maps `ErrRelayOpOpen` to 409 `relay_open`), and §8.2 "`--wait` blocks … with the same restart-aware polling".

**Files.**
- Create `internal/module/team/relay_member.go` and `relay_member_test.go`.
- Modify `module.go` (routes) and `relay_report.go` (`handleRelayOp` long-poll; wake on every applied report) with tests.

**Interfaces.**
- `POST /api/team/relays`, body `RelayCreateRequest` → 201 `{op}`, or 200 with the same op on replay. Errors:
  - 400 (id not UUID v4);
  - 400 `origin_unknown`;
  - 404 `unknown_session` (the member is not running);
  - 409 `not_lead`, `not_your_member`, `relay_unsupported` (detail exactly `<ref> 沒有載入 Purdex mod（或版本不符），無法接力；請手動接力或重開這個 member`), `relay_open` (with `op`), `id_conflict`;
  - 503.
- `POST /api/relay/ops/{id}/claim`, body `RelayClaimRequest` → 200 `RelayClaimResponse` | 404 `not_found` | 409 `not_your_op` / `bad_transition` (with `op`).
- `GET /api/relay/ops/{id}?wait=N`, with N ≤ 25 (`pollWait`, `handler.go:267-276`), answers when the op's state changes or at N.

**Behaviour rules.**
1. **Create** (under `createMu`):
   - **Replay.** `GetRelayOp(req.ID)` for the same target session → that op, as is. Another session → `id_conflict`.
   - **Lead.** The caller (by inbox) leads a live team.
   - **Target.** `matchMember` (P4-6) on an **active local** member. P6-7 adds remote members.
   - **Running.** The member is live (`ResolveOriginBySession`).
   - **Mod.** `modSeen[member sid].ModVersion` ≥ 2 (decimal), else `relay_unsupported`. A mod of protocol 1 cannot handle the control message (deviation 16).
   - **One op.** `OpenRelayOpBySession` → 409 `relay_open` with the op.
   - **Insert.** `{ID: req.ID, Kind member, TeamID, SessionID, Ref, State requested, HandoffPath: <relay dir>/<id>.md, PID, PaneID}`, where PID and pane come from the member row. `ErrRelayOpOpen` from the insert → the same 409.
2. **Control message.** After the commit, `notify(<self alias>/_<member ref>, "[pdx-relay:control] op=<id>")`. A failure is logged; the claim timeout (P6-4) covers it.
3. **Claim.**
   - Op kind must be `member`, else 409 `bad_transition`.
   - `op.SessionID == req.SessionID`, else 409 `not_your_op`.
   - `ReportRelay(claimed)`: Applied or Noop → 200 with `Lead{Address: <live origin address of teams.lead_session_id, else <self alias>/_<lead_ref>>, Ref: lead_ref, TeamID}`. BadTransition → 409 with the op.
4. **Wake-ups.** Every applied report and claim wakes `op.ID` waiters (`addWaiter`/`wake`).

**Tests.**
- `TestRelayCreate_Checks`: `not_lead`, `not_your_member`, `unknown_session`; no hello → `relay_unsupported`; version 1 → `relay_unsupported`; `relay_open`; replay; `id_conflict`.
- `TestRelayCreate_SendsTheControlMessageToTheMember`.
- `TestRelayCreate_TableConflictIs409`.
- `TestClaim_OnlyTheTargetSession` (the spec's mutation gate).
- `TestClaim_IsIdempotentAndCarriesTheLead`.
- `TestClaim_SelfOpIsBadTransition`.
- `TestRelayOp_LongPollWakesOnReport`.

**Mutation gates.**
- Drop the claim's session check → `…OnlyTheTargetSession` red (spec §15).
- Drop the version comparison → the version-1 case red.

**Size.** 650 lines, 6 files.

**Risks.** **Needs measuring:** whether `session.receive` fires while a turn is running (open question 3). If it does not, the 60 s claim timeout may fire on a busy member.

## PR P6-3 — the relay lock (U17 use 2)

**Goal.** Spec §6.6 table row 2 ("relay op past `claimed` … deny, reason `接力進行中，這一輪只寫接力檔`"), plan v2 coordinator decision ("P6 must check the relay lock before that removal"). The flag is raised and lowered by the daemon (deviation 3, open question 2b).

**Files.**
- Create `internal/team/hooklock_flock.go`: move `openHookLockLocked`, `writeHookLock`, `removeHookLock` and `hookLockExists` out of `cmd/pdx/hooklock.go` (`:23-102`), exported. Move their tests too.
- Modify the call sites `cmd/pdx/lead.go:197-198` and `cmd/pdx/hook.go:249`; delete `cmd/pdx/hooklock.go`.
- Modify `internal/module/team/hooks.go:23-110` (decide order, prune guard) and `hooks_test.go`.
- Modify `relay_member.go` (raise at claim), `relay_handler.go` (raise in `afterClose` when the op turns `claimed`) and `relay_report.go` (lower in `afterReport`).

**Interfaces.**
- `team.WriteHookLock(path, id string) error`.
- `team.RemoveHookLock(path, id string) (bool, error)`: compare-and-remove under flock.
- `team.HookLockExists(path string) bool`.
- `HookDecideResponse` for this lock: `{decision:"deny", reason:"接力進行中，這一輪只寫接力檔", lock:"relay", id:<op>}` (`team.HookLockRelay`, `wire.go:191`).

**Behaviour rules.**
1. **Raise.** When an op reaches `claimed` (member: the claim route; self: `afterClose` approved), write `<data_dir>/hooklocks/cc/<op session id>` with the op id as content.
2. **Lower.** At `cleared` (the old session id's flag) and at any terminal state, through `afterReport`, so the re-send and reconciliation paths are covered too. Compare-and-remove by op id.
3. **Decide order**, under `createMu`:
   1. the lead lock (unchanged; it wins);
   2. the relay lock: `OpenRelayOpBySession(sid)` in `claimed|writing|written` and agent `cc`;
   3. only then the existing `{}` plus flag removal (`openLeadAndRemoveStaleFlag`).
4. **The relay-lock answer** (open question 2):
   - `PermissionRequest` → `{}`.
   - `PreToolUse` → `{}` (no decision) for:
     - `Write` or `Edit` whose `file_path` equals the op's `handoff_path`;
     - `Read`, `Glob`, `Grep`;
     - `Bash` whose command matches `^git (status|diff|log|show)( [^;&|<>$`\\\n]*)?$` and has no `--output` argument.
   - Every other tool → deny.
   - It applies to both kinds: the spec's table is kind-agnostic.
5. **Prune.** `pruneUnlessLeadOpen` (`hooks.go:161-175`) also keeps a flag whose session has an op in `claimed|writing|written`.
6. **Boot.** `Start` re-raises flags for live sessions with ops in those states.

**Tests.**
- `TestHookDecide_RelayLockDeniesOtherToolsAllowsHandoffAndGitReads`.
- `TestHookDecide_RelayLockIsCheckedBeforeFlagRemoval`.
- `TestHookDecide_LeadLockWinsOverRelay`.
- `TestRelayLock_UpAtClaimDownAtClearedAndTerminal`.
- `TestRelayLock_SelfOpUpAtApproval`.
- `TestPrune_KeepsTheFlagOfAnActiveRelay`.
- `TestBoot_ReraisesRelayLocks`.
- The moved `TestHookLock_*` tests, unchanged in content.
- `TestRunHook_RelayDenyIsPrinted` (CLI end to end against a fake daemon).

**Mutation gates.**
- Put the removal before the relay check → `…BeforeFlagRemoval` red.
- Do not lower at `cleared` → its case red.
- Allow every `Bash` → the deny case red.

**Size.** 620 lines, 10 files.

**Risks.**
- **Self relays change.** This changes shipped self-relay behaviour: prompts released after an approval can no longer run arbitrary tools (P5b-3 released them with NOTE only). The spec's §6.6 row requires it. Open question 2 asks the coordinator to confirm the allow-list.

## PR P6-4 — timeouts, reconciliation from frames (#1735), completion and failure notices

**Goal.**
- Spec §8.2 timeouts (claim 60 s → `failed{member_unresponsive}`; whole op 15 min).
- §9.3 relay ops: `requested` → re-send; past `claimed` → compare with the pane's verified frame; a different session id → write the lineage; no frame → `failed{member_gone}`.
- Issue #1735: a stuck op is ended, then follows its state's retention rule.
- §8.2 step 8 and "the lead is told about every failure".

**Files.**
- Create `internal/module/team/relay_reconcile.go` and `relay_reconcile_test.go`.
- Modify `relay_report.go:270-332` (`reconcileRelays` calls the new pieces), `sweeper.go` (runtime timeouts), `notice.go` and `retention.go:41-55` (comment only: #1735 closed), with tests.

**Interfaces.**
- `func (m *Module) reconcileFromFrames(ctx context.Context, op team.RelayOp) (team.RelayOp, error)`.
- `func (m *Module) opBinding(op team.RelayOp) (pid int, pane string)`: the op's columns, else (old self ops) the approval row's `Origin.PID` and the pane of `Origin.Tmux`.

**Behaviour rules.**
1. **`reconcileFromFrames`.**
   - **Another session on the pane.** A verified frame on the pane (`LiveSessions(ctx,"cc")`) with `sid' != op.SessionID` and `ResolveOriginBySession(sid').PID == pid` → `ReportRelay(cleared, sid', RefID(sid'))`, then `afterReport` (title move, lock lower, handover). Another process's session is never written.
   - **CC gone.** No verified frame on the pane and `!LiveSession(op.SessionID)` → `failed{member_gone}`.
   - **Same session, alive.** Left as is.
2. **Boot.** `reconcileRelays` adds:
   - a member op in `requested` → re-send the control message (the claim is a CAS);
   - any op in `claimed|writing|written` → `reconcileFromFrames`.
3. **Sweeper, liveness tick.**
   - A member op in `requested` older than 60 s → `failed{member_unresponsive}`.
   - A member op not terminal 15 min after `created_at` → `reconcileFromFrames`, then, if still not terminal, `failed{member_unresponsive}`.
   - A **self** op past `claimed` with no progress (`updated_at`) for 15 min → `reconcileFromFrames`, then `failed{handoff_incomplete}` (open question 4).
   - `cleared` → `failed` is legal (`relayTransitions`).
4. **Notices to the lead**, member ops only, sent on `ReportApplied` only:
   - `done` → the done text;
   - `failed` / `cancelled` → the failure or cancel text.
   - The lead address is the live origin address of the team's `lead_session_id`, else `<self alias>/_<lead_ref>`. P6-7 extends this to remote leads.

**Tests.**
- `TestReconcile_PastClaimedWithANewFrameSessionWritesTheLineage` (#1735).
- `TestReconcile_NewFrameUnderAnotherPIDIsNotCleared`.
- `TestReconcile_NoFrameFailsMemberGone`.
- `TestReconcile_SameSessionAliveIsLeftAlone`.
- `TestReconcile_RequestedResendsTheControl`.
- `TestSweep_ClaimTimeoutFailsAndNotifiesOnce`.
- `TestSweep_WholeOp15Min`.
- `TestSweep_SelfOpStalledFails`.
- `TestNotice_DoneAndFailureOncePerTransition`.
- `TestRetention_AStuckOpEndedByReconcileLosesItsFileByItsStateRule`: #1735 end to end with P5a-3a's sweeper.

**Mutation gates.**
- Skip the PID check → `…AnotherPID…` red.
- Reconcile only `awaiting_approval` (today's behaviour) → the #1735 test red.
- Notify on Noop → the once test red.

**Size.** 700 lines, 6 files.

**Risks.** None beyond open question 4.

## PR P6-5 — CLI: `pdx relay <ref> [--wait <dur>]`, `pdx relay claim`

**Goal.** Spec §8.2 ("`pdx relay` returns once the op is accepted and prints the op id. `--wait` blocks until done or failed"), §8.3 `claim`, §14.

**Files.**
- Modify `cmd/pdx/relay.go` (dispatch, usage, refusal map).
- Create `cmd/pdx/relay_member_cmd.go` and `relay_member_cmd_test.go`.

**Interfaces.**

```
pdx relay <ref|address> [--wait <dur>] [--config <path>]
pdx relay claim <op> --session <sid> [--config <path>]
```

**Behaviour rules.**
1. **Dispatch.** A first argument that is not one of the known words (`hello`, `begin`, `wait`, `self`, `report`, `op`, `claim`, and `compacted` from P7-2) is a member relay when it matches `^_[0-9a-z]{6}$`, contains `/`, or has the `name [xxxxxx]` form. Otherwise it is a usage error, exit 2.
2. **The member relay.**
   - `CLAUDE_CODE_MESSAGING_SOCKET` is required.
   - A UUID v4. `POST /api/team/relays` with `Idempotent()`.
   - stdout gets the op JSON (it carries the id); exit 0.
3. **`--wait <dur>`** (default 0 = no wait; cap 9 m, so it fits one Bash call) long-polls `GET /api/relay/ops/{id}?wait=25` until terminal or the bound. Exits:
   - `done` → 0;
   - `failed{member_unresponsive|member_gone}` → 14;
   - other `failed` → 1;
   - `cancelled` → 12;
   - the bound → stdout the op JSON, exit 0 (mirrors `pdx relay wait`).
4. **`claim`** → stdout the claim JSON. `not_your_op` → 13. `bad_transition` → 13, with the op on stdout.
5. **The refusal map** (`relay.go:47-53`) adds `not_lead`, `not_your_member`, `relay_unsupported` and `not_your_op`. The code stays the last stderr token.

**Tests.**
- `TestRelayCmd_RefDispatchAndUnknownWordIs2`.
- `TestRelayCmd_MemberRelayPrintsTheOpExit0`.
- `TestRelayCmd_RefusalsExit13CodeLast`.
- `TestRelayCmd_ReplayAfterALostResponseIsTheSameOp`.
- `TestRelayCmd_WaitMapping`: a table of the five outcomes.
- `TestRelayCmd_ClaimPrintsAndMaps`.

**Mutation gates.** Map `member_gone` to 1 → the mapping table red.

**Size.** 520 lines, 4 files.

## PR P6-6 — the mod: control message, claim, write with team facts; protocol 2; acceptance

**Goal.**
- Spec §8.2 steps 3–8 for members.
- §15 Mod: "the control message is consumed only with the marker; claim failure does nothing; write → check → fix rounds → clear → seed".
- §8.2 step 4: "§8 協作關係 is filled from the claim. A member gets its lead and team id; a lead gets its roster".
- §15 real acceptance 2 and 3 (relay half).
- Rebase onto P8a-2 if it merged; it touches `hooks.json` and `embed_test.go`, not `register.js`.

**Files.**
- Modify `cmd/pdx/plugin/purdex/hooks/register.js`:
  - `VERSION` (`:33`) becomes `'2'`;
  - a new `session.receive` hook;
  - `settle`'s approved branch (`:481-505`) is factored into `startWrite($, p)`;
  - `writePrompt` facts (`:275-302`);
  - main-turn tracking in `turn.start` and `turn.complete` (`:600-627`).
- Create `cmd/pdx/plugin/purdex/hooks/member.test.ts`.
- Modify `relay.test.ts` (the hello `--version 2` assertion).
- Modify `SKILL.md` (As a member: the lead relays you; do nothing when 接力 starts except write the file asked).
- Create `docs/testing/member-relay-acceptance.md`.

**Interfaces.**
- `session.receive`:
  - The text contains `RelayControlPrefix` followed by a UUID, and `e.origin.kind === 'peer'` (M1) → return `{ consumed: true }`. The model never sees it.
  - When `s.interactive`, `s.state === 'idle'` and no claim is pending, schedule `later($, 0, claim(opId))`.
  - Otherwise `next(e)`.
- `claim`:
  - `pdx relay claim <op> --session <sid>` (`CALL_TIMEOUT_MS`).
  - Exit 0 → `s.pending = {op, requestId: undefined, path: op.handoff_path, oldSession: sid, oldRef: op.ref, before: usageLine(u), lead: body.lead, answer: deferred()}` with `answer` resolved `'approved'`, and `s.state = 'approved'`.
  - If a main-conversation turn is running, `startWrite` waits for its `turn.complete`; otherwise it starts now.
  - Any non-zero exit → nothing, so the daemon's claim timeout reports it.
- `writePrompt` facts, one line each:
  - member: `- 我的 lead：<lead.address>（ref <lead.ref>，team <lead.team_id>）`;
  - lead (`s.role === 'lead'`): `- 我管理的 members：` followed by `pdx team --json` rows (`ref`, `address`, `title`, `cwd`), or `無`.

**Behaviour rules.**
1. **Everything after the claim** is the shipped path:
   - write → check → fix ×2 → `written`;
   - `/clear` → `cleared` with the new id → seed → `done`.
   - The reports carry the member op's id.
   - P6-3's lock is raised by the daemon at the claim.
2. **The `prompt.submit` hold** (P5b-3) is untouched. It only applies to `awaiting` / `beginning`, which a member relay never enters.
3. **A member never calls `maybeBegin`.** Unchanged (`register.js:338-340`).
4. **The model never decides** a relay (U9). The control message is invisible to it (M1).

**Tests** (`member.test.ts`):
- `the control message is consumed only with the marker and only from a peer`.
- `a claim that fails does nothing`.
- `a claim while idle writes with the lead facts`.
- `a claim during a running turn writes after that turn completes`.
- `the full member path reports claimed-op written, cleared (new id), done`.
- `a lead's write prompt carries the roster from pdx team --json`.
- `hello reports --version 2`.

**Mutation gates.**
- Consume without the marker check → the first test red.
- Start writing before the running turn completes → the fourth red.

**Acceptance** (`docs/testing/member-relay-acceptance.md`, throwaway sessions, scratch plugin copy as `docs/testing/self-relay-acceptance.md` does):
1. A throwaway lead spawns two members.
2. `pdx relay _<ref>` on one. The member writes, clears and seeds. The lead gets the done notice.
3. `pdx msg send <host>/_<old ref>` still reaches the member (lineage).
4. Restart the daemon mid-relay: the relay finishes, or is reconciled from frames.
5. The statusline before and after shows the same `model.id` / `effort.level` (U20 (f), M21).
6. Afterwards, no flags are left and no op is open.

**Size.** 560 lines, 5 files.

**Risks.** `register.js` grows past 900 lines. Splitting the member path into `hooks/member.js` would need shared state across modules. That is left for a follow-up issue, not this PR.

## PR P6-7 — cross-host member relay

**Goal.** Spec §7.4 (c) "`POST /api/peers/team/relay` … The relay op row and the lineage live on the member host. Notices to the lead … from the member host's virtual peer to the lead's cross-host address"; §12 "P6 rides on P4c for a member on another host". The route is deferred from P4c to here (deviation 4).

**Files.**
- Modify `peer_team_handler.go` (relay route) and its test.
- Modify `forward.go` (`kind relay`; proxied wait) and its test.
- Modify `peers/policy.go` and its test.
- Modify `relay_member.go` (remote-member branch; notices to a remote lead) and `relay_report.go` (`handleRelayOp` wait proxy).

**Interfaces.**
- `type RemoteRelayRequest struct{ ID, TeamID, MemberSessionID string }`.
- `POST /api/peers/team/relay` → 200 `{op}`. It is create-or-join: a replay returns the current op.

**Behaviour rules.**
1. **On the member host.**
   - The same principal binding and `AllowTeam` gate as P4c-2.
   - A `remote_members` row with that session id, team and `lead_host_id == principal.HostID`, else `not_your_member`.
   - Then P6-2b's create steps from "running" on: live check, mod version, one op, insert, control message.
   - Done, failure and P7 notices go to `<lead_host_alias>/_<lead_ref>`, the member host's own alias for the lead host (`principal.Alias` at spawn).
2. **On the lead host.** `POST /api/team/relays` for a remote member persists `forwarded_ops{kind:relay}` and forwards. Classification as P4c-3.
   - `GET /api/relay/ops/{id}?wait` for a forwarded op re-posts the relay with the same id to the member host (≤ 8 s per call) and answers the op it returns. `pdx relay <ref> --wait` therefore works unchanged.

**Tests.**
- `TestPeerRelay_BindingAndAllowTeam`.
- `TestForwardRelay_CreatesTheOpOnTheMemberHost`.
- `TestForwardRelay_WaitIsProxied`.
- `TestNotice_RemoteLeadAddressedByTheMemberHostsAlias`.
- `TestHostRoutePolicy_TeamRelayExactPath`.

**Mutation gates.** Address notices by the lead's self alias → the alias test red.

**Acceptance.** a26 lead, mlab member: `pdx relay _<ref>`. The member relays on mlab; a26's lead gets the done notice; the old ref resolves from a26.

**Size.** 650 lines, 8 files.

## PR P6-8 — SPA: the restart-confirm relay line

**Goal.** Spec §9.5: `N 個申請等待核准、N 個接力進行中（重啟後會接續）`, each shown only when non-zero. P3b shipped the approvals half; the daemon has filled `relays_active` since P5a.

**Files.**
- Modify `spa/src/components/hosts/RestartDaemonButton.tsx`: `PendingConfirm` (`:33-41`) gains `relays`, from `fetchInflight` at `:97`.
- Modify its test, `spa/src/lib/team/types.ts:142` (comment) and both locale files.

**Behaviour rules.**
1. **Count.** `relays_active` is read beside `approvals_open` within the same 3 s budget. A failure → `null` → no relay line. There is no store fallback for relays.
2. **One line.** When both counts are non-zero they join into one line, with the 、 between them.

**Tests.**
- `relays line shows only when non-zero`.
- `joins both with 、`.
- `fetch failure shows no relay line`.

**Mutation gates.** Show the line at 0 → red.

**Size.** 120 lines, 5 files.

---

# Phase P7 — detection and notices (spec §8.5, U9)

## PR P7-1 — the 70% idle notice

**Goal.** Spec §8.5:
- a member's usage reaches 70% while it is idle (its `Stop`) → **one** notice to the lead;
- running when it crosses → wait for idle;
- re-arm only after a relay or a drop below 70%;
- "The daemon decides nothing".

**Files.**
- Modify `internal/module/agent/module.go` (`AgentStatus` accessor, under `m.mu`; `currentStatus` is keyed by tmux session name, `handler.go:614`) and its test.
- Create `internal/module/team/notice_usage.go` and its test.
- Modify `team_store.go` and `remote_store.go` (`notice_armed` column via `ensureColumn`), `relay_store_report.go` (`cleared` re-arms) and `sweeper.go`.

**Interfaces.**
- `type AgentStatusReader interface{ AgentStatus(tmuxSession string) (string, bool) }`, implemented by `*agent.Module` and type-asserted like `ContextUsageReader`.
- Column `notice_armed INTEGER NOT NULL DEFAULT 1` on `team_members` and `remote_members`.

**Behaviour rules.**
1. **The check.** On the liveness tick, for each active member (local with a live team; remote with `team_ended_at = 0`):
   - `u` = `ContextUsage(sid)`; `st` = `AgentStatus(tmux_session)`.
   - Fire when `u.UsedPercentage ≥ team.RelayThresholdPct`, `st == "idle"`, the row is armed, and no relay op of the session is open. Then send the notice and disarm.
   - When `u.UsedPercentage < 70` → arm.
2. **The `cleared` transaction** arms the moved row.
3. **Text.** As the notices table. The address is the member's live address. `<N>` is the integer percentage. `<title>` is the member's title, else its tmux session name.
4. **Acceptance threshold.** For acceptance only, the daemon reads `PDX_TEAM_NOTICE_THRESHOLD` (1–100) once at `Init`; unset means 70 (open question 15). It never changes the mod's threshold.

**Tests.**
- `TestNotice70_CrossingWhileRunningWaitsForIdle`.
- `TestNotice70_OnceThenRearmsAfterARelay`.
- `TestNotice70_RearmsAfterADropBelow70`.
- `TestNotice70_NotWhileARelayIsOpen`.
- `TestNotice70_RemoteMemberAddressedToTheLeadHost`.
- `TestAgentStatus_ByTmuxSession`.

**Mutation gates.**
- Drop the idle check → the waits-for-idle test red.
- Drop disarming → duplicate notices red.

**Size.** 620 lines, 8 files.

## PR P7-2 — member auto-compact report

**Goal.** Spec §8.5 "derived: auto-compact": "Member: never intercepted … its mod lets compaction run and reports `compacted`. The lead hears: `[pdx team] <ref> 已自動壓縮（lead 未在 70% 時接力）`". The spec names no route, so this PR defines one (deviation 8).

**Files.**
- Create `internal/module/team/compacted.go` and its test.
- Modify `module.go` (route) and `cmd/pdx/relay.go` / `relay_subcmds.go` (`compacted`) with their tests.
- Modify `register.js`: the `session.compact` hook (`:727-746`).
- Modify `member.test.ts` and `SKILL.md` (notice texts match §8.5).

**Interfaces.**
- `POST /api/relay/compacted {session_id, trigger}` → 200 `{noticed bool}` | 400.
- CLI: `pdx relay compacted --session <sid> --trigger auto|manual [--config]`.

**Behaviour rules.**
1. **The daemon decides.** Only for an active member (any table) **and** `trigger == "auto"`: send the notice and arm (P7-1). Otherwise `{noticed:false}`.
2. **The mod** reports every compaction it does not intercept, from a timer (`later`), never awaited. Compaction never waits for it. It does not use `s.role` (open question 12): the first hello may answer before the member row exists (P4-3 risk).
3. **Skill.** The `[pdx team]` notice texts match §8.5 exactly. Today's skill paraphrases the 70% notice (`SKILL.md:21`).

**Tests.**
- Daemon: `TestCompacted_MemberAutoNotifiesOnce`, `…ManualDoesNot`, `…NonMemberDoesNot`.
- CLI grammar.
- Mod: `an auto compaction in idle reports once from a timer`, `the compaction never waits for the report`.

**Mutation gates.** Notice on `manual` → red.

**Size.** 420 lines, 8 files.

---

## Open questions for the coordinator

Each has a proposed default, which the sections above are written to.

1. **Order.**
   - Spec order is P4 → P4b → P4c → P6 → P7.
   - The local member relay (P6-1…P6-6) needs nothing from P4b or P4c, so it could ship first and give spec goal 2 sooner on one host.
   - **Default: spec order.** Reorder only if the user wants member relay before cross-host spawn.
2. **The relay lock's allowed tools.** Spec §6.6 allows only the handoff write, but the shipped write prompt (`register.js:281`) requires `git status`, `git diff --stat` and `git log`.
   - **Default:** allow `Read`, `Glob`, `Grep`, the read-only git `Bash` pattern, and `Write`/`Edit` to the handoff path; deny the rest; apply to both kinds (spec table).
   - **2b. Who writes the flag:** the spec says the mod. **Default:** the daemon. It owns the op state, already writes the sibling `hookasks` flags, and shares the CLI's flock protocol (moved to `internal/team`). A dead mod then leaves no flag that only it could lower.
3. **Needs measuring: does `session.receive` fire while a turn is running?** The 60 s claim timeout assumes it does (spec §8.2).
   - **Steps:** a throwaway tmux `claude --plugin-dir <probe>` whose `session.receive` logs `Date.now()`. Start a turn running `Bash sleep 45`. `pdx msg send` it at t+5 s. Compare the log time with the turn's end.
   - **If it fires only at idle:** the claim timeout counts from the member's next `idle` (P7-1's accessor) instead of from the request. That changes P6-4 only.
4. **Self ops stuck at runtime** (#1735 without a restart). The spec times member ops (15 min); self ops are reconciled only at boot.
   - **Default:** a self op past `claimed` with no progress for 15 min → frames → `failed{handoff_incomplete}`.
5. **cwd scope on a member host.** The grant's roots are paths on the lead's host, and the grant does not travel (§7.4 (c)).
   - **Default:** the member host requires the cwd to be under its own `team.repo_roots`.
6. **Default `--cwd` of `pdx spawn`.** The spec names none.
   - **Default:** the CLI's working directory (as `pdx lead request --root` defaults). A `--host` other than this one requires `--cwd` or `--repo`.
7. **Older-daemon detection.** The spec says "a plain 404 means an older daemon". In this codebase an older daemon answers **403 `forbidden` (plain text)**, because `PeerAuth` refuses a host principal on any path `HostRoutePolicy` does not list (`internal/middleware/peer_auth.go:88-90`).
   - **Default:** 404, or a 403 whose body is not a JSON `APIError`, → `remote_unsupported`. A JSON 403 is a real refusal.
8. **Account identity across hosts.** The spec compares accounts by `oauthAccount.emailAddress`.
   - **Default:** send a fingerprint (`sha256(lowercase email)[:16]`) over the peer API, never the email.
9. **The plugin tree at spawn.** The spec appends `--plugin-dir` but does not say who extracts the tree.
   - **Default:** extract only when it is absent, never overwrite. A version refresh stays with `pdx setup` (the user and d3 control that).
   - **Also needs measuring** (P4-5): whether a never-opened cwd stops a tmux-launched `claude --dangerously-skip-permissions` at a trust or bypass dialog before `SessionStart`.
     - **Steps:** a throwaway dir; `tmux new -d -s t1 -c <dir>`; send the launch line; capture the pane at 2 s and 10 s; check `~/.claude/sessions/` for the pid and the daemon's frames for the pane.
     - **If it stops:** document it in the skill (spawn into a repo root you have opened before), and report `member_start_timeout` with that hint.
10. **Persisted usage in P4 vs P7.** Plan v2's coordinator decision says P4; spec §12 says P7. **Default:** P4-6, per the binding decision.
11. **Persisting `hello`.** Without it, every daemon restart makes `pdx relay <ref>` answer `relay_unsupported`, because the mod re-sends hello only after a failure or a `/clear`.
    - **Default:** persist in `mod_hello` (P6-2a).
    - #1832 (presence never expires) stays as accepted in P8a-1a.
12. **Who sends the member compaction report.** **Default:** the mod reports every un-intercepted compaction and the daemon decides member-ness. This avoids the first-hello race.
13. **Nexen's host quota** (`GET /v1/host` → `quota.seven_day_pct`, read through the usage API, shown in the status bar since alpha.575) could replace or back up the statusline reading for rule 2.
    - **Default:** no. Follow the spec (statusline, stale after 60 min). Revisit if `unknown` turns out to be common.
14. **A brief that fails after a successful spawn.** **Default:** exit 1, stdout still carries the member, stderr says to send it by hand.
15. **The acceptance threshold for the 70% notice.** **Default:** the daemon env `PDX_TEAM_NOTICE_THRESHOLD`, read at `Init`. The member relay itself needs no threshold: the lead decides.
16. **`not_your_op`.** A new 409 code (exit 13) for a claim from a session that is not the target. Spec §14 has no code for it.
17. **Every candidate below the weekly threshold.** The spec does not say. **Default:** the same ranking still picks the one with the most remaining. Not refused.
18. **P4b before P4c.** **Default:** a rule that picks another host answers `409 remote_unsupported` (detail names the host) until P4c-3. `--host <self>` and local picks work.
19. **Team end reaching member hosts.** The spec has no route for it, so a remote member would stay "member" (no self relay) after its lead's team ended.
    - **Default:** a new `POST /api/peers/team/end`, with the same outbox retry as `lead-moved` (P4c-2/3).

## Deviations from spec

1. **Team id = the approving request's id** (`teams.id = teams.request_id`). The spec's row keeps both fields. This lets `pdx lead request` print the team id with no extra call, and lets approval replays find the team.
2. **Relay-lock answers.** Allowed tools answer `{}` (no decision) instead of `allow`, and the allow-list is wider than "only the handoff write" (open question 2). `pdx hook` prints decisions only for `deny` (P2c), and in bypass mode `allow` and `{}` behave the same.
3. **The relay flag is written by the daemon, not the mod** (open question 2b).
4. **`POST /api/peers/team/relay` lands in P6-7, not P4c.** Its handler needs P6's member-op creation.
5. **New peer route `POST /api/peers/team/end`; `lead-moved` also carries `lead_ref`.** Notices are addressed by ref, so the member host needs the current ref (open question 19).
6. **`remote_unsupported` also on a plain-text 403** (open question 7).
7. **`pdx peers host allow-team`.** Singular, as the existing grammar is (`cmd/pdx/peers.go:70-80`). The spec wrote `hosts`.
8. **New interfaces the spec does not name:**
   - code `not_your_op`;
   - spawn failure reasons `session_create_failed`, `launch_failed`, `tmux_name_taken`, `abandoned`;
   - routes `POST /api/relay/ops/{id}/claim`, `POST /api/team/kill`, `GET /api/team`, `POST /api/relay/compacted`;
   - CLI `pdx relay compacted`.
9. **Persisted usage in P4-6** (plan v2 decision), not P7 (spec §12).
10. **The lead-handover notice in P6-1.** Plan v2's P5a section deferred it to P4, but it needs the notifier.
11. **Member-host cwd scope = its own `team.repo_roots`** (open question 5).
12. **The account is compared by fingerprint** (open question 8).
13. **Spawn POSTs are create-or-join** and answer the op's state within a wait (25 s local, 8 s across hosts). There is no separate GET.
14. **`relayRole` returns an error.** A store error is 500 (fail closed), not "none".
15. **A whole-op timeout for self ops** (open question 4).
16. **"A compatible version" (spec §8.2 step 1) = mod protocol ≥ 2.** P6-6 bumps `VERSION` from `'1'` to `'2'`. Protocol 1 has no control-message handler.
