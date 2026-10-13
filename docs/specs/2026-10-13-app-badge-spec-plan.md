# App badge: one count per session, on the Mac Dock and the iPhone (spec + plan)

**Status**: draft, 2026-10-13. Owner: interface line (88). Rules: the user, 2026-10-13, relayed by 1f, then confirmed
with 88 in four rounds (quoted in §1). Amends `docs/specs/2026-10-09-push-spec.md` §1 (non-goals), §5.2, §6 and §8,
and closes the badge half of purdex-ios#119.

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
- **Agent pushes** carry **`purdex.member: true`** when the event's session is a team member (a live member of a team
  on this host, or a remote member adopted by a lead elsewhere and running here); absent otherwise.
- **Gate** (`gate.go` `DeviceWants`), after rules 5-6:
  - a member session pushes only when its code is in the device's `prefs.tabs` (whatever `notify_without_tab` says);
  - a session whose code is in the device's **`prefs.muted`** gets no agent push. Approval pushes ignore `muted`.
- **Background badge push** (new): when an approval of a badge kind closes, the daemon waits 2 s (later closes in the
  window join it), reads the open set once, and sends every device with **`prefs.badge_push: true`** one push:
  headers `apns-push-type: background`, `apns-priority: 5`, `apns-expiration: now + 1 h`, no collapse id; body
  `{"aps":{"content-available":1},"purdex":{"kind":"badge","host_id":…,"open_approvals":n,"open_approval_keys":[…],
  "closed_approval_ids":[…]}}`. A failed read sends nothing. No background push for opened approvals (they already get
  a visible one).
- **Registration** (`prefs`, stored as JSON, so no migration; older daemons ignore unknown fields): `muted` (session
  codes, ≤ 500, same validation as `tabs`), `badge_push` (bool).
- **Capability** `push.badge.v2` in `/api/info`: this daemon honours `muted`, `member`, `c:` keys and `badge_push`.

### 3.2 Mac SPA → Electron

- `window.electronAPI.badge.setKeys(keys: string[])`: the window's current badge keys (sorted, de-duplicated).
  Main keeps the last set per window (`webContents.id`), drops it when the window closes or its renderer is gone, and
  sets `app.setBadgeCount(|union|)` (0 clears the Dock badge). Outside Electron the call does not exist (no-op).

## 4. Behaviour per platform

### 4.1 Mac

A window's keys = **U ∪ C**:
- U: every `hostId:code` with `unread[k] === true` whose session is shown by a live pane of any tab in that window's
  tab store (`tabAgentPanes` over all tabs, every workspace). Worker panes (`exec-<id>` keys) count the same way.
- C: one key per open card, from `useApprovalStore` (all non-hook kinds) and a new hook-card slice fed by the same
  `approval.request` events `approval-ws.ts` now drops (`hook_ask`, `hook_permission`; opened / snapshot add,
  closed removes). Key: `hostId:code` when `origin.tmux`'s session name resolves through that host's session list;
  else `c:hostId:<origin.session_id>`; else `a:hostId:<id>`.

The renderer sends its keys when the set changes (debounced 300 ms). Main unions the windows (the same card seen by
two windows counts once). No setting in v1 (out of scope).

### 4.2 iPhone

- badge = |(unread sessions − members − muted) ∪ `s:` keys ∪ `c:` keys| + `a:` keys (+ legacy `open_approvals` of a
  host with no keys), per host, summed. Unread is tracked for every session of the host (`WorkbenchStore.sessions`),
  not only visible tabs. Members come from the roster (`QuotaCenter`) in the App and from `purdex.member` in the NSE;
  the muted set is in the App Group so the NSE can read it.
- `CountsForBadge` matches §3.1 (`adopt` and terminal-only asks count).
- Registration sends `notify_without_tab: true` explicitly for every agent type it lists (the daemon's default for an
  unmentioned type stays tabs-only), `muted` (per host), and `badge_push: true` once background handling ships.
- Mute: a toggle in the session's long-press menu and its conversation menu (placement follows the App's patterns),
  shown only for sessions on hosts declaring `push.badge.v2`; a muted session shows a small muted mark.
- Background: `UIBackgroundModes` gains `remote-notification`; `didReceiveRemoteNotification` handles `kind: "badge"`
  (apply keys to `BadgeState`, `setBadgeCount`, remove delivered notifications of `closed_approval_ids`).
- `member_relay` becomes a card kind (`isApproval`, `isCardKind`, tap routing, `clearClosed`) — purdex-ios#119.

## 5. Plan (one task = one PR; TDD; review per CLAUDE.md)

| Task | Where | What | Depends on |
|---|---|---|---|
| BG-1a | daemon `internal/push`, `internal/module/push`, team reader | `CountsForBadge`; `c:` keys; `member` flag + `MembershipReader` (registry key, team module implements it); gate: member-without-tab, `prefs.muted`; prefs validation; `push.badge.v2`; push spec §5.2/§6 updated | — |
| BG-1b | daemon `internal/push/apns`, `internal/module/push` | `apns` headers per push (type, priority); background badge push on close (2 s coalesce, `badge_push` devices); push spec §1 non-goal removed, §6 added | BG-1a |
| BG-2 | SPA + Electron | hook-card slice; `badgeKeys` (pure, unit-tested) + hook that sends them; `electronAPI.badge.setKeys`; main union + `app.setBadgeCount`; split into SPA / Electron PRs if over 800 lines | — |
| BG-3a | purdex-ios | formula of §4.2 (all sessions, minus members / muted; `c:` keys; `adopt` + terminal-only) | — (uses `member` once BG-1a ships) |
| BG-3b | purdex-ios | `notify_without_tab` default on + explicit per type; mute (App Group set, UI, `prefs.muted`) | BG-1a deployed |
| BG-3c | purdex-ios | background mode + `kind: "badge"` handling; `badge_push: true` | BG-1b deployed |
| BG-3d | purdex-ios | `member_relay` card (#119) | — |

Tests per task (beyond each PR's own TDD):
- BG-1a: an adopt and a terminal-only ask are counted and keyed; two cards of one unresolved session give one `c:`
  key; an agent push for a member carries `member: true` and reaches only a device with that code in `tabs`; a muted
  code gets no agent push but its approval push still goes; prefs with `muted` round-trip through the store.
- BG-1b: one close → one background push per `badge_push` device with the right headers and body; three closes in
  2 s → one push listing all three; a read failure → none; devices without `badge_push` → none.
- BG-2: `badgeKeys` — unread with / without a tab, a card with / without a tab, the same session with unread + card
  = 1, unresolved card keys; main — two windows with overlapping keys count the union, a closed window's keys drop.
- iOS: the formula table of §4.2, NSE ignoring a member's or a muted session's agent push, background handler.

Mutation checks are deliverables (e.g. drop the member gate, count `a:` and `s:` of one session twice).

## 6. Out of scope

Syncing read state across devices; a Mac setting to turn the Dock badge off; counting Nexen workers on the phone;
withdrawing delivered notifications on the Mac.
