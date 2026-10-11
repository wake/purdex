# Push: phone only when you are away from the computer — spec + plan (rev 3)

Status: approved (codex reviewed, rev 3.1); PA-1 in progress. User decisions 2026-10-11 (purdex-1f). rev 2 takes codex review `task-mv3nefgr-rbvam2` (11 important, 1 minor; all taken); rev 3 takes the incremental review `task-mv3nyz3w-cw3kv0` (5 partial + 3 new important; all taken). See §9. Amends `docs/specs/2026-10-09-push-spec.md` §5.1 (approvals R6), §5.2 rules 4/7/8 (agent events) and §5.4 (Mac presence). Desktop notifications (the Mac App's own `shouldNotify`, `spa/src/lib/notification-gate.ts`) are **unchanged**.

## 1. Why

Today a phone push is skipped only while a Mac window **has focus, saw input in the last 120 s, and shows that very session** (rule 4 / R6). A user at the Mac who types in another app or has the session in a background tab gets the Mac notification **and** the phone push; a user who stepped away a minute ago is pushed at once. Messaging apps decide by "is the person at the computer" instead (Slack: 1 min after screen lock or 10 min without input; Teams: ~3 min idle; Discord: a user-set inactive timeout).

## 2. Behaviour (user-confirmed, 2026-10-11)

1. **At the computer** = some Mac reports, within its TTL, that the **machine** (not the Purdex window) saw keyboard or pointer input in the last **N** minutes and the screen is not locked. N defaults to **5**; the user sets it in the Mac App's Settings (choices 1 / 2 / 3 / 5 / 10 / 15 / 30 / 60 min).
2. **Screen locked**: 1 minute after the lock the user counts as away. Fixed.
3. **Mac App not running, Mac asleep, network gone**: no report within the TTL (45 s) → away.
4. **Several Macs**: any one at the computer is enough (each Mac judged with its own N).
5. **At the computer → no phone push at all**: agent events (waiting / done / error) and approvals alike, whatever session, tab or tmux origin. The Mac App's own notifications follow today's rule.
6. **Away → phone push as today** (device prefs, phone tabs / `notify_without_tab`, error debounce, AskUserQuestion de-duplication, workbook hold). `waiting` gets no shorter threshold.
7. **No replay**: an event that arrived while the user was at the computer is not pushed later when they leave; only events that happen while away are pushed. (Acceptance is phrased as boundary probes, §8.)

## 3. Wire: `PUT /api/push/presence`

### 3.1 v2 fields (optional; absent = a v1 report, exactly as today)

```json
{ "client_id": "mac:<install id>", "active": true, "sessions": [...], "ttl_ms": 45000,
  "v": 2, "idle_ms": 12000, "locked": false, "locked_ms": 0, "away_after_ms": 300000 }
```

- `v`: the integer `2` marks a v2 report. Any other non-null value → 400 `v: want 2`. Absent **or `null`** → v1 (the other v2 fields are then ignored).
- With `v: 2` all of these are **required**: `idle_ms` (int64, 0 – 2,592,000,000), `locked` (bool), `locked_ms` (int64, 0 – 2,592,000,000; validated even when `locked` is false, ignored then), `away_after_ms` (int64, 60,000 – 3,600,000). Missing or out of range → 400 naming the field (`idle_ms: required`, `away_after_ms: want 60000-3600000`). A JSON type error (string, fraction, null) stays 400 `invalid_json` as for every field today — the existing error contract is not changed.
- Decoding: the request type gains `V *int`, `IdleMs *int64`, `Locked *bool`, `LockedMs *int64`, `AwayAfterMs *int64` (pointers so "absent" is distinguishable; `null` decodes as absent and, with `v: 2`, is reported as `required`). Tests cover omitted, 0, null, string, fraction, negative, max, max+1 for each.
- `idle_ms` / `locked_ms` are **as of the moment the body is built** (the sender adds the age of its sample, §5.3).
- `active` / `sessions` keep their v1 meaning and are still sent (legacy rule, and older daemons).
- **The threshold travels in the report** (`away_after_ms`): N is a per-Mac App setting, so there is no host setting, no hostconfig key and no per-host settings UI (rev 1's `push.away_after_s` is dropped).

### 3.2 Capability

`/api/info` lists `push.presence.v2` (next to `push.v1`, `internal/core/info_handler.go:93`) **only when the push module is ready**, i.e. under the same condition as `push.v1`.

## 4. Daemon rules

### 4.1 Presence store

- Each entry stores today's fields plus, for a v2 report: `idleMs`, `locked`, `lockedMs`, `awayAfterMs`, `receivedAt` (daemon monotonic clock, `time.Now()` values).
- `AtComputer(now) bool`: some unexpired **v2** entry with
  - `elapsed = now − receivedAt`;
  - locked: `lockedMs + elapsed < 60 s`;
  - not locked: `idleMs + elapsed < awayAfterMs`.
- `ShowsCode(code)` / `ShowsName(name)` (legacy): unchanged, but consider **only v1 entries** (a v2 entry never feeds the legacy match; its `sessions` are informational).
- `Status()` adds `presence_at_computer` (0/1 now, plus the count of v2 entries) — numbers only.
- In memory only, as today; a daemon restart forgets it (the Mac re-sends within 20 s, §5.3; until then a push may go out — accepted).

### 4.2 Agent events (§5.2 order, amended)

- Event stage: rule 0 (freshness, records the timestamp even when later gated) → 1 → 2 → 3 → **4′**:
  - `AtComputer(now)` → gated out (no push; rule 7 is not touched).
  - else legacy: a v1 entry is `active` and shows the session code → gated out (today's behaviour for older Apps).
- Device stage (5, 6) as today.
- **Final presence check**: the waiting hold (rule 8, 2 s) and the workbook hold (`push_wait_s`, ≤ 30 s) run between the gate and the enqueue. Right before enqueueing, `AtComputer(now)` is checked again (no side effects); true → dropped.
- **Rule 7 (error debounce) stays where it is** (at arrival, inside `Gate.Decide`, `gate.go:74`), keeping today's contract: the arrival-time window, and a suppressed proxy StopFailure still feeding the debounce (`agent_trigger_proxy_test.go:44`). Accepted side effect: an error dropped by the final check (the user came back during a hold) still opens its 60 s debounce window — the user is at the computer then and sees the Mac's own notification.

### 4.3 Approvals (§5.1, amended)

- `AtComputer(now)` first, **unconditionally** (an approval whose origin has no tmux is suppressed too) → no push.
- Else legacy: only when the origin has a tmux name, a v1 entry that is `active` and shows that name → no push (today's behaviour).
- Else push to every device as today, re-checking `AtComputer` right before the enqueue.

### 4.4 Rule 8 (AskUserQuestion de-duplication)

- Today `onApproval` records the ask interval before the presence gate (`module.go:478` vs `:493`), so a suppressed approval still makes a later `waiting` event look like a duplicate and the only notice is lost.
- New: the ask interval used by rule 8 is recorded **only when the approval push was actually enqueued for at least one device** (`sender.Enqueue`'s bool, `sender.go:96`); a gated-out approval (presence, zero devices, queue full) records nothing (`openAsks` serves only rule 8).
- **Restart snapshot** (`Start` → `m.asks.Load(open)`, `module.go:231`): asks that were open when the daemon started carry no proof they were pushed, so they are **not** loaded as dedupe intervals any more. Accepted cost: a `waiting` event for an AskUserQuestion pushed before a restart may be pushed once more (a duplicate beats losing the only notice). The test that expects snapshot suppression (`agent_trigger_test.go:257`) changes accordingly.
- Tests: approval while at computer → user leaves → `waiting` for the same session is pushed; approval pushed → `waiting` deduplicated as today; enqueue failure → `waiting` pushed; an ask open at daemon start → its `waiting` is pushed.

## 5. Mac App

### 5.1 Owner: one reporter per Mac, in the Electron main process

- On macOS the main process outlives every window (`electron/main.ts:405`); a renderer-hosted reporter dies with its window (`electron/window-manager.ts:69`). So the **v2 reporter lives in the main process**, one per App, independent of windows (no throttling, no per-window races).
- **Host list**: the renderer hands the main process the **persisted host configuration** only — `{rev, hosts: [{hostId, baseUrl, token}]}` — never per-window connection state (that differs between windows, `useHostStore.ts:782`). `rev` is written as `max(rev + 1, now)` (ms) on every host-store write, so two windows cannot produce the same number; the main process keeps the copy with the highest `rev` and ignores older ones, so a window that is reconnecting or stale cannot overwrite a newer list; the copy survives closed windows (on App restart a window opens first and hands it over).
- **Capabilities**: the main process asks each host itself — `GET /api/info` with the token — at start, when the list changes, after a failed send, and every 10 min; it reports v2 only to hosts whose `/api/info` lists `push.presence.v2`.
- **IPC hygiene**: the handler validates the payload shape, `http(s)` URL scheme, at most 64 hosts and a size cap; tokens are never logged or sent back to a renderer.
- `client_id` for v2 = `mac:<install id>` (one per App install, not per window).
- The per-window v1 reporter (`spa/src/lib/presence/reporter.ts`) keeps reporting v1 to every host **until the main process says it has taken that host over**: the host lists `push.presence.v2` **and** the main process's last v2 send to it succeeded within the last 45 s. When a main-process send fails, the host leaves the taken-over set and v1 resumes at once — so there is no gap while v2 takes over or falls back (PA-2 plan rev 2, `docs/plans/2026-10-11-push-presence-pa2-plan.md`).

### 5.2 Sampling

- `powerMonitor.getSystemIdleTime()` (whole seconds) sampled every 5 s; `powerMonitor` `lock-screen`, `unlock-screen`, `suspend`, `resume` events; on start and on `resume`, reconcile with `powerMonitor.getSystemIdleState(threshold)` (returns `locked` when locked). Lock start is kept on a monotonic clock (`performance.now()`/`process.hrtime`), so `locked_ms` does not jump with wall-clock changes.
- **Lock age unknown** (the App starts, or the Mac resumes, already locked — Electron gives no lock start time): report `locked_ms: 60000`, i.e. away at once. The idle time is not used as a lock age (a long idle followed by a fresh lock would make it wrong the other way).
- Subscribe to the events first, then read the current state, and keep a sample revision so an older read never overwrites a newer event.

### 5.3 Sending

- To every connected host announcing `push.presence.v2`, a v2 body with `ttl_ms: 45000`:
  - every **20 s**, from the first moment (not gated on focus or on the legacy `active`);
  - **forced** immediately on: the user came back (sampled idle dropped by ≥ 5 s), lock, unlock, resume, a change of N, a host list change, capability first seen for a host, and the first success after a failed send to that host.
  - After a daemon restart the new daemon has no entry until the next send: **up to 20 s** of possible false push (accepted; the daemon keeps presence in memory only).
- The body is built from a **fresh sample** at send time (or the latest sample plus its age), so `idle_ms` / `locked_ms` are current to the second.
- Per host, sends are serialised as today; a forced reason that arrives while one is in flight is coalesced into one send of the latest state when it settles. **Every request has a 10 s deadline** (as the renderer reporter today, `reporter.ts:24`): on timeout the in-flight mark is cleared and the latest state is sent again, so one stuck connection cannot silence a host.
- A host that answers 4xx to v2 (an old daemon that lists the capability by mistake should not exist; still) is reported at most once per minute in the log and falls back to nothing (v1 is the renderer's job).

### 5.4 Compatibility

- **New SPA + old Electron shell** (dev-server SPA newer than the preload) or a plain browser: the bridge methods are optional and feature-detected; without them the main process cannot report, so the renderer keeps sending **v1** to every host (today's behaviour). v2 is sent only when the bridge exists **and** the host announces `push.presence.v2`, and v1 stops for a host only once the main process reports it has taken that host over (§5.1).
- **v2 App + old daemon** (no capability): v1 only, as today.
- **v1 App + new daemon**: legacy rule (§4.2/§4.3), as today.

### 5.5 Settings

- Settings → Desktop App (the `ElectronSection`, shown only when the bridge exists; the repo has no separate Notifications page): 「離開電腦幾分鐘後推到手機」 1 / 2 / 3 / 5 / 10 / 15 / 30 / 60 min, default 5, with the line 「在電腦前（這台 Mac 有鍵盤或滑鼠動作、沒鎖螢幕）時不推到手機；鎖螢幕 1 分鐘後也會推。」 **Stored by the main process** in its own app prefs (under `userData`), read and written through the bridge — not in the renderer's notification store (that one is synced across windows and projected by Profile Sync, `useNotificationSettingsStore.ts:52`, `projections.ts:67`), so N stays per Mac. A change forces a send (§5.3).

## 6. Phases

- **PA-1 daemon (A line)** — §3, §4. Tests listed in §4 plus: v2 idle `N−1s` → no push, `N` → push; extrapolation across the gap; a newer report with smaller idle wins; locked 59 s / 61 s; expired v2 entry → push; two entries, one at computer → no push; v2 entry never matches legacy `ShowsCode`; final presence check drops an event whose user came back during the hold (rule 7 stays at arrival, so its debounce window still opens — asserted); a suppressed proxy StopFailure still feeds the debounce (existing test kept); capability only when push is ready; validation table (§3.1). Mutation evidence for the idle and lock comparisons and for the final check.
- **PA-2a Electron shell (interface line)** — §5.1 main-process reporter (host list by `rev`, own `/api/info` capability check, IPC validation), §5.2 sampling, §5.3 sending (10 s deadline), N in main app prefs, preload bridge methods (optional in types). Tests: reporter keeps sending with no window; two windows handing different `rev`s → the higher wins, a stale one is ignored; already-locked start → `locked_ms` 60000; event-during-read race (revision); resume-still-locked; forced sends and coalescing; a request that never settles → timeout and resend; first success after failure → forced send. Needs `pnpm run electron:build` + dev update to reach the App. Its own plan goes through codex before code.
- **PA-2b SPA (interface line)** — handing hosts / N to the bridge, v1 reporter skipping v2 hosts only when the bridge exists, Settings item. Tests: old shell (no bridge) keeps v1 to all hosts; mixed v1/v2 hosts; plain browser.
- **Order**: PA-1 merged **and deployed to both hosts** → PA-2a (shell) → PA-2b (SPA; it goes live through the dev server on merge, so the shell must already carry the bridge, or PA-2b's feature detection keeps v1).

## 7. Out of scope

- Clearing a phone notification when the session is seen on the Mac (sync dismissal).
- A shorter threshold for `waiting`; replaying events that happened while at the computer.
- iOS changes (the push-tap destination is task c9d9c7-75).

## 8. Acceptance (real machine, after PA-2b)

Boundary probes — an event is triggered on each side of the boundary; nothing is replayed:
- Typing in Ghostty with Purdex in the background, trigger a Stop → no phone push.
- Lock the Mac; trigger at ~30 s → no push; trigger again at ~70 s → push.
- Leave the Mac idle; trigger at N−1 min → no push; at N+1 min → push.
- Close every Purdex window (App still running), stay at the Mac, trigger → no push.
- Quit the App, wait 60 s, trigger → push.

## 9. Review map (codex `task-mv3nefgr-rbvam2`)

1 no window → main-process owner §5.1 · 2 new SPA/old shell → optional bridge §5.4, PA-2a/2b split · 3 heartbeat/initial send → main-process 20 s from start §5.3 · 4 lock init/races/per-window → reconcile, monotonic, revision, one reporter §5.2 · 5 forced sends/dedup/sample age → §5.3 · 6 optional field decoding, error contract → §3.1 · 7 approval without tmux → §4.3 · 8 rule 8 eats the only notice → §4.4 · 9 delayed send → final check, rule 7 after it §4.2 · 10 restart/reconnect → forced send on boot_id/connect §5.3, accepted window §4.1 · 11 host setting/UI did not fit → threshold in the report, device-local setting §3.1/§5.5 · 12 acceptance wording → boundary probes §8.

rev 3 (incremental `task-mv3nyz3w-cw3kv0`): 4 already-locked start → `locked_ms` 60000 §5.2 · 6 `v:null` → v1 §3.1 · 8 restart snapshot not loaded as dedupe, `openAsks` only serves rule 8 §4.4 · 10 no `boot_id` from the renderer; main detects recovery, ≤ 20 s window accepted §5.3 · 11 N in main app prefs, not the synced/projected store §5.5 · new-1 multi-window race → persisted host list by `rev`, main checks capabilities itself §5.1 · new-2 rule 7 stays at arrival §4.2 · new-3 10 s request deadline §5.3.

rev 3.1 (PA-2 plan rev 2, #2671, codex-reviewed by the interface line): v1 yields per host only after the main process reports it has taken the host over (capability + a v2 send succeeded within 45 s), and resumes at once when a v2 send fails §5.1/§5.4 · host-list `rev` = `max(rev+1, now)` §5.1 · the setting lives in Settings → Desktop App §5.5.
