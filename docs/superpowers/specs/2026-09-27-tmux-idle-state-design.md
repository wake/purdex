# "No tmux server" is a usable host, not a broken one (#1108, #1474) — design

## Background

#1473 (alpha.458) made the daemon recognise both "no server" forms, so a host
without a tmux server now reports an empty session list. Three problems remain:

1. **The daemon still calls "no server" `unavailable`.** `TmuxAlive()` is
   `tmux info` succeeding; when it fails for any reason the watcher broadcasts
   `tmux: unavailable` (on an alive → down edge only). The SPA shows a yellow
   "tmux 環境無法連線", notifies, and three gates (#1108) refuse to create a
   session. But creating a session is exactly what starts a server: on air26
   (2026-09-27) a session created from the SPA with no server running worked
   and brought tmux up. "No server" is the normal empty state after a reboot.
2. **The SPA never learns the tmux state when the daemon starts with tmux
   down** (#1474 §2): the value is only broadcast on an edge, and nothing is
   sent to a new subscriber.
3. **A pane waits forever on `connecting...`** when the host is reachable but
   its `sessions` payload never arrives (#1474 §1): the terminal attach gate
   only opens on that payload.

#1108 also recorded a real, never-explained "create failed with no server"
error. The most likely cause is the post-create check racing the watcher:
`SessionLauncher` checks `isHostLive` milliseconds after the create returns,
while `tmuxState` stays `unavailable` until the next 5 s tick notices the new
server (#1108 note ②). With (1) fixed the state is never `unavailable` for a
missing server, so that race cannot fire.

## Decisions (user, 2026-09-27)

- A host with no tmux server is shown as a **normal empty state**: green status,
  no warning text, no notification, the create button works (and starts tmux).
- A pane whose host is reachable but whose session list has not arrived for
  **10 s** shows: 「已連線到主機，但讀不到 tmux session 清單，會自動重試」
  (en: "Connected to the host, but the tmux session list can't be read. Retrying
  automatically."). It returns to normal by itself when the list arrives.

## Key choice: no new wire value

The `tmux` event keeps its two values; their meaning becomes **"can tmux be
used"** rather than "is a server running":

| Wire value | Server state |
|---|---|
| `ok` | a server answers, **or** no server exists (creating a session starts one) |
| `unavailable` | tmux cannot be used: binary missing / not executable, `tmux info` timing out, any other `tmux info` failure |

A new `idle` value was considered and rejected (plan review, 2026-09-27): the
Electron app loads its **bundled** renderer by default (`electron/window-manager.ts`
`app://./index.html`; the dev server only via Settings → Development), so any
installed `.app` older than the change would map `idle` to `unavailable`
(`useMultiHostEventWs.ts:197`) and block create on every rebooted host. The
decided UX renders "no server" exactly like `ok`, so no client needs the
distinction. Old SPA + new daemon and new SPA + old daemon both behave
correctly.

## Design

### Daemon

- D1. `tmux.ServerState` (`ServerUp`, `ServerAbsent`, `ServerBroken`) and
  `Executor.ServerState() ServerState`. `RealExecutor`: `tmux info` under a 5 s
  timeout (as `TmuxAlive` today) → nil error: `ServerUp`; `*exec.ExitError`
  whose stderr satisfies `IsNoServer`: `ServerAbsent`; anything else (exec not
  found, other exit, deadline): `ServerBroken`. `TmuxAlive()` stays
  `ServerState() == ServerUp` — the watcher's internal up/down state machine
  (wait-for gate, hooks, `tickNormal` vs `tickTmuxDown`) is unchanged.
  `FakeExecutor`: `SetAlive(v)` maps to Up/Absent; add `SetServerState`.
- D2. The **reported** value is separate from the internal up/down:
  `reported = ok` unless the last probe said `ServerBroken`. `watcherState`
  gains `broken bool`. Every place that probes (`Start()`, `tickNormal`'s empty
  list check, `tickTmuxDown`) records the probe result; a `broken` change
  broadcasts the new reported value. Internal up → down with the server merely
  absent broadcasts nothing (the reported value stays `ok`).
- D3. Current value on subscribe: an `OnSubscribe` callback queues one `tmux`
  frame with the reported value. Ordering: the reported-value change + its
  broadcast and the subscribe callback's read + queue happen under one mutex
  (`statusMu`), so a new subscriber cannot receive a value older than one
  broadcast after it was added. (Events adds the subscriber before callbacks
  run, `core/events.go`; with the mutex, whichever of broadcast / snapshot runs
  second carries the newest value.)
- D4. After a successful create (`create.go`, after the session is confirmed
  and listed), if the watcher is internally down, **start** the recovery path
  (`go m.markServerUp()`, after `createMu` is released) so hooks, wait-for and
  the sessions broadcast do not wait for the next 5 s tick. `markServerUp()` —
  the body of today's `tickTmuxDown` recovery — runs only for the caller whose
  `setTmuxAlive(true)` flips the state.
  - Asynchronous on purpose (PR review A1/A2): the hook subprocesses have no
    deadline (pre-existing), so running them under `createMu` could block every
    later create; and a create that loses the flip to an in-progress tick
    recovery has nothing to wait for. Nothing user-visible needs the recovery to
    finish before the create returns: the reported value does not depend on it,
    and the creating client uses the create response.
  - Lifecycle (review A4 + re-reviews): recovery and `Stop()` are linearised
    by `lifeMu` (recovery holds it shared for its whole run, `Stop()` takes it
    exclusively after cancelling `runCtx`). Guarantee: a recovery whose entry
    check runs after `runCtx` is cancelled changes nothing; a recovery already
    past its entry check is **before** `Stop()` — it completes (its state flip
    and `tmux: ok` are true statements about a server that is up) and `Stop()`
    waits for it before removing the hooks, so nothing is announced or
    installed after `Stop()` removed them. A second check before
    `notifyWaitFor` / `broadcastSessions` skips the tail when `Stop()` began
    meanwhile. "Stop began" is not a point after which the watcher's cached
    alive flag must stay false: after `Stop()` the module no longer acts on it.
  - A `tickNormal` that probed "down" before the create and writes after it can
    still flip the internal state back; that is self-healing (the next
    `tickTmuxDown` probe sees Up and recovers) and never changes the reported
    value, so the SPA sees nothing.

### SPA

- S1. Attach-stall message: a `tmux-session` pane bound to a known host shows
  the 10 s message **while its terminal is not yet attached** (the only time
  `TerminalView` shows an overlay; an attached terminal keeps working when the
  host-events connection drops, and needs no message). Condition, held
  continuously for 10 s: the attach gate is closed
  (`runtime.attachReady !== true`) **and** the daemon is reachable
  (`runtime.daemonState === 'connected'`). `status` flapping between
  `reconnecting` and `connected` during the daemon's subscribe-retry loop does
  not reset it (`daemonState` stays `connected` across an events-WS close,
  `useMultiHostEventWs.ts:231-247`). Either condition breaking resets the timer.
  The timer is scoped to the host: a pane re-bound to another host starts a
  fresh 10 s even if both hosts are waiting (review R1-b/A3).
  Hook `useAttachStall(hostId, 10_000)` in `SessionPaneContent`, passed through
  `TerminalView`'s existing `connectingMessage` prop.
- S2. i18n key `session.attach_stalled` in `en.json` and `zh-TW.json`.

No other SPA change: every reader already treats anything but `unavailable` as
usable, and #1108's three gates only block on `unavailable`, which now means
tmux is genuinely unusable — where blocking is correct.

## Non-goals

- A persistently failing list read does not flip the reported value to
  `unavailable` (the pane stall message covers the user-visible side).
- #1478 items (A1/A4/A5).

## Acceptance

- Daemon unit tests: D1 classification with a fake `tmux` on PATH (exit 0 → Up;
  stale and absent-socket stderr → Absent; other error → Broken; tmux not on
  PATH → Broken; a hung `tmux info` → Broken after the deadline — make the
  deadline injectable for the test). D2: Up→Absent broadcasts nothing;
  Up→Broken broadcasts `unavailable`; Absent→Broken broadcasts `unavailable`;
  Broken→Absent broadcasts `ok`; repeated same-state ticks broadcast nothing.
  D3: first `tmux` frame for a new subscriber is `ok` for Up and Absent,
  `unavailable` for Broken; a concurrent change + subscribe never leaves the
  subscriber's last frame stale (`-race`). D4: create with the watcher down
  → shortly after it returns (poll with a deadline), hooks installed and a
  `sessions` frame broadcast with no tick called; create never runs the
  recovery while holding `createMu`; after `Stop()` a recovery broadcasts nothing; two
  concurrent `markServerUp` → one recovery.
- Mutation: classify `ServerAbsent` as broken → a D2 test fails.
- SPA unit tests: `useAttachStall` with fake timers (9.9 s false; 10 s true;
  gate opens → false; `daemonState` unreachable → false and restarts; status
  flapping `reconnecting`/`connected` with gate closed and daemon connected
  across 10 s → true). `SessionPaneContent` renders the message when stalled
  and the terminal is not attached.
- Real host (air26 after deploy): `kill-server` + remove socket → host green,
  no "tmux 環境無法連線", create from the SPA opens the session with no
  `launcher.created_offline`.
