# Spec — Explicit dev host & local-daemon token visibility

**Status:** v1 — draft for codex quick review.
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
| D3 | `devHostId` is persisted in `useHostStore` (localStorage via `purdexStorage`), **not** included in the hosts sync contributor. | Device-local preference, like `token`. Sync-applied host maps keep ids stable, so the id survives; a dangling id is simply treated as unset (see §2.1). |
| D4 | Local-daemon token is sent from Electron main to the renderer over the existing `localDaemonStatus` IPC. | It is the machine's own `config.toml`; the renderer already receives the token in `localDaemonInstall` / `Start` / `Restart` results. No new trust boundary. |
| D5 | Auto-`registerLocalHost` after Install / Start / Restart is unchanged. | User confirmed it is fine; the gap is the *external* / pre-existing case. |

## 2. Change 1 — Explicit dev host

### 2.1 Store (`spa/src/stores/useHostStore.ts`)

- New state `devHostId: string | null` (default `null`), added to `partialize`.
- New action `setDevHost(hostId: string | null)`. Setting an id that is not in `hosts` is a no-op (state unchanged), mirroring `setActiveHost`.
- New selector helper `getDevHostId(): string | null` — returns `devHostId` only if `hosts[devHostId]` exists, else `null`. Consumers use this, never the raw field, so a host deleted locally or by sync reads as "unset".
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
- When `devHostId` changes from A → B: the existing effect keyed on `[appInfo, daemonBase, token]` re-runs the check and `closeStream()` drops the stale stream, as today.

### 2.3 i18n

`en.json` / `zh-TW.json`: `settings.dev.host.label`, `settings.dev.host.none`, `settings.dev.host.required`. `locale-completeness.test.ts` enforces parity.

## 3. Change 2 — Local daemon URL / token / Add to hosts

### 3.1 Electron (`electron/local-daemon/index.ts`, `types.ts`)

- `LocalDaemonStatus.config` changes from `{ bind, port, hasToken }` to `{ bind, port, token: string | null }`.
- `statusUnlocked()` fills `token: cfgFile.token` (already parsed by `parseDaemonConfig`).
- No new IPC channel.

### 3.2 Types (`spa/src/types/electron.d.ts`)

- `ElectronLocalDaemonStatus.config` mirrors §3.1. `hasToken` is removed (grep confirms it has no other consumer; if one appears during implementation, keep both).

### 3.3 UI (`spa/src/components/settings/LocalDaemonSection.tsx`)

Props change: `daemonBase: string | null`.

Rendering, whenever `status.config` is non-null (managed **or** external, running or not):

- Row `settings.dev.local.url` → `http://{bind}:{port}` (monospace).
- Row `settings.dev.local.token` →
  - `token === null` → `settings.dev.local.token_missing`.
  - else masked (`••••••••` fixed width, never a prefix of the real value), an eye toggle (`Eye` / `EyeSlash` from Phosphor) that reveals the full value, and a copy button (`Copy` icon) using `navigator.clipboard.writeText`; after copy show `settings.dev.local.copied` for ~1.5 s.
- Host-list membership, computed from `useHostStore((s) => s.hosts)` by matching `ip === bind && port === port`:
  - found → text `settings.dev.local.in_hosts` with the host name.
  - not found → button `settings.dev.local.btn.add_host` → `registerLocalHost({ url, token: token ?? '', hostname })`. `hostname` comes from `status.hostname` — **new optional field on `LocalDaemonStatus`**, filled from `os.hostname()` via the existing `deps` (the `node-deps` adapter already imports `hostname`). If `token === null` the button is disabled (a host without a token cannot authenticate).
- Install / Update buttons: `disabled` when `daemonBase === null` (in addition to `busy`), with title `settings.dev.host.required`. Start / Restart / Refresh unaffected.

### 3.4 i18n

`settings.dev.local.url`, `.token`, `.token_missing`, `.copied`, `.in_hosts` (`{{name}}`), `.btn.add_host`, `.btn.reveal`, `.btn.hide`, `.btn.copy`.

## 4. Testing

| Area | Tests |
|---|---|
| Store | `setDevHost` valid / unknown id; `getDevHostId` returns null when the host is gone; `removeHost` clears it; persisted via `partialize`. |
| DevEnvironmentSection | with `devHostId === null`: notice shown, no fetch / `streamCheck` called, buttons disabled; picking a host triggers the check against that host's base + token; switching hosts re-runs the check. |
| Electron `index.test.ts` | `status().config.token` equals the config file's token; `hostname` present. |
| LocalDaemonSection | URL + masked token rendered; reveal shows the full token; copy writes to clipboard; "Add to hosts" shown only when `bind:port` is absent and calls `registerLocalHost`; "in hosts: name" otherwise; Install/Update disabled when `daemonBase === null`. |
| Locales | existing completeness test. |

## 5. Out of scope

- Per-host "dev host" flag on the Hosts page.
- Syncing `devHostId` across devices.
- Any change to daemon-side dev endpoints.
- Rotating / editing the local daemon token from the UI.
