# Unattended mode (U23) and lead adopt / release / team display (U24) — Implementation Plan

> **Status (2026-10-08):** revised after one codex round (plan + both specs: 3 critical / 15 important / 2 minor) and the coordinator's binding rulings — the two sections at the end of this file, **"Coordinator decisions (2026-10-08, purdex-f0)"** and **"Codex review of this plan"**, record every disposition and win over anything above them. Written against origin/main **`e47c1f35`** (alpha.596, after P9b-2 #1922 and the host-config GET change #1914). Every `file:line` below was read on that commit. P9a-3 (Hosts › 接力 editors) is not merged on it.
> **Display-first reorder (2026-10-08, purdex-1f):** the PL display path (PL-1a′ → PL-1f′ → PL-2b → PL-2c → PL-3a → PL-3b) now runs before adopt / release; the section **"Display-first reorder (2026-10-08)"** at the end of this file wins over the PR table and the PL sections for the PRs it names.
> **Source:** spec `docs/specs/2026-10-08-unattended-mode-spec.md` (U23, D-U23-1…7, PU-1 / PU-2), spec `docs/specs/2026-10-08-lead-adopt-release-spec.md` (U24, D-U24-1…6, PL-1 / PL-2 / PL-3), the main spec `docs/specs/2026-10-06-lead-team-relay-spec.md` (§6.2 approvals, §6.5 the audit layer, §6.6 the hook lock, §7 team, §8.7 switches, §14 exit codes), plan v3 `docs/specs/2026-10-06-lead-team-relay-plan-v3.md` (its "Global constraints" and binding coordinator decisions apply here unchanged; this commit also edits its P4b-4, P6-1 and P6-2b, see "Contracts for later PRs"), and the line's memory `kickoff_lead_team_relay.md`.
> **Format:** plan v3's compact format — contracts, rules, named tests and mutation gates; no full code blocks. The implementer writes the code test-first from these contracts.
> **Order (spec):** P9 → PU-1 → PU-2 → PL-1 → PL-2 → PL-3, with one exception the deploy order forces: **PL-2a (the adopt card) merges before PL-1c** (decision 3). The PR table is the merge order, and its Needs column is binding.

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development (recommended) or superpowers:executing-plans. Each PR is TDD, one task per commit. Before you start a PR, re-verify its `file:line`s against main: the P9 line, the interface-unification line (U1) and #1866 all move fast.

**Goal.**
- **U23:** one title-bar button turns 無人值守模式 on or off on every shown host. While it is on, each host's daemon approves `lead`, `self_relay` (and, from PL-1, `adopt`) requests itself, through the same approve statements a click runs, recorded with the decider `unattended`. A request created while the switch is on is written already approved, so no client ever sees it open: no dialog, no notification. The App lists the auto-approvals when the person comes back.
- **U24:** a lead adopts a running session as a member (`pdx adopt <ref>`, one click or unattended), releases one (`pdx release <ref>`), and asks the user before ending a member on its own judgement (skill). The App shows teams: an `adopt` card, member tabs under their lead in the sidebar's tab list, and Chrome-style tab groups in the tab bar with a setting to turn them off.
- **U25 (D-U24-7):** the lead dialog's member limit is prefilled 3 (the request shown beside it when different), and an unattended lead approval grants `min(requested, 3)` members.

---

## Global constraints

Plan v3's "Global constraints" (`plan-v3.md:15-44`) apply as written: ≤ 800 diff lines **and** ≤ 20 files per PR; Go `go test ./<pkg>/ -race` + `gofmt -l`; SPA `npx vitest run <path>`, `pnpm run lint`, `npx tsc -p tsconfig.app.json --noEmit`, `pnpm run build`; mod `claude plugin validate|test cmd/pdx/plugin/purdex` + `go test ./cmd/pdx/plugin/`; `git checkout -- pdx` after `go build ./cmd/pdx/` (#1699); English commits with the session's attribution line; `git commit --only` for parallel subagents; spec strings verbatim; SPA strings in `spa/src/locales/{en,zh-TW}.json`; errors on the CLI with the code as the last stderr token; times in unix ms; never print a token; the restart-aware `daemonclient` with a 35 s attempt timeout and `Idempotent()` on create POSTs; the deploy tags (daemon / CLI / setup / SPA / none) and purdex-d3's binding deploy asks (ask d3 and check running workers before every deploy; back up `~/.claude/settings.json` 0600 and key-path-diff it around every `pdx setup --agent cc`); throwaway sessions only in acceptance, no flag left in `~/.config/pdx/hooklocks/`.

Additions for this plan:
- **Sizes and cut points.** Every PR names a planned cut point: where it splits if the diff passes 800 lines or 20 files. PU-1b and PL-1d are pre-split (their estimates after the review exceed 800).
- **No new tab-hosted component.** Every component added here is app-level (title bar, dialog host, sidebar, tab bar). Each PR still says so explicitly, keeps any per-window state in a store outside the component, and tests an unmount/remount where the component can unmount (repo `CLAUDE.md` checklist).
- **Nothing in `pdx` and nothing in the mod turns unattended on** (D-U23-2). A guard test pins it (PU-1c). That the App alone *can* turn it on is not technically enforceable with one shared host token; see deviation 6.
- **Older clients stay silent.** Every new wire field is additive (`omitempty`), every new event type is one an older SPA ignores (`spa/src/hooks/useMultiHostEventWs.ts:219-243` matches types one by one and falls through), and an older SPA drops an unknown approval kind row by row (`spa/src/lib/team/approval-ws.ts:43,52`).
- **Deploy batches** (decision 3):
  - **U23 daemon batch:** PU-1b1 + PU-1b2 + PU-1c (daemon + setup).
  - **U24 batch A:** first PL-2a merged and the main checkout fast-forwarded (SPA); then **PL-1b + PL-1c + PL-1d1 + PL-1d2 + PL-1e in one deploy** (daemon + CLI + `pdx setup --agent cc`: the skill ships with the release and kill capabilities, decision 8).
  - **U24 batch B:** PL-1f (daemon), and PL-1g (daemon + setup) once U1-1b has merged.
  - SPA PRs fast-forward as they merge.

## Review focus

1. **Approved at create ⇒ never open (D-U23-1, D-U23-6).** While the switch is on, a request is inserted and approved in **one** write transaction, read under `createMu`; its first committed state is `approved`, so no snapshot and no poll ever sees it open, and its only event is `closed`. → PU-1b1, PU-1b2, PL-1c.
2. **The daemon decides through the decide statements.** Every approve — a click, create-time, the switch-on sweep, the sweeper's reconciliation, boot — runs the kind's own `…ApprovedIn(tx)` statements; the decider is `{kind: "unattended"}`. → PU-1b1, PU-1b2, PL-1c.
3. **One winner point for side effects.** Every approved close, on every path, ends in `announceClosed` → `afterApproved`; the adopt notice and the roster event hang there, never on a route. → PL-1c, PL-1d1, PL-1f.
4. **The switch never leaves a request behind (D-U23-3).** Switch-on sweeps under `createMu`; whatever stays open is approved by the sweeper's next tick while the switch is on. Hook kinds are never touched. → PU-1b2, PU-1c.
5. **The safety layer (D-U23-2) and its limit.** Admin token only (peer token 401), no `pdx` command, no mod call, skill forbids, every change audited (`changed_by`, address, log line) and broadcast; one shared token means a same-uid agent can still call the route (deviation 6). → PU-1c.
6. **Adopt re-checks every refusal at approve, in one transaction (D-U24-2);** the registry read is the one thing outside it, and the gone-sweeper is its compensation. → PL-1b, PL-1c.
7. **Notices are at least once.** The pending notice is written in the transaction that changes the membership and cleared only after a send. → PL-1b, PL-1d1.
8. **Release and kill never touch what is not the member's.** Release blocks on any relay op in flight and is exclusive with a later member-relay create (P6-2b contract); an adopted member's kill signals only a re-verified process, never the user's tmux session. → PL-1d2.
9. **Groups are derived, never stored (D-U24-6).** Membership comes from the roster and the tab's panes; a drop outside a group snaps back without writing the order. → PL-2b, PL-3b.

---

## PR table

| PR | Content | Needs | ≈ lines / files | Deploy |
|---|---|---|---|---|
| **PU-1a** | Wire contract of unattended (`relay.unattended.v1`, `team.unattended` event, decider, state and page DTOs); host config key `unattended` with its reader / writer (no generic route) | P9 complete | 440 / 5 | none |
| **PU-1b1** | Store: each kind's approve as `…ApprovedIn(tx)`; `CreateApproved` / `CreateSelfRelayApproved` (insert + approve in one transaction); `ListAutoApproved` with a cursor | PU-1a | 480 / 6 | U23 daemon batch |
| **PU-1b2** | Module: shared `approve()`; create-time approval at create (lead) and begin (self_relay); `sweepUnattended`; the sweeper's per-tick reconciliation; boot; the unattended lead grant `min(requested, 3)` (U25) | PU-1b1 | 700 / 7 | U23 daemon batch |
| **PU-1c** | `GET` (paged) / `PUT /api/team/unattended` (admin only, client required, `swept` / `pending`), `team.unattended` snapshot + changed events, capability `relay.unattended.v1`, skill line, guard tests | PU-1b2 | 700 / 9 | U23 daemon batch (daemon + setup) |
| **PU-2a** | SPA data: unattended API, per-host store, support probe from `/api/info`, WS branch | PU-1c | 520 / 13 | SPA |
| **PU-2b** | SPA: aggregate (off / on / partial / none), fan-out, the title-bar toggle with its tooltip; no toast for an unattended close | PU-2a | 700 / 12 | SPA |
| **PU-2c** | SPA: the ▾ "while you were away" panel with 「顯示更多」 | PU-2b | 440 / 6 | SPA |
| **PU-2d** | SPA (U25, D-U24-7): the lead dialog's member limit prefilled 3, with 「lead 申請 N 個」 beside it | PU-2c | 160 / 4 | SPA |
| **PL-1a** | Wire contract of adopt / release (`adopt` kind, payload, codes, `released`, member `origin`, `close_reason`, notices) | P9 complete, PU-2c merged | 440 / 5 | none |
| **PL-1b** | team.db: `close_reason`, `team_members.origin` / `ended_at` / `notice_pending` / `notice_since`; `adoptApprovedIn` with every re-check; `ReleaseMember`; reads for create | PL-1a, PU-1b1 | 780 / 8 | U24 batch A |
| **PL-2a** | SPA: the `adopt` card in `ApprovalDialogHost`, its notification and kind label | PL-1a | 480 / 10 | SPA — **merged and fast-forwarded before PL-1c** |
| **PL-1c** | `POST /api/team/approvals {kind:"adopt"}` (target, refusals, one open per target, create-time approval), decide of `adopt`, unattended covers `adopt`; `afterApproved` winner hook; resolver `ResolveOriginByRef` / `InboxOf` | PL-1b, PU-1b2, **PL-2a merged** | 740 / 9 | U24 batch A |
| **PL-1d1** | Peers in-process sender (moved from plan v3 P6-1); the notice outbox: kick, sweeper retry, 10-minute give-up; the four approve paths each send | PL-1c | 680 / 9 | U24 batch A |
| **PL-1d2** | `POST /api/team/release` (+ its notice); kill of an adopted member by a re-verified process; released × switch × pause matrix | PL-1d1 | 720 / 8 | U24 batch A |
| **PL-1e** | CLI `pdx adopt`, `pdx release`; exit codes; skill text for D-U24-1…4 with a pinned-rule guard | PL-1d2 | 720 / 7 | U24 batch A (CLI + setup) |
| **PL-1f** | `GET /api/team/roster` + `team.roster` snapshot / changed events (host-wide teams for the App) | PL-1e | 620 / 11 | U24 batch B (daemon) |
| **PL-1g** | Mod: a cached `member` role is re-read at the threshold, so a released or ended-team member can self-relay again | PL-1d2, **U1-1b merged** | 160 / 2 | U24 batch B (daemon + setup) |
| **PL-2b** | SPA: roster parse + `useTeamRosterStore` + WS branch; pure team-tab layout (any pane, blocks, display order, orphan hint, block-aware reorder) | PL-1f | 700 / 8 | SPA |
| **PL-2c** | SPA: sidebar tab list — member tabs under their lead, indented, collapsible; "member of" hint; block-aware drag | PL-2b | 650 / 9 | SPA |
| **PL-3a** | SPA: setting 「分頁群組顯示 team」 (new 介面 subsection), team palette, `TeamGroupChip` | PL-2b | 520 / 11 | SPA |
| **PL-3b** | SPA: `TabBar` groups — chip, underline, contiguity, collapse, active never hidden, drop-outside snaps back | PL-3a, PL-2c | 760 / 5 | SPA |

Total ≈ 12 500 lines across 21 PRs.

---

## Shared contracts (read once; the PR sections refer to them)

**Exit codes added** (spec §14; `cmd/pdx/exitcodes.go:8-17`):

| Code | `pdx adopt <ref>` | `pdx release <ref>` |
|---|---|---|
| 0 | approved: one JSON line on stdout | released (or already not active): the member JSON |
| 2 | usage (no target, a target that is not a ref form, an extra word, bad `--wait`) | usage |
| 10 | denied | — |
| 11 | timed out (U7) | — |
| 12 | cancelled (signal → DELETE) or abandoned | — |
| 13 | `not_lead`, `team_full`, `adopt_self`, `adopt_target_is_lead`, `adopt_already_member`, `adopt_target_not_found`, `remote_unsupported`, `request_open`; **and** a request that closed `cancelled` with one of those as its `close_reason` (a re-check at approve failed) | `not_lead`, `not_your_member`, `relay_open` |
| 20 / 21 | daemon unreachable through the grace / plain 404 | same |

**Approval kinds after this plan:** `lead`, `self_relay`, `hook_ask`, `hook_permission`, **`adopt`**. Auto-approvable (unattended): `lead`, `self_relay`, `adopt` — never the hook kinds (U23 "不在範圍內").

**Decider of an auto-approval:** `decided_by = {"kind": "unattended", "label": "無人值守模式"}` (no `addr`). `team.Client.Kind` today is `"app"` or `"terminal"` (`internal/team/wire.go:81-86`, `spa/src/lib/team/types.ts:93-98`).

**Host events added** (`core.HostEvent{Type, Value}`, `internal/core/events.go:14-22`; each sends a snapshot to every new subscriber through `OnSubscribe`, `events.go:218`):

| Type | Value | PR |
|---|---|---|
| `team.unattended` | `{op: "snapshot" \| "changed", state: UnattendedState}` | PU-1c |
| `team.roster` | `{op: "snapshot" \| "changed", teams: [TeamRoster]}` (`[]` never null) | PL-1f |

**Routes added** (all on the general chain, `TokenAuth` = admin token only, `cmd/pdx/http_chain.go:28-36`; none under `/api/peers/`):

| Route | Answers | PR |
|---|---|---|
| `GET /api/team/unattended?before=<ms>&limit=<n>` | 200 `UnattendedView` (a page of `approved`, `truncated`, `next_before`) · 400 · 500 `storage_error` | PU-1c |
| `PUT /api/team/unattended` | 200 `UnattendedView` with `swept` and `pending` (and `list_failed: true` with `approved: []` when the write took effect but the list could not be read) · 400 `bad_request` · 500 · 503 `not_ready` | PU-1c |
| `POST /api/team/approvals` with `kind:"adopt"` | 201 / 200 replay · 400 · 409 (adopt refusals) · 503 | PL-1c |
| `POST /api/team/release` | 200 `Member` · 400 `origin_unknown` · 409 `not_lead` / `not_your_member` / `relay_open` · 503 | PL-1d2 |
| `GET /api/team/roster` | 200 `Roster` · 500 · 503 | PL-1f |

**team.db columns added** (each through `ensureColumn`, `internal/module/team/migrate.go:29-47`, in a new `migrateAdopt(db)` run after `migrateUsage` at `internal/module/team/store.go:83-86`; PL-1b):

| Column | Declaration | Meaning |
|---|---|---|
| `approval_requests.close_reason` | `TEXT NOT NULL DEFAULT ''` | the code of an adopt cancelled at approve |
| `team_members.origin` | `TEXT NOT NULL DEFAULT 'spawned'` | `spawned` \| `adopted` |
| `team_members.ended_at` | `INTEGER NOT NULL DEFAULT 0` | when the row left `active` |
| `team_members.notice_pending` | `TEXT NOT NULL DEFAULT ''` | `''` \| `adopted` \| `released`: a notice owed to the session |
| `team_members.notice_since` | `INTEGER NOT NULL DEFAULT 0` | when that notice became owed (the 10-minute give-up) |

No new table. The unattended switch is **host config** (`host_config.db`, key `unattended`; PU-1a).

