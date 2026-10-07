# Lead / member / team and context relay — Implementation Plan v3 (P4, P4b, P4c, P6, P7)

> **Status (2026-10-07):** revised after one codex round (8 findings: 2 critical, 5 important, 1 minor; all applied) and the coordinator's rulings on every open question. The binding text is **"Coordinator decisions (plan v3)"** at the end. Written against origin/main `d57ca13b` (alpha.581; spec U20 merged in PR #1838); every `file:line` below was read on that commit. P8a-2 is still in flight on its own branch. Every PR here that touches the mod (P6-3a, P6-3c, P6-6, P7-2) rebases onto it.
> **Source:** spec `docs/specs/2026-10-06-lead-team-relay-spec.md` (U1–U20, M1–M25), the "Coordinator decisions" and "Fix notes" of plan v1 and plan v2 (binding), and the line's memory `kickoff_lead_team_relay.md` (what shipped, review rulings, pitfalls).
> **Measurement numbers:** M25 is U20's launch flags (spec). **M26** is P8a-2's hours-long hold. **M27** (measured 2026-10-07, below) is the member launch in a never-opened directory. **M28** (still to measure) is whether `session.receive` fires while a turn runs.
> **Relation to v1 and v2:** v1 shipped P0–P3 (alpha.513–527), v2 shipped P2c, P5a, P5b and P8a-1a…1d (alpha.529–579). v3 schedules what is left of spec §12: **P4, P4b, P4c, P6, P7**. P8b is not scheduled and not written here. v3 uses a **compact format**: contracts, rules, tests and mutation gates, but no full code blocks. v2's code blocks went stale after review, so v3 does not repeat that mistake. The implementer writes the code test-first from these contracts.

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development (recommended) or superpowers:executing-plans. Each PR is TDD, one task per commit. Before you start a PR, re-verify its `file:line`s against main, because another line moves fast.

**Goal:** a lead spawns, lists and kills members, on whichever paired host the rule picks for the repo. The lead relays a member when it decides to; the member's mod writes, clears and seeds. A crash mid-relay is reconciled from frames. The lead is told when a member is idle past 70% or was auto-compacted.

---

## Global constraints

- **Size.** Each PR is ≤ 800 lines of diff **and** ≤ 20 files. The sections below are already split. A pure-move PR (P4-0, P6-0) is proved by per-declaration byte comparison, not by diffstat. Pure-move and bump PRs get no codex review (repo CLAUDE.md).
- **Order.** Merge in the order of the PR table (spec order, coordinator decision 1).
  - P6-0 and P4c-1 have no upstream dependency and may land early.
  - P6-3a (mod only) may land any time before P6-3c.
  - P6-8 needs only P6-2b.
  - **M28 should be measured before P6-4 starts** (the coordinator said before P6-6). The branch-specific code is in P6-4, P6-5 and P6-6 (P6-4 "Claim timeout"). If M28 is not ready by then, P6-4 takes branch B, which is correct for either outcome.
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
3. **Cross-host trust.** Only `principal.HostID == lead_host_id` with a matching `team_id` may kill or relay. `AllowTeam` gates writes. The admin token is refused on the peer team routes. An older daemon (404 **or** plain-text 403) reads as `remote_unsupported`. → P4c-2, P4c-3, P4c-4, P6-7a.
4. **The relay lock is exactly spec §6.6.** Only a `Write` to the op's handoff path is `allow`; every other tool is `deny`. It is checked before the flag is removed.
   - The mod raises the flag only at the write turn and lowers it before `/clear`. The daemon's safety net removes it at every terminal state.
   - The write prompt no longer asks the model to run git: the mod embeds the outputs.
   - → P6-3a, P6-3b, P6-3c.
