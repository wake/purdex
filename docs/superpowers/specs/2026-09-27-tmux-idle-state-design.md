# "No tmux server" is an idle host, not a broken one (#1108, #1474) — design

## Background

#1473 (alpha.458) made the daemon recognise both "no server" forms, so a host
without a tmux server now reports an empty session list. Two problems remain:

1. **The daemon still calls "no server" `unavailable`.** `TmuxAlive()` is
   `tmux info` succeeding; when it fails for any reason the watcher broadcasts
   `tmux: unavailable` (on an alive → down edge only). The SPA shows a yellow
   "tmux 環境無法連線", notifies, and three gates (#1108) refuse to create a
   session. But creating a session is exactly what starts a server: on air26
   (2026-09-27) a session created from the SPA with no server running worked
   and brought tmux up. "No server" is the normal empty state after a reboot.
2. **The SPA never learns the tmux state when the daemon starts with tmux
   down** (#1474 §2): the value is only broadcast on an edge, and nothing is
   sent to a new subscriber. Fixing that alone would make things worse — every
   rebooted host would then be marked `unavailable` and lose its create button —
   so it ships together with (1).
3. **A pane waits forever on `connecting...`** when the host is reachable but
   its `sessions` payload never arrives (#1474 §1): the terminal attach gate
   only opens on that payload.

#1108 also recorded a real, never-explained "create failed with no server"
error. The most likely cause is the post-create check racing the watcher:
`SessionLauncher` checks `isHostLive` milliseconds after the create returns,
while `tmuxState` stays `unavailable` until the next 5 s tick notices the new
server (#1108 note ②). This design removes both halves of that race.

## Decisions (user, 2026-09-27)

- A host with no tmux server is shown as a **normal empty state**: green status,
  no warning text, no notification, the create button works (and starts tmux).
- A pane whose host is reachable but whose session list has not arrived for
  **10 s** shows: 「已連線到主機，但讀不到 tmux session 清單，會自動重試」
  (en: "Connected to the host, but the tmux session list can't be read. Retrying
  automatically."). It returns to normal by itself when the list arrives.

## Design

### Tmux state has three values

| Value | Meaning | SPA |
|---|---|---|
| `ok` | a tmux server answers | normal |
| `idle` | **new** — no tmux server (socket stale or absent); creating a session starts one | normal: green, no warning, create allowed |
| `unavailable` | tmux cannot be used (binary missing / not executable, any other `tmux info` failure) | as today: yellow warning, notification, create blocked |

### Phase 1 — SPA (PR-1, ships first)

A daemon that sends `idle` to an SPA that does not know it would be mapped to
`unavailable` (`useMultiHostEventWs.ts:197` maps every non-`ok` value to
`unavailable`), which would block create on every rebooted host. So the SPA
learns `idle` first; the current daemon never sends it, so PR-1 changes nothing
until PR-2 is deployed.

- S1. `HostRuntime.tmuxState`: `'ok' | 'idle' | 'unavailable'`.
  `useMultiHostEventWs`: `ok` → `ok`, `idle` → `idle`, anything else →
  `unavailable` (unknown values stay conservative).
- S2. Every existing reader already compares against `'unavailable'` only
  (`connectionErrorMessage`, `isHostLive`, `SessionSection.createDisabled`,
  `SessionsSection.isOffline`, `StatusBar`, `OverviewSection`, `HostSidebar`),
  so `idle` renders as normal with no change there. The notification
  dispatcher's L3 rule becomes "into `unavailable` from `ok` **or `idle`**"
  (a host whose tmux breaks while idle is still worth a notification); `ok` ↔
  `idle` never notifies. Tests lock each reader's `idle` rendering.
- S3. Attach-stall message. A pane bound to a known host whose attach gate is
  closed shows the 10 s message when **both** hold continuously for 10 s:
  the gate is closed (`runtime.attachReady !== true`) and the daemon is
  reachable (`runtime.daemonState === 'connected'`). Either condition breaking
  resets the timer. Implemented as a hook `useAttachStall(hostId, 10_000)` in
  `SessionPaneContent`, which passes the text through `TerminalView`'s existing
  `connectingMessage` prop. Not shown for terminated or missing-host panes
  (they render other components). While the daemon is unreachable the existing
  overlay text is unchanged.
- S4. i18n keys in `en.json` and `zh-TW.json`.

### Phase 2 — daemon (PR-2)

- D1. `tmux.ServerState` (`ServerUp`, `ServerAbsent`, `ServerBroken`) and
  `Executor.ServerState() ServerState`. `RealExecutor`: `tmux info` (5 s
  timeout, as `TmuxAlive` today) → nil error: `ServerUp`; `*exec.ExitError`
  whose stderr satisfies `IsNoServer`: `ServerAbsent`; anything else
  (exec not found, other exit): `ServerBroken`. `TmuxAlive()` becomes
  `ServerState() == ServerUp`, so its callers are unchanged.
  `FakeExecutor.SetAlive(v)` maps to Up/Absent; add `SetServerState`.
- D2. The watcher tracks the down kind: `watcherState` gains `downState string`
  (`idle` | `unavailable`), meaningful while `tmuxAlive` is false.
  - `Start()`: seed from `ServerState()` (today it seeds `tmuxAlive` only).
  - `tickNormal`, empty list and not alive: `setDown(kind)`; broadcast the kind
    when the value the SPA would see changes (ok → idle, ok → unavailable).
  - `tickTmuxDown`: `ServerUp` → recovery (below). Otherwise, if the down kind
    changed (idle ↔ unavailable), broadcast the new kind.
- D3. Current state on subscribe: register an `OnSubscribe` callback that
  queues one `tmux` frame (`ok` / `idle` / `unavailable`) for the new
  subscriber — the same frame `Events.Broadcast("", "tmux", v)` produces.
  Fixes #1474 §2.
- D4. Recovery is one guarded method, `markServerUp()`: only the caller whose
  `setTmuxAlive(true)` actually flips the state runs clear-hooks → ensureHooks →
  broadcast `ok` → `notifyWaitFor(true)` → `broadcastSessions()`. Used by
  `tickTmuxDown` and by D5; the guard makes concurrent callers safe (one
  broadcast, not two).
- D5. A successful create (`create.go`, after the session is confirmed and
  listed) calls `markServerUp()` when the watcher still thinks tmux is down.
  The creating client and every other client learn `ok` and the new list
  immediately instead of on the next tick (removes #1108 race ②).

## Non-goals

- A persistently failing list read does not flip the state to `unavailable`
  (the pane stall message covers the user-visible side). Tracked in #1478's
  spirit if needed later.
- #1478 items (A1/A4/A5).

## Acceptance

- PR-1: unit tests for S1 mapping (incl. unknown → unavailable), S2 per reader
  (`idle` = normal; create enabled; no notification on ok→idle; notification on
  idle→unavailable), S3 hook with fake timers (10 s threshold, reset on gate
  open / daemon unreachable, no message before 10 s). `pnpm run lint`,
  `npx tsc --noEmit -p tsconfig.app.json`, `npx vitest run` green.
- PR-2: unit tests for D1 classification (fake tmux on PATH: success, stale,
  absent, other error, missing binary), D2 transitions and broadcast values,
  D3 frame on subscribe for each state, D4 single broadcast under concurrent
  callers (`-race`), D5 create with watcher down → `ok` + sessions broadcast
  without waiting for a tick. Mutation: map `ServerAbsent` to `unavailable` →
  a D2 test fails.
- Real host (air26, after PR-2 deploy): `kill-server` + remove socket → SPA
  shows the host green with no sessions and the create button enabled; create
  from the SPA → session opens with no `launcher.created_offline`; stop tmux
  entirely (rename the binary is **not** done — covered by unit tests only).
