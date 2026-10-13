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
- ~~**Read state is not synced** across devices~~ — *superseded the same day (user, 2026-10-13, round 6): reads are
  synced half-passively, best effort, §7.*
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
  when the approval has an origin session id of at most 64 bytes (new: several cards of one unresolved session count
  1; a longer id falls back to `a:`, so one odd id can never push the payload past 4 KiB); else `a:<approval id>`.
  Same cap (32) and sort.
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
  - `Membership` is read once per event (not per device), before the device loop, single-flight with a 250 ms
    budget: a read still running, or one past the budget, is "member, unknown" (as a read error), so a busy `team.db`
    never stalls the agent hub's queue.
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
- `sessions` ⊇ U: every `hostId:code` with `unread[k] === true` whose session is shown by a **live** pane of any tab
  in that window's tab store (`paneAgentKey` over every leaf of every tab, every workspace; a terminated pane does
  not count — `tabAgentPanes` keeps a terminated primary pane's key for the lights, and a tmux restart reuses codes).
  Worker panes (`exec-<id>` keys) count the same way.
- `cards`: every open card, from `useApprovalStore` (all non-hook kinds) and a new hook-card slice fed by the same
  `approval.request` events `approval-ws.ts` now drops (`hook_ask`, `hook_permission`; snapshot replaces the host's
  set, opened adds, closed removes; the existing hook handling is untouched).
- Resolving a card to `hostId:code`: the session whose `name` equals the session part of `origin.tmux` in that
  host's session list, **and**, when a live pane of that `hostId:code` records a Claude Code session id (the pane's
  `rebuild.agent.sessionId`, written at SessionStart; any of several live panes matching is enough), it must equal
  `origin.session_id` — otherwise (a recreated session with the same name) the card stays unresolved and counts by its
  fallback. A session with no tab has no such record and resolves by name alone. A resolved card's key is also added to
  `sessions`. (BG-2 review, 2026-10-13.)

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

Guaranteed read-state consistency across devices (§7 is best effort); a Mac setting to turn the Dock badge off;
counting Nexen workers on the phone;
withdrawing delivered notifications on the Mac.

## 7. Read sync, half-passive (user, 2026-10-13, round 6)

### 7.1 Rules

- When any device turns a session X from unread to read (a user action — on the Mac the tab that shows X becomes
  visible, or a notification click; on the iPhone X is opened), it tells X's host daemon "X read through T".
  A session that was already read, or one cleared by its own `running` status, sends nothing.
- **Seen live counts as read** (88, 2026-10-13, from the BG-3e review): when an event that would set X's unread
  arrives while X is on screen on a device (so that device never marks it unread), that device sends "X read through
  the event's cursor" too (debounced 2 s per session) — otherwise the other devices keep an unread the user already
  saw.
- The daemon **remembers** the last read of each session in memory (forgotten on restart; never on disk).
- It **forwards** the read at once over WS to every connected device that opted in (Mac windows, other Macs, an
  iPhone in the foreground).
- **Pushes carry it on the way**: per phone, the daemon keeps the reads since the last push it sent that phone; the
  next push of any kind to that phone (agent, approval, background badge) carries them and clears the list.
- **Flush after n minutes**: when that list is not empty and nothing has been pushed to that phone for **n = 15
  minutes** (a setting), the daemon sends one background push carrying it.
- **Reconcile on (re)connect**: a device connecting to the daemon receives every remembered read and clears what they
  cover.
- A receiving device clears X's unread **only if that unread came from an event at or before T** (a later event
  stays unread); the iPhone also removes X's delivered agent notification. A received read is never forwarded again.
- Best effort: a device that misses a read (offline, force-quit, a push iOS delayed or dropped) keeps its state; the
  reconcile on its next connection is the only catch-up.


### 7.2 Contract

