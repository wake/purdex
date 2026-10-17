# Team members stay quiet: no waiting / idle push, no unread count, a hidden unread mark (spec + plan)

User, 2026-10-17 (through lead 1f; the four details confirmed one by one). B line (88). Supersedes parts of the push
spec (`docs/specs/2026-10-09-push-spec.md` §5.2 rule 6 and its membership paragraph) and of the app badge spec
(`docs/specs/2026-10-13-app-badge-spec-plan.md` §1, §3.1); §2 lists every sentence that changes.

## 1. The rules

**Team member** (unchanged definition): a session that is an **active member** of a team — on the daemon,
`team.MembershipReader.Membership(sessionID)` (badge spec §3.1: a local member row in state `active` of a team whose
`ended_at = 0`, or an active remote member row running on this host; a lead is never a member); in the Apps, a member
whose roster row has `state === "active"` on any host's roster. A lead is not a member and keeps today's behaviour.

- **R1 Push.** A member's **waiting** and **idle** events are never pushed — not as a Mac desktop notification, not
  as an iPhone push — whether or not the session has a tab. A member still gets:
  - (a) **cards** that need the user — `hook_ask`, `hook_permission`, `self_relay`, `member_relay`, `lead`, `adopt` —
    under today's approval push rules (which never went through the member rule);
  - (b) **errors**, under the rules of any other session: the same per-agent settings, the same tab /
    「沒開分頁也通知」 condition, the same 60 s error debounce. Being a member adds no condition to an error.
- **R2 Count.** A member's unread counts **nowhere**: the Mac Dock, the iPhone badge, the Mac workspace numbers, the
  iPhone page numbers. A member's cards count as today.
- **R3 Display.** A member's **unread mark is hidden** wherever the member's session is drawn: Mac tab, pane, team
  bead; iPhone session row. Hidden means the unread tint of the status light too (the tint *is* the unread mark); the
  light's own status colour (running / waiting / idle / error) is unchanged. The unread **state is kept**, only not
  shown. When the session **leaves the team** (released / killed / gone / the team ends — it is no longer an active
  member on any roster), its mark shows again and it counts again, with whatever unread it has by then.
