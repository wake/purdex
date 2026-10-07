# Unattended mode (U23) and lead adopt / release / team display (U24) — Implementation Plan

> **Status (2026-10-08):** draft for one codex round (plan + both specs together), then the coordinator's rulings on "Open questions". Written against origin/main **`e47c1f35`** (alpha.596, after P9b-2 #1922 and the host-config GET change #1914). Every `file:line` below was read on that commit. P9a-3 (Hosts › 接力 editors) is not merged on it.
> **Source:** spec `docs/specs/2026-10-08-unattended-mode-spec.md` (U23, D-U23-1…7, PU-1 / PU-2), spec `docs/specs/2026-10-08-lead-adopt-release-spec.md` (U24, D-U24-1…6, PL-1 / PL-2 / PL-3), the main spec `docs/specs/2026-10-06-lead-team-relay-spec.md` (§6.2 approvals, §6.5 the audit layer, §6.6 the hook lock, §7 team, §8.7 switches, §14 exit codes), plan v3 `docs/specs/2026-10-06-lead-team-relay-plan-v3.md` (its "Global constraints" and binding coordinator decisions apply here unchanged), and the line's memory `kickoff_lead_team_relay.md`.
> **Format:** plan v3's compact format — contracts, rules, named tests and mutation gates; no full code blocks. The implementer writes the code test-first from these contracts.
> **Order (spec):** PU-1 → PU-2 → PL-1 → PL-2 → PL-3. Each is pre-split below; the PR table is the merge order.

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development (recommended) or superpowers:executing-plans. Each PR is TDD, one task per commit. Before you start a PR, re-verify its `file:line`s against main: the P9 line, the interface-unification line (U1) and #1866 all move fast.

**Goal.**
- **U23:** one title-bar button turns 無人值守模式 on or off on every shown host. While it is on, each host's daemon approves `lead`, `self_relay` (and, from PL-1, `adopt`) requests itself, in the same compare-and-set a click uses, recorded with the decider `unattended`; the App shows no dialog and no notification for them, and lists them when the person comes back.
- **U24:** a lead adopts a running session as a member (`pdx adopt <ref>`, one click or unattended), releases one (`pdx release <ref>`), and asks the user before ending a member on its own judgement (skill). The App shows teams: an `adopt` card, member tabs under their lead in the sidebar's tab list, and Chrome-style tab groups in the tab bar with a setting to turn them off.

---

## Global constraints

Plan v3's "Global constraints" (`plan-v3.md:15-44`) apply as written: ≤ 800 diff lines **and** ≤ 20 files per PR; Go `go test ./<pkg>/ -race` + `gofmt -l`; SPA `npx vitest run <path>`, `pnpm run lint`, `npx tsc -p tsconfig.app.json --noEmit`, `pnpm run build`; mod `claude plugin validate|test cmd/pdx/plugin/purdex` + `go test ./cmd/pdx/plugin/`; `git checkout -- pdx` after `go build ./cmd/pdx/` (#1699); English commits with the session's attribution line; `git commit --only` for parallel subagents; spec strings verbatim; SPA strings in `spa/src/locales/{en,zh-TW}.json`; errors on the CLI with the code as the last stderr token; times in unix ms; never print a token; the restart-aware `daemonclient` with a 35 s attempt timeout and `Idempotent()` on create POSTs; the deploy tags (daemon / CLI / setup / SPA / none) and purdex-d3's binding deploy asks (ask d3 and check running workers before every deploy; back up `~/.claude/settings.json` 0600 and key-path-diff it around every `pdx setup --agent cc`); throwaway sessions only in acceptance, no flag left in `~/.config/pdx/hooklocks/`.

Additions for this plan:
- **No new tab-hosted component.** Every component added here is app-level (title bar, dialog host, sidebar, tab bar). Each PR still says so explicitly, keeps any per-window state in a store outside the component, and tests an unmount/remount where the component can unmount (repo `CLAUDE.md` checklist).
- **Nothing in `pdx` and nothing in the mod turns unattended on** (D-U23-2). A guard test pins it (PU-1c).
- **Older clients must stay silent.** Every new wire field is additive (`omitempty`), every new event type is one an older SPA ignores (`spa/src/hooks/useMultiHostEventWs.ts:219-243` matches types one by one and falls through), and an older SPA drops an unknown approval kind row by row (`spa/src/lib/team/approval-ws.ts:43,52`).

## Review focus

1. **The daemon decides, through the decide path itself (D-U23-1).** An auto-approval runs the same store CAS (`CloseLeadApproved` / `CloseSelfRelayApproved` / `CloseAdoptApproved`) as a click, through one shared `approve()` both callers use; the decider is `{kind: "unattended"}`. → PU-1b, PL-1c.
2. **Approved at create ⇒ no `opened`.** A request the daemon approves while creating it is never broadcast as open, so no client draws a dialog or raises a notification, old builds included. → PU-1b.
3. **Switch-on approves what is open, atomically with creates (D-U23-3).** The switch write and the sweep run under `createMu`, so every request is either created before the switch (and swept) or after it (and approved at create). Hook kinds are never touched. → PU-1b, PU-1c.
4. **The safety layer (D-U23-2).** Admin token only (peer token 401), no `pdx` command, no mod call, skill forbids, every change audited (`changed_by`, address, log line) and broadcast. → PU-1c.
5. **Adopt re-checks at approve, in one transaction (D-U24-2).** Every refusal is checked again under the write lock; a failed re-check closes the request `cancelled` with its code; a session never holds two live roles. → PL-1b, PL-1c.
6. **Release and kill never touch what is not the member's.** Release blocks on any relay op in flight; an adopted member's kill ends its own Claude Code process, never the user's tmux session. → PL-1d.
7. **Groups are derived, never stored (D-U24-6).** Group membership comes from the roster and the tab's tmux session; a drop outside a group snaps back without writing the order. → PL-2b, PL-3b.

---

## PR table

| PR | Content | Needs | ≈ lines / files | Deploy |
|---|---|---|---|---|
| **PU-1a** | Wire contract of unattended (`relay.unattended.v1`, `team.unattended` event, decider, state DTOs); host config key `unattended` with its reader / writer (no generic route) | main | 420 / 5 | none |
| **PU-1b** | `approve()` shared by decide and the daemon; approve at create (lead) and at begin (self_relay) while on, with no `opened`; `sweepUnattended` (switch-on, boot); `ListAutoApproved` | PU-1a | 700 / 7 | daemon (with PU-1c) |
| **PU-1c** | `GET`/`PUT /api/team/unattended` (admin only, client required), `team.unattended` snapshot + changed events, capability `relay.unattended.v1`, skill line, guard tests | PU-1b | 650 / 9 | daemon + setup |
| **PU-2a** | SPA data: unattended API, per-host store, support probe from `/api/info`, WS branch | PU-1c | 520 / 13 | SPA |
| **PU-2b** | SPA: aggregate (off / on / partial / none), fan-out, the title-bar toggle with its tooltip; no toast for an unattended close | PU-2a | 700 / 12 | SPA |
| **PU-2c** | SPA: the ▾ "while you were away" panel; opened once on a switch-off with auto-approvals | PU-2b | 400 / 6 | SPA |
| **PL-1a** | Wire contract of adopt / release (`adopt` kind, payload, codes, `released`, member `origin`, `close_reason`, notices) | PU-1a | 420 / 5 | none |
| **PL-1b** | team.db: `close_reason`, `team_members.origin` / `ended_at` (ensureColumn); `CloseAdoptApproved` with every re-check; `ReleaseMember`; `ended_at` on kill and gone | PL-1a | 700 / 8 | daemon |
| **PL-1c** | `POST /api/team/approvals {kind:"adopt"}` (target resolution, refusals, one open per target), decide of `adopt`, unattended covers `adopt`; resolver `ResolveOriginByRef` / `InboxOf` | PL-1b, PU-1b | 720 / 8 | daemon (after PL-2a merged) |
| **PL-1d** | Peers in-process sender (pulled forward from P6-1); adopt and release notices from the lead's inbox; `POST /api/team/release`; kill of an adopted member by its process | PL-1c | 760 / 11 | daemon |
| **PL-1e** | CLI `pdx adopt`, `pdx release`; exit codes; skill text for D-U24-1…4 | PL-1d | 700 / 7 | CLI + setup |
| **PL-1f** | `GET /api/team/roster` + `team.roster` snapshot / changed events (host-wide teams for the App) | PL-1d | 550 / 12 | daemon |
| **PL-1g** | Mod: a cached `member` role is re-read at the threshold, so a released or ended-team member can self-relay again | PL-1d | 160 / 2 | daemon + setup |
| **PL-2a** | SPA: the `adopt` card in `ApprovalDialogHost`, its notification and kind label | PL-1a | 480 / 10 | SPA |
| **PL-2b** | SPA: roster parse + `useTeamRosterStore` + WS branch; pure team-tab layout (blocks, display order, orphan hint, block-aware reorder) | PL-1f | 650 / 8 | SPA |
| **PL-2c** | SPA: sidebar tab list — member tabs under their lead, indented, collapsible; "member of" hint; block-aware drag | PL-2b | 650 / 9 | SPA |
| **PL-3a** | SPA: setting 「分頁群組顯示 team」 (new 介面 subsection), team palette, `TeamGroupChip` | PL-2b | 520 / 11 | SPA |
| **PL-3b** | SPA: `TabBar` groups — chip, underline, contiguity, collapse, active never hidden, drop-outside snaps back | PL-3a, PL-2c | 700 / 5 | SPA |

Total ≈ 10 400 lines across 18 PRs.

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
| `GET /api/team/unattended` | 200 `UnattendedView` · 500 `storage_error` | PU-1c |
| `PUT /api/team/unattended` | 200 `UnattendedView` · 400 `bad_request` · 500 · 503 `not_ready` | PU-1c |
| `POST /api/team/approvals` with `kind:"adopt"` | 201 / 200 replay · 400 · 409 (adopt refusals) · 503 | PL-1c |
| `POST /api/team/release` | 200 `Member` · 400 `origin_unknown` · 409 `not_lead` / `not_your_member` / `relay_open` · 503 | PL-1d |
| `GET /api/team/roster` | 200 `Roster` · 500 · 503 | PL-1f |

**team.db columns added** (each through `ensureColumn`, `internal/module/team/migrate.go:29-47`, in a new `migrateAdopt(db)` run after `migrateUsage` at `internal/module/team/store.go:83-86`):

| Column | Declaration | PR |
|---|---|---|
| `approval_requests.close_reason` | `TEXT NOT NULL DEFAULT ''` | PL-1b |
| `team_members.origin` | `TEXT NOT NULL DEFAULT 'spawned'` | PL-1b |
| `team_members.ended_at` | `INTEGER NOT NULL DEFAULT 0` | PL-1b |

No new table. The unattended switch is **host config** (`host_config.db`, key `unattended`; PU-1a).