- **T is a hook cursor, not a time**: `{"epoch": E, "seq": N}`, the `(epoch, seq)` the daemon stamps on every `hook`
  frame (`internal/module/agent/hookemitter.go:49-67`). It orders the events of one daemon process exactly — no clock,
  no unit, no same-millisecond tie — and a new epoch (a daemon restart) makes older reads incomparable. **A read
  covers an unread iff they share the epoch and the unread's seq ≤ T.seq**; a different epoch never clears anything.
  Because seq only grows within an epoch, a session code reused by a later session cannot be cleared by an earlier
  read. An unread with no cursor (set by a frame not from the emit slot — epoch `""`, seq 0 — or before this feature)
  is covered by any read of its code.
- **Which T a reader sends**: the cursor of the event that last set this unread (each platform keeps it per unread
  key, updated by every event that sets or re-sets the unread), captured **at the unread → read transition**, never
  re-read later. A reader with no cursor (an event from before this feature) omits T and the daemon uses the newest
  cursor it has emitted for that session.
- `POST /api/sessions/{code}/read` `{"through": T?}` → 204, or **404 `{"error":"no_such_session"}`** for a code that is
  not a session code or that the daemon's in-memory session list (the one `GET /api/sessions` serves, never a tmux read
  of its own) does not have; with no usable list (never filled, invalidated by a create / rename / delete since, or older
  than 2 minutes) nothing is evidence, and the read is accepted only for a session the daemon has seen (an event of it, or an
  earlier read), so made-up codes cannot push real reads out of the bounded store. A client ignores the 404. A `through`
  is clamped to what the daemon has emitted: a seq above the newest cursor of that session (above the epoch's
  high-water mark for a session with no event) is lowered to it, so a wrong or hostile cursor cannot cover unreads that
  do not exist yet; a trailing second JSON value in the body is a 400. A session created a moment ago
  and not yet in the list may be refused: best effort. In the device scope allow-list (BG-4). The daemon keeps
  `lastRead[code]` = the larger T (a smaller or other-epoch T never replaces a current-epoch one), bounded to 4096
  codes (least recently updated evicted), and **once a minute drops the codes the in-memory session list no longer has**
  (it does nothing while the list is unusable); all of it lives in memory.
- **WS** `/ws/host-events?reads=v1` (opt-in): on connect a `session.reads` frame `{"reads":[{"code":…,"through":T}]}`
  (`[]` when there is none), and a `session.read` frame `{"code":…,"through":T}` per accepted read (one that moved
  `lastRead` forward; a smaller or equal T is answered 204 and announced to no one). Both are the usual host-event
  envelope `{"type":…,"session":…,"value":"<that JSON as a string>"}` (`session` is the code on `session.read`), to every opted-in connection including the
  sender's. These two frame types are **strict for opted-in connections only** (`BroadcastStrictTo` with a `reads`
  feature): a full buffer closes that connection, whose reconnect brings a fresh snapshot; `reads=v1` does not make
  any other frame strict, and connections without it never receive them. Applying a read is idempotent and
  monotonic, so the snapshot and the live frames need no relative order (an older snapshot after a newer delta
  changes nothing).
