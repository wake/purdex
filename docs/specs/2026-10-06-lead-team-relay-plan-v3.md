# Lead / member / team and context relay — Implementation Plan v3 (P4, P4b, P4c, P6, P7)

> **Status (2026-10-07):** revised after one codex round (8 findings: 2 critical, 5 important, 1 minor; all applied) and the coordinator's rulings on every open question. The binding text is **"Coordinator decisions (plan v3)"** at the end. Written against origin/main `d57ca13b` (alpha.581; spec U20 merged in PR #1838); every `file:line` below was read on that commit. P8a-2 is still in flight on its own branch. Every PR here that touches the mod (P6-3a, P6-3c, P6-6, P7-2) rebases onto it.
> **Addendum (2026-10-08):** phases **P9b** and **P9a** (spec U21 / U22, §6.3, §8.8) were added after "Deviations from spec", before the coordinator decisions. They were written against origin/main `993c5c11` (alpha.588), and the PR table carries their rows. Spec §12 orders them **P4 → P9b → P9a → P4b**. Their open questions are not yet ruled.
> **Edited 2026-10-08 by the U23/U24 plan** (`docs/specs/2026-10-08-unattended-adopt-plan.md`, its decisions 12 and 15): P4b-4 needs U24 PL-1a and reuses its `remote_unsupported`; P6-1 needs U24 PL-1d1, which builds the in-process peers sender; P6-2b's member-relay insert confirms the membership in its own transaction, exclusive with U24's `pdx release`.
> **Source:** spec `docs/specs/2026-10-06-lead-team-relay-spec.md` (U1–U20, M1–M25), the "Coordinator decisions" and "Fix notes" of plan v1 and plan v2 (binding), and the line's memory `kickoff_lead_team_relay.md` (what shipped, review rulings, pitfalls).
> **Measurement numbers:** M25 is U20's launch flags (spec). **M26** is P8a-2's hours-long hold. **M27** (measured 2026-10-07, below) is the member launch in a never-opened directory. **M28** (still to measure) is whether `session.receive` fires while a turn runs.
> **Relation to v1 and v2:** v1 shipped P0–P3 (alpha.513–527), v2 shipped P2c, P5a, P5b and P8a-1a…1d (alpha.529–579). v3 schedules what is left of spec §12: **P4, P4b, P4c, P6, P7**. P8b is not scheduled and not written here. v3 uses a **compact format**: contracts, rules, tests and mutation gates, but no full code blocks. v2's code blocks went stale after review, so v3 does not repeat that mistake. The implementer writes the code test-first from these contracts.

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development (recommended) or superpowers:executing-plans. Each PR is TDD, one task per commit. Before you start a PR, re-verify its `file:line`s against main, because another line moves fast.

**Goal:** a lead spawns, lists and kills members, on whichever paired host the rule picks for the repo. The lead relays a member when it decides to; the member's mod writes, clears and seeds. A crash mid-relay is reconciled from frames. The lead is told when a member is idle past 70% or was auto-compacted.

---

## Global constraints