**Notices** (texts pinned in `internal/team/wire_adopt.go`; sent from the lead's inbox, PL-1d):

| Notice | Text |
|---|---|
| adopted (to the target) | `[pdx team] 你已成為 <lead address> 的 member（team <team id>）。自我接力已關閉，接力由 lead 安排；回報請送 <lead address>。` |
| released (to the member) | `[pdx team] <lead address> 已讓你離開 team <team id>：你現在是一般 session，自我接力依這台主機的設定。` |

---

# Phase PU-1 — the daemon decides (unattended spec D-U23-1…4, D-U23-6 list, D-U23-7)

**Facts this phase rests on** (read in the code, not inferred):
- **One approve path per kind, today inside `handleDecide`.** `internal/module/team/handler.go:339-462`: hook kinds branch out at `:375-378`; a lead approve builds the grant from the payload when the client sent none (`:383-405`) and runs `CloseLeadApproved` through `closeWith` (`:409-414`); a self_relay approve runs `CloseSelfRelayApproved` (`:415-419`); everything else `closeAs` (`:420-422`). Errors map to 409 `already_lead` / `member_cannot_lead` with the row left open (`:427-439`); a `memberCancelled` self relay answers 409 `member_relay_is_leads` after its commit (`:449-455`); one log line per decision (`:460`).
- **Every close that wins broadcasts once and wakes its pollers.** `closeWith` → `closeWithOp` → `announceClosed` = `broadcast("closed")` + `wake` + `afterClose` (`internal/module/team/module.go:387-410`); `broadcast` holds `eventMu` (`:414-423`). `afterClose` moves a self_relay row's op (`internal/module/team/relay_handler.go:374-401`).
- **Create broadcasts `opened` after the insert, under `createMu`.** Lead: `handler.go:173-246` (`createMu` at `:173-174`, insert `:227-243`, `broadcast("opened")` `:245`, 201 `:246`). Self relay: `relay_handler.go:261-350` (`createMu` `:261-262`, op + row `:311-347`, `broadcast("opened")` `:349`, 201 `:350`).
- **A self relay is refused before any row when the session is a member, the host switch is off, or the session paused** (`relay_handler.go:229-245`, `selfRelayState` `:62-89`). So D-U23-4 holds with no new check: unattended never sees those.
- **The audit is the row itself.** `approval_requests.decided_by_json` / `decided_at` (`internal/module/team/store.go:49-67`), written only by `closeRowIn` (`:259-298`); no code deletes a row (`grep "DELETE FROM" internal/module/team` finds none outside tests). The daemon log line per decision is the second record (spec §6.5).
- **The SPA is silent about a close it never saw open.** `approval-ws.ts:99-105`: a notification is raised only on `opened` (`notifyApprovalOpened`, `:101`), and a toast only when `applyClosed` answers `'elsewhere'`; `applyClosed` answers `'absent'` for a request it does not hold (`spa/src/stores/useApprovalStore.ts:127-142`).
- **The mod does not read the op state `begin` answers.** It takes `op.id`, `request_id`, `op.handoff_path` and `op.ref` (`cmd/pdx/plugin/purdex/hooks/register.js:409-451`) and then waits with `pdx relay wait`, whose answer `approved` starts the write turn (`:459-470`). An already-approved request therefore needs no mod change.
- **`pdx lead request` takes a closed row from create.** Its poll loop runs only `for ap.State == team.StateOpen` (`cmd/pdx/lead.go:201-226`), and the hook flag it raises after the 201 is lowered by its own `defer` (`:194-199`); `leadFinish` prints the grant (`:285-304`).
- **Host config today.** Keys are rows of `host_config(key, value, revision, updated_at)` with a CAS `Put` (`internal/module/hostconfig/store.go:52-63, 108-152`). The GET answers a fixed map of keys (`internal/module/hostconfig/handler.go:87-106`) and each PUT route is bound to one key (`internal/module/hostconfig/module.go:44-53`), so a key no route names cannot be written through `/api/hostconfig`. Services are published in `Init` (`internal/module/hostconfig/module.go:34-39`) and looked up by the team module (`internal/module/team/module.go:233-246`, `lookup` in `spawn_runner.go:51-58`).
- **Capabilities are a static list in core** (`internal/core/info_handler.go:41-49`), pinned in order by `TestHandleInfo_Capabilities` (`info_handler_test.go:372-379`).
- **Admin token only off `/api/peers/*`.** `TokenAuth` accepts the host token, or a ticket on a real WebSocket handshake only (`internal/middleware/middleware.go:71-96`); the outer handler routes only `/api/peers` and `/api/peers/` through `PeerAuth` (`cmd/pdx/http_chain.go:28-36`). The pattern of an admin-only test is `TestNewOuterHandler_HostConfigTeamPutIsAdminOnly` (`cmd/pdx/http_chain_test.go:754-784`).
- **No `pdx` command reaches an arbitrary route.** The dispatcher is a fixed list (`cmd/pdx/main.go:45-93`); `pdx nex` speaks only Nexen's grammar (`cmd/pdx/nex.go:40-100`).

## PR PU-1a — wire contract and the switch's storage

**Goal.** Fix the unattended contract once (decider, state, event, capability name) and give the switch a home in host config that no generic route can write (D-U23-1, D-U23-2, D-U23-7).

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
	UnattendedListMax    = 200                   // GET's approved list cap
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
	UnattendedState                  // flattened
	Approved []Approval `json:"approved"` // never null: decided by "unattended" since Since, newest first
	Swept    int        `json:"swept,omitempty"` // PUT only: open requests the switch-on approved
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
1. **A never-written key is off** (`UnattendedState{}`). A stored value that does not decode as the struct (unknown field, wrong type, `null`) is an **error**, never "off with no error" and never "on": the team module then treats the switch as off and logs (fail closed, PU-1b rule 2).
2. **`SetUnattended` is idempotent.** Same value → `changed=false`, nothing written, `Since`/`ChangedAt` untouched. Off→on sets `Since = ChangedAt = now`; on→off keeps `Since` (the list survives the switch-off, D-U23-6) and sets `ChangedAt = now`. `ChangedBy = &by` on every write.
3. **CAS with retry.** `Get` → compute → `store.Put(KeyUnattended, rev, …)`; a lost race (`ok=false`) re-reads and retries, at most 3 times, then an error.
4. **No generic route.** `KeyUnattended` is not in `handleGet`'s map (`internal/module/hostconfig/handler.go:89-96`), not in `readers` (`internal/module/hostconfig/read.go:100-107`) and has no `PUT /api/hostconfig/*` route. The only writer is the team module's route (PU-1c), which runs the D-U23-3 sweep with the write.

**Tests.**
- `TestWireUnattended_LiteralsArePinned`: the capability, event type, client kind, label and list cap.
- `TestWireUnattended_JSONShapes`: `UnattendedView` flattens the state; `approved:[]` is never `null`; `changed_by` absent when nil.
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

**Size.** ≈ 420 lines, 5 files. **Deploy.** None: nothing reads it yet (fold into the next bump).

**Risks.** None at runtime.

## PR PU-1b — the daemon approves: at create, at begin, at switch-on and at boot

**Goal.** D-U23-1 (approved at once, same CAS, same close event, same audit row), D-U23-3 (switch-on approves what is open; the route itself is PU-1c), D-U23-4 (nothing new for members, off and paused sessions), D-U23-6 first two bullets (no dialog, no notification), and the data source of the "while you were away" list.

**Files.**
- Create `internal/module/team/unattended.go` (`approve`, `autoApprove`, `unattendedOn`, `sweepUnattended`) and `unattended_test.go`.
- Create `internal/module/team/store_unattended.go` (`ListAutoApproved`) with its test in `unattended_test.go`.
- Modify `internal/module/team/handler.go`: decide's approve branch (`:379-422`) becomes a call to `approve`; create (`:236-246`).
- Modify `internal/module/team/relay_handler.go:348-350` (begin).
- Modify `internal/module/team/module.go`: the `unattended hostconfig.UnattendedStore` field, its `Init` lookup beside the prompts' (`:242-246`), and the boot sweep in `Start` after `resumeSpawns` (`:322-323`).
- Modify `internal/module/team/handler_test.go` (the fixture registers a fake `UnattendedStore` under `hostconfig.UnattendedKey`, beside `:244-246`).

**Interfaces.**
- `func (m *Module) approve(a team.Approval, c Close) (after team.Approval, won bool, out approveOutcome, err error)` — **the** approve path. By kind: `lead` → `CloseLeadApproved` with `c.Grant` (nil → the payload's grant, `leadGrantOf(a)`, factored out of `handler.go:383-404`); `self_relay` → `CloseSelfRelayApproved` (`out = memberCancelled` when its transaction cancelled instead); anything else → an error (`hook kinds decide through decideHook`). Every call goes through `closeWith`, so the winner broadcasts `closed` once.
- `handleDecide` keeps its request parsing, grant editing, error mapping and log line; only the store calls move into `approve`.
- `func (m *Module) unattendedOn() bool` — `m.unattended.Unattended()`; a read error is **false** plus one log line (fail closed).
- `func (m *Module) autoApprove(a team.Approval) (team.Approval, bool)` — `approve(a, Close{State: approved, DecidedAt: m.now(), DecidedBy: ptr(team.UnattendedClient())})`; true only for a won close whose row is now `approved`. Logs `[team] approval <id> approved by unattended (origin <ref>)<team note>`, or `… not auto-approved: <why>` on an error or a member cancel.
- `func (m *Module) sweepUnattended(why string) int` — **caller holds `createMu`**. `ListOpen()`, then `autoApprove` for every `AutoApprovable` row, oldest first; returns how many were approved; one summary log line.
- `func (s *Store) ListAutoApproved(since int64, limit int) ([]team.Approval, error)` —
  `WHERE state = 'approved' AND decided_at >= ? AND json_extract(decided_by_json, '$.kind') = 'unattended' ORDER BY decided_at DESC, id LIMIT ?`. `since = 0` answers `[]` without a query. Never nil.

**Behaviour rules.**
1. **At create (lead), under the `createMu` the handler already holds:** after the insert (`handler.go:227-243`), `if AutoApprovable(stored.Kind) && m.unattendedOn()` → `autoApprove(stored)`. Approved → **no `opened` broadcast**; the 201 body is the approved row (decided_by unattended, grant set). Not approved (error, refusal) → `broadcast("opened")` and 201 with the open row, exactly as today.
2. **At begin (self_relay), the same,** after the approval row's insert (`relay_handler.go:334-347`): approved → no `opened`; the 201 carries `RelayBeginResponse{Op: <the op re-read, now claimed>, RequestID}`; a re-read error falls back to the op as created. The mod's `pdx relay wait` then answers `approved` on its first poll.
3. **A replay of the same id is unchanged.** `getRow` answers the stored row (`handler.go:183-191`), approved or not; `RelayOpByRequest` the stored op (`relay_handler.go:192-218`).
4. **Members, off and paused sessions raise nothing** (D-U23-4): `begin`'s 409s (`relay_handler.go:235-245`) run before any row; `create`'s `already_lead` / `member_cannot_lead` (`handler.go:205-225`) too.
5. **Hook kinds are never approved by the daemon.** `AutoApprovable` is false for them; `sweepUnattended` skips them; `/api/ask/begin` (`ask_handler.go`) is not touched.
6. **Boot:** `Start` calls `sweepUnattended("boot")` under `createMu` when `unattendedOn()`, after `reconcileRelays` and `resumeSpawns`. It closes the crash window between a switch-on write and its sweep, and approves a request whose create-time approve failed for a transient reason.
7. **Lock order is today's:** `createMu` → store transaction → `eventMu` (broadcast). No store write under `eventMu`.
8. **A manual decide and the daemon race on the CAS.** Exactly one wins; the loser of a click gets today's 409 `already_decided` carrying the row with `decided_by.kind == "unattended"`.

**Tests** (fixture: `newFixture`, `handler_test.go:235-268`; its test subscriber collects every event):
- `TestCreate_UnattendedOnApprovesAtOnceWithNoClient`: 201, `state approved`, `decided_by {kind unattended, label 無人值守模式}`, no `addr`; `LiveTeamByLead` finds the team; the subscriber saw exactly one event, `closed`.
- `TestCreate_UnattendedOffOpensAsToday`: `opened` then nothing.
- `TestCreate_UnattendedReadErrorIsOff`: the fake store errors → the row stays open and `opened` is broadcast.
- `TestCreate_UnattendedReplayAnswersTheApprovedRow`: same id again → 200, the approved row, no second event.
- `TestCreate_UnattendedApproveRefusedStaysOpenWithOpened`: the origin becomes a member between the create checks and the approve (seam) → row open, `opened` broadcast, log names `member_cannot_lead`.
- `TestRelayBegin_UnattendedOnApprovesAndClaimsTheOp`: 201 op `claimed`; `GET /api/relay/wait/{id}` answers `approved` at once; one `closed`, no `opened`.
- `TestRelayBegin_UnattendedMemberPausedOrOffRaisesNothing`: table — member → 409 `member_relay_is_leads`, host switch off → 409 `self_relay_off`, paused → 409 `self_relay_paused`; no row, no event in each.
- `TestSweepUnattended_ApprovesOpenLeadAndSelfRelayLeavesHookKinds`: one open of each of the four kinds → lead and self_relay `approved` by unattended, `hook_ask` and `hook_permission` still `open`; returns 2.
- `TestSweepUnattended_OldestFirst`.
- `TestStart_UnattendedOnSweepsAtBoot`: an open lead row in team.db, the switch on → after `Start` it is approved; with the switch off it stays open.
- `TestDecide_RacesUnattendedOneWins`: `beforeTerminalClose`-style seam in `approve`; a click and `autoApprove` on one row → one `approved`, one `closed`, the other side 409 / not won.
- `TestDecide_ApproveStillWorksThroughApprove`: the existing decide tests stay green unchanged (the refactor gate).
- `TestListAutoApproved_SinceAndKindOnly`: rows approved by an app, by unattended before `since`, by unattended after `since`, and denied → only the third, newest first; `since = 0` → `[]`.

**Mutation gates.**
- Broadcast `opened` before the auto-approve → `…ApprovesAtOnceWithNoClient` red (two events).
- `AutoApprovable` includes `hook_ask` → `…LeavesHookKinds` red.
- A read error answers on → `…ReadErrorIsOff` red.
- Drop the `decided_at >= since` filter → `ListAutoApproved_SinceAndKindOnly` red.
- `autoApprove` writes its own `UPDATE` instead of `approve` → `…RacesUnattendedOneWins` red (two winners).

**Size.** ≈ 700 lines, 7 files.

**Deploy.** daemon, together with PU-1c (until PU-1c nothing can turn the switch on).

**Risks.**
- **A snapshot between the insert and the approve.** A WS client that subscribes in that window (`sendSnapshot` reads under `eventMu`, not `createMu`, `module.go:434-477`) sees the row open, then `closed`. It draws the dialog for a moment and, before PU-2b, toasts "已由 無人值守模式 核准". Accepted (milliseconds, one connecting client); PU-2b drops the toast.
- **`pdx lead request` still says** 「請在 Purdex 介面核准」 before an approval that lands at once (`lead.go:161`). Harmless; not changed.

## PR PU-1c — the switch route, the event, the capability, the safety layer

**Goal.** D-U23-1 (the route the App calls), D-U23-2 (admin only, no `pdx` command, skill forbids, audited and broadcast), D-U23-3 (switch-on sweeps), D-U23-5 (capability), D-U23-6 (the list's route; every window sees the state), D-U23-7 (persisted, not synced).

**Files.**
- Create `internal/module/team/unattended_handler.go` and `unattended_handler_test.go`.
- Modify `internal/module/team/module.go`: routes (`:275-298`), `Start`'s `OnSubscribe` (`:324`).
- Modify `internal/core/info_handler.go:45-49` and `info_handler_test.go:372-379`.
- Modify `cmd/pdx/http_chain_test.go` (a new admin-only test beside `:754-784`).
- Create `cmd/pdx/unattended_guard_test.go`.
- Modify `cmd/pdx/plugin/purdex/skills/pdx-team/SKILL.md` and `cmd/pdx/plugin/embed_test.go:43-77`.

**Interfaces.**
- `GET /api/team/unattended` → 200 `UnattendedView` (`approved = ListAutoApproved(state.Since, UnattendedListMax)`); a store error → 500 `storage_error`.
- `PUT /api/team/unattended`, body `UnattendedPutRequest` → 200 `UnattendedView` with `swept`. 400 `bad_request` when `on` is missing or not a boolean, when `client.kind` or `client.label` is blank, or when `client.kind` is not `"app"`. 503 `not_ready` while stopping. The daemon sets `client.addr = r.RemoteAddr`.
- Event `team.unattended`: `{op:"snapshot", state}` to each new subscriber (`OnSubscribe`), `{op:"changed", state}` after every write that changed something, under `eventMu` like `broadcast`.
- `capabilities` gains `"relay.unattended.v1"` (appended; the order is the contract).

**Behaviour rules.**
1. **One critical section.** Under `createMu`: stopping → 503; `SetUnattended(on, client, now)`; when it changed **to on**, `sweepUnattended("switch on")`. Then (still in order, outside the store write) the `changed` event and the 200. So a request is either created before the switch (swept here) or after it (approved at create).
2. **Audit.** One log line per change: `[team] unattended on|off by app "<label>" from <addr> (swept N)`. `changed_by` is stored with the state.
3. **Off changes nothing else.** Open requests stay open; the list stays until the next switch-on (D-U23-6 "Cleared when the switch is turned on again").
4. **Per host, persisted, not synced** (D-U23-7): host config of this daemon; Profile Sync never sees it (no SPA store writes it).
5. **The safety layer (D-U23-2):**
   - no `pdx` command names the route or the word; the dispatcher (`main.go:45-93`) is unchanged;
   - the mod never calls it (`hooks/*.js`);
   - the skill says, in "When to ask for lead mode" and "Self relay": **"Never turn on 無人值守模式 (unattended mode).** It is the user's switch in Purdex.app: there is no `pdx` command for it, and you must not call the daemon's route or edit host config to get around that.";
   - what remains unprotected is spec §6.5's: one host token is shared by the App and `pdx`, so a same-uid agent can still `curl` the route — the broadcast (every window's title bar turns on, PU-2) and the audit line are what catch it.

**Tests.**
- `TestUnattendedPut_OnSweepsOpenRequestsAndBroadcastsChanged`: an open lead and an open self_relay → `swept: 2`, both `closed` by unattended, then `team.unattended {op:changed, state.on:true}`.
- `TestUnattendedPut_OnTwiceKeepsSinceAndSweepsNothing`.
- `TestUnattendedPut_OffKeepsTheListAndOpenRequests`.
- `TestUnattendedGet_ListsAutoApprovalsSinceTheLastOn`: on → two auto-approvals → off → GET lists both → on again → GET lists none.
- `TestUnattendedPut_RequiresAnAppClient`: table — no `on`, `on:"yes"`, no client, `kind:"unattended"`, `kind:"terminal"` → 400; nothing stored, no event.
- `TestUnattendedPut_AuditLineAndChangedBy`: the stored `changed_by` has the label and `100.64.0.4:51234`.
- `TestUnattendedSnapshot_ToEveryNewSubscriber`.
- `TestUnattended_SurvivesARestart`: the real hostconfig module and two team modules over one data dir: on in the first, the second's GET says on with the same `since`.
- `TestHandleInfo_Capabilities` (updated): `[…, "conversations.scope.v1", "relay.unattended.v1"]`.
- `TestNewOuterHandler_UnattendedIsAdminOnly`: recording stubs mounted at `teammod.UnattendedRoute` (the path constant the module itself registers, exported for this test) behind `newOuterHandler`, the pattern of `http_chain_test.go:754-784`; `PUT` and `GET` with the peer host's inbound token, a wrong token and none → 401 and the stub never runs; the admin token → the stub runs.
- `TestPdx_NoCommandOrModCallTurnsUnattendedOn` (`cmd/pdx/unattended_guard_test.go`): every non-test `cmd/pdx/*.go` file and every `cmd/pdx/plugin/purdex/hooks/*.js` is read; none contains `/api/team/unattended`; the command list printed at `main.go:46` has no `unattended`.
- `TestSkill_SaysWhatSpec10Requires` (extended): the skill holds the line "Never turn on 無人值守模式" and the words "no pdx command for it" (the skill writes `pdx` in backticks; the test matches the text around it).

**Mutation gates.**
- Drop the sweep → `…OnSweepsOpenRequests…` red.
- Accept any client kind → `…RequiresAnAppClient` red.
- Move `UnattendedRoute` under `/api/peers/team/` → `…UnattendedIsAdminOnly` red (the peer token then meets `PeerAuth` and `HostRoutePolicy`'s 403, not 401).
- Add a `pdx unattended` dispatcher case → `…NoCommandOrModCall…` red.

**Size.** ≈ 650 lines, 9 files.

**Deploy.** daemon + setup (the skill text). Batch with PU-1b.

**Acceptance** (mlab, throwaway lead session, App connected): with the switch off, `pdx lead request` opens the dialog; PUT on (API, admin header file) → the dialog closes on every window (with no toast once PU-2b is in); a second `pdx lead request` from another throwaway session exits 0 at once and no dialog or notification appears; `GET /api/team/unattended` lists both; PUT off; restart the daemon; GET still lists them, `on:false`; no flag left in `hooklocks/`.

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
- Create `spa/src/lib/team/unattended-api.ts` and its test: `getUnattended`, `putUnattended`. They go through the approval API's transport and error mapping: `send` and `errorFromResponse` (`approval-api.ts:38-77`) become exported from there, unchanged in behaviour.
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
- `handleUnattendedEvent(hostId, value)` — validates `{op: 'snapshot' | 'changed', state}` whole (`isUnattendedState`: `on` a boolean, `since` and `changed_at` finite numbers, `changed_by` absent or a record); a bad frame is dropped with one `console.warn`; a good one → `applyState`. A good `team.unattended` frame also proves support: `setSupport(hostId, 'yes')`.

**Behaviour rules.**
1. **State only from the daemon.** The store never writes host config and never guesses.
2. **Every window follows:** each renderer has its own sockets and store; a `changed` event reaches all of them.
3. **Not tab-hosted.** Nothing mounts inside a tab; the store is module scope.

**Tests.**
- `unattended-api.test.ts`: GET and PUT bodies; the client descriptor is sent; a plain 404 → `'unsupported'`; a 400's detail surfaces.
- `useUnattendedStore.test.ts`: apply, replace, forget.
- `unattended-ws.test.ts`: snapshot and changed apply; a frame with `on: "true"`, a missing `since`, or `op: 'x'` is dropped whole; a good frame sets support `'yes'`.
- `unattended-support.test.ts`: connected → one `/api/info`; the capability present → `'yes'`, absent → `'no'`, `capabilities` not an array → `'no'`; a stale answer after a re-point is dropped; no second call without a new transition.
- `approval-api.test.ts`, unchanged, is the gate for the export.

**Mutation gates.**
- Accept a frame without validating `on` → the malformed-frame case red.
- Read a missing `capabilities` as `'yes'` → the not-an-array case red.

**Size.** ≈ 520 lines, 13 files. **Deploy.** SPA (nothing renders it yet).

**Risks.** #1866 PR2b also adds branches to `useMultiHostEventWs.ts` and the `host-events.ts` union (design `docs/specs/2026-10-08-worker-status-deltas-design.md:258`): textual conflicts only.

## PR PU-2b — the aggregate, the fan-out, the title-bar toggle

**Goal.** D-U23-5 (one button for every shown host: off / on / partial, the tooltip, the press semantics, a host that comes back is not changed) and D-U23-6 first two bullets (visibly on in every window with the label 無人值守中; no toast for what the daemon approved — Open question 1).

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
- `toggleUnattended(agg) → Promise<{ target: boolean; failed: Array<{ hostId; code }> }>`: `target = agg.mode !== 'on'` (off, partial → on; on → off); `putUnattended(hostId, target)` for every **reachable** host in parallel; unreachable and unsupported hosts are never written; the caller toasts `unattended.toast.failed` once, naming every failed host.
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

**Size.** ≈ 700 lines, 12 files. **Deploy.** SPA.

**Risks.**
- **U1 (interface unification) may restyle the title bar.** Agree the slot with `mlab/_3fj93m` before this PR starts.

## PR PU-2c — the "while you were away" panel

**Goal.** D-U23-6 third bullet: opening the button's menu lists auto-approvals since the switch last turned on (host, session, kind, time), read from each daemon's audit (PU-1c's GET).

**Files.**
- Create `spa/src/components/UnattendedPanel.tsx` and `UnattendedPanel.test.tsx`.
- Modify `spa/src/components/UnattendedButton.tsx` (the ▾ button; the panel; the open-once rule) and `UnattendedButton.test.tsx`.
- Modify `spa/src/locales/en.json`, `zh-TW.json`.

**Interfaces.**
- A second button `data-testid="unattended-list"` (Phosphor `CaretDown`) opens `UnattendedPanel` anchored on the pair (Open question 3).
- `UnattendedPanel` (`FloatingPanel`, `placement="below"`, width 360): on open, `getUnattended` for every reachable shown host; rows merged newest first: `<host>：<session> · <kind> · <time>` (`approvalSessionLabel`, `approvalKindLabel`, local `HH:mm`); empty → `unattended.panel.empty`; a host whose GET failed → one line naming it. It fetches again each time it opens.
- Locale keys: `unattended.list`, `panel.title`, `panel.empty`, `panel.since`, `panel.host_failed`.

**Behaviour rules.**
1. **Turning off with auto-approvals opens the panel once** (Open question 2): after a switch-off that answered 200 on at least one host whose `approved` is non-empty.
2. **Not tab-hosted:** the open / closed flag is the component's own state (closing it on an unmount is right); every row is fetched.

**Tests.**
- `UnattendedPanel.test.tsx`: `merges two hosts newest first`; `empty state`; `a host whose GET failed is named`; `kind labels for lead and self_relay` (PL-2a adds `adopt`).
- `UnattendedButton.test.tsx`: `▾ opens the panel and it fetches`; `switching off with auto-approvals opens the panel once`; `switching off with none does not`.

**Mutation gates.**
- Fetch once per mount instead of per open → the re-open case red (stale rows).
- Open the panel on every switch-off → `switching off with none does not` red.

**Size.** ≈ 400 lines, 6 files. **Deploy.** SPA.

**Acceptance** (mlab App with two windows, two throwaway sessions): press on → both windows show 無人值守中; a self relay at a test threshold (`PDX_RELAY_THRESHOLD`) relays with no dialog and no notification; a host whose daemon is down shows partial with its name in the tooltip; press off → the panel opens with the relay listed.

---

# Phase PL-1 — adopt, release, the roster (adopt spec D-U24-1…4; D-U24-5 data)

**Facts this phase rests on:**
- **The create route is one switch on `kind`.** `handler.go:119-128` accepts `lead`, refuses `self_relay` (it opens through `/api/relay/begin`) and anything else (`"kind must be lead"`). `CreateApprovalRequest` has `ID, Kind, OriginInbox, Reason, MaxMembers, Roots, WaitS` (`internal/team/wire.go:104-113`); reason is required for `lead` (`handler.go:133-137`).
- **`approval_requests` has no reason column for a close** (`internal/module/team/store.go:49-67`); `scanRow` reads `selectCols` (`:93-94, 106-141`).
- **`team_members`' primary key is `spawn_op`** (`internal/module/team/team_store.go:46-67`), with `team_members_one_active ON team_members (session_id) WHERE state = 'active'` (`:67`). `validMemberState` knows `active | killed | gone` (`:128-134`); `MemberState` likewise (`internal/team/wire_team.go:32-39`). A spawned member is stored by the runner (`internal/module/team/spawn_register.go:144-147`).
- **An ended team leaves its members `active`.** `EndTeam` touches `teams` only (`team_store.go:379-393`, D4), and the sweeper marks only members of live teams gone (`sweeper.go:212-237`, `team_store_members.go:66-70`). So a session that was a member of an ended team still has an `active` row, and a second `active` row for it violates `team_members_one_active`.
- **Roles are read live.** `relayRole` = lead of a live team, else an active member of a live team, else none (`relay_handler.go:44-56`); `selfRelayState` answers `off` for a member and the host switch then the pause for the others (`:62-89`).
- **The team limit counts team.db alone:** running spawn ops (this one excepted) + `active` member rows ≥ `max_members` (`internal/module/team/spawn_store.go:138-175`).
- **Kill ends the member's tmux session by its spawn tag.** `killMember` reads the session's identity and refuses when `id.Tag != mr.SpawnOp` (`team_handler.go:204-224`, the tag check at `:217`); `killAndMark` refuses a member mid-relay (`claimed|writing|written`, `:158-162`) and marks with `MarkMemberKilled`'s CAS (`team_store_members.go:104-111`). An adopted session's tmux session has no `@pdx_spawn_op` tag (the option is set only by the spawn's own tagged create, `internal/module/team/spawn_tmux.go:23`), so today's kill would refuse it with 409 `not_your_member` — and killing it would end a tmux session the user made.
- **Targets are parsed by `parseKillTarget`** (`_xxxxxx`, `xxxxxx`, `<host>/…` with this host's alias or id, `<name> [xxxxxx]`; `team_handler.go:253-281`), matched over the team's members by current ref, then lineage (`store.PreviousRefs()`), then live name (`:232-323`).
- **The origin resolver** offers `ResolveOrigin(inbox)`, `ResolveOriginBySession(sid)`, `LiveSession`, `LeadPresence` (`internal/module/team/module.go:31-43`; implementation `internal/module/peers/origin_resolver.go:29-67, 141-215`). Registry entries carry `Inbox` and `Tmux` (`internal/peers/registry.go:50-62`). There is one fake, `fakeOrigins` (`internal/module/team/handler_test.go:54-160`).
- **The daemon has no in-process send.** `handleSend` is admin-only by principal (`internal/module/peers/send.go:197-206`) and attributes the sender by `origin_inbox` (`:236-302`); `SendRequest{To, Text, Mode, OriginInbox}` (`internal/peers/wire.go:290-295`). Plan v3 P6-1 (`plan-v3.md:1188-1240`) designs `peers.Sender`, running `handleSend` in process with `middleware.WithPrincipal(ctx, Principal{Kind: PrincipalAdmin})` (`internal/middleware/peer_auth.go:44`).
- **The hook lock is for lead requests only:** `openLeadAndRemoveStaleFlag` and the prune guard look up `KindLead` (`internal/module/team/hooks.go:87-100, 161-170`); spec §6.6 names only two flag writers. An `adopt` request therefore locks nothing.
- **The CLI's team commands** share `teamSetup` / `teamReportErr` (code last on stderr; `cmd/pdx/team_cmd.go:128-168`) and `teamRefusalCodes` (`:72-78`); `pdx lead request`'s create-then-poll loop is `cmd/pdx/lead.go:132-228`.
- **The skill and its test:** `cmd/pdx/plugin/purdex/skills/pdx-team/SKILL.md:10-35`; `TestSkill_SaysWhatSpec10Requires` (`cmd/pdx/plugin/embed_test.go:43-77`).
- **The mod caches the role.** `hello` sets `s.role` (`register.js:175-189`); `maybeBegin` returns early for `s.role === 'member'` (`:380-381`); a 409 `member_relay_is_leads` sets it (`:422`). Hello is re-sent only at `session.start`, after a `/clear`, or after a failed hello (`:637, 662, 686, 720`).

## PR PL-1a — wire contract: adopt, release, origins, close reasons

**Goal.** Fix the U24 contract (D-U24-2, D-U24-3) before the store and routes build on it.

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
	ErrAdoptSelf          = "adopt_self"
	ErrAdoptTargetIsLead  = "adopt_target_is_lead"
	ErrAdoptAlreadyMember = "adopt_already_member"
	ErrAdoptTargetNotFound = "adopt_target_not_found"
	ErrRemoteUnsupported  = "remote_unsupported" // P4b-4 reuses it
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
	AdoptNoticeFmt   = "[pdx team] 你已成為 %s 的 member（team %s）。自我接力已關閉，接力由 lead 安排；回報請送 %s。"
	ReleaseNoticeFmt = "[pdx team] %s 已讓你離開 team %s：你現在是一般 session，自我接力依這台主機的設定。"
)
type ReleaseRequest = KillRequest // POST /api/team/release: {origin_inbox, target}
// Member: Origin string `json:"origin"` ("spawned" | "adopted"); EndedAt int64 `json:"ended_at,omitempty"`;
//         AdoptRequest string `json:"adopt_request,omitempty"` (adopted only; SpawnOp is "" then)
func adoptPayloadOf(a Approval) (AdoptPayload, error) // strict decode for the daemon
```

**Behaviour rules.**
1. JSON names are snake_case as listed; every new field on an existing type is `omitempty` except `Member.Origin` (always present: an older daemon's view lacks it, which a client reads as `spawned`).
2. `AutoApprovable(KindAdopt)` is true (U24: "U23 開著時自動通過").

**Tests.**
- `TestWireAdopt_LiteralsArePinned`: kind, five codes, `released`, both origins, both notice formats.
- `TestWireAdopt_JSONShapes`: `AdoptPayload` full and minimal; `Approval` with and without `close_reason`; `Member` adopted (`spawn_op:""`, `adopt_request`, `origin:"adopted"`) and spawned.
- `TestAutoApprovable_Adopt`.

**Mutation gates.**
- `close_reason` without `omitempty` → the minimal `Approval` encoding red.
- `AutoApprovable` without `adopt` → `TestAutoApprovable_Adopt` red.

**Size.** ≈ 420 lines, 5 files. **Deploy.** None.

## PR PL-1b — team.db: adopt and release in the store

**Goal.** D-U24-2 "On approve: in one transaction the daemon re-checks every refusal above … and inserts the `team_members` row (origin `adopted`, no spawn op …) … If a re-check fails, the request closes as `cancelled` with that code"; D-U24-3 `state=released`, `ended_at` set.

**Files.**
- Modify `internal/module/team/migrate.go` (`migrateAdopt`) and `migrate_test.go`.
- Modify `internal/module/team/store.go` (`selectCols`, `scanRow`, `closeRowIn` write `close_reason`; `Close.Reason`).
- Modify `internal/module/team/team_store.go` (`memberCols` and `dest` add `origin`, `ended_at`; `validMemberState`; `CloseAdoptApproved`) and `team_store_test.go`.
- Modify `internal/module/team/team_store_members.go` (`ReleaseMember`; `MarkMemberKilled` / `MarkMemberGone` set `ended_at`).
- Modify `internal/module/team/spawn_register.go:144-147` (`Origin: spawned`).
- Create `internal/module/team/adopt_store_test.go`.

**Interfaces.**
- `Close` gains `Reason string`, written to `close_reason` by `closeRowIn` (empty for every existing caller).
- `type adoptCheck struct{ TargetLive bool }` — what only the registry can say, read by the caller just before.
- `func (s *Store) CloseAdoptApproved(id string, c Close, p team.AdoptPayload, chk adoptCheck, m memberRow) (a team.Approval, won bool, refused string, err error)`. One write transaction:
  1. `UPDATE approval_requests SET id = id WHERE id = ?` (the write lock first, as `CloseSelfRelayApproved` does, `team_store.go:191`).
  2. Re-checks, in order, each answering a code: the team `p.TeamID` is live and led by `p.LeadSessionID` (else `not_lead`); `p.TargetSessionID != p.LeadSessionID` (else `adopt_self`); the target leads no live team (else `adopt_target_is_lead`); the target is no active member of a live team (else `adopt_already_member`); running spawns + active members < `max_members` (else `team_full`, the query of `spawn_store.go:156-159` without the excepted op); `chk.TargetLive` (else `adopt_target_not_found`).
  3. A refusal: `closeRowIn(cancelled, Reason=code)`; commit; `refused = code`.
  4. Otherwise: an `active` row of the target in an **ended** team becomes `released` with `ended_at` (Open question 10; it would violate `team_members_one_active`); `closeRowIn(c)`; insert `m` (`spawn_op` = the request id, `origin = adopted`, `state = active`); commit.
  5. A lost CAS (`n == 0`) writes nothing and answers `won=false` with the row as it is.
- `func (s *Store) ReleaseMember(rowKey, sessionID string, at int64) (released bool, err error)`:
  `UPDATE team_members SET state='released', ended_at=?, updated_at=? WHERE spawn_op=? AND session_id=? AND state='active' AND NOT EXISTS (SELECT 1 FROM relay_ops WHERE session_id=? AND state NOT IN ('done','failed','cancelled'))`.
- `MarkMemberKilled` and `MarkMemberGone` also set `ended_at = at` (same statements, `team_store_members.go:104-128`).
- Two reads PL-1c's create uses: `OpenAdoptForTarget(targetSessionID) (team.Approval, bool, error)` (`kind = 'adopt' AND state = 'open' AND json_extract(payload_json, '$.target_session_id') = ?`), and `SeatsUsed(teamID) (used, limit int, err error)` (the count of step 2, outside a transaction; the approve re-checks it inside).

**Behaviour rules.**
1. **Migration is additive.** Old rows read `close_reason = ''`, `origin = 'spawned'`, `ended_at = 0`; an older daemon ignores the columns (its `INSERT`s name their columns and every new column has a default).
2. **A `released` member is no member:** `ActiveMemberInLiveTeam` and every `state = 'active'` query already exclude it, so `relayRole` answers none, and the `cleared` member move (`relay_store_report.go:93-95`) does not move it.
3. **A released row may be followed by a new adoption** of the same session: a new row keyed by the new request id; the old row stays `released`.

**Tests.**
- `TestMigrateAdopt_AddsColumnsOnceKeepsData`.
- `TestCloseAdoptApproved_InsertsTheAdoptedMemberInOneTx`: row approved; member row with `origin adopted`, `spawn_op = id`, the target's pid / proc start / pane; `ActiveMemberInLiveTeam(target)` true.
- `TestCloseAdoptApproved_EveryRecheckCancelsWithItsCode`: table of the six codes, each set up after create; the row `cancelled` with `close_reason`; no member row.
- `TestCloseAdoptApproved_RetiresAStaleRowOfAnEndedTeam`: the target `active` in an ended team → that row `released`, the new row `active`.
- `TestCloseAdoptApproved_LostCASWritesNothing`.
- `TestCloseAdoptApproved_TwoLeadsOneTarget`: two rows for one target, both approved → the first inserts, the second cancels `adopt_already_member`.
- `TestReleaseMember_SetsReleasedAndEndedAt`.
- `TestReleaseMember_RefusedWhileARelayOpIsOpen`: ops in `awaiting_approval`, `requested`, `claimed`, `written` each block; `done` does not.
- `TestRelayRole_ReleasedMemberIsNone`.
- `TestMarkMemberKilledAndGone_SetEndedAt`.

**Mutation gates.**
- Insert the member after the commit → `…EveryRecheckCancelsWithItsCode` red (a member row for a cancelled request).
- Drop the ended-team retirement → `…RetiresAStaleRow…` red (unique index violation).
- Guard release on `claimed|writing|written` only → the `awaiting_approval` / `requested` cases red.

**Size.** ≈ 700 lines, 8 files. **Deploy.** daemon (columns only; nothing writes `adopt` yet).

**Risks.**
- **Rollback.** A daemon from before PL-1b reading a team.db with adopted rows: `pdx kill` of an adopted member answers 409 `not_your_member` (no spawn tag) — safe; `pdx team` shows the rows. No migration down is needed.

## PR PL-1c — adopt: the route, decide, unattended

**Goal.** D-U24-2 in full on the daemon: `adopt` requests created by a lead, refused before opening with the spec's codes, decided like `lead`, and covered by U23.

**Files.**
- Create `internal/module/team/adopt_handler.go` and `adopt_handler_test.go`.
- Modify `internal/module/team/handler.go:119-128` (the kind switch hands `adopt` to `handleCreateAdopt`).
- Modify `internal/module/team/unattended.go` (`approve` gains the `adopt` branch).
- Modify `internal/module/team/module.go:31-43` (`OriginResolver` gains two methods).
- Modify `internal/module/peers/origin_resolver.go` and `origin_resolver_session_test.go`.
- Modify `internal/module/team/handler_test.go` (`fakeOrigins` gains the two methods).

**Interfaces.**
- `OriginResolver` gains:
  - `ResolveOriginByRef(ref string) (team.Origin, bool, error)` — the live, non-proxy entry with `RefID(SessionID) == ref` (same contract as `ResolveOriginBySession`);
  - `InboxOf(sessionID string) (string, bool, error)` — that session's live entry's `Inbox` (used by PL-1d).
- `POST /api/team/approvals` with `kind: "adopt"`: body `{id, kind, origin_inbox, target, wait_s}` → **201** the row (open, or approved at once when unattended is on), 200 on an idempotent replay. Errors: 400 `bad_request` (id, `target` empty or not a ref form, negative `wait_s`); 400 `origin_unknown`; 409 `not_lead`, `remote_unsupported`, `adopt_target_not_found`, `adopt_self`, `adopt_target_is_lead`, `adopt_already_member`, `request_open` (carrying the open row), `team_full`; 503.
- `POST /api/team/approvals/{id}/decide` on an `adopt` row: approve → `approve()` → `CloseAdoptApproved`; a refusal answers **409 `<code>`** after its commit, with the closed row and the `closed` broadcast (as `member_relay_is_leads`, `handler.go:449-455`). Deny / timeout / cancel / abandon are today's closes.

**Behaviour rules.**
1. **Target forms** (Open question 12): `_xxxxxx`, `xxxxxx`, `<host>/_xxxxxx`, `<host>/<name> [xxxxxx]` — the ref decides; the name of the bracket form is display only. `<host>` must be this host's alias or id (`ipeers.HostMatches`), else **409 `remote_unsupported`** (D-U24-2 v1 same-host). A bare name is 400 (`pdx adopt` refuses it first with exit 2).
2. **Resolution:** `ResolveOriginByRef(ref)`; else the lineage tier — a live session whose `PreviousRefs()` holds `ref` (`relay_store_lineage.go`); else 409 `adopt_target_not_found`.
3. **Order of the create checks** (under `createMu`): origin resolves (400 / 503) → idempotent replay by id (`getRow`, as `handler.go:183-196`) → the caller leads a live team (`LiveTeamByLead`, else `not_lead`) → target (rules 1–2) → `adopt_self` → `adopt_target_is_lead` → `adopt_already_member` → an open `adopt` row for the same `target_session_id`, any lead (`OpenAdoptForTarget`) → `request_open` → `team_full` (`SeatsUsed`) → insert (`deadline = now + wait_s`, 540 default, cap 600; `lease = now + 30 s`) → `AutoApprovable && unattendedOn()` → `autoApprove` (PU-1b rule 1: no `opened` when approved) → 201.
4. **Payload** = `AdoptPayload` from the lead's team and the target's resolved `Origin`; `requestHash(kind, origin sid, wait_s, payload)` (`handler.go:92-97`), so a replay with another target is `id_conflict`.
5. **Approve** reads the target's liveness from the registry just before the transaction (`ResolveOriginBySession(p.TargetSessionID)`, a read error → 503 for decide, "not approved" for the daemon), builds the member row from that live origin (pid, proc start, cwd, title, tmux name and pane, ref) and calls `CloseAdoptApproved`.
6. **The lease, the deadline and the origin-gone abandonment** are today's sweeper rules (`sweeper.go:75-96`); the origin is the lead. A target that dies while the request is open is refused at approve (`adopt_target_not_found`).
7. **No hook lock** for `adopt`: the lead keeps working while it waits (spec §6.6 names only the lead request and the relay as flag writers).
8. **Unattended covers `adopt`:** at create (rule 3), at switch-on and at boot (`sweepUnattended` filters by `AutoApprovable`). A refusal at an unattended approve closes the row `cancelled` with its code, exactly as a click would.

**Tests.**
- `TestAdoptCreate_RefusalsBeforeOpening`: table — not a lead, another host (`remote_unsupported`), unknown ref, self, a lead target, a member target, a second open adopt of that target (`request_open` carrying it), a full team; no row and no event for any.
- `TestAdoptCreate_ByOldRefThroughTheLineage`.
- `TestAdoptCreate_OpensWithThePayloadAndBroadcastsOpened`.
- `TestAdoptCreate_ReplayIsIdempotentOtherTargetConflicts`.
- `TestAdoptDecide_ApproveInsertsTheMemberAndRoleIsMember`: then `POST /api/relay/hello` for the target answers `role:"member", self_relay:"off"` (spec PL-1 test) and its `begin` is 409 `member_relay_is_leads`.
- `TestAdoptDecide_RecheckFailureIs409WithTheCode`: the target became a lead meanwhile → 409 `adopt_target_is_lead`, row `cancelled` with `close_reason`, one `closed`.
- `TestAdoptDecide_TargetGoneIsAdoptTargetNotFound`.
- `TestAdoptDecide_DenyChangesNothingForTheTarget`.
- `TestAdopt_UnattendedOnApprovesAtCreate`: 201 approved, member row, one `closed`, no `opened`.
- `TestAdopt_SwitchOnSweepIncludesOpenAdopt`.
- `TestAdopt_NoHookLockWhileOpen`: `POST /api/hooks/decide` for the lead's session answers `{}`.
- `TestOriginResolver_ResolveOriginByRefAndInboxOf` (peers): live, dead, proxy, unknown.

**Mutation gates.**
- Skip the re-check of `adopt_target_is_lead` at approve → `…RecheckFailureIs409WithTheCode` red.
- Answer a remote host as `adopt_target_not_found` → the `remote_unsupported` row red.
- Drop the per-target `request_open` → its row red.
- Leave `adopt` out of `approve` → `…UnattendedOnApprovesAtCreate` red.

**Size.** ≈ 720 lines, 8 files.

**Deploy.** daemon — **after PL-2a is merged and the main checkout fast-forwarded**: an older SPA drops `adopt` rows (`approval-ws.ts:43,52`), so until then an `adopt` request can only be approved by unattended or time out.

**Risks.**
- **Virtual peer names (peer mailbox spec, `docs/specs/2026-10-08-peer-mailbox-integration-spec.md` §3).** A name changes meaning there; refs do not. Rule 1 lets the ref decide, so the bracket form keeps working.

## PR PL-1d — release, the notices, the kill of an adopted member

**Goal.** D-U24-2 "The adopted session is told", D-U24-3 (release, its notice, `relay_open`), and a `pdx kill` that ends an adopted member without touching the user's tmux session (spec §3 "Changing `pdx kill` semantics" is out of scope: closing still ends the session; Open question 9).

**Files.**
- Create `internal/module/peers/sender.go` and `sender_test.go` (P6-1's sender, pulled forward unchanged in contract).
- Modify `internal/module/peers/module.go:368` (register `SenderKey` beside `OriginResolverKey`).
- Create `internal/module/team/notify.go` and `notify_test.go`.
- Create `internal/module/team/release_handler.go` and `release_handler_test.go`.
- Modify `internal/module/team/team_handler.go` (`killMember` branches on the row's origin; `memberView` fills `Origin`, `EndedAt`, `AdoptRequest`).
- Modify `internal/module/team/module.go` (field `sender`, optional lookup in `Init`, route, `noticeWG` joined in `Stop`).
- Modify `internal/module/team/adopt_handler.go` (send the notice after a won approve) and `team_handler_test.go`.

**Interfaces.**
- Peers (P6-1's contract, `plan-v3.md:1198-1209`): `const SenderKey = "peers.sender"`; `type Sender interface{ Send(ctx context.Context, req ipeers.SendRequest) (ipeers.SendResponse, error) }`; `type SendError struct{ Status int; API ipeers.APIError }`. The implementation runs `m.handleSend` in process under `middleware.WithPrincipal(ctx, Principal{Kind: PrincipalAdmin})` into a buffered `ResponseWriter`. P6-1 then adds only the virtual peer, the auto-reply and the handover notice.
- Team: `func (m *Module) notifyFromLead(leadSessionID, to, text string)` — `InboxOf(leadSessionID)`, then `sender.Send({To: to, Text: text, OriginInbox: inbox})` on a goroutine tracked by `noticeWG` with a 5 s context; every failure (no sender, no inbox, a `SendError`) is one log line. Never under `createMu`.
- `POST /api/team/release`, body `team.ReleaseRequest` → 200 `team.Member` (state `released`, or the row as it is when it was not active); 400 `origin_unknown`; 409 `not_lead`, `not_your_member`, `relay_open` (with the op); 503.
- Kill of an adopted member: `m.killProcess` seam (default `syscall.Kill(pid, syscall.SIGTERM)`).

**Behaviour rules.**
1. **Adopt notice:** after a won approve (click or unattended), `notifyFromLead(p.LeadSessionID, "<self alias>/<target ref>", fmt.Sprintf(AdoptNoticeFmt, lead address, team id, lead address))`. The message arrives from the lead's address, so the target's replies reach the lead.
2. **Release:** `callerTeam` (`team_handler.go:46-67`) → `matchMember` (any state, `:232-248`) → not found → `not_your_member`. Not `active` → 200 with the row (idempotent; Open question 19). Active → `ReleaseMember` (PL-1b); refused by its relay guard → 409 `relay_open` with `OpenRelayOpBySession`'s op; released → `notifyFromLead` with `ReleaseNoticeFmt` → 200.
3. **After release** the session is `none` (`relayRole`), its hello answers the host switch and its pause (U13), and a new `pdx lead request` or adoption may follow.
4. **Kill of an adopted member** (`origin = adopted`): `killAndMark`'s relay guard first (unchanged); then `ResolveOriginBySession(mr.SessionID)`: live → SIGTERM that process's pid (a live registry entry has its inbox socket present, its pid alive and its start time equal to `procStart` at read, `internal/peers/registry.go:292-400`), then `MarkMemberKilled`; not live → `MarkMemberKilled` with no signal; a registry error → 503. **The tmux session is never killed** (it is the user's). Spawned members keep today's path (`killMember`, tag-checked).
5. `memberView` sets `SpawnOp = ""` and `AdoptRequest = row key` for an adopted row; `Origin` always.

**Tests.**
- `TestSender_RunsHandleSendAsAdmin`, `TestSender_RefusalIsASendError` (P6-1's names; P6-1 keeps them).
- `TestNotify_AdoptNoticeFromTheLeadsInbox`: a fake sender records `OriginInbox = lead inbox`, `To = <alias>/<target ref>`, the exact text.
- `TestNotify_FailureIsLoggedNotFatal`; `TestNotify_NeverUnderCreateMu` (a sender that blocks does not block a concurrent create).
- `TestRelease_ReleasesAndTellsTheMember`: row `released` + `ended_at`; hello of the session answers `role:"none"` and the host switch (spec PL-1 test); the notice.
- `TestRelease_RelayOpenBlocks` (spec PL-1 test).
- `TestRelease_NotYourMemberAndNotLead`.
- `TestRelease_AgainAnswersTheRowUnchanged`.
- `TestKill_AdoptedMemberEndsItsProcessNotTheTmuxSession`: `killProcess` called with the live pid; no tmux call; row `killed`.
- `TestKill_AdoptedMemberAlreadyGoneMarksWithoutSignal`.
- `TestKill_SpawnedMemberUnchanged` (today's kill tests stay green).
- `TestStop_WaitsForNotices`.

**Mutation gates.**
- Send the notice from the daemon's own address or without `OriginInbox` → `…FromTheLeadsInbox` red.
- Kill an adopted member's tmux session → `…NotTheTmuxSession` red.
- Release without the relay guard → `…RelayOpenBlocks` red.

**Size.** ≈ 760 lines, 11 files.

**Deploy.** daemon. **M30 must be measured before this PR starts** (Measurements).

**Risks.**
- **P6-1 shrinks.** Its sender and two tests land here; P6-1 keeps the virtual peer, the auto-reply and the handover notice. The plan v3 PR table entry for P6-1 needs a note when this merges.
- **The notice is attributed to the lead.** The audit row of `peers` records the lead as the sender of a message its agent did not type; the `[pdx team]` prefix says it is the system's.

## PR PL-1e — CLI: `pdx adopt`, `pdx release`; the skill

**Goal.** D-U24-1 (skill only), D-U24-2 / D-U24-3 (the commands), D-U24-4 (skill only), spec §14 codes.

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
   - **"When you yourself judge a member no longer needed, ask the user first with AskUserQuestion, naming the member, with exactly three options: 釋出 / 關閉 / 保留 — and act on the answer. When the user asked you directly to release or close a member, do it without asking."**
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
- `TestSkill_SaysWhatSpec10Requires` (extended): the skill holds "pdx adopt <ref>", "pdx release <ref>", "釋出 / 關閉 / 保留", "AskUserQuestion" and "asks this session to become a lead".

**Mutation gates.**
- Map `cancelled` + `close_reason` to 12 → `…CancelledWithCloseReasonExits13…` red.
- Drop the AskUserQuestion line → the skill test red.

**Size.** ≈ 700 lines, 7 files. **Deploy.** CLI + setup (skill).

**Acceptance** (mlab, two throwaway sessions A, B): A `pdx lead request` → approve; A `pdx adopt _<B ref>` → the `adopt` card (PL-2a) → approve → B receives the notice from A's address; `pdx team --json` shows B with `origin:"adopted"`, `state:"active"`; B's `pdx relay self status` says member; A `pdx release _<B ref>` → B's notice; B is `none`; A adopts B again with unattended on → exit 0 at once, no card; A `pdx kill _<B ref>` → B's Claude Code exits, its tmux session and shell stay; no flag left.

## PR PL-1f — the roster: `GET /api/team/roster`, `team.roster`

**Goal.** D-U24-5 "Team data comes from the team module … cached per host like other host data", for an App that has no session inbox: `GET /api/team` is the caller's own team only (`team_handler.go:23-39`, keyed by `origin_inbox`), so the App needs a host-wide view (deviation 8).

**Files.**
- Create `internal/team/wire_roster.go` and `wire_roster_test.go`.
- Create `internal/module/team/roster.go` and `roster_test.go`.
- Modify `internal/module/team/module.go` (route; `OnSubscribe`; `rosterMu`, `lastRosterHash`).
- Modify the call sites: `handler.go` / `unattended.go` (lead approve won), `adopt_handler.go` (adopt approve won), `spawn_register.go:160-163` (spawn done), `team_handler.go` (kill marked), `release_handler.go` (released), `sweeper.go` (team ended, member gone, the liveness tick), `relay_report.go:154-162` (`cleared` applied).

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
2. **Each session:** live registry origin when present (`ResolveOriginBySession`: address, title, name, tmux name before `:`); else the stored values (lead: the request row's origin, `team_store.go`/`store.go`; member: its row) with `live:false`.
3. **`rosterChanged()`** builds the roster, hashes its JSON, and broadcasts `changed` only when the hash differs from the last broadcast, under `rosterMu` (read + send, like `snapshotUnderLock`, `module.go:453-477`). Called after every write listed in Files and on the sweeper's liveness tick (every 10 s, `sweeper.go:15-18`), which also catches title and name changes.
4. **Snapshot** to every new subscriber through `OnSubscribe`; a send that fails closes the subscriber as `sendSnapshot` does (`module.go:434-445`).

**Tests.**
- `TestWireRoster_JSONShapes`: `teams:[]`, `members:[]` never null.
- `TestRoster_LiveTeamsActiveMembersWithTmuxNames`: a spawned member (`tm-…`), an adopted member (its user's tmux session name), a lead.
- `TestRoster_ReleasedKilledGoneAndEndedTeamsAreOut`.
- `TestRoster_SnapshotToEveryNewSubscriber`.
- `TestRoster_ChangedAfterAdoptReleaseKillSpawnAndTeamEnd`: one `changed` each.
- `TestRoster_TickBroadcastsOnlyWhenItChanged`: two ticks, no change → no event; a title change → one.
- `TestRoster_ClearedMovesTheLeadsSession`.

**Mutation gates.**
- Include `released` members → `…AreOut` red.
- Broadcast on every tick → `…OnlyWhenItChanged` red.

**Size.** ≈ 550 lines, 12 files. **Deploy.** daemon.

## PR PL-1g — the mod re-reads a cached `member` role at the threshold

**Goal.** Make D-U24-3 "可以自己接力" true in practice. The mod caches `s.role` from hello and from a 409 (`register.js:183, 422`) and skips every ask while it reads `member` (`:381`); hello is not re-sent until the next start or `/clear`. A released member (or the member of a team that ended, D4) would therefore never self-relay and would be auto-compacted instead.

**Files.** Modify `cmd/pdx/plugin/purdex/hooks/register.js` (`maybeBegin`, `:380-400`) and `relay.test.ts`.

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

**Size.** ≈ 160 lines, 2 files. **Deploy.** daemon (the embedded mod) + setup.

**Risks.** **U1-1b** edits `register.js` (session.start / turn.start / turn.complete / classic.SessionStart observer lines, `hooks/events.js`, `embed_test.go`). This PR changes only `maybeBegin`; rebase onto U1-1b if it merged first, and tell `mlab/_3fj93m` before merging (memory: "they notify before merging").

---

# Phase PL-2 — the adopt card, the roster store, the sidebar (adopt spec D-U24-2 card, D-U24-5)

**Facts this phase rests on:**
- **The dialog body switches on the kind inline.** `OpenApprovalDialog` (`spa/src/components/ApprovalDialogHost.tsx:64`) sets `isSelfRelay` (`:71`); the title (`:242`), the rows (`:262-282`) and the grant inputs / self-relay note (`:286-334`) branch on it; the overlay carries `data-kind` (`:229`). `grantOk` is always true for a self relay (`:180`).
- **Kinds the SPA knows:** `APPROVAL_KINDS` (`spa/src/lib/team/types.ts:152`); an unknown kind is skipped row by row (`:159-161`, `approval-ws.ts:43,52`). Labels: `approvalKindLabel` (`spa/src/lib/team/approval-format.ts:29-31`); notification text by kind (`spa/src/lib/team/approval-notify.ts:38-55`). Locale keys `approval.*` at `zh-TW.json` / `en.json` `:1979-2022`.
- **"Back to the requester"** (U22) is `gotoRequester` (`spa/src/lib/team/approval-goto.ts:38-56`): it maps `origin.tmux`'s session **name** to the host's session list (`useSessionStore.sessions[hostId]`) and activates or opens that tab. For an `adopt` row the requester is the lead.
- **The sidebar has no session list.** The wide activity bar shows workspaces (`spa/src/features/workspace/components/ActivityBarWide.tsx:298-388`); each workspace lists its **tabs** (`InlineTabList` → `InlineTab`) only when `tabPosition !== 'top'` (`WorkspaceRow.tsx:47-48, 148-150`); the other list is Workers (executions, `components/executions/WorkerList.tsx`), whose rows are Nexen executions, not tmux sessions. The session lists (`components/SessionSection.tsx:78-215`, Hosts › Sessions) are on the New Tab page and the Hosts page, not in the sidebar. `TabPosition = 'top' | 'left' | 'both'` (`spa/src/stores/useLayoutStore.ts:14`).
- **`InlineTabList`** renders `validIds` in `workspace.tabs` order in a vertical `SortableContext` (`spa/src/features/workspace/components/InlineTabList.tsx:30-58`); rows are indented `pl-[18px]` (`InlineTab.tsx:117`). Drags end in `computeDragEndAction` (`spa/src/features/workspace/lib/computeDragEndAction.ts:37-110`): same-workspace `arrayMove` over `ws.tabs` by index (`:67-80`), cross-workspace move (`:83-93`), header drop (`:99-106`).
- **A tab binds a tmux session by code**, not name (`spa/src/types/tab.ts:123`: `kind 'tmux-session'`, `hostId`, `sessionCode`, `cachedName`, `tmuxInstance`); the code ↔ name map is `useSessionStore.sessions[hostId]` (`spa/src/stores/useSessionStore.ts:8`, rows `lib/host-api.ts:7-20`). No SPA store maps a session to a CC session id or ref for all sessions (`stores/usePeerStore.ts:65-117` is fetched for the active pane only).

## PR PL-2a — the `adopt` card

**Goal.** D-U24-2 "decided like `lead` (one click on any App)" — a card in the existing dialog host, its notification and kind label; U22's switch to the requester applies (the requester is the lead).

**Files.**
- Modify `spa/src/lib/team/types.ts` (`ApprovalKind` and `APPROVAL_KINDS` add `'adopt'`; `AdoptPayload`, `adoptPayloadOf`; `Approval.close_reason?`).
- Modify `spa/src/components/ApprovalDialogHost.tsx` (the `adopt` body) and create `ApprovalDialogHost.adopt.test.tsx`.
- Modify `spa/src/lib/team/approval-format.ts:29-31` and `approval-notify.ts:38-55` with their tests.
- Modify `spa/src/locales/en.json`, `zh-TW.json`.

**Interfaces.**
- `adoptPayloadOf(a: Approval): AdoptPayload` — defensive, strings default `''` (as `selfRelayPayloadOf`, `types.ts:204-215`).
- The `adopt` body: title `approval.dialog.title_adopt` (「{{host}}：{{lead}} 想把 {{target}} 納入 team」); rows host, lead session (origin label + address), **target** (title else name else ref, address, cwd, tmux), team id, countdown; note `approval.dialog.adopt_note` 「核准後這個 session 會成為 member：自我接力關閉，接力由 lead 安排；它的模型不變。」; buttons 核准 / 拒絕; no grant inputs (`grantOk` true).
- Notification title `approval.notify.title_adopt` (「{{host}}：{{lead}} 想納入 {{target}}」); body the target's cwd.
- Kind label `approval.kind.adopt` (「納入申請」 / `adoption request`).

**Behaviour rules.**
1. The `adopt` card is the `lead` card's layout with the target block in place of the grant; minimize, the pill, the focus guard and the queue behave as for every kind (P9b-2).
2. A `409` with a U24 code (`adopt_target_is_lead`, …) carries the closed row → today's "handled" toast path (`approval-decide.ts:111-115`) names the state 「已取消」.

**Tests.**
- `ApprovalDialogHost.adopt.test.tsx`: `renders lead and target, no grant inputs`; `approve and deny are one click`; `minimize → pill counts an adopt`; `a decision here switches to the lead's tab` (`gotoRequester` with the lead's origin).
- `approval-notify.test.ts`: `adopt title names the lead and the target`.
- `approval-format.test.ts`: `kind label adopt`; `a cancelled adopt toasts its state`.
- `approval-ws.test.ts`: `an adopt row in a snapshot is kept, not skipped as unknown`.
- `locale-completeness.test.ts`.

**Mutation gates.**
- Leave `'adopt'` out of `APPROVAL_KINDS` → the snapshot test red.
- Render the grant inputs for `adopt` → the no-inputs assertion red.

**Size.** ≈ 480 lines, 10 files. **Deploy.** SPA (before PL-1c's daemon deploy).

## PR PL-2b — the roster store and the team-tab layout

**Goal.** Cache each host's roster like other host data (D-U24-5 last sentence) and derive, once, how tabs group by team, for both the sidebar (PL-2c) and the tab bar (PL-3b).

**Files.**
- Create `spa/src/lib/team/roster.ts` (types, `isRoster`, `parseRosterEvent`) and its test.
- Create `spa/src/stores/useTeamRosterStore.ts` and its test.
- Create `spa/src/lib/team/team-tab-layout.ts` and `team-tab-layout.test.ts`.
- Modify `spa/src/lib/host-events.ts:4-16` and `spa/src/hooks/useMultiHostEventWs.ts` (branch `team.roster`).

**Interfaces.**
- `useTeamRosterStore`: `byHost: Record<hostId, TeamRoster[]>`; `apply(hostId, teams)` (snapshot and changed both replace the host's set); `forgetHost`. Not persisted, per renderer.
- `teamRoleOfTab(tab, rosterByHost, sessionsByHost) → { hostId; teamId; role: 'lead' | 'member'; joinedAt; leadLabel } | null` — the tab's **primary** pane if it is `tmux-session`; its name = the host's session list row with that code, else `cachedName`; matched against the roster's `tmux_session` names (lead first). Any other pane kind → null.
- `layoutTeamTabs(tabIds: string[], tabsById, rosterByHost, sessionsByHost) → { order: string[]; blocks: TeamBlock[]; orphanHint: Record<tabId, string> }`:
  - a **block** = one team's tabs **within this list**: the lead's tab(s) first, then members' tabs in their stored relative order (Open question 13);
  - the block sits where its first tab (in stored order) sits; non-team tabs keep their positions;
  - a member tab whose lead has no tab in this list is not blocked: it keeps its position and gets `orphanHint[tabId] = leadLabel` (D-U24-5 "member of <lead name>");
  - `TeamBlock = { key: '<hostId>\0<teamId>'; hostId; teamId; leadLabel; leadTabIds; memberTabIds }`.
- `reorderWithBlocks(stored: string[], layout, activeId, overId) → string[] | null` — a drag in display space mapped back to stored order: within one block (a member over a member) → the stored order with the member moved; a lead dragged within its block, a block tab dropped outside its block, or a non-team tab dropped inside a block → `null` (snap back, nothing written).

**Behaviour rules.**
1. **Pure and derived** (D-U24-6 "Membership is derived, not manual"): nothing here writes a store but the roster store.
2. Pinned tabs are passed in by the caller only from the normal zone (Open question 14).

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
  - `reorderWithBlocks: member within block moves; lead within block, block tab outside, outside tab inside → null`.

**Mutation gates.**
- Order members by `joinedAt` instead of stored order → the in-block drag test red (the move does not show).
- Block an orphan member → the hint test red.
- Let a block tab leave its block → the `null` cases red.

**Size.** ≈ 650 lines, 8 files. **Deploy.** SPA.

## PR PL-2c — the sidebar's tab list: members under their lead

**Goal.** D-U24-5 where the sidebar lists sessions: its per-workspace tab list (Open question 7).

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

**Size.** ≈ 650 lines, 9 files. **Deploy.** SPA.

**Risks.** #1866 changes the Workers list (`components/executions/*`); this PR does not touch it.

---

# Phase PL-3 — tab groups (adopt spec D-U24-6)

**Facts this phase rests on:**
- **The tab bar** is `spa/src/components/TabBar.tsx`, mounted when `tabPosition !== 'left'` (`spa/src/App.tsx:216-228`) with `displayTabs` = the active workspace's `ws.tabs` (`App.tsx:97-104`, `features/workspace/lib/getVisibleTabIds.ts:27-36`). It splits a pinned zone and a normal zone (`TabBar.tsx:30-33, 93-152`), each a horizontal `SortableContext`; `handleDragEnd` allows same-zone moves and calls `onReorderTabs(newOrder)` (`:58-79`) → `reorderWorkspaceTabs` (`features/workspace/hooks.ts:147-151`, `features/workspace/store.ts:136-156`). The drag clamp measures `normalTabsRef` (`TabBar.tsx:41-56`). A chip before a group's first tab goes inside the `normalTabs.map` fragment before `<SortableTab>` (`:136-149`), outside `items`.
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

**Size.** ≈ 520 lines, 11 files. **Deploy.** SPA.

## PR PL-3b — groups in the tab bar

**Goal.** D-U24-6 in the tab bar: a group per team with an open tab, chip before its first tab, coloured underline, contiguity, drag within a group, snap back outside it, collapse with the active tab never hidden, indicators unchanged, the setting.

**Files.**
- Modify `spa/src/components/TabBar.tsx` (layout of the normal zone; chips; hidden collapsed tabs; `handleDragEnd` through `reorderWithBlocks`; the active-tab effect).
- Modify `spa/src/components/SortableTab.tsx` (`groupColor?: string` → a 2 px underline; nothing else changes).
- Create `spa/src/components/TabBar.team-groups.test.tsx`.
- Modify `spa/src/components/TabBar.test.tsx` and `SortableTab.test.tsx` (the setting off ⇒ today's bar, unchanged).

**Behaviour rules.**
1. **Setting off ⇒ exactly today's bar** (no layout call, no chip, no underline).
2. **Normal zone only.** Pinned tabs are never grouped (Open question 14).
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

**Size.** ≈ 700 lines, 5 files. **Deploy.** SPA.

**Acceptance** (mlab App): a lead with one spawned and one adopted member, three tabs: one group, chip = the lead's title, members after the lead; drag a member within → order kept; drag it out → snaps back; collapse, then activate a member from the sidebar → expands; release the adopted member → its tab leaves the group; Settings › 介面 › 分頁 off → plain bar.

**Risks.** **U1's lights v2** changes how the status dot is aggregated per tab (interface-language page, "聚合：逐 pane 計算狀態，tab 和 session 取最高優先序"); it may edit `SortableTab.tsx` / `TabStatusIndicator.tsx`. This PR only adds an underline prop to `SortableTab`; rebase onto whichever lands first.

---

## Measurements

- **M30 — does Claude Code end cleanly on SIGTERM?** (PL-1d rule 4; Open question 9.) Before PL-1d starts, by the coordinator, never on a real or peer session:
  1. a throwaway `tmux new -d -s m30 'claude --dangerously-skip-permissions --model haiku'` in a fresh `/private/tmp` directory; wait for `~/.claude/sessions/<pid>.json`;
  2. `kill -TERM <pid>`;
  3. within 5 s check: the process is gone (`kill -0`), the registry file is removed, the pane shows the shell (`tmux capture-pane -p -t m30`), and the daemon log has the SessionEnd hook event for that session id;
  4. `tmux kill-session -t m30`; no flag left in `~/.config/pdx/hooklocks/`.

  If Claude Code ignores SIGTERM or leaves the registry file, PL-1d falls back to `kill-pane` of the member's recorded pane under the same tmux generation (Open question 9's alternative), and this plan is amended before PL-1d.

No other new behaviour of Claude Code is relied on: an approved request needs no mod change (PU-1 facts), and PL-1g only re-sends the existing hello.

## Intersections with other lines

| Line | What touches what | Handling |
|---|---|---|
| **U1 interface unification** (`mlab/_3fj93m`) | U1-1b adds `hooks/events.js`, observer lines in `register.js`'s four hooks and an `embed_test.go` guard; lights v2 changes tab status aggregation. | **PL-1g** is the only mod change here and edits `maybeBegin` alone (no hook registered, so M-U1-3 is not triggered); merge after U1-1b or tell them first. **PU-1c / PL-1e** edit `SKILL.md` and `embed_test.go`'s skill test (textual). **PU-2b** puts a button in the title bar and **PL-3b** an underline in `SortableTab`: confirm with U1 before each starts. |
| **#1866 worker status deltas** (purdex-6d) | PR2b routes `nex.executions.hello` / `nex.execution` in `useMultiHostEventWs.ts` and the `host-events.ts` union; touches the Workers list. | **PU-2a** and **PL-2b** add one branch each to the same two files (textual). D-U24-5 deliberately does not touch the Workers list (Open question 7). Daemon side: both use `OnSubscribe`, no shared code. |
| **P9a-3 Hosts › 接力 editors** (not merged on `e47c1f35`) | `RelaySection.tsx`, `host-config-api.ts`, the `relay` row's queue. | **None.** The unattended switch is deliberately not a field of the `relay` row and not in `GET /api/hostconfig` (PU-1a rule 4), so P9a-3's queue, revision and editors are untouched. |
| **P6-1** (plan v3) | Its `peers.Sender` and two tests move into **PL-1d**. | P6-1 keeps the virtual peer, auto-reply and handover; update the plan v3 table when PL-1d merges. |
| **P4b-4** (plan v3) | Defines `remote_unsupported`. | **PL-1a** defines it first; P4b-4 reuses it. |
| **Peer mailbox** (`2026-10-08-peer-mailbox-integration-plan.md`) | Virtual names in addresses. | Adopt targets resolve by ref (PL-1c rule 1); the roster's `address` is display only. |
| **Mobile API P3** | Adds names to `capabilities`. | Textual conflict with PU-1c; both append. |

## Open questions

Each has a recommended default; the sections above are written to it. None reopens a user decision.

1. **A close decided by unattended: toast it?** (PU-2b.) **Default: no toast.** The dialog still closes. The panel (PU-2c) is the review surface (D-U23-6), and a switch-on that sweeps several requests would otherwise toast once per request on every window.
2. **Turning the switch off when auto-approvals exist: open the list?** (PU-2c rule 1.) **Default: yes, once.** Turning it off is the moment the person is back (U23 「回來時看得到」).
3. **The button's shape.** **Default: a split pair — the toggle, then ▾ for the list.** D-U23-5 gives the press a meaning (toggle) and D-U23-6 gives the list a menu, so one click cannot do both.
4. **Approve at boot while on.** (PU-1b rule 6.) **Default: yes.** It closes the crash window between a switch-on write and its sweep, and is what "approved while I am away" means after a restart.
5. **A request whose daemon approve fails a rule** (the lead's origin became a member; an adopt re-check fails). **Default: a lead row stays open and shows its dialog; an adopt row closes `cancelled` with its code.** Both are what a click does (`handler.go:427-439`; D-U24-2).
6. **The PUT requires an app client descriptor and stores `changed_by`.** **Default: yes.** It costs nothing and gives the U5b audit layer a "who turned it on" (D-U23-2).
7. **Where D-U24-5's indentation goes.** The sidebar has no session list (PL-2 facts). **Default: the sidebar's per-workspace tab list (tab position 左 / 兩者), as PL-2c does; the Workers list (executions) and the New Tab page's session list are not changed.** Alternative: also indent the New Tab session list (`SessionSection.tsx`), ≈ 250 more lines.
8. **Who sends the adopt / release notices.** **Default: the daemon, in process, from the lead's inbox** (PL-1d), so the message reads as from the lead and replies reach it. Alternatives: the CLI after the approval (as spawn's brief, but lost if the CLI dies — and an unattended approve has no CLI turn of its own), or the daemon's virtual peer (P6-1, not built yet).
9. **How `pdx kill` ends an adopted member.** **Default: SIGTERM its Claude Code process (M30); the user's tmux session and shell stay.** Alternative: `kill-pane` of its recorded pane (ends the shell, may close the user's window).
10. **A target that is still `active` in an ended team.** **Default: that stale row becomes `released` inside the approve transaction** (D4 made it an ordinary session; the unique index would otherwise refuse the new row).
11. **One open `adopt` per target.** **Default: yes, `request_open` carrying the open row**, so two leads (or two retries with new ids) never raise two cards for one session.
12. **Accepted target forms.** **Default: refs only** (`_xxxxxx`, `xxxxxx`, `<host>/_xxxxxx`, `<host>/<name> [xxxxxx]`), plus the lineage tier for an old ref; a bare name is exit 2. Spec D-U24-2 says `pdx adopt <ref>`; names change, refs are redirected.
13. **Order inside a group.** D-U24-6 says "the lead's tab first, then members in join order" and also "dragging a grouped tab reorders it within the group". **Default: the lead first, then members in their stored relative order** (which is join order until someone drags), so a drag inside the group persists.
14. **Pinned tabs.** **Default: never grouped.** The pinned zone has its own drag rules (`TabBar.tsx:65-69`).
15. **Collapse state and the active tab.** **Default: per window, in memory (a reload expands); collapsing the active tab's group first activates the nearest tab outside it, and does nothing when there is none** (Chrome's behaviour; D-U24-6 "the active tab is never hidden").
16. **Which tab belongs to a team.** **Default: a tab whose primary pane shows the lead's or a member's tmux session (by name, through the host's session list, `cachedName` as fallback); a tmux session holding both a lead and a member (an adopted pane beside its lead) is the lead's.**
17. **The setting's home and scope.** **Default: a new 介面 subsection 「分頁」 holding 「分頁群組顯示 team」; device-local, not Profile Sync** (the spec's purpose is to compare with it off on one screen).
18. **The mod's role re-read (PL-1g).** **Default: ship it after U1-1b** (it is the only mod change; without it a released member does not self-relay until its next start or `/clear`).
19. **Release of a member that is not active** (killed, gone, already released). **Default: 200 with the row unchanged**, as `pdx kill` of a killed member answers 200 (`team_handler.go:129-134`).
20. **Team palette values.** **Default: the Tailwind 300 tier of eight hues** (PL-3a), checked to differ from every host preset. The spec allows a later switch to host colours.

## Deviations from spec

1. **The switch's route is `GET`/`PUT /api/team/unattended`** (team module), and its storage is the host-config key `unattended` (PU-1a), not a field of the `relay` row or a `PUT /api/hostconfig/*` route. D-U23-1 asks for host config "next to the relay switches" and PU-1 for a host-config route. The team module must run the D-U23-3 sweep in the same critical section as the write, and only the daemon may set `since`; a generic CAS PUT can do neither, and the `relay` row is shared with P9a-3's editors.
2. **An approval made at create is never broadcast as `opened`**; its only event is `closed` (PU-1b). D-U23-2 says "broadcast … exactly like a manual approval" — the close event is the same; the open event is left out so that no client, old builds included, draws a dialog or a notification (D-U23-6).
3. **`PUT` requires an app `client` descriptor**, stored as `changed_by` (Open question 6). The spec does not mention it.
4. **`AdoptPayload` carries more than the spec lists**: `team_id` and the target's `name`, `address`, `cwd`, `tmux`, for the card. Additive.
5. **An adopted member's row key is the adopt request's id, stored in `team_members.spawn_op`** (the table's primary key); the wire shows `spawn_op: ""` and `adopt_request`. D-U24-2 says "no spawn op".
6. **`pdx kill` of an adopted member ends its process, not a tmux session** (PL-1d, M30, Open question 9). The spec keeps `pdx kill` "unchanged"; its meaning (the session ends) is kept, its mechanism cannot be (the tmux session is the user's and carries no spawn tag).
7. **The notices are attributed to the lead** (sent from its inbox, Open question 8). The spec says "the daemon sends".
8. **The App reads a new `GET /api/team/roster` and `team.roster` events** (PL-1f). D-U24-5 names `GET /api/team`, which answers the calling session's own team only.
9. **D-U24-5 applies to the sidebar's tab list** (Open question 7): the sidebar has no session list to indent.
10. **Release is blocked by any relay op in flight on the session**, not only a "member relay op": member relay ops arrive with P6, and a self op left over from before an adoption must block too.
11. **`team_members.ended_at` is also set by kill and gone**, not only by release. Additive.
12. **`Approval.close_reason`** (new, `omitempty`) carries the code of an adopt cancelled at approve; the spec says the request "closes as `cancelled` with that code" without naming a field.
13. **A mod change in PL-1** (PL-1g). The spec's PL-1 lists daemon, CLI and skill only.
14. **Spec §14's exit-13 row does not list the U24 codes** (`adopt_self`, `adopt_target_is_lead`, `adopt_already_member`, `adopt_target_not_found`) **or a `cancelled` adopt with a `close_reason`.** D-U24-2 says "409, exit 13", so the plan follows it; §14 needs the row when the relay spec's U-table is next edited (U23 / U24 fold-in).
15. **A stale `active` row of an ended team is marked `released`** when its session is adopted again (Open question 10).