- **Pushes** (BG-4 extends the sender, since only it knows the device's current prefs and the APNs outcome):
  - Pending reads are kept per push registration `DeviceID`, in the order each code first became pending (a newer T
    for a pending code updates it in place). They are deleted with the registration on every removal path:
    unregister, APNs "remove", owner revoke, re-registration under a new device id; `Stop` cancels every flush timer.
  - Every push to a device with **`prefs.read_sync: true`** takes up to the 16 oldest pending entries as
    `purdex.reads: [{"code":…,"through":T}]`. The visible-push builder fits them in the 4 KiB budget before trimming
    the body; if the fixed fields still do not fit with the body empty it drops reads from the newest end until they
    do, and those stay pending. A push that fails to build carries nothing.
  - **Acknowledge by version**: after a successful send, each sent `(code, T)` is removed only if the pending entry
    for that code still holds that T (compare-and-delete); a failed send (after the sender's one retry) removes
    nothing. A read arriving while the push is in flight therefore survives.
  - **Flush**: when a device has pending reads and nothing was sent to it for **15 minutes** (setting
    `push.read_flush_minutes`, 1–120), one background push (the BG-1b headers) with
    `{"aps":{"content-available":1},"purdex":{"kind":"reads","host_id":…,"reads":[…]}}`. The 15 minutes count from the
    last push sent to that device, of any kind.
  - Every agent push carries `purdex.event_cursor: {"epoch":…, "seq":…}` (the event's hook cursor,
    `agent.NotifyEvent.Event.Epoch / Seq`); absent when the epoch is `""`.
- Capability **`session.read_sync.v1`**; a client uses none of this against a host that does not declare it.

### 7.3 Per platform

- **Mac (BG-5)**:
  - The agent store keeps, per unread key, the hook cursor of the event that last set it.
  - `markRead` becomes source-aware: a **user read** (`active-tab-mark-read.ts`, the notification-click paths in
    `notification-click.ts`) of a host session key whose unread was set returns that key's cursor and POSTs it
    (debounce 2 s per key, keeping the largest captured T — never a cursor read at fire time). No POST for `exec-*`
    worker keys, a key that was already read, a `running` / `clear` / host-prefix clear, or a received read.
  - Seen live: an actionable event of a host session key that is not marked unread only because the agent is visible
    in the active tab POSTs that event's cursor (same debounce).
  - Opt in with `reads=v1` and POST only to hosts declaring the capability; apply `session.reads` / `session.read` by
    the covering rule of §7.2.
- **iPhone (BG-3e)**, all gated per host by `session.read_sync.v1` (POST, `reads=v1`, `prefs.read_sync`):
  - Unread entries keep their hook cursor (`purdex.event_cursor` from the NSE, the frame's cursor in the App); the
    App Group format gains it (an alpha reset is acceptable).
  - On opening a session (`markOpened`), POST the captured cursor.
  - Seen live (follow-up BG-3f): an actionable event of the session whose conversation or deck is on screen POSTs
    that event's cursor (debounced 2 s).
  - Apply reads from WS, from `purdex.reads` in the NSE, and from `kind: "reads"` / `kind: "badge"` in the background
    handler; remove the delivered notification `agent-<code>` (the agent push's collapse id) of each covered session.
  - An older host: everything stays local, no new request is sent.

### 7.4 Plan

| Task | Where | What | Depends on |
|---|---|---|---|
| BG-4 | daemon | read route + device scope; `lastRead` (bounded, session end); `reads=v1` frames (feature-scoped strict); sender integration: per-device pending, reservation, version ack, removal paths, flush timer; `event_cursor`; `read_sync` pref; setting; capability; push spec | BG-1b merged |
| BG-5 | SPA | per-key unread cursor; source-aware `markRead` and its callers; POST with debounce; `reads=v1` apply | BG-4 deployed |
| BG-3e | purdex-ios | unread cursor; POST on open; apply reads (WS, NSE, background); remove delivered notifications; capability gate | BG-4 deployed, BG-3c |

Tests:
- BG-4:
  - the cover rule: same epoch with seq ≤ T clears, a later seq or another epoch does not; `lastRead` only moves
    forward; eviction at 4096; the entry goes with its session;
  - a connect gets the snapshot; a read reaches every `reads=v1` connection and no other; a full buffer closes only
    an opted-in connection, and other frames keep their treatment;
  - reads ride the next push of each kind to `read_sync` devices only; 20 pending → the 16 oldest go, 4 stay; a
    newer T arriving while the push is in flight survives its ack; a failed send keeps all; reads trimmed for size
    stay pending; a build failure acks nothing;
  - the flush fires 15 min after the last push and not while pushes keep going; the removal paths drop pending and
    timers; `Stop` leaves no timer.
- BG-5: the cursor captured at the transition (an event inside the debounce does not raise T); only user reads of
  host sessions POST; a received read posts nothing; a covering / non-covering read; an older host gets no POST.
- BG-3e: the same cover cases; NSE and background application; delivered notification removal; an older host.