- **Size.** Each PR is ≤ 800 lines of diff **and** ≤ 20 files. The sections below are already split. A pure-move PR (P4-0, P6-0) is proved by per-declaration byte comparison, not by diffstat. Pure-move and bump PRs get no codex review (repo CLAUDE.md).
- **Order.** Merge in the order of the PR table (spec order, coordinator decision 1). P9b and P9a (U21 / U22, 2026-10-08) sit between P4 and P4b (spec §12).
  - P6-0 and P4c-1 have no upstream dependency and may land early.
  - P6-3a (mod only) lands after P9a-2, because it edits P9a's write template (spec §12), and before P6-3c.
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
8. **Only a decision made here moves the tabs (U22).** The switch runs only on `submitDecision`'s 200, including a queued click resent on reconnect. Never on a close made elsewhere, a 409, a timeout or a cancel. → P9b-1.
9. **Minimized is not modal (U22).** There is no focus trap, a new request never expands the dialog, and the grant edits survive minimize. → P9b-2.
10. **A relay never fails because of the prompts (U21).** The fixed head and tail are always the mod's own, and any failure gives the built-in copy. The daemon's defaults and the mod's copy are one source, pinned byte for byte. → P9a-1, P9a-2.

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
| **P9b-1** | SPA: after a decision made in this window succeeds (a queued click resent on reconnect included), activate the requester's tab, or open one on its tmux session through the session list's path | main | 420 / 9 | SPA |
| **P9b-2** | SPA: **縮小** → corner pill `● 待核准 N · m:ss`; no focus trap while minimized; a new request flashes the pill and never expands it; per window, not persisted | main | 520 / 10 | SPA |
| **P9a-1** | Host config `relay.prompt_write|fix|seed` and their validation; `GET /api/relay/prompts` (bodies, defaults, fixed parts, variables); `pdx relay prompts`; defaults in `internal/team`, the mod's copy generated and pinned | main | 720 / 14 | daemon + CLI |
| **P9a-2** | Mod: `pdx relay prompts` before each write, fix and seed (8 s); fixed head + body + fixed tail; variables; fallback to the built-in copy | P9a-1 | 380 / 3 | daemon + setup |
| **P9a-3** | SPA: Hosts › 接力 gets three editors with the fixed parts read-only, the variable list and 還原預設; drafts survive a tab switch | P9a-1 | 760 / 11 | SPA |
| **P4b-1** | Parse `rate_limits`; account fingerprint; per-host weekly reading; `GET /api/peers/team/usage` | P4-1 | 600 / 10 | daemon |
| **P4b-2** | Repo inventory (scan, `developable`, canonical key); `team.repo_roots|min_weekly_remaining|preferred_host`; `GET /api/team/repos`, `GET /api/peers/team/repos` | P4-4, P4b-1 | 680 / 10 | daemon |
| **P4b-3** | Peers `HostCaller` (call path X on host Y); `GET /api/team/hosts` | P4b-2 | 400 / 6 | daemon |
| **P4b-4** | Selection rule; `pdx spawn --repo/--host`; the choice line; `no_host_for_repo`; `remote_unsupported` (reused from U24 PL-1a) until P4c | P4b-3, P4-7, U24 PL-1a | 550 / 10 | daemon + CLI + setup |
| **P4c-1** | `PeerHost.AllowTeam`; `pdx peers host allow-team`; Hosts toggle | main | 380 / 12 | daemon + CLI + SPA |
| **P4c-2** | Member host side: `remote_members`; `POST /api/peers/team/spawn|kill|lead-moved|end`; policy; role of a remote member | P4b-4, P4c-1 | 760 / 12 | daemon |
| **P4c-3** | Lead host side, spawn: `forwarded_ops`; forwarding with op-id idempotency, classification and the restart grace; resume at boot; CLI codes | P4c-2 | 520 / 8 | daemon (both hosts) + CLI |
| **P4c-4** | Lead host side, the rest: kill forwarding; lead-moved / end outbox (retried forever, backoff ≤ 10 min); remote members in `pdx team` (`ContextInfo` model/effort); `matchMember` on remote hosts | P4c-3 | 620 / 10 | daemon (both hosts) |
| **P6-0** | Pure move: split `cmd/pdx/relay.go` (#1730) | main | 560 moved / 3 | none |
| **P6-1** | Daemon notifier: virtual peer, auto-reply; lead-handover notice to members (the in-process peers sender is U24 PL-1d1's) | P4-3, U24 PL-1d1 | 420 / 5 | daemon |
| **P6-2a** | Member-relay wire; `relay_ops.pid/pane_id`; persisted mod `hello`; `cleared` binding by op pid | P6-1 | 420 / 10 | daemon |
| **P6-2b** | `POST /api/team/relays` and the control message; `POST /api/relay/ops/{id}/claim` (no lock at claim); op long-poll | P6-2a, P4-6 | 650 / 6 | daemon |
| **P6-3a** | Mod: run the read-only git commands itself and embed them in the write prompt; write and fix prompts say "one `Write` of the whole file" | P9a-2 | 300 / 3 | daemon + setup |
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

Total ≈ 19 800 lines across 36 PRs. Spec order P4 → P9b → P9a → P4b → P4c → P6 → P7 is kept (coordinator decision 1; spec §12 added P9b and P9a on 2026-10-08).

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
  - **P4-5a**, tmux and session: `NewSessionTaggedContext` (`new-session ; set-option @opt v` in one invocation) and `PaneIdentity` (generation, session id, pane id, the user option and `pane_current_path` in one `display-message`); `session.CreateSessionTagged`. The create's `new-session` runs with `-P -F '#{session_id} #{pid}:#{start_time}'`. Measured on tmux 3.6a: that line is printed even when the `set-option` after it fails (exit 1), and nothing is printed when `new-session` itself fails. A tagged create that fails half way kills **that id** under **that generation**; it never looks the name up, so a session of that name another process made is never touched.
  - **P4-5b**, the runner's create and launch steps.
  - **P4-5c**, registration, the member row, boot resume and the plugin tree.
  - **P4-5d**, the route.
- **Test names.** `TestSpawn_LaunchLineHasPluginDirModelEffortAndRegisters` is split into `TestSpawn_LaunchLineHasPluginDirModelAndEffort` (P4-5b) and `TestSpawn_AMemberThatRegistersIsStoredAndTitled` (P4-5c). The POST's answer, with `lead_address`, is `TestSpawn_PostAnswersTheMemberAndTheLeadAddress` (P4-5d).
- **Rule 4, the launch: record first, then send.** `launched` (with `launched_at`) is persisted **before** the generation-guarded send. Winning that compare-and-set is the right to send, so the line is typed at most once whatever races or restarts. The price is a crash window: a daemon that dies between the record and the send leaves a launched op that was never sent, and the next boot times it out (killed, `member_start_timeout`).
- **Rule 3, the checks (H1).** The POST checks in this order: lead → cwd → (one write transaction) lead again + limit, then insert. `AcceptSpawnOp` takes the write lock with `UPDATE teams … WHERE id = ? AND lead_session_id = ? AND ended_at = 0` (no row → `not_lead`). It then counts the team's running ops (this one excepted) plus **every** `active` member row (≥ `max_members` → `team_full`) and inserts, all in that one transaction.
  - The count is team.db alone and conservative. A member whose session ended holds its place until P4-6's sweeper marks its row `gone`.
  - **Deploy dependency:** P4-5 and P4-6 ship in one deploy. Without P4-6's sweeper, a member that leaves keeps its place forever, and its team answers `team_full`.
  - "A live member … or whose relay is in flight" above is replaced by this count: a registry read before the transaction could undercount at its commit.
- **The cwd (H2), two more checks after the POST's.**
  - (a) Before the create, the runner resolves the op's cwd again and checks it against the team's roots (else `session_create_failed`, nothing created).
  - (b) Before any key, the pane's real `pane_current_path`, symlinks evaluated, must be under the roots. Otherwise the session is killed (recorded id and generation) and the op fails `launch_failed`, with nothing sent.
  - The cwd is still a path, not a handle: tmux takes only paths, and the lead runs as the same user. The roots are a guard rail, not a security boundary.
- **Ownership (H3).** The session is born with the session user option `@pdx_spawn_op` = the op's full UUID (the tmux name gives away 10 hex digits of it).
  - An accepted op that finds a session of its name adopts it only when one `PaneIdentity` answer shows that tag. Untagged (a stranger, or the name on a restarted server) or another op's tag → `tmux_name_taken`, left alone. "Present but no `tmux_id` recorded: adopt it" above is replaced by this.
  - Every later step re-reads the pane's identity: generation, session, pane and tag must all be the op's.
- **Registration (H4).** A verified frame on the pane is the member only after one `PaneIdentity` answer confirms that the pane is still the op's (generation, session id, tag). Frames carry no tmux identity of their own. A pane that answers but is not the op's → `failed{abandoned}`; an unreadable one is looked at again.
- **Registration deadline (R1, ruled after the critic).** Every poll judges `now ≥ launched_at + 20 s` first, and times the op out even when the member has shown up by then, because nothing records when it registered.
  - **The timeout decides before it kills (re-review).** `FailSpawnOpAtStep` moves the op from `launched` to `failed{member_start_timeout}` only while it is still running at `launched`. That is the same row and step the registration's compare-and-set moves from, so exactly one of the two wins. Only the timeout's winner kills, generation-guarded.
  - A daemon that dies between that compare-and-set and the kill leaves the session of a failed op behind.
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
- Code `ErrNoHostForRepo = "no_host_for_repo"`, 409 and exit 13. **`ErrRemoteUnsupported = "remote_unsupported"` already exists** (U24 plan `2026-10-08-unattended-adopt-plan.md`, PL-1a, edited in by that plan's commit) and the CLI already maps it to exit 13 (its PL-1e): reuse both, do not define them again.
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
- Create `cmd/pdx/relay_subcmds.go`: `runRelayHello`, `runRelayBegin`, `runRelaySelf`, `runRelayReport`, `runRelayOp`, and `runRelayPrompts` (P9a-1 adds it before this PR).

**Contract, proof, tests.** As P4-0: per-declaration byte comparison, `go test ./cmd/pdx/ -race`, no codex.

**Size.** About 280 lines moved (≈ 560 diff lines), 3 files.

## PR P6-1 — the daemon's notifier; the lead-handover notice

**Goal.** Spec §8.5 "Daemon notices come from the daemon's own virtual peer … A reply to it gets one line back"; §7.4 (c) "notices … go through `POST /api/peers/send` from the member host's virtual peer"; §8.4 "When a lead relays, the daemon also tells each active member". Plan v2's P5a section deferred this notice to P4. It lands here, with the notifier it needs (deviation 8).

> **Edited 2026-10-08 by the U23/U24 plan** (`docs/specs/2026-10-08-unattended-adopt-plan.md`, its decision 15): the in-process peers sender (`SenderKey`, `Sender`, `SendError`, `internal/module/peers/sender.go` and its two tests) is built by that plan's **PL-1d1**, which this PR needs. This PR adds only the virtual peer, the auto-reply and the handover notice; `notify.go` is shared with PL-1d1's notice outbox (extend it, do not create it).

**Files.**
- Modify `internal/module/team/notify.go` (PL-1d1's) and create `notice.go`; modify `notify_test.go`.
- Modify `internal/module/team/module.go` (`Start` / `Stop` for the virtual peer) and `relay_report.go` (handover after `cleared` applied).

**Interfaces.**
- Peers (provided by PL-1d1, unchanged here): `SenderKey = "peers.sender"`, `Sender`, `SendError`; the implementation runs `m.handleSend` (`internal/module/peers/send.go:197`) in process under the admin principal, keeping one send path: resolution, local and remote delivery, audit.
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
- (`TestSender_RunsHandleSendAsAdmin` and `TestSender_RefusalIsASendError` moved to U24 PL-1d1.)
- `TestNotifier_StartsAndStopsWithTheModule`: temp sock and registry dirs; files gone after `Stop`.
- `TestNotify_UsesTheVirtualPeersInbox`.
- `TestAutoReply_OncePerSenderPerWindow_NeverToPdxDaemon`.
- `TestHandover_SentOnceOnAppliedClearedNotOnResend`.

**Mutation gates.**
- Notify on `ReportNoop` → the once test red.
- Drop the `pdx-daemon` guard → the loop test red.

**Size.** ≈ 420 lines, 5 files (the sender moved to U24 PL-1d1).

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
   - **Exclusive with a release** (added 2026-10-08 by the U23/U24 plan, its "Contracts for later PRs" 1 and decision 12): the insert runs in **one write transaction that first takes the write lock and confirms the target's member row is still `active` in a live team**; no row → 409 `not_your_member`, nothing inserted. U24 PL-1d2's `ReleaseMember` is a conditional `UPDATE` that refuses while any relay op of the session is not terminal, so exactly one of a concurrent create and release commits: a release first → this create answers `not_your_member`; the op first → the release answers `relay_open`.
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
- `TestRelayCreate_RacesReleaseOneWins`: a seam runs a `pdx release` of the member between the create's checks and its insert → the create answers 409 `not_your_member` and no op exists; the reverse order → the release answers 409 `relay_open`. Mutation gate: confirm the membership outside the insert's transaction → red.
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

> **After P9a (spec §12, 2026-10-08).** The write and fix texts are no longer literals in `register.js`; P9a-2 composes them from the bodies and fixed parts of `internal/team/relay_prompts.go`. So this PR does the following:
> - It edits the default write **body** there: it drops the `自己跑 git…` line and adds the `機器提供的 git 狀態（照抄進 §3）：` block with **`{{git}}`**.
> - It appends `git` to `RelayPromptVariables` and to the mod's `fill` values.
> - It puts `- 只用 Write 工具一次寫入整個接力檔；…` in the write **fixed tail**. The lock enforces that rule, so a custom body must not be able to drop it. The Write-not-Edit wording goes in the fix tail.
> - It regenerates `hooks/prompts.js` (`go test ./cmd/pdx/plugin/ -run TestPromptsJS -update`).
> - It re-reads the `register.js` line numbers below against P9a-2.
> - **Files therefore add** `internal/team/relay_prompts.go`, `relay_prompts_test.go` and `cmd/pdx/plugin/purdex/hooks/prompts.js`. **Its gate adds** `go test ./internal/team/ ./cmd/pdx/plugin/ -count=1`: `TestPromptsJS_IsGeneratedFromTheDaemonsDefaults` green after the regeneration, `TestRelayPromptDefaults_ValidAndPublicVariablesOnly` green with `git` in the list, and a new `TestRelayPromptFixedParts_WriteTailHoldsTheOneWriteRule`. Mutation gate: change the Go default without `-update` → the golden test red (plan review).
>
> A host whose stored write body still asks the model to run git keeps that text. P9a-3's page shows it as 已自訂.

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
- `writePrompt` facts, one line each. **After P9a**, the facts list is part of the write **fixed tail** (`internal/team/relay_prompts.go`, generated into `hooks/prompts.js`). These lines go there through one placeholder of the mod's own, like `{{missing}}`, never through a body variable, and `prompts.js` is regenerated:
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

# Phase P9b — approval dialog: back to the requester, and minimize (spec §6.3, §2 "How this spec reads U22")

> **Addendum (2026-10-08).** U21 and U22 (spec PR #1877) add P9b and P9a. Spec §12 puts them after P4 and before P4b: **P4 → P9b → P9a → P4b**. Neither needs anything from P4, so their branches can start at once; they merge in table order. Every `file:line` in P9b and P9a was read on origin/main `993c5c11` (alpha.588).

**Facts this phase rests on** (read in the code, not inferred):
- **`origin.tmux` is the CC registry's `tmux` field, verbatim.** It reads `<session name>:@<win>.%<pane>` (`internal/team/wire.go:63`, filled at `internal/peers/registry.go:439`; a live sample is `nexen33:@95.%95`). The session part is the tmux session **name**, cut at the first `:` (`registry.go:64-72`). tmux refuses `:` and `.` in session names, so the cut is exact.
- **A tab binds a tmux session by session code, not by name** (`types/tab.ts:123`). The code is an encoding of tmux's `$N` (`lib/pane-tree.ts:88-91`). The name → code map is the host's live session list, `useSessionStore.sessions[hostId]` (`stores/useSessionStore.ts:8`), whose rows carry `code`, `name` and `tmux_instance` (`lib/host-api.ts:7-20`).
- **"Open a session from the host's session list"** is Hosts › Sessions `handleOpen` (`components/hosts/SessionsSection.tsx:79-93`):
  - skip a host hidden in this workbench (`isRefShownNow`);
  - `useTabStore.openSingletonTab({ kind: 'tmux-session', hostId, sessionCode, mode: 'terminal', cachedName, tmuxInstance })`, which always adds a tab for this kind (`lib/pane-utils.ts:25`);
  - `useWorkspaceStore.insertTab(tabId)`, which picks the active workspace, else the first, else Unsorted (`features/workspace/store.ts:187-195`);
  - `setActiveTab(tabId)`.
- **"Activate a tab that already shows the session"** is the notification click (`hooks/useNotificationDispatcher.ts:466-482`):
  - `findTabAndPaneBySessionCode` finds any live pane, a primary one preferred (`lib/pane-tree.ts:136-150`);
  - `usePaneFocusStore.requestFocus(tabId, paneId)` and `setActiveTab`;
  - the tab's workspace becomes active (`setActiveWorkspace` + `setWorkspaceActiveTab`).

  Its new-tab branch also activates the new tab's workspace (`:501-503`).
- **"This window" is the renderer that made the decision.** Tabs and workspaces are one persisted world shared by a device's windows (`stores/useTabStore.ts:1027-1047`, `features/workspace/store.ts:346-360`, synced through `syncManager`). `useApprovalStore`, though, is neither persisted nor synced (`stores/useApprovalStore.ts:3, 72`): each renderer has its own, with its own queue. The deciding renderer runs the switch through the same store calls as the two paths above. P9b does not change how other windows follow a tab activation.
- **One decision path.** `submitDecision` (`lib/team/approval-decide.ts:52-116`) serves both the dialog's click (`components/ApprovalDialogHost.tsx:147`) and the reconnect resend of a queued click (`lib/team/approval-ws.ts:91-96`, `fromQueue`). Its 200 path is `:69-81`. A close made elsewhere arrives either through the WS `closed` branch (`approval-ws.ts:105`) or through the 409 path (`approval-decide.ts:102-107`).

## PR P9b-1 — switch to the requester's tab after a decision made here

**Goal.** U22 (a) and spec §15 "Approval dialog (U22)", first three clauses. After **核准** or **拒絕** succeeds in this window, the window activates the tab showing the requester's tmux session; when none shows it, it opens one, the way the session list does.
- "Succeeds in this window" means the 200 of `submitDecision`, which includes a queued click resent on reconnect.
- Nothing switches for a close made elsewhere, a timeout, a cancel, or an empty `origin.tmux`.

**Files.**
- Create `spa/src/lib/open-session-tab.ts` and its test. The two paths above become functions there, so the existing callers and the approval share one path:
  - `openSessionTab(hostId, session): string | null` is `SessionsSection.handleOpen`'s body, moved. It keeps the hidden-host gate and answers `null` when gated.
  - `activateTabPane(tabId, paneId)` is the notification click's existing-tab block, moved (`useNotificationDispatcher.ts:467-481`).
- Modify `spa/src/components/hosts/SessionsSection.tsx:79-93` and `spa/src/hooks/useNotificationDispatcher.ts:466-482` to call them. Behaviour does not change; their existing tests are the gate.
- Create `spa/src/lib/team/approval-goto.ts` and `approval-goto.test.ts`.
- Modify `spa/src/lib/team/approval-decide.ts:69-81` and create `approval-decide.goto.test.ts`.
- Modify `spa/src/lib/team/approval-ws.test.ts` (the resend and closed-elsewhere cases).

**Interfaces.**
- `parseOriginTmux(tmux: string): { session: string; pane: string } | null`. `session` is the text before the first `:`; `pane` is the `%…` after the last `.`. It answers `null` for `''` and for text with no `:` (the `TmuxSessionName` rule).
- `gotoRequester(hostId: string, approval: Approval): 'activated' | 'opened' | 'none'`, in this order:
  1. `parseOriginTmux(approval.origin.tmux)` is `null` → `'none'`.
  2. `isRefShownNow(hostId)` is false → `'none'` (open question 1).
  3. `session = useSessionStore.getState().sessions[hostId]?.find((s) => s.name === parsed.session)`. Not found → `'none'` (open question 2).
  4. `findTabAndPaneBySessionCode(tabs, hostId, session.code)` hits → `activateTabPane(hit.tabId, hit.paneId)` → `'activated'`.
  5. Otherwise `openSessionTab(hostId, session)`, then activate the new tab's workspace as the notification's new-tab branch does → `'opened'`.
  - The pane id is not used. A tab attaches to the whole tmux session, and moving tmux to `@win` is not part of U22.
- `submitDecision`, on the 200 path: after `applyClosed(hostId, closed)`, call `gotoRequester(hostId, closed)` inside a `try`, whose `catch` only logs (`console.warn`). A failed navigation must never turn a decision that was sent into `'failed'`. No other path calls it.

**Behaviour rules.**
1. **Only this window's own successful decide switches.** These never call `gotoRequester`:
   - the 409 path (`already_decided`, `member_relay_is_leads`);
   - 404 and `host_removed`;
   - a network failure, queued or failed;
   - the WS `closed` branch.
2. **A queued click switches when it lands.** The resend runs in the renderer that queued it, because the queue is per renderer. It may land long after the click (spec U22 (a); see the risk below).
3. **Behind the next dialog.** When other requests are open, the next one shows at once (`selectCurrent`), and the switch happens behind it.
4. **Not tab-hosted.** Nothing here mounts inside a tab, and no state is added.

**Tests.**
- `approval-goto.test.ts`:
  - `parses <session>:@<win>.%<pane>; '' and 'a' give null`.
  - `a live pane of that host and session is activated, its pane focused and its workspace shown; no tab is added`.
  - `no live pane: one tmux-session tab with the list's code, name and tmux_instance is added, inserted and activated`.
  - `the same session name on another host is not a match`.
  - `an ended pane on that code is not a match: a new tab opens`.
  - `empty origin.tmux, a name not in the host's list, a host hidden in this workbench: nothing changes`.
- `approval-decide.goto.test.ts`:
  - `approve 200 and deny 200 each switch once, with the closed approval` — table-driven over both kinds, `lead` and `self_relay` (spec U22 (c); plan review).
  - `409 already_decided, 409 member_relay_is_leads, 404, host_removed, network (queued) and network (failed) switch nothing`.
  - `a gotoRequester that throws still returns 'closed'`.
- `approval-ws.test.ts`:
  - `a queued decision resent on the reconnect snapshot switches on its 200`.
  - `a closed event from another client switches nothing`.
- `open-session-tab.test.ts`: the two helpers, including `opening the same session twice adds two tabs` (`tmux-session` is never a singleton, `lib/pane-utils.ts:23-26`; the helper must not change that). The existing `SessionsSection` and `useNotificationDispatcher*.test.ts` tests stay green unchanged.

**Mutation gates.**
- Call `gotoRequester` from the WS `closed` branch → `a closed event from another client switches nothing` red.
- Call it on the 409 path → the 409 case red.
- Drop `hostId` from the session lookup → the other-host case red.
- Always open a new tab → `… no tab is added` red.

**Size.** ≈ 420 lines, 9 files.

**Deploy.** SPA.

**Risks.**
- **A queued click switches tabs when it lands on reconnect,** possibly while the person is typing elsewhere. That is the spec's rule (U22 (a)). The keyboard follows the switched pane (`requestFocus`), as on a notification click.
- **A session renamed between the request and the decision** is not found by its old name, so nothing switches. That is not worth a second key.

## PR P9b-2 — minimize to a corner pill

**Goal.** U22 (b) and spec §15 "Approval dialog (U22)", last three clauses:
- The dialog gets a **縮小** button. Minimized, it becomes a pill in this window's bottom-right corner: `● 待核准 N · m:ss`. N is the number of open requests across hosts, and the countdown is that of the nearest deadline.
- Clicking the pill restores the dialog.
- While minimized there is no focus trap, and keys and clicks reach the tabs. A new request updates N and flashes the pill once, but never expands it. Deadlines and notifications run as before.
- Minimized is per window and not persisted.

**Not tab-hosted** (the repo CLAUDE.md checklist):
- `ApprovalDialogHost`, and the pill it renders, are app-level overlays mounted once in `App.tsx:283`, outside `TabContent`. A tab switch never unmounts them, so the unmount/remount test does not apply.
- `minimized` lives in `useApprovalStore`: module scope, one store per renderer, not persisted. A reload therefore shows the dialog again, as the spec says.
- The grant edits (max members, roots, 「這個 session 不再詢問」) stay in `OpenApprovalDialog`'s own state, because the component **stays mounted, hidden,** while minimized. A test pins that minimize → restore keeps them.
- The only remount is a change of the current request (its `key`, `ApprovalDialogHost.tsx:46`), which starts fresh, as today.

**Files.**
- Modify `spa/src/stores/useApprovalStore.ts` and `useApprovalStore.test.ts`.
- Create `spa/src/components/ApprovalPill.tsx` and `ApprovalPill.test.tsx`.
- Modify `spa/src/components/ApprovalDialogHost.tsx` and create `ApprovalDialogHost.minimize.test.tsx`.
- Modify `spa/src/hooks/useNotificationDispatcher.ts:523-528` (open question 4) and `useNotificationDispatcher.approval.test.ts`.
- Modify `spa/src/locales/en.json` and `zh-TW.json`.

**Interfaces.**
- **Store:**
  - `minimized: boolean`, initially `false`, with `setMinimized(v: boolean)`. `true` takes effect only while `entries` is non-empty.
  - `applySnapshot`, `applyClosed` and `reset` set `minimized: false` whenever they leave `entries` empty (open question 3). `applyOpened` never touches it.
  - `selectNearestDeadline(s): number | null`: the smallest `approval.deadline_at` in `entries`.
- **`ApprovalDialogHost`:**
  - It renders `<OpenApprovalDialog … minimized={minimized} />`, plus `<ApprovalPill />` when minimized.
  - The overlay (`:157-166`) gets `hidden={minimized}`.
  - The Escape swallow (`:80-88`), the Tab trap (`:91-110`) and **the focus guard** (P9b-1b: a document `focusin` listener that pulls focus back into the panel, added after P9b-1's review) are not registered while minimized: all three effects depend on `minimized`. Test: `minimized: a focus moved to a terminal stays there` (mutation: the guard ignores `minimized` → red).
  - The focus effect (`:74`) records `document.activeElement` before it focuses the panel. On minimize, it gives focus back to that element if it `isConnected`, else blurs. On restore, it focuses the panel again.
  - The header (`:173-176`) gets a `type="button"` with `data-testid="approval-minimize"`, the Phosphor `ArrowsInSimple` icon and `t('approval.dialog.minimize')`. It is never disabled: minimizing during a send is harmless.
- **`ApprovalPill`:**
  - It is a `button` with `data-testid="approval-pill"` and `aria-label={t('approval.pill.restore')}`.
  - It shows a Phosphor `Circle weight="fill"` dot in the status-warning colour, then `t('approval.pill.label', { count, countdown })`. Here `countdown = formatCountdown(nearest - now)` (`lib/team/approval-format.ts:52-55`), and a 1 s interval drives `now`, as the dialog's does (`:69-72`).
  - It sits at `fixed bottom-8 right-3 z-50`, above the 24 px status bar (`StatusBar.tsx:212`, `h-6`) and clear of the bottom-centre toast (`GlobalUndoToast.tsx:25`), with `WebkitAppRegion: 'no-drag'`.
  - A click calls `setMinimized(false)`.
  - **Flash.** A `useRef<Set<string>>` holds the request keys the pill has shown. When `entries` gains a key that is not in it, the `data-flash` counter goes up by one and the pill runs one 600 ms background flash through `element.animate(…)` (Web Animations). The flash is skipped when `animate` is absent (jsdom). A close never flashes.
- **Locale keys**, in both files:
  - `approval.dialog.minimize`: `縮小` / `Minimize`;
  - `approval.pill.label`: `待核准 {{count}} · {{countdown}}` / `{{count}} pending · {{countdown}}`;
  - `approval.pill.restore`: `還原核准對話框` / `Restore the approval dialog`.

**Behaviour rules.**
1. **Never auto-expands.** Only a click on the pill expands it, or a click on the approval notification (open question 4). An `opened` event does not, and neither does a snapshot.
2. **The notification still fires,** unchanged (`approval-ws.ts:101`). A deadline still runs out into a denial: the daemon owns it.
3. **Per window.** Minimizing in one window leaves the other windows as they are: each renderer has its own store and WS branch.
4. **Nothing open → no pill.** When the last request closes, here or elsewhere, the pill goes and `minimized` resets, so the next request opens the dialog (open question 3).

**Tests.**
- Store:
  - `minimized resets when applyClosed, applySnapshot or reset leave no entry`.
  - `a snapshot that still holds entries keeps minimized` (the reconnect case; plan review).
  - `setMinimized(true) with no entry stays false`.
  - `selectNearestDeadline across hosts`.
- `ApprovalDialogHost.minimize.test.tsx`:
  - `縮小 hides the dialog and shows the pill with N across hosts and the nearest m:ss` — for a `lead` and for a `self_relay` current request (spec U22 (c)); the new-request and restore cases below also run once with each kind as the newly opened one (plan review).
  - `minimized: Tab and Escape keydowns are not default-prevented, and focus returns to the element focused before the dialog`.
  - `a new request while minimized updates N, raises data-flash by one, and the dialog stays hidden`.
  - `a request closed while minimized updates N without a flash`.
  - `clicking the pill restores the dialog and focuses its panel`.
  - `grant edits and the 不再詢問 tick survive minimize and restore`.
  - `the countdown ticks while minimized`.
  - `the last close removes the pill, and the next request opens the dialog`.
  - `a fresh store (reload) shows the dialog`.
- `useNotificationDispatcher.approval.test.ts`: `an open-approval click restores a minimized dialog and still focuses the window`.
- `locale-completeness.test.ts`, unchanged, covers the three new keys.

**Mutation gates.**
- `applyOpened` clears `minimized` → `… the dialog stays hidden` red.
- `applySnapshot` always sets `minimized: false` → `a snapshot that still holds entries keeps minimized` red.
- The Tab trap kept while minimized → the focus test red.
- The dialog unmounted while minimized → the edits test red.

**Size.** ≈ 520 lines, 10 files.

**Deploy.** SPA.

**Risks.** None at runtime. In jsdom, hidden means the `hidden` attribute, so the tests assert the attribute, not layout.

---

# Phase P9a — relay prompts in the daemon (spec §8.8, §2 "How this spec reads U21")

**Where each line of today's prompts goes.** `cmd/pdx/plugin/purdex/hooks/register.js:278-323` builds the three prompts. Each line becomes part of a **fixed head**, an editable **body** or a **fixed tail**. The mod composes:

```
prompt = fill(head, all) + fill(body, public) + (tail === '' ? '' : '\n' + fill(tail, all))
```

- `body` has its trailing newlines removed.
- `public` is the five variables of U21 (d): `path`, `old_ref`, `old_session`, `context`, `whoami`.
- `all` is `public` plus the mod's own `op`, `nonce` and `missing`. Only the fixed parts use these three.

| `register.js` | Text | Part |
|---|---|---|
| 278, 282 | `[pdx-relay op=<op> n=<nonce>]` and one space | write **head** |
| 282 (rest) | `這個 session 的 context 已達接力門檻，使用者已核准接力（之後會 /clear）。` | write body |
| 283 | `請先停下手邊工作，用你完整的工具撰寫接力檔：` + path | write body, `{{path}}` |
| 284–287 | blank; `要求：`; the `git` line; the 自成一體 line | write body |
| 288 | `- 寫完後只回一行「HANDOFF-WRITTEN」，不要繼續原本的工作。` | write **tail**: the reply rule |
| 289–299 | blank; `格式（每一段都要有，沒有內容就寫「無」）：`; `# HANDOFF`; the eight `## N.` lines, parentheses included (open question 6) | write **tail**: the headings `checkHandoff` reads (`:53`, `:530-534`) |
| 300–305 | blank; `機器提供的事實（請照抄進對應段落）：`; the four fact lines | write **tail**: `{{old_session}}`, `{{old_ref}}`, `{{context}}`, `{{whoami}}` |
| 310 | tag and one space | fix **head** |
| 310 | `接力檔 ` + path + ` 不完整` | fix body: `接力檔 {{path}} 不完整。` |
| 310 | `，缺少段落：` + missing + `。請補齊後只回「HANDOFF-WRITTEN」。` | fix **tail**, on its own line: `缺少段落：{{missing}}。請補齊後只回「HANDOFF-WRITTEN」。` (deviation 3) |
| 315 | `↪ 接手自 ` + old ref | seed **head**, line 1 (spec §8.2 step 7 makes it the first line; deviation 2) |
| 316 | `[pdx-relay seed op=<op> n=<nonce>]` and one space | seed **head**, line 2 |
| 316 (rest)–321 | `你是接手的新對話：…` through `回覆的第一行請寫「↪ 接手自 <old ref>」。` | seed body: `{{path}}`, `{{old_ref}}` |
| — | — | seed tail = `''` |

**What the defaults contain.** The default bodies use `{{path}}` (all three) and `{{old_ref}}` (seed) and nothing else. The other three variables already appear in the fixed facts list; they are offered for a body that wants them. With the default bodies:
- the composed **write** and **seed** prompts equal today's text byte for byte;
- the **fix** prompt differs only by the line break before `缺少段落`.

P9a-2 pins all three. The default bodies, copied from today's lines (the P9a-2 fixture is the authority):

```text
write.body:
這個 session 的 context 已達接力門檻，使用者已核准接力（之後會 /clear）。
請先停下手邊工作，用你完整的工具撰寫接力檔：{{path}}

要求：
- 自己跑 `git status`、`git diff --stat`、`git log --oneline -10` 取得檔案狀態，不要憑記憶寫。
- 接力檔必須自成一體：讀它的是一個完全沒有這段對話記憶的新對話。

fix.body:
接力檔 {{path}} 不完整。

seed.body:
你是接手的新對話：前一段對話 context 已滿並已清空。
請先讀接力檔 {{path}}，然後：
1. 用三行複述：目標、下一步第一個動作、目前有哪些檔案異動。
2. 跑 `git status` 確認與接力檔一致，不一致就指出來。
3. 接著從「下一步」繼續原本的工作。
回覆的第一行請寫「↪ 接手自 {{old_ref}}」。
```

The fixed parts are `write.head = fix.head = "[pdx-relay op={{op}} n={{nonce}}] "` and `seed.head = "↪ 接手自 {{old_ref}}\n[pdx-relay seed op={{op}} n={{nonce}}] "`. The write tail is lines 288–305 with the four facts as variables, the fix tail is the line in the table, and the seed tail is `''`.

**One source for the defaults.** Spec §8.8 says "the mod keeps an identical built-in copy … and a test pins that the two copies are equal". Three ways were considered:
- **A Go test that parses the literals in `register.js`.** Rejected: the texts are built by string concatenation, so the test would need a JS parser or a regex over code.
- **One JSON or text file, read by `go:embed` and by the mod at run time.** Rejected for now. Whether the engine accepts `import … with { type: 'json' }` in a hooks module is unmeasured. Reading the file through `$.fs.read($.plugin.root + …)` adds an I/O step that can fail exactly when the fallback is needed.
- **Chosen: Go is the source, and the mod's copy is generated from it.**
  - The Go source is `internal/team/relay_prompts.go`.
  - The mod's copy, `cmd/pdx/plugin/purdex/hooks/prompts.js`, is **generated**: plain ES module text that `register.js` imports the way it imports `ask.js` (`:36`).
  - A golden test in `cmd/pdx/plugin` (a package that may import `internal/team`) byte-compares the committed file with what the generator renders, and `-update` rewrites the file.
  - Any drift, on either side, turns the test red.

## PR P9a-1 — daemon and CLI: the three bodies in host config, `GET /api/relay/prompts`, `pdx relay prompts`, the generated mod copy

**Goal.** Spec §8.8 bullets 1–3; U21 (a), (c), (d), (e); §15 "Relay prompts (U21)": the defaults, the 400 cases, and 還原預設 clearing the value. The mod does not read the bodies yet (P9a-2).

**Files.**
- Create `internal/team/relay_prompts.go` and `relay_prompts_test.go`.
- Modify `internal/module/hostconfig/relay.go` and `relay_test.go`, and `module.go:34-37` (register the reader).
- Create `internal/module/team/relay_prompts_handler.go` and its test.
- Modify `internal/module/team/module.go`:
  - the field;
  - the `Init` lookup beside the switches' (`:230-238`, or the `lookup` helper of `spawn_runner.go:51-58`);
  - the route beside `:279-284`.

  Test helpers that register a fake switches reader register the same fake under the new key.
- Modify `cmd/pdx/relay.go` (`relayUsage` `:27-32`, the dispatcher `:96-112`, `runRelayPrompts` after `runRelayOp` `:418-440`) and `relay_test.go`.
- Modify `cmd/pdx/http_chain_test.go:747-778`: the admin-only table also covers `PUT /api/hostconfig/relay` with a prompt.
- Create `cmd/pdx/plugin/purdex/hooks/prompts.js` (generated) and `cmd/pdx/plugin/prompts_test.go`.
- Modify `cmd/pdx/plugin/embed_test.go:12`: the layout list gains `hooks/prompts.js`.

**Interfaces.**
- **`internal/team/relay_prompts.go`:**
  - `const RelayPromptMaxBytes = 16 << 10`.
  - `var RelayPromptVariables = []string{"path", "old_ref", "old_session", "context", "whoami"}`; P6-3a appends `"git"`.
  - `type RelayPromptBodies struct{ Write, Fix, Seed string }`, JSON `write`, `fix`, `seed`.
  - `type RelayPromptFixed struct{ Head, Tail string }`, JSON `head`, `tail`.
  - `type RelayPromptSkeleton struct{ Write, Fix, Seed RelayPromptFixed }`.
  - `var DefaultRelayPromptBodies RelayPromptBodies` and `var RelayPromptFixedParts RelayPromptSkeleton`, with the texts above. They are built with `strings.Join` of lines, not raw literals, because the bodies contain backticks.
  - `type RelayPrompts struct{ Write, Fix, Seed string; Defaults RelayPromptBodies; Fixed RelayPromptSkeleton; Variables []string }`, JSON `write`, `fix`, `seed`, `defaults`, `fixed`, `variables` (`fixed` and `variables` are deviation 1).
  - `func ValidateRelayPromptBody(s string) error` refuses text that:
    - is longer than `RelayPromptMaxBytes` bytes;
    - is not `utf8.ValidString`;
    - holds any rune that `unicode.IsControl` accepts, other than `\n` and `\t` (so `\r`, NUL, DEL and C1 are refused);
    - contains `[pdx-relay`.

    Its error text becomes the 400 detail.
- **Host config, `relay.go`:**
  - `RelaySwitches` gains `PromptWrite`, `PromptFix`, `PromptSeed string`, JSON `prompt_write`, `prompt_fix`, `prompt_seed`, all **`omitempty`**. A row without bodies therefore serializes as today, and so does the never-written default `relaySwitchesJSON` (`:29`).
  - `normalizeRelay` (`:39-71`) takes the three keys:
    - each must be a JSON string (`null` and other types are errors, as for the switches);
    - **the field's raw bytes** (its `json.RawMessage` in `fields`) must pass `utf8.Valid` **before** it is decoded. `encoding/json` turns an invalid byte into U+FFFD while decoding, so a check on the decoded string can never see one (plan review);
    - whitespace-only text is stored as `""` (= the default; spec U21 (a) and §8.8 say so since the plan review);
    - any other text must pass `team.ValidateRelayPromptBody`;
    - an absent key stays `""`.

    Unknown keys are still refused, and the message lists the five known ones.
  - `const RelayPromptsKey = "hostconfig.relay-prompts"` and `type RelayPromptReader interface{ RelayPrompts() (team.RelayPromptBodies, error) }`, which returns the stored values (`""` = unset). `Init` registers it.
  - **`RelaySwitches()` (`:77-86`) decodes only the two switches.** A stored body that fails validation must not make the switches an error, because that error turns the self-relay `begin` into a 503. `RelayPrompts()` validates the bodies and reports the error instead.
- **Team module:**
  - `GET /api/relay/prompts` → 200 `team.RelayPrompts`. Each effective body is the stored one, or the default when that is `""`.
  - A reader error → 500 `storage_error`, the module's code for a store failure.
  - The route sits behind TokenAuth with the other `/api/relay/*` routes. There is no `HostRoutePolicy` entry, so a peer token gets 401.
- **CLI:** `pdx relay prompts [--config <path>]`.
  - It sends the GET through `relayClient(…, daemonclient.DefaultAttemptTimeout, …)` and prints the JSON on one line through `relayPrintJSON`, exit 0.
  - Errors go through `relayReportErr`: 20 when the daemon is unreachable through the grace, 21 on a plain 404 (a daemon from before P9a-1), and 1 otherwise. An extra argument is exit 2.
- **Generated `hooks/prompts.js`:**
  - First line: `// GENERATED from internal/team/relay_prompts.go by: go test ./cmd/pdx/plugin/ -run TestPromptsJS -update — do not edit.`
  - Then one line each: `export const DEFAULT_BODIES = <json>`, `export const FIXED = <json>`, `export const VARIABLES = <json>`.
  - The JSON comes from an encoder with `SetEscapeHTML(false)`. JSON is a valid JS expression, and Go escapes U+2028 and U+2029.

**Behaviour rules.**
1. **Edits apply at once.** The GET reads the store on every call, and nothing is cached: the next relay sees an edit (spec §8.8; U21 (b)).
2. **還原預設** is a PUT whose field is `""`. The row then holds no `prompt_*` key (`omitempty`).
3. **Per host** (U21 (e)). The route answers for its own daemon only. A cross-host member reads its member host's bodies, because its mod asks the daemon it reports to.
4. **One revision for the row.** The switches and the bodies share the `relay` row and its CAS revision, and P9a-3 serializes both through one queue key.
   - An older SPA's toggle keeps the stored bodies. It PUTs `{...current, [field]: !current[field]}` of the raw stored object (`components/hosts/RelaySection.tsx:32`), which `stores/useHostConfigStore.ts:186` keeps whole.

**Tests.**
- `relay_prompts_test.go`:
  - `TestValidateRelayPromptBody`: 16 384 bytes pass and 16 385 fail; invalid UTF-8 fails; `\r`, `\x00`, `\x7f` and U+0085 fail; `\n` and `\t` pass; `[pdx-relay` and `[pdx-relay:control]` fail wherever they stand.
  - `TestRelayPromptDefaults_ValidAndPublicVariablesOnly`.
  - `TestRelayPromptFixedParts_CarryTheTagReplyRuleHeadingsAndFacts`:
    - the write and fix heads start with `[pdx-relay op={{op}} n={{nonce}}]`;
    - the seed head is exactly its two lines;
    - the write tail holds `寫完後只回一行「HANDOFF-WRITTEN」`, `# HANDOFF`, `## 1.`…`## 8.` and the four fact variables;
    - the fix tail holds `{{missing}}` and `HANDOFF-WRITTEN`.
  - `TestRelayPrompts_WireNames`.
- Host config `relay_test.go`:
  - `TestNormalizeRelay_PromptFields`: a string is stored; `null` or a number is an error; whitespace becomes `""`; over-limit text, the tag and control characters give a `ValidationError` (400); an unknown key is an error.
  - `TestNormalizeRelay_InvalidUTF8InTheRawBodyIs400`: a PUT body built as bytes, with `\xff` inside a `prompt_write` string, is a `ValidationError`, and nothing is stored (not a U+FFFD body). Mutation gate: check only the decoded string → red.
  - `TestGetHostConfig_RelayDefaultUnchanged`: the never-written row equals `relaySwitchesJSON` byte for byte.
  - `TestRelayPrompts_DefaultsAndStored`.
  - `TestRelaySwitches_IgnoreABadStoredPrompt`: a row written straight into the DB with a tagged body still reads its switches, while the prompts reader errors.
  - `TestPutRelay_RestoreDefaultClearsTheKey`.
- `relay_prompts_handler_test.go`:
  - `TestRelayPrompts_UnsetAnswersTheDefaults`.
  - `TestRelayPrompts_StoredBodyIsEffectiveDefaultsStillListed`.
  - `TestRelayPrompts_EditServedOnTheNextGet`, with the real hostconfig module.
  - `TestRelayPrompts_ReaderErrorIs500`.
- `cmd/pdx/relay_test.go`: `TestRelayPrompts_PrintsOneJSONLine`, `TestRelayPrompts_Plain404Is21`, `TestRelayPrompts_UnreachableIs20`, `TestRelayPrompts_ExtraArgIs2`.
- `http_chain_test.go`: the relay PUT with a body is 401 for a peer token and 200 for the admin token.
- `prompts_test.go`: `TestPromptsJS_IsGeneratedFromTheDaemonsDefaults`, the spec's "byte for byte".

**Mutation gates.**
- Drop the `[pdx-relay` check → the validation test red.
- Edit one character of a Go default without `-update` → the golden test red.
- Let `RelaySwitches()` validate the bodies → `…IgnoreABadStoredPrompt` red.
- Remove `omitempty` → `…RelayDefaultUnchanged` red.

**Size.** ≈ 720 lines, 14 files. If the PR runs past 800, the CLI (`relay.go`, `relay_test.go`) moves to a P9a-1b.

**Deploy.** daemon and CLI, which are one binary (the route). `prompts.js` is inert until P9a-2 imports it, so no `pdx setup` is needed.

**Risks.**
- `cmd/pdx/relay.go` gains about 25 lines before P6-0 splits it. P6-0 moves `runRelayPrompts` with the other subcommands, as noted there.
- `GET /api/hostconfig` now carries up to 48 KiB of bodies on every SPA load. That is acceptable.
- **Rollback (review A-2, accepted for alpha; shipped in #1890/#1891).** A daemon from before P9a-1 refuses `prompt_*` as unknown keys, and its `RelaySwitches()` decodes the whole row, so after any custom body is stored a rolled-back daemon answers self-relay `begin` with 503. **Before rolling back below P9a-1, press 還原預設 on all three bodies** (or PUT them as `""`): `omitempty` then leaves no `prompt_*` on the row. The CHANGELOG entry of the bump says so.

## PR P9a-2 — the mod reads the bodies at use; fixed head and tail; built-in fallback

**Goal.** Spec §8.8 bullet 4; U21 (b)–(d); §15 "Relay prompts (U21)":
- an edited body is used from the next relay without `pdx setup`;
- a failure, a timeout or a 404 gives the built-in defaults, and the relay proceeds;
- the tag, the reply rule and the eight headings are present whatever the body says.

The P5b-2 machine tag and nonce rules are unchanged.

**Files.**
- Modify `cmd/pdx/plugin/purdex/hooks/register.js`:
  - import `./prompts.js` beside `ask.js` (`:36`);
  - `tag` / `writePrompt` / `fixPrompt` / `seedPrompt` (`:278-323`) become `compose`;
  - change the write step (`settle`, `:495-506`), the fix step (`:556-564`) and the seed step (`:646-658`).
- Modify `relay.test.ts`.
- Touch `SKILL.md` only if review finds a stale line. Its "Self relay" paragraph (`:31`) names no prompt text.

**Interfaces.**
- `const PROMPTS_TIMEOUT_MS = 8_000`.
- `async function bodyFor($, kind)` → `string`:
  - It calls `pdx($, ['relay', 'prompts'], PROMPTS_TIMEOUT_MS)`, which carries `--config` (`:145-150`).
  - It uses `JSON.parse(stdout)[kind]` only when the exit is 0 and the value is a non-empty string of at most 16 384 UTF-8 bytes.
  - Anything else gives `DEFAULT_BODIES[kind]` and one log line: exit 20, 21 or 1, a rejected run (`run` reads it as 20, `:137-143`), junk JSON, a missing field, a wrong type.
  - `$.process.run` keeps up to 4 MiB of stdout (2.1.293 d.ts, `ProcessRunResult`), far above the at most 60 KB answer.
- `function compose(kind, body, p, extra)`: the formula above, over `FIXED[kind]`.
  - `fill(text, vars)` is one pass of `text.replace(/\{\{([a-z_]+)\}\}/g, (m, k) => Object.hasOwn(vars, k) ? vars[k] : m)`. So a value is never expanded again, and an unknown `{{…}}` stays as typed (U21 (d)).
  - The values are `path = p.path`, `old_ref = p.oldRef`, `old_session = p.oldSession`, `context = p.before` and `whoami = p.who`. `whoami` is captured at the write step, so it is the identity before `/clear`.
  - The mod's own values are `op = p.op.id` and `nonce = p.nonce`, and for fix `missing = missing.join('、') || '(內容過短)'`.
- **Each step:** read the body, re-check the step's guard (the await may span a `/clear` or a compaction), then `arm` and `submit`.
  - write: `const [who, body] = await Promise.all([whoami($), bodyFor($, 'write')])`. The step costs no more than today's `whoami` bound (10 s).
  - fix: `bodyFor($, 'fix')` inside its `later`.
  - seed: `bodyFor($, 'seed')` inside its `later`, then `if (s.pending !== p || s.state !== 'seeding') return`.

**Where the call runs, and HookBudget.** P5b-3's fact is that a hook's 10 s budget pauses only while that hook's own `next(e)` or `$` call is in flight. That budget does not apply here:
- **No hook is involved.** All three calls run inside `later()`, which is `$.clock.after` (`:132-135`). By the 2.1.293 d.ts, `fn` "stays in the plugin's environment and runs once the dispatch resolves", outside every hook. So no hook awaits the call and no hook budget applies. This is the file's own rule (`:22-31`), which `whoami` and every report already follow.
- **No person's prompt is held by it.** The hold (`prompt.submit`, `:706-725`) covers only `awaiting` and `beginning`, and these steps run after approval.
- **The cost is latency before the mod's own next prompt:**
  - write: none beyond `whoami`;
  - fix: at most 8 s after the write turn;
  - seed: at most 8 s after `/clear`. The new conversation is idle meanwhile, and a prompt typed in that window runs before the seed. Today that window is `STEP_MS` (50 ms) plus a spawn.
- **With the daemon up**, a call is one process spawn plus one local GET: **M29**, measured in this PR's acceptance and expected to be under 200 ms on mlab. With the daemon down, the call hits its 8 s bound and then the fallback.

**Behaviour rules.**
1. **The fixed parts are always the mod's own.** The mod never uses the daemon's `fixed`, so a daemon of another version cannot change the machine format the mod reads back.
2. **Read afresh for every prompt.** The next relay, and even the next fix round, uses an edit made a moment ago (open question 9).
3. **A relay never fails because of this** (U21 (b)). Every failure path ends in the built-in body, and `compose` cannot throw on strings.
4. **Existing tests.** Their default fake answers every pdx call with the hello JSON (`relay.test.ts:182`), which has no string `write`, `fix` or `seed`.
   - They therefore take the fallback, and their write and seed text assertions stand unchanged.
   - Assertions that list every pdx argv in order gain the `relay prompts` calls.
   - The fix-text assertion follows deviation 3.

**Tests** (`relay.test.ts`):
- `with the defaults, the write and seed prompts equal the pre-P9a text byte for byte` (a literal fixture of today's output for `OP`).
- `the fix prompt is head, body, then 缺少段落 and the reply rule on the next line`.
- `an edited write body lands between the fixed head and tail; the tag, the reply rule, # HANDOFF, ## 1.–## 8. and the facts are there for a one-word body`.
- `an edited fix body keeps the tag head and the 缺少段落 + HANDOFF-WRITTEN tail; an edited seed body keeps both fixed head lines (↪ 接手自 <old ref>, then the seed tag with the nonce)` — each with a one-word body and with a 16 384-byte body (plan review).
- `pdx relay prompts runs before the write, before each fix and before the seed, each with --config and timeoutMs 8000`.
- `exit 20, 21 and 1, a rejected run, junk stdout and a non-string field each give the built-in body; the relay reports writing, written, cleared and done`.
- `variables are filled once: {{path}} {{old_ref}} {{old_session}} {{context}} {{whoami}}; {{foo}} and {{nonce}} in a body stay as typed; a path holding {{path}} is not expanded again`.
- `trailing newlines of a body give one newline before the tail`.
- `a second relay uses the body the daemon answers then`.
- `a pdx relay prompts that never answers holds no hook: turn.complete and classic.SessionStart return before it`.
- `a pdx relay prompts that runs into its 8 s timeoutMs gives the built-in body, and the relay reports writing, written, cleared and done` — the fake settles the run the way the engine does at `timeoutMs` (read the 2.1.293 d.ts `ProcessRunResult` / rejection for the exact shape), driven by the kit's clock, once at each of the three steps (plan review).
- `every REQUIRED heading and # HANDOFF are in FIXED.write.tail`, a guard between `:53` and the generated file.

**Mutation gates.**
- Skip the tail when a body is set → the one-word-body test red.
- Drop the seed head's first line, or the fix tail, for a custom body → the fix/seed custom-body test red.
- Use the daemon's body without the timeout fallback (await the run with no bound) → the timeout test red.
- Fall back only on a non-zero exit → the junk-stdout case red.
- Fill recursively → `… not expanded again` red.
- Read once per relay → `… before each fix and before the seed` red.

**Size.** ≈ 380 lines, 2–3 files.

**Deploy.** daemon (swap `bin/pdx`, which carries the embedded mod) and **setup** (re-run `pdx setup --agent cc` under the binding asks). It needs P9a-1 deployed; without it, every relay silently uses the built-in bodies.

**Acceptance.** Follow `docs/testing/self-relay-acceptance.md` (P5b-3) on a throwaway session with `PDX_RELAY_THRESHOLD`:
1. With no body set, the write prompt in the transcript equals today's.
2. After a write body is PUT through the API, the next relay's write prompt shows it between the fixed parts, with no `pdx setup` in between.
3. Measure M29: the wall time of `pdx relay prompts` from the mod, and the time from `classic.SessionStart{clear}` to the seed's `turn.start`.

The fallback paths are proved by the tests above, not live.

**Risks.**
- **A newer variable.** A body written for a newer daemon may use a variable this mod does not know, such as `{{git}}` before P6-3a. It stays as typed, so the model sees the braces. P9a-3 lists only the variables the daemon reports.
- **Seed latency:** see above (M29).

## PR P9a-3 — SPA: the three editors on Hosts › 接力

**Goal.** Spec §8.8 bullet 5 and U21: three editors (write, fix, seed), with the fixed parts shown read-only around each, the variable list, and **還原預設** for each one. Also spec §15 "還原預設 clears the stored value".

**Tab-hosted: yes.** The Hosts page is a tab, and `hosts` is not a light kind (`lib/pane-weight.ts:13, 18-24`). So switching tabs unmounts `RelaySection` under the default `keepAliveCount: 0` (`stores/useUISettingsStore.ts:215`; with a larger count the tab is kept hidden in `TabContent`'s alive pool until evicted), and so does switching the Hosts sub-page.
- Unsaved text therefore lives in `spa/src/lib/relay-prompt-draft-memory.ts`, keyed `${hostId}:${kind}`. It is memory only, the pattern of `lib/nex/worker-draft-memory.ts`, and is forgotten after a successful save or 還原預設.
- A regression test mounts the real `TabContent` (example: `components/execution/ExecutionView.tab-switch.test.tsx`).

**Files.**
- Create `spa/src/lib/relay-prompts-api.ts` and its test.
- Create `spa/src/lib/relay-prompt-draft-memory.ts`.
- Create `spa/src/components/hosts/RelayPromptEditor.tsx` and its test.
- Modify `spa/src/components/hosts/RelaySection.tsx` and `RelaySection.test.tsx`; create `RelaySection.tab-switch.test.tsx`.
- Modify `spa/src/lib/host-config-api.ts:16-17`: `RelaySwitches` gains optional `prompt_write?`, `prompt_fix?` and `prompt_seed?`.
- Modify `spa/src/locales/en.json` and `zh-TW.json`.

**Interfaces.**
- `fetchRelayPrompts(hostId, signal?): Promise<RelayPrompts | 'unsupported'>`.
  - It sends a GET to `/api/relay/prompts` through `hostFetch` (`lib/host-api.ts:194`).
  - A plain 404 → `'unsupported'`.
  - The body is checked whole, else it throws: every body is a string, every `fixed.*.head` and `fixed.*.tail` is a string, and `variables` is an array of strings.
- `RelayPromptEditor({ hostId, kind, fixed, defaultBody, stored, variables, locked, onSave, onRestore })` shows:
  - the head and the tail as read-only `<pre>` blocks, captioned `t('hosts.relay.prompts.fixed')`, with `{{op}}`, `{{nonce}}` and `{{missing}}` explained as 由 mod 填入;
  - a monospace `textarea` whose value is `draft ?? (stored || defaultBody)`;
  - the variables, each with its i18n description;
  - a byte counter `n / 16384`;
  - **儲存** and **還原預設** buttons.

  Its client checks mirror `ValidateRelayPromptBody`:
  - `\r\n` is turned into `\n` first;
  - `[pdx-relay`, any other control character, or more than 16 384 bytes shows an inline error and disables 儲存.

  The daemon stays the authority: its 400 detail is shown.
- `RelaySection`:
  - It fetches the prompts once per mount. `'unsupported'` shows one line (`hosts.relay.prompts.unsupported`) and leaves the toggles unchanged.
  - **Save** is a task on `hostConfigQueueKey(hostId, 'relay')`, the switches' queue (`lib/host-config-queue.ts:18-31`). When the task runs, it reads the store's `relay` and calls `saveRelay(hostId, { ...current, [field]: value })`. The switches and the other two bodies are kept.
  - A value equal to `defaultBody` is sent as `""` (open question 12).
  - **還原預設** sends `""`, and is disabled while the stored value is already `""`.
  - A 409 shows `host_config.conflict` and keeps the draft.
- About 22 locale keys under `hosts.relay.prompts.*`:
  - the title and description; `write`, `fix` and `seed` with their descriptions;
  - `fixed`, `variables`, and one key per variable;
  - `save` and `restore_default`;
  - the `custom` / `default` badges;
  - the errors `too_long`, `has_tag` and `control_chars`;
  - `unsupported` and `load_failed`.

**Tests.**
- `relay-prompts-api.test.ts`: the shape check; a plain 404 → `'unsupported'`.
- `RelayPromptEditor.test.tsx`:
  - the head and tail are not editable;
  - the box starts with the stored text, else the default;
  - the counter is shown;
  - the tag, a control character or 16 385 bytes disables 儲存;
  - CRLF is sent as LF;
  - saving the default text sends `""`;
  - 還原預設 sends `""` and is disabled at the default.
- `RelaySection.test.tsx`:
  - three editors when the route exists;
  - `'unsupported'` → the line, and the toggles still work;
  - a save PUTs the whole `relay` object, both switches included, through the relay queue;
  - a toggle after a save keeps the bodies;
  - a 409 keeps the draft and shows the conflict;
  - a daemon 400's detail is shown.
- `RelaySection.tab-switch.test.tsx`: a typed draft survives a switch to another tab and back, and a Hosts sub-page switch; it is gone after a save. The test sets `keepAliveCount: 0` itself and asserts that `RelaySection` really unmounted in between (an unmount spy or a missing node), so it does not pass by the tab being kept alive (plan review).
- `locale-completeness.test.ts`.

**Mutation gates.**
- PUT only `{ prompt_write }` → `… both switches included` red.
- Keep the draft in `useState` → the tab-switch test red.
- Send the default text verbatim → `… sends ""` red.

**Size.** ≈ 760 lines, 11 files. If it is over, the tab-switch test and the locales split into P9a-3b.

**Deploy.** SPA.

**Risks.** Two clients may edit the same body. The CAS makes the second save a 409, and its draft stays, so nothing is lost silently.

## Open questions (P9 addendum, all decided 2026-10-08)

Kept for the record. Each had a recommended default, and the sections above are written to it. The ruling is in **Coordinator decisions (P9 addendum)** at the end of this file.

1. **A hidden host (P9b-1).** Should a decision for a host hidden in this workbench switch nothing (the Hosts › Sessions gate, `SessionsSection.tsx:80`), or land on the Hosts page (the notification's `landOnHostsPageIfHidden`, `lib/shown-hosts.ts:175-180`)? **Default: nothing.** A decision is not a request to navigate, and the person hid that host here.
2. **The requester's session is not in this window's list (P9b-1)**, because it ended or the list has not loaded. **Default: nothing, with no toast.** The spec says the dialog "just closes" when there is nothing to switch to.
3. **The last request closes while minimized (P9b-2).** **Default: `minimized` resets**, so the next request opens the dialog. The alternative, staying minimized until a click, lets a request an hour later arrive as a pill only. U22 calls the minimize 暫時.
4. **A click on the approval notification while minimized (P9b-2).** **Default: the click restores the dialog.** It is the person's own action, not an automatic expansion, and spec §6.3 says the click "focuses the window, where the dialog already is". The alternative is to focus the window only, where the pill is.
5. **The seed's first line (P9a).** **Default: `↪ 接手自 <old ref>` is part of the fixed seed head** (deviation 2): §8.2 step 7 requires it as the first line, and U16 keeps the phrase. U21 (c) names only the tag for the seed.
6. **The heading lines (P9a).** **Default: the whole lines are fixed**, `## N. 標題（說明）` with the parentheses, not only the `## N.` the check reads. U21 says "8 個必要段落標題由程式固定", and guidance can go in the body.
7. **The fix prompt's line break (P9a).** One composition rule (head + body + `\n` + tail) puts `缺少段落：…` on its own line. **Default: accept** (deviation 3). The alternative, a per-kind joiner, keeps today's bytes but glues a fixed `，缺少段落` to whatever the body ends with.
8. **The single source (P9a).** **Default: Go plus a generated `prompts.js`**, for the reasons above. A runtime JSON import by the mod becomes possible only if a probe shows that the engine supports it, and v1 does not need it.
9. **A call per prompt or per relay (P9a-2).** The spec says before each prompt. **Default: per prompt.** The cost is the seed gap after `/clear`: at most 8 s when the daemon hangs, M29 when it is up. The alternative takes one snapshot at the write step for the whole relay, which removes the call between `/clear` and the seed and keeps one version per relay. Only the call sites differ.
10. **`fixed` and `variables` in the GET (P9a-1).** **Default: add them** (deviation 1). The settings page then shows the fixed parts and the variable list from the one source, not from a third copy in the SPA.
11. **One `relay` row or a separate one (P9a-1).** **Default: one row**, as §8.8 says ("read with the relay switches"). The shared revision is handled by the existing queue, and an older SPA's toggle keeps the bodies (verified, `RelaySection.tsx:32`). A separate `relay_prompts` row would decouple the revisions, at the cost of one more route and one more GET field.
12. **Saving text equal to the default (P9a-3).** **Default: store `""`**, so a later change of the default reaches this host. Typing the default then means the same as 還原預設.
13. **M29 (P9a-2 acceptance).** Measure the wall time of `pdx relay prompts` from the mod on mlab, and the time from `classic.SessionStart{clear}` to the seed's `turn.start`, with and without the call. If it is well above 200 ms, revisit question 9.

## Deviations from spec (P9 addendum)

1. **`GET /api/relay/prompts` also answers `fixed` and `variables`.** `fixed` is `{write|fix|seed: {head, tail}}` with the mod's placeholders. Spec §8.8 lists `{write, fix, seed, defaults}`, so the two fields are additive. The mod ignores `fixed`: its own copy is the authority.
2. **The seed's fixed head is two lines,** `↪ 接手自 {{old_ref}}` and then the tag. *Resolved in the spec (plan review, 2026-10-08):* U21 (c) now says the tag opens the write and fix prompts and the seed's second line, both seed lines fixed, which is the shipped order (`register.js:315-316`); the mod finds its turns by the nonce anywhere in the text (`:609`). So this is no longer a deviation.
3. **The fix prompt's composed default has a line break before `缺少段落：`**, where today it has `，`.

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

## Coordinator decisions (P9 addendum, 2026-10-08, purdex-f0)

Binding. **All 13 open questions of the P9 addendum take the plan's recommended default**, so the sections above stand as written:

1. A decision for a host hidden in this workbench switches nothing (P9b-1).
2. The requester's session is not in this window's list → nothing, and no toast (P9b-1).
3. The last request closing while minimized resets `minimized` (P9b-2).
4. A click on the approval notification while minimized restores the dialog (P9b-2).
5. `↪ 接手自 <old ref>` is part of the seed's fixed head (P9a, deviation 2).
6. The eight heading lines are fixed as whole lines, parentheses included (P9a).
7. The fix prompt's line break before `缺少段落：` is accepted (P9a, deviation 3).
8. The single source of the defaults is Go (`internal/team/relay_prompts.go`), with the mod's `hooks/prompts.js` generated from it and pinned by a golden test (P9a-1).
9. One `pdx relay prompts` call per prompt. Revisit only if M29 is well above 200 ms (P9a-2).
10. `GET /api/relay/prompts` also answers `fixed` and `variables` (P9a-1, deviation 1).
11. The bodies live in the one `relay` host-config row, beside the switches (P9a-1).
12. Saving text equal to the default stores `""` (P9a-3).
13. M29 is measured in P9a-2's acceptance and recorded in spec §3.2.

**Codex review of the P9 addendum** (one round, plan + spec; job output `p9-plan-review.txt` in the scratchpad of session 8fff4c6b; 2 critical / 7 important / 2 minor):
- **Critical 1, the seed tag is not the first line** — rebutted with evidence and fixed in the spec's wording: the shipped seed puts `↪ 接手自 <old ref>` first and the tag second (`register.js:315-316`), §8.2 step 7 requires that first line, and the mod finds its turns by the nonce anywhere (`:609`). U21 (c) now says so; deviation 2 is resolved.
- **Critical 2, "only this window"** — rebutted with evidence: `activeTabId` and the workspaces are persisted and synced to every window of the device (`useTabStore.ts:1035-1043`, `lib/storage/sync.ts`), so every tab activation, a click included, already moves them all. U22 (a) now says "this window" names who acts; P9b changes nothing there.
- **Adopted:** raw-bytes UTF-8 check before decoding (P9a-1); the timeout fallback test and the fix/seed custom-body tests (P9a-2); the tab-switch test pins `keepAliveCount: 0` and a real unmount (P9a-3); both kinds in the P9b tests; a non-empty snapshot keeps `minimized` (P9b-2); P6-3a's files and golden gate; the session list still opens a second tab (P9b-1).
- **Minor, whitespace-only body** — the spec now says "unset, empty or whitespace only" means the default (U21 (a), §8.8), which is what the plan does.

---

## 2026-10-09 現況對齊（P6）

Draft by η for the coordinator's ruling (task c933b0-26). Written against main at `d5c5d2cc`+ (alpha.638 line); no code was changed. Everything below is read from the tree, with file:line. It follows the shape of the U24 plan's "現況對齊" (`2026-10-08-unattended-adopt-plan.md`).

### 1. Status of what is left of P6

P4 (teams, spawn, kill, `pdx team`), P9a (relay prompts in the daemon) and P9b (approval dialog) are merged; P4b and P4c are **not built** (no `--repo/--host`, no `AllowTeam`, no `forwarded_ops`/`remote_members` anywhere in `internal/` or `cmd/`). Of P6:

| PR | Status | Evidence on main |
|---|---|---|
| **P6-0** split `cmd/pdx/relay.go` | ❌ not done | one file, 468 lines (`cmd/pdx/relay.go`); `runRelayWait` `:242`, `runRelayPrompts` `:450` (P9a-1 added it, as the plan expected) |
| **P6-1** notifier (virtual peer, auto-reply, handover notice) | ◐ the **sender** is done (PL-1d1: `internal/module/peers/sender.go`, `internal/module/team/notify.go` — a membership-notice outbox, not a generic `notify`); the virtual peer, auto-reply and the handover notice are not | `ccuds.StartVirtualPeer` is called only by the peer-proxy helper (`internal/peers/proxyhelper/helper.go:114`); no `notice.go` |
| **P6-2a** member-relay wire, op pid/pane, persisted hello, `cleared` binding | ❌ not done | `RelayOp` has no `pid`/`pane_id` (`internal/team/wire_relay.go:65-82`); `modSeen` is in memory only (`internal/module/team/module.go:106`, filled by `handleRelayHello`, capped 512 at `relay_handler.go:30`); `RelayKindMember` exists only as a constant (`wire_relay.go:10`) |
| **P6-2b** `POST /api/team/relays`, control message, `claim`, op long-poll | ❌ not done | `relay_handler.go:185` refuses a non-self begin with "a member relay is POST /api/team/relays"; `relay_report.go:59` still says "claimed is not reportable: … a member op by pdx relay claim" |
| **P6-3a** mod embeds git facts | ❌ not done | the default write body still says `自己跑 git status…` (`internal/team/relay_prompts.go:77`; the comment at `:28` says "P6-3a appends git") |
| **P6-3b** relay-lock machinery | ❌ not done | `cmd/pdx/hooklock.go` still holds the flock helpers; `handleHookDecide` (`internal/module/team/hooks.go:23`) knows only the lead lock (`openLeadAndRemoveStaleFlag` `:87`); no `pdx relay lock|unlock` |
| **P6-3c** mod raises/lowers the flag | ❌ not done | `register.js` (1047 lines) has no lock call; the only `onWriteTurnDone` is `:796` |
| **P6-4** timeouts, frame reconciliation, notices | ❌ not done | `reconcileRelays` (`relay_report.go:285`) only handles self ops `awaiting_approval` and `cleared`'s title move; **#1735 is still open**; no claim/stall timeout in `sweeper.go` |
| **P6-5** CLI `pdx relay <ref>` / `claim` / `seen` | ❌ not done | `runRelayCmd` (`relay.go:92`) dispatches only hello/begin/wait/self/report/op/prompts |
| **P6-6** mod: control consumed, claim, protocol 2 | ❌ not done | `VERSION = '1'` (`register.js:51`); no `session.receive` hook anywhere in `hooks/*.js` or `hooks.json` |
| **P6-7a / P6-7b** cross-host member relay | ❌ not done, **blocked** | needs P4c-4, which is not built |
| **P6-8** SPA restart-confirm line | ❌ not done | the daemon half exists (`relays_active` in `/api/team/inflight`, `handler.go:315-330`; SPA type in `approval-api.ts`), no line in `RestartDaemonButton` |
| **P7-1 / P7-2** 70% idle notice, auto-compact report | ❌ not done | no `AgentStatus` accessor in `internal/module/agent` (only `currentStatus`, `handler.go`/`frame_ops.go:1408`) |

**M28 is measured** (spec `2026-10-06-lead-team-relay-spec.md:277`, 2026-10-08): `session.receive` fires within ~1 s while a turn runs. So P6-4 takes **branch A** (claim timeout counted from the request, `pdx relay seen`, `RelayOp.SeenAt`); branch B and its `internal/module/agent` accessor are not needed for P6 — which also keeps P6 out of 88's agent-module ground (ed-72's PU-3a, #2180, just changed `hookemitter.go`/`module.go` there).

### 2. What other PRs already did, or changed under P6

1. **The in-process sender exists (PL-1d1).** P6-1's `notify(ctx, to, text)` need not build a sender. PL-1d1 chose to send **from the lead's inbox** (`InboxOf(lead)` as `OriginInbox`, not a daemon virtual peer; text prefix `[pdx team]`; adopted by the coordinator for the adopt/release notices). The same works for P6's two messages: the **control message** `[pdx-relay:control] op=<id>` is a peer message from the lead (the requester) and arrives with `origin.kind === 'peer'` (M1), which is all the mod checks; the **handover notice** goes out from the *new* lead session's inbox. → **Recommend dropping the virtual peer and the auto-reply from P6-1** (the "a reply to it gets one line back" rule has no sender to reply to), leaving only the handover notice. Ruling needed (§5, Q1).
2. **Release/relay exclusion is half done.** `ReleaseMember` already refuses while any relay op of the session is non-terminal (`internal/module/team/release_store.go:21`, the `NOT EXISTS … relay_ops … NOT IN ('done','failed','cancelled')` guard) — so a `requested` member op blocks a release, and `kill`'s own guard (`team_handler.go:220`) only looks at `claimed|writing|written` (a `requested` op does not block a kill; P6-4's `failed{member_gone}` covers it). The other half — P6-2b's create confirming `active` membership **in its insert transaction** — is still to write (the plan's "Exclusive with a release" rule stands as written).
3. **Adopted members are valid targets for free.** The member row of an adopted session carries `PID`, `ProcStart`, `PaneID` and `Ref` (`adopt_handler.go` `adoptMemberRow`), so P6-2a's "pid/pane from the member row" works for spawned and adopted rows alike; `moveTeamRoles` in `cleared` keys by session id, and `PendingNotices` addresses by the row's ref which lineage forwards after a relay (PL-1b2 note). `matchMember` already prefers the unique active row when a session was released and re-adopted (PL-1d2).
4. **The winner point exists.** Every approved close ends in `announceClosed → afterApproved` (`unattended.go`); a `member_relay` approval's side effect (sending the control message) hangs there, never on a route (U24 decision, shared contract 4).
5. **Unattended mode and the quota (U23/RQ-0…1c).** The relay quota rule is live (`relay_quota.rule` is on). Self relays spend `self_left` at the automatic approval; **member relays spend the lead's `member_pool_left` (RQ-2, spec §3.4)** — see §4 for the hook points P6-2b must leave.
6. **Mod ground moved.** `register.js` is 1047 lines (the plan's "grows past 900" risk is already past). Since the plan: `/relay now`, `/lead`, PL-1g's member re-read (`recheckMember`), `recv` state `s.state`/`s.leadAsk`. The new member path (P6-6) must not take `s.state` before an await the way `/relay now` does and must coexist with `recheckMember` (a *member*'s hello re-send at the threshold is harmless: a member never reaches `maybeBegin`'s ask). Hooks: no `session.receive` registration exists, so P6-6's registration cannot hit the "second registration of one event without a matcher" failure (U1 M-U1-3) — but `TestHooks_NoEventRegisteredTwiceWithoutMatcher` must keep passing.
7. **Skill ahead of the code.** `SKILL.md` ("As a lead") already tells a lead to run `pdx relay <ref>` when a context notice arrives; the command does not exist (`runRelayCmd` answers usage, exit 2) and the notice (P7-1) is not built either. Low risk today; P6-5 should land with or before any notice, or the line be softened until then.
8. **Hook decide grew.** `handleHookDecide` now sits beside U23/P8a pieces (ask flags, `pruneAskFlags`); P6-3b's decide order (lead lock → relay lock → stale-flag removal) still fits the structure (`hooks.go:23-100`), but the plan's line numbers (`hooks.go:23-175`) are stale; re-read at start.
9. **Deployed schema.** `relay_ops` is a deployed table: P6-2a's `pid`/`pane_id` and branch-A's `seen_at` are `ensureColumn` migrations — **verify the host DB with `sqlite3 -readonly` before writing them** (the RQ-1a2 rule, `docs/specs/2026-10-09-relay-quota-spec-plan.md`). `mod_hello` is a new table (`CREATE TABLE IF NOT EXISTS`).
10. **State machine.** `relayTransitions` (`relay_store_report.go:14`) has `requested → claimed|cancelled|failed` and `awaiting_approval → claimed|cancelled`. RQ-2 needs `awaiting_approval → requested` for **member** ops (see §4).

### 3. Remaining order and sizes (every PR ≤ 800 lines / ≤ 20 files, tests included; ×2 allowance already applied)

Deploy: P6 needs no new batch rule except that anything touching the **mod** (3a, 3c, 6) needs `pdx setup`, and **P6-6 must wait for P6-2b+P6-4+P6-5 to be deployed** (protocol 2 hello makes the daemon's `relay_unsupported` check pass).

| # | PR | Re-estimate (lines / files) | Notes |
|---|---|---|---|
| 1 | **P6-0** pure move | ~560 diff / 3 | optional, off the critical path: `relay.go` 468 lines; P6-5 adds new files anyway. Do it just before P6-5 if wanted (byte-comparison proof, no codex). |
| 2 | **P6-1′** handover notice only (no virtual peer) | ~180 / 4 | if Q1 is ruled as recommended; else the plan's 420/5 minus the sender. Uses `Sender`; lead address helper reused from adopt. |
| 3 | **P6-2a** wire + columns + `mod_hello` + `cleared` binding | ~600 / 10 | `ensureColumn` ×2 on `relay_ops`, new table, hello upsert, binding by `op.PID` (fail closed for a member op with pid 0). |
| 4 | **P6-2b-1** create route + control message + exclusion tx | ~520 / 5 | `relay_member.go`: checks (lead, `matchMember`, running, mod ≥ 2, one op), the **single create transaction** (membership confirm + op insert, §4), control send after commit via the sender; replay/`id_conflict`; tests for the release race. |
| 5 | **P6-2b-2** claim + op long-poll + wake-ups (+ `seen`, branch A) | ~480 / 5 | `POST /api/relay/ops/{id}/claim` (only the target session), `GET /api/relay/ops/{id}?wait=N`, `POST …/seen` + `seen_at` column. The plan's 650/6 split in two. |
| 6 | **P6-3a** git facts in the mod, write-tail rule | ~340 / 5 | edits `relay_prompts.go` default body + fixed tail, regenerates `prompts.js`; mod only; `pdx setup`. |
| 7 | **P6-3b-1** move flock helpers to `internal/team` | ~300 / 6 | pure move + call sites (`lead.go`, `hook.go`); byte-comparison proof. |
| 8 | **P6-3b-2** `pdx relay lock|unlock`, decide `allow`/`deny`, `pdx hook` prints allow, safety net, prune guard | ~620 / 9 | the plan's 720/12 minus the move. |
| 9 | **P6-3c** mod raises/lowers the flag | ~320 / 3 | needs the kit's `turn.start` hold check (plan rule 1 has the fallback). |
| 10 | **P6-4a** `reconcileFromFrames`, boot reconciliation (#1735), completion/failure notices | ~560 / 5 | closes #1735 (`LiveSessions(ctx,"cc")` verified frame + pid check); notices on `ReportApplied` only. |
| 11 | **P6-4b** claim/stall timeouts in the sweeper (branch A) | ~320 / 4 | `created_at + 60 s` unseen → `member_unresponsive`; seen → 15 min; stall 15 min after `updated_at`; no agent-module change. |
| 12 | **P6-5** CLI | ~560 / 4 | dispatch by ref shape, `claim`, `seen`, `--wait` mapping, refusal map (`not_lead`, `not_your_member`, `relay_unsupported`, `not_your_op`). |
| 13 | **P6-6** mod: control consumed, claim after the running turn, protocol 2, acceptance doc | ~600 / 5 | **consider `hooks/member.js`** (Q3): `register.js` is 1047 lines; ES imports between hook files already work (`ask.js`, `events.js`, `lease.js`, `prompts.js`). |
| — | **P6-7a / P6-7b** cross-host | 600 + 450 | **deferred until P4c-4 exists** (P4b/P4c unbuilt); not on RQ-2's path. |
| — | **P6-8** SPA line | ~120 / 5 | independent of P6-2b (the daemon half is merged); can land any time. |
| — | **P7-1 / P7-2** | 620 + 430 | unchanged in kind; P7-1 needs the `AgentStatus` accessor only if P6-4 had taken branch B — it did not, so P7-1 adds it (agent-module change; coordinate with 88). |

Total remaining on the local path (rows 2–13) ≈ 5 000 lines in 13 PRs (the plan had ≈ 5 300 for the same PRs), plus P6-0/P6-8 optional.

**Critical path to a working member relay:** 2a → 2b-1 → 2b-2 → 3a → 3b-1 → 3b-2 → 3c → 4a → 4b → 5 → 6. **RQ-2 depends only on 2b-1** (the create transaction and the control send); it is *useless* before the mod side (6) exists, because nothing can be relayed — recommend RQ-2 right after P6-6, or alongside 2b-1 behind the existing `relay_quota` rule only when the mod is ≥ 2 (the create already answers `relay_unsupported` otherwise, so RQ-2 can merge earlier without a user-visible change).

### 4. Hook points P6-2b must leave for RQ-2 (spec §3.4)

RQ-2's rule: *while unattended is on and the quota rule is on, a lead's member relay spends 1 of the lead's chain `member_pool_left`; at 0 the request becomes a `member_relay` approval and waits for a person; unattended off → no approval, nothing spent.* To make that a small PR, P6-2b-1 must be written so that:

1. **One store function creates the op in one transaction** — `CreateMemberRelayOp(op, gate func(tx *sql.Tx) (needsApproval bool, err error))`: takes the write lock, confirms the target's member row is `active` in a live team (the release exclusion), runs `gate`, inserts the op (`requested`, or `awaiting_approval` when the gate says so), and — for the approval case — inserts the approval row in the **same** transaction. In P6-2b-1 the gate is `nil` (always `requested`). RQ-2 supplies the gate: spend pool (same guarded `UPDATE … member_pool_left >= 1` + `rev + 1`, `spendSelfQuotaIn`'s sibling) or request approval.
2. **Sending the control message is its own step** — `m.sendMemberControl(op)` — called from the create path *and*, for RQ-2, from `afterApproved` for a `member_relay` approval (the winner point), and from boot reconciliation for `requested` ops. It must be idempotent on the op id (the claim is a CAS, so a second delivery is harmless).
3. **State machine:** add `awaiting_approval → requested` for **member** ops in `relayTransitions` (a self op keeps `awaiting_approval → claimed`); a denied/expired/cancelled `member_relay` row maps the op to `cancelled` the way `afterClose` does for self ops (`relay_report.go`); deadline/lease sweeper rules apply to the approval row as for any kind.
4. **Wire/SPA first:** a new approval kind `member_relay` is skipped by an older SPA row by row (`APPROVAL_KINDS`, `approval-ws.ts`) — the PL-2a lesson. 88's SPA card must merge and be fast-forwarded **before** the daemon that can open such a row is deployed. P6-2b-1 itself only reserves the constant `KindMemberRelay` (not yet opened by any path), so P6-2b-1 can deploy before the SPA.
5. **The lead's CLI wait** (`pdx relay <ref> --wait`, P6-5) already long-polls the *op*, which is the right object for an approval wait too (`awaiting_approval` is a non-terminal op state); the foreground-600 s-timeout rule from `pdx lead request` applies to the *first* response when the op may sit in `awaiting_approval` — P6-5's refusal/exit map should reserve exit 10/11/12 (denied/timeout/cancelled) for RQ-2 (a `cancelled` op from a denied approval already maps to 12 in the plan).
6. **Quota rev/event:** the pool spend, like the self spend, bumps `rev` and publishes `team.relay_quota` through the same `announceSpend` mechanism (a `SpentOut`-style flag carried from the create transaction to the post-commit step).

### 5. Open questions for the coordinator

1. **P6-1 without the virtual peer.** Send the control message and the handover notice from the *lead's* inbox through the PL-1d1 sender (recommended: no new process-level component; the control message is invisible to the model either way, the handover notice carries the `[pdx team]` prefix), or build the daemon's own virtual peer as P6-1 and spec §8.5 say? Consequence of the first: a control message cannot be re-sent at boot while the lead's session is not live (the claim timeout then fails the op `member_unresponsive`, which is the right outcome for an unattended lead that died).
2. **Branch A for P6-4** (M28 is measured; recommended) — confirm, which retires branch B and P7-1's dependency on P6-4.
3. **Where the member mod path lives.** Keep it in `register.js` (the plan) or put it in `hooks/member.js`, sharing `s` and `later`/`pdx` helpers by import? The file is 1047 lines now; the plan's own risk note predicted this.
4. **P6-7a/b** — formally defer until P4b/P4c exist? (Recommended; nothing in U23/U24/RQ needs them.)
5. **RQ-2 timing** — merge right after P6-2b-1 (the create route answers `relay_unsupported` until a protocol-2 mod exists) or after P6-6? Recommended: after P6-6, so every RQ-2 test can run against a real end-to-end relay.
6. **Skill line (§2 item 7)** — soften now or accept until P6-5.
