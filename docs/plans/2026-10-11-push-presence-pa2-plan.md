# PA-2 — the Mac App reports "at the computer" (main-process presence reporter + SPA) — plan (rev 2, after the plan review)

Spec: `docs/specs/2026-10-11-push-away-presence-spec.md` (rev 3; §2 behaviour, §3.1 wire, §5 Mac App, §6 phases, §8 acceptance).
Daemon side is PA-1 (A line). PRs here: **PA-2a-1** (sampler, prefs, type-check scope), **PA-2a-2** (reporter, IPC, wiring, bridge)
and **PA-2b** (SPA), each ≤ 800 changed lines / 20 files.
Order (spec §6): PA-1 merged and deployed on mlab and air26 → PA-2a-2 merged + `pnpm run electron:build` + dev update → PA-2b.
PA-2a-1 has no runtime effect (nothing imports it from `main.ts`) and may merge before PA-1 is deployed.

## Facts (code at dc44e4d52)
- Electron: `electron/main.ts` registers IPC inline (`registerIpcHandlers`, ~L121-302) plus modules (`registerTrayIpc`, `tray-ipc.ts:9`);
  `ipcMain.handle` with positional args; validation is per handler (`fs-ipc.ts` checks absolute paths and the home boundary,
  `browser-view-ipc.ts:38,87` bounds and URL schemes), no shared schema library. The process outlives windows on macOS
  (`main.ts:405-408`); app ready ~L360-395 (prefs path `:364`, IPC `:366-367`); `before-quit` ~L413.
- Preload: `contextBridge.exposeInMainWorld('electronAPI', …)` (`electron/preload.ts`), grouped sub-objects (`tray: {…}` L131-140);
  SPA type `window.electronAPI?` in `spa/src/types/electron.d.ts:92`, newer methods optional, feature-detected with optional chaining
  (`ElectronSection.tsx:14,31`). Main → every window broadcast pattern: `tray-ipc.ts:25-27`.
- Main-process HTTP: global `fetch` with `Authorization: Bearer` (`electron/updater.ts:38-43`); tokens are never persisted in main today.
- Main prefs: `electron/app-prefs.ts` — `userData/app-prefs.json`, `AppPrefs { showTray }`, lenient `parseAppPrefs`, sync load/save;
  tests with a temp dir (`app-prefs.test.ts`). No install id anywhere in main.
- No `powerMonitor` use yet. Electron tests (`electron/vitest.config.ts`) never import `electron`: pure modules with injected deps and
  fake timers (`tray-visibility.test.ts`, `notification-router.test.ts`, `local-daemon/index.test.ts`).
- `electron/tsconfig.json:14` includes only top-level `*.ts`: a new `electron/presence/` directory is not type-checked unless added. There
  is no root `tsconfig.json`.
- v1 reporter `spa/src/lib/presence/reporter.ts`: per window (`usePushPresence`, mounted in `App.tsx`), body `{client_id, active,
  sessions, ttl_ms}`, 300 ms debounce, 20 s heartbeat while active, 10 s request deadline, `connectedHosts()`, per-host `push.v1` probe
  via `fetchHostInfo` (retry 20 s), identity check on re-point (L100-116), one in flight per host, 4xx → `rejected` until the body
  changes; `client_id = <getClientId()>:<window id>`; ~20 tests in `reporter.test.ts`.
- Host store `spa/src/stores/useHostStore.ts`: persisted `hosts`, `hostOrder`, `activeHostId`, `devHostId` (L768-788), synced across
  windows by `syncManager` (BroadcastChannel + storage event, `lib/storage/sync.ts`; a broadcast can arrive before the write is visible);
  `HostConfig` has `ip`, `port`, `token?` — no `baseUrl` (always `http://${ip}:${port}`, `getDaemonBase` L738-745) and **no update
  counter**. Mutations that change what the reporter needs (membership, id, ip, port, token): `addHost`, `removeHost`, `updateHost`
  (L612), `applyHostTransfer` (L630, may create a host or overwrite ip / port / token), `registerLocalHost` (L681, delegates to add /
  update); `reorderHosts` changes order only.
