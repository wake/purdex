# Profile Sync — a lapsed lease leaves "sync state unknown" on screen for good

Date: 2026-09-30 · Scope: SPA (`spa/src/lib/profile/`) · One PR

## Symptom

Settings › Workbench › Sync, on both machines, repeatedly shows
"同步狀態不明" + "原本負責同步的視窗已經不在了，這裡顯示的內容可能已經過期。"
(`settings.profile.current.stale`) — with only ONE Purdex window open, and it stays so.

## Root cause (reproduced in a unit test before this spec was written)

1. The main Electron window keeps Chromium's default `backgroundThrottling`
   (only browser views turn it off, `electron/browser-view-manager.ts:80`). A hidden or
   occluded window runs its timers late: aligned to 1 s, and to once a minute under
   intensive throttling. Browsers do the same to background tabs (web version).
2. The lease (`leader.ts`) is renewed every 2 s and lives 6 s. A late renewal lets it
   EXPIRE while the holder is alive. `isLeader()` reads storage and checks `expiresAt`,
   so it answers `false` during the lapse — while the in-memory `leader` flag stays `true`.
3. Any `channel.refresh()` during the lapse (the executor reporting, a host-store change)
   puts the holder's own window on the FOLLOWER branch of `sync-status.ts`. It reads the
   record it wrote itself; older than 10 s and no live lease → `stale: true`.
4. Nothing ever undoes it:
   - `sync-status.ts:659-660`: once stale, no timer is armed ("only a new record changes that").
   - `renew()` finds the lease still its own and rewrites it. `leader` never changed in
     memory, so `onChange` does not fire, so start.ts never calls `changed()`/`refresh()`.
   - A synced profile has nothing new to publish, so no new record comes.
5. The same holds for a real follower: if the holder recovers without a new publish, a
   follower that already called the record stale keeps saying so.

Data is not lost: during the lapse the executor's `isLeader()` gate refuses writes
(`not-leader` problem, push retried with backoff), and resumes after the renewal.

## Changes

### F1 — leader.ts: say when a lapsed lease is taken back

`Leadership` gains `onRecovered(cb: () => void): () => void`. It fires when `renew()`
read a lease that is this window's but had EXPIRED (`expiresAt <= now()` at the read),
and rewrote it successfully — i.e. `isLeader()` answered `false` for a while and now
answers `true` again, with no `onChange` in between.

- Not fired on an ordinary renewal (lease still live), nor in storageless mode, nor
  after `stop()` / while hidden, nor when the renewal steps down (that is `onChange(false)`).
- Same listener rules as `onChange`: no replay, a throwing listener is logged and does
  not stop the others, nothing after `stop()`.
- No step-down/re-lead: the lease was never anyone else's, so the driver is kept
  (a teardown would cost a full reindex every time the window goes to the background).

### F1b — start.ts: re-judge on recovery

`enterMasterMode` subscribes `leadership.onRecovered(() => apply(leadership.isLeader()))`
and unsubscribes in `end()`. `apply` already rebuilds a driver torn down during the lapse
(it may have been: `apply(false)` from a host/suspension change) and calls `changed()`,
which refreshes the channel — the leader branch then re-publishes if the follower
branch had cleared `publishedSignature`.

### F2 — sync-status.ts: stale is not terminal

On the follower branch, when the record is judged stale, arm `staleTimer =
setTimeout(refresh, STALE_RECHECK_MS)` (new exported constant, 2 000 ms = the lease's
renew period) instead of no timer. Each recheck reads the record and the lease again:
a lease that is live again (holder recovered, or THIS window renewed its own) clears
`stale` within 2 s — in a follower, and in the holder's own window even without F1.
`setSnapshot` is deduplicated, so a recheck that finds nothing new notifies nobody.
The timer is cleared exactly like the existing one (leader branch, other-master record, `close`).

## Not changed

- Lease TTL / renew period, and the stale threshold (10 s).
- Electron `backgroundThrottling` of the main window: turning it off only makes the
  lapse rarer in Electron (not in a browser tab), and costs background CPU for every
  terminal. F1+F2 fix the display wherever the lapse comes from.
- The `not-leader` problem reported by the executor during a lapse stays as is.

## Acceptance

- Unit (leader): lapse → renew fires `onRecovered` once, `onChange` not at all; live renewal
  does not fire it; stop/hidden/storageless do not; unsubscribe works.
- Unit (sync-status): holder's own window goes stale during a lapse and returns to
  `remote:false, stale:false` after the lease is live again (≤ `STALE_RECHECK_MS`);
  a follower's stale clears within `STALE_RECHECK_MS` once `leaseLive` is true with no new record;
  close clears the recheck timer (update the "no timer behind" test).
- Unit (start): after `onRecovered`, the snapshot is the local leader's, not stale.
- Manual: mlab + air26 App, leave the window hidden > 1 min, come back: no stale warning
  (or it disappears within 2 s).