**Notices** (texts pinned in `internal/team/wire_adopt.go`; sent from the lead's inbox, at least once, PL-1d1):

| Notice | Text |
|---|---|
| adopted (to the target) | `[pdx team] 你已成為 <lead address> 的 member（team <team id>）。自我接力已關閉，接力由 lead 安排；回報請送 <lead address>。` |
| released (to the member) | `[pdx team] <lead address> 已讓你離開 team <team id>：你現在是一般 session，自我接力依這台主機的設定。` |

**The winner point** (PU-1b2, extended by PL-1c and PL-1f): every close that wins, on every path, ends in `announceClosed(after, rep)` (`internal/module/team/module.go:406-410`: `broadcast("closed")`, `wake`, `afterClose`). This plan adds `afterApproved(after)` there for an `approved` row, and calls `announceClosed` after a create-time approval commits too, so a click, create-time, the switch-on sweep, the per-tick reconciliation and boot share one place for side effects.

---

## Contracts for later PRs

These bind PRs of plan v3. This commit edits plan v3 to match (decisions 12 and 15).

1. **P6-2b — a member-relay create is exclusive with a release.** `POST /api/team/relays` inserts the op in **one write transaction that also confirms the target's member row is still `active` in a live team** (the write lock first, as `CloseSelfRelayApproved` does at `internal/module/team/team_store.go:191`; the membership read and the insert in the same transaction). With PL-1d2's `ReleaseMember` — a conditional `UPDATE` that refuses while any relay op of the session is not terminal — exactly one of the two commits: a release that committed first makes the create answer 409 `not_your_member`; an op that committed first makes the release answer 409 `relay_open`. P6-2b's test `TestRelayCreate_RacesReleaseOneWins` pins it.
2. **P6-1 — the sender exists.** `peers.Sender` / `SenderKey` and its two tests land in PL-1d1. P6-1 needs PL-1d1 and adds only the virtual peer, the auto-reply and the handover notice.
3. **P4b-4 — `remote_unsupported` exists.** PL-1a defines `team.ErrRemoteUnsupported` and PL-1e maps it to exit 13; P4b-4 needs PL-1a and adds only `no_host_for_repo`.
4. **Any later side effect of an approval** (a new kind, a P4c forward) hangs on `afterApproved`, never on a route.

---

# Phase PU-1 — the daemon decides (unattended spec D-U23-1…4, D-U23-6 list, D-U23-7)

**Facts this phase rests on** (read in the code, not inferred):
- **One approve path per kind, today inside `handleDecide`.** `internal/module/team/handler.go:339-462`: hook kinds branch out at `:375-378`; a lead approve builds the grant from the payload when the client sent none (`:383-405`) and runs `CloseLeadApproved` through `closeWith` (`:409-414`); a self_relay approve runs `CloseSelfRelayApproved` (`:415-419`); everything else `closeAs` (`:420-422`). Errors map to 409 `already_lead` / `member_cannot_lead` with the row left open (`:427-439`); a `memberCancelled` self relay answers 409 `member_relay_is_leads` after its commit (`:449-455`); one log line per decision (`:460`).
- **The approves are each one transaction.** `CloseLeadApproved` begins its own transaction, runs the CAS `closeRowIn` first, then the member re-check and the team insert (`internal/module/team/team_store.go:274-322`); `CloseSelfRelayApproved` takes the write lock with `UPDATE … SET id = id` first (`:184-220`). The CAS statement is `closeRowIn` (`internal/module/team/store.go:259-298`), and an insert is `insertRowIn` (`:186-205`), both on a `dbtx`.
- **Every close that wins broadcasts once and wakes its pollers.** `closeWith` → `closeWithOp` → `announceClosed` = `broadcast("closed")` + `wake` + `afterClose` (`internal/module/team/module.go:387-410`); `broadcast` holds `eventMu` (`:414-423`). `afterClose` moves a self_relay row's op through `ReportRelay` (`internal/module/team/relay_handler.go:374-401`), which runs its own transaction (`internal/module/team/relay_store_report.go:147-201`) and is idempotent per (op, state).
- **Create broadcasts `opened` after the insert, under `createMu`; the snapshot reads under `eventMu` only.** Lead: `handler.go:173-246` (`createMu` at `:173-174`, insert `:227-243`, `broadcast("opened")` `:245`, 201 `:246`). Self relay: `relay_handler.go:261-350` (`createMu` `:261-262`, `CreateRelayOp` `:316` then the row's `Create` `:334-347`, `broadcast("opened")` `:349`, 201 `:350`). `snapshotUnderLock` reads `ListOpen` under `eventMu` (`module.go:453-477`), which `createMu` does not exclude — so a row that is ever committed open can reach a new subscriber.
- **A self relay is refused before any row when the session is a member, the host switch is off, or the session paused** (`relay_handler.go:229-245`, `selfRelayState` `:62-89`). So D-U23-4 holds with no new check: unattended never sees those.
- **The sweeper ticks every second and lists the open rows** (`internal/module/team/sweeper.go:15-18, 50-97`); it returns early when nothing is open (`:68-70`).
- **The audit is the row itself.** `approval_requests.decided_by_json` / `decided_at` (`store.go:49-67`), written only by `closeRowIn`; no code deletes a row (`grep "DELETE FROM" internal/module/team` finds none outside tests). The daemon log line per decision is the second record (spec §6.5).
- **The SPA is silent about a close it never saw open.** `approval-ws.ts:99-105`: a notification is raised only on `opened` (`notifyApprovalOpened`, `:101`), and a toast only when `applyClosed` answers `'elsewhere'`; `applyClosed` answers `'absent'` for a request it does not hold (`spa/src/stores/useApprovalStore.ts:127-142`).
- **The mod does not read the op state `begin` answers.** It takes `op.id`, `request_id`, `op.handoff_path` and `op.ref` (`cmd/pdx/plugin/purdex/hooks/register.js:409-451`) and then waits with `pdx relay wait`, whose answer `approved` starts the write turn (`:459-470`). An already-approved request needs no mod change.
- **`pdx lead request` takes a closed row from create.** Its poll loop runs only `for ap.State == team.StateOpen` (`cmd/pdx/lead.go:201-226`), and the hook flag it raises after the 201 is lowered by its own `defer` (`:194-199`); `leadFinish` prints the grant (`:285-304`).
- **Host config today.** Keys are rows of `host_config(key, value, revision, updated_at)` with a CAS `Put` (`internal/module/hostconfig/store.go:52-63, 108-152`). The GET answers a fixed map of keys (`internal/module/hostconfig/handler.go:87-106`) and each PUT route is bound to one key (`internal/module/hostconfig/module.go:44-53`), so a key no route names cannot be written through `/api/hostconfig`. Services are published in `Init` (`internal/module/hostconfig/module.go:34-39`) and looked up by the team module (`internal/module/team/module.go:233-246`, `lookup` in `spawn_runner.go:51-58`).
- **Capabilities are a static list in core** (`internal/core/info_handler.go:41-49`), pinned in order by `TestHandleInfo_Capabilities` (`info_handler_test.go:372-379`).
- **Admin token only off `/api/peers/*`.** `TokenAuth` accepts the host token, or a ticket on a real WebSocket handshake only (`internal/middleware/middleware.go:71-96`); the outer handler routes only `/api/peers` and `/api/peers/` through `PeerAuth` (`cmd/pdx/http_chain.go:28-36`). The pattern of an admin-only test is `TestNewOuterHandler_HostConfigTeamPutIsAdminOnly` (`cmd/pdx/http_chain_test.go:754-784`). The SPA and `pdx` share that one token (spec §3.3, §6.5).
- **No `pdx` command reaches an arbitrary route.** The dispatcher is a fixed list (`cmd/pdx/main.go:45-93`); `pdx nex` speaks only Nexen's grammar (`cmd/pdx/nex.go:40-100`).

## PR PU-1a — wire contract and the switch's storage

**Goal.** Fix the unattended contract once (decider, state, page, event, capability name) and give the switch a home in host config that no generic route can write (D-U23-1, D-U23-2, D-U23-7).

**Files.**
- Create `internal/team/wire_unattended.go` and `wire_unattended_test.go`.
- Create `internal/module/hostconfig/unattended.go` and `unattended_test.go`.
- Modify `internal/module/hostconfig/module.go:34-39` (register `UnattendedKey`).

**Interfaces.**

```go
// internal/team/wire_unattended.go
const (
	CapabilityUnattended = "relay.unattended.v1" // /api/info capabilities (D-U23-5)
	UnattendedEventType  = "team.unattended"     // HostEvent.Type
	ClientKindUnattended = "unattended"          // Client.Kind of a daemon auto-approval (D-U23-2)
	UnattendedLabel      = "無人值守模式"            // Client.Label of it
	UnattendedPageDefault = 50                   // GET's page size when limit is absent
	UnattendedPageMax     = 200                  // and its cap
	UnattendedLeadMaxMembers = 3                 // U25 / D-U24-7: an unattended lead grant is min(requested, 3)
)
func UnattendedClient() Client            // {Kind: "unattended", Label: UnattendedLabel}
func AutoApprovable(k Kind) bool          // lead, self_relay (PL-1a adds adopt); never hook kinds
type UnattendedState struct {
	On        bool    `json:"on"`
	Since     int64   `json:"since"`                // unix ms of the last off→on; 0 = never on
	ChangedAt int64   `json:"changed_at"`           // last change; 0 = never written
	ChangedBy *Client `json:"changed_by,omitempty"` // who changed it (addr added by the daemon)
}
type UnattendedPutRequest struct { On *bool `json:"on"`; Client Client `json:"client"` }
type UnattendedView struct {
	UnattendedState                          // flattened
	Approved   []Approval `json:"approved"`  // never null: one page, newest first, decided by "unattended" since Since
	Truncated  bool       `json:"truncated"` // more rows exist before NextBefore
	NextBefore int64      `json:"next_before,omitempty"` // the cursor of the next page (a decided_at)
	Swept      int        `json:"swept,omitempty"`   // PUT only: open requests the switch-on approved
	Pending    int        `json:"pending,omitempty"` // PUT only: auto-approvable requests still open after the sweep
	ListFailed bool       `json:"list_failed,omitempty"` // PUT only: the write took effect, the list was not read (approved is [] and means nothing); GET it (decision 30)
}
type UnattendedEventValue struct { Op string `json:"op"`; State UnattendedState `json:"state"` } // op: snapshot | changed
```

```go
// internal/module/hostconfig/unattended.go
const KeyUnattended = "unattended"
const UnattendedKey = "hostconfig.unattended" // registry key
type UnattendedStore interface {
	Unattended() (team.UnattendedState, error)
	SetUnattended(on bool, by team.Client, now int64) (state team.UnattendedState, changed bool, err error)
}
```

**Behaviour rules.**
1. **A never-written key is off** (`UnattendedState{}`). A stored value that does not decode as the struct (unknown field, wrong type, `null`) is an **error**, never "off with no error" and never "on": the team module then treats the switch as off and logs (fail closed, PU-1b2 rule 1).
2. **`SetUnattended` is idempotent.** Same value → `changed=false`, nothing written, `Since`/`ChangedAt` untouched. Off→on sets `Since = ChangedAt = now`; on→off keeps `Since` (the list survives the switch-off, D-U23-6) and sets `ChangedAt = now`. `ChangedBy = &by` on every write.
3. **CAS with retry.** `Get` → compute → `store.Put(KeyUnattended, rev, …)`; a lost race (`ok=false`) re-reads and retries, at most 3 times, then an error.
4. **No generic route.** `KeyUnattended` is not in `handleGet`'s map (`internal/module/hostconfig/handler.go:89-96`), not in `readers` (`internal/module/hostconfig/read.go:100-107`) and has no `PUT /api/hostconfig/*` route. The only writer is the team module's route (PU-1c), which runs the D-U23-3 sweep with the write.

**Tests.**
- `TestWireUnattended_LiteralsArePinned`: the capability, event type, client kind, label, page sizes, `UnattendedLeadMaxMembers = 3`.
- `TestWireUnattended_JSONShapes`: `UnattendedView` flattens the state; `approved:[]` is never `null`; `truncated` always present; `next_before`, `swept`, `pending`, `changed_by` absent when zero / nil.
- `TestAutoApprovable_LeadAndSelfRelayOnly`: `hook_ask`, `hook_permission` and an unknown kind are false.
- `TestUnattended_NeverWrittenIsOff`.
- `TestSetUnattended_OnSetsSinceOffKeepsIt`.
- `TestSetUnattended_OnAgainIsANoop`: `changed=false`, revision unchanged, `Since` unchanged.
- `TestUnattended_CorruptValueIsAnError`: a row written straight into the DB as `{"on":"yes"}` and as `null`.
- `TestSetUnattended_RetriesALostCAS`: the host-config store's `afterRead` seam (`internal/module/hostconfig/store.go:130-132`) bumps the revision once.
- `TestHostConfig_GetAndPutNeverSeeUnattended`: the GET body has no `unattended` field after a `SetUnattended(true)`; `PUT /api/hostconfig/unattended` is not routed (405/404 from the mux).
- `TestInit_RegistersUnattendedStore`.

**Mutation gates.**
- Set `Since` on every write → `…OnAgainIsANoop` red.
- Decode errors as off with a nil error → `…CorruptValueIsAnError` red.
- Add the key to the GET map → `…NeverSeeUnattended` red.

**Size.** ≈ 440 lines, 5 files. **Cut point:** the host-config half (`unattended.go` + test + `module.go`) moves to PU-1a2. **Deploy.** None: nothing reads it yet (fold into the next bump).

**Risks.** None at runtime.

## PR PU-1b1 — the store: approve in the caller's transaction, create already approved, the paged list

**Goal.** Make D-U23-1's "same code path" literal — every approve is the kind's own statements on a transaction — and make the create-time approval one transaction whose first committed state is `approved` (decision 1), so no snapshot and no poll can see the row open.

**Files.**
- Modify `internal/module/team/team_store.go` (`CloseLeadApproved` and `CloseSelfRelayApproved` become thin wrappers over `closeLeadApprovedIn` / `closeSelfRelayApprovedIn`; their behaviour and tests unchanged).
- Modify `internal/module/team/store.go` (`CreateApproved`; the `afterApprovedInsert` seam).
- Modify `internal/module/team/relay_store.go` and `relay_store_report.go` (`insertRelayOpIn`; `reportRelayIn(tx, …)` factored out of `ReportRelay`, `relay_store_report.go:147-201`).
- Create `internal/module/team/store_unattended.go` (`CreateSelfRelayApproved`, `ListAutoApproved`) and `store_unattended_test.go`.

**Interfaces.**
- `func closeLeadApprovedIn(tx *sql.Tx, id string, c Close, t team.Team) (n int64, err error)` — `CloseLeadApproved`'s body after `Begin` (CAS, member re-check, team insert), unchanged; `ErrLeadHasTeam` / `ErrMemberCannotLead` as today.
- `func closeSelfRelayApprovedIn(tx *sql.Tx, id string, c Close, sessionID string) (n int64, memberCancelled bool, err error)` — likewise from `CloseSelfRelayApproved`.
- `func (s *Store) CreateApproved(a team.Approval, hash string, approveIn func(tx *sql.Tx) (refused error)) (team.Approval, error)` — **one write transaction:** `insertRowIn(tx, a, hash, "")` (conflict = error: the caller already answered replays under `createMu`) → `s.afterApprovedInsert` (test seam, nil in production) → `approveIn(tx)` (the kind's `…ApprovedIn`) → `getRowIn` → commit. A refusal or an error from `approveIn` rolls the insert back: nothing is written.
- `func (s *Store) CreateSelfRelayApproved(op team.RelayOp, a team.Approval, hash string, c Close) (team.Approval, team.RelayOp, error)` — one transaction: `insertRelayOpIn` (`awaiting_approval`) → `insertRowIn` → seam → `closeSelfRelayApprovedIn` (a member → rollback, `ErrMemberRelayIsLeads`) → `reportRelayIn(claimed)` → commit.
- `func (s *Store) ListAutoApproved(since, before int64, limit int) (rows []team.Approval, truncated bool, err error)` —
  `WHERE state = 'approved' AND decided_at >= :since AND (:before = 0 OR decided_at < :before) AND json_extract(decided_by_json, '$.kind') = 'unattended' ORDER BY decided_at DESC, id`, reading `limit + 1` rows. **A page never ends inside one millisecond:** when row `limit + 1` shares the last row's `decided_at`, the page is extended to every row with that `decided_at`, so the next cursor (`before = that decided_at`) skips nothing and repeats nothing. `since = 0` answers `[]` without a query. Never nil.

**Behaviour rules.**
1. **First committed state = approved.** Inside `CreateApproved` and `CreateSelfRelayApproved` the row exists as `open` only in the uncommitted transaction; WAL readers on other connections see the last commit (team.db opens `journal_mode(wal)`, `store.go:40`), so `ListOpen`, `sendSnapshot` and a poll never see it.
2. **The statements are the decide's.** `CloseLeadApproved` / `CloseSelfRelayApproved` call the same `…In` functions, so a click and a create-time approval run byte-identical SQL.

**Tests** (`store_unattended_test.go`, a file-backed team.db in a `TempDir`, so a second connection exists):
- `TestCreateApproved_NeverVisibleOpen`: the `afterApprovedInsert` seam calls `ListOpen()` and `Get(id)` on the pool → no such row; after the commit the row is `approved` with the decider.
- `TestCreateApproved_RefusalWritesNothing`: `approveIn` answers `ErrMemberCannotLead` → no row, no team.
- `TestCreateApproved_LeadCreatesTheTeam`: `LiveTeamByLead` finds it.
- `TestCreateSelfRelayApproved_OpClaimedRowApprovedNeverOpen`: the seam sees neither the op nor the row; afterwards op `claimed`, row `approved`.
- `TestCreateSelfRelayApproved_MemberRollsBack`.
- `TestCloseLeadApproved_UnchangedThroughTheRefactor`: the existing `team_store_test.go` and `store_test.go` cases stay green unchanged (the refactor gate).
- `TestListAutoApproved_SinceKindAndCursor`: rows approved by an app, by unattended before `since`, by unattended after `since`, denied → only the third; pages of 2 over 5 rows walk all 5 once.
- `TestListAutoApproved_PageNeverSplitsAMillisecond`: three rows share one `decided_at` at the page boundary → the page holds all three; the next page starts after them.

**Mutation gates.**
- Commit the insert before the approve (two transactions) → `…NeverVisibleOpen` red.
- End a page at exactly `limit` → `…PageNeverSplitsAMillisecond` red.
- Drop the `decided_at >= since` filter → `…SinceKindAndCursor` red.

**Size.** ≈ 480 lines, 6 files. **Cut point:** `ListAutoApproved` and its two tests move to PU-1c. **Deploy.** U23 daemon batch (behaviour unchanged until PU-1b2 calls it).

## PR PU-1b2 — the module approves: at create, at begin, at switch-on, every tick, at boot

**Goal.** D-U23-1 (approved at once, same statements, same close event, same audit row), D-U23-3 (switch-on approves what is open, and nothing stays behind — decision 5), D-U23-4 (nothing new for members, off and paused sessions), D-U23-6 first two bullets (no dialog, no notification), and the daemon half of U25 / D-U24-7 (an unattended lead grant is `min(requested, 3)` members — decision 22).

**Files.**
- Create `internal/module/team/unattended.go` (`approve`, `afterApproved`, `unattendedOn`, `autoApprove`, `sweepUnattended`, `reconcileUnattended`) and `unattended_test.go`.
- Modify `internal/module/team/handler.go`: decide's approve branch (`:379-422`) becomes a call to `approve`; create (`:226-246`).
- Modify `internal/module/team/relay_handler.go:305-350` (begin).
- Modify `internal/module/team/sweeper.go:63-97` (`reconcileUnattended` after the open list is read).
- Modify `internal/module/team/module.go`: the `unattended hostconfig.UnattendedStore` field, its `Init` lookup beside the prompts' (`:242-246`), `announceClosed` calls `afterApproved` (`:406-410`), and the boot sweep in `Start` after `resumeSpawns` (`:322-323`).
- Modify `internal/module/team/handler_test.go` (the fixture registers a fake `UnattendedStore` under `hostconfig.UnattendedKey`, beside `:243-246`).

**Interfaces.**
- `func (m *Module) approve(a team.Approval, c Close) (after team.Approval, won bool, out approveOutcome, err error)` — **the** approve of an open row, through `closeWith` and a transaction running the kind's `…ApprovedIn`: `lead` (grant `c.Grant`, nil → the payload's, `leadGrantOf(a)` factored out of `handler.go:383-404`), `self_relay` (`out = memberCancelled`), and (PL-1c) `adopt`. Hook kinds → an error (they decide through `decideHook`).
- `handleDecide` keeps its parsing, grant editing, error mapping and log line; only the store calls move into `approve`.
- `func (m *Module) afterApproved(a team.Approval)` — called by `announceClosed` for a row now `approved`. PU-1b2 leaves it empty; PL-1c adds the adopt notice kick, PL-1f the roster event.
- `func (m *Module) unattendedOn() bool` — read **under `createMu`** by every caller; a read error is **false** plus one log line (fail closed).
- `func (m *Module) autoApprove(a team.Approval) (team.Approval, bool)` — `approve(a, Close{State: approved, DecidedAt: m.now(), DecidedBy: ptr(team.UnattendedClient()), Grant: unattendedGrant(a)})`; true for a won close now `approved`.
- `func unattendedGrant(a team.Approval) *team.Grant` — **U25 / D-U24-7:** for a `lead` row, `{MaxMembers: min(p.MaxMembers, team.UnattendedLeadMaxMembers), Roots: p.Roots}` from the stored payload (the create already normalised an unspecified value to 3, `handler.go:79-88, 162`); nil for every other kind. A click's grant is unchanged: the person edits it in the dialog (PU-2d), and a decide with no grant still takes the payload's values (`handler.go:390-392`). Logs `[team] approval <id> approved by unattended (origin <ref>)<team note>`, or `… not auto-approved: <why>` once per row id (an in-memory set, so a rule refusal retried every tick logs once).
- `func (m *Module) sweepUnattended(why string) (approved, pending int)` — caller holds `createMu`. `ListOpen()`, `autoApprove` for every `AutoApprovable` row, oldest first; `pending` = those still open; one summary log line.
- `func (m *Module) reconcileUnattended(open []team.Approval)` — called by `tick` after its `ListOpen` (`sweeper.go:63-70`): when some row is `AutoApprovable` and `unattendedOn()`, takes `createMu` and `autoApprove`s each still open.

**Behaviour rules.**
1. **At create (lead), under the `createMu` the handler already holds,** after every create check and before any write: `if m.unattendedOn()` → `CreateApproved(row, hash, closeLeadApprovedIn(… UnattendedClient …))` (PU-1b1) → commit → `announceClosed(row, nil)` → 201 with the approved row. **No `opened` is broadcast** and no committed state was ever open. A refusal from the transaction (`ErrLeadHasTeam`, `ErrMemberCannotLead`) answers the create's own 409 (`already_lead`, `member_cannot_lead`) with nothing written; a storage error is today's 500 with nothing written. Switch off → today's path exactly (insert open, `opened`, 201).
2. **At begin (self_relay), the same,** with `CreateSelfRelayApproved` in place of `CreateRelayOp` + `Create` (`relay_handler.go:311-347`): 201 `RelayBeginResponse{Op: <claimed op>, RequestID}`; `announceClosed` runs `afterClose`, whose `claimed` report is the idempotent no-op. The mod's `pdx relay wait` answers `approved` on its first poll.
3. **A replay of the same id is unchanged.** `getRow` answers the stored row (`handler.go:183-191`), approved or not; `RelayOpByRequest` the stored op (`relay_handler.go:192-218`).
4. **Members, off and paused sessions raise nothing** (D-U23-4): `begin`'s 409s (`relay_handler.go:235-245`) run before any row; `create`'s `already_lead` / `member_cannot_lead` (`handler.go:205-225`) too.
5. **Hook kinds are never approved by the daemon.** `AutoApprovable` is false for them; `/api/ask/begin` (`ask_handler.go`) is not touched.
6. **Nothing stays open while the switch is on** (decision 5): a row the switch-on sweep could not approve (a transient storage error, a lost race) is approved by the sweeper's next tick (`reconcileUnattended`, every second). A row refused by a rule (the lead's origin became a member) stays open and shows its dialog, as a click's refusal does; its deadline still runs.
7. **Boot:** `Start` calls `sweepUnattended("boot")` under `createMu` when `unattendedOn()`, after `reconcileRelays` and `resumeSpawns`.
8. **Lock order is today's:** `createMu` → store transaction → `eventMu` (broadcast). No store write under `eventMu`.
9. **A manual decide and the daemon race on the CAS** (an open row approved by a click while the tick reconciles it): exactly one wins; the loser of a click gets today's 409 `already_decided` carrying the row with `decided_by.kind == "unattended"`.

**Tests** (fixture: `newFixture`, `handler_test.go:235-268`, file-backed team.db; its test subscriber collects every event):
- `TestCreate_UnattendedOnIsApprovedNeverOpen`: the store's `afterApprovedInsert` seam calls `ListOpen()` and runs `sendSnapshot` to a second test subscriber → neither sees the row; then 201, `state approved`, `decided_by {kind unattended, label 無人值守模式}`, no `addr`; `LiveTeamByLead` finds the team; the first subscriber saw exactly one event, `closed`.
- `TestCreate_UnattendedOffOpensAsToday`: `opened` then nothing.
- `TestUnattendedLeadGrant_IsMinOfRequestAndThree` (U25 / D-U24-7): a table over requested `max_members` — not given (→ 3), 1, 2, 3, 5, 8 → the team's grant `max_members` is 3, 1, 2, 3, 3, 3, and the roots are the requested ones; once at create-time, once through `sweepUnattended`, once through `reconcileUnattended` (the three daemon paths share `unattendedGrant`).
- `TestDecide_ClickGrantIsNotCapped`: a click that approves with `max_members: 5` still creates a team of 5 (U25 caps only the daemon's own approvals).
- `TestCreate_UnattendedReadErrorIsOff`: the fake store errors → the row opens, `opened` is broadcast.
- `TestCreate_UnattendedReplayAnswersTheApprovedRow`.
- `TestCreate_UnattendedRefusalInTheTransactionIs409NothingWritten`: the origin becomes a member at the seam → 409 `member_cannot_lead`, no row, no event.
- `TestCreate_SwitchOnRacesCreateNeverOpen`: a seam pauses a create just before it takes `createMu` while a switch-on PUT completes; the create then goes on → it sees the switch on: the row is never committed open and no `opened` is broadcast.
- `TestRelayBegin_UnattendedOnClaimedNeverOpen`: the seam sees neither op nor row; 201 op `claimed`; `GET /api/relay/wait/{id}` answers `approved` at once; one `closed`, no `opened`.
- `TestRelayBegin_UnattendedMemberPausedOrOffRaisesNothing`: member → 409 `member_relay_is_leads`, host switch off → 409 `self_relay_off`, paused → 409 `self_relay_paused`; no row, no event.
- `TestSweepUnattended_ApprovesOpenLeadAndSelfRelayLeavesHookKinds`: one open of each of the four kinds → lead and self_relay approved by unattended, both hook kinds still open; `(2, 0)`.
- `TestTick_ApprovesWhatTheSwitchOnSweepLeftOpen`: a seam fails the first approve once → the sweep answers `(1, 1)`; the next `tick()` approves the second row.
- `TestTick_RuleRefusalLogsOnce`: a lead row whose origin is a member stays open over three ticks; one log line.
- `TestStart_UnattendedOnSweepsAtBoot`.
- `TestDecide_RacesUnattendedOneWins`.
- `TestDecide_ApproveStillWorksThroughApprove`: the existing decide tests stay green unchanged.

**Mutation gates.**
- Insert open, broadcast `opened`, then approve (today's order) → `…ApprovedNeverOpen` red.
- Read the switch before taking `createMu` → `TestCreate_SwitchOnRacesCreateNeverOpen` red (the row commits open after the sweep and is broadcast `opened`).
- Drop `reconcileUnattended` from `tick` → `…LeftOpen` red.
- `AutoApprovable` includes `hook_ask` → `…LeavesHookKinds` red.
- `unattendedGrant` returns the payload's `max_members` uncapped → `…IsMinOfRequestAndThree` red (rows 5 and 8); cap the click's grant too → `…ClickGrantIsNotCapped` red.

**Size.** ≈ 700 lines, 7 files. **Cut point:** `reconcileUnattended`, the boot sweep and their tests move to PU-1b3, which then must ship in the same U23 batch. **Deploy.** U23 daemon batch.

**Risks.**
- **`pdx lead request` still says** 「請在 Purdex 介面核准」 before an approval that lands at once (`lead.go:161`). Harmless; not changed.

## PR PU-1c — the switch route, the event, the capability, the safety layer

**Goal.** D-U23-1 (the route the App calls), D-U23-2 (admin only, no `pdx` command, skill forbids, audited and broadcast), D-U23-3 (switch-on sweeps; `pending` reported), D-U23-5 (capability), D-U23-6 (the paged list's route; every window sees the state), D-U23-7 (persisted, not synced).

**Files.**
- Create `internal/module/team/unattended_handler.go` and `unattended_handler_test.go`.
- Modify `internal/module/team/module.go`: routes (`:275-298`), `Start`'s `OnSubscribe` (`:324`).
- Modify `internal/core/info_handler.go:45-49` and `info_handler_test.go:372-379`.
- Modify `cmd/pdx/http_chain_test.go` (a new admin-only test beside `:754-784`).
- Create `cmd/pdx/unattended_guard_test.go`.
- Modify `cmd/pdx/plugin/purdex/skills/pdx-team/SKILL.md` and `cmd/pdx/plugin/embed_test.go:43-77`.

**Interfaces.**
- `const UnattendedRoute = "/api/team/unattended"` (exported for the chain test).
- `GET /api/team/unattended?before=<ms>&limit=<n>` → 200 `UnattendedView`: the state and one page `ListAutoApproved(state.Since, before, limit)` (limit default 50, max 200), `truncated`, `next_before` (the last row's `decided_at` when truncated). A `before` or `limit` that is not a positive integer → 400 `bad_request`. A store error → 500 `storage_error`.
- `PUT /api/team/unattended`, body `UnattendedPutRequest` → 200 `UnattendedView` (first page) with `swept` and `pending`; once the write has taken effect the PUT is 200 even when the list cannot be read, then with `approved: []` and `list_failed: true` (decision 30). 400 `bad_request` when `on` is missing or not a boolean, when `client.kind` or `client.label` is blank, or when `client.kind` is not `"app"`. 503 `not_ready` while stopping. The daemon sets `client.addr = r.RemoteAddr`.
- Event `team.unattended`: `{op:"snapshot", state}` to each new subscriber (`OnSubscribe`), `{op:"changed", state}` after every write that changed something, under `eventMu` like `broadcast`.
- `capabilities` gains `"relay.unattended.v1"` (appended; the order is the contract).

**Behaviour rules.**
1. **One critical section.** Under `createMu`: stopping → 503; `SetUnattended(on, client, now)`; when it changed **to on**, `sweepUnattended("switch on")`. Then the `changed` event and the 200 with `swept` / `pending`. A `pending` row is approved by the sweeper's next tick (PU-1b2 rule 6); the PUT is not failed for it.
2. **Audit.** One log line per change: `[team] unattended on|off by app "<label>" from <addr> (swept N, pending M)`. `changed_by` is stored with the state.
3. **Off changes nothing else.** Open requests stay open; the list stays until the next switch-on (D-U23-6).
4. **Per host, persisted, not synced** (D-U23-7).
5. **The safety layer (D-U23-2) and its limit (deviation 6):**
   - no `pdx` command names the route or the word; the dispatcher (`main.go:45-93`) is unchanged;
   - the mod never calls it (`hooks/*.js`);
   - the skill says, in "When to ask for lead mode" and "Self relay": **"Never turn on 無人值守模式 (unattended mode).** It is the user's switch in Purdex.app: there is no `pdx` command for it, and you must not call the daemon's route or edit host config to get around that.";
   - **not enforceable:** the App and `pdx` share one host token (spec §3.3, §6.5), so a same-uid agent holding it can `curl` the route with a forged `client`. As U5b says of approvals, this is "told and agreed", not a security boundary; the broadcast (every window's title bar turns on, PU-2b) and the audit (`changed_by` with the remote address, the log line) are what remain.

**Tests.**
- `TestUnattendedPut_OnSweepsOpenRequestsAndBroadcastsChanged`: an open lead and an open self_relay → `swept: 2, pending: 0`, both `closed` by unattended, then `team.unattended {op:changed, state.on:true}`.
- `TestUnattendedPut_PendingIsReportedAndTheTickFinishesIt`: a seam fails one approve → 200 `swept: 1, pending: 1`; one `tick()` later the row is approved.
- `TestUnattendedPut_OnTwiceKeepsSinceAndSweepsNothing`.
- `TestUnattendedPut_OffKeepsTheListAndOpenRequests`.
- `TestUnattendedGet_ListsAutoApprovalsSinceTheLastOn`: on → two auto-approvals → off → GET lists both → on again → GET lists none.
- `TestUnattendedGet_PagesWithBeforeAndTruncated`: 5 rows, `limit=2` → pages 2, 2, 1; `truncated` true, true, false; `next_before` chains; `before=abc` → 400.
- `TestUnattendedPut_RequiresAnAppClient`: no `on`, `on:"yes"`, no client, `kind:"unattended"`, `kind:"terminal"` → 400; nothing stored, no event.
- `TestUnattendedPut_AuditLineAndChangedBy`: the stored `changed_by` has the label and `100.64.0.4:51234`.
- `TestUnattendedSnapshot_ToEveryNewSubscriber`.
- `TestUnattended_SurvivesARestart`: the real hostconfig module and two team modules over one data dir.
- `TestHandleInfo_Capabilities` (updated): `[…, "conversations.scope.v1", "relay.unattended.v1"]`.
- `TestNewOuterHandler_UnattendedIsAdminOnly`: recording stubs mounted at `teammod.UnattendedRoute` behind `newOuterHandler` (the pattern of `http_chain_test.go:754-784`); `PUT` and `GET` with the peer host's inbound token, a wrong token and none → 401 and the stub never runs; the admin token → the stub runs.
- `TestPdx_NoCommandOrModCallTurnsUnattendedOn` (`cmd/pdx/unattended_guard_test.go`): every non-test `cmd/pdx/*.go` file and every `cmd/pdx/plugin/purdex/hooks/*.js` is read; none contains `/api/team/unattended`; the command list printed at `main.go:46` has no `unattended`.
- `TestSkill_SaysWhatSpec10Requires` (extended): the skill holds the line "Never turn on 無人值守模式" and the words "no pdx command for it" (the skill writes `pdx` in backticks; the test matches the text around it).

**Mutation gates.**
- Drop the sweep → `…OnSweepsOpenRequests…` red.
- Fail the PUT when `pending > 0` → `…PendingIsReported…` red.
- Accept any client kind → `…RequiresAnAppClient` red.
- Move `UnattendedRoute` under `/api/peers/team/` → `…UnattendedIsAdminOnly` red (the peer token then meets `PeerAuth` and `HostRoutePolicy`'s 403, not 401).
- Add a `pdx unattended` dispatcher case → `…NoCommandOrModCall…` red.

**Size.** ≈ 700 lines, 9 files. **Cut point:** the capability, the guard test and the skill line (`info_handler*.go`, `unattended_guard_test.go`, `SKILL.md`, `embed_test.go`) move to PU-1c2, in the same batch. **Deploy.** U23 daemon batch (daemon + setup for the skill text).

**Acceptance** (mlab, throwaway lead session, App connected): with the switch off, `pdx lead request` opens the dialog; PUT on (API, admin header file) → the dialog closes on every window (with no toast once PU-2b is in); a second `pdx lead request` from another throwaway session exits 0 at once and no dialog or notification appears on any window; `GET /api/team/unattended` lists both; PUT off; restart the daemon; GET still lists them, `on:false`; no flag left in `hooklocks/`.

**Risks.**
- **Mobile API P3 (capabilities)** also edits `info_handler.go:45-49` (spec `2026-10-08-mobile-api-spec.md`, P3). A textual conflict only; both append.

---

# Phase PU-2 — the title-bar button (unattended spec D-U23-5, D-U23-6)

**Facts this phase rests on:**
- **The title bar** is `spa/src/components/TitleBar.tsx` (`TitleBar` at `:104`), mounted once per window at `spa/src/App.tsx:192`, outside `TabContent`; each Electron window has its own renderer (`electron/window-manager.ts:19,81`). The bar is a 36 px drag region (`TitleBar.tsx:157-159`); the layout buttons sit in a `no-drag` wrapper after a `flex-1` spacer (`:175-198`) with the styles `BUTTON` / `PRESSED` / `IDLE` (`:24-26`) and `onMouseDown={keepFocus}` (`spa/src/lib/keep-focus.ts:14`). It has no menu today.
- **Anchored panels:** `FloatingPanel` (`spa/src/components/FloatingPanel.tsx:55`, props `:6-17`: `title`, `anchorRef`, `onClose`, `width`, `placement`, `testId`; `no-drag`, Escape and outside-click close) and `Menu` (`Menu.tsx:63`). `TITLE_BAR_HEIGHT = 36` (`FloatingPanel.tsx:28`).
- **Shown hosts:** no exported hook returns the shown ids; the list pattern is `hostOrder.filter(useShownRefFilter())` (`spa/src/lib/shown-hosts.ts:161-165`, used at `components/hosts/HostSidebar.tsx:37,60`).
- **Every configured host has a host-events socket**, shown or not (`spa/src/hooks/useMultiHostEventWs.ts:85-111`); a new event type is one more `if (event.type === …)` before the agent whitelist (`:219-224` is the approval branch) plus the literal in `spa/src/lib/host-events.ts:4-16`.
- **Connection state:** `useHostStore.runtime[hostId].status` ∈ `connected | disconnected | reconnecting | auth-error` (`spa/src/stores/useHostStore.ts:82-102`), written by `useMultiHostEventWs.ts:146-150, 252, 258-261`.
- **`/api/info`** typed as `fetchHostInfo(hostId): Promise<HostInfo>` (`spa/src/lib/host-api.ts:508-510`); `HostInfo.capabilities?: unknown` (`useHostStore.ts:113`). The pattern of "run once per transition to connected" is `startHostDaemonIdVerification` (`spa/src/lib/host-daemon-id.ts:22-60`, started at `spa/src/main.tsx:43`).
- **Team API errors:** `ApprovalApiError` and a plain-text 404 → `'unsupported'` (`spa/src/lib/team/approval-api.ts:38-59`); `pinnedHostFetch` refuses an unconfigured host (`approval-api.ts:63-77`). The client label is `clientDescriptor()` (`spa/src/lib/team/client-label.ts:32-35`).
- **Toasts:** `useUndoToast.getState().show(message)` (`spa/src/stores/useUndoToast.ts:24`).

## PR PU-2a — the data: support and state per host

**Goal.** Know, per host, whether its daemon supports unattended (D-U23-5 "a daemon without the capability … counts as unsupported") and what its switch says, live on every window (D-U23-6 "on every window").

**Files.**
- Create `spa/src/lib/team/unattended-api.ts` and its test: `getUnattended(hostId, { before?, limit? })`, `putUnattended`. They go through the approval API's transport and error mapping: `send` and `errorFromResponse` (`approval-api.ts:38-77`) become exported from there, unchanged in behaviour.
- Modify `spa/src/lib/team/approval-api.ts` (the export above).
- Create `spa/src/stores/useUnattendedStore.ts` and its test.
- Create `spa/src/lib/team/unattended-ws.ts` and its test (`handleUnattendedEvent`).
- Create `spa/src/lib/team/unattended-support.ts` and its test (`startUnattendedSupport`).
- Modify `spa/src/lib/team/types.ts` (`Client.kind` adds `'unattended'`; `UnattendedState`, `UnattendedView`, `isUnattendedState`).
- Modify `spa/src/lib/host-events.ts:4-16` and `spa/src/hooks/useMultiHostEventWs.ts` (one branch beside `:219-224`).
- Modify `spa/src/main.tsx` (start the support probe beside `:43`).

**Interfaces.**
- `UNATTENDED_CAPABILITY = 'relay.unattended.v1'`.
- `useUnattendedStore`: `byHost: Record<hostId, { support: 'unknown' | 'yes' | 'no'; state?: UnattendedState }>`; `setSupport(hostId, s)`, `applyState(hostId, state)`, `forgetHost(hostId)`. Not persisted, not synced (one per renderer).
- `startUnattendedSupport(): () => void` — on each transition of a host to `connected` (and on a change of its endpoint or token), one `fetchHostInfo`; `capabilities` an array that holds the name → `'yes'`, any other answer → `'no'`; a failed request leaves `'unknown'` until the next transition (no polling). It reuses the newest-answer-only generation guard of `host-daemon-id.ts:30-49`.
- `handleUnattendedEvent(hostId, value)` — validates `{op: 'snapshot' | 'changed', state}` whole (`isUnattendedState`: `on` a boolean, `since` and `changed_at` finite numbers, `changed_by` absent or a record); a bad frame is dropped with one `console.warn`; a good one → `applyState`, and proves support: `setSupport(hostId, 'yes')`.

**Behaviour rules.**
1. **State only from the daemon.** The store never writes host config and never guesses.
2. **Every window follows:** each renderer has its own sockets and store; a `changed` event reaches all of them.
3. **Not tab-hosted.** Nothing mounts inside a tab; the store is module scope.

**Tests.**
- `unattended-api.test.ts`: GET (with and without `before`/`limit`) and PUT bodies; the client descriptor is sent; a plain 404 → `'unsupported'`; a 400's detail surfaces.
- `useUnattendedStore.test.ts`: apply, replace, forget.
- `unattended-ws.test.ts`: snapshot and changed apply; a frame with `on: "true"`, a missing `since`, or `op: 'x'` is dropped whole; a good frame sets support `'yes'`.
- `unattended-support.test.ts`: connected → one `/api/info`; the capability present → `'yes'`, absent → `'no'`, `capabilities` not an array → `'no'`; a stale answer after a re-point is dropped; no second call without a new transition.
- `approval-api.test.ts`, unchanged, is the gate for the export.

**Mutation gates.**
- Accept a frame without validating `on` → the malformed-frame case red.
- Read a missing `capabilities` as `'yes'` → the not-an-array case red.

**Size.** ≈ 520 lines, 13 files. **Cut point:** `unattended-support.ts` + its test + `main.tsx` move to PU-2a2. **Deploy.** SPA (nothing renders it yet).

**Risks.** #1866 PR2b also adds branches to `useMultiHostEventWs.ts` and the `host-events.ts` union (design `docs/specs/2026-10-08-worker-status-deltas-design.md:258`): textual conflicts only.

## PR PU-2b — the aggregate, the fan-out, the title-bar toggle

**Goal.** D-U23-5 (one button for every shown host: off / on / partial, the tooltip, the press semantics, a host that comes back is not changed) and D-U23-6 first two bullets (visibly on in every window with the label 無人值守中; no toast for what the daemon approved — decision on Open question 1).

**Files.**
- Create `spa/src/lib/team/unattended-aggregate.ts` and its test.
- Create `spa/src/lib/team/unattended-toggle.ts` and its test.
- Create `spa/src/components/UnattendedButton.tsx` and `UnattendedButton.test.tsx`.
- Modify `spa/src/components/TitleBar.tsx:175-176` (render `<UnattendedButton />` between the spacer and the layout buttons) and its test.
- Modify `spa/src/lib/team/approval-ws.ts:104-105` and `approval-ws.test.ts`.
- Modify `spa/src/locales/en.json`, `zh-TW.json`.

**Interfaces.**
- `aggregateUnattended(shownHostIds, runtime, byHost) → { mode: 'off' | 'on' | 'partial' | 'none'; on: string[]; off: string[]; unreachable: string[]; unsupported: string[]; reachable: string[] }`:
  - unreachable = not `connected`, or `support === 'unknown'`, or no `state` yet;
  - unsupported = `support === 'no'`;
  - reachable = connected, supported, with a state;
  - `none` = no shown host; `on` = every shown host reachable and on; `off` = every shown host reachable and off; anything else `partial` (D-U23-5: mixed, unreachable, or too old — an unreachable host may still be on and approving).
- `toggleUnattended(agg) → Promise<{ target: boolean; failed: Array<{ hostId; code }> }>`: `target = agg.mode !== 'on'` (off, partial → on; on → off); `putUnattended(hostId, target)` for every **reachable** host in parallel (a 200 is success whatever its `list_failed`: only the state is read from it, never `approved` — the panel always GETs its list, PU-2c); unreachable and unsupported hosts are never written; the caller toasts `unattended.toast.failed` once, naming every failed host.
- `UnattendedButton`, in a `no-drag` wrapper with `onMouseDown={keepFocus}`. The shown hosts are `hostOrder.filter(useShownRefFilter())`.
  - The toggle `data-testid="unattended-toggle"`, `aria-pressed={mode === 'on'}`. Off: Phosphor `MoonStars` in `IDLE`. On: `PRESSED` plus the text `t('unattended.on_label')` = 「無人值守中」. Partial: `MoonStars` with a warning-coloured ring and `data-state="partial"`. `none`: disabled.
  - Its `title` is the tooltip: one line for off and on; for partial, the hosts by group — 「未開啟：…」「無法連線：…」「daemon 版本過舊：…」 (labels via `hostLabel(hostId, hostLookOf(hostId))`).
- `approval-ws.ts`: a `closed` whose `decided_by.kind === 'unattended'` still goes to the store (the dialog closes) but is **not toasted**.
- Locale keys (`unattended.*`, both files): `button_off`, `button_on`, `button_partial`, `on_label`, `tooltip.off`, `tooltip.on`, `tooltip.partial_off`, `tooltip.partial_unreachable`, `tooltip.partial_unsupported`, `tooltip.none`, `toast.failed`.

**Behaviour rules.**
1. **Press semantics** (D-U23-5): from off or partial → on for every reachable shown host; from on → off for every reachable shown host. A host that comes back later is **not** changed: the button reads partial until the next press.
2. **Hidden hosts never count and are never written** (D-U23-5 "every host shown on the current workbench").
3. **Not tab-hosted:** mounted in `TitleBar` (`App.tsx:192`); everything it shows is in the store.

**Tests** (real `TitleBar` + `UnattendedButton` + stores; the API mocked at `unattended-api`):
- `unattended-aggregate.test.ts`: table — all on; all off; one off; one disconnected with the rest off (**partial**); one unsupported; no shown host (`none`); a host not shown that is on does not count.
- `unattended-toggle.test.ts`: the target rules; parallel PUTs; failures collected.
- `UnattendedButton.test.tsx`:
  - `off: no accent, no label; pressing PUTs on to every reachable shown host and nothing to the unreachable or unsupported ones`;
  - `on: accent and 無人值守中; pressing PUTs off`;
  - `partial: the ring and data-state; the tooltip names the off, unreachable and unsupported hosts; pressing turns the reachable ones on`;
  - `a hidden host is neither counted nor written`;
  - `a changed event (another window's press) turns the button on without a press here`;
  - `none: disabled`;
  - `a failed PUT toasts once naming the host`.
- `TitleBar.test.tsx`: `the unattended button sits in a no-drag wrapper before the layout buttons`; `TitleBar remounted keeps the button's state`.
- `approval-ws.test.ts`: `a closed decided by unattended closes the dialog and shows no toast`; `a closed decided by an app still toasts`.
- `locale-completeness.test.ts`, unchanged, covers the keys.

**Mutation gates.**
- Treat `support: 'unknown'` as reachable → the disconnected / unknown aggregate rows red.
- Target = `mode === 'off'` (partial would turn off) → the partial-press test red.
- Write to unreachable hosts too → the off-press test red.
- Toast every close → the unattended toast test red.

**Size.** ≈ 700 lines, 12 files. **Cut point:** the `approval-ws.ts` toast rule and `unattended-toggle.ts` move to PU-2b2. **Deploy.** SPA.

**Risks.**
- **U1 (interface unification) may restyle the title bar.** Agree the slot with `mlab/_3fj93m` before this PR starts.

## PR PU-2c — the "while you were away" panel

**Goal.** D-U23-6 third bullet: opening the button's menu lists auto-approvals since the switch last turned on (host, session, kind, time), read page by page from each daemon's audit (PU-1c's GET), with nothing silently cut (decision 17).

**Files.**
- Create `spa/src/components/UnattendedPanel.tsx` and `UnattendedPanel.test.tsx`.
- Modify `spa/src/components/UnattendedButton.tsx` (the ▾ button and the panel) and `UnattendedButton.test.tsx`.
- Modify `spa/src/locales/en.json`, `zh-TW.json`.

**Interfaces.**
- A second button `data-testid="unattended-list"` (Phosphor `CaretDown`) opens `UnattendedPanel` anchored on the pair.
- `UnattendedPanel` (`FloatingPanel`, `placement="below"`, width 360): on open, `getUnattended(hostId)` (first page) for every reachable shown host; rows merged newest first: `<host>：<session> · <kind> · <time>` (`approvalSessionLabel`, `approvalKindLabel`, local `HH:mm`); empty → `unattended.panel.empty`; a host whose GET failed → one line naming it.
- **「顯示更多」** (`data-testid="unattended-more"`): shown while any host answered `truncated`; a click fetches the next page of each such host (`before = next_before`) and merges it in order. It fetches afresh each time the panel opens.
- Locale keys: `unattended.list`, `panel.title`, `panel.empty`, `panel.since`, `panel.more`, `panel.host_failed`.

**Behaviour rules.**
1. **The panel opens only on a click of ▾** (decision 19): turning the switch off opens nothing.
2. **Nothing is cut silently:** a host with more rows keeps 「顯示更多」 until its last page.
3. **Not tab-hosted:** the open / closed flag and the loaded pages are the component's own state (closing on an unmount is right); every row is fetched.

**Tests.**
- `UnattendedPanel.test.tsx`: `merges two hosts newest first`; `empty state`; `a host whose GET failed is named`; `kind labels for lead and self_relay` (PL-2a adds `adopt`); `顯示更多 fetches the next page of the truncated host only and merges it in order`; `the button goes when no host is truncated`.
- `UnattendedButton.test.tsx`: `▾ opens the panel and it fetches`; `switching off opens nothing`.

**Mutation gates.**
- Fetch once per mount instead of per open → the re-open case red (stale rows).
- Fetch the next page of every host, not only the truncated ones → the truncated-only case red.

**Size.** ≈ 440 lines, 6 files. **Cut point:** 「顯示更多」 and its two tests move to PU-2c2. **Deploy.** SPA.

**Acceptance** (mlab App with two windows, two throwaway sessions): press on → both windows show 無人值守中; a self relay at a test threshold (`PDX_RELAY_THRESHOLD`) relays with no dialog and no notification; a host whose daemon is down shows partial with its name in the tooltip; press off → nothing opens; ▾ lists the relay.

## PR PU-2d — U25: the lead dialog's member limit

**Goal.** The dialog half of U25 / D-U24-7 (decision 22): the lead dialog's member-limit field is always prefilled with 3, not with the lead's requested number; when the request is not 3, 「lead 申請 N 個」 stands beside the field; the range stays 1–8. (The daemon half, the unattended grant, is PU-1b2.)

**Facts.** The field's state is `useState(String(payload.max_members))` in `OpenApprovalDialog` (`spa/src/components/ApprovalDialogHost.tsx:74`), where `payload = leadPayloadOf(approval)` normalises an unspecified request to 3 (`spa/src/lib/team/types.ts:217-225`); the field is rendered at `ApprovalDialogHost.tsx:302-318` with its range check at `:175-176` (`MAX_MAX_MEMBERS = 8`, `types.ts:22`); the approve sends `{max_members, roots}` from the fields (`:187`). The default constant is `DEFAULT_MAX_MEMBERS = 3` (`types.ts:21`).

**Files.**
- Modify `spa/src/components/ApprovalDialogHost.tsx` (`:74`: `useState(String(DEFAULT_MAX_MEMBERS))`; the note beside the field).
- Create `spa/src/components/ApprovalDialogHost.memberLimit.test.tsx`.
- Modify `spa/src/locales/en.json`, `zh-TW.json` (`approval.dialog.max_members_requested`: 「lead 申請 {{n}} 個」 / `lead asked for {{n}}`).

**Interfaces.** The note is `<span data-testid="approval-max-members-requested">` after the input, rendered only when `payload.max_members !== DEFAULT_MAX_MEMBERS`.

**Behaviour rules.**
1. **Prefill 3, always** (D-U24-7), whatever the request says; the person may still type 1–8.
2. **The note names the request** only when it differs from 3.
3. **Self relay and adopt cards are untouched** (they have no member field).
4. **Not tab-hosted:** the dialog host is app-level (P9b-2); the field's edits keep surviving minimize / restore (the existing `ApprovalDialogHost.minimize.test.tsx` case stays green).

**Tests** (`ApprovalDialogHost.memberLimit.test.tsx`, the real dialog host and store):
- `a request of 5 prefills 3 and shows 「lead 申請 5 個」`;
- `a request of 3 (or none) prefills 3 with no note`;
- `a request of 1 prefills 3 and shows 「lead 申請 1 個」`;
- `approving untouched sends max_members 3`; `typing 5 sends 5`; `0 and 9 still show the range error`.
- The existing `ApprovalDialogHost.test.tsx` case that edits the field to 5 (`:129`) stays green; any case that asserted the prefill equals the payload is updated to 3.
- `locale-completeness.test.ts`.

**Mutation gates.**
- Prefill from `payload.max_members` → `a request of 5 prefills 3…` red.
- Show the note when the request is 3 → `…no note` red.

**Size.** ≈ 160 lines, 4 files. **Cut point:** none needed. **Deploy.** SPA.

---

# Phase PL-1 — adopt, release, the roster (adopt spec D-U24-1…4; D-U24-5 data)

**Facts this phase rests on:**
- **The create route is one switch on `kind`.** `internal/module/team/handler.go:119-128` accepts `lead`, refuses `self_relay` (it opens through `/api/relay/begin`) and anything else (`"kind must be lead"`). `CreateApprovalRequest` has `ID, Kind, OriginInbox, Reason, MaxMembers, Roots, WaitS` (`internal/team/wire.go:104-113`); reason is required for `lead` (`handler.go:133-137`).
- **`approval_requests` has no reason column for a close** (`internal/module/team/store.go:49-67`); `scanRow` reads `selectCols` (`:93-94, 106-141`).
- **`team_members`' primary key is `spawn_op`** (`internal/module/team/team_store.go:46-67`), with `team_members_one_active ON team_members (session_id) WHERE state = 'active'` (`:67`). `validMemberState` knows `active | killed | gone` (`:128-134`); `MemberState` likewise (`internal/team/wire_team.go:32-39`). A spawned member is stored by the runner (`internal/module/team/spawn_register.go:144-147`).
- **An ended team leaves its members `active`.** `EndTeam` touches `teams` only (`team_store.go:379-393`, D4), and the sweeper marks only members of live teams gone (`sweeper.go:212-237`, `team_store_members.go:66-70`). So a session that was a member of an ended team still has an `active` row, and a second `active` row for it violates `team_members_one_active`.
- **Roles are read live.** `relayRole` = lead of a live team, else an active member of a live team, else none (`relay_handler.go:44-56`); `selfRelayState` answers `off` for a member and the host switch then the pause for the others (`:62-89`).
- **The team limit counts team.db alone:** running spawn ops (this one excepted) + `active` member rows ≥ `max_members` (`internal/module/team/spawn_store.go:138-175`).
- **Kill ends the member's tmux session by its spawn tag.** `killMember` reads the session's identity and refuses when `id.Tag != mr.SpawnOp` (`team_handler.go:204-224`, the tag check at `:217`); `killAndMark` refuses a member mid-relay (`claimed|writing|written`, `:158-162`) and marks with `MarkMemberKilled`'s CAS (`team_store_members.go:104-111`). An adopted session's tmux session has no `@pdx_spawn_op` tag (the option is set only by the spawn's own tagged create, `internal/module/team/spawn_tmux.go:23`), so today's kill would refuse it with 409 `not_your_member` — and killing it would end a tmux session the user made.
- **Process identity.** A live registry entry has its inbox socket present, its pid alive and its start time equal to its `procStart` at read (`internal/peers/registry.go:292-400`). `LeadPresence` re-verifies a recorded pid the same way — `PidAlive`, then the process start time against `procStart` (`internal/module/peers/origin_resolver.go:159-176`, `startTime` `:181-193`). `ResolveOriginBySession` answers the first live entry with the session id (`:51-67`).
- **Targets are parsed by `parseKillTarget`** (`_xxxxxx`, `xxxxxx`, `<host>/…` with this host's alias or id, `<name> [xxxxxx]`; `team_handler.go:253-281`), matched over the team's members by current ref, then lineage (`store.PreviousRefs()`), then live name (`:232-323`).
- **The origin resolver** offers `ResolveOrigin(inbox)`, `ResolveOriginBySession(sid)`, `LiveSession`, `LeadPresence` (`internal/module/team/module.go:31-43`; implementation `internal/module/peers/origin_resolver.go:29-67, 141-215`). Registry entries carry `Inbox` and `Tmux` (`internal/peers/registry.go:50-62`). There is one fake, `fakeOrigins` (`internal/module/team/handler_test.go:54-160`).
- **The daemon has no in-process send.** `handleSend` is admin-only by principal (`internal/module/peers/send.go:197-206`) and attributes the sender by `origin_inbox` (`:236-302`); `SendRequest{To, Text, Mode, OriginInbox}` (`internal/peers/wire.go:290-295`). Plan v3 P6-1 (`plan-v3.md:1188-1240`) designed `peers.Sender`, running `handleSend` in process with `middleware.WithPrincipal(ctx, Principal{Kind: PrincipalAdmin})` (`internal/middleware/peer_auth.go:44`); it moves here (Contracts for later PRs 2).
- **The hook lock is for lead requests only:** `openLeadAndRemoveStaleFlag` and the prune guard look up `KindLead` (`internal/module/team/hooks.go:87-100, 161-170`); spec §6.6 names only two flag writers. An `adopt` request therefore locks nothing.
- **The CLI's team commands** share `teamSetup` / `teamReportErr` (code last on stderr; `cmd/pdx/team_cmd.go:128-168`) and `teamRefusalCodes` (`:72-78`); `pdx lead request`'s create-then-poll loop is `cmd/pdx/lead.go:132-228`.
- **The skill and its test:** `cmd/pdx/plugin/purdex/skills/pdx-team/SKILL.md:10-35`; `TestSkill_SaysWhatSpec10Requires` (`cmd/pdx/plugin/embed_test.go:43-77`).
- **The mod caches the role.** `hello` sets `s.role` (`register.js:175-189`); `maybeBegin` returns early for `s.role === 'member'` (`:380-381`); a 409 `member_relay_is_leads` sets it (`:422`). Hello is re-sent only at `session.start`, after a `/clear`, or after a failed hello (`:637, 662, 686, 720`).

## PR PL-1a — wire contract: adopt, release, origins, close reasons

**Goal.** Fix the U24 contract (D-U24-2, D-U24-3) before the store and routes build on it. It starts only after P9 is complete and U23 (PU-2c) has merged (adopt spec §4 "after P9 and U23"; decision 20).

**Files.**
- Create `internal/team/wire_adopt.go` and `wire_adopt_test.go`.
- Modify `internal/team/wire.go` (`KindAdopt`; `Approval.CloseReason`; `CreateApprovalRequest.Target`).
- Modify `internal/team/wire_team.go` (`MemberReleased`; `Member.Origin`, `Member.EndedAt`, `Member.AdoptRequest`).
- Modify `internal/team/wire_unattended.go` (`AutoApprovable` adds `adopt`).

**Interfaces.**

```go
const KindAdopt Kind = "adopt"
// Approval: CloseReason string `json:"close_reason,omitempty"` — a cancel's code (adopt re-checks)
// CreateApprovalRequest: Target string `json:"target,omitempty"` — adopt only: the target as `pdx adopt` takes it
const ( // 409, exit 13
	ErrAdoptSelf           = "adopt_self"
	ErrAdoptTargetIsLead   = "adopt_target_is_lead"
	ErrAdoptAlreadyMember  = "adopt_already_member"
	ErrAdoptTargetNotFound = "adopt_target_not_found"
	ErrRemoteUnsupported   = "remote_unsupported" // plan v3 P4b-4 reuses it (Contracts for later PRs 3)
)
type AdoptPayload struct { // Approval.Payload for KindAdopt
	TeamID          string `json:"team_id"`
	LeadSessionID   string `json:"lead_session_id"`
	TargetRef       string `json:"target_ref"`        // the target's current ref, "_xxxxxx"
	TargetSessionID string `json:"target_session_id"`
	Title           string `json:"title,omitempty"`   // the target's title
	TargetName      string `json:"target_name,omitempty"`
	TargetAddress   string `json:"target_address,omitempty"`
	TargetCwd       string `json:"target_cwd,omitempty"`
	TargetTmux      string `json:"target_tmux,omitempty"` // "<session>:@<win>.%<pane>"
}
const (
	MemberReleased MemberState = "released"
	MemberOriginSpawned = "spawned"; MemberOriginAdopted = "adopted"
	NoticeAdopted = "adopted"; NoticeReleased = "released"; NoticeGiveUpS = 600 // the outbox (PL-1d1)
	AdoptNoticeFmt   = "[pdx team] 你已成為 %s 的 member（team %s）。自我接力已關閉，接力由 lead 安排；回報請送 %s。"
	ReleaseNoticeFmt = "[pdx team] %s 已讓你離開 team %s：你現在是一般 session，自我接力依這台主機的設定。"
)
type ReleaseRequest = KillRequest // POST /api/team/release: {origin_inbox, target}
// Member: Origin string `json:"origin"` ("spawned" | "adopted"); EndedAt int64 `json:"ended_at,omitempty"`;
//         AdoptRequest string `json:"adopt_request,omitempty"` (adopted only; SpawnOp is "" then)
func AdoptPayloadOf(a Approval) (AdoptPayload, error) // strict decode for the daemon
```

**Behaviour rules.**
1. JSON names are snake_case as listed; every new field on an existing type is `omitempty` except `Member.Origin` (always present: an older daemon's view lacks it, which a client reads as `spawned`).
2. `AutoApprovable(KindAdopt)` is true (U24: "U23 開著時自動通過").

**Tests.**
- `TestWireAdopt_LiteralsArePinned`: kind, five codes, `released`, both origins, both notice kinds, the give-up bound, both notice formats.
- `TestWireAdopt_JSONShapes`: `AdoptPayload` full and minimal; `Approval` with and without `close_reason`; `Member` adopted (`spawn_op:""`, `adopt_request`, `origin:"adopted"`) and spawned.
- `TestAutoApprovable_Adopt`.

**Mutation gates.**
- `close_reason` without `omitempty` → the minimal `Approval` encoding red.
- `AutoApprovable` without `adopt` → `TestAutoApprovable_Adopt` red.

**Size.** ≈ 440 lines, 5 files. **Cut point:** `wire_team.go`'s member fields move to PL-1a2. **Deploy.** None.

## PR PL-1b — team.db: adopt and release in the store

**Goal.** D-U24-2 "On approve: in one transaction the daemon re-checks every refusal above … and inserts the `team_members` row (origin `adopted`, no spawn op …) … If a re-check fails, the request closes as `cancelled` with that code" (decision 4: every code, one by one); D-U24-3 `state=released`, `ended_at` set; the notice outbox fields (decision 7).

**Files.**
- Modify `internal/module/team/migrate.go` (`migrateAdopt`) and `migrate_test.go`.
- Modify `internal/module/team/store.go` (`selectCols`, `scanRow`, `closeRowIn` write `close_reason`; `Close.Reason`).
- Modify `internal/module/team/team_store.go` (`memberCols` and `dest` add `origin`, `ended_at`, `notice_pending`, `notice_since`; `validMemberState`; `adoptApprovedIn`, `CloseAdoptApproved`; the create reads) and `team_store_test.go`.
- Modify `internal/module/team/team_store_members.go` (`ReleaseMember`; `MarkMemberKilled` / `MarkMemberGone` set `ended_at`; `PendingNotices`, `ClearNotice`).
- Modify `internal/module/team/spawn_register.go:144-147` (`Origin: spawned`).
- Create `internal/module/team/adopt_store_test.go`.

**Interfaces.**
- `Close` gains `Reason string`, written to `close_reason` by `closeRowIn` (empty for every existing caller).
- `type adoptCheck struct{ HostID string; TargetLive bool }` — what only the caller can say, read just before the transaction: this daemon's host id and the registry's answer for the target.
- `func adoptApprovedIn(tx *sql.Tx, id string, c Close, p team.AdoptPayload, chk adoptCheck, m memberRow) (n int64, refused string, err error)` — the approve of an `adopt` row on the caller's transaction (decide through `CloseAdoptApproved`, create-time through `CreateApproved`, PU-1b1):
  1. the write lock first (`UPDATE approval_requests SET id = id WHERE id = ?`, as `team_store.go:191`);
  2. **every refusal of D-U24-2 again, in the create order, each answering its code:** the team `p.TeamID` is live and led by `p.LeadSessionID` (`not_lead`); the row's `host_id == chk.HostID` — the target was resolved on this host (`remote_unsupported`); `chk.TargetLive` (`adopt_target_not_found`); `p.TargetSessionID != p.LeadSessionID` (`adopt_self`); the target leads no live team (`adopt_target_is_lead`); the target is no active member of a live team (`adopt_already_member`); no **other** open `adopt` row for `p.TargetSessionID` (`request_open` — the create invariant, asserted); running spawns + active members < `max_members` (`team_full`, the query of `spawn_store.go:156-159` without the excepted op);
  3. a refusal: `closeRowIn(cancelled, Reason=code)`, `refused = code` (the create-time caller rolls back instead, PL-1c rule 3);
  4. otherwise: an `active` row of the target in an **ended** team becomes `released` with `ended_at`; `closeRowIn(c)`; insert `m` (`spawn_op` = the request id, `origin = adopted`, `state = active`, **`notice_pending = adopted`, `notice_since = now`**).
- `func (s *Store) CloseAdoptApproved(id string, c Close, p team.AdoptPayload, chk adoptCheck, m memberRow) (a team.Approval, won bool, refused string, err error)` — `adoptApprovedIn` in its own transaction.
- `func (s *Store) ReleaseMember(rowKey, sessionID string, at int64) (released bool, err error)`:
  `UPDATE team_members SET state='released', ended_at=?, updated_at=?, notice_pending='released', notice_since=? WHERE spawn_op=? AND session_id=? AND state='active' AND NOT EXISTS (SELECT 1 FROM relay_ops WHERE session_id=? AND state NOT IN ('done','failed','cancelled'))`.
- `MarkMemberKilled` and `MarkMemberGone` also set `ended_at = at` (same statements, `team_store_members.go:104-128`).
- Reads for PL-1c's create: `OpenAdoptForTarget(targetSessionID) (team.Approval, bool, error)` (`kind = 'adopt' AND state = 'open' AND json_extract(payload_json, '$.target_session_id') = ?`) and `SeatsUsed(teamID) (used, limit int, err error)` (step 2's count outside a transaction).
- The outbox (used by PL-1d1): `PendingNotices() ([]memberRow, error)` (rows with `notice_pending <> ''`, oldest `notice_since` first); `ClearNotice(rowKey, kind string, since int64) (bool, error)` (conditional on both, so a newer notice is never cleared by an older send).

**Behaviour rules.**
1. **Migration is additive.** Old rows read `close_reason = ''`, `origin = 'spawned'`, `ended_at = 0`, `notice_pending = ''`; an older daemon ignores the columns (its `INSERT`s name their columns and every new column has a default).
2. **A `released` member is no member:** `ActiveMemberInLiveTeam` and every `state = 'active'` query already exclude it, so `relayRole` answers none, and the `cleared` member move (`relay_store_report.go:93-95`) does not move it.
3. **A released row may be followed by a new adoption** of the same session: a new row keyed by the new request id; the old row stays `released`.
4. **The registry is the one thing outside the transaction** (decision 4): `chk.TargetLive` is read just before; a target that dies between that read and the commit becomes a member whose process is gone, and the existing liveness tick marks it `gone` (`markGoneMembers`, `sweeper.go:212-237`, with the row's pid and start time) within ten seconds.

**Tests.**
- `TestMigrateAdopt_AddsColumnsOnceKeepsData`.
- `TestCloseAdoptApproved_InsertsTheAdoptedMemberInOneTx`: row approved; member row with `origin adopted`, `spawn_op = id`, the target's pid / proc start / pane, `notice_pending adopted`; `ActiveMemberInLiveTeam(target)` true.
- `TestCloseAdoptApproved_EachRefusalCancelsWithItsCode`: one sub-test per code — `not_lead`, `remote_unsupported`, `adopt_target_not_found`, `adopt_self`, `adopt_target_is_lead`, `adopt_already_member`, `request_open` (a second open row inserted straight into the DB), `team_full` — each set up after create; the row `cancelled` with `close_reason`; no member row.
- `TestCloseAdoptApproved_RetiresAStaleRowOfAnEndedTeam`.
- `TestCloseAdoptApproved_LostCASWritesNothing`.
- `TestCloseAdoptApproved_TwoLeadsOneTarget`: both approved → the first inserts, the second cancels `adopt_already_member`.
- `TestReleaseMember_SetsReleasedEndedAtAndTheNotice`.
- `TestReleaseMember_RefusedWhileARelayOpIsOpen`: ops in `awaiting_approval`, `requested`, `claimed`, `written` each block; `done` does not.
- `TestClearNotice_IsConditionalOnKindAndSince`.
- `TestRelayRole_ReleasedMemberIsNone`.
- `TestMarkMemberKilledAndGone_SetEndedAt`.

**Mutation gates.**
- Insert the member after the commit → `…EachRefusalCancelsWithItsCode` red (a member row for a cancelled request).
- Drop the `remote_unsupported` or `request_open` re-check → its sub-test red.
- Drop the ended-team retirement → `…RetiresAStaleRow…` red (unique index violation).
- Guard release on `claimed|writing|written` only → the `awaiting_approval` / `requested` cases red.

**Size.** ≈ 780 lines, 8 files. **Cut point:** `ReleaseMember`, the outbox reads and `ended_at` on kill / gone (with their tests) move to PL-1b2, which then joins batch A. **Deploy.** U24 batch A (columns only; nothing writes `adopt` before PL-1c).

**Risks.**
- **Rollback.** A daemon from before PL-1b reading a team.db with adopted rows: `pdx kill` of an adopted member answers 409 `not_your_member` (no spawn tag) — safe; `pdx team` shows the rows. No migration down is needed.

## PR PL-1c — adopt: the route, decide, create-time approval, the winner hook

**Goal.** D-U24-2 in full on the daemon: `adopt` requests created by a lead, refused before opening with the spec's codes, decided like `lead`, covered by U23 at create, switch-on, every tick and boot — and every approved close's side effects on one winner hook (decision 2).

**Files.**
- Create `internal/module/team/adopt_handler.go` and `adopt_handler_test.go`.
- Modify `internal/module/team/handler.go:119-128` (the kind switch hands `adopt` to `handleCreateAdopt`).
- Modify `internal/module/team/unattended.go` (`approve` gains the `adopt` branch; `afterApproved` gains the adopt case).
- Modify `internal/module/team/module.go:31-43` (`OriginResolver` gains two methods; field `noticeKick`).
- Modify `internal/module/peers/origin_resolver.go` and `origin_resolver_session_test.go`.
- Modify `internal/module/team/handler_test.go` (`fakeOrigins` gains the two methods).
- Modify `internal/module/team/sweeper.go` (`reconcileUnattended` already covers `adopt` through `AutoApprovable`; the test only).

**Interfaces.**
- `OriginResolver` gains:
  - `ResolveOriginByRef(ref string) (team.Origin, bool, error)` — the live, non-proxy entry with `RefID(SessionID) == ref` (the contract of `ResolveOriginBySession`);
  - `InboxOf(sessionID string) (string, bool, error)` — that session's live entry's `Inbox` (PL-1d1).
- `POST /api/team/approvals` with `kind: "adopt"`: body `{id, kind, origin_inbox, target, wait_s}` → **201** the row (open, or approved when unattended is on), 200 on an idempotent replay. Errors: 400 `bad_request` (id, `target` empty or not a ref form, negative `wait_s`); 400 `origin_unknown`; 409 `not_lead`, `remote_unsupported`, `adopt_target_not_found`, `adopt_self`, `adopt_target_is_lead`, `adopt_already_member`, `request_open` (carrying the open row), `team_full`; 503.
- `POST /api/team/approvals/{id}/decide` on an `adopt` row: approve → `approve()` → `CloseAdoptApproved`; a refusal answers **409 `<code>`** after its commit, with the closed row and the `closed` broadcast (as `member_relay_is_leads`, `handler.go:449-455`). Deny / timeout / cancel / abandon are today's closes.
- `afterApproved(a)` for `adopt`: `m.noticeKick()` — a non-blocking signal the PL-1d1 drain listens to (a no-op channel until then).

**Behaviour rules.**
1. **Target forms** (Open question 12): `_xxxxxx`, `xxxxxx`, `<host>/_xxxxxx`, `<host>/<name> [xxxxxx]` — the ref decides; the bracket form's name is display only. `<host>` must be this host's alias or id (`ipeers.HostMatches`), else **409 `remote_unsupported`** (D-U24-2 v1 same-host). A bare name is 400 (`pdx adopt` refuses it first with exit 2).
2. **Resolution:** `ResolveOriginByRef(ref)`; else the lineage tier — a live session whose `PreviousRefs()` holds `ref` (`relay_store_lineage.go`); else 409 `adopt_target_not_found`.
3. **Create** (under `createMu`): origin resolves (400 / 503) → idempotent replay by id (`getRow`, as `handler.go:183-196`) → `not_lead` → target (rules 1–2) → `adopt_self` → `adopt_target_is_lead` → `adopt_already_member` → `OpenAdoptForTarget` → `request_open` → `SeatsUsed` → `team_full` → **switch read (under `createMu`):** off → insert open (`deadline = now + wait_s`, 540 default, cap 600; `lease = now + 30 s`), `opened`, 201; **on → `CreateApproved(row, hash, adoptApprovedIn(… UnattendedClient …))`**: the insert, every re-check and the member row in one transaction, first committed state `approved`; then `announceClosed` → 201. A refusal inside that transaction rolls everything back and answers that code's 409 (no row).
4. **Payload** = `AdoptPayload` from the lead's team and the target's resolved `Origin`; `requestHash(kind, origin sid, wait_s, payload)` (`handler.go:92-97`), so a replay with another target is `id_conflict`.
5. **Approve** (click, sweep, tick, boot) reads `adoptCheck` just before the transaction — `HostID = m.hostID()`, `TargetLive` from `ResolveOriginBySession(p.TargetSessionID)` (a read error → 503 for decide, "not approved, try next tick" for the daemon) — builds the member row from that live origin (pid, proc start, cwd, title, tmux name and pane, ref) and calls `CloseAdoptApproved`.
6. **One winner point** (decision 2): every won `adopt` approval — decide, create-time, `sweepUnattended`, `reconcileUnattended`, boot — ends in `announceClosed` → `afterApproved`; nothing adopt-specific runs on a route.
7. **The lease, the deadline and the origin-gone abandonment** are today's sweeper rules (`sweeper.go:75-96`); the origin is the lead.
8. **No hook lock** for `adopt` (spec §6.6 names only the lead request and the relay as flag writers).

**Tests.**
- `TestAdoptCreate_RefusalsBeforeOpening`: table — not a lead, another host (`remote_unsupported`), unknown ref, self, a lead target, a member target, a second open adopt of that target (`request_open` carrying it), a full team; no row and no event for any.
- `TestAdoptCreate_ByOldRefThroughTheLineage`.
- `TestAdoptCreate_OpensWithThePayloadAndBroadcastsOpened`.
- `TestAdoptCreate_ReplayIsIdempotentOtherTargetConflicts`.
- `TestAdoptCreate_UnattendedIsApprovedNeverOpen`: the `afterApprovedInsert` seam sees no open row; member row; one `closed`, no `opened`.
- `TestAdoptCreate_UnattendedRefusalInTheTransactionIs409`.
- `TestAdoptDecide_ApproveInsertsTheMemberAndRoleIsMember`: then `POST /api/relay/hello` for the target answers `role:"member", self_relay:"off"` (spec PL-1 test) and its `begin` is 409 `member_relay_is_leads`.
- `TestAdoptDecide_EachRefusalIs409WithItsCode`: the eight codes through the route (the store's table, PL-1b, proves the transaction; this proves the mapping).
- `TestAdoptDecide_TargetDiesAfterCommitIsMarkedGone`: the target's registry entry disappears right after the commit → the next liveness `tick()` (after the boot grace) marks the member `gone` (decision 4's boundary).
- `TestAdoptDecide_DenyChangesNothingForTheTarget`.
- `TestAfterApproved_RunsOnEveryApprovePath`: a counting `noticeKick` seam; one adopt approved by a click, one at create-time, one by the switch-on sweep, one by a tick, one at boot → five kicks, one per approval.
- `TestAdopt_NoHookLockWhileOpen`: `POST /api/hooks/decide` for the lead's session answers `{}`.
- `TestOriginResolver_ResolveOriginByRefAndInboxOf` (peers): live, dead, proxy, unknown.

**Mutation gates.**
- Kick the notice from `handleCreateAdopt` instead of `afterApproved` → `…RunsOnEveryApprovePath` red (the decide, sweep, tick and boot paths miss it).
- Answer a remote host as `adopt_target_not_found` → the `remote_unsupported` row red.
- Drop the per-target `request_open` → its row red.
- Commit an open row before the unattended approve → `…UnattendedIsApprovedNeverOpen` red.

**Size.** ≈ 740 lines, 9 files. **Cut point:** the resolver methods, the lineage tier and `TestOriginResolver_*` move to PL-1c0, merged first. **Deploy.** U24 batch A — **only after PL-2a has merged and the main checkout is fast-forwarded** (an older SPA drops `adopt` rows, `approval-ws.ts:43,52`).

**Risks.**
- **Virtual peer names (peer mailbox spec, `docs/specs/2026-10-08-peer-mailbox-integration-spec.md` §3).** A name changes meaning there; refs do not. Rule 1 lets the ref decide, so the bracket form keeps working.

## PR PL-1d1 — the sender and the notice outbox

**Goal.** D-U24-2 "The adopted session is told" and D-U24-3 "The daemon tells the released session", **at least once** (decision 7): the owed notice is written in the transaction that changes the membership (PL-1b), sent from the lead's inbox, retried by the sweeper, cleared only after a send, given up after ten minutes with a log line.

**Files.**
- Create `internal/module/peers/sender.go` and `sender_test.go` (plan v3 P6-1's sender, moved here unchanged in contract).
- Modify `internal/module/peers/module.go:368` (register `SenderKey` beside `OriginResolverKey`).
- Create `internal/module/team/notify.go` and `notify_test.go`.
- Modify `internal/module/team/module.go` (field `sender`, optional lookup in `Init`; the drain goroutine started in `Start`, joined in `Stop`).
- Modify `internal/module/team/sweeper.go` (the liveness tick calls `noticeKick`).
- Modify `internal/module/team/team_handler.go` (`memberView` fills `Origin`, `EndedAt`, `AdoptRequest`) and `team_handler_test.go`.

**Interfaces.**
- Peers (P6-1's contract, `plan-v3.md:1198-1209`): `const SenderKey = "peers.sender"`; `type Sender interface{ Send(ctx context.Context, req ipeers.SendRequest) (ipeers.SendResponse, error) }`; `type SendError struct{ Status int; API ipeers.APIError }`. The implementation runs `m.handleSend` in process under `middleware.WithPrincipal(ctx, Principal{Kind: PrincipalAdmin})` into a buffered `ResponseWriter`.
- Team: `func (m *Module) drainNotices()` — one goroutine, woken by `noticeKick` (from `afterApproved`, from the release route in PL-1d2, and from every liveness tick); for each `PendingNotices()` row: older than `NoticeGiveUpS` → `ClearNotice` + log `[team] notice <kind> to <ref> given up after 10 min`; otherwise `InboxOf(team.LeadSessionID)` and `sender.Send({To: "<self alias>/<member ref>", Text, OriginInbox: inbox})` with a 5 s context; 2xx → `ClearNotice(row, kind, since)`; anything else → kept, one log line per row per minute.
- `noticeText(row, team, leadAddress)`: `AdoptNoticeFmt` or `ReleaseNoticeFmt` filled from the row and the team — the same text on every try (idempotent content).

**Behaviour rules.**
1. **At least once:** the owed notice is in team.db before any send (PL-1b); a crash, a stop or a failed send leaves it owed; the next kick or liveness tick (≤ 10 s) sends it again. A duplicate delivery is possible and harmless (identical text).
2. **Ten minutes, then give up** (`notice_since + 600 s`), with a log line: a lead that is gone (no inbox) cannot be the sender, and a stale notice is worse than none.
3. **A newer notice replaces an older one:** a release before the adopt notice was sent sets `released`; the adopt notice is then never sent (the session is no member any more).
4. **Never under `createMu`;** the drain is one goroutine, so two kicks never send one row twice concurrently.

**Tests.**
- `TestSender_RunsHandleSendAsAdmin`, `TestSender_RefusalIsASendError` (P6-1's names; they move here).
- `TestNotice_AdoptFromTheLeadsInbox`: a fake sender records `OriginInbox = lead inbox`, `To = <alias>/<target ref>`, the exact text; the row's `notice_pending` is cleared.
- `TestNotice_EveryApprovePathSends`: adopt approvals by a click, at create-time, by the switch-on sweep, by a tick and at boot each produce exactly one send (decision 2's four paths plus the tick).
- `TestNotice_FailedSendIsRetriedByTheSweeper`: the first send fails → still pending → one liveness `tick()` → sent and cleared.
- `TestNotice_GivenUpAfterTenMinutes`: the clock moves past `notice_since + 600 s` → cleared, one log line, no send.
- `TestNotice_CrashBetweenCommitAndSendIsResent`: a module stopped before the drain ran; a second module over the same team.db sends it on its first tick.
- `TestNotice_NeverUnderCreateMu`: a sender that blocks does not block a concurrent create.
- `TestStop_JoinsTheDrain`.

**Mutation gates.**
- Clear the notice before the send → `…FailedSendIsRetried…` red.
- Send from the daemon's own address or without `OriginInbox` → `…FromTheLeadsInbox` red.
- Never give up → `…GivenUpAfterTenMinutes` red.

**Size.** ≈ 680 lines, 9 files. **Cut point:** the sender (`peers/sender.go` + test + `module.go`) moves to PL-1d0, merged first. **Deploy.** U24 batch A.

**Risks.**
- **The notice is attributed to the lead.** The peers audit records the lead as the sender of a message its agent did not type; the `[pdx team]` prefix says it is the system's.

## PR PL-1d2 — release; the kill of an adopted member

**Goal.** D-U24-3 (release, `relay_open`, the session back to `none` under the host switch and its own pause — decision 14's matrix) and a `pdx kill` that ends an adopted member without touching the user's tmux session, signalling only a re-verified process (decision 13).

**Files.**
- Create `internal/module/team/release_handler.go` and `release_handler_test.go`.
- Create `internal/module/team/kill_adopted.go` and `kill_adopted_test.go`.
- Modify `internal/module/team/team_handler.go` (`killAndMark` branches on the row's origin).
- Modify `internal/module/team/module.go` (route; the `killProcess` seam; `OriginResolver` gains `SameProcess`).
- Modify `internal/module/peers/origin_resolver.go` (`SameProcess`) and `origin_resolver_presence_test.go`.
- Modify `internal/module/team/handler_test.go` (`fakeOrigins.SameProcess`).

**Interfaces.**
- `POST /api/team/release`, body `team.ReleaseRequest` → 200 `team.Member` (state `released`, or the row as it is when it was not active); 400 `origin_unknown`; 409 `not_lead`, `not_your_member`, `relay_open` (with the op); 503.
- `OriginResolver.SameProcess(pid int, procStart string) (bool, error)` — `LeadPresence`'s step 2 alone (`origin_resolver.go:159-176`): `pid` alive and its start time equal to `procStart`; an unreadable start time is an error.
- `m.killProcess func(pid int) error` (default `syscall.Kill(pid, syscall.SIGTERM)`).

**Behaviour rules.**
1. **Release:** `callerTeam` (`team_handler.go:46-67`) → `matchMember` (any state, `:232-248`) → not found → `not_your_member`. Not `active` → 200 with the row (idempotent; Open question 19). Active → `ReleaseMember` (PL-1b, which also owes the `released` notice); refused by its relay guard → 409 `relay_open` with `OpenRelayOpBySession`'s op; released → `noticeKick()` → 200.
2. **After release** the session is `none` (`relayRole`): its hello and begin follow the host switch, then its own pause (U13).
3. **Kill of an adopted member** (`origin = adopted`), after `killAndMark`'s relay guard (unchanged):
   1. `ResolveOriginBySession(mr.SessionID)`: not live → `MarkMemberGone`, 200 with the row `gone`; a registry error → 503.
   2. **Re-verify right before the signal:** `SameProcess(o.PID, o.ProcStart)` false → `MarkMemberGone` (the process ended or was reused); an error → 503, nothing marked.
   3. `killProcess(o.PID)`: `nil` → `MarkMemberKilled`, 200; `ESRCH` → the process ended meanwhile → `MarkMemberGone`, 200; `EPERM` or any other error → 500 `kill_failed` with the errno, **nothing marked**.
   4. **The tmux session is never killed** (it is the user's). Spawned members keep today's path (`killMember`, tag-checked).
   - The window between the re-verification and `kill(2)` is the one race left (pid reuse inside milliseconds by a process of the same uid); it is stated, not closed.
4. **M30 decides the wait.** If M30 shows Claude Code may outlive SIGTERM past 10 s, this PR adds a bounded wait (poll `SameProcess` for 10 s) and then SIGKILL of the same re-verified process; the plan is amended before the PR starts (decision 13; spec D-U24-3 as revised by d3 says the close follows M30).

**Tests.**
- `TestRelease_ReleasesAndOwesTheNotice`: row `released` + `ended_at`; `notice_pending released`; the drain sends it.
- `TestRelease_HelloAndBeginMatrix` (decision 14): the released session × host switch `self_solo` on / off × its pause on / off → hello answers `on` / `off` / `paused` / `off` with `role:"none"`; begin answers 201 / 409 `self_relay_off` / 409 `self_relay_paused` / 409 `self_relay_off`.
- `TestRelease_RelayOpenBlocks` (spec PL-1 test).
- `TestRelease_NotYourMemberAndNotLead`.
- `TestRelease_AgainAnswersTheRowUnchanged`.
- `TestKillAdopted_SignalsTheReverifiedProcessNotTheTmuxSession`: `killProcess` called with the live pid; no tmux call; row `killed`.
- `TestKillAdopted_ReusedPidIsNeverSignalled`: `SameProcess` false → no signal; row `gone`.
- `TestKillAdopted_ESRCHMarksGone`; `TestKillAdopted_EPERMMarksNothing` (500, row `active`).
- `TestKillAdopted_NotLiveMarksGoneWithoutSignal`.
- `TestKill_SpawnedMemberUnchanged` (today's kill tests stay green).

**Mutation gates.**
- Signal before `SameProcess` → `…ReusedPidIsNeverSignalled` red.
- Mark `killed` on `EPERM` → `…EPERMMarksNothing` red.
- Kill an adopted member's tmux session → `…NotTheTmuxSession` red.
- Release without the relay guard → `…RelayOpenBlocks` red.

**Size.** ≈ 720 lines, 8 files. **Cut point:** the kill (`kill_adopted*.go`, the `team_handler.go` branch, `SameProcess`) moves to PL-1d3, in the same batch. **Deploy.** U24 batch A. **M30 is measured before this PR starts** (Measurements).

## PR PL-1e — CLI: `pdx adopt`, `pdx release`; the skill

**Goal.** D-U24-1 (skill only), D-U24-2 / D-U24-3 (the commands), D-U24-4 (skill only, pinned by a structured guard — decision 8), spec §14 codes. It deploys with the release and kill capabilities (batch A), so the rule ships with them.

**Files.**
- Create `cmd/pdx/adopt_cmd.go` and `adopt_cmd_test.go`.
- Modify `cmd/pdx/main.go:45-46, 54-93` (commands line; dispatch `adopt`, `release`).
- Modify `cmd/pdx/team_cmd.go:72-78` (refusal codes) and `team_cmd_test.go` (`pdx team` shows `released`).
- Modify `cmd/pdx/plugin/purdex/skills/pdx-team/SKILL.md` and `cmd/pdx/plugin/embed_test.go`.

**Interfaces.**

```
pdx adopt <ref> [--wait 9m] [--config <path>]
pdx release <ref> [--config <path>]
```

**Behaviour rules.**
1. **Grammar first, exit 2, before any config load:** exactly one target; a target that is not `_xxxxxx`, `xxxxxx`, `<host>/_xxxxxx` or `<host>/<name> [xxxxxx]`; `--wait` ≤ 0, < 1 s or > 10 min (as `lead.go:108-115`).
2. **`pdx adopt`** mints a UUID v4, prints one stderr line 「申請納入 <ref> 中（<id>），請在 Purdex 介面核准；這個呼叫必須在前景等待（Bash timeout 600000）」, POSTs with `Idempotent()`, then polls `GET /api/team/approvals/{id}?wait=25` while open (the `lead.go:201-226` loop, three hung polls → 20). SIGINT / SIGTERM → best-effort DELETE → 12.
3. **Outcome:** approved → stdout one JSON line `{"request_id","team_id","ref","address","session_id"}` from the payload, exit 0. Denied 10, timeout 11, cancelled / abandoned 12 — **except** `cancelled` with a `close_reason`: `pdx adopt: <detail> <code>` on stderr, exit 13. Refusals at create → `teamReportErr` (code last), exit 13 for the shared table.
4. **`pdx release`** → `POST /api/team/release` → stdout the member JSON, exit 0; 409s exit 13.
5. **Refusal codes** for 13 gain `adopt_self`, `adopt_target_is_lead`, `adopt_already_member`, `adopt_target_not_found`, `remote_unsupported`, `request_open`.
6. **Skill** (the user's words in D-U24-1…4, in English like the rest of the file):
   - "When the user asks this session to become a lead, run `pdx lead request` (above); nothing else changes."
   - As a lead: `pdx adopt <ref>` adopts a running session on this host as a member, **in the foreground with Bash `timeout: 600000`**, after a click in Purdex.app (or at once in 無人值守模式); exit 13 codes listed; the member keeps its own model.
   - `pdx release <ref>` releases a member (it keeps running as an ordinary session); `pdx kill <ref>` closes one.
   - **The D-U24-4 rule, one fixed paragraph** (`skillEndMemberRule`): "When you yourself judge that a member is no longer needed, first ask the user with AskUserQuestion. Name the member (its address and title) in the question, and give exactly three options: 釋出 / 關閉 / 保留. Then do what the answer says: 釋出 → `pdx release <ref>`, 關閉 → `pdx kill <ref>`, 保留 → nothing. When the user asked you directly to release or close a member, do it without asking."
   - As a member: an adopted member reads `[pdx team] 你已成為 …` and follows the member rules.

**Tests.**
- `TestAdoptCmd_UsageErrorsExit2`: none, two targets, a bare name, `--wait 0`, `--wait 11m`.
- `TestAdoptCmd_ApprovedPrintsOneJSONLine`.
- `TestAdoptCmd_DeniedTimeoutCancelled`: 10 / 11 / 12.
- `TestAdoptCmd_CancelledWithCloseReasonExits13CodeLast`.
- `TestAdoptCmd_RefusalsExit13CodeLast`: each create code.
- `TestAdoptCmd_SignalCancels`.
- `TestReleaseCmd_ReleasedAndRefusals`.
- `TestDispatch_AdoptRelease`.
- `TestTeamCmd_ShowsReleasedState`.
- `TestSkill_EndMemberRuleIsPinned` (decision 8, structured): the skill holds `skillEndMemberRule` **byte for byte** as one paragraph; inside it the option list is exactly `釋出 / 關閉 / 保留` (three items, that order, no fourth); it names the member (`Name the member`); it maps each option to its action; it carries the direct-request bypass sentence. A test-local golden constant, so any edit of the rule is a reviewed change of two places.
- `TestSkill_SaysWhatSpec10Requires` (extended): "pdx adopt <ref>", "pdx release <ref>", "asks this session to become a lead".

**Mutation gates.**
- Map `cancelled` + `close_reason` to 12 → `…CancelledWithCloseReasonExits13…` red.
- Add a fourth option, or drop the bypass sentence, in the skill → `…EndMemberRuleIsPinned` red.

**Size.** ≈ 720 lines, 7 files. **Cut point:** `pdx release` and its tests move to PL-1e2, same batch. **Deploy.** U24 batch A (CLI + setup).

**Acceptance** (mlab, two throwaway sessions A, B; after the whole batch is deployed): A `pdx lead request` → approve; A `pdx adopt _<B ref>` → the `adopt` card (PL-2a) → approve → B receives the notice from A's address; `pdx team --json` shows B with `origin:"adopted"`, `state:"active"`; B's `pdx relay self status` says member; A `pdx release _<B ref>` → B's notice; B is `none`; A adopts B again with unattended on → exit 0 at once, no card; A `pdx kill _<B ref>` → B's Claude Code exits, its tmux session and shell stay; no flag left.

## PR PL-1f — the roster: `GET /api/team/roster`, `team.roster`

**Goal.** D-U24-5 "Team data comes from the team module … cached per host like other host data", for an App that has no session inbox: `GET /api/team` is the caller's own team only (`team_handler.go:23-39`, keyed by `origin_inbox`), so the App needs a host-wide view (deviation 8).

**Files** (11):
- Create `internal/team/wire_roster.go` and `wire_roster_test.go`.
- Create `internal/module/team/roster.go` and `roster_test.go`.
- Modify `internal/module/team/module.go` (route; `OnSubscribe`; `rosterMu`, `lastRosterHash`).
- Modify the call sites: `unattended.go` (`afterApproved`, which covers every approved `lead` and `adopt` on every path), `spawn_register.go:160-163` (spawn done), `team_handler.go` (spawned kill marked), `kill_adopted.go` (adopted kill / gone), `release_handler.go` (released), `sweeper.go` (team ended, member gone, the liveness tick), `relay_report.go:154-162` (`cleared` applied) — seven files.

**Interfaces.**

```go
const RosterEventType = "team.roster"
type RosterSession struct {
	SessionID, Ref, Address string // json: session_id, ref, address
	Title       string `json:"title,omitempty"`
	Name        string `json:"name,omitempty"`        // registry name
	TmuxSession string `json:"tmux_session,omitempty"` // tmux session NAME ("" when not in tmux)
	Live        bool   `json:"live"`
}
type RosterMember struct { RosterSession; State MemberState `json:"state"`; Origin string `json:"origin"`; JoinedAt int64 `json:"joined_at"` }
type TeamRoster struct { ID, HostID string; CreatedAt int64; Lead RosterSession; Members []RosterMember } // members never null
type Roster struct { Teams []TeamRoster `json:"teams"` }                                                   // never null
type RosterEventValue struct { Op string `json:"op"`; Teams []TeamRoster `json:"teams"` }                  // snapshot | changed
```

**Behaviour rules.**
1. **Content:** live teams only, oldest first; members `active` only, by `created_at` (join order). Released, killed and gone members leave at once (D-U24-6 last bullet).
2. **Each session:** live registry origin when present (`ResolveOriginBySession`: address, title, name, tmux name before `:`); else the stored values (lead: the request row's origin; member: its row) with `live:false`.
3. **`rosterChanged()`** builds the roster, hashes its JSON, and broadcasts `changed` only when the hash differs from the last broadcast, under `rosterMu` (read + send, like `snapshotUnderLock`, `module.go:453-477`). Called after every write listed in Files and on the sweeper's liveness tick (every 10 s, `sweeper.go:15-18`), which also catches title and name changes.
4. **Snapshot** to every new subscriber through `OnSubscribe`; a send that fails closes the subscriber as `sendSnapshot` does (`module.go:434-445`).

**Tests.**
- `TestWireRoster_JSONShapes`: `teams:[]`, `members:[]` never null.
- `TestRoster_LiveTeamsActiveMembersWithTmuxNames`: a spawned member (`tm-…`), an adopted member (its user's tmux session name), a lead.
- `TestRoster_ReleasedKilledGoneAndEndedTeamsAreOut`.
- `TestRoster_SnapshotToEveryNewSubscriber`.
- `TestRoster_ChangedOnEveryAdoptApprovePath` (decision 2): click, create-time, switch-on sweep, tick, boot → one `changed` each.
- `TestRoster_ChangedAfterReleaseKillSpawnAndTeamEnd`.
- `TestRoster_TickBroadcastsOnlyWhenItChanged`: two ticks, no change → no event; a title change → one.
- `TestRoster_ClearedMovesTheLeadsSession`.

**Mutation gates.**
- Include `released` members → `…AreOut` red.
- Broadcast from `handleCreateAdopt` instead of `afterApproved` → `…OnEveryAdoptApprovePath` red.
- Broadcast on every tick → `…OnlyWhenItChanged` red.

**Size.** ≈ 620 lines, 11 files. **Cut point:** the tick diff and the call sites outside `afterApproved` move to PL-1f2. **Deploy.** U24 batch B (daemon).

## PR PL-1g — the mod re-reads a cached `member` role at the threshold

**Goal.** Make D-U24-3 "可以自己接力" true in practice. The mod caches `s.role` from hello and from a 409 (`register.js:183, 422`) and skips every ask while it reads `member` (`:381`); hello is not re-sent until the next start or `/clear`. A released member (or the member of a team that ended, D4) would therefore never self-relay and would be auto-compacted instead.

**Needs U1-1b merged** (decision 16): U1-1b rewrites `register.js`'s hooks; this PR is written on top of it.

**Files.** Modify `cmd/pdx/plugin/purdex/hooks/register.js` (`maybeBegin`, `:380-400` at `e47c1f35`) and `relay.test.ts`.

**Interfaces.** `const ROLE_RECHECK_MS = 60_000`; state `s.roleCheckedAt`.

**Behaviour rules.**
1. In `maybeBegin`, read the usage first. When `s.role === 'member'`, the usage is at or above the threshold, no hello is in flight and `ROLE_RECHECK_MS` has passed since `s.roleCheckedAt`: `helloLater($)` and return. The hello's answer sets the role (`:183`); a `none` lets the next `turn.complete` ask.
2. Below the threshold, nothing is sent. No hook is added or re-registered (U1's M-U1-3: a second registration of one event without a matcher fails the whole mod), and nothing new runs inside a hook (the call runs from `$.clock.after`, `:193-197`).

**Tests** (`relay.test.ts`):
- `a member at the threshold sends hello again, and asks at the next turn end when the role is now none`;
- `a member below the threshold sends nothing`;
- `the re-check runs at most once a minute`;
- `a member whose hello still says member never begins`.

**Mutation gates.**
- Re-check below the threshold → `…below the threshold sends nothing` red.
- No rate limit → `…at most once a minute` red.

**Size.** ≈ 160 lines, 2 files. **Cut point:** none needed. **Deploy.** U24 batch B (daemon for the embedded mod, + setup).

---

# Phase PL-2 — the adopt card, the roster store, the sidebar (adopt spec D-U24-2 card, D-U24-5)

**Facts this phase rests on:**
- **The dialog body switches on the kind inline.** `OpenApprovalDialog` (`spa/src/components/ApprovalDialogHost.tsx:64`) sets `isSelfRelay` (`:71`); the title (`:242`), the rows (`:262-282`) and the grant inputs / self-relay note (`:286-334`) branch on it; the overlay carries `data-kind` (`:229`). `grantOk` is always true for a self relay (`:180`).
- **Kinds the SPA knows:** `APPROVAL_KINDS` (`spa/src/lib/team/types.ts:152`); an unknown kind is skipped row by row (`:159-161`, `approval-ws.ts:43,52`). Labels: `approvalKindLabel` (`spa/src/lib/team/approval-format.ts:29-31`); notification text by kind (`spa/src/lib/team/approval-notify.ts:38-55`). Locale keys `approval.*` at `zh-TW.json` / `en.json` `:1979-2022`.
- **"Back to the requester"** (U22) is `gotoRequester` (`spa/src/lib/team/approval-goto.ts:38-56`): it maps `origin.tmux`'s session **name** to the host's session list (`useSessionStore.sessions[hostId]`) and activates or opens that tab. For an `adopt` row the requester is the lead.
- **The sidebar has no session list.** The wide activity bar shows workspaces (`spa/src/features/workspace/components/ActivityBarWide.tsx:298-388`); each workspace lists its **tabs** (`InlineTabList` → `InlineTab`) only when `tabPosition !== 'top'` (`WorkspaceRow.tsx:47-48, 148-150`). The other list is Workers: one `ExecutionsView` per shown host (`components/executions/WorkerList.tsx`), whose rows are Nexen executions keyed by execution id (`ExecutionsView.tsx:39-61`, `ExecutionRowCompact.tsx:77`); a row does carry the conversation's `session_id` and `pid` (`spa/src/lib/nex/types.ts:29-31`). The session lists (`components/SessionSection.tsx:78-215`, Hosts › Sessions) are on the New Tab page and the Hosts page, not in the sidebar. `TabPosition = 'top' | 'left' | 'both'` (`spa/src/stores/useLayoutStore.ts:14`).
- **No Workers row holds a durable team role** (decision 9; d3 wrote it into spec D-U24-5, decision 21): a role is bound to the process its row recorded — the team ends when the lead's own process is gone (`sweeper.go:122-154` via `LeadPresence`), a member is marked gone the same way (`:212-237`) — and an execution has a process and a registry entry only while a turn runs (peer-mailbox spec §2: 睡著的執行體沒有行程、沒有 registry 檔). Spawn creates only tagged tmux sessions (`spawn_tmux.go:23`), never executions. So an execution can hold a role for at most one turn, and the sweeper ends it within ten seconds of that turn; the Workers list therefore has no team session to indent.
- **`InlineTabList`** renders `validIds` in `workspace.tabs` order in a vertical `SortableContext` (`spa/src/features/workspace/components/InlineTabList.tsx:30-58`); rows are indented `pl-[18px]` (`InlineTab.tsx:117`). Drags end in `computeDragEndAction` (`spa/src/features/workspace/lib/computeDragEndAction.ts:37-110`): same-workspace `arrayMove` over `ws.tabs` by index (`:67-80`), cross-workspace move (`:83-93`), header drop (`:99-106`).
- **A tab binds tmux sessions by code**, not name (`spa/src/types/tab.ts:123`: `kind 'tmux-session'`, `hostId`, `sessionCode`, `cachedName`, `tmuxInstance`), one per pane of its layout; the code ↔ name map is `useSessionStore.sessions[hostId]` (`spa/src/stores/useSessionStore.ts:8`, rows `lib/host-api.ts:7-20`). The host badge already walks a tab's panes in pre-order to pick the first tmux pane (`spa/src/lib/host-color.ts:154-181`). No SPA store maps a session to a CC session id or ref for all sessions (`stores/usePeerStore.ts:65-117` is fetched for the active pane only).

## PR PL-2a — the `adopt` card

**Goal.** D-U24-2 "decided like `lead` (one click on any App)" — a card in the existing dialog host, its notification and kind label; U22's switch to the requester applies (the requester is the lead). **It merges and the main checkout fast-forwards before PL-1c** (decision 3).

**Files.**
- Modify `spa/src/lib/team/types.ts` (`ApprovalKind` and `APPROVAL_KINDS` add `'adopt'`; `AdoptPayload`, `adoptPayloadOf`; `Approval.close_reason?`).
- Modify `spa/src/components/ApprovalDialogHost.tsx` (the `adopt` body) and create `ApprovalDialogHost.adopt.test.tsx`.
- Modify `spa/src/lib/team/approval-format.ts:29-31` and `approval-notify.ts:38-55` with their tests.
- Modify `spa/src/lib/team/approval-ws.test.ts`.
- Modify `spa/src/locales/en.json`, `zh-TW.json`.

**Interfaces.**
- `adoptPayloadOf(a: Approval): AdoptPayload` — defensive, strings default `''` (as `selfRelayPayloadOf`, `types.ts:204-215`).
- The `adopt` body: title `approval.dialog.title_adopt` (「{{host}}：{{lead}} 想把 {{target}} 納入 team」); rows host, lead session (origin label + address), **target** (title else name else ref, address, cwd, tmux), team id, countdown; note `approval.dialog.adopt_note` 「核准後這個 session 會成為 member：自我接力關閉，接力由 lead 安排；它的模型不變。」; buttons 核准 / 拒絕; no grant inputs (`grantOk` true).
- Notification title `approval.notify.title_adopt` (「{{host}}：{{lead}} 想納入 {{target}}」); body the target's cwd.
- Kind label `approval.kind.adopt` (「納入申請」 / `adoption request`).

**Behaviour rules.**
1. The `adopt` card is the `lead` card's layout with the target block in place of the grant; minimize, the pill, the focus guard and the queue behave as for every kind (P9b-2).
2. A `409` with a U24 code (`adopt_target_is_lead`, …) carries the closed row → today's "handled" toast path (`approval-decide.ts:111-115`) names the state 「已取消」.
3. **Harmless before the daemon knows the kind:** a daemon before PL-1c never sends an `adopt` row.

**Tests.**
- `ApprovalDialogHost.adopt.test.tsx`: `renders lead and target, no grant inputs`; `approve and deny are one click`; `minimize → pill counts an adopt`; `a decision here switches to the lead's tab` (`gotoRequester` with the lead's origin).
- `approval-notify.test.ts`: `adopt title names the lead and the target`.
- `approval-format.test.ts`: `kind label adopt`; `a cancelled adopt toasts its state`.
- `approval-ws.test.ts`: `an adopt row in a snapshot is kept, not skipped as unknown`.
- `locale-completeness.test.ts`.

**Mutation gates.**
- Leave `'adopt'` out of `APPROVAL_KINDS` → the snapshot test red.
- Render the grant inputs for `adopt` → the no-inputs assertion red.

**Size.** ≈ 480 lines, 10 files. **Cut point:** the notification text (`approval-notify.ts` + test) moves to PL-2a2. **Deploy.** SPA, fast-forwarded before batch A.

## PR PL-2b — the roster store and the team-tab layout

**Goal.** Cache each host's roster like other host data (D-U24-5 last sentence) and derive, once, how tabs group by team, for both the sidebar (PL-2c) and the tab bar (PL-3b), from **any** pane of a tab (decision 11).

**Files.**
- Create `spa/src/lib/team/roster.ts` (types, `isRoster`, `parseRosterEvent`) and its test.
- Create `spa/src/stores/useTeamRosterStore.ts` and its test.
- Create `spa/src/lib/team/team-tab-layout.ts` and `team-tab-layout.test.ts`.
- Modify `spa/src/lib/host-events.ts:4-16` and `spa/src/hooks/useMultiHostEventWs.ts` (branch `team.roster`).

**Interfaces.**
- `useTeamRosterStore`: `byHost: Record<hostId, TeamRoster[]>`; `apply(hostId, teams)` (snapshot and changed both replace the host's set); `forgetHost`. Not persisted, per renderer.
- `teamRoleOfTab(tab, rosterByHost, sessionsByHost) → { hostId; teamId; role: 'lead' | 'member'; joinedAt; leadLabel } | null`:
  - every `tmux-session` pane of the tab's layout is looked at; a pane's session name = the host's session list row with its code, else its `cachedName`; matched against the roster's `tmux_session` names (a lead match before a member match);
  - **one team:** that team; **several teams:** the primary pane's team when the primary pane matches one, else the first matching pane in layout pre-order (the order `host-color.ts:154-181` walks);
  - no match, or no `tmux-session` pane → null. `leadLabel` = the lead's title, else the name part of its address.
- `layoutTeamTabs(tabIds: string[], tabsById, rosterByHost, sessionsByHost) → { order: string[]; blocks: TeamBlock[]; orphanHint: Record<tabId, string> }`:
  - a **block** = one team's tabs **within this list**: the lead's tab(s) first, then members' tabs in their stored relative order (Open question 13);
  - the block sits where its first tab (in stored order) sits; non-team tabs keep their positions;
  - a member tab whose lead has no tab in this list is not blocked: it keeps its position and gets `orphanHint[tabId] = leadLabel` (D-U24-5 "member of <lead name>");
  - `TeamBlock = { key: '<hostId>\0<teamId>'; hostId; teamId; leadLabel; leadTabIds; memberTabIds }`.
- `reorderWithBlocks(stored: string[], layout, activeId, overId) → string[] | null` — a drag in display space mapped back to stored order: within one block (a member over a member) → the stored order with the member moved; a lead dragged within its block, a block tab dropped outside its block, or a non-team tab dropped inside a block → `null` (snap back, nothing written).

**Behaviour rules.**
1. **Pure and derived** (D-U24-6 "Membership is derived, not manual"): nothing here writes a store but the roster store.
2. Pinned tabs are passed in by the caller only from the normal zone (deviation 10).

**Tests.**
- `roster.test.ts`: a valid snapshot; `teams` not an array, a member without `tmux_session` type, `op: 'x'` → dropped whole.
- `useTeamRosterStore.test.ts`: replace, forget.
- `team-tab-layout.test.ts`:
  - `lead first then members in stored order, placed at the block's first tab`;
  - `non-team tabs keep their positions`;
  - `a member without its lead in the list keeps its place and gets the hint`;
  - `two teams on two hosts make two blocks`;
  - `the session list unknown: cachedName decides`;
  - `a released member (absent from the roster) is not blocked`;
  - `a split tab whose second pane shows a member joins that member's team` (decision 11);
  - `a tab with panes of two teams follows its primary pane; with the primary in neither, the first matching pane in pre-order`;
  - `reorderWithBlocks: member within block moves; lead within block, block tab outside, outside tab inside → null`.

**Mutation gates.**
- Look at the primary pane only → the split-tab test red.
- Order members by `joinedAt` instead of stored order → the in-block drag test red.
- Let a block tab leave its block → the `null` cases red.

**Size.** ≈ 700 lines, 8 files. **Cut point:** `reorderWithBlocks` and its test move to PL-2b2 (needed by PL-2c and PL-3b). **Deploy.** SPA.

## PR PL-2c — the sidebar's tab list: members under their lead

**Goal.** D-U24-5 where the sidebar lists sessions: its per-workspace tab list. The Workers list is not changed: it has no team session to indent (PL-2 facts; spec D-U24-5 as revised, decisions 9 and 21).

**Files.**
- Modify `spa/src/features/workspace/components/InlineTabList.tsx` (display order from `layoutTeamTabs`; caret on the lead row; members hidden when collapsed) and `InlineTab.tsx` (`depth` and `hint` props; indent `pl-[30px]` at depth 1).
- Modify `spa/src/features/workspace/lib/computeDragEndAction.ts:67-80` (same-workspace reorder through `reorderWithBlocks`; `null` → `noop`) and its test.
- Create `spa/src/stores/useTeamTabUiStore.ts` and its test (collapse state for both surfaces).
- Create `spa/src/features/workspace/components/InlineTabList.team.test.tsx`.
- Modify `spa/src/locales/en.json`, `zh-TW.json` (`team.member_of`: 「{{lead}} 的 member」 / `member of {{lead}}`; `team.collapse_members`, `team.expand_members`).

**Interfaces.**
- `useTeamTabUiStore`: `sidebarCollapsed: Record<blockKey, true>`, `barCollapsed: Record<blockKey, true>`; `toggleSidebar(key)`, `toggleBar(key)`, `expandFor(tabId)`. Memory only, per window (Open question 15).
- `InlineTab` gains `depth?: 0 | 1` and `hint?: string` (a muted second line).

**Behaviour rules.**
1. **Always on in the sidebar** (the setting of PL-3a changes only the tab bar, D-U24-6).
2. **The lead row's caret** toggles its members; a collapsed lead row shows the member count.
3. **The active tab is never hidden:** when the active tab is a collapsed member, its block expands (`expandFor` on `activeTabId` change).
4. **Drag:** within a block, members reorder; anything else that would break a block snaps back (`noop`). Moving a tab to another workspace is unchanged (it then shows in that workspace as an orphan with the hint, or in that workspace's block).
5. **Not tab-hosted:** the sidebar is app-level; the collapse state is in the store, so a switch of `tabPosition` (which unmounts the list, `WorkspaceRow.tsx:48`) keeps it.

**Tests** (real `InlineTabList` + `InlineTab` + stores):
- `members render indented right under the lead's tab`;
- `the caret collapses and expands the members; collapsed shows the count`;
- `activating a collapsed member expands its block`;
- `an orphan member shows 「<lead> 的 member」 in place`;
- `a roster change that releases a member un-indents it at once`;
- `collapse survives an unmount and remount of the list`;
- `computeDragEndAction`: `member over member → reorder`, `member over a non-team tab → noop`, `cross-workspace move unchanged`.

**Mutation gates.**
- Keep `useState` for the collapse → the remount test red.
- Drop `expandFor` → the activation test red.

**Size.** ≈ 650 lines, 9 files. **Cut point:** the `computeDragEndAction` change and its test move to PL-2c2. **Deploy.** SPA.

**Risks.** #1866 changes the Workers list (`components/executions/*`); this PR does not touch it.

---

# Phase PL-3 — tab groups (adopt spec D-U24-6)

**Facts this phase rests on:**
- **The tab bar** is `spa/src/components/TabBar.tsx`, mounted when `tabPosition !== 'left'` (`spa/src/App.tsx:216-228`) with `displayTabs` = the active workspace's `ws.tabs` (`App.tsx:97-104`, `features/workspace/lib/getVisibleTabIds.ts:27-36`). It splits a pinned zone and a normal zone (`TabBar.tsx:30-33, 93-152`), each a horizontal `SortableContext`; `handleDragEnd` allows same-zone moves only (`:65-69`) and calls `onReorderTabs(newOrder)` (`:58-79`) → `reorderWorkspaceTabs` (`features/workspace/hooks.ts:147-151`, `features/workspace/store.ts:136-156`). The drag clamp measures `normalTabsRef` (`TabBar.tsx:41-56`). A chip before a group's first tab goes inside the `normalTabs.map` fragment before `<SortableTab>` (`:136-149`), outside `items`.
- **Tab indicators** live in `SortableTab` (`spa/src/components/SortableTab.tsx`): host badge `:149-159`, status dot via `TabIcon` `:148`, unread pip `:164-167`, offline / lock `:162-163`; the approval hand is drawn by `TabStatusIndicator` (`components/TabStatusIndicator.tsx:93-138`).
- **The host palette** is 8 Tailwind-500 values, `HOST_COLOR_PRESETS` (`spa/src/lib/host-color.ts:46-55`): `#ef4444 #f97316 #eab308 #22c55e #14b8a6 #3b82f6 #8b5cf6 #ec4899`. There is no categorical palette for anything else.
- **Settings › 介面** is `InterfaceSection` with subsections from `registerInterfaceSubsection` (`spa/src/lib/interface-subsection-registry.ts:16`); only `new-tab` is registered (`spa/src/lib/register-modules/index.tsx:300-307`). Toggle rows are `SettingItem` + `ToggleSwitch` (`components/settings/TerminalSection.tsx:204-206`). Boolean UI settings live in `useUISettingsStore` (persisted `purdex-ui-settings`, synced across windows, `stores/useUISettingsStore.ts:241-242, 265-268, 342`); only fields listed in `lib/profile/projections.ts:55-64` travel between devices.

## PR PL-3a — the setting, the palette, the chip

**Goal.** D-U24-6 "Colour", "Setting", and the chip as a component.

**Files.**
- Modify `spa/src/stores/useUISettingsStore.ts` (`teamTabGroups: boolean`, default `true`, `setTeamTabGroups`) and its test.
- Create `spa/src/components/settings/TabsSubsection.tsx` and its test; register it in `spa/src/lib/register-modules/index.tsx:300-307` (`id 'tabs'`, label `settings.interface.tabs`, order 1).
- Create `spa/src/lib/team/team-colors.ts` and its test.
- Create `spa/src/components/TeamGroupChip.tsx` and its test.
- Modify `spa/src/locales/en.json`, `zh-TW.json` (`settings.interface.tabs`, `settings.interface.team_tab_groups.label` = 「分頁群組顯示 team」, `.desc`; `team.chip.*`).

**Interfaces.**
- `TEAM_COLORS = ['#fda4af', '#fcd34d', '#bef264', '#6ee7b7', '#67e8f9', '#7dd3fc', '#c4b5fd', '#f0abfc']` (Tailwind 300 of rose, amber, lime, emerald, cyan, sky, violet, fuchsia).
- `teamColor(teamId: string): string` = `TEAM_COLORS[fnv1a32(teamId) % 8]` (the FNV of `approval-notify.ts:17-24`, moved to a shared helper or duplicated with a test).
- `TeamGroupChip({ label, color, collapsed, count, onToggle })`: a `button` with `aria-expanded={!collapsed}`, `aria-label` = `t('team.chip.aria', { lead, count })`, `data-testid="team-chip"`; background `color` at 25 % alpha, text primary; collapsed shows `+N`.

**Behaviour rules.**
1. **Device-local** (Open question 17): `teamTabGroups` is not added to `PROJECTIONS.settings`.
2. **Stable colour** per team id across restarts and windows (D-U24-6), never one of `HOST_COLOR_PRESETS`.

**Tests.**
- `team-colors.test.ts`: `stable per id`; `eight distinct values`; `none equals a host preset`.
- `TeamGroupChip.test.tsx`: label, colour, `aria-expanded`, `+N` when collapsed, click toggles.
- `TabsSubsection.test.tsx`: the toggle reads and writes `teamTabGroups`; default on.
- `useUISettingsStore.test.ts`: default `true`.
- `locale-completeness.test.ts`.

**Mutation gates.**
- Reuse `HOST_COLOR_PRESETS` → `none equals a host preset` red.
- Default `false` → the default test red.

**Size.** ≈ 520 lines, 11 files. **Cut point:** the settings subsection (`TabsSubsection` + registration + store field) moves to PL-3a2. **Deploy.** SPA.

## PR PL-3b — groups in the tab bar

**Goal.** D-U24-6 in the tab bar: a group per team with an open tab in the normal zone, chip before its first tab, coloured underline, contiguity, drag within a group, snap back outside it, collapse with the active tab never hidden, indicators unchanged, the setting.

**Files.**
- Modify `spa/src/components/TabBar.tsx` (layout of the normal zone; chips; hidden collapsed tabs; `handleDragEnd` through `reorderWithBlocks`; the active-tab effect).
- Modify `spa/src/components/SortableTab.tsx` (`groupColor?: string` → a 2 px underline; nothing else changes).
- Create `spa/src/components/TabBar.team-groups.test.tsx`.
- Modify `spa/src/components/TabBar.test.tsx` and `SortableTab.test.tsx` (the setting off ⇒ today's bar, unchanged).

**Behaviour rules.**
1. **Setting off ⇒ exactly today's bar** (no layout call, no chip, no underline).
2. **Normal zone only.** Pinned tabs are never grouped (deviation 10: Chrome takes a pinned tab out of its group).
3. **Display order** = `layoutTeamTabs(normalIds, …).order`; each block renders `<TeamGroupChip>` then its tabs; tabs carry `groupColor = teamColor(teamId)`.
4. **Collapse** (`useTeamTabUiStore.barCollapsed`): a collapsed block renders the chip only; its tabs leave `SortableContext.items`. **The active tab is never hidden:** activating a collapsed block's tab expands it; collapsing the block that holds the active tab first activates the nearest tab outside the block (right, else left), and does nothing when there is none (Open question 15).
5. **Drag:** `reorderWithBlocks`; `null` ⇒ no `onReorderTabs` call (the tab snaps back). The chip is not draggable.
6. **Indicators** (host badge, status dot, approval hand, unread pip, offline, lock) render on grouped tabs as on any tab.
7. **Not tab-hosted:** `TabBar` is app-level; collapse lives in the store, so `tabPosition` top → left → top keeps it.

**Tests** (real `TabBar` + `SortableTab` + stores; rosters seeded in `useTeamRosterStore`, sessions in `useSessionStore`):
- `a lead tab and two member tabs render one chip with the lead's name, before the lead's tab, contiguous`;
- `tabs of the group carry the team colour underline; others none`;
- `a non-team tab between them in the stored order is shown after the block`;
- `dragging a member within the group calls onReorderTabs with the member moved`;
- `dragging a member outside the group, and a non-team tab into it, never calls onReorderTabs`;
- `clicking the chip hides the members and shows +N; again shows them`;
- `activating a tab of a collapsed group expands it`;
- `collapsing the group of the active tab activates the nearest tab outside it`;
- `the host badge, status dot and unread pip still render on grouped tabs`;
- `a roster changed event that releases a member removes its underline at once`;
- `setting off: no chip, no underline, today's order`;
- `pinned lead tab: no group in the pinned zone`;
- `collapse survives a TabBar unmount and remount`.

**Mutation gates.**
- Call `onReorderTabs` on a `null` reorder → the drag-outside test red.
- Drop the active-tab expansion → `activating a tab of a collapsed group expands it` red.
- Render chips with the setting off → `setting off` red.

**Size.** ≈ 760 lines, 5 files. **Cut point:** collapse (rule 4, its store wiring and its four tests) moves to PL-3b2. **Deploy.** SPA.

**Acceptance** (mlab App): a lead with one spawned and one adopted member, three tabs: one group, chip = the lead's title, members after the lead; drag a member within → order kept; drag it out → snaps back; collapse, then activate a member from the sidebar → expands; release the adopted member → its tab leaves the group; Settings › 介面 › 分頁 off → plain bar.

**Risks.** **U1's lights v2** changes how the status dot is aggregated per tab (interface-language page, "聚合：逐 pane 計算狀態，tab 和 session 取最高優先序"); it may edit `SortableTab.tsx` / `TabStatusIndicator.tsx`. This PR only adds an underline prop to `SortableTab`; rebase onto whichever lands first.

---

## Measurements

- **M30 — how Claude Code ends on SIGTERM** (PL-1d2 rules 3–4; decision 13; spec D-U24-3 as revised). Before PL-1d2 starts, by the coordinator, never on a real or peer session:
  1. a throwaway `tmux new -d -s m30 'claude --dangerously-skip-permissions --model haiku'` in a fresh `/private/tmp` directory; wait for `~/.claude/sessions/<pid>.json`;
  2. **idle:** `kill -TERM <pid>`; record the time until `kill -0 <pid>` fails, until the registry file is gone, and whether the daemon log has the SessionEnd hook event for that session id; `tmux capture-pane -p -t m30` shows the shell;
  3. **busy:** a second throwaway session running a foreground `ping -c 60 127.0.0.1` Bash call (M28's note: a standalone `sleep` is refused); `kill -TERM` mid-call; the same three times;
  4. **not ended within 10 s** in either case: record whether a second SIGTERM or only SIGKILL ends it, and whether the registry file is left behind;
  5. `tmux kill-session` for both; no flag left in `~/.config/pdx/hooklocks/`.

  If either case can take longer than 10 s, PL-1d2 adds the bounded wait and SIGKILL of rule 4, and this plan is amended before PL-1d2.

No other new behaviour of Claude Code is relied on: an approved request needs no mod change (PU-1 facts), and PL-1g only re-sends the existing hello.

## Intersections with other lines

| Line | What touches what | Handling |
|---|---|---|
| **U1 interface unification** (`mlab/_3fj93m`) | U1-1b adds `hooks/events.js`, observer lines in `register.js`'s four hooks and an `embed_test.go` guard; lights v2 changes tab status aggregation. | **PL-1g needs U1-1b merged** (decision 16) and edits `maybeBegin` alone (no hook registered, so M-U1-3 is not triggered). **PU-1c / PL-1e** edit `SKILL.md` and `embed_test.go`'s skill test (textual). **PU-2b** puts a button in the title bar and **PL-3b** an underline in `SortableTab`: confirm with U1 before each starts. |
| **#1866 worker status deltas** (purdex-6d) | PR2b routes `nex.executions.hello` / `nex.execution` in `useMultiHostEventWs.ts` and the `host-events.ts` union; touches the Workers list. | **PU-2a** and **PL-2b** add one branch each to the same two files (textual). The Workers list holds no team session (PL-2 facts), so nothing here touches it. Daemon side: both use `OnSubscribe`, no shared code. |
| **P9a-3 Hosts › 接力 editors** (not merged on `e47c1f35`) | `RelaySection.tsx`, `host-config-api.ts`, the `relay` row's queue. | **None.** The unattended switch is deliberately not a field of the `relay` row and not in `GET /api/hostconfig` (PU-1a rule 4). |
| **P6-1, P6-2b** (plan v3) | P6-1's `peers.Sender` and two tests move into **PL-1d1**; P6-2b's member-relay create must be exclusive with release. | **Edited in plan v3 by this commit** (Contracts for later PRs 1–2): P6-1 needs PL-1d1 and keeps the virtual peer, auto-reply and handover; P6-2b's rule 1 and test carry the membership-in-transaction contract. |
| **P4b-4** (plan v3) | Defines `remote_unsupported`. | **Edited in plan v3 by this commit:** P4b-4 needs PL-1a and reuses its code (Contracts for later PRs 3). |
| **Peer mailbox** (`2026-10-08-peer-mailbox-integration-plan.md`) | Virtual names in addresses. | Adopt targets resolve by ref (PL-1c rule 1); the roster's `address` is display only. |
| **Mobile API P3** | Adds names to `capabilities`. | Textual conflict with PU-1c; both append. |

## User-visible behaviour (for coordinator review)

這 21 個 PR 全部上線後，使用者會看到或操作到的行為與預設。每條後面是來源：spec 的 D 編號／U 編號，或本 plan 的 Open question（OQ）／Coordinator decision（決定）編號。

**無人值守模式（U23）**

1. **按鈕位置**：每個視窗的標題列右側、版型按鈕的左邊，一顆月亮圖示的按鈕，旁邊一個小的「▾」。（D-U23-5、D-U23-6；OQ3）
2. **按鈕狀態**：關＝一般顏色的月亮；開＝強調色＋文字「無人值守中」；部分＝月亮加警示色外框；工作台上沒有任何顯示中的主機時，按鈕不能按。（D-U23-5、D-U23-6）
3. **「部分」是什麼**：顯示中的主機有的開有的關、有主機連不上、或有主機的 daemon 太舊不支援，都顯示「部分」。連不上的主機算「部分」，因為它可能還開著、還在自動核准。被隱藏的主機不算、也不會被改。（D-U23-5）
4. **滑鼠停在按鈕上（tooltip）**：關或開時一行說明；部分時分組列出主機：「未開啟：…」「無法連線：…」「daemon 版本過舊：…」。（D-U23-5）
5. **按下去**：從「關」或「部分」→ 所有連得上、支援的顯示中主機一起打開；從「開」→ 一起關掉。連不上或太舊的主機不會被寫入；之後才連上的主機不會自動跟著改，按鈕維持「部分」，直到再按一次。某台設定失敗時跳一則 toast 列出那幾台。（D-U23-5）
6. **打開的那一刻**：主機上已經在等核准的 lead 申請、自我接力申請（U24 上線後也包括納入申請）立刻被核准；每個視窗上的核准框直接關掉，不跳「已由…核准」的 toast。少數當下沒核准成功的，daemon 每秒自動補核准。（D-U23-3；OQ1；決定 5）
7. **開著期間新來的申請**（lead、自我接力、納入）：完全不出現核准框、不發系統通知、不跳 toast；申請的 session 馬上拿到「已核准」。（D-U23-1、D-U23-6；決定 1）
8. **無人值守通過 lead 申請時的 member 上限**＝min(lead 申請的數字, 3)，沒指定的申請視為 3；允許的根目錄照申請。（D-U24-7／U25）
9. **不在範圍、照常等人**：worker 的工具權限核准、agent 的 AskUserQuestion 與權限詢問。（U23）
10. **關掉**：只是不再自動核准；已經在等的申請照樣等人；關掉時**不會**自動跳出清單。（D-U23-6；決定 19）
11. **「▾」期間自動通過清單**：只在點「▾」時打開。列出「最近一次打開以來」被自動核准的申請：主機、session 名稱、種類（lead 申請／接力申請／納入申請）、時間（時:分），多台主機合併、新的在上。每台一次讀 50 筆，還有更多時出現「顯示更多」；某台讀取失敗會寫出那台的名字；沒有任何一筆時顯示空清單的說明。下次再「打開」開關時，清單從新的打開時間重新算起。（D-U23-6；OQ3；決定 17）
12. **開關會留著**：daemon 重啟後保持；每台主機各自一份；不跟 Profile Sync 同步。（D-U23-7）
13. **所有視窗同步**：任一視窗或其他裝置按下，所有視窗的按鈕立刻跟著變。（D-U23-6）
14. **誰能打開**：`pdx` 沒有打開無人值守的指令，skill 也禁止 agent 去開；但同一個使用者帳號下的 agent 如果拿 token 直接呼叫 daemon，技術上仍能打開——這時每個視窗的按鈕都會變成「無人值守中」，daemon log 記下是哪個 App 標籤、從哪個位址開的。（D-U23-2；deviation 6）
15. **agent 端看到的**：開著時 `pdx lead request` 幾乎立刻回傳核准結果（stderr 仍會先印「請在 Purdex 介面核准」那行）；自我接力不需等待，直接開始寫接力檔。（PU-1b2）

**lead 核准框的 member 上限（U25）**

16. lead 申請的核准框裡，「member 上限」欄一律預填 3；lead 申請的數字不是 3 時，欄位旁顯示「lead 申請 N 個」；可以改成 1 到 8，超出範圍顯示錯誤、不能核准。（D-U24-7）

**納入（adopt）**

17. lead 用 `pdx adopt <ref>` 申請把**同一台主機**上正在跑的 session 納進 team。只接受 ref 的寫法（`_xxxxxx`、`<主機>/_xxxxxx`、`pdx peers` 印出的 `<主機>/<名字> [xxxxxx]`）；只寫名字是用法錯誤（exit 2）；對方接力過、ref 換了，用舊 ref 也找得到。（D-U24-2；OQ12）
18. **申請前就被擋下（不會出現核准框）**：不是 lead、對方在別台主機、找不到對方、納入自己、對方是 lead、對方已經是 member、對方已有一筆待核准的納入申請、team 已滿。CLI exit 13，錯誤碼是 stderr 最後一個字。（D-U24-2；OQ11）
19. **納入核准框**：標題「<主機>：<lead> 想把 <對方> 納入 team」；顯示 lead 與對方的名稱、位址、目錄、tmux、team id、倒數；說明「核准後這個 session 會成為 member：自我接力關閉，接力由 lead 安排；它的模型不變。」；核准／拒絕各一鍵；沒有可編輯的欄位；可以縮小成角落的膠囊；系統通知標題「<主機>：<lead> 想納入 <對方>」；在這個視窗核准或拒絕後切到 lead 的 tab（U22）。（D-U24-2；PL-2a）
20. **核准時情況已經變了**（對方變成 lead、已被別人納入、行程已結束、team 滿了…）：申請改為取消，各視窗跳「已取消」的 toast；`pdx adopt` exit 13 並帶原因碼。（D-U24-2；決定 4）
21. 無人值守開著時，納入申請自動通過，不出現核准框。（D-U24-2、U23）
22. **被納入的 session 收到的訊息**（寄件者顯示為 lead）：「[pdx team] 你已成為 <lead 位址> 的 member（team <id>）。自我接力已關閉，接力由 lead 安排；回報請送 <lead 位址>。」送不到時 daemon 會在 10 分鐘內重送，所以偶爾可能收到兩則一樣的；10 分鐘還送不到就放棄並記 log。（D-U24-2；決定 7）
23. 被納入後：這個 session 到了接力門檻不再跳自我接力申請，它原本的模型與 effort 不變；`pdx team` 會列出它（`--json` 的 origin 是 adopted）；它占 team 的名額。（D-U24-2）
24. 一個 session 若曾是某個**已結束** team 的 member，照樣可以被納入。（OQ10）
25. `pdx adopt` 必須在前景等待（Bash timeout 600000），stderr 印「申請納入 <ref> 中（<id>），請在 Purdex 介面核准；這個呼叫必須在前景等待（Bash timeout 600000）」；結果：核准 0、拒絕 10、逾時 11、取消 12、規則擋下 13。（D-U24-2；spec §14）

**釋出與關閉**

26. **釋出**：lead 用 `pdx release <ref>`，不需要核准；那個 session 繼續跑、回到一般 session；它收到「[pdx team] <lead 位址> 已讓你離開 team <id>：你現在是一般 session，自我接力依這台主機的設定。」；之後能不能自我接力，看主機的自我接力開關與它自己的 `/relay` 暫停。正在接力中的 member 不能釋出（exit 13 `relay_open`）；釋出一個已經不是 active 的 member 不報錯，回傳它現在的狀態。（D-U24-3；OQ19；決定 14）
27. 被釋出的 session 到了接力門檻時，mod 會重新向 daemon 確認角色（每分鐘最多一次），確認已是一般 session 後就照一般 session 自我接力。（PL-1g）
28. **關閉被納入的 member**（`pdx kill <ref>`）：只結束那個 member 的 Claude Code 行程，tmux session 與 shell 留著（那是使用者自己的）；對方行程早已不在時標記為已結束；權限不足等錯誤時不標記、回報錯誤。關閉 spawn 出來的 member 不變（結束它自己的 tmux session）。（D-U24-3）
29. **lead 自己判斷某個 member 不需要時**：必須先用 AskUserQuestion 問使用者，點名那個 member，選項恰好三個「釋出／關閉／保留」，照答案做；使用者直接要求釋出或關閉時，lead 直接做、不再問。（D-U24-4）
30. 使用者要某個 session 升級成 lead：skill 叫它用原本的 `pdx lead request`（一鍵核准；無人值守開著時自動通過）。（D-U24-1）

**側欄縮排（D-U24-5）**

31. tab 放在**左側或兩側**時，側欄每個工作區的 tab 清單裡，member 的 tab 緊接在 lead 的 tab 底下、往內縮一層；lead 的 tab 多一個收合箭頭，收合時顯示 member 數量。（D-U24-5）
32. lead 的 tab 不在這個清單裡時（lead 在別台沒顯示的主機、或已結束），member 的 tab 留在原位，下面一行小字「<lead> 的 member」。（D-U24-5）
33. 正在看的 tab 若在收合裡，會自動展開。收合狀態每個視窗各自記，重開 App 後全部展開。（OQ15）
34. 拖曳：member 的 tab 可以在 lead 底下互換順序；把它拖出去、或把不屬於 team 的 tab 拖進來，會彈回原位；拖到別的工作區照常可以。（OQ13）
35. 這個縮排永遠開著，不受下面「分頁群組顯示 team」設定影響；Workers 清單不變。（D-U24-5、D-U24-6）

**上方 tab 列的群組（D-U24-6）**

36. 每個至少有一個開著 tab 的 team 有一個群組：群組最前面一個小標籤，寫 lead 的名稱（有 title 用 title，沒有就用位址裡的名字）；群組裡每個 tab 底下有一條該群組顏色的線。（D-U24-6）
37. **顏色**：8 種淡色（玫瑰、琥珀、萊姆、翠綠、青、天藍、紫、洋紅）依 team 自動分配，重開不變，刻意和主機色不同。（D-U24-6；OQ20）
38. **順序**：lead 的 tab 在最前，member 的 tab 接在後面（一開始大致是加入的順序，可以在群組內拖曳調整並記住）；整個群組放在其中第一個 tab 原本的位置；不屬於 team 的 tab 位置不動。（D-U24-6；OQ13）
39. 一個 tab 只要有任何一個分割窗格顯示 lead 或 member，就算進群組；同一個 tab 同時有兩個 team 的窗格時，以主窗格那個 team 為準。（決定 11）
40. **收合**：點標籤，群組收成只剩標籤（顯示 +N）；再點展開；切到收合群組裡的 tab 會自動展開；要收合「目前正在看的 tab」所在的群組時，會先切到群組外最近的 tab，沒有可切的就不收合。（D-U24-6；OQ15）
41. **拖曳**：群組內可以調順序；把群組裡的 tab 拖出去、或把外面的 tab 拖進群組，都會彈回原位；標籤本身不能拖。（D-U24-6）
42. **釘選的 tab 不分組**（參考 Chrome：釘選的 tab 會離開群組）。（決定 10；deviation 10）
43. 每個 tab 原本的標示——主機徽章、狀態點、等待核准的手、未讀紅點、離線、鎖——都照舊顯示。（D-U24-6）
44. member 被釋出或結束時立刻離開群組；沒有任何開著 tab 的 team 沒有群組。（D-U24-6）
45. **設定**：設定 → 介面 → 分頁 →「分頁群組顯示 team」，預設開；關掉後上方 tab 列完全回到原本的樣子（側欄縮排不受影響）；這個設定只存在這台裝置，不同步到其他裝置。（D-U24-6；OQ17）

**上線順序對使用者的影響**

46. 納入功能的 daemon、CLI、skill 一次一起上線，而且納入核准框會先上 App，所以不會出現「有納入申請卻沒有核准框可以按」的空窗。（決定 3）

## Open questions (ruled 2026-10-08; the rulings are in "Coordinator decisions" below)

Each had a recommended default. Decisions 1–20 below changed or confirmed several; the text of each question is kept for the record.

1. **A close decided by unattended: toast it?** (PU-2b.) Default: no toast. — *Ruled: default.*
2. **Turning the switch off when auto-approvals exist: open the list?** Default was yes, once. — *Ruled otherwise (decision 19): nothing opens; the list opens only from ▾.*
3. **The button's shape.** Default: a split pair — the toggle, then ▾ for the list. — *Ruled: default.*
4. **Approve at boot while on.** Default: yes. — *Ruled: default, plus the per-tick reconciliation (decision 5).*
5. **A request whose daemon approve fails a rule.** Default: a lead row stays open and shows its dialog; an adopt row closes `cancelled` with its code. — *Ruled: default; at create-time a refusal inside the transaction is the create's 409 (decision 1).*
6. **The PUT requires an app client descriptor and stores `changed_by`.** Default: yes. — *Ruled: default, as audit only (decision 6).*
7. **Where D-U24-5's indentation goes.** Default: the sidebar's per-workspace tab list; the Workers list and the New Tab session list unchanged. — *Ruled: default; d3 wrote it into spec D-U24-5 (decision 21).*
8. **Who sends the adopt / release notices.** Default: the daemon, in process, from the lead's inbox. — *Ruled: default, made at least once (decision 7).*
9. **How `pdx kill` ends an adopted member.** Default: SIGTERM its process. — *Ruled: default with re-verification and errno rules (decision 13); d3 wrote it into spec D-U24-3 (decision 21).*
10. **A target still `active` in an ended team.** Default: that stale row becomes `released` inside the approve. — *Ruled: default.*
11. **One open `adopt` per target.** Default: yes, `request_open`. — *Ruled: default; re-asserted at approve (decision 4).*
12. **Accepted target forms.** Default: refs only plus the lineage tier; a bare name is exit 2. — *Ruled: default.*
13. **Order inside a group.** Default: the lead first, then members in stored relative order. — *Ruled: default.*
14. **Pinned tabs.** Default: never grouped. — *Ruled: kept, as deviation 10 (decision 10).*
15. **Collapse state and the active tab.** Default: per window, in memory; collapsing the active tab's group first activates the nearest tab outside it. — *Ruled: default.*
16. **Which tab belongs to a team.** Default was the primary pane only. — *Ruled otherwise (decision 11): any pane; several teams → the primary pane's, else the first in layout order.*
17. **The setting's home and scope.** Default: a new 介面 subsection 「分頁」; device-local. — *Ruled: default.*
18. **The mod's role re-read (PL-1g).** Default: ship it after U1-1b. — *Ruled: a hard dependency (decision 16).*
19. **Release of a member that is not active.** Default: 200 with the row unchanged. — *Ruled: default.*
20. **Team palette values.** Default: the Tailwind 300 tier of eight hues. — *Ruled: default.*

## Deviations from spec

1. **The switch's route is `GET`/`PUT /api/team/unattended`** (team module), and its storage is the host-config key `unattended` (PU-1a), not a field of the `relay` row or a `PUT /api/hostconfig/*` route. D-U23-1 asks for host config "next to the relay switches" and PU-1 for a host-config route. The team module must run the D-U23-3 sweep in the same critical section as the write, and only the daemon may set `since`; a generic CAS PUT can do neither, and the `relay` row is shared with P9a-3's editors.
2. **A request created while the switch is on is written approved in one transaction and broadcast only as `closed`** (PU-1b1, PU-1b2, PL-1c). D-U23-2 says "broadcast … exactly like a manual approval": the close event and the audit row are the same; there is no `opened`, because the request is never open — which is what makes D-U23-6's "no dialog, no notification" hold for every client, old builds included.
3. **`PUT` requires an app `client` descriptor**, stored as `changed_by` (decision 6). The spec does not mention it.
4. **`AdoptPayload` carries more than the spec lists**: `team_id` and the target's `name`, `address`, `cwd`, `tmux`, for the card. Additive.
5. **An adopted member's row key is the adopt request's id, stored in `team_members.spawn_op`** (the table's primary key); the wire shows `spawn_op: ""` and `adopt_request`. D-U24-2 says "no spawn op".
6. **Security limitation (decision 6): D-U23-2's "set only through the Purdex App" is not technically enforced.** The App and `pdx` share one host token (spec §3.3, §6.5), and the PUT's `client` is the caller's own claim, so a same-uid agent can turn the switch on with `curl`. What stays, as U5b says of approvals ("告知與同意，不是安全邊界"): no `pdx` command, the skill's prohibition, the guard test, the broadcast to every window, and the audit (`changed_by` with the remote address, one log line per change). Hardening is the spec's §11 (signed client keys).
7. ~~`pdx kill` of an adopted member ends its process, not a tmux session.~~ **Folded into the spec** (decision 21): D-U24-3 now says closing an adopted member sends SIGTERM to its re-verified Claude Code process and keeps the tmux session and shell, following M30; a spawned member's `pdx kill` is unchanged. No longer a deviation. The one race left — the milliseconds between the re-verification and `kill(2)` — is stated in PL-1d2.
8. **The App reads a new `GET /api/team/roster` and `team.roster` events** (PL-1f). D-U24-5 names `GET /api/team`, which answers the calling session's own team only.
9. ~~D-U24-5 applies to the sidebar's tab list.~~ **Folded into the spec** (decision 21): D-U24-5 now names the sidebar's tab list and leaves the Workers list unchanged, with the reason from the PL-2 facts. No longer a deviation.
10. **Pinned tabs are never grouped** (decision 10). D-U24-6 says every team with an open tab has a group; it also says "參考 Chrome 分頁群組", and Chrome takes a tab out of its group when it is pinned. The pinned zone keeps its own drag rules (`TabBar.tsx:65-69`).
11. **The notices are attributed to the lead** (sent from its inbox, decision 7) and are **at least once** (a duplicate is possible). The spec says "the daemon sends".
12. **Release is blocked by any relay op in flight on the session**, not only a "member relay op": member relay ops arrive with P6, and a self op left over from before an adoption must block too. P6-2b's create is bound to it by contract (Contracts for later PRs 1).
13. **`team_members.ended_at` is also set by kill and gone**, not only by release; `notice_pending` / `notice_since` are new. Additive.
14. **`Approval.close_reason`** (new, `omitempty`) carries the code of an adopt cancelled at approve; the spec says the request "closes as `cancelled` with that code" without naming a field.
15. **A mod change in PL-1** (PL-1g, after U1-1b). The spec's PL-1 lists daemon, CLI and skill only.
16. **Spec §14's exit-13 row does not list the U24 codes** (`adopt_self`, `adopt_target_is_lead`, `adopt_already_member`, `adopt_target_not_found`) **or a `cancelled` adopt with a `close_reason`.** D-U24-2 says "409, exit 13", so the plan follows it; §14 needs the row when the relay spec's U-table is next edited (U23 / U24 fold-in).
17. **A stale `active` row of an ended team is marked `released`** when its session is adopted again (Open question 10).
18. **PL-2a merges before PL-1c** (decision 3), against the spec's PL-1 → PL-2 order, so the one-click card exists before the daemon can raise an `adopt` request.

---

## Coordinator decisions (2026-10-08, purdex-f0)

Binding. Each names where it landed.

1. **[critical] Create-time approval is one transaction, never open.** While the switch is on (read under `createMu`), a request is inserted and approved in one write transaction; its first committed state is `approved`; only `closed` is broadcast; a seam between the insert and the approve proves no open row is visible. → Review focus 1; PU-1b1 (`CreateApproved`, `CreateSelfRelayApproved`, `TestCreateApproved_NeverVisibleOpen`); PU-1b2 rules 1–2 and `TestCreate_UnattendedOnIsApprovedNeverOpen`, `TestRelayBegin_UnattendedOnClaimedNeverOpen`; PL-1c rule 3 and `TestAdoptCreate_UnattendedIsApprovedNeverOpen`; deviation 2.
2. **[critical] Adopt side effects hang on the shared winner point.** `announceClosed` → `afterApproved` for every approved close (decide, create-time, switch-on sweep, tick, boot). → Shared contracts "The winner point"; PU-1b2 (`afterApproved`); PL-1c rule 6 and `TestAfterApproved_RunsOnEveryApprovePath`; PL-1d1 `TestNotice_EveryApprovePathSends`; PL-1f `TestRoster_ChangedOnEveryAdoptApprovePath`.
3. **[critical] Deploy order.** PL-2a merges and fast-forwards before PL-1c; PL-1b + PL-1c + PL-1d1 + PL-1d2 + PL-1e are one deploy (daemon + CLI + `pdx setup`, the skill with them); PL-1f / PL-1g another batch. → Global constraints "Deploy batches"; PR table Needs / Deploy; PL-2a and PL-1c Deploy; deviation 18.
4. **Every refusal re-checked at approve,** `remote_unsupported` and `request_open` as invariants, one test per code; the registry read is the one thing outside the transaction, compensated by the gone-sweeper. → PL-1b `adoptApprovedIn`, rule 4, `TestCloseAdoptApproved_EachRefusalCancelsWithItsCode`; PL-1c rule 5, `TestAdoptDecide_EachRefusalIs409WithItsCode`, `TestAdoptDecide_TargetDiesAfterCommitIsMarkedGone`.
5. **Nothing stays open while the switch is on.** The sweeper's tick reconciles still-open `lead` / `self_relay` / `adopt` rows; PUT on still answers 200 with `pending`. → PU-1b2 `reconcileUnattended`, rule 6, `TestTick_ApprovesWhatTheSwitchOnSweepLeftOpen`; PU-1c rule 1, `TestUnattendedPut_PendingIsReportedAndTheTickFinishesIt`.
6. **D-U23-2's "only the App" is a stated security limitation,** aligned with U5b; the no-command rule, the skill, the guard test and the `changed_by` audit stay. → Global constraints; PU-1c rule 5; deviation 6.
7. **Notices at least once:** `notice_pending` / `notice_since` written at commit, cleared after a send, retried by the sweeper, given up after 10 minutes with a log line; idempotent text. → Shared contracts (columns, notices); PL-1b; PL-1d1 (whole PR) and `TestNotice_FailedSendIsRetriedByTheSweeper`; deviation 11.
8. **The D-U24-4 skill rule is pinned structurally** (the whole paragraph, the member named, exactly three options, the direct-request bypass) and ships in the same batch as release / kill. → PL-1e rule 6 and `TestSkill_EndMemberRuleIsPinned`; Deploy batches.
9. **The Workers list.** Read in the code: an execution can hold a role for at most one turn (the sweeper ends it), spawn never creates one; written as "the Workers list holds no team session; nothing to indent" and a spec clarification. *d3 ruled it into spec D-U24-5 (decision 21).* → PL-2 facts; PL-2c Goal; deviation 9 (folded).
10. **Pinned tabs stay ungrouped,** as a deviation with Chrome's behaviour as the reason. → PL-2b rule 2; PL-3b rule 2; deviation 10.
11. **A tab joins a team through any pane;** several teams → the primary pane's, else the first in layout order; tested. → PL-2b `teamRoleOfTab` and two tests.
12. **Release vs a member-relay begin.** This plan requires only `ReleaseMember`'s conditional UPDATE; P6-2b gets a contract (membership confirmed in the insert's transaction, exclusive with release), written here and synced into plan v3 P6-2b. → Contracts for later PRs 1; plan v3 P6-2b rule 1 and `TestRelayCreate_RacesReleaseOneWins`.
13. **The adopted member's kill:** re-verify pid + start time before the signal; `ESRCH` → gone; `EPERM` / other → error, nothing marked; mark killed only after a successful signal; M30 extended (exit time, registry update, the no-exit case). *d3 ruled it into spec D-U24-3 (decision 21).* → PL-1d2 rules 3–4 and four `TestKillAdopted_*`; Measurements M30; deviation 7 (folded).
14. **Released × host switch × pause** matrix for hello and begin. → PL-1d2 `TestRelease_HelloAndBeginMatrix`.
15. **Plan v3 synced in this commit:** P6-1 needs PL-1d1 and no longer builds the sender; P4b-4 needs PL-1a and reuses `remote_unsupported`. → plan v3 PR table rows P4b-4 and P6-1, sections P4b-4 and P6-1; Contracts for later PRs 2–3.
16. **PL-1g needs U1-1b merged.** → PR table; PL-1g; Intersections.
17. **The "while you were away" list is paged** (`?before=<ts>&limit=`, `truncated`, `next_before`; a page never splits a millisecond), with 「顯示更多」. → PU-1a `UnattendedView`; PU-1b1 `ListAutoApproved`; PU-1c GET and `TestUnattendedGet_PagesWithBeforeAndTruncated`; PU-2c.
18. **Sizes:** PU-1b and PL-1d are pre-split (PU-1b1 / PU-1b2, PL-1d1 / PL-1d2); every PR names a cut point; PL-1f's file count corrected to 11 (the winner hook replaces two call sites). → Global constraints; every PR's Size line; PR table.
19. **Switching off opens nothing** (the auto-open and Open question 2's default are dropped). → PU-2c rule 1 and `switching off opens nothing`; Open question 2.
20. **PL-1a needs P9 complete and PU-2c merged.** → PR table; PL-1a Goal.

Follow-up rulings the same day (purdex-d3, relayed by purdex-f0), folded into this revision:

21. **The U24 spec is revised in this plan PR** (`docs/specs/2026-10-08-lead-adopt-release-spec.md`): D-U24-5 = the sidebar's tab list, the Workers list unchanged with the reason from the code; D-U24-3 gains the close of an adopted member (SIGTERM to the re-verified process, tmux session and shell kept, per M30; a spawned member's `pdx kill` unchanged); §3 says so; D-U24-7 is added word for word, with a U25 row in §1. The plan's matching deviations become "folded into the spec". → the spec diff; deviations 7 and 9; decisions 9 and 13; Open questions 7 and 9.
22. **U25 / D-U24-7 in the plan.** Daemon half: an unattended lead approval's grant is `min(requested — unspecified counts as 3 —, 3)` members, in PU-1b2 (`unattendedGrant`, `TestUnattendedLeadGrant_IsMinOfRequestAndThree`, `TestDecide_ClickGrantIsNotCapped`, a mutation gate). Dialog half: a small PR of its own, PU-2d (`OpenApprovalDialog`'s member field prefilled 3, 「lead 申請 N 個」 beside it, tests and gates). → PR table; PU-1a constant; PU-1b2; PU-2d.
23. **A user-visible behaviour list** in Traditional Chinese, every item with its source, for d3 to review. → "User-visible behaviour (for coordinator review)".

Implementation rulings (U23 daemon batch, purdex-1f, 2026-10-08):

24. **PU-1b2 was cut at its cut point:** `reconcileUnattended`, the boot sweep and the once-per-row refusal log are PU-1b3 (#1969), shipped in the same U23 batch. → PU-1b2 / PU-1b3.
25. **The daemon never approves an overdue row:** an auto-approval's CAS also requires `deadline_at > now AND lease_until > now` (`Close.UnexpiredAt`); the overdue row is left to the expiry sweeper and is not counted as pending. A click's approve is unchanged (a click on a row overdue by less than one tick stays the person's decision). → PU-1b3.
26. **`GET /api/team/unattended` caps `limit` at 200** instead of answering 400; `before`/`limit` that are 0, negative or not a number are 400. → PU-1c.
27. **A switch value that cannot be read sends no snapshot** and keeps the subscriber (reconnecting would not repair it; the App's GET gets the 500). → PU-1c.
28. **The guard test is stricter than planned:** no pdx source names the route or `UnattendedRoute`, no `hooks/*.js` contains "unattended", SKILL.md never writes the route; `main()` strings are checked through the AST. → PU-1c.
29. **`team.unattended` `changed` is sent strictly to every subscriber** (`BroadcastStrict`): a subscriber that cannot take it is closed so it reconnects for the snapshot; a dropped frame would leave a window showing the wrong switch (D-U23-6). → PU-1c.
30. **A PUT whose write took effect answers 200** even when the list cannot be read, with `approved: []` and `list_failed: true`; the client GETs the list. → PU-1a wire (additive), Shared contracts (routes, `UnattendedView`), PU-1c Interfaces; PU-2b `toggleUnattended` reads only the state from a PUT and never its `approved`.
31. **The skill's lead-mode paragraph says the answer may be an automatic approval** under unattended mode, which the agent never turns on and never asks for. → PU-1c.

**The other Open questions** take the plan's recommended defaults: 1, 3, 4, 5, 6, 8, 10, 11, 12, 13, 15, 17, 19, 20 (each marked *Ruled* in "Open questions"). Open questions 7 and 9 follow their defaults, now written into the spec (decision 21); 2, 14, 16 and 18 are decided by decisions 19, 10, 11 and 16.

## Codex review of this plan

One round, plan + both specs (job output `scratchpad/u23u24-plan-review.txt` of session 8fff4c6b): **3 critical / 15 important / 2 minor, all adopted** through the decisions above.

| # | Severity · confidence | Finding | Disposition |
|---|---|---|---|
| 1 | critical · 0.98 | A snapshot between the create's insert and its auto-approve shows a dialog / notification (D-U23-6). | Adopted → decision 1: one transaction, never open; seam tests. |
| 2 | critical · 0.97 | Adopt notice and roster wired to the create route; decide, switch-on sweep and boot would miss them. | Adopted → decision 2: `afterApproved` on the winner point; five-path tests. |
| 3 | critical · 0.96 | PL-1c's deploy needs PL-2a, which the table put after it; PL-1d/1f/1g deploys would carry PL-1c. | Adopted → decision 3: PL-2a before PL-1c; batch A / batch B. |
| 4 | important · 0.97 | The approve-time re-check misses `remote_unsupported` / `request_open`; target liveness is outside the transaction; one code tested. | Adopted → decision 4: eight codes, invariants, the stated boundary, the gone-after-commit test. |
| 5 | important · 0.96 | A partial switch-on sweep leaves requests open with the switch on; create-time failures never retried. | Adopted → decision 5: per-tick reconciliation; `pending` in the PUT answer. |
| 6 | important · 0.94 | "Only the App can turn it on" is not enforced; the plan's safety claim is too strong. | Adopted → decision 6: deviation 6 (security limitation). |
| 7 | important · 0.94 | Notices are best effort and can be lost forever. | Adopted → decision 7: outbox columns, sweeper retry, 10-minute give-up. |
| 8 | important · 0.94 | The D-U24-4 skill test is substrings only; the skill deploys after the capabilities. | Adopted → decision 8: pinned paragraph test; same batch. |
| 9 | important · 0.93 | The Workers list is excluded from D-U24-5 without a reason. | Adopted → decisions 9 and 21: the code shows no durable team session there; spec D-U24-5 revised by d3. |
| 10 | important · 0.93 | Pinned tabs never grouped, not listed as a deviation. | Adopted → decision 10: deviation 10 with Chrome's behaviour. |
| 11 | important · 0.92 | Team membership of a tab reads the primary pane only. | Adopted → decision 11: any pane; tie rule; tests. |
| 12 | important · 0.92 | Release vs a future member-relay begin is not exclusive. | Adopted → decision 12: P6-2b contract, synced into plan v3. |
| 13 | important · 0.92 | SIGTERM without re-verifying the pid; errno and partial failures; M30 too thin. | Adopted → decisions 13 and 21: re-verify, errno rules, extended M30; spec D-U24-3 revised by d3. |
| 14 | important · 0.91 | No released × pause test. | Adopted → decision 14: the hello / begin matrix. |
| 15 | important · 0.90 | P6-1 / P4b-4 "pulled forward / reused" without plan v3 edits or Needs. | Adopted → decision 15: plan v3 edited in this commit. |
| 16 | important · 0.90 | PL-1g's U1-1b prerequisite is not in Needs. | Adopted → decision 16. |
| 17 | important · 0.89 | The 200-row cap drops auto-approvals silently. | Adopted → decision 17: cursor pages, `truncated`, 「顯示更多」. |
| 18 | important · 0.90 | PL-1d / PL-1b / PL-3b near the limit with no cut point; PL-1f's file count wrong; others need cut points. | Adopted → decision 18: two pre-splits, a cut point per PR, PL-1f recounted. |
| 19 | minor · 0.90 | Auto-opening the panel on switch-off is not in the spec and would fire on every window. | Adopted → decision 19: removed. |
| 20 | minor · 0.89 | "After P9 and U23" is only table order, not a Needs gate. | Adopted → decision 20. |

---

## Display-first reorder (2026-10-08)

> **Binding over the PR table and the PL sections above** for the PRs it names. Asked by the coordinator (purdex-1f): the user cannot see who runs whom, and named the two surfaces they want first — the sidebar's tab list with members indented under their lead (PL-2c) and Chrome-style groups in the tab bar (PL-3a / PL-3b). Written against origin/main **`286ab4af`** (alpha.605: U23 daemon batch PU-1a…PU-1c merged; PU-2a…2d not merged). Every `file:line` here was read on that commit.

**Why the display does not need adopt.** The roster lists teams and their members from team.db, and team.db already holds every member there is today: the only writer of `team_members` is the spawn runner (`internal/module/team/spawn_register.go:144`, `InsertMember` at `team_store.go:140-156`), each row carrying `ref`, `title`, `tmux_session` (`team_store.go:46-67`); `teams` carries `lead_session_id`, `lead_ref` and the approving `request_id` (`team_store.go:34-44`), whose request row holds the lead's origin (`approval_requests.origin_json`, `store.go:65`). Adopt and release add a second kind of member and two more ways to leave; they add rows and call sites to the roster, not its shape.

### The display path

| PR | Content | Needs | ≈ lines / files | Deploy |
|---|---|---|---|---|
| **PL-1a′** | PL-1a's wire contract **except `AutoApprovable(KindAdopt)`** (moved to PL-1c), plus `memberView` setting `Origin` | P9 complete | 440 / 6 | none (types only; rides the next daemon deploy, inert) |
| **PL-1f′** | Roster over today's team.db: `GET /api/team/roster`, `team.roster` snapshot / changed; spawned members only, `origin` always `"spawned"` | PL-1a′ | 560 / 10 | **daemon** (coordinator), alone |
| **PL-2b** | unchanged | PL-1f′ (wire only) | 700 / 8 | SPA |
| **PL-2c** | unchanged | PL-2b | 650 / 9 | SPA |
| **PL-3a** | unchanged | — (no code from PL-2b; merges after PL-2b) | 520 / 11 | SPA |
| **PL-3b** | unchanged | PL-3a, PL-2c | 760 / 5 | SPA |

The adopt / release path then runs in its own order, after U23's SPA (PU-2a…2d, member β): **PL-1b → PL-2a → PL-1c → PL-1d1 → PL-1d2 → PL-1e → PL-1f″ → PL-1g**, with **U24 batch A = PL-1b + PL-1c + PL-1d1 + PL-1d2 + PL-1e + PL-1f″** in one deploy (decision 3 unchanged: PL-2a merged and fast-forwarded before PL-1c) and **batch B = PL-1g** alone (still needs U1-1b). The PR table's PL-1f row is replaced by PL-1f′ + PL-1f″.

### PL-1a′ — what changes against PL-1a

1. **Needs: P9 complete only.** Decision 20 gated PL-1a on PU-2c to keep spec §4's "after P9 and U23" for the adopt *feature*; PL-1a′ adds types and constants and no behaviour, so the gate moves to PL-1b (the first PR that writes adopt state) — the adopt feature still starts after U23.
2. **`AutoApprovable(KindAdopt)` moves to PL-1c**, with `TestAutoApprovable_Adopt`, its mutation gate, and the flip of `"adopt": false` in `internal/team/wire_unattended_test.go:112-116` (which pins today's set and says "PL-1a adds adopt"). Reason: PL-1a′ is deployed with PL-1f′'s daemon, months of other deploys may follow before batch A, and the unattended sweep, the tick's reconciliation and boot all filter on `AutoApprovable` (`internal/module/team/unattended.go:263, 291`). No `adopt` row can exist before PL-1c (`handler.go:119-128` refuses every kind but `lead`), so the change is harmless either way, but keeping it out makes PL-1a′ a pure wire PR whose deploy changes nothing, and puts the switch-covers-adopt rule in the PR whose tests exercise it.
3. `wire_unattended.go` is therefore **not** touched. Files: `wire_adopt.go`, `wire_adopt_test.go`, `wire.go`, `wire_team.go` (PL-1a's four without `wire_unattended.go`), plus item 5's `team_handler.go` and `team_handler_test.go` = 6 files, ≈ 440 lines.
4. Everything else — `KindAdopt`, the five codes, `AdoptPayload`, `MemberReleased`, the two origins, the notice constants and formats, `Approval.CloseReason`, `CreateApprovalRequest.Target`, `Member.Origin` / `EndedAt` / `AdoptRequest` — as PL-1a. `Member.Origin` is always present (rule 1): `GET /api/team` starts answering `"origin":"spawned"` for every member as soon as it deploys; `pdx team --json` passes it through, which is additive.
5. **`memberView` sets `Origin`** (codex plan review 1): the only place a `team.Member` is built for the wire is `memberView` (`internal/module/team/team_handler.go:75-78`), which names every field; without a line there `GET /api/team` would answer `"origin":""`, breaking rule 1. PL-1a′ sets `Origin: team.MemberOriginSpawned` there (every row is spawned today, see PL-1f′ "Origin"), and `TestTeamGet_MemberOriginIsSpawned` asserts it on `GET /api/team`; mutation gate: drop the line → red. PL-1f″ switches it to the column, as it does for the roster.

### PL-1f′ — what changes against PL-1f

**Content kept:** `internal/team/wire_roster.go` + test (the types of PL-1f, unchanged: `RosterSession`, `RosterMember`, `TeamRoster`, `Roster`, `RosterEventValue`, `RosterEventType`); `internal/module/team/roster.go` + test; `module.go` (route on the general chain, `OnSubscribe(m.sendRosterSnapshot)` next to `:361-362`, `rosterMu`, `lastRosterHash`); behaviour rules 1–4 of PL-1f, with rule 3 amended: **`changed` is sent with `BroadcastStrict`** (`internal/core/events.go:394`), as `team.unattended` is (decision 29) — the roster is state, a dropped `changed` would leave a window grouping wrong until the next change, and a subscriber that cannot take it is closed so it reconnects for the snapshot (suggested by member β). Added test `TestRoster_ChangedIsStrict` (a full subscriber is closed, not skipped); mutation gate: plain `Broadcast` → red.

**Call sites — today's writes only** (6 files instead of 7):

| Write | Where (at `286ab4af`) |
|---|---|
| a team is created (a lead approve, on every path: click, create-time, switch-on sweep, tick reconciliation, boot) | `afterApproved` (`unattended.go:77`), reached from the winner point `announceClosed` (`module.go:446-452`); the team row is inserted inside the approve (`closeLeadApprovedIn`, `team_store.go:316-350`). `self_relay` approvals pass here too; the hash gate makes them free. |
| a spawned member joins | `spawn_register.go` `spawnFinish` (after `InsertMember` **and** the title claim) |
| a spawned member is killed | `team_handler.go:171` (after `MarkMemberKilled` wins) |
| a team ends; a member is marked gone; the liveness tick | `sweeper.go:152` (`EndTeam`), `:235` (`MarkMemberGone`), the `checkLive` tick (`:52`) |
| a relay's `cleared` moves the lead's or a member's session | `relay_report.go` `afterReport` (`:170-176`; the move itself is `relay_store_report.go:89-93`) |

`kill_adopted.go` and `release_handler.go` do not exist yet; their calls come with PL-1f″.

**Origin:** `RosterMember.Origin` is always `team.MemberOriginSpawned`, the truth for every row today (one writer, the spawn runner). It is a constant in `roster.go` with a comment naming PL-1f″ as where it becomes the column. The wire shape is final from the first deploy, so the SPA never sees it change. (Omitting it was the other option; rejected because PL-1f made it always-present and an older-shape roster would need a reader branch.)

**Stored fallback** (rule 2, when the registry has no live entry): member — its row's `ref`, `title`, `tmux_session`; lead — `teams.lead_ref` and the origin decoded from its `request_id`'s `origin_json` (address, title, name, tmux name before `:`), `live:false`.

**Tests — renamed or narrowed, none weakened:**
- `TestWireRoster_JSONShapes` — unchanged.
- `TestRoster_LiveTeamsActiveMembersWithTmuxNames` — a lead and two spawned members (`tm-…`); the adopted member's case moves to PL-1f″.
- `TestRoster_KilledGoneAndEndedTeamsAreOut` (was `…ReleasedKilledGone…`): `released` cannot be written before PL-1b; PL-1f″ adds it back.
- `TestRoster_SnapshotToEveryNewSubscriber` — unchanged.
- `TestRoster_ChangedOnEveryLeadApprovePath` (was `…EveryAdoptApprovePath`): click, create-time, switch-on sweep, tick, boot → one `changed` each — all five paths exist for `lead` since PU-1b3 (decision 24), so decision 2 is tested now instead of later.
- `TestRoster_ChangedAfterKillSpawnAndTeamEnd` (was `…AfterReleaseKillSpawn…`).
- `TestRoster_TickBroadcastsOnlyWhenItChanged`, `TestRoster_ClearedMovesTheLeadsSession` — unchanged.
- **Added:** `TestRoster_OriginIsSpawned` (every member `origin:"spawned"`), `TestRoster_StoredFallbackWhenNotLive` (lead from the request's origin, member from its row, `live:false`).

**Mutation gates:** include `killed` members → `…AreOut` red; broadcast from the decide handler instead of `afterApproved` → `…OnEveryLeadApprovePath` red (the create-time / sweep / boot cases); broadcast on every tick → `…OnlyWhenItChanged` red.

**Size.** ≈ 560 lines, 10 files. **Cut point** as PL-1f: the tick diff and the call sites outside `afterApproved` move to PL-1f′2. **Deploy.** Daemon alone (the coordinator; "U24 display batch"). **Coordination:** `internal/module/team` is also member β's line (U23); β's remaining work (PU-2a…2d) is SPA, but the call sites are confirmed with β before the PR opens.

**Review fold-in (codex R1 + attacker + critic):**
- *Spawn announces after the title claim* (R1-1): the roster's title comes from the title store, so `spawnFinish` signals once the claim has run (won or failed), not right after `InsertMember`; `TestRoster_SpawnAnnouncesTheClaimedTitle`.
- *A snapshot that cannot be built* (A-1) sends nothing and keeps the subscriber, but marks the publisher unsent, so the next successful sync (the next signal, at the latest the 10 s liveness tick) broadcasts the full roster as `changed`; the hash gate no longer hides an unchanged roster from a subscriber that never got it; `TestRoster_SnapshotBuildFailureIsRepairedByTheNextSync`.
- *`rosterChanged` is an async coalescing signal* (A-2a): a non-blocking send on a one-slot channel, answered by one publisher goroutine that Start launches and Stop joins (`roster_publish.go`), so no registry or naming I/O runs on the caller's thread — most callers hold `createMu` (`afterApproved`); `TestRosterChanged_NeverBlocks`, `TestRosterPublisher_CoalescesBursts`, `TestRosterPublisher_StopsWithTheModule`.
- *One registry read per build* (A-2b): `buildRoster` collects every lead and active-member session id and resolves them with the new `ResolveOriginsBySession` (one `ReadRegistry`, the single form's filter), instead of one read per session; `TestRoster_BuildResolvesOncePerBuild`.
- *A-3* (file responsibilities) was rejected by the critic as a finding, but the split below answers it anyway: PL-1f′ ships as two stacked PRs — **A** (wire types, `roster.go` = materialization, the route, `ResolveOriginsBySession`) and **B** (`roster_publish.go` = the signal, the publisher, the snapshot, all call sites).

### PL-1f″ — the roster's adopt / release delta (new, last PR of batch A)

**Needs** PL-1e (all adopt / release writes exist). **Content:** `roster.go` reads `team_members.origin` (PL-1b's column) instead of the constant; `release_handler.go` and `kill_adopted.go` call `rosterChanged()` after their winning write; an adopted member's `tmux_session` is its user's tmux session name (stored by PL-1b's insert). **Tests restored from PL-1f:** `TestRoster_LiveTeamsActiveMembersWithTmuxNames` gains the adopted member; `…AreOut` gains `released`; `TestRoster_ChangedOnEveryAdoptApprovePath` (the five paths, now for `adopt`); `TestRoster_ChangedAfterReleaseAndAdoptedKill`. **Mutation gates:** include `released` members → red; the origin constant left in place → the adopted member's `origin` red; broadcast from `handleCreateAdopt` instead of `afterApproved` → `…OnEveryAdoptApprovePath` red (PL-1f's gate, restored; codex plan review 2). `memberView` reads the column too (PL-1a′ item 5), with `TestTeamGet_AdoptedMemberOriginIsAdopted`. **Size** ≈ 220 lines, 7 files. **Deploy** in batch A, so the roster is right the moment adopt exists. Between PL-1f′'s deploy and batch A no adopted or released row can exist, so nothing is wrong in the meantime; the 10-second liveness tick would also catch a missed call site.

### PL-2b, PL-2c, PL-3a, PL-3b — unchanged, with these notes

1. **Merge before the daemon is harmless.** A daemon without PL-1f′ never sends `team.roster`; `useMultiHostEventWs` matches types one by one and falls through (`spa/src/hooks/useMultiHostEventWs.ts:178-230`), the roster store stays empty, every `teamRoleOfTab` is `null`, and the sidebar and tab bar render exactly as today. So the SPA PRs need PL-1f′ **merged** (the wire), not deployed.
2. **The WS branch is bound to its connection.** β's PU-2a binds its `team.unattended` branch to the `hostEndpointKey` the socket was opened with, so a frame from an old socket is dropped after the host is removed or re-pointed. PL-2b's `team.roster` branch has the same exposure and uses the same binding: if PU-2a has merged, reuse it; if not, PL-2b adds the same check and whichever merges second unifies them. Test `a roster frame from a socket of a removed / re-pointed host is dropped`.
3. **Tests stay as written.** Their rosters are seeded in `useTeamRosterStore`; `origin` is not read by any of them. `a released member (absent from the roster) is not blocked` is a data case (absent from the roster) and stays valid before release exists.
4. **Textual intersections:** PL-2b and β's PU-2a each add one type to `spa/src/lib/host-events.ts:4-18` and one branch to `useMultiHostEventWs.ts`; whichever merges second rebases. PL-3b's underline in `SortableTab.tsx` vs U1 lights v2 (member α): confirm with α before PL-3b starts (as "Intersections with other lines" already says).
5. **Acceptance before batch A** (PL-3b's acceptance, narrowed): a lead with two spawned members, three tabs: one group, chip = the lead's title, members after the lead; drag a member within → kept; out → snaps back; collapse, activate a member from the sidebar → expands; `pdx kill` one member → its tab leaves the group within one tick; Settings › 介面 › 分頁 off → plain bar. The adopt / release steps are re-run after batch A.

### User-visible behaviour, display-first

Items 31–45 of "User-visible behaviour" ship with PL-2c / PL-3b as written, with two differences until batch A: every member is one the lead spawned (`pdx spawn`), and item 44's "被釋出" cannot happen yet — a member leaves its group when it is killed or gone, or its team ends.

**A gap to rule on (not decided here):** a spawned member runs in a tmux session the daemon creates (`tm-…`, `spawn_tmux.go:23`); nothing in the SPA opens a tab for it, and both surfaces group **open tabs** only. Until the person opens the member's session from the session list, its lead's tab shows no members (codex plan review 3: the reorder's data path works, but the display may stay invisible). Not ruled here: it goes into the user confirmation pack below.

### Gate: the interface PRs wait for the user (coordinator, 2026-10-08)

The user asked to confirm the team interface in detail first. **PL-2c, PL-3a and PL-3b are not implemented until the user has confirmed them.** The data path proceeds: PL-1a′ → PL-1f′ → PL-2b. Before PL-2c the implementer hands the coordinator a confirmation pack — every user-visible rule of items 31–45 in plain words with a concrete scenario (e.g. "a lead with three members, one of them closed: what the sidebar shows, what the tab bar shows"), the open choices with a recommended value and its reason (the open-tab gap above among them), and a clickable mock under `docs/pages/` — and waits. Whatever the user rules is folded into this plan before PL-2c starts.

### Codex review of this reorder

One round (`task-muznye8r-9trvm2`, plan + spec): 0 critical / 2 important / 4 minor.

| # | Severity · confidence | Finding | Disposition |
|---|---|---|---|
| 1 | important · 0.99 | `memberView` (`team_handler.go:75`) builds `team.Member` without `Origin`; `GET /api/team` would answer `origin:""`. | Adopted → PL-1a′ item 5 (6 files, test + gate); PL-1f″ reads the column there too. |
| 2 | minor · 0.99 | PL-1f″ lost PL-1f's "broadcast from `handleCreateAdopt`" gate. | Adopted → restored in PL-1f″. |
| 3 | important · 0.97 | Spawned members have no tab, so the display may be invisible until the person opens them. | Recorded; goes to the user confirmation pack (gate above), not ruled here. |
| 4 | minor · 0.99 | PL-1a′'s file count double-counted the test. | Adopted → recounted (with item 5: 6 files). |
| 5 | minor · 0.98 | Pinned tabs ungrouped vs D-U24-6. | Already deviation 10 / decision 10; no change. |
| 6 | minor · 0.97 | Call sites, the `AutoApprovable` move, sizes: no error found. | No change. |

Also folded in from member β (U23): `team.roster` `changed` via `BroadcastStrict` (PL-1f′) and the WS branch bound to its connection (PL-2b note 2).