5. **#1735.** An op stuck past `claimed` is ended from frames. A cleared report is accepted only for the op's own process. → P6-4.
6. **The member relay is safe to spoof and never leaks.** The claim is accepted only for the target session. The control message is **always** consumed with its marker, from a peer: busy or idle, the model never sees it. A mod older than protocol 2 is refused (`relay_unsupported`). → P6-2b, P6-6.
7. **A cross-host relay updates the lead host's roster.** After the member host's `cleared`, the lead host's member row carries the new session and ref, and the old ref still targets it. → P6-7b.

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
| **P4c-3** | Lead host side, spawn: `forwarded_ops`; forwarding with op-id idempotency, classification and the restart grace; resume at boot; CLI codes | P4c-2 | 520 / 8 | daemon (both hosts) + CLI |
| **P4c-4** | Lead host side, the rest: kill forwarding; lead-moved / end outbox (retried forever, backoff ≤ 10 min); remote members in `pdx team` (`ContextInfo` model/effort); `matchMember` on remote hosts | P4c-3 | 620 / 10 | daemon (both hosts) |
| **P6-0** | Pure move: split `cmd/pdx/relay.go` (#1730) | main | 560 moved / 3 | none |
| **P6-1** | Daemon notifier: virtual peer, in-process peers sender, auto-reply; lead-handover notice to members | P4-3 | 600 / 8 | daemon |
| **P6-2a** | Member-relay wire; `relay_ops.pid/pane_id`; persisted mod `hello`; `cleared` binding by op pid | P6-1 | 420 / 10 | daemon |
| **P6-2b** | `POST /api/team/relays` and the control message; `POST /api/relay/ops/{id}/claim` (no lock at claim); op long-poll | P6-2a, P4-6 | 650 / 6 | daemon |
| **P6-3a** | Mod: run the read-only git commands itself and embed them in the write prompt; write and fix prompts say "one `Write` of the whole file" | main (P8a-2 if merged) | 300 / 3 | daemon + setup |
| **P6-3b** | Relay-lock machinery: flock helpers moved to `internal/team`; `pdx relay lock|unlock`; decide answer (`allow` exact handoff `Write`, `deny` others) before flag removal; `pdx hook` prints `allow`; daemon safety net; prune guard | P6-0, P6-2b | 720 / 12 | daemon + CLI |
| **P6-3c** | Mod raises the flag at the write turn's `turn.start`, lowers it before `/clear` and on every give-up | P6-3a, P6-3b | 320 / 3 | daemon + setup |
| **P6-4** | Claim timeout (60 s; basis by M28: branch A `seen` from the request, or branch B from the next idle); the 15-min stall timeout; boot and sweeper reconciliation from frames (#1735); completion and failure notices | P6-3c | 720 / 7 (A: 780 / 10) | daemon |
| **P6-5** | CLI `pdx relay <ref> [--wait]`, `pdx relay claim` (+ `pdx relay seen`, branch A) | P6-0, P6-2b, P6-4 | 560 / 4 | CLI |
| **P6-6** | Mod: control message always consumed, kept until the running turn ends, then claim; write with 協作關係 facts; protocol `VERSION` 2; acceptance recipe | P6-5, P6-4 (P8a-2 if merged) | 600 / 5 | daemon + setup |
| **P6-7a** | Cross-host member relay: `POST /api/peers/team/relay`; forwarding; proxied wait; notices to a remote lead | P6-6, P4c-4 | 600 / 8 | daemon (both hosts) |
| **P6-7b** | After a cross-host `cleared`: member host → lead host `POST /api/peers/team/member-moved` (outbox); lead host updates the remote member row and keeps its old refs (`prev_refs`) for `matchMember` | P6-7a | 450 / 9 | daemon (both hosts) |
| **P6-8** | SPA: restart-confirm line `N 個接力進行中` | P6-2b | 120 / 5 | SPA |
| **P7-1** | Agent-status accessor (unless P6-4 branch B already added it); the 70% idle notice, armed once and re-armed after a relay or a drop below 70% | P6-4 | 620 / 8 | daemon |
| **P7-2** | Member auto-compact report: route, CLI, mod; the notice **disarms** the 70% notice; skill notice texts | P7-1 | 430 / 8 | daemon + CLI + setup |

Total ≈ 17 000 lines across 31 PRs. Spec order P4 → P4b → P4c → P6 → P7 is kept (coordinator decision 1).

---

## Shared contracts (read once; the PR sections refer to them)

**Exit codes** (spec §14) for the commands added here:

| Code | Cases |
|---|---|
| 0 | spawn done; kill done; team listed; relay accepted; claim (or `seen`) accepted; `relay lock|unlock` done; `--wait` ended `done` or hit its bound (op JSON printed) |
| 1 | runtime/API error; spawn `failed` with any reason other than `member_start_timeout`; relay `failed{handoff_incomplete}`; brief not sent after a successful spawn (stdout still carries the member); a flag file that cannot be written or removed |
| 2 | usage, including `--model` / `--effort` invalid (U20), both `--brief` and `--brief-file`, an unknown `pdx relay` word |
| 12 | relay op `cancelled` (`--wait`) |
| 13 | `not_lead`, `team_full`, `cwd_outside_grant`, `not_your_member`, `relay_unsupported`, `relay_open`, `bad_transition`, `not_your_op` (new), `no_host_for_repo`, `host_not_allowed`, `remote_unsupported`, `already_lead`, `member_cannot_lead` |
| 14 | `member_start_timeout` (spawn), `remote_unreachable`, relay `failed{member_unresponsive|member_gone}` (`--wait`) |
| 20 / 21 | daemon unreachable through the grace / plain 404 |

**`team.db` tables added in v3** (each `CREATE … IF NOT EXISTS` in `OpenStore`, `internal/module/team/store.go:31-72`; later columns go through one `ensureColumn` helper, P4-6):

| Table | PR | Key | Purpose |
|---|---|---|---|
| `teams` | P4-2 | `id` (= the approving request's id) | one row per approval; `lead_session_id` follows relays |
| `team_members` | P4-3 | `spawn_op` | the lead host's members (local and, from P4c-3, remote; `prev_refs` from P6-7b) |
| `spawn_ops` | P4-4 | `id` (client UUID) | persisted spawn steps (spec §9.3) |
| `remote_members` | P4c-2 | `spawn_op` | the member host's record of members whose lead is elsewhere (spec §7.4 (c)) |
| `forwarded_ops` | P4c-3 | `id` | forwarded spawn/kill/relay and the cross-host outbox (`lead_moved`, `end` on the lead host; `member_moved` on the member host, P6-7b) |
| `mod_hello` | P6-2a | `session_id` | the persisted `hello` (mod presence survives a restart) |

**Who writes the relay flag** (spec §6.6, coordinator decision on codex finding 4):
- `<data_dir>/hooklocks/cc/<session_id>`, content = the op id, is written **by the mod** through `pdx relay lock`.
- It goes up at the write turn's `turn.start` and down through `pdx relay unlock` before `/clear` and on every give-up.
- The daemon only **removes** it, by op id (compare-and-remove), when the op reaches `cleared` or any terminal state. That is a safety net for a mod that died.

**`Start` order after v3** (`internal/module/team/module.go:243-263`): MkdirAll relay dir → boot lease grace → start the notifier's virtual peer (P6-1) → `reconcileRelays` (P5a + P6-4) → resume running spawns (P4-5) and forwarded ops and the outbox (P4c-3/P4c-4) → `OnSubscribe` → sweepers → repo scan (P4b-2, async). `Stop` cancels `stopCtx`, waits for the sweepers **and** the spawn and forward goroutines (`spawnWG`), then closes the virtual peer.

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
func SpawnTmuxName(opID string) (string, error) // "tm-" + first 10 lowercase hex digits of a canonical UUID v4 (dashes removed); error otherwise (PR #1849 review)

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

All JSON keys are snake_case as listed. P4b-4 adds `SpawnRequest.Repo/Host`, `SpawnOp.Choice` and two codes. P4c-2 adds `host_not_allowed` and `admin_not_allowed`. P4c-3 adds `remote_unreachable`. Nothing is added ahead of its phase.

**Behaviour rules.**
- `ValidModel` accepts `opus`, `sonnet`, `fable`, `claude-opus-5-5` and `opus[1m]`. It refuses `""`, `a b`, `'x'`, `x;y`, `$(x)`, `-x`, a 65-character name and `opus[2m]`.
- `ValidEffort` is case-sensitive. `High` is refused.
- `SpawnTmuxName` is deterministic and **refuses anything but a canonical UUID v4** (36 characters, 8-4-4-4-12, version 4, RFC 4122 variant; upper or lower case in, lower case out) with an error and `""` (PR #1849 review: a fallback for non-UUIDs would put arbitrary characters, including tmux target characters, into a session name). Callers already hold a validated UUID v4; they still check the error.

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
- `func (m *Module) relayRole(sessionID string) (string, error)`: `"lead"` when `LiveTeamByLead`, `"member"` when `ActiveMemberInLiveTeam`, else `"none"`. **A store error is an error** (deviation 12): hello, self and begin answer 500, so the mod treats it as unavailable and nothing relays (fail closed; spec §8.7 (d)).
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

**Fix notes (PR #1859 review, binding).** Shipped as P4-3a (table, roles, `member_cannot_lead`, H1, H2) and P4-3b (the `cleared` moves, `team_id`, H3, R1).
- **H1, approve re-checks the member rule.** `CloseLeadApproved` checks in its write transaction that the origin is not an active member of a live team. If it is: `409 member_cannot_lead` without an approval, the row stays open (as `already_lead`).
- **H2, a member's self relay is never claimed.** The approve of a `self_relay` row is one write transaction (`CloseSelfRelayApproved`). If the origin is now an active member of a live team, the row closes `cancelled` and its op `cancelled{member_relay_is_leads}`, both or neither. decide answers `409 member_relay_is_leads` only after that commit (the `closed` event goes out; the mod's wait exits 12); a failed commit is 500 and nothing changed. `afterClose` and the awaiting-op reconciliation apply the same rule, and begin re-reads the role under `createMu` just before the op. The row is what matters: the mod follows the row, not the op.
- **H3, the member move needs a live team.** The `cleared` member UPDATE also requires the row's team to be live. An ended team's member rows stay as they ended (D4).
- **R1, no session holds two live roles.** When the old session has a live role, a new session holding either one fails the whole `cleared` (`ErrClearedTargetHasRole`): nothing moves, and the report answers **500**, not 400. This is a broken invariant (a `/clear` makes a fresh session), not a bad report: the mod re-sends it, and the op stays `written` for P6-4's reconciliation (#1735).
- After the lead move, the sweeper asks `LeadPresence(new sid, same pid, same start)`, which answers live (`TestTick_ALeadRelayKeepsItsTeam`).

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
  - `ListRunningSpawnOps(now int64) ([]spawnRow, error)` (PR #1862 review: a row that fails `checkRunning` is marked `failed{abandoned}` and not returned).
  - `CountRunningSpawns(teamID string, exceptID string) (int, error)`.
- `func launchLine(memberCommand, pluginDir, model, effort string) (string, error)`:
  - Result: `<member_command re-quoted word by word> --plugin-dir '<dir>'[ --model '<m>'][ --effort <e>]`, with no trailing newline (the runner adds `\n`). The default renders as `'claude' '--dangerously-skip-permissions' --plugin-dir '<dir>' …` (PR #1862 review).
  - It re-validates with `team.ValidModel` / `ValidEffort`.
  - It uses a local `shellQuote` (`'` → `'\''`), the same rule as `internal/agent/cc/statusline.go:85`, which is unexported.

**Behaviour rules.**
1. **Changed by PR #1862 review:** `member_command` must parse as **one simple command** (`hostconfig.ParseMemberCommand` → `MemberArgv{Env, Args}`: leading `NAME=value` words, plain words, single quotes, double quotes without `$`/backtick/`\`; outside quotes `# ; & | < > ( ) ` + "`" + ` $ { } * ? [ ~ ! \` and newlines are refused — PUT 400, the reader fails closed). `launchLine` re-quotes every word with `shellQuote` (env values as `NAME='value'`), so no member_command can swallow or change the appended flags. Every value the daemon appends is quoted, except the effort enum.
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
- `POST /api/team/spawns`, body `team.SpawnRequest` → **200 `team.SpawnOp`**, in state `running|done|failed`. This is create-or-join: the handler waits up to `SpawnPollWaitS` for the op to leave `running` (deviation 11). Errors:
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
   - Then insert the op (`step accepted`, `tmux_name, err = SpawnTmuxName(id)`; an error here is a programming error after the handler's UUID check → `500 internal`, nothing inserted), release `createMu`, and start `go m.runSpawn(id)` under `spawnWG`.
4. **The runner.** Each step is a CAS from the recorded step, and every terminal transition wakes the op's waiters (`addWaiter`/`wake`, `module.go:409-445`).
   - **accepted**
     - Tmux session absent: `CreateSession(tmux_name, cwd)`, then record `tmux_id`, `tmux_instance`, and `pane_id` from `ActivePaneMetadata` → `session_created`. A create error → `failed{session_create_failed}`.
     - Tmux session present but no `tmux_id` recorded: a crash between create and record, so adopt it (the name derives from this op's id).
     - Present on a brand-new op: `failed{tmux_name_taken}`.
   - **session_created**
     - Ensure the plugin tree: if `<data_dir>/cc-plugin/purdex/hooks/register.js` is missing and `agentcc.PluginSource != nil`, call `agentcc.ExtractPlugin(…)` (`internal/agent/cc/plugin.go:35,63`). It is never called on an existing tree (coordinator decision 9). A failure is logged.
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
- `TestSpawn_LaunchLineHasPluginDirModelEffortAndRegisters`: keys = `'claude' '--dangerously-skip-permissions' --plugin-dir '<dd>/cc-plugin/purdex' --model 'opus[1m]' --effort high\n` (word-by-word quoting, PR #1862); member row; title claimed; op done; `lead_address` set.
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

**Risks.**
- **Trust dialogs: measured, none (M27).** On 2026-10-07, with CC 2.1.292, `claude --dangerously-skip-permissions --model haiku` run in tmux in a directory never opened before (new under `/private/tmp`) behaved as follows:
  - the input box appeared within 2 s;
  - there was no folder-trust dialog and no bypass dialog;
  - `~/.claude/sessions/<pid>.json` was written;
  - the status line showed Haiku 4.5, so `--model` took effect.

  So a spawn does not stop at a dialog. Neither the runner nor the skill handles one.
- **No Purdex hooks.** A host without the Purdex hooks never produces a frame, so every spawn there times out. The CLI says so in P4-7.

**Fix notes (PR P4-5 review, binding).** The review (R1, attacker H1–H4, critic) changed the rules above as follows; where they disagree, these notes win.
- **Shipped as four stacked PRs**, each ≤ 800 lines:
  - **P4-5a**, tmux and session: `NewSessionTaggedContext` (`new-session ; set-option @opt v` in one invocation) and `PaneIdentity` (generation, session id, pane id, the user option and `pane_current_path` in one `display-message`); `session.CreateSessionTagged`. A tagged create that fails after its `new-session` reads the name back and kills an untagged session on its own generation.
  - **P4-5b**, the runner's create and launch steps.
  - **P4-5c**, registration, the member row, boot resume and the plugin tree.
  - **P4-5d**, the route.
- **Test names.** `TestSpawn_LaunchLineHasPluginDirModelEffortAndRegisters` is split into `TestSpawn_LaunchLineHasPluginDirModelAndEffort` (P4-5b) and `TestSpawn_AMemberThatRegistersIsStoredAndTitled` (P4-5c). The POST's answer, with `lead_address`, is `TestSpawn_PostAnswersTheMemberAndTheLeadAddress` (P4-5d).
- **Rule 4, the launch: record first, then send.** `launched` (with `launched_at`) is persisted **before** the generation-guarded send. Winning that compare-and-set is the right to send, so the line is typed at most once whatever races or restarts. The price is a crash window: a daemon that dies between the record and the send leaves a launched op that was never sent, and the next boot times it out (killed, `member_start_timeout`).
- **Rule 3, the checks (H1).** The POST checks in this order: lead → cwd → (one write transaction) lead again + limit, then insert. `AcceptSpawnOp` takes the write lock with `UPDATE teams … WHERE id = ? AND lead_session_id = ? AND ended_at = 0` (no row → `not_lead`). It then counts the team's running ops (this one excepted) plus **every** `active` member row (≥ `max_members` → `team_full`) and inserts, all in that one transaction.
  - The count is team.db alone and conservative. A member whose session ended holds its place until P4-6's sweeper marks its row `gone`.
  - "A live member … or whose relay is in flight" above is replaced by this count: a registry read before the transaction could undercount at its commit.
- **The cwd (H2), two more checks after the POST's.**
  - (a) Before the create, the runner resolves the op's cwd again and checks it against the team's roots (else `session_create_failed`, nothing created).
  - (b) Before any key, the pane's real `pane_current_path`, symlinks evaluated, must be under the roots. Otherwise the session is killed (recorded id and generation) and the op fails `launch_failed`, with nothing sent.
  - The cwd is still a path, not a handle: tmux takes only paths, and the lead runs as the same user. The roots are a guard rail, not a security boundary.
- **Ownership (H3).** The session is born with the session user option `@pdx_spawn_op` = the op's full UUID (the tmux name gives away 10 hex digits of it).
  - An accepted op that finds a session of its name adopts it only when one `PaneIdentity` answer shows that tag. Untagged (a stranger, or the name on a restarted server) or another op's tag → `tmux_name_taken`, left alone. "Present but no `tmux_id` recorded: adopt it" above is replaced by this.
  - Every later step re-reads the pane's identity: generation, session, pane and tag must all be the op's.
- **Registration (H4).** A verified frame on the pane is the member only after one `PaneIdentity` answer confirms that the pane is still the op's (generation, session id, tag). Frames carry no tmux identity of their own. A pane that answers but is not the op's → `failed{abandoned}`; an unreadable one is looked at again.
- **Registration deadline (R1, ruled after the critic).** Every poll judges `now ≥ launched_at + 20 s` first: kill, `member_start_timeout`. This holds even when the member has shown up by then, because nothing records when it registered.
- **Failure reasons.** No new wire reason:
  - H2 (a) → `session_create_failed`, H2 (b) → `launch_failed`;
  - H3 → `tmux_name_taken`;
  - H4 and a step team.db refuses to record (a corrupt row, a write error; P4-4 review) → `abandoned`, the session killed first. A refused write is not retried.

## PR P4-6 — kill, `pdx team`'s route, persisted usage, gone members

**Goal.** Spec §7.3 (`pdx kill`, `pdx team` with context, model and effort; U20 (e)), §8.5 "Persist it for teams only". Persisted usage sits here by plan v2's coordinator decision ("persisted usage on team rows: P4"); spec §12 lists it under P7 (deviation 7, coordinator decision 10).

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
  - `<host>/_xxxxxx`, `<host>/<name>`, `<host>/<name> [xxxxxx]`. In P4 the host must be this host (self alias or host id, from `c.Cfg.Peers` under `CfgMu`); P4c-4 extends it to remote hosts.
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
2. **Inbox.** `CLAUDE_CODE_MESSAGING_SOCKET`; unset → exit 1, as `pdx lead request` does (`lead.go:143-147`). `--cwd` defaults to the process cwd, made absolute (coordinator decision 6).
3. **Without `--model`.** stderr gets `team.ReminderNoModel`, then the spawn goes on and the exit code is unchanged (U20 (c)).
4. **The spawn loop.**
   - Mint a UUID v4. `POST /api/team/spawns` with `Idempotent()`.
   - While the op is `running`, POST the same body again (the daemon joins). Three consecutive hung attempts exit 20.
   - `done` → stdout one JSON line `{"ref","address","tmux_session","session_id","host_id","spawn_op"}`.
   - `failed` → `member_start_timeout` exits 14, with the stderr hint `member 沒有在 20 秒內啟動（這台主機需要 Purdex hooks：pdx setup --agent cc）`. Other reasons exit 1.
5. **The brief**, only after `done`.
   - `POST /api/peers/send` through `Once`, never replayed: `{to: member.address, origin_inbox: inbox, text: fmt.Sprintf(MemberBriefPrefixFmt, op.lead_address, op.team_id) + "\n" + brief}`.
   - On failure: stderr `pdx spawn: member 已開啟，但 brief 沒送出：<err>；請用 pdx msg send <address> 手動送`, exit 1, stdout already printed (coordinator decision 14).
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
2. **The account.** Read from `~/.claude.json` `oauthAccount.emailAddress` (seam `claudeJSONPath`) at `Start` and every 10 minutes on the sweeper. It is stored as `AccountFP = hex(sha256(lowercase email))[:16]` and never the email itself (coordinator decision 8). Unreadable → `""` and one log line per state change.
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
   - **404, or a 403 whose body is not a JSON `APIError`** → `Supported=false` ("older daemon"; coordinator decision 7). An older daemon's `PeerAuth` answers plain-text `forbidden` for an unknown `/api/peers/*` path (`internal/middleware/peer_auth.go:88-90`).
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

**Goal.** Spec §7.4 rule steps 1–5 and the stderr line; §7.2 `--repo` / `--host`. Execution is still local only (§7.4 (d)): a rule that picks another host answers `409 remote_unsupported` until P4c-3 (coordinator decision 18).

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
   - If every candidate is below the threshold, the same ordering still picks one. This case is unspecified (coordinator decision 17).
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
- CLI: `pdx peers host allow-team <alias> on|off [--config <path>]` (deviation 5: the existing grammar is singular `host`).
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

**Goal.** Spec §7.4 (c): `remote_members`; `POST /api/peers/team/spawn|kill|lead-moved` behind `HostRoutePolicy`; `AllowTeam` on the writes; accept only `principal.HostID == lead_host_id` with a matching `team_id`; spawn idempotent on the op id. Plus `POST /api/peers/team/end` (deviation 3, coordinator decision 19).

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
  - `type LeadMovedRequest struct{ TeamID, LeadSessionID, LeadRef string }`, with `lead_ref` added (deviation 3);
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
   - `cwd` must exist and lie under this host's own `team.repo_roots` (deviation 9, coordinator decision 5), else 409 `cwd_outside_grant`.
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

## PR P4c-3 — the lead host side, spawn: forwarding, classification, resume

Pre-split from the original P4c-3 per codex finding 6. Kill forwarding, the outbox and the team view are P4c-4.

**Goal.** Spec §7.4 (c):
- forwarding with op-id idempotency and the restart grace;
- `remote_unsupported`, `host_not_allowed`, `remote_unreachable`;
- the cross-host brief;
- U20 (f): model and effort travel with the spawn.

**Files.**
- Create `internal/module/team/forward.go`, `forward_store.go` and their tests.
- Modify `spawn_handler.go` (replace P4b-4's remote branch) and `module.go` (resume at `Start`).
- Modify `internal/team/wire_team.go` and its test (the code).
- Modify `cmd/pdx/team_cmd.go` (map the two new codes) and its test.

**Interfaces.**
- Schema. The outbox columns (`attempts`, `next_at`) are used from P4c-4 on.
  ```sql
  CREATE TABLE IF NOT EXISTS forwarded_ops (id TEXT PRIMARY KEY, kind TEXT NOT NULL,
    host_alias TEXT NOT NULL, host_id TEXT NOT NULL, path TEXT NOT NULL,
    body_json TEXT NOT NULL, state TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '',
    result_json TEXT NOT NULL DEFAULT '', attempts INTEGER NOT NULL DEFAULT 0,
    next_at INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
  ```
  `kind`:
  - `spawn` (here);
  - `kill`, `lead_moved`, `end` (P4c-4);
  - `relay` (P6-7a);
  - `member_moved` (P6-7b, on the member host).

  `state` is `forwarding`, `done` or `failed`.
- Codes `ErrRemoteUnreachable = "remote_unreachable"` (exit 14). The lead host maps the member host's JSON 403 `host_not_allowed` to 409 `host_not_allowed` (exit 13).
- `func (m *Module) forwardCall(ctx context.Context, f forwardRow) (status int, raw []byte, class forwardClass, err error)`. `forwardClass` is one of `ok`, `unsupported`, `not_allowed`, `transient`, `refused`; spawn and kill here, and the outbox and relay later, all classify through it.

**Behaviour rules.**
1. **Spawn to a remote host.**
   - The lead-host checks are unchanged: lead, `team_full` counting remote active rows, and no root check (the grant does not travel).
   - Persist `forwarded_ops{kind:spawn, state:forwarding}` and the local `spawn_ops` row (`host_id` = remote).
   - Loop `HostCaller.Call(POST /api/peers/team/spawn, RemoteSpawnRequest{… Model, Effort …})` with the same id. A `running` op loops; the total budget is 60 s.
   - `done` → insert `team_members` (`host_id` remote, `ref`, `session_id`, and `address = <entry alias>/_<ref>`, the lead's own alias for that host). The local op is done.
   - `failed` → failed with its reason.
2. **Classification** (`forwardCall`):
   - **404, or a 403 whose body is not a JSON `APIError`** → `unsupported` → `remote_unsupported` (coordinator decision 7).
   - **JSON 403 `host_not_allowed`** → `not_allowed`.
   - **Transport errors and 503** → `transient`. A spawn retries them with backoff 0.25 s → 1 s for a 30 s grace (spec §9.1 applied daemon-to-daemon), then `remote_unreachable`.
   - **Other 4xx** → `refused` (its code passes through).
3. **Restart.** The forwarded op is persisted, so a lead-host restart resumes it in `Start`. A re-POST is idempotent on the member host.
4. **The brief.** It is unchanged in the CLI because it is address-based. Its first line names `LeadAddress` (`<lead self alias>/<name|_ref>`); risk below.

**Tests.** Forwarding runs against two team modules over `httptest`.
- `TestForward_SpawnDoneRecordsTheRemoteMember`: model and effort reach the member host.
- `TestForward_RetryAfterLeadRestartOpensOneMember`.
- `TestForward_404AndPlain403AreRemoteUnsupported`.
- `TestForward_JSON403IsHostNotAllowed`.
- `TestForward_UnreachableThroughTheGraceIsRemoteUnreachable` (fake clock).
- CLI: `TestSpawnCmd_HostNotAllowed13RemoteUnreachable14`.

**Mutation gates.**
- Treat a plain 403 as `host_not_allowed` → its test red.
- Mint a new id per retry → the one-member test red.

**Size.** 520 lines, 8 files.

**Risks.**
- **Brief address.** If a member host knows the lead host under an alias other than the lead's self alias, the brief's lead address does not resolve from the member host. Replies still route through the message envelope; the member host also matches the host segment against the host id (`ipeers.HostMatches`). Noted, not fixed.

## PR P4c-4 — the lead host side, the rest: kill, the outbox, remote members in `pdx team`

**Goal.**
- Spec §7.4 (c): kill on another host; "the member host is told through the same route" (`lead-moved`); team end reaches the member host (coordinator decision 19).
- U20 (e): remote model and effort visible in `pdx team`.
- Codex finding 7: the outbox never gives up for good.

**Files.**
- Modify `internal/module/team/forward.go`, `forward_store.go` and their tests (the outbox pump).
- Modify `team_handler.go` (kill and view for remote members; `matchMember` on remote host segments), `relay_report.go` (after `cleared` applied: enqueue `lead_moved`), `sweeper.go` (on team end: enqueue `end`; pump the outbox) and their tests.
- Modify `internal/peers/record.go:22-26` and `internal/module/peers/module.go:779-787` (`ContextInfo` gains `model_id`, `effort`) with their tests.

**Interfaces.**
- `ipeers.ContextInfo` gains `ModelID string \`json:"model_id,omitempty"\``, `Effort string \`json:"effort,omitempty"\``. This is additive: older readers ignore it.
- `const outboxBackoffMin = 30 * time.Second`, `outboxBackoffMax = 10 * time.Minute`.
- `func (m *Module) enqueueOutbox(kind, hostAlias, hostID, path string, body any) error`.
- `func (m *Module) pumpOutbox(now int64)`.

**Behaviour rules.**
1. **Kill of a remote member.** Forward `RemoteKillRequest` through `forwardCall` (`transient` is retried through the 30 s grace, then `remote_unreachable`). A 200 sets the local row `killed`. `matchMember` accepts `<host>/…` for every host its team has members on.
2. **The outbox.**
   - After a lead relay's `cleared` (P4-3 moved `lead_session_id`), enqueue one `lead_moved` per remote host that has active members of that team.
   - When P4-2's sweeper ends a team, enqueue one `end` per such host.
   - Entries go out FIFO per host.
3. **Outbox retry: forever, with a cap.**
   - The backoff starts at 30 s, doubles, and is **capped at 10 minutes**. There is no attempt limit and no `failed` for a transient cause.
   - An entry ends only when:
     - it was delivered (2xx);
     - the row it describes no longer needs it: for `lead_moved`, the team ended meanwhile (an `end` follows);
     - the member host refused it for good: `refused`, e.g. 400, or JSON 409 `not_your_member` because the member host has no such rows. One log line;
     - the host was unpaired (`HostCaller` → `ErrHostUnknown`). One log line.
   - `unsupported` (an older member daemon) keeps retrying at the cap: the host may be upgraded.
4. **No member-host pull.** Infinite retry is enough:
   - The outbox lives in the same `team.db` as the team rows it describes, and is retried while the host stays paired.
   - The only loss it cannot cover is a lead host whose `team.db` was deleted, or an unpairing. In the latter case the member host could not ask the lead host either.
   - A pull route (`GET /api/peers/team/state`, the member host asking at boot and every 10 min) would cover only the deleted-`team.db` case, for about 220 lines. It goes to a follow-up issue, not v3.
5. **The team view for remote members.**
   - Fetch `/api/peers` from each member host through `HostCaller` (3 s; host principals may GET it). Match by session id, and take `agent.context` including the new `model_id` and `effort`.
   - An unreachable host leaves the columns blank, and the CLI marks the row `(主機無回應)`.

**Tests.**
- `TestForward_KillMarksTheRowKilled`.
- `TestKill_ByRemoteAddress`.
- `TestOutbox_LeadMovedAfterALeadRelay`.
- `TestOutbox_EndAfterTeamEnd`.
- `TestOutbox_RetriesForeverWithBackoffCappedAt10Min`: fake clock; 200 transient failures are still retried, at most 10 min apart; then delivered → done.
- `TestOutbox_DropsOnPermanentRefusalOrUnpairedHost`.
- `TestOutbox_LeadMovedDroppedWhenTheTeamEnded`.
- `TestOutbox_FIFOPerHost`.
- `TestTeamView_RemoteMemberShowsModelAndEffort`.
- `TestBuild_ContextCarriesModelAndEffort`.

**Mutation gates.**
- Give up after N attempts → the forever test red.
- No 10-min cap → the backoff assertion red.
- No outbox retry → the end test red.

**Acceptance** (after P4c-4; needs both daemons at this version, and air26's daemon has been behind before):
1. On mlab, allow-team for a26.
2. From a26, a throwaway lead runs `pdx spawn --host mlab --model sonnet --brief …`.
3. `pdx team` on a26 shows the mlab member with its model and effort.
4. `pdx kill` it.
5. Repeat with allow-team off → exit 13 `host_not_allowed`.
6. Stop mlab's daemon, end the a26 team, start mlab's daemon again: the `end` lands within 10 min and the mlab member's `hello` answers `role:"none"`.

**Size.** 620 lines, 10 files.

**Risks.** None.

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

**Goal.** Spec §8.5 "Daemon notices come from the daemon's own virtual peer … A reply to it gets one line back"; §7.4 (c) "notices … go through `POST /api/peers/send` from the member host's virtual peer"; §8.4 "When a lead relays, the daemon also tells each active member". Plan v2's P5a section deferred this notice to P4. It lands here, with the notifier it needs (deviation 8).

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
  - `const MinMemberRelayModVersion = 2`, `RelayControlPrefix = "[pdx-relay:control] op="`, `RelayClaimTimeoutS = 60`, `RelayStallTimeoutS = 900`.
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

**Risks.** #1832 (`modPresent` never expires) is unchanged in kind. Persistence lets presence outlive a restart, which matches how long the process lives (coordinator decision 11).

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
   - **Target.** `matchMember` (P4-6) on an **active local** member. P6-7a adds remote members.
   - **Running.** The member is live (`ResolveOriginBySession`).
   - **Mod.** `modSeen[member sid].ModVersion` ≥ 2 (decimal), else `relay_unsupported`. A mod of protocol 1 cannot handle the control message (deviation 14).
   - **One op.** `OpenRelayOpBySession` → 409 `relay_open` with the op.
   - **Insert.** `{ID: req.ID, Kind member, TeamID, SessionID, Ref, State requested, HandoffPath: <relay dir>/<id>.md, PID, PaneID}`, where PID and pane come from the member row. `ErrRelayOpOpen` from the insert → the same 409.
2. **Control message.** After the commit, `notify(<self alias>/_<member ref>, "[pdx-relay:control] op=<id>")`. A failure is logged; the claim timeout (P6-4) covers it.
3. **Claim.**
   - Op kind must be `member`, else 409 `bad_transition`.
   - `op.SessionID == req.SessionID`, else 409 `not_your_op`.
   - `ReportRelay(claimed)`: Applied or Noop → 200 with `Lead{Address: <live origin address of teams.lead_session_id, else <self alias>/_<lead_ref>>, Ref: lead_ref, TeamID}`. BadTransition → 409 with the op.
4. **Wake-ups.** Every applied report and claim wakes `op.ID` waiters (`addWaiter`/`wake`).
5. **The claim does not lock.** The relay flag is the mod's to raise, at the write turn (P6-3c, coordinator decision on codex finding 4).

**Tests.**
- `TestRelayCreate_Checks`: `not_lead`, `not_your_member`, `unknown_session`; no hello → `relay_unsupported`; version 1 → `relay_unsupported`; `relay_open`; replay; `id_conflict`.
- `TestRelayCreate_SendsTheControlMessageToTheMember`.
- `TestRelayCreate_TableConflictIs409`.
- `TestClaim_OnlyTheTargetSession` (the spec's mutation gate).
- `TestClaim_IsIdempotentAndCarriesTheLead`.
- `TestClaim_SelfOpIsBadTransition`.
- `TestRelayOp_LongPollWakesOnReport`.
- `TestClaim_RaisesNoFlag`.

**Mutation gates.**
- Drop the claim's session check → `…OnlyTheTargetSession` red (spec §15).
- Drop the version comparison → the version-1 case red.

**Size.** 650 lines, 6 files.

**Risks.** None. Everything that depends on M28 is in P6-4, P6-5 and P6-6.

## PR P6-3a — the mod embeds the git facts; one `Write` of the whole file

**Goal.** Coordinator decision on codex finding 3. The relay lock is exactly spec §6.6: only the handoff `Write` is allowed. But the shipped write prompt tells the model to run `git status`, `git diff --stat` and `git log --oneline -10` itself (`register.js:281`), and those calls would be denied once the lock is up.

So the **mod** runs these read-only git commands itself and embeds their output, and the model only writes. This PR must ship **before or with** the lock taking effect, which is P6-3c (the mod raising the flag). It is mod-only and safe alone, so it may land any time.

**Files.**
- Modify `cmd/pdx/plugin/purdex/hooks/register.js`: `writePrompt` and `fixPrompt` (`:275-306`), and the approved branch of `settle` (`:481-505`, collect the facts before `arm`).
- Modify `relay.test.ts`.
- Modify `SKILL.md` ("Self relay": write the file the prompt names with one `Write`, then answer `HANDOFF-WRITTEN`).

**Interfaces.**
- `async function gitFacts($)` returns `{ status, diffStat, log }`, each a string.
  - It runs `git status --short`, `git diff --stat` and `git log --oneline -10` through `$.process.run`, each with `timeoutMs: 10_000`, in the session's working directory.
  - **Check the 2.1.292 d.ts in this PR** (a read, not a measurement): which call gives that directory (a `$.session` accessor, or `$.process.run`'s default `cwd`). Run `git -C <dir>` when the d.ts gives a directory but not a default. If neither exists, stop and report.
  - Each output is capped at 200 lines / 12 000 bytes, with a final `…（截斷）`.
  - A failure embeds `（git 失敗：<first stderr line>）`, which also covers "not a git repo".
- Write prompt changes:
  - The line `自己跑 git status…` is removed.
  - New block `機器提供的 git 狀態（照抄進 §3）：` with the three outputs, fenced.
  - New requirement `- 只用 Write 工具一次寫入整個接力檔；這一輪不要執行其他工具（接力期間其他工具會被拒絕）。`
- Fix prompt: `… 請用 Write 重寫整個接力檔（不要用 Edit），補齊後只回「HANDOFF-WRITTEN」。`

**Behaviour rules.**
1. The facts are collected once per relay, from the timer that already runs `whoami` (`settle`'s approved branch). They are not collected again for fix rounds.
2. Nothing else changes: the nonce, the check (8 headings, > 200 characters) and the fix rounds stay as shipped.

**Tests** (`relay.test.ts`):
- `the write prompt embeds git status, diff --stat and log from the mod's own runs`.
- `the write prompt no longer asks the model to run git`.
- `git output is capped and a failing git is embedded as such`.
- `the fix prompt asks for one Write of the whole file`.

**Mutation gates.** Keep the old instruction line → the second test red.

**Size.** 300 lines, 3 files.

**Risks.** The working-directory API is the one unknown; see the d.ts check under Interfaces.

## PR P6-3b — the relay-lock machinery: flock helpers, `pdx relay lock|unlock`, the decide answer, `allow` in `pdx hook`

**Goal.**
- Spec §6.6 table row 2, **exactly**: the session is in a relay op past `claimed`, and the tool is not the handoff write → PreToolUse `deny`, reason `接力進行中，這一輪只寫接力檔`. A `Write` to exactly `<data_dir>/relay/<op>.md` is `allow`.
- Spec §6.6 "Who writes the flag: … the Purdex mod while a relay op runs": the CLI the mod calls.
- Plan v2 coordinator decision: "P6 must check the relay lock before that removal".
- Codex findings 3 and 4.

In this PR no mod raises a flag yet (that is P6-3c), so running sessions see no change.

**Files.**
- Create `internal/team/hooklock_flock.go` and its test: move `openHookLockLocked`, `writeHookLock`, `removeHookLock` and `hookLockExists` out of `cmd/pdx/hooklock.go` (`:23-102`), exported, with their tests. Delete `cmd/pdx/hooklock.go`.
- Modify the call sites `cmd/pdx/lead.go:197-198` and `cmd/pdx/hook.go:249`.
- Modify `cmd/pdx/hook.go:240-283` (print `allow`) and `hook_decide_test.go`.
- Modify `internal/team/wire.go:211-218` (`Decision` comment: `"deny" | "allow" | ""`).
- Create `cmd/pdx/relay_lock_cmd.go` and its test.
- Modify `cmd/pdx/relay.go` (dispatch).
- Modify `internal/module/team/hooks.go:23-175` (decide order, prune guard), `hooks_test.go`, and `relay_report.go` (the safety net in `afterReport`).

**Interfaces.**
- `team.WriteHookLock(path, id string) error`.
- `team.RemoveHookLock(path, id string) (removed bool, err error)`: compare-and-remove under flock, as P2c-2 shipped it.
- `team.HookLockExists(path string) bool`.
- CLI:
  ```
  pdx relay lock <op> --session <sid> [--config <path>]
  pdx relay unlock <op> --session <sid> [--config <path>]
  ```
  - Both are **local file operations** on `team.HookLockPath(cfg.DataDir, "cc", sid)` with the op id as content. There is no daemon call, so they work during a daemon restart.
  - The op id must be a single path element (UUID form).
  - Exit 0: done, including an `unlock` that found someone else's or no flag. Exit 1: an I/O error. Exit 2: usage.
- Relay-lock answers on `/api/hooks/decide`:
  - **PreToolUse `Write`** whose `tool_input.file_path` equals the op's `handoff_path` (both `filepath.Clean`'d; a relative path never matches) → `{decision:"allow", reason:"Purdex 接力檔", lock:"relay", id:<op>}`.
  - **Any other PreToolUse**, `Edit` included → `{decision:"deny", reason:"接力進行中，這一輪只寫接力檔", lock:"relay", id:<op>}`.
  - **PermissionRequest** → `{}`, flag kept. The spec gives the relay row no PermissionRequest answer, and a PreToolUse `allow` already skips the prompt.
- `pdx hook` prints, for PreToolUse only:
  - `allow` as `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":<reason>}}` (M18, and the same shape for Codex, M20);
  - `deny` as today (`hook.go:276-282`).

**Behaviour rules.**
1. **Decide order**, under `createMu`:
   1. the lead lock (unchanged; it wins);
   2. the relay lock: `OpenRelayOpBySession(sid)` in `claimed|writing|written` and agent `cc`;
   3. only when neither applies, the existing `{}` plus flag removal (`openLeadAndRemoveStaleFlag`).

   A flag left behind by a relay that ended therefore costs one `{}` and is removed, as P2c intended.
2. **The daemon never raises the flag.** As a safety net for a mod that died, it compare-and-removes `HookLockPath(dataDir, cc, op.SessionID)` **by op id** when the op reaches `cleared` (the old session id) or any terminal state, in `afterReport`. Reports, reconciliation and timeouts all pass through it. A flag holding a lead request's id is never touched.
3. **Prune.** `pruneUnlessLeadOpen` (`hooks.go:161-175`) also keeps a flag whose session has an op in `claimed|writing|written`.

**Tests.**
- `TestHookDecide_RelayLockAllowsExactlyTheHandoffWrite`.
- `TestHookDecide_RelayLockDeniesEditReadBashAndOtherPaths`.
- `TestHookDecide_RelayLockPermissionRequestIsEmpty`.
- `TestHookDecide_RelayLockIsCheckedBeforeFlagRemoval`.
- `TestHookDecide_LeadLockWinsOverRelay`.
- `TestSafetyNet_RemovesTheRelayFlagByOpIDAtClearedAndTerminal_KeepsALeadFlag`.
- `TestPrune_KeepsTheFlagOfAnActiveRelay`.
- `TestRelayLockCmd_LockUnlockCompareAndRemove`.
- `TestRelayLockCmd_UsageAndBadOpID`.
- `TestRunHook_PrintsAllowAndDenyShapes`.
- The moved `TestHookLock_*` tests, unchanged in content.

**Mutation gates.**
- Allow `Edit` too → the deny test red.
- Put the removal before the relay check → `…BeforeFlagRemoval` red.
- A blind (not by-op-id) safety-net remove → the lead-flag case red.
- `pdx hook` printing only deny → the allow shape red.

**Size.** 720 lines, 12 files.

**Risks.** None. Until P6-3c no flag is raised by a relay.

## PR P6-3c — the mod raises the flag at the write turn and lowers it before `/clear`

**Goal.** Spec §6.6 (the mod writes the flag while a relay op runs on that session) and U17 use 2. The coordinator's precision (codex finding 4): the flag goes up only in **the turn that actually writes the handoff**, after any running or released turn has completed, and comes down before `/clear`.

**Files.**
- Modify `cmd/pdx/plugin/purdex/hooks/register.js`:
  - `turn.start` (`:600-608`);
  - `onWriteTurnDone` (`:531-565`, unlock before `/clear`);
  - `toIdle` / `giveUp` (`:331-336`, `:517-523`, unlock on every give-up);
  - the pending object gains `locked`.
- Modify `relay.test.ts`.

**Behaviour rules.**
1. **Raise.** In `turn.start`, when the write nonce matches (state `approved`, the write prompt's turn), the hook awaits `pdx relay lock <op> --session <sid>` (8 s bound) **before** `next(e)`. Its own `$` call is in flight, so the hook budget pauses.
   - On exit 0: `p.locked = true`.
   - On any failure: one log line and the write goes on (fail-open: the lock is a guard, never a reason to lose the relay).
   - Fix-round turns do not lock again.
   - **If the kit shows that `turn.start` cannot hold the turn,** raise it right before `$.prompt.submit` of the write prompt instead, and say so in the PR. Released prompts queued ahead would then run under the lock, which is acceptable: NOTE already tells them to stay short.
2. **Lower.** Before `$.command.run('clear')` in `onWriteTurnDone`, the timer awaits `pdx relay unlock` (8 s). A failure is logged and the `/clear` goes on: the daemon's safety net removes the flag at `cleared`.
3. **Every give-up** with `p.locked` (failed check, a refused prompt or `/clear`, the user's own `/clear`, a compaction while approved) unlocks from a timer, never awaited in a hook.
4. **Member relays** (P6-6) use the same write path, so they inherit 1–3.

**Tests.**
- `the write turn's turn.start runs pdx relay lock before next(e)`.
- `a released prompt's turn does not lock`.
- `unlock runs before /clear`.
- `every give-up path unlocks`.
- `a failed lock does not stop the write`.
- `fix rounds do not lock again`.

**Mutation gates.**
- Lock at claim or approval instead of the write turn → the released-prompt test red.
- Unlock after `/clear` → the order test red.

**Size.** 320 lines, 3 files.

**Risks.** Whether `turn.start` can hold the turn is checked in the kit (rule 1 has the fallback).

## PR P6-4 — timeouts, reconciliation from frames (#1735), completion and failure notices

**Goal.**
- Spec §8.2 timeouts: claim 60 s → `failed{member_unresponsive}`; whole op 15 min (here a 15-min stall, deviation 13).
- §9.3 relay ops: `requested` → re-send; past `claimed` → compare with the pane's verified frame; a different session id → write the lineage; no frame → `failed{member_gone}`.
- Issue #1735: a stuck op is ended, then follows its state's retention rule.
- §8.2 step 8 and "the lead is told about every failure".

**Files.**
- Create `internal/module/team/relay_reconcile.go` and `relay_reconcile_test.go`.
- Modify `relay_report.go:270-332` (`reconcileRelays` calls the new pieces), `sweeper.go` (runtime timeouts), `notice.go` and `retention.go:41-55` (comment only: #1735 closed), with tests.
- **Branch A only:** modify `internal/team/wire_relay.go` and its contract test (`RelayOp.SeenAt`), `relay_store.go` (column `seen_at INTEGER NOT NULL DEFAULT 0` via `ensureColumn`), and `relay_member.go` (the `seen` route) with its test.
- **Branch B only:** modify `internal/module/agent/module.go` (the `AgentStatus` accessor, moved here from P7-1) and its test.

**Interfaces.**
- `func (m *Module) reconcileFromFrames(ctx context.Context, op team.RelayOp) (team.RelayOp, error)`.
- `func (m *Module) opBinding(op team.RelayOp) (pid int, pane string)`: the op's columns, else (old self ops) the approval row's `Origin.PID` and the pane of `Origin.Tmux`.
- Branch A:
  - `RelayOp.SeenAt int64 \`json:"seen_at,omitempty"\``.
  - `POST /api/relay/ops/{id}/seen`, body `RelayClaimRequest` → 200 `{op}` | 404 | 409 `not_your_op` / `bad_transition`. It records `seen_at` once (a CAS on `seen_at = 0 AND state = 'requested'`; a repeat answers 200 unchanged), changes no state, and is accepted only from the target session.
- Branch B: `type AgentStatusReader interface{ AgentStatus(tmuxSession string) (string, bool) }`, implemented by `*agent.Module` under `m.mu` (`currentStatus` is keyed by tmux session name, `internal/module/agent/handler.go:614`).

**Claim timeout: the basis is decided by M28** (to be measured before this PR starts). Both branches are written. The claim itself always comes after the member's running turn has completed (coordinator ruling on codex finding 1).

- **Branch A — M28 shows `session.receive` fires while a turn runs. Count from the request.**
  - The mod consumes the control at once and sends `pdx relay seen` right away (route here, CLI in P6-5, mod in P6-6). It claims after the running turn ends.
  - The daemon fails `requested` with `seen_at = 0` and `created_at + 60 s` passed → `failed{member_unresponsive}`.
  - After `seen`, an op still `requested` 15 min after `seen_at` → `failed{member_unresponsive}`: the relay waits at most 15 min for the member's running turn.
  - This detects a dead mod within 60 s even while the member keeps running.
- **Branch B — M28 shows it fires only when idle. Count from the member's next idle.**
  - The delivery itself waits for the turn's end, so no early sign is possible.
  - The sweeper records, per `requested` op in memory, the first liveness tick at or after `created_at` where `AgentStatus(member tmux) == "idle"`. It fails the op 60 s after that (`failed{member_unresponsive}`). The record restarts at boot.
  - A member whose status is not readable counts as idle, so the op cannot wait forever.
- **If M28 is not measured before this PR starts, implement branch B** and drop branch A (and `pdx relay seen` from P6-5/P6-6). Branch B is correct for either outcome. Branch A is valid only for M28 = yes, and adds only the earlier dead-mod detection.

**Behaviour rules.**
1. **`reconcileFromFrames`.**
   - **Another session on the pane.** A verified frame on the pane (`LiveSessions(ctx,"cc")`) with `sid' != op.SessionID` and `ResolveOriginBySession(sid').PID == pid` → `ReportRelay(cleared, sid', RefID(sid'))`, then `afterReport` (title move, flag safety net, handover). Another process's session is never written.
   - **CC gone.** No verified frame on the pane and `!LiveSession(op.SessionID)` → `failed{member_gone}`.
   - **Same session, alive.** Left as is.
2. **Boot.** `reconcileRelays` adds:
   - a member op in `requested` → re-send the control message (the claim is a CAS);
   - any op in `claimed|writing|written` → `reconcileFromFrames`.
3. **Sweeper, liveness tick.**
   - The claim timeout of the chosen branch.
   - **Stall.** An op of either kind past `claimed` with no state change (`updated_at`) for 15 min → `reconcileFromFrames`. If still not terminal: `failed{member_unresponsive}` for a member op, `failed{handoff_incomplete}` for a self op (coordinator decision 4).
   - The 15 minutes count from the last state change, not from the request, because the claim deliberately waits for the member's running turn. `cleared` → `failed` is legal (`relayTransitions`).
4. **Notices to the lead**, member ops only, sent on `ReportApplied` only:
   - `done` → the done text;
   - `failed` / `cancelled` → the failure or cancel text.
   - The lead address is the live origin address of the team's `lead_session_id`, else `<self alias>/_<lead_ref>`. P6-7a extends this to remote leads.

**Tests.**
- `TestReconcile_PastClaimedWithANewFrameSessionWritesTheLineage` (#1735).
- `TestReconcile_NewFrameUnderAnotherPIDIsNotCleared`.
- `TestReconcile_NoFrameFailsMemberGone`.
- `TestReconcile_SameSessionAliveIsLeftAlone`.
- `TestReconcile_RequestedResendsTheControl`.
- Claim timeout:
  - branch A: `TestSeen_OnlyTheTargetSessionOnceWhileRequested`, `TestSweep_UnseenRequested60sFails`, `TestSweep_SeenRequestedWaitsUpTo15Min`;
  - branch B: `TestSweep_ClaimTimeoutCountsFromTheNextIdle` (a member running for 5 min is not failed; 60 s after its idle it is).
- `TestSweep_StallAfterClaim15Min`.
- `TestSweep_SelfOpStalledFails`.
- `TestNotice_DoneAndFailureOncePerTransition`.
- `TestRetention_AStuckOpEndedByReconcileLosesItsFileByItsStateRule`: #1735 end to end with P5a-3a's sweeper.

**Mutation gates.**
- Skip the PID check → `…AnotherPID…` red.
- Reconcile only `awaiting_approval` (today's behaviour) → the #1735 test red.
- Notify on Noop → the once test red.
- Branch B: count from `created_at` → the running-member case red.

**Size.** 720 lines, 7 files (branch B, with the accessor). Branch A: about 780 lines, 10 files.

**Risks.** M28 should be known before this PR starts. Without it, branch B (Global constraints, Order).

## PR P6-5 — CLI: `pdx relay <ref> [--wait <dur>]`, `pdx relay claim` (+ `pdx relay seen`, branch A)

**Goal.** Spec §8.2 ("`pdx relay` returns once the op is accepted and prints the op id. `--wait` blocks until done or failed"), §8.3 `claim`, §14.

**Files.**
- Modify `cmd/pdx/relay.go` (dispatch, usage, refusal map).
- Create `cmd/pdx/relay_member_cmd.go` and `relay_member_cmd_test.go`.

**Interfaces.**

```
pdx relay <ref|address> [--wait <dur>] [--config <path>]
pdx relay claim <op> --session <sid> [--config <path>]
pdx relay seen <op> --session <sid> [--config <path>]      (M28 branch A only)
```

**Behaviour rules.**
1. **Dispatch.** A first argument that is not one of the known words is a member relay when it matches `^_[0-9a-z]{6}$`, contains `/`, or has the `name [xxxxxx]` form. Otherwise it is a usage error, exit 2.
   - Known words: `hello`, `begin`, `wait`, `self`, `report`, `op`, `claim`, `lock`, `unlock`; `seen` in branch A; `compacted` from P7-2.
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
4. **`claim`** → stdout the claim JSON. **`seen`** → stdout the op JSON. `not_your_op` → 13. `bad_transition` → 13, with the op on stdout.
5. **The refusal map** (`relay.go:47-53`) adds `not_lead`, `not_your_member`, `relay_unsupported` and `not_your_op`. The code stays the last stderr token.

**Tests.**
- `TestRelayCmd_RefDispatchAndUnknownWordIs2`.
- `TestRelayCmd_MemberRelayPrintsTheOpExit0`.
- `TestRelayCmd_RefusalsExit13CodeLast`.
- `TestRelayCmd_ReplayAfterALostResponseIsTheSameOp`.
- `TestRelayCmd_WaitMapping`: a table of the five outcomes.
- `TestRelayCmd_ClaimPrintsAndMaps`.
- Branch A: `TestRelayCmd_SeenPrintsAndMaps`.

**Mutation gates.** Map `member_gone` to 1 → the mapping table red.

**Size.** 560 lines, 4 files (branch B: about 520).

## PR P6-6 — the mod: control always consumed, claim after the running turn, write with team facts; protocol 2; acceptance

**Goal.**
- Spec §8.2 steps 3–8 for members.
- §15 Mod: "the control message is consumed only with the marker; claim failure does nothing; write → check → fix rounds → clear → seed".
- §8.2 step 4: "§8 協作關係 is filled from the claim. A member gets its lead and team id; a lead gets its roster".
- §15 real acceptance 2 and 3 (relay half).
- Codex finding 1 (critical): the control message is **always** consumed, busy or idle. While a turn runs, the mod keeps the op and claims after that turn's `turn.complete`.
- Rebase onto P8a-2 if it merged; it touches `hooks.json` and `embed_test.go`, not `register.js`.

**Files.**
- Modify `cmd/pdx/plugin/purdex/hooks/register.js`:
  - `VERSION` (`:33`) becomes `'2'`;
  - a new `session.receive` hook;
  - `settle`'s approved branch (`:481-505`, already holding P6-3a's facts) is factored into `startWrite($, p)`;
  - `writePrompt` facts (`:275-302`);
  - main-turn tracking and the deferred claim in `turn.start` / `turn.complete` (`:600-627`).
- Create `cmd/pdx/plugin/purdex/hooks/member.test.ts`.
- Modify `relay.test.ts` (the hello `--version 2` assertion).
- Modify `SKILL.md` (As a member: the lead relays you; when 接力 starts, write the file asked with one `Write` and nothing else).
- Create `docs/testing/member-relay-acceptance.md`.

**Interfaces.**
- New state: `s.turnRunning` (a main-conversation turn is between `turn.start` and `turn.complete`, `e.agentId` unset) and `s.control` (`{ opId }` or `undefined`, the newest control kept).
- `session.receive`:
  - The text contains `RelayControlPrefix` followed by a UUID, and `e.origin.kind === 'peer'` (M1) → **always** return `{ consumed: true }`, whatever the state. The model never sees it.
  - Unless the op is already `s.pending`'s, set `s.control = { opId }`.
  - **Branch A:** also run `pdx relay seen <op> --session <sid>` from a timer at once, never awaited.
  - Then, if `!s.turnRunning && s.state === 'idle'`, schedule `later($, 0, claim)`. Otherwise the claim waits.
  - Any other text → `next(e)`.
- `turn.complete` (main conversation): if `s.control` is set and `s.state === 'idle'`, schedule the claim from a timer. This runs before `maybeBegin`, which a member never runs anyway.
- `claim`:
  - `pdx relay claim <op> --session <sid>` (`CALL_TIMEOUT_MS`); `s.control` is cleared.
  - Exit 0 → `s.pending = {op, requestId: undefined, path: op.handoff_path, oldSession: sid, oldRef: op.ref, before: usageLine(u), lead: body.lead, answer: deferred()}` with `answer` resolved `'approved'`; `s.state = 'approved'`; then `startWrite` (no turn is running at this point).
  - Any non-zero exit → nothing, so the daemon's timeout reports it.
- `writePrompt` facts, one line each:
  - member: `- 我的 lead：<lead.address>（ref <lead.ref>，team <lead.team_id>）`;
  - lead (`s.role === 'lead'`): `- 我管理的 members：` followed by `pdx team --json` rows (`ref`, `address`, `title`, `cwd`), or `無`.

**Behaviour rules.**
1. **Everything after the claim** is the shipped path, now with P6-3a's facts and P6-3c's lock:
   - write → check → fix ×2 → `written`;
   - unlock → `/clear` → `cleared` with the new id → seed → `done`.
   - The reports carry the member op's id.
2. **The `prompt.submit` hold** (P5b-3) is untouched. It only applies to `awaiting` / `beginning`, which a member relay never enters.
3. **A member never calls `maybeBegin`.** Unchanged (`register.js:338-340`).
4. **The model never decides** a relay (U9). The control message is invisible to it in every state (M1).

**Tests** (`member.test.ts`). Each test drives the engine through the interface: `turn.start` / `turn.complete` / `session.receive` from the test's own `on` hooks.
- `the control message is consumed only with the marker and only from a peer`.
- `a control that arrives while a turn runs is consumed, not claimed, and claimed at that turn's turn.complete`. It asserts the sequence `session.receive` → `{consumed}` → no `pdx relay claim` → `turn.complete` → `pdx relay claim` → write prompt.
- `a control that arrives while idle is claimed at once`.
- `a claim that fails does nothing`.
- `the write prompt carries the lead facts`.
- `the full member path reports written, cleared (new id), done under the member op id; lock before the write turn, unlock before /clear`.
- `a lead's write prompt carries the roster from pdx team --json`.
- `hello reports --version 2`.
- Branch A: `a control that arrives mid-turn sends pdx relay seen at once`.

**Mutation gates.**
- Return `next(e)` for a busy control → the mid-turn test red (codex finding 1).
- Claim before the running turn completes → the same test red.
- Consume without the marker check → the first test red.

**Acceptance** (`docs/testing/member-relay-acceptance.md`, throwaway sessions, scratch plugin copy as `docs/testing/self-relay-acceptance.md` does):
1. A throwaway lead spawns two members.
2. `pdx relay _<ref>` on one, **once while the member is idle and once while it runs a long turn** (`Bash sleep 90`). In both cases the model never shows the control text. The member writes, clears and seeds. The lead gets the done notice.
3. `pdx msg send <host>/_<old ref>` still reaches the member (lineage).
4. Restart the daemon mid-relay: the relay finishes, or is reconciled from frames.
5. The statusline before and after shows the same `model.id` / `effort.level` (U20 (f), M21).
6. Afterwards, no flags are left and no op is open.

**Size.** 600 lines, 5 files.

**Risks.** `register.js` grows past 900 lines. Splitting the member path into `hooks/member.js` would need shared state across modules. That is left for a follow-up issue, not this PR.

## PR P6-7a — cross-host member relay: the relay route, forwarding, proxied wait, remote-lead notices

**Goal.** Spec §7.4 (c) "`POST /api/peers/team/relay` … The relay op row and the lineage live on the member host. Notices to the lead … from the member host's virtual peer to the lead's cross-host address"; §12 "P6 rides on P4c for a member on another host". The route is deferred from P4c to here (deviation 2).

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
   - A `remote_members` row with that session id, team and `lead_host_id == principal.HostID`, else `not_your_member`. P6-7b adds the lineage-forward lookup.
   - Then P6-2b's create steps from "running" on: live check, mod version, one op, insert, control message.
   - Done, failure and P7 notices go to `<lead_host_alias>/_<lead_ref>`, the member host's own alias for the lead host (`principal.Alias` at spawn).
2. **On the lead host.** `POST /api/team/relays` for a remote member persists `forwarded_ops{kind:relay}` and forwards through `forwardCall` (P4c-3).
   - `GET /api/relay/ops/{id}?wait` for a forwarded op re-posts the relay with the same id to the member host (≤ 8 s per call) and answers the op it returns. `pdx relay <ref> --wait` therefore works unchanged.

**Tests.**
- `TestPeerRelay_BindingAndAllowTeam`.
- `TestForwardRelay_CreatesTheOpOnTheMemberHost`.
- `TestForwardRelay_WaitIsProxied`.
- `TestNotice_RemoteLeadAddressedByTheMemberHostsAlias`.
- `TestHostRoutePolicy_TeamRelayExactPath`.

**Mutation gates.** Address notices by the lead's self alias → the alias test red.

**Size.** 600 lines, 8 files.

## PR P6-7b — the lead host learns the member's new session after a cross-host relay

**Goal.** Codex finding 2 (critical). After a cross-host relay, the member host's `cleared` transaction moves its own `remote_members` row and lineage, but the lead host's `team_members` row still names the old session and ref. Then `pdx team`, `pdx relay <old ref>` and `pdx kill <old ref>` on the lead host would all target a session that no longer exists.

Spec §8.4 (member row moves with the relay; the old ref keeps working) holds across hosts **eventually**, through the outbox. A cross-host transaction does not exist.

**Choice.** A new route on the **lead host**, `POST /api/peers/team/member-moved`, fed by the member host's outbox (P4c-4's pump, every host runs it). The alternative, carrying the move in the proxied `--wait` answer, was rejected: a lead that never waits would never learn it.

**Files.**
- Modify `internal/module/team/peer_team_handler.go` (the route) and its test.
- Modify `relay_report.go` (member host: enqueue on an applied `cleared` of a remote member's op) and its test.
- Modify `team_store.go` (`prev_refs` column via `ensureColumn`; the move) and its test.
- Modify `team_handler.go` (`matchMember` reads `prev_refs` for remote members).
- Modify `internal/module/peers/policy.go` and its test.
- Modify `internal/team/wire_team.go` and its test.

**Interfaces.**
- `type MemberMovedRequest struct{ TeamID, SpawnOp, OpID, OldSessionID, NewSessionID, NewRef string }`.
- `POST /api/peers/team/member-moved`, exact path in `HostRoutePolicy` → 200 `{updated: 0|1}` | 403 (admin, unverified, rebind) | 409 `not_your_member`.
- Column `team_members.prev_refs TEXT NOT NULL DEFAULT '[]'`: a JSON array, newest first, uncapped (spec §8.4, review (e)).

**Behaviour rules.**
1. **On the member host.** When `cleared` is **applied** for an op whose session was an active `remote_members` row (moved in the transaction, P4c-2), enqueue one `member_moved` to that row's lead host. Same outbox rules as P4c-4: forever, backoff ≤ 10 min, FIFO per host.
2. **On the lead host.**
   - Principal binding as P4c-2. **No `AllowTeam`:** it is the member host reporting on a member it was allowed to run.
   - The row with `spawn_op` must have `host_id == principal.HostID` and `team_id` equal to the request's, else 409 `not_your_member`.
   - If the row is still at `OldSessionID`: set `session_id = NewSessionID`, `ref = NewRef`, `address = <entry alias>/_<NewRef>`, and prepend the old ref to `prev_refs`, in one statement. If it is already at the new session: 200 `{updated:0}`, idempotent.
3. **`matchMember`** (P4-6 / P4c-4): for a remote member, try the current ref, then `prev_refs`. Local members keep using the lineage.
4. **The stale window.** Between the member host's `cleared` and delivery, the lead host may forward a relay or kill naming the old session.
   - The member host's relay and kill routes therefore follow `session_lineage` **forward** (predecessor → successor, repeatedly) when no active `remote_members` row matches the given session id, then apply the same binding.
   - `pdx msg send <member host>/_<old ref>` needs nothing new: the member host's rows carry `previous_refs` (P5a-1b), and Resolve runs over them from any host.

**Tests.**
- `TestMemberMoved_UpdatesTheRemoteRowAndKeepsTheOldRef`.
- `TestMemberMoved_IsIdempotent`.
- `TestMemberMoved_FromAnotherHostOrTeamIsRefused`.
- `TestOutbox_MemberMovedEnqueuedOnClearedOfARemoteMember` (member host).
- `TestTeamView_ShowsTheNewRefAfterACrossHostRelay`.
- `TestRelay_ByTheOldRefAfterACrossHostRelayTargetsTheNewSession`.
- `TestKill_ByTheOldRefAfterACrossHostRelayTargetsTheNewSession`.
- `TestPeerRelayAndKill_FollowTheLineageForwardInTheStaleWindow`.
- `TestHostRoutePolicy_MemberMovedExactPath`.

**Mutation gates.**
- Drop the `prev_refs` tier → the old-ref relay and kill tests red.
- No lineage-forward lookup → the stale-window test red.
- No enqueue on `cleared` → the outbox test red.

**Acceptance** (with P6-7a). a26 lead, mlab member:
1. `pdx relay _<ref>`. The member relays on mlab and a26's lead gets the done notice.
2. Within the outbox delay, `pdx team` on a26 shows the new ref.
3. `pdx relay _<old ref>` and `pdx kill _<old ref>` on a26 reach the new session.
4. `pdx msg send mlab/_<old ref>` from a26 reaches it.

**Size.** 450 lines, 9 files.

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
- Modify `internal/module/agent/module.go` (`AgentStatus` accessor, under `m.mu`; `currentStatus` is keyed by tmux session name, `handler.go:614`) and its test. **Skip this if P6-4 shipped branch B**, which added it already.
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
4. **Acceptance threshold** (codex finding 8). The daemon reads **`PDX_RELAY_THRESHOLD`**, the name spec §15 real acceptance 2 already uses for the prototype's test threshold, from its own environment once at `Init`. Accepted values are 1–100; unset or invalid means `team.RelayThresholdPct` (70).
   - It sets this notice's threshold only. The `hello` answer's `threshold` and the mod's own reading of the same variable (P5b, `register.js:582`) are unchanged.
   - The acceptance starts the daemon under test with it set.

**Tests.**
- `TestNotice70_CrossingWhileRunningWaitsForIdle`.
- `TestNotice70_OnceThenRearmsAfterARelay`.
- `TestNotice70_RearmsAfterADropBelow70`.
- `TestNotice70_NotWhileARelayIsOpen`.
- `TestNotice70_RemoteMemberAddressedToTheLeadHost`.
- `TestAgentStatus_ByTmuxSession` (unless P6-4 branch B has it).
- `TestNotice70_ThresholdFromPDXRelayThreshold`: `5` → fires at 6%; invalid → 70.

**Mutation gates.**
- Drop the idle check → the waits-for-idle test red.
- Drop disarming → duplicate notices red.
- Read another variable name → the env test red.

**Size.** 620 lines, 8 files.

## PR P7-2 — member auto-compact report

**Goal.** Spec §8.5 "derived: auto-compact": "Member: never intercepted … its mod lets compaction run and reports `compacted`. The lead hears: `[pdx team] <ref> 已自動壓縮（lead 未在 70% 時接力）`". The spec names no route, so this PR defines one (deviation 6).

**Files.**
- Create `internal/module/team/compacted.go` and its test.
- Modify `module.go` (route) and `cmd/pdx/relay.go` / `relay_subcmds.go` (`compacted`) with their tests.
- Modify `register.js`: the `session.compact` hook (`:727-746`).
- Modify `member.test.ts` and `SKILL.md` (notice texts match §8.5).

**Interfaces.**
- `POST /api/relay/compacted {session_id, trigger}` → 200 `{noticed bool}` | 400.
- CLI: `pdx relay compacted --session <sid> --trigger auto|manual [--config]`.

**Behaviour rules.**
1. **The daemon decides.** Only for an active member (any table) **and** `trigger == "auto"`: send the notice and **disarm** the 70% notice (`notice_armed = 0`). Otherwise `{noticed:false}`.
   - The compaction notice never re-arms (codex finding 5): the last reading can still be ≥ 70% until the next statusline refresh, so arming here would send the 70% notice on the very next tick.
   - Re-arming stays exactly P7-1's: after a relay (the `cleared` transaction), or when a reading below 70% arrives (spec §8.5).
2. **The mod** reports every compaction it does not intercept, from a timer (`later`), never awaited. Compaction never waits for it. It does not use `s.role` (coordinator decision 12): the first hello may answer before the member row exists (P4-3 risk).
3. **Skill.** The `[pdx team]` notice texts match §8.5 exactly. Today's skill paraphrases the 70% notice (`SKILL.md:21`).

**Tests.**
- Daemon: `TestCompacted_MemberAutoNotifiesOnce`, `…ManualDoesNot`, `…NonMemberDoesNot`.
- `TestCompacted_NoticeIsNotFollowedByA70NoticeOnTheNextTick`: a stale ≥ 70% idle reading, compaction notice, next tick → no 70% notice. Then a 30% reading → armed. Then a 75% idle reading → one 70% notice.
- CLI grammar.
- Mod: `an auto compaction in idle reports once from a timer`, `the compaction never waits for the report`.

**Mutation gates.**
- Notice on `manual` → red.
- Arm instead of disarm → the next-tick test red.

**Size.** 430 lines, 8 files.

---

## Open questions (all decided 2026-10-07)

The questions this plan raised, kept for the record, with the ruling each received. The binding text is in **Coordinator decisions** below, numbered the same.

1. **Order.** The local member relay could ship before P4b/P4c. **Decided: spec order.**
2. **The relay lock's allowed tools.** **Decided: spec §6.6 exactly.**
   - Only a `Write` to the op's handoff path is `allow`; everything else is `deny`.
   - So the write turn needs no other tool, the mod embeds the git facts (P6-3a).
   - `pdx hook` learns to print `allow` (P6-3b).
   - **2b. Who writes the flag. Decided: the mod** (spec), through `pdx relay lock|unlock`, at the write turn. The daemon removes it by op id as a safety net (P6-3b, P6-3c).
3. **`session.receive` while a turn runs.** **Decided: to be measured as M28**, by the coordinator, before P6-4 starts. Both claim-timeout branches are written (P6-4).
   - **M28 steps:**
     1. A throwaway tmux `claude --plugin-dir <probe>` whose `session.receive` logs `Date.now()`.
     2. Start a turn running `Bash sleep 45`.
     3. `pdx msg send` it at t+5 s.
     4. Compare the log time with the turn's end.
4. **Self ops stuck at runtime.** **Decided:** a self op stalled past `claimed` for 15 min → frames → `failed{handoff_incomplete}`.
5. **cwd scope on a member host.** **Decided:** under the member host's own `team.repo_roots`.
6. **Default `--cwd` of `pdx spawn`.** **Decided:** the CLI's working directory. A remote `--host` needs `--cwd` or `--repo`.
7. **Older-daemon detection.** **Decided:** 404, or a non-JSON 403, → `remote_unsupported`.
8. **Account identity across hosts.** **Decided:** a fingerprint, never the email.
9. **The plugin tree at spawn.** **Decided:** extract only when absent, never overwrite.
   - The launch in a never-opened directory **was measured (M27): no dialog**. P4-5 and the skill handle none.
10. **Persisted usage.** **Decided:** P4-6 (plan v2's binding decision).
11. **Persisting `hello`.** **Decided:** `mod_hello` in P6-2a.
12. **Who sends the member compaction report.** **Decided:** every role reports; the daemon decides.
13. **Nexen's host quota as a usage source.** **Decided:** no; the spec's statusline reading.
14. **A brief that fails after a successful spawn.** **Decided:** exit 1, stdout keeps the member.
15. **The 70% notice's acceptance threshold.** **Decided: `PDX_RELAY_THRESHOLD`** (spec §15). No new variable.
16. **`not_your_op`.** **Decided:** a new 409, exit 13.
17. **Every candidate below the weekly threshold.** **Decided:** the same ranking picks the most remaining.
18. **P4b before P4c.** **Decided:** a remote pick answers `remote_unsupported` until P4c-3.
19. **Team end reaching member hosts.** **Decided:** `POST /api/peers/team/end` through the outbox, retried forever (codex finding 7).

## Deviations from spec

1. **Team id = the approving request's id** (`teams.id = teams.request_id`). The spec's row keeps both fields. This lets `pdx lead request` print the team id with no extra call, and lets approval replays find the team.
2. **`POST /api/peers/team/relay` lands in P6-7a, not P4c.** Its handler needs P6's member-op creation.
3. **New peer routes the spec does not list:**
   - `POST /api/peers/team/end` (lead host → member host, coordinator decision 19);
   - `POST /api/peers/team/member-moved` (member host → lead host, P6-7b, codex finding 2).
   - `lead-moved` also carries `lead_ref`, because notices are addressed by ref.
   - The spec's member-row move "in the same transaction" holds across hosts only eventually, through the outbox.
4. **`remote_unsupported` also on a plain-text 403** (coordinator decision 7). This corrects the spec's "plain 404" for this codebase's `PeerAuth`.
5. **`pdx peers host allow-team`.** Singular, as the existing grammar is (`cmd/pdx/peers.go:70-80`). The spec wrote `hosts`.
6. **New interfaces the spec does not name:**
   - code `not_your_op`;
   - spawn failure reasons `session_create_failed`, `launch_failed`, `tmux_name_taken`, `abandoned`;
   - routes `POST /api/relay/ops/{id}/claim`, `POST /api/team/kill`, `GET /api/team`, `POST /api/relay/compacted`; `POST /api/relay/ops/{id}/seen` (M28 branch A only);
   - CLI `pdx relay lock|unlock` (the mod's way to write the flag), `pdx relay compacted`; `pdx relay seen` (branch A only).
7. **Persisted usage in P4-6** (plan v2 decision), not P7 (spec §12).
8. **The lead-handover notice in P6-1.** Plan v2's P5a section deferred it to P4, but it needs the notifier.
9. **Member-host cwd scope = its own `team.repo_roots`** (coordinator decision 5).
10. **The account is compared by fingerprint** (coordinator decision 8).
11. **Spawn POSTs are create-or-join** and answer the op's state within a wait (25 s local, 8 s across hosts). There is no separate GET.
12. **`relayRole` returns an error.** A store error is 500 (fail closed), not "none".
13. **The 15-minute limit counts from the last state change (a stall), not from the request, for both kinds** (coordinator decision 4). The claim deliberately waits for the member's running turn, so counting from the request would fail any relay asked for during a long turn.
14. **"A compatible version" (spec §8.2 step 1) = mod protocol ≥ 2.** P6-6 bumps `VERSION` from `'1'` to `'2'`. Protocol 1 has no control-message handler.
15. **The claim comes after the member's running turn has completed** (the coordinator's reading of spec §8.2 step 3, codex finding 1). The control message is consumed at once in every state. The claim timeout's basis (from the request with a `seen` sign, or from the member's next idle) is decided by M28 (P6-4).

---

## Coordinator decisions (plan v3, 2026-10-07, mlab/_7wcg1d purdex-f0)

Binding. Each item names where it landed.

**Codex review of plan v3** (one round; job output `scratchpad/planv3-review.txt` of session 27d44874; all 8 findings adopted):

1. **[critical] The control message is always consumed. While a turn runs the op is kept, and the claim follows that turn's `turn.complete`.** → P6-6:
   - `session.receive` returns `{consumed}` in every state;
   - `s.control` holds the op;
   - the claim runs from `turn.complete` or at once when idle;
   - the test drives `turn.start` → `session.receive` → `turn.complete` through the interface.

   **The claim timeout's basis is decided by M28.** Both branches are written in P6-4 "Claim timeout":
   - **Branch A** (fires mid-turn): from the request, stopped by the mod's `seen`; the `seen` route is in P6-4 and the CLI in P6-5.
   - **Branch B** (fires only when idle): from the member's next idle, with the agent-status accessor moved into P6-4.
2. **[critical] After a cross-host relay the lead host learns the member's new session and ref.** → **P6-7b** (pre-split from P6-7):
   - the member host's outbox sends `POST /api/peers/team/member-moved`;
   - the lead host updates the row and keeps `prev_refs`;
   - `matchMember` reads them;
   - the member host follows its lineage forward for a stale session id.
   - Tests cover `pdx team` showing the new ref, and `pdx relay` / `pdx kill` by the old ref reaching the new session.
3. **[important] The relay lock is spec §6.6 exactly.** Only the exact handoff `Write` gets `allow`; everything else gets `deny`; `PermissionRequest` gets `{}`. `pdx hook` prints `allow` (the same PR) → **P6-3b**.
   - To keep the handoff's quality, the mod runs `git status --short`, `git diff --stat` and `git log --oneline -10` itself and embeds them in the write prompt. The model only writes, with one `Write` of the whole file → **P6-3a**, which ships before the lock takes effect (P6-3c).
   - Open question 2 is decided.
4. **[important] The mod writes the flag** (spec):
   - `pdx relay lock|unlock <op> --session <sid>` use the flock helpers moved to `internal/team` (compare-and-remove by op id) → P6-3b.
   - The mod locks at the write turn's `turn.start`, after any existing turn completed, and unlocks before `/clear` and on every give-up → **P6-3c**.
   - The daemon compare-and-removes by op id at `cleared` and every terminal state as a safety net. The prune guard keeps flags of ops in `claimed|writing|written` → P6-3b.
   - Open question 2b and the former deviation 3 are decided, so this is no longer a deviation.
5. **[important] The compaction notice disarms the 70% notice.** Re-arm only after a relay or a reading below 70% → **P7-2**, with the test "no 70% notice on the next tick after the compaction notice".
6. **[important] P4c-3 is pre-split into P4c-3 (spawn forwarding) and P4c-4 (kill, outbox, remote team view)**, each ≤ 800 lines and ≤ 20 files. P6-7 is likewise pre-split into P6-7a and P6-7b, and P6-3 into P6-3a, b and c.
7. **[important] The outbox never fails for good.**
   - The backoff is capped at 10 minutes and retries are unbounded.
   - An entry ends only on delivery, when the row it describes no longer needs it, on a permanent refusal, or when the host is unpaired.
   - The member-host pull (`GET /api/peers/team/state`) is not added: P4c-4 rule 4 says why unbounded retry is enough, and the pull becomes a follow-up issue.
   - → **P4c-4**, with tests.
8. **[minor] The 70% notice's acceptance threshold is `PDX_RELAY_THRESHOLD`** (spec §15). There is no `PDX_TEAM_NOTICE_THRESHOLD` → **P7-1**.

**The open questions** (numbers as in "Open questions (all decided)"):
- **1 Order:** spec order, P4 → P4b → P4c → P6 → P7.
- **3:** to be measured as **M28** by the coordinator before P6-4 starts. The plan carries both branches (above, item 1).
- **4, 5, 6, 7, 8, 10, 11, 12, 13, 14, 16, 17, 18, 19:** the plan's defaults are adopted. For 19, the outbox retries forever (item 7).
- **9:** the default is adopted (extract only when absent, never overwrite). **Measured as M27** on 2026-10-07, CC 2.1.292, in tmux, in a directory never opened before (new under `/private/tmp`), with `claude --dangerously-skip-permissions --model haiku`:
  - the input box appeared within 2 s;
  - there was no trust dialog and no bypass dialog;
  - `~/.claude/sessions/<pid>.json` was written;
  - the status line showed Haiku 4.5, so `--model` took effect.

  So a spawn does not stop at a dialog. P4-5 needs no special handling and the skill says nothing about it.
- **15:** item 8 above.
- **Measurement numbers:** M25 = U20's `--model` / `--effort` (spec); **M26** = P8a-2's hours-long hold; **M27** = the launch in a never-opened directory; **M28** = whether `session.receive` fires while a turn runs.