- **R4 Unknown membership** (lead 88's call, user confirmed 2026-10-17; see §2 last row). Whenever membership cannot be
  established, the session is treated as **not a member** — pushed, shown and counted (an extra notification beats a
  silent miss):
  - the daemon: a failed or late membership read (the payload leaves `member` out, so the phone counts it, as today);
  - the Mac: until **both** the roster and that host's session list have arrived, and for a roster seat whose session
    is not (yet) in the session list (the seat cannot be mapped to a `hostId:code`);
  - the iPhone: until that host's roster has arrived, and after it is forgotten (host removed or repointed).
  When membership becomes known the mark hides at once (the unread state is kept); when it is lost again the mark
  shows again. In practice the unknown window is the first moments of a connection.

## 2. Original text → new text

| Where | Original | New |
|---|---|---|
| push spec §5.2 rule 6 (L206-207) | "`SessionCode` is in the device's `tabs`, or `notify_without_tab` is on for the agent type — **except** that a team member (below) needs the tab whatever `notify_without_tab` says." | "`SessionCode` is in the device's `tabs`, or `notify_without_tab` is on for the agent type." (no member exception) |
| push spec §5.2 (new rule 4a, event stage) | — | "4a. Not (`st` ∈ {`waiting`, `idle`} and the session is a team member) — member spec R1." |
| push spec §5.2 membership (L210-219) | "**A read error is "member, unknown"**: rule 6 applies the member rule (the quieter side) and the payload leaves `member` out" | "A read error (or a late read) is "unknown": the gate treats the session as **not a member** (rule 4a does not drop it) and the payload leaves `member` out (the phone counts it)." |
| push spec §5.2 (L235) | "Rules 1–3, 5–7 are the Mac's (6 gains the member rule; 6a is new)" | "Rules 1–3, 4a, 5–7 are the Mac's (the Mac applies 4a from its roster; 6a is new)" |
| push spec §6 (L297-298) | "`purdex.member` … so the phone does not count its unread event" | unchanged; note added: "after member spec R1 only a member's **error** push carries it" |
| badge spec §1 Mac (L19-20) | "**Mac (Dock)**: an unread event counts only for a session open as a tab in any workspace of any window" | "… open as a tab in any workspace of any window, **and not a team member** (member spec R2)" |
| badge spec §1 iPhone pushes (L24-25) | "A member pushes only when it has a tab (as today), and its event does not count." | "A member's waiting and idle events are not pushed, tab or not; its errors push like any session's; its event does not count (member spec R1, R2)." |
| badge spec §3.1 gate (L70) | "a member session pushes only when its code is in the device's `prefs.tabs` (whatever `notify_without_tab` says)" | "a member session's waiting / idle event is not pushed; its error follows rules 5-6 like any session (member spec R1)" |
| badge spec §3.1 (L66-67) | "the gate applies the member rule (tabs only — the quieter side)" | "the gate treats it as not a member (member spec R4)" |
| team interface spec (L96) | "Beads … unread overlays as today" | "… unread overlays as today, except that a member's unread is hidden (member spec R3)" |
| badge spec §2 (L35-40, the 10-13 inventory) | "it knows nothing of team membership" | unchanged (it is the inventory of 10-13, before BG-1a); a note says the gate has known membership since BG-1a and follows this spec since MQ-1 |
| Go comments and tests (MQ-1) | `gate.go:44` (`AgentEvent.Member`: a read error counts as member), `gate.go:120-122` (`DeviceWants`: a member needs the tab), `agent_trigger.go:45-52` and `:120` (unknown → member), `internal/team/membership.go:7` ("a member pushes only with a tab"); `gate_member_test.go`, `badge_v2_test.go:137`, `:167` assert the old rule | rewritten to R1 / R4 in MQ-1 (the tests change their expectations; nothing of the old gate stays to keep them green) |
| new | — | R3: a member's unread mark is hidden on Mac and iPhone; state kept; shows again when it leaves the team |
| changed by 88 (R4) | read error = "member, unknown" = quieter side (tabs only) | read error = not a member for R1. Reason: under R1 the quieter side drops **every** waiting push of the event, and a waiting event is an agent waiting for the user; a silent miss is worse than an extra push. Read errors are rare (250 ms budget, single-flight). |

## 3. Behaviour per platform

### 3.1 Daemon (`internal/module/push`)

- Event stage gains **rule 4a**: `ev.Member && st ∈ {waiting, idle}` → no push for any device. It sits with the other
  event-stage rules (`gate.go` `EventStage`, today L101-118), before the device loop.
- `DeviceWants` (`gate.go:136-143`) loses the member clause: the tab condition is `inTabs || p.NotifyWithoutTab`.
- `agent_trigger.go:47-52`: the gate's `in.Member` becomes `member && err == nil` (unknown → not a member, R4); the
  payload's `content.Member` stays `member && err == nil` (unchanged). The membership read still runs once per event
  for the events that can push (waiting, idle, error), single-flight, 250 ms.
- Errors keep the 60 s debounce (`gate.go` `SendStage`) unchanged; a member's error passes the event stage and is
  judged per device by rules 5-6a like any session's.
- The comments and tests that state the old rule (§2, "Go comments and tests") are rewritten in the same PR.
- Approval pushes, badge pushes, read sync: untouched. No wire change, no capability: the payload is the same, an App
  sees fewer pushes. A capability would not help: an App cannot take back a push its host's daemon already sent (iOS
  can only drop a delivered notification with the filtering entitlement, which the App does not have). So R1 on the
  iPhone holds only once **every** daemon that runs members has MQ-1: **MQ-1 ships to mlab and air26 in the same
  release** (as every daemon release does), and the release's acceptance includes one member waiting event on each
  host with no iPhone push and no Mac notification. Until then a daemon without MQ-1 still pushes a tabbed member's
  waiting / idle.

### 3.2 Mac SPA

