# App badge: one count per session, on the Mac Dock and the iPhone (spec + plan)

**Status**: draft, 2026-10-13. Owner: interface line (88). Rules: the user, 2026-10-13, relayed by 1f, then confirmed
with 88 in four rounds (quoted in §1). Amends `docs/specs/2026-10-09-push-spec.md` §1 (non-goals), §5.2, §6 and §8,
and closes the badge half of purdex-ios#119.
Review: codex (gpt-5.6-sol, low) on spec + plan, 10 findings (1 critical: one capability for two steps), all folded in
(§3.1 membership / coalescing / size / capabilities, §3.2 card identity and reload drop, §4.1 resolution check,
§4.2 compatibility, §5 dependencies and tests).

## 1. The rules (user, 2026-10-13)

- **One number per device**: the count of distinct sessions that have an unread event (under each platform's condition
  below) or an open card. A session with several unread events and several cards counts 1.
- **Read state is not synced** across devices: reading a session on the Mac does not lower the iPhone badge.
- **Cards** are approvals the user must act on: `lead`, `self_relay`, `member_relay`, `adopt`, and Claude Code asking
  the user (`hook_ask`, including terminal-only ones the phone cannot answer, and `hook_permission`). A paused session
  (unattended quota pause) is not counted on its own: the card it waits on is.
- **Mac (Dock)**: an unread event counts only for a session open as a tab in any workspace of any window; a card counts
  whether or not its session has a tab.
- **iPhone**: an unread event counts for every session except **team members** and sessions **muted on this phone**;
  a card counts always (members and muted sessions included). The phone's tab list is the Mac's (profile sync), so
  "every session" means sessions with no tab too.
- **iPhone pushes (option 甲)**: every non-member session pushes (a visible notification) whether or not it has a tab.
  A member pushes only when it has a tab (as today), and its event does not count. A session can be **muted on the
  phone** (new): no agent-event push and no unread count; its cards still push and count. Mute is per phone and
  can be undone. The per-agent-type 「沒開分頁也通知」 switch stays, its default becomes **on**; a type switched off
  neither pushes nor counts for its sessions without a tab.
- **The iPhone badge drops while the App is in the background** when a card closes elsewhere (best effort: iOS may delay
  a background push, and a force-quit App receives none; the next foreground corrects it).

## 2. What exists (inventory 2026-10-13, origin/main 10116256b; purdex-ios 2d7e7fa / 0.6.73)

- Daemon: every push carries `purdex.open_approvals` + `open_approval_keys` (`s:<code>` / `a:<id>`), read at send time
  (`internal/module/push/module.go:415-466`). The kinds counted are those `push.ApprovalContent` builds a notification
  for (`internal/push/content.go:148-219`): no `adopt`, no terminal-only `hook_ask`. The agent gate
  (`internal/module/push/gate.go`) pushes a session with no tab only when the agent type's `notify_without_tab` is on;
  it knows nothing of team membership. No background push exists (`apns.go:148` hard-codes `alert`, priority 10); the
  module subscribes to approval `closed` events but only forgets the ask (`module.go:473-477`).
- Mac: unread lives in `useAgentStore.unread` (key `hostId:code`, per window, in memory). Team cards are in
  `useApprovalStore`; hook cards are filtered out of it (`spa/src/lib/team/approval-ws.ts:77-78`). No total count, no
  Dock badge, no badge IPC; several windows may each run the SPA.
