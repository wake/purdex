# Peer self alias — settable from the API, the CLI and the Peers page (#1196)

## 1. Goal

The name a daemon shows to its peers — the `calls itself "…"` reading on the counterpart's Peers page, the entry name a peer adopts on `POST /api/peers/hosts` without an alias, the prefix of every `<host>/<name>` address that names a session on this host — is `PeerAlias()` (`internal/config/config.go:154`): `[peers] alias` in the daemon's `config.toml`, falling back to `host_id` up to the first `:`. Today the only way to set it is to hand-edit the file and restart the daemon. This spec makes it settable in place, with the same validation an entry alias gets, from the three surfaces the rest of Phase D uses.

## 2. Facts (measured at `82823d33`, alpha.398)

- `PeerAlias()` is read live under `CfgMu.RLock` at every use (`module.go:181 configSnapshot`, `deliver.go:99 snapshotForDeliver`, `hosts.go:296/435/525`); nothing caches it, so a write through `Core.UpdateConfig` takes effect on the next request without a restart.
- `PUT /api/peers/settings` (`internal/module/peers/settings.go:41`) decodes `ipeers.PutSettingsRequest{Deliver *bool}` (`internal/peers/wire.go:424`), writes under `UpdateConfig`, and answers `SettingsResponse{deliver, alias}` with the effective alias. Admin-only (`HostRoutePolicy` + `requireAdmin`). Body bounded by `maxSettingsBodyBytes`.
- `config.ValidateAlias(alias, localAlias)` (`config.go:72`): rejects `.`/`..`, enforces `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`, and rejects a case-insensitive match with `localAlias`. It is what every entry alias — typed or learned — must clear.
- `FindPeerHostByAlias` is case-insensitive; so is the address routing that puts the local alias and the peer aliases in the same namespace (`mini-lab/<name>` vs `air26/<name>`): a self alias equal to a configured peer alias would make `<alias>/<name>` ambiguous on this host.
- The CLI has no settings subcommand at all (`cmd/pdx/peers.go` `peersUsage`); `deliver` is only settable through the API.
- The SPA reads `alias` through `fetchPeerSettings` (`host-api.ts`), shows it read-only on the `peers-self` line (`PeersSection.tsx`), and uses it as the entry name it creates on Y in `pairHosts` step 1.
- Live: mlab `alias = ""` → derived `mini-lab` from `mini-lab:278cbm`; air-2026 `alias = "air26"` (hand-edited; derived would be `wakes-air-2026`).

## 3. Decisions