- **Member set** (new, pure + hook): `activeMemberKeys(rosterByHost, sessionsByHost, …) → Set<compositeKey>` — every
  roster member with `state === "active"` on any host's roster, mapped to the SPA's `hostId:code` the way
  `lib/team/team-views.ts` maps seats to tabs (`seatHostOf` for the seat's host, the session whose `name` equals the
  member's `tmux_session` for the code). The roster's lead is not in `members`, so it is never in the set. A seat whose
  session is not in the session list (not loaded yet, or gone) maps to nothing. `useActiveMemberKeys()` subscribes to
  `useTeamRosterStore` and `useSessionStore`. No session-id mapping is used (the SPA has none for a pane without a
  SessionStart record).
- **R1**: `lib/notification-gate.ts` `shouldNotify` takes `isMember`; `isMember && derived ∈ {waiting, idle}` →
  false, before the tab / settings checks; errors unchanged (same debounce). The caller
  (`hooks/useAgentNotifications.ts:62`) computes `isMember` **at the moment of the event** from the stores'
  current state (`activeMemberKeysOf(useTeamRosterStore.getState(), useSessionStore.getState(), …)`), never from a
  set captured when the hook mounted: its subscription is created once (`useEffect(…, [])`, L19-21), so a captured set
  would freeze membership at mount.
- **R2**: `lib/badge/badge-report.ts` (L85, L97-98) takes the member set and skips a member key's unread when it fills
  `sessions`; a resolved card still adds its key. `lib/badge/badge-reporter.ts` (L16-25, L48) passes the member set
  to `currentBadgeReport()` and **also subscribes to `useTeamRosterStore`** (it listens to unread, tabs, cards and
  sessions today), so joining or leaving a team re-sends the Dock report by itself.
  `features/workspace/useWorkspaceIndicators.ts` (L25-31) skips member keys when it counts tabs with unread, and
  subscribes to the member set so the number follows a roster change.
- **R3**: the two producers of `isUnread` return false for a member key — `hooks/useSessionAgentIndicator.ts` (L35;
  sidebar rows and team beads through `TeamSeatIcon`) and `lib/agent-lights/tab-aggregate.ts` (L74, L88; a tab is
  unread when any **non-member** pane is unread). `aggregateTabAgents` is pure: it gains a member-set argument, and its
  caller `hooks/useTabAgentAggregate.ts` (L40; used by `useTabDisplay.ts:84`) subscribes to the member set and passes
  it, so a **mounted** tab's mark changes when the roster changes. Every mark drawn from them (`TabIcon`,
  `renderInlineTabIcon`, `TabStatusIndicator` tint, the global pip in `SortableTab` / `InlineTab`) follows without
  change. `status` is untouched.
- `stores/useAgentStore.ts` (L347-387, unread set / clear) is **not** changed: the state is kept (R3).

### 3.3 iPhone (purdex-ios)

- Pushes: nothing to do; the daemon decides (R1). The NSE already does not count a push with `purdex.member`.
- Badge: already excludes members (`App/Shared/BadgeState.swift:56`); unchanged.
- **R2 / R3**: the session row light (`AgentStatus.rowLight(status:unread:approvalOpen:)`, `Models.swift:19-24`; callers
  `PageView.swift:120`, `TeamViews.swift:102`) gets `unread: false` for a member; the page unread number
  (`WorkbenchStore.aggregate(on:)`, `WorkbenchStore.swift:209-215`, shown by `OverviewView`) counts a member only for
  its open cards (`approvalSessions`), not its unread. The member set is the one the badge already computes
  (`BadgeMembers.buckets`, `BadgeCenter.swift:78-96`).
- The unread state in `WorkbenchStore` / `agentStates` is not changed (R3).

## 4. Plan (one task = one PR; TDD; full flow: plan + spec → codex, PR → codex R1 + R2)

