# Spec — Explicit dev host & local-daemon token visibility

**Status:** v2 — after codex quick review (`task-mu0x1w36-ttmyue`: 3 Important, 3 Minor; all adopted, see §6).
**Follows:** `docs/specs/2026-09-14-local-daemon-install-spec.md` (Local daemon install, Plan B). This spec changes that spec's D2 ("binary source is `hostOrder[0]`") on the user's request; everything else in that spec stands.

## 0. Summary

Two independent changes to Settings → Development, shipped in one PR as two commits.

1. **Explicit dev host.** Today every dev-page request (app update check /
   download, daemon rebuild check, local-daemon binary source) goes to
   `hostOrder[0]`. On a second machine that first host may be the machine's
   own local daemon, which has no repo and cannot serve updates. The page
   grows a host picker; the choice is persisted as `devHostId`. **There is no
   fallback**: until the user picks a host the page performs no dev requests
   and says so.

2. **Local daemon shows its own URL and token, decoupled from the host list.**
   The *Local daemon* block reads `~/.config/pdx/config.toml` already
   (`status().config`) but only exposes `hasToken`. It now exposes the token
   itself, renders URL + token (masked, reveal, copy), and offers **Add to
   hosts** when that `bind:port` is not yet in the host list. Auto-register
   after Install / Start / Restart stays as is. This covers the case where
   the daemon on this machine is *external* (started outside the app, e.g.
   a hand-installed daemon) and therefore never went through
   `registerLocalHost`.

## 1. Decisions (settled with the user)

| # | Decision | Why |
|---|---|---|
| D1 | Dev host is picked from a dropdown at the top of the Development page (not a per-host flag on the Hosts page). | Self-contained: the only consumer is this page. |
| D2 | No implicit dev host. `devHostId === null` → no fallback to `hostOrder[0]` / `activeHostId`. | User's call: "一定要手動給". Avoids silently pointing at a daemon that cannot serve updates. |
| D3 | `devHostId` is persisted in `useHostStore` (localStorage via `purdexStorage`) and shared across windows on the same device (storage rehydrate), but **not** included in the cross-device hosts sync contributor. | Device-local preference, like `token`. Host ids are generated locally (`generateId()`), so the same daemon may carry different ids on different devices and a sync full-replace may drop the id this device chose. The only guarantee is: **if `hosts[devHostId]` exists it is used, otherwise the page reads as unset and the user re-picks** (§2.1). If the id survives but its endpoint changed, the page follows the new endpoint — it reads `hosts[devHostId]` on every render. |
| D4 | Local-daemon token is sent from Electron main to the renderer over the existing `localDaemonStatus` IPC. | It is the machine's own `config.toml`; the renderer already receives the token in `localDaemonInstall` / `Start` / `Restart` results. No new trust boundary. |
| D5 | Auto-`registerLocalHost` after Install / Start / Restart is unchanged. | User confirmed it is fine; the gap is the *external* / pre-existing case. |

## 2. Change 1 — Explicit dev host

### 2.1 Store (`spa/src/stores/useHostStore.ts`)

- New state `devHostId: string | null` (default `null`), added to `partialize`.
- New action `setDevHost(hostId: string | null)`. Setting an id that is not in `hosts` is a no-op (state unchanged), mirroring `setActiveHost`.
- New **pure selector** exported from the store module: `selectDevHostId(state): string | null` — returns `state.devHostId` only if `state.hosts[state.devHostId]` exists, else `null`. Consumers call `useHostStore(selectDevHostId)` (primitive return, so no unstable-snapshot loop) and never read the raw field, so a host deleted locally or dropped by a sync full-replace reads as "unset". Not a store action / getter: a getter subscribed through `(s) => s.getDevHostId()` would read a second copy of state inside the closure and is harder to unit-test.
- `removeHost(hostId)`: if `state.devHostId === hostId` → `devHostId: null`.
- `reset()` keeps `devHostId: null` (part of `createDefaultState`).
- Persist `version` stays `1`; a stored blob without the field hydrates as `null` (alpha: no migration, per project convention).

### 2.2 Development page (`spa/src/components/settings/DevEnvironmentSection.tsx`)