- iPhone: badge = |unread ∪ `s:` keys| + `a:` keys (+ legacy counts) (`Shared/BadgeState.swift:26-41`), unread only for
  sessions on visible tabs (`Push/BadgeCenter.swift:88-89`); `adopt` and terminal-only asks not counted
  (`Approvals/TeamApproval.swift:34-40`); no background mode; `member_relay` has no card, its notification is never
  cleared and a tap says 「這則通知的類型這個版本不認得」 (purdex-ios#119); no per-session mute.

## 3. Contract changes

### 3.1 Daemon → phone

- **Badge kinds**: `open_approvals` / `open_approval_keys` count the cards of §1 — every open approval of kind `lead`,
  `self_relay`, `member_relay`, `adopt`, `hook_ask` (terminal-only included) or `hook_permission`. A new predicate
  `push.CountsForBadge(kind)` decides this; `ApprovalContent` keeps deciding only what gets a visible push.
- **Keys**: `s:<session code>` when the origin's tmux session resolves (unchanged); else **`c:<origin session_id>`**
  when the approval has an origin session id (new: several cards of one unresolved session count 1); else
  `a:<approval id>`. Same cap (32) and sort.
- **Membership** (who is a "team member"): `team.MembershipReader.Membership(sessionID) (member bool, err error)`, a
  registry interface the team module implements and the push module reads (like `OpenApprovalsReader`). True when the
  Claude Code session id is (a) a local member row (no `host_id`; never the lead) in state `active` of a team whose
  `ended_at = 0` on this host, or (b) `ActiveRemoteMemberBySession` finds it (adopted by a lead elsewhere, running
  here). Rows of remote members on a lead host describe sessions running elsewhere and are not consulted (their
  events reach their own host's daemon). A lead is not a member; an event with no session id is not a member. The
  implementation must not reuse `SessionRole` as is (it ranks lead first and has its own state set); a test pins each
  row state. **A read error is treated as "member, unknown"**: the gate applies the member rule (tabs only — the quieter
  side) and the payload leaves `member` out (the phone then counts it: an over-count beats a silent miss).
- **Agent pushes** carry **`purdex.member: true`** when `Membership` says member; absent otherwise.
- **Gate** (`gate.go` `DeviceWants`), after rules 5-6:
  - a member session pushes only when its code is in the device's `prefs.tabs` (whatever `notify_without_tab` says);
  - a session whose code is in the device's **`prefs.muted`** gets no agent push. Approval pushes ignore `muted`.
  - `Membership` is read once per event (not per device), before the device loop.
- **Background badge push** (new, BG-1b):
  - Trigger: an approval whose kind `CountsForBadge` reaches `closed` (any close path: decide, timeout, lease expiry,
    answered locally, terminal override, dismissed — all go through the team module's `closed` broadcast). Opened
    approvals never trigger it (they get a visible push or, for the kinds `ApprovalContent` refuses, none).
  - Coalescing lives **before** the sender queue: the first close starts a fixed 2 s window (not sliding, so latency is
    bounded); closes inside it add their ids; when it fires, one `badge` job is enqueued. The job reads the open set
    when the sender processes it (the freshest value). A full queue drops the job with a log line (best effort; the
    next push or foreground corrects). `Stop` cancels a pending window.
  - Recipients: devices with **`prefs.badge_push: true`**. Headers `apns-push-type: background`, `apns-priority: 5`,
    `apns-expiration: now + 1 h`, no collapse id. Body `{"aps":{"content-available":1},"purdex":{"kind":"badge",
    "host_id":…,"open_approvals":n,"open_approval_keys":[…],"closed_approval_ids":[…]}}` — no `alert`, `sound`,
    `mutable-content` or `badge` in `aps`. A failed read sends nothing.
  - Size: `closed_approval_ids` holds at most 16 ids (the rest are left out: their delivered notifications are cleared
    at the next foreground, as today); the payload stays ≤ 4096 bytes with 32 keys of maximum length plus 16 ids,
    and the builder drops ids before keys if it ever would not.
- **Registration** (`prefs`, stored as JSON, so no migration; older daemons ignore unknown fields): `muted` (session
  codes, ≤ 500, same validation as `tabs`), `badge_push` (bool).
- **Capabilities** in `/api/info`, one per shipped step so the App never trusts a half: **`push.badge.v2`** (BG-1a:
  badge kinds, `c:` keys, `member`, member gate, `muted`) and **`push.badge_bg.v1`** (BG-1b: background badge pushes
  to `badge_push` devices).

### 3.2 Mac SPA → Electron

- `window.electronAPI.badge.set(report)`, `report = { sessions: string[], cards: { id: string, session?: string,
  fallback: string }[] }`: the window's resolved session keys (unread on a tab, plus cards it resolved), and every
  open card by identity — `id` = `hostId\0approvalId` (the same in every window), `session` = the `hostId:code` this
  window resolved it to (absent if it could not), `fallback` = `c:hostId:<origin session_id>` or `a:hostId:<id>`.
- Main keeps the last report per window (`webContents.id`) and computes **S** = ∪ `sessions` over windows; then, for
  each card id, the `session` any window resolved it to (if one did) else its `fallback`, added to S. It sets
  `app.setBadgeCount(|S|)` (0 clears the Dock badge). So one card resolved by one window and not yet by another
  counts once.
- Main drops a window's report when the window closes, **and** when its renderer goes away or reloads
  (`render-process-gone`, a main-frame navigation / reload start), then recomputes; the reloaded SPA reports afresh.
- Outside Electron `window.electronAPI` is absent and the SPA sends nothing.

## 4. Behaviour per platform

### 4.1 Mac

A window's report (§3.2), built by a pure `badgeReport(...)`:
- `sessions` ⊇ U: every `hostId:code` with `unread[k] === true` whose session is shown by a live pane of any tab in
  that window's tab store (`tabAgentPanes` over all tabs, every workspace). Worker panes (`exec-<id>` keys) count the
  same way.
- `cards`: every open card, from `useApprovalStore` (all non-hook kinds) and a new hook-card slice fed by the same
  `approval.request` events `approval-ws.ts` now drops (`hook_ask`, `hook_permission`; snapshot replaces the host's
  set, opened adds, closed removes; the existing hook handling is untouched).
- Resolving a card to `hostId:code`: the session whose `name` equals the session part of `origin.tmux` in that
  host's session list, **and**, when the agent store knows a Claude Code session id for that `hostId:code`
  (`agent.sessionId`), it must equal `origin.session_id` — otherwise (a recreated session with the same name) the card
  stays unresolved and counts by its fallback. A resolved card's key is also added to `sessions`.

The renderer sends its report when it changes (debounced 300 ms, compared after sorting). No setting in v1.

### 4.2 iPhone

- badge = |(unread sessions − members − muted) ∪ `s:` keys ∪ `c:` keys| + `a:` keys (+ legacy `open_approvals` of a
  host with no keys), per host, summed. Unread is tracked for every session of the host (`WorkbenchStore.sessions`),
  not only visible tabs. Members come from the roster (`QuotaCenter`) in the App and from `purdex.member` in the NSE;
  the muted set is in the App Group so the NSE can read it.
- `CountsForBadge` matches §3.1 (`adopt` and terminal-only asks count).
- Registration sends `notify_without_tab: true` explicitly for every agent type it lists (the daemon's default for an
  unmentioned type stays tabs-only), `muted` (per host), and `badge_push: true` once background handling ships.
- Compatibility: the App writes each host's capabilities into the App Group. For a host without `push.badge.v2`
  (an older daemon: no `member` field, member events of tabbed members still pushed) the NSE falls back to the member
  set the App last wrote to the App Group from the roster; with neither, it counts the event (over-count, corrected
  at the next foreground). Unknown key prefixes are counted like `a:` keys (one each), so a newer daemon never makes
  the App under-count.
- Mute: a toggle in the session's long-press menu and its conversation menu (placement follows the App's patterns),
  shown only for sessions on hosts declaring `push.badge.v2`; a muted session shows a small muted mark.
- Background (hosts declaring `push.badge_bg.v1`): `UIBackgroundModes` gains `remote-notification`;
  `didReceiveRemoteNotification` handles `kind: "badge"` (apply keys to `BadgeState`, `setBadgeCount`, remove
  delivered notifications of `closed_approval_ids` — their identifiers are the approval ids, the visible push's
  collapse id). `badge_push: true` is registered only on such hosts.
- `member_relay` becomes a card kind (`isApproval`, `isCardKind`, tap routing, `clearClosed`) — purdex-ios#119.

## 5. Plan (one task = one PR; TDD; review per CLAUDE.md)

| Task | Where | What | Depends on |
|---|---|---|---|
| BG-1a | daemon `internal/push`, `internal/module/push`, `internal/team`, team module | `CountsForBadge`; `c:` keys (`toPushApproval` carries the origin session id); `MembershipReader` (registry key; team module implements it, §3.1); `member` flag; gate: member-without-tab, `prefs.muted`; prefs validation; `push.badge.v2`; push spec §5.2 / §6 updated | — |
| BG-1b | daemon `internal/push/apns`, `internal/module/push` | per-push APNs headers (type, priority); close coalescer before the sender; `badge` job + payload builder with the size rule; `push.badge_bg.v1`; push spec §1 non-goal removed, §6 extended | BG-1a merged |
| BG-2 | SPA + Electron | hook-card slice; `badgeReport` (pure) + the hook that sends it; `electronAPI.badge.set`; main union + reload / crash drop + `app.setBadgeCount`; split into an SPA PR and an Electron PR if over 800 lines | — |
| BG-3a | purdex-ios | formula of §4.2 (all sessions, minus members / muted; `c:` and unknown keys; `adopt` + terminal-only; per-host capability + member set in the App Group) | BG-1a deployed |
| BG-3b | purdex-ios | `notify_without_tab` default on + explicit per type; mute (App Group set, UI, `prefs.muted`) | BG-1a deployed |
| BG-3c | purdex-ios | background mode + `kind: "badge"` handling; `badge_push: true` on `push.badge_bg.v1` hosts | BG-1b deployed |
| BG-3d | purdex-ios | `member_relay` card (#119) | — |

Tests per task (beyond each PR's own TDD):
- BG-1a:
  - counted: `adopt`, terminal-only `hook_ask`, `hook_permission`; not counted: any other kind; a terminal-only ask
    still gets **no visible push** (`ApprovalContent` unchanged) while it counts;
  - two cards of one unresolved session give one `c:` key;
  - membership: a local `active` member → member; `killed` / `gone` / `released` → not; a lead → not; a member of an
    ended team → not; an active remote member running here → member; a read error → gated as member, no `member`
    field;
  - a member's agent push carries `member: true` and reaches only a device with that code in `tabs`; a muted code
    gets no agent push while its approval push still goes; prefs with `muted` / `badge_push` round-trip the store.
- BG-1b: one close → one background push per `badge_push` device, headers `background` / 5, body without
  `alert` / `sound` / `mutable-content`; three closes in 2 s → one push listing all three; a close of each badge kind
  through each close path triggers it, a non-badge kind and an opened approval do not; a read failure → none;
  devices without `badge_push` → none; 20 closes → 16 ids and a payload ≤ 4096 bytes with 32 maximal keys; a full
  queue drops with a log line; `Stop` with a pending window sends nothing.
- BG-2: `badgeReport` — unread with / without a tab, a card with / without a tab, the same session with unread +
  card = 1, a same-name recreated session (session id mismatch) stays unresolved; main — two windows with overlapping
  sessions count the union, one card resolved in one window and unresolved in another counts once, a closed window
  and a crashed / reloaded renderer drop their report.
- iOS: the formula table of §4.2; NSE ignoring a member's or a muted session's agent push; an old host (no
  capability) using the App Group member set; unknown key prefixes; the background handler.
- Acceptance on a real phone (BG-3c): App suspended → a card closed on the Mac lowers the badge; App force-quit → no
  change until the next foreground, which corrects it.

Mutation checks are deliverables (e.g. drop the member gate, count `a:` and `s:` of one session twice, skip the
renderer-reload drop).

## 6. Out of scope

Syncing read state across devices; a Mac setting to turn the Dock badge off; counting Nexen workers on the phone;
withdrawing delivered notifications on the Mac.