| Task | Where | What | Depends on |
|---|---|---|---|
| MQ-1 | daemon `internal/module/push` (`gate.go`, `agent_trigger.go`), push spec + badge spec text | rule 4a; drop the member tab clause; unknown → not a member; spec edits of §2 | — |
| MQ-2 | SPA (`lib/team` member set, `notification-gate.ts`, `useAgentNotifications.ts`, `badge-report.ts`, `badge-reporter.ts`, `useWorkspaceIndicators.ts`, `useSessionAgentIndicator.ts`, `tab-aggregate.ts`, `useTabAgentAggregate.ts`) | §3.2 | — (independent of MQ-1: the Mac's notifications are its own) |
| MQ-3 | purdex-ios (`Models.swift` callers, `WorkbenchStore.aggregate`, page counts) | §3.3 | — |

Release gate: MQ-1 reaches the iPhone only when both daemons that run members (mlab, air26) have it — one release,
both hosts (§3.1). MQ-2 and MQ-3 can ship before or after; each is correct on its own platform.

Tests (each with a mutation that turns it red):
- MQ-1: member waiting → no device gets it, tab or not (M: drop rule 4a); member idle → same; member error with
  `notify_without_tab` on and no tab → pushed (M: keep the member tab clause); member error, type switched off, no tab
  → not pushed; non-member unchanged (tab / no tab / switch); membership read error on a waiting event → pushed, no
  `member` in the payload (M: unknown → member); late read (past 250 ms) → same; lead → like any session; a card of a
  member → pushed (approval path untouched); the error debounce still applies to a member's error.
- MQ-2: `activeMemberKeys` — active local member, active remote member (seat on another host, `seatHostOf`), a
  `releasing` / `joining` member excluded, the lead excluded, a seat without a session excluded (R4), no roster or no
  session list for a host → none of its sessions (R4), two hosts (M: drop the state filter; M: map by code instead of
  name); `shouldNotify` member waiting / idle → false, member error → as non-member (M: drop the member check; M: apply
  it to error too); Dock report — member unread not in `sessions`, member card still in it (M: skip cards too);
  workspace number skips a member tab; `isUnread` false for a member in the sidebar row, the bead and the tab
  aggregate, `status` unchanged (M: hide status too); a session that leaves the roster shows its kept unread again and
  counts again (M: clear unread when a member is set).
  **Transition tests on live wiring** (pure-function tests alone stay green when the wiring is missing): with the
  reporter running, a roster update that makes a tabbed unread session a member re-sends the Dock report without it,
  and a roster update that removes it re-sends it with it (M: drop the reporter's roster subscription); a **mounted**
  tab, sidebar row and workspace number flip when the roster changes, both ways (M: compute the set once); with
  `useAgentNotifications` mounted, a session that becomes a member after mount gets no waiting notification, and one
  that leaves the team after mount gets it again (M: capture the set at mount).
- MQ-3: the row light of a member with unread shows its status colour, not the unread tint; the page number skips a
  member's unread but counts its open card; leaving the team shows and counts again (M: the same three, reversed).
  Transitions (R4): a host whose roster has not arrived → its sessions shown and counted; the roster arrives → a
  member's mark hides at once, its unread kept; the host is forgotten or repointed → shown and counted again
  (M: keep the last member bucket after the host is forgotten; M: treat "no roster yet" as "all members").

Test tiers (CLAUDE.md, 2026-10-15): while developing, only the changed tests; before merge, Go — the changed packages
(`-race` only if locks or goroutines change) + `make lint`; SPA — `npx vitest related --run <changed files>` **plus every
repo-scanning guard test** (`find spa/src -name '*guard*.test.ts*'`, never picked by `related`) + `tsc` (app project
through the heavy slot: `pdx gate --class heavy -- npx tsc --noEmit -p tsconfig.app.json`; node project as is) +
eslint on the changed files; iOS — the changed test files while developing, the full `PurdexTests` once before merge,
on air26. Full suites belong to the release.

## 5. Out of scope

- Leads, muted sessions, cards, the 「沒開分頁也通知」 switch, read sync: unchanged.
- Hiding a member's **status light** or its subagent dots: R3 hides only the unread mark.
- A per-member opt-out ("let this member notify"): not asked for.