**S-1. One rule for both kinds of alias.** A self alias must clear `aliasPattern` and the reserved names exactly as an entry alias does, plus: it must not case-insensitively equal the alias of any configured `[[peers.hosts]]` entry (the mirror image of `ValidateAlias`'s "not the local alias"). New `config.ValidateSelfAlias(alias string, hosts []PeerHost) error`. The check runs under the config write lock against the entries present at that moment.

**S-2. Empty means "back to the default", absent means "unchanged".** `PutSettingsRequest` gains `Alias *string`: `nil` leaves `Peers.Alias` as it is; `""` clears it (the effective alias becomes the `host_id`-derived one again); anything else is validated and stored verbatim (no trimming, no case folding — the daemon stores what the operator typed, as `hosts.go` does for entries).

**S-3. The response says where the alias comes from.** `SettingsResponse` gains `alias_source: "config" | "host_id"`. The page needs it to say "(from host_id)" and to offer Clear only when there is something to clear; the CLI needs it to detect an old daemon (see S-5). GET and PUT both carry it.

**S-4. Nothing follows a rename automatically.** Peers keep the entry name they have; their next verify shows drift and offers Rename (D1 §4.3 / D2). Helper instance names on peers keep the old alias until re-registered (#1136). Refs (`_xxxxxx`) never change. The daemon does not touch `Peers.Hosts`, helpers, or proxies on a self-alias change. The page states this next to the editor.

**S-5. Old-daemon detection is by the response, not the status.** A daemon < this version decodes the PUT body without the `alias` key, ignores it and answers 200 with the previous alias and **no `alias_source`**. The CLI refuses to report success unless the response carries `alias_source` and its `alias` equals the requested value (or, for `--clear`, `alias_source == "host_id"`) — the same exact-compare rule `host rename` uses. The page shows the daemon's answer and, when `alias_source` is absent, says the daemon is too old.

**S-6. Errors.** Pattern / reserved → 400 with `ValidateSelfAlias`'s text; collision with a peer entry → 409 `alias %q is already used by a peer host` (the same word the entry side uses for the mirror case); invalid JSON → 400 `invalid json` as today.

## 4. Surfaces

### 4.1 Daemon

- `internal/config/config.go`: `ValidateSelfAlias`.
- `internal/peers/wire.go`: `PutSettingsRequest.Alias *string`, `SettingsResponse.AliasSource string`.
- `internal/module/peers/settings.go`: `handleGetSettings` fills `alias_source`; `handlePutSettings` applies `Alias` per S-2/S-6 inside the existing `UpdateConfig` closure (validation under the lock, `apiError` → `writeAPIError`), then reports.

### 4.2 CLI

```
pdx peers alias                       # prints "alias: <effective> (config|host_id)"
pdx peers alias <name>                # PUT {alias: <name>} → prints "alias: <name> (config)"
pdx peers alias --clear               # PUT {alias: ""}     → prints "alias: <derived> (host_id)"
```

`--config <path>` as the other verbs. Exit 1 with the daemon's `{error}` on 400/409; exit 1 with "daemon did not apply the alias (daemon too old?)" per S-5. Usage line added to `peersUsage`.

### 4.3 Peers page

The `peers-self` line gains an inline editor: **Edit** (`peers-self-edit`) → `<input data-testid="peers-self-input">` prefilled with the effective alias, **Save** (`peers-self-save`), **Cancel** (`peers-self-cancel`), and — only when `alias_source === 'config'` — **Clear** (`peers-self-clear`, back to the `host_id` default, shown as `peers.self_alias_default_hint` with the derived value). A note `peers.self_alias_note` says what a rename does and does not do (S-4). The write runs through the page's single flow runner (D4 `runFlow`: page lock, then refresh — the counterparts' return lines will show the drift). Errors (400/409, or the too-old case of S-5) render inline in `peers-self-error` with the daemon's text. `host-api.ts`: `PeerSettings.alias_source?: 'config' | 'host_id'` (optional — an older daemon omits it), `updatePeerSettings(hostId, {alias?: string; deliver?: boolean})` → `PeerSettings`.

## 5. Testing

- config: `ValidateSelfAlias` table — pattern, reserved, case-insensitive collision with a host alias, no hosts, empty string is not this function's business (the handler clears before validating).
- settings handler: GET carries `alias_source` for both cases; PUT `{alias:"x"}` persists to the cfg path and answers `x/config`; PUT `{alias:""}` clears and answers derived/`host_id`; PUT without `alias` leaves it unchanged (with and without `deliver`); 400 on `..` and on a bad pattern with nothing written; 409 on a collision with a configured host alias (case-insensitive) with nothing written; host principal still 403.
- CLI: the three forms hit `PUT /api/peers/settings` with the right body (`{alias:"x"}`, `{alias:""}`, none for the query form which is a GET); prints; an old daemon's echo (200, no `alias_source`) → exit 1 with the too-old message; 409 text passthrough; `pdx peers alias a b` → usage.
- SPA: wrapper `updatePeerSettings` PUTs the body given and returns the response; page: Edit → input prefilled; Save → `updatePeerSettings(hM, {alias:'mlab'})` then refresh (`fetchPeerSettings` called again) and the line shows the new value; Clear only when `alias_source==='config'`, sends `{alias:''}`; 409 → `peers-self-error` shows the daemon text and the input stays; a response without `alias_source` → the too-old text; Save is disabled while a flow runs (page lock); no token in DOM/store/localStorage (the D4 afterEach helpers).
- Mutation: (a) drop the collision clause in `ValidateSelfAlias` → the 409 test red; (b) CLI accepts a 200 without `alias_source` → the too-old test red; (c) the page's Save bypasses `runFlow` → the lock test red.

## 6. Real-machine acceptance (must be run, not assumed)

Both daemons on the version under test. mlab is the canary because its effective alias is derived: `pdx peers alias` on mlab prints `mini-lab (host_id)` → from the page set it to the **same string** `mini-lab` → `alias_source` becomes `config`, air26's Peers page / `pdx peers host verify mini-lab` on air26 shows no drift, `pdx peers --all` green both ways, addresses unchanged → `pdx peers alias --clear` on mlab → `mini-lab (host_id)` again. Negative from the page: `air26` → 409 shown inline; `..` → 400 shown inline. air26's own alias (`air26`, config) is read but not changed.

## 7. Out of scope

`deliver` toggle in the CLI/page (spec D §10 still), renaming helpers on peers (#1136), any automatic propagation of the new name to peers (S-4 by design).