- Replace `firstHostId = hostOrder[0]` with `devHostId = getDevHostId()`.
- `daemonBase` becomes `devHostId ? getDaemonBase(devHostId) : null`; `token` likewise from `hosts[devHostId]?.token`. Note `getDaemonBase` itself falls back to active/first host for unknown ids — the page must never call it with `null`.
- New first block under the title: label `settings.dev.host.label` + `<select>` listing `hostOrder` (option text `name (ip:port)`), plus a leading "— not set —" option (`settings.dev.host.none`). `onChange` → `setDevHost(value || null)`.
- When `devHostId === null`:
  - Render a notice `settings.dev.host.required` under the picker.
  - Do not run the mount-time app update check nor `checkDaemon()` (the effects early-return on `daemonBase === null`).
  - Disable: Check for updates, Update, daemon Check, daemon Rebuild.
  - `LocalDaemonSection` still renders; it receives `daemonBase: null` and disables only its Install / Update buttons (§3.3).
- **Source change discipline** (A → B, A → unset, token edit). Today only `checkUpdate()` closes the stream and the unmount cleanup is the only other close; `checkDaemon`'s fetch and the 3 s post-rebuild `setTimeout(checkDaemon)` have no cancellation, so a late response from A would overwrite B's view. Required behaviour:
  1. One effect keyed on `[daemonBase, token]` runs **first**: `closeStream()`, then reset `remoteInfo`, `status` (→ `idle`), `updateError`, `buildEvents`, `daemonCheck` (→ `null`, which also nulls the `latestHash` passed to `LocalDaemonSection`), `daemonLog`, `daemonError`, `daemonPhase` (→ `idle`).
  2. A `sourceGen` ref increments in that same effect. `checkDaemon`, the rebuild reader loop, the post-rebuild timer and the `streamCheck` callback each capture the generation when they start and **discard** any result whose generation no longer matches; the timer id is kept in a ref and cleared on source change.
  3. Only after the reset, and only when `daemonBase !== null`, the check effects fire.
  4. The picker is `disabled` while an app update (`updating`) or a daemon rebuild (`daemonPhase === 'rebuilding' | 'restarting'`) is in flight — those operations cannot be cancelled and their target must not become ambiguous mid-way. A local-daemon install captures `daemonBase` at click time inside `LocalDaemonSection`, so it needs no lock.

### 2.3 i18n

`en.json` / `zh-TW.json`: `settings.dev.host.label`, `settings.dev.host.none`, `settings.dev.host.required`. `locale-completeness.test.ts` enforces parity.

## 3. Change 2 — Local daemon URL / token / Add to hosts

### 3.1 Electron (`electron/local-daemon/index.ts`, `types.ts`)

- `LocalDaemonStatus.config` changes from `{ bind, port, hasToken }` to `{ bind, port, token: string | null }`.
- `statusUnlocked()` fills `token: cfgFile.token` (already parsed by `parseDaemonConfig`).
- `LocalDaemonStatus` gains **required** `hostname: string`, filled from `deps.hostname()` in the shared base object at the top of `statusUnlocked()` (all three return branches carry it). Required, not optional, because `registerLocalHost` demands `hostname: string`. Test fixtures in `index.test.ts` gain the field.
- No new IPC channel.

### 3.2 Types (`spa/src/types/electron.d.ts`)

- `ElectronLocalDaemonStatus.config` mirrors §3.1 and gains `hostname: string`. `hasToken` is removed (grep confirms it has no other consumer; if one appears during implementation, keep both).

### 3.3 UI (`spa/src/components/settings/LocalDaemonSection.tsx`)

Props change: `daemonBase: string | null`.

Rendering, whenever `status.config` is non-null (managed **or** external, running or not):

- Row `settings.dev.local.url` → `http://{bind}:{port}` (monospace).
- Row `settings.dev.local.token` →
  - `token === null` → `settings.dev.local.token_missing`.
  - else masked (`••••••••` fixed width, never a prefix of the real value), an eye toggle (`Eye` / `EyeSlash` from Phosphor) that reveals the full value, and a copy button (`Copy` icon) using `navigator.clipboard.writeText`; after copy show `settings.dev.local.copied` for ~1.5 s.
