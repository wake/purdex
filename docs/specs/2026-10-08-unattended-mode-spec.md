# Unattended mode (spec)

Date: 2026-10-08. Coordinator: purdex-d3. Status: user decisions final (§1); plan and implementation pending.
Extends: `docs/specs/2026-10-06-lead-team-relay-spec.md` (U5b, U6, U9, U13, U13a). When that spec's U-table is next edited, fold §1 in as **U23** and link here.

## 1. User decisions (2026-10-08, do not reopen)

| # | Decision |
|---|---|
| U23 | 標題列放一顆「無人值守模式」按鈕。開著時，**自我接力申請**與**成為 lead 的申請**自動通過，不等任何人按。用途：睡覺、臨時不在、不能用手機。<br>- 一顆按鈕套用到工作台上顯示的**所有主機**。<br>- **手動關才關**，不自動到期；開著時標題列持續顯示，回來時看得到期間自動通過了哪些申請。<br>- 不在範圍內、照常等人：worker 的工具權限核准（permission channel）、agent 的 AskUserQuestion／權限詢問分流（U19 的 `hook_ask`／`hook_permission`）。 |

## 2. Derived rules (from U23 and existing decisions)

**D-U23-1 · The daemon decides, not the App.** The switch is stored in each host's daemon (host config, next to the relay switches in `internal/module/hostconfig/relay.go`). When a `lead` or `self_relay` approval request is created while the switch is on, the daemon approves it immediately in the same code path a client's one-click approve uses (same CAS, same `approval.request` close event, same audit row). So it works with every App closed, the laptop asleep, or no client connected. *Why:* the whole point is the user is not there.

**D-U23-2 · Same safety layer as U5b.** U5b already made lead approval "inform and consent", not a security boundary. U23 keeps U5a's remaining layer:
- `pdx` gets **no** command that turns the switch on; the skill forbids an agent from turning it on;
- the switch is set only through the Purdex App (the daemon route the App calls);
- every auto-approval is broadcast and written to the audit log exactly like a manual approval, with the decider recorded as `unattended` (not a client id).

**D-U23-3 · Turning on with requests already open.** When the switch turns on, every `lead` / `self_relay` request already awaiting approval on that host is approved at once (the user just said "approve these while I'm away"). Requests of other kinds stay open.

**D-U23-4 · Member, lead and solo rules are unchanged.** A member still has no self relay (U13; the request is refused before approval, so unattended never sees it). A lead's `pdx relay <ref>` for a member still needs no approval (U13). The host's self-relay switches (`self_solo`, `self_lead`) and a session's own pause (`/relay off`) still apply first: an "off" session raises no request, so nothing is auto-approved for it.

**D-U23-5 · All shown hosts.** The title-bar button reads and writes every host shown on the current workbench (Profile Sync's shown hosts).
- Button states: **off** (all off), **on** (all on), **partial** (mixed, or a host could not be reached / is too old to support it). Partial is shown distinctly, with a tooltip listing which hosts are off or unreachable.
- Pressing it from off or partial turns all reachable hosts on; from on, turns all reachable hosts off.
- A host that comes back online later is **not** changed automatically; the button shows partial until the user presses it again.
- A daemon without the capability (`relay.unattended.v1` in `/api/info` capabilities) counts as unsupported: excluded from the write, shown in the tooltip.

**D-U23-6 · What the user sees while it is on and after.**
- The title-bar button is visibly "on" (accent colour + label 無人值守中) on every window.
- The approval dialog host shows no dialog and raises no desktop notification for an auto-approved request (no one is there to act on it; the list below is how the user reviews them).
- A small "while you were away" list: opening the button's menu lists auto-approvals since the switch last turned on (host, session, kind, time), read from the daemon's audit log. Cleared when the switch is turned on again.

**D-U23-7 · Persistence.** The switch survives daemon restart (host config on disk). It is not part of Profile Sync (each daemon owns its own state), and not device-local.

## 3. Not in scope
- Auto-answering worker tool permissions or AskUserQuestion.
- Timed expiry (U23: manual off only).
- A per-host switch UI (U23: one button for all shown hosts). The daemon route is per host; the App fans out.

## 4. Phases (each ≤ 800 lines / ≤ 20 files)
- **PU-1 daemon:** host-config field + route (GET/PUT, App-only, no `pdx` command), auto-approve on request creation and on switch-on (D-U23-3), decider `unattended` in audit/broadcast, capability `relay.unattended.v1`, list route for D-U23-6. Tests: request created while on is approved with no client; switch-on approves open `lead`/`self_relay` and leaves `hook_ask`/`hook_permission`; member still refused; paused session raises nothing; restart keeps the switch; audit row decider.
- **PU-2 SPA:** title-bar button with off/on/partial, fan-out over shown hosts, capability gating, tooltip, "while you were away" menu, no dialog or notification for auto-approved requests. Tests use real components; tab-hosted state follows the CLAUDE.md checklist.

Owner: the lead/team/relay line (purdex-f0's successor), after its current P9 work. Review: plan with this spec through codex once; PRs R1 + attacker + critic.