- Settings: there is **no Settings → Notifications section** (spec §5.5's wording); registered sections are appearance / terminal /
  interface / profile / module-config / electron ("Desktop App", `ElectronSection.tsx`, already bridge-gated) / dev-environment
  (`register-modules/index.tsx:294-355`).
- Build: anything imported from `electron/main.ts` is bundled (no config change); a change under `electron/` changes `electronHash` →
  needs `pnpm run electron:build` on mlab, then the dev update on the Air (CLAUDE.md "Dev Update").

## Decisions
- **D1 Modules (pure, injected deps).** `electron/presence/sampler.ts` (idle + lock state), `electron/presence/reporter.ts` (hosts,
  capabilities, send loop, owned set), `electron/presence/ipc.ts` (validation + handlers), wired in `main.ts`. Deps injected:
  `powerMonitor` subset (`getSystemIdleTime`, `getSystemIdleState`, `on`), `fetch`, monotonic `now()`, timers, prefs load/save, logger,
  broadcast. Tests never import `electron`.
- **D2 Sampler (spec §5.2).** Subscribe to `lock-screen`, `unlock-screen`, `suspend`, `resume` first, then read the current state; every
  event and read bumps a sample revision and a read never overwrites a newer revision. `getSystemIdleTime()` every 5 s. Lock start on the
  monotonic clock; on start and on `resume` reconcile with `getSystemIdleState(60)` — `locked` with no known lock start → lock age
  unknown → `locked_ms: 60000`. `snapshot(at)` returns `{idleMs, locked, lockedMs}` aged to `at` (idle seconds × 1000 + time since that
  sample). "User came back" = sampled idle dropped by ≥ 5 s → emits `back`.
- **D3 Host list (spec §5.1).** IPC `presence:set-hosts` with `{rev: number, hosts: [{hostId, baseUrl, token}]}`; main keeps the copy with
  the highest `rev`; an **equal** `rev` replaces the held copy (latest arrival) — `rev` is a write time (D8), so equal values come from
  the same persisted write. Memory only: tokens are never written to disk, never logged, never put into an error message, never sent to a
  renderer. Validation: plain object; `rev` a safe non-negative integer; ≤ 64 hosts; `hostId` non-empty string ≤ 128, **unique**;
  `baseUrl` parses with `http:` / `https:`, no username / password, **path empty or `/`, no query, no hash**, canonicalised to its origin;
  `token` null or string ≤ 4096; payload ≤ 256 KiB serialised; anything else → rejected (the handler returns `false` and logs the reason
  without any value).
- **D3b Per-host identity.** Each held host carries an identity `(origin, token)`; a probe or send records the identity it started with,
  and its completion is applied only if the host still exists with the same identity: a removed host is never re-sent, and a changed
  endpoint / token discards stale probe answers and in-flight results (as the v1 reporter's identity check, `reporter.ts:100-116`).
- **D4 Capabilities.** Main fetches `GET {origin}/api/info` (Bearer token, 5 s deadline) at start, for new / changed hosts when the list
  changes, after a failed send to that host, and every 10 min; a host is reported v2 only while its last `capabilities` array contains
  `push.presence.v2`. A failed probe retries after 20 s.
- **D5 Sending (spec §5.3).** Per v2 host: a send every 20 s from the first moment; forced on `back`, lock, unlock, resume, N change,
  host-list change, capability first seen, first success after a failure. Body `{client_id: "mac:<installId>", active, sessions: [],
  ttl_ms: 45000, v: 2, idle_ms, locked, locked_ms, away_after_ms}` built from `snapshot(now)` at send time; `active` = at the computer by
  this Mac's own rule (not locked and `idle_ms < away_after_ms`, or locked under 60 s) — informational, the daemon ignores v1 fields of a
  v2 entry (spec §4.1). One request in flight per host; reasons arriving meanwhile coalesce into one send of the latest state when it
  settles; every request has a 10 s deadline (abort → in-flight cleared → resend latest). 2xx → ok; 4xx → logged at most once a minute
  per host, retried only on the next tick; 5xx / network → failed (capability re-probe + forced send on the next success).
- **D6 Prefs.** `AppPrefs` gains `presenceAwayMinutes` (one of 1/2/3/5/10/15/30/60, default 5; anything else → default) and `installId`
  (generated once with `crypto.randomUUID()` on first need and saved; an invalid stored value is replaced). IPC
  `presence:get-away-minutes` → number; `presence:set-away-minutes` validates the value is in the set, saves, forces a send, returns the
  saved value.
- **D7 Bridge.** Preload `presence: { setHosts(payload), getAwayMinutes(), setAwayMinutes(n), getOwned(), onOwnedChanged(cb) }`; SPA
  type `presence?: {…}` with every method optional. PA-2a-2 ships the bridge; nothing in the SPA calls it until PA-2b.
- **D8 Host rev (PA-2b).** `useHostStore` gains a persisted `hostsRev`: on every action that changes the reporter projection (the set of
  hosts and each host's id / ip / port / token) it becomes `max(hostsRev + 1, Date.now())` — a write time on this Mac's one clock, so the
  highest `rev` is the latest write, which is also what the shared storage keeps (last writer wins); two windows taking "the same next
  step" can no longer produce an equal number for different content (they would need the same millisecond, and then main takes the later
  arrival). One helper used by `addHost`, `removeHost`, `updateHost`, `applyHostTransfer`, `registerLocalHost`; `reorderHosts` and any
  action that leaves the projection unchanged do not bump it; a rehydrate never bumps it. Part of `partialize`; alpha — no migration
  (absent → 0).
- **D9 Hand-over (PA-2b).** One hook mounted in `App.tsx` (per window): when `window.electronAPI?.presence?.setHosts` exists, send `{rev:
  hostsRev, hosts: [{hostId, baseUrl: getDaemonBase(host), token}]}` on mount, whenever `hostsRev` changes, and after a rehydrate from
  another window. Every window sends; main keeps the highest `rev` (D3), so a stale window cannot win and all windows converge on the
  persisted list.
- **D10 Ownership, acknowledged by main.** Main keeps an **owned** set: a host is owned while its capability includes `push.presence.v2`
  **and** its latest v2 send succeeded less than 45 s ago (one TTL); it leaves the set on a failed send, a 4xx, a lost capability,
  removal or an identity change. Bridge `presence.getOwned(): string[]` (host ids) and `presence.onOwnedChanged(cb)` (broadcast to every
  window, ids only). **v1 yields (PA-2b):** a window's v1 reporter stops reporting to a host only while main lists it as owned, after one
  final v1 `active:false` for it (so a lingering v1 entry cannot feed the legacy rule); when the host leaves the owned set, v1 resumes at
  once. Without the bridge (old shell, plain browser), or for hosts main does not own, behaviour is unchanged (spec §5.4). No presence
  gap: v1 keeps reporting until main has proven a v2 send.
- **D11 Setting (PA-2b).** One row in Settings → **Desktop App** (`ElectronSection`; there is no Notifications section, and this section
  is already the bridge-gated place): 「離開電腦幾分鐘後推到手機」 select 1 / 2 / 3 / 5 / 10 / 15 / 30 / 60, description 「在電腦前（這台 Mac
  有鍵盤或滑鼠動作、沒鎖螢幕）時不推到手機；鎖螢幕 1 分鐘後也會推。」 (zh-TW + en). Read through `getAwayMinutes`, written through
  `setAwayMinutes`; hidden when `presence.getAwayMinutes` is absent. Not in the notification store, not in Profile Sync (spec §5.5).
- **D12 Type-check scope.** `electron/tsconfig.json` includes `presence/**/*.ts`; the electron PRs' gates run
  `pnpm exec tsc -p electron/tsconfig.json --noEmit` and `npx electron-vite build` (bundles main / preload into the worktree's `out/`, no
  packaging) besides the electron vitest suite.

## PA-2a-1 — sampler, prefs, type-check scope (D2, D6 prefs, D12), ~450
1. `electron/tsconfig.json` include + the extra gates (D12).
2. `app-prefs.ts`: `presenceAwayMinutes`, `installId` (+ tests: defaults, invalid values, lazy id persisted once, no token-like field is
   ever serialised).
3. `presence/sampler.ts` + tests: 5 s idle sampling; lock / unlock / suspend / resume; already-locked start → `locked_ms` 60000;
   resume-still-locked; event-during-read race (an older read never overwrites); monotonic lock age (a wall-clock jump does not move it);
   `back` on idle drop ≥ 5 s; `snapshot` aging.

## PA-2a-2 — reporter, IPC, wiring, bridge (D1, D3–D5, D6 IPC, D7, D10 main side), ~700
1. `presence/reporter.ts` + tests: keeps sending with no window; highest `rev` wins, lower ignored, equal replaces; identity guard (a stale
   probe answer after a token / endpoint change is discarded; an in-flight completion for a removed host is ignored and never resent);
   capability gating, probe retry, re-probe after failure, 10 min refresh; 20 s cadence from the first moment; each forced reason;
   coalescing while in flight; a request that never settles → 10 s timeout → resend latest; first success after failure → forced send; 4xx
   log throttle; owned set enters / leaves per D10 and broadcasts changes; body fields exact (`v: 2`, ages current at send time,
   `client_id` `mac:<installId>`); tokens never appear in log lines or error messages (assert on captured logger output for every failure
   path).
2. `presence/ipc.ts` + tests: the validation table (shape, scheme, userinfo, path / query / hash, canonical origin, duplicate `hostId`,
   sizes, counts, `rev` types); set-away-minutes rejects values outside the set; logged reasons carry no values.
3. `main.ts` wiring (start at ready with real `powerMonitor` / `fetch`, stop on `before-quit`), preload `presence` group, SPA type
   (optional methods).
Merge gate: PA-1 deployed on both hosts. After merge: `pnpm run electron:build` on mlab, then the dev update on the Air (coordinated with
1f, who owns deploys).

## PA-2b — SPA (D8–D11), ~450
1. `hostsRev` in `useHostStore` (+ tests: each of `addHost`, `removeHost`, `updateHost`, `applyHostTransfer` (create and overwrite),
   `registerLocalHost` bumps it; `reorderHosts` and no-op updates do not; the value is `max(prev + 1, now)`; a rehydrate does not bump;
   persisted).
2. Hand-over hook (+ tests: no bridge → no call; bridge → call on mount, on rev change and after a rehydrate, with `http://ip:port` base and
   token; unchanged rev → no call).
3. v1 reporter yields to main's owned set (+ tests: old shell / plain browser → v1 to every push host as today; bridge but host not owned
   → v1 continues; owned → one final `active:false`, then nothing; owned → not owned (main's send failed) → v1 resumes at once; ownership
   events for unknown hosts ignored).
4. Settings row (+ tests: hidden without the bridge; shows the stored value; change calls `setAwayMinutes` and shows the saved value;
   zh-TW / en strings).
Merge gate: PA-2a-2's shell is on the Air (dev update done) — otherwise feature detection keeps v1, which is safe but untested; full
vitest + `tsc -b` (spa).

## Reviews and gates
- Each PR: codex R1 + attack in parallel, then critic (gpt-5.6-sol, effort low); the electron PRs run the electron vitest suite,
  `tsc -p electron/tsconfig.json --noEmit` and `electron-vite build`; PA-2b the full SPA vitest (--maxWorkers=2) + `tsc -b` (spa).
- Acceptance after PA-2b (spec §8, real machine): typing in another app with Purdex in the background → no phone push; lock → 30 s no
  push, 70 s push; idle N−1 / N+1 min; every Purdex window closed (App running), at the Mac → no push; App quit, 60 s → push.