- **Endpoint membership** — "is this exact `bind:port` already a host entry", nothing more. A shared pure helper `findHostByEndpoint(hosts, ip, port)` is added to `useHostStore.ts` and used by **both** `registerLocalHost` (replacing its inline `find`) and this block, so the UI can never disagree with what registration would do. It is a strict `ip === bind && port === port` match: `127.0.0.1` and the machine's Tailscale IP are two different endpoints and may legitimately each be registered; no hostname/token-based merging (that would need a verifiable daemon-identity contract, out of scope).
  - found → text `settings.dev.local.in_hosts` (`{{name}}`), worded as "registered as *name*".
  - not found → button `settings.dev.local.btn.add_host` → `registerLocalHost({ url: \`http://${bind}:${port}\`, token, hostname: status.hostname })`. If `token === null` the button is disabled (a host without a token cannot authenticate).
- Install / Update buttons: `disabled` when `daemonBase === null` (in addition to `busy`), with title `settings.dev.host.required`. Start / Restart / Refresh unaffected.

### 3.4 i18n

`settings.dev.local.url`, `.token`, `.token_missing`, `.copied`, `.in_hosts` (`{{name}}`), `.btn.add_host`, `.btn.reveal`, `.btn.hide`, `.btn.copy`.

## 4. Testing

| Area | Tests |
|---|---|
| Store | `setDevHost` valid / unknown id; `selectDevHostId` returns null when the host is gone; `removeHost` clears it; persisted via `partialize`; a sync full-replace that drops the id → `selectDevHostId` null; same id with a changed endpoint → page reads the new endpoint; `findHostByEndpoint` exact-match semantics (loopback vs Tailscale IP are distinct). |
| DevEnvironmentSection | with `devHostId === null`: notice shown, no fetch / `streamCheck` called, buttons disabled; picking a host triggers the check against that host's base + token; A → B and A → unset with a **deferred** A response (deferred promise / captured `streamCheck` callback / fake timers for the 3 s timer): A's late result is discarded and the view shows B's / the empty state; picker disabled during `updating` and `rebuilding`. |
| Electron `index.test.ts` | `status().config.token` equals the config file's token; `status().hostname` present on all three `managed` branches. |
| LocalDaemonSection | URL + masked token rendered; reveal shows the full token; copy writes to clipboard; "Add to hosts" shown only when the exact endpoint is absent and, when clicked, the host store actually gains an entry with that ip/port/token/hostname (assert store state, not just a spy); "registered as name" otherwise; button disabled when `token === null`; Install/Update disabled when `daemonBase === null`. |
| Locales | existing completeness test. |

## 5. Out of scope

- Per-host "dev host" flag on the Hosts page.
- Syncing `devHostId` across devices.
- Any change to daemon-side dev endpoints.
- Rotating / editing the local daemon token from the UI.

## 6. Codex quick review — dispositions (`task-mu0x1w36-ttmyue`)

| # | Sev | Finding | Disposition |
|---|---|---|---|
| 1 | Important | Early-return on `daemonBase === null` leaves A's stream / results on screen. | Adopted → §2.2 source-change discipline step 1. |
| 2 | Important | Clearing the view does not stop late responses (uncancelled fetch, reader loop, 3 s timer) from writing back. | Adopted → §2.2 steps 2–4 (generation ref, timer ref, picker lock during update/rebuild). |
| 3 | Important | D3 "ids are stable across devices" is not guaranteed; sync full-replace can drop the id. | Adopted → D3 narrowed to the `hosts[devHostId]`-exists guarantee; tests added in §4. |
| 4 | Minor | `bind:port` equality is "same endpoint", not "same daemon"; loopback vs Tailscale IP. | Adopted → §3.3 shared `findHostByEndpoint`, strict match, wording "registered as". |
| 5 | Minor | Prefer a pure `selectDevHostId(state)` over a store getter. | Adopted → §2.1. |
| 6 | Minor | `hostname` must be required (registerLocalHost needs `string`); share the endpoint helper; assert real store changes in tests. | Adopted → §3.1, §3.3, §4. |
