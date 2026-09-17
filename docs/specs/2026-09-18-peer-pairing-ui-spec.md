# Peer Pairing UI, Return-Path Verification and Token Rotation (Phase D)

**Date:** 2026-09-18
**Status:** Draft — supersedes `2026-09-18-phase-d-pairing-ui-spec.md` (deleted; see §11)
**Depends on:** `2026-09-17-peer-address-v4-spec.md` §7 (a host publishes its own alias),
`2026-09-13-peer-bridge-spec.md` §4.3 (the two-token entry and the two-step pairing)

## 1. Goal

A daemon-to-daemon pairing is two config entries on two machines, four tokens, and today three CLI
commands typed on two hosts with a token carried between them by hand. Nothing tells the operator
whether the pair actually works in both directions: `verifyHost` proves only "I can reach and read
that peer", and `pdx peers host list` renders local config and contacts nobody. The concrete case
that motivates this spec is live right now: mlab's entry is named `air`, the peer calls itself
`air26`, and `pdx peers --all` prints the drift on every call with nowhere to act on it (§2.1).

The App holds an admin token for every daemon it is connected to (`useHostStore`, one `token` per
host). That is exactly the set of daemons whose **both** sides it can read and write, and it is the
single premise of everything below:

1. a **Peers page** under Hosts that shows, per configured peer of the selected daemon, whether the
   pair is bidirectional, one-way or unpaired, with alias drift surfaced and adoptable in one click;
2. **return-path verification** — "can the peer reach me" — obtained by running the existing
   outbound verify *from the peer's side*, which the App can do because it is also the peer's admin;
3. **inbound-token rotation** with a transition field, so a token can be replaced without a window
   in which the other side is locked out.

A pair whose other side the App does not control is shown as exactly that: half-verifiable. The
page never manufactures a "verified" that only covers one direction.

## 2. Facts (measured, not inferred)

### 2.1 Live state on 2026-09-18

```
$ bin/pdx peers host list                       # on mlab
ALIAS  URL                     HOST_ID                VERIFIED  TOKEN  INBOUND  ALLOW_BYPASS
air    http://100.64.0.4:7860  wakes-air-2026:oa6drb  yes       yes    yes      yes

$ bin/pdx peers --all --json | jq -c '.hosts[] | {alias, self_alias, host_id, daemon_version}'
{"alias":"mini-lab","self_alias":"mini-lab","host_id":"mini-lab:278cbm","daemon_version":"1.0.0-alpha.376"}
{"alias":"air","self_alias":"air26","host_id":"wakes-air-2026:oa6drb","daemon_version":"1.0.0-alpha.376"}

$ curl -H 'Authorization: Bearer $ADMIN' http://100.64.0.2:7860/api/peers/settings
{"deliver":true,"alias":"mini-lab"}
```

Both daemons are on alpha.376. One machine has three names in play and the page must label
which is which:

| name | where it lives | who chose it | example |
|---|---|---|---|
| App host name | `useHostStore.hosts[id].name` (App-local, synced across devices) | the App user | `mlab` |
| self alias | daemon `peers.alias` (`PeerAlias()`, falls back to host_id prefix) | that machine's operator | `mini-lab` |
| peer alias | another daemon's `[[peers.hosts]].alias` | whoever ran `host add` there | `air` (for the machine that calls itself `air26`) |

### 2.2 What exists in the daemon (`internal/module/peers/hosts.go`, `internal/config/config.go`)

- `PeerHost{Alias, URL, HostID, Token, InboundToken, AllowBypass}`. `Token` is outbound (what we
  present to them); `InboundToken` is what they must present to us, minted here at POST, unique per
  entry, and **never readable again** after the POST 201 body (`hostRow` is the never-secret view).
- `verifyHost(ctx, url, token)` = `fetch(url + "/api/peers", token)` + `env.OK` + `validHostID`.
  It proves the **outbound** direction of the caller only. It fetches the peer's full inventory
  (the daemon resolves every session's owner under a 2 s budget), so it costs what one row of a
  `scope=all` fan-out costs; `remoteFetchTimeout` is 3 s. No lighter probe endpoint exists.
- `fetchHostResult(ctx, h)` (`module.go`) is `verifyHost` plus the four failure translations the
  fan-out needs (no token / transport / host_id mismatch / invalid host_id) and yields
  `HostResult{Alias, SelfAlias, HostID, OK, Error, DaemonVersion, …}` — every field already bounded
  for display (`boundRemoteText`). Its `SelfAlias` is the peer's self-report, deliberately *not*
  validated (v4 §7.1: validation is right before adoption, wrong before display).
- Routes: `GET/POST /api/peers/hosts`, `PUT/DELETE /api/peers/hosts/{alias}`,
  `GET/PUT /api/peers/settings` (settings returns `{deliver, alias}` — the self alias).
  `HostRoutePolicy` admits a host principal to `GET /api/peers` (scope local) and
  `POST /api/peers/deliver` only; **every other path is admin-only by construction**, so any new
  route under `/api/peers/hosts/…` is admin-only without a policy change. `requireAdmin` is the
  in-handler defense-in-depth.
- `PUT /api/peers/hosts/{alias}` takes `{token?, allow_bypass?}`, each optional and independent.
  Its commit re-check compares `URL` and `InboundToken` to a pre-lock snapshot to detect a
  delete+re-create at the same alias while the verify was in flight.
- `MatchInboundToken` compares the bearer against every entry's non-empty `InboundToken` with
  `subtle.ConstantTimeCompare`, no early exit, last match wins. `PeerAuth` (`middleware/peer_auth.go`)
  is its only caller. `Redacted()` blanks `Token` and `InboundToken` for every config response.
- Pairing today (bridge spec §4.3): on B `host add A <urlA>` (no token → explicit alias required,
  mints `tB`); on A `host add B <urlB> --token tB` (verifies A→B, learns B's host_id and self alias,
  mints `tA`); on B `host set-token A tA` (verifies B→A). Three commands, two machines, two tokens
  carried by hand.
- Alias is read from config on every request. A helper (`pdx peer-proxy`) for a remote sender is
  named `<alias>/<address>` at the sender's `address_rev` (`deliver.go` step 9) and is renamed only
  when that revision advances (`helpers.go` `Acquire` never renames).
- `pdx peers host` grammar: `add | set-token | remove | list` (`cmd/pdx/peers.go`, 936 lines, #1113).

### 2.3 What exists in the SPA

- `hostFetch(hostId, path, init)` attaches the App's admin token for that host;
  `getDaemonBase` is `http://<ip>:<port>`. There is **no** call to `/api/peers/hosts` or
  `/api/peers/settings` anywhere in `spa/src` today (`fetchPeers` reads `GET /api/peers` for the
  tab panel; `usePeerStore` caches it per host).
- Hosts is a per-host page with sub-pages contributed via `setHostBuiltinSections` in
  `register-modules/index.tsx` (`overview … snapshots`, orders 0–9); each section is a component
  taking `{ hostId }`, routed at `/hosts/<encoded id>/<localId>`. Labels are flat i18n keys
  (`hosts.snapshots`) in `locales/en.json` + `zh-TW.json`.
- The App does not cache a daemon's `host_id`; `OverviewSection` fetches `/api/info` on demand.
- The App↔daemon pairing code (`XXXX-XXXX-XXXXX`, `internal/core/pairing_handler.go`,
  `fetchPairVerify`/`fetchPairSetup` in `host-api.ts`) is a different mechanism and is not touched.

## 3. Decisions

**D-1. The return path is verified by running `verifyHost` on the other side.** "B can reach A"
is, by definition, B's outbound verify of its entry for A. A new admin route
`POST /api/peers/hosts/{alias}/verify` runs the existing probe for one entry and returns the
result; the App calls it on A for B and on B for A. Two greens are a bidirectional pair. No new
inbound endpoint, no new token flow, no token leaves the daemon that holds it.

*Rejected:* the inherited draft's `POST /api/peers/verify` where the SPA presents `inbound_token`
to the peer. `inbound_token` is the credential the *peer* must present to *us*; presenting it to
the peer proves nothing about the peer's ability to reach us, and would require the SPA to hold a
value the API deliberately never returns after creation.

**D-2. The page lives under Hosts as a per-host sub-page (`/hosts/<id>/peers`), not a global
matrix.** A pairing is always described from one daemon's config; the sub-page shows that daemon's
`[[peers.hosts]]` and joins each entry to the App host that *is* that peer. Cross-host actions
(verify the return path, adopt the peer's alias on the other side) are still one click, because the
App is admin on both. A global matrix would be a second view of the same data with no extra power
and a harder empty state; it can be added later without changing the daemon.

**D-3. The join between "an entry on X" and "an App host Y" is by daemon `host_id` first, URL
second.** `host_id` is learned at verify time and is what authentication is keyed on; it is the
identity. URL is the fallback only for an entry with `host_id: ""` (added without a token) and is
compared after `normalizeHostURL` on both sides. An App host is identified by `GET /api/info`
`host_id`; the page fetches it (and `/api/peers/settings` for the self alias) for every connected
App host on mount — two small calls per host, nothing cached across mounts.

**D-4. A pair whose other side is not an App host is half-verifiable and is drawn as such.** The
outbound column is verified; the return column reads "not verifiable — <peer> is not a host in this
App" and the row's status is `outbound-only`, never `bidirectional`. This is the v4 §10 constraint
made visible: the App can only pair both directions of hosts it is admin on.

**D-5. Alias drift is resolved by a plain rename, and the daemon does not know it is "adopting".**
`PUT /api/peers/hosts/{alias}` gains an optional `alias` field. The SPA passes the `self_alias` it
just read from the verify result; the daemon validates it with `ValidateAlias` and the uniqueness
rule exactly as it would an operator-typed name (v4 §7.2: a learned alias must clear the same bar).
No `/adopt` route: the primitive is rename, and "adopt what the peer calls itself" is a UI
composition over it. Drift is still surfaced, never followed automatically (v4 §7.4 stands).

**D-6. Verification never persists.** `POST …/verify` reads config, dials, and reports. It does not
write a learned `host_id` into an entry that lacks one (that path — token without host_id — cannot
be produced by the API and exists only for hand-edited configs); making a probe idempotent and
side-effect-free is worth more than closing that corner.

**D-7. Rotation is three steps with the old token valid throughout, and both a commit and a
cancel.** `inbound_token_prev` holds the outgoing token; `MatchInboundToken` accepts either; the
order is mint → push to the peer (its `PUT {token}` verifies against us with the new token, which we
already accept) → commit (drop `prev`). A failed push leaves the peer on the old token, which still
works; cancel restores it as the sole token. A daemon cannot know whether the push landed, so cancel
is offered for "push failed" and the page re-verifies after every step so an inconsistent state is
visible and repairable through the same buttons (§7.4).

**D-8. Live tokens stay in memory for one flow.** The only responses that carry a token value are
POST 201 (`inbound_token`, existing) and rotate (`inbound_token`, new). The SPA holds such a value
in component state only for the duration of the push that consumes it, never in a Zustand store,
never in `localStorage`, never in the synced host config. Token *values* are never rendered; the UI
renders only "has token" booleans.

**D-9. Phases are four PRs, in dependency order, each usable on its own.** D1 daemon (verify +
rename + CLI) → D2 SPA page (status, verify, adopt) → D3 daemon rotation (+ CLI) → D4 SPA pairing
and rotation. D2 already closes the user's live case (§2.1) with nothing from D3/D4. D4 may ship as
two PRs (pair/unpair, then rotation UI) if the plan finds it over the review-size bar.

**D-10. Not in scope, recorded so the boundary is deliberate:** #1120 (origin_inbox impersonation —
user decision: leave it; its "bind admin to loopback" option would cut the App's own tailnet
access); a lighter probe endpoint than `GET /api/peers` (follow-up, §10); renaming live helper
instances on alias rename (§4.3, follow-up); `allow_bypass` and `deliver` toggles in the page (they
are one PUT each and can be added to D2 without design, but they are not part of the goal).

## 4. Phase D1 — daemon: verify one entry, rename an entry

### 4.1 `POST /api/peers/hosts/{alias}/verify`

Admin-only (by `HostRoutePolicy`, plus `requireAdmin`). Body ignored. Looks the entry up under
`CfgMu.RLock`, copies it, releases the lock, and runs **`fetchHostResult(r.Context(), h)`** — the
very function one `scope=all` row is built by — then answers with that row minus its peer rows:

```json
{ "alias": "air", "host_id": "wakes-air-2026:oa6drb", "ok": true,
  "self_alias": "air26", "daemon_version": "1.0.0-alpha.376" }
```

```json
{ "alias": "air", "host_id": "wakes-air-2026:oa6drb", "ok": false,
  "error": "Get \"http://100.64.0.4:7860/api/peers\": dial tcp …: connect: host is down" }
```

- `404 {error: "unknown alias"}` when no entry matches (case-insensitive, `FindPeerHostByAlias`).
- Every probe outcome is a **200 with `ok`**, never a 5xx: "no outbound token", transport errors,
  `HTTP <code>`, "peer: <bounded>", "host_id mismatch: got <bounded>", "peer returned an invalid
  host_id" — the exact strings `fetchHostResult` already produces, so the page and `pdx peers --all`
  can never disagree about one host.
- `host_id` is the configured value, or the learned one when the entry had none (as in the fan-out).
- `self_alias` and `daemon_version` are bounded (`boundRemoteText`) and otherwise verbatim: display
  values, never validated here (v4 §7.1). `self_alias` is `""` when the peer never said.
- No persistence (D-6). No rate limit: it is admin-only, and the peer's `hostLimiter` covers
  `/deliver` only (measured: `deliver.go:171` is the sole `hostLimit.Allow` call).

Reusing `fetchHostResult` is the point, not a shortcut: the verify route *is* one fan-out row, and a
reviewer can check the claim "verify and `--all` agree" by reading one call site.

### 4.2 Rename via `PUT /api/peers/hosts/{alias}`

`putHostRequest` gains `Alias string \`json:"alias"\``. Empty means unchanged. When non-empty:

1. `config.ValidateAlias(req.Alias, localAlias)` → 400 on failure (same message as POST).
2. Uniqueness: `FindPeerHostByAlias(req.Alias)` must be `-1` **or the entry itself** (so `air` →
   `Air` is a legal case change). Otherwise 409 `alias %q is already used by another host`.
3. Both checks re-run inside the `UpdateConfig` closure, after the existing identity re-checks,
   before any field is written; `h.Alias = req.Alias` is the last write. The response `hostRow`
   carries the new alias (captured inside the closure, as today).

`token`, `allow_bypass` and `alias` remain independent: any subset in one request. When `token` and
`alias` arrive together, the verify runs against the URL found under the *old* alias and the commit
finds the entry by the *old* alias; only the final write changes the name.

### 4.3 Consequences of a rename, stated

- Every address on this host under that peer changes head immediately: `air/barbox-a6` becomes
  `air26/barbox-a6` on the next `GET /api/peers?scope=all`, `pdx peers --all`, and in the tab panel.
  That is the goal — after the rename the address is the same string on both machines.
- Delivery, audit, and `Principal.Alias` read config per request; nothing keyed on the old alias
  survives in memory except **helper instance names** (`<alias>/<address>`), which are renamed only
  when the sender's `address_rev` advances. A remote sender's proxy therefore keeps showing
  `air/…` in this host's registry until that sender next changes its own address. Cosmetic, bounded,
  and listed as a follow-up (§10) rather than fixed here.
- The counterpart entry on the peer is untouched: renaming what *we* call *them* says nothing about
  what they call us. The page (D2) shows the other direction's drift as its own row-side action.

### 4.4 CLI

```
pdx peers host verify <alias> [--json] [--config <path>]
pdx peers host rename <alias> <new-alias> [--config <path>]
```

`verify` prints `ok` / `FAILED: <error>` plus `self alias: <bounded, sanitizeCell>` and, when it
differs from `<alias>`, `alias drift: this host calls it "<alias>", it calls itself "<self>"` — the
same wording `pdx peers --all` uses — exit 0 on `ok`, 1 otherwise. `rename` is a thin wrapper over
the PUT and prints `renamed <old> -> <new>`. Both are ~40 lines in `cmd/pdx/peers.go`; #1113
(splitting that file) is not made harder and is not attempted here.

## 5. Phase D2 — SPA: the Peers sub-page

### 5.1 Placement and data

- Host sub-page contribution `{ localId: 'peers', labelKey: 'hosts.peers', order: 10,
  component: PeersSection }`; route `/hosts/<id>/peers`. Component
  `spa/src/components/hosts/PeersSection.tsx`, taking `{ hostId }` like its siblings.
- `host-api.ts` gains typed wrappers: `listPeerHosts(hostId)` (`GET /api/peers/hosts` →
  `PeerHostRow[]`), `verifyPeerHost(hostId, alias)` (`POST …/verify` → `PeerHostVerify`),
  `updatePeerHost(hostId, alias, {alias?, token?, allow_bypass?})` (PUT → `PeerHostRow`),
  `fetchPeerSettings(hostId)` (`GET /api/peers/settings` → `{deliver, alias}`),
  plus `fetchInfo` (exists). Wire types mirror `hostRow` / §4.1 field-for-field.
- Pure logic in `spa/src/lib/peer-pairing.ts`, unit-tested without React:
  - `matchCounterpart(entry, appHosts: {hostId, host_id, url}[])` → the App host whose daemon
    `host_id` equals `entry.host_id`, else (only when `entry.host_id === ''`) whose normalized URL
    equals `entry.url`, else `null` (D-3).
  - `pairStatus(outbound: VerifyOutcome, inbound: VerifyOutcome | 'no-entry' | 'not-app-host')` →
    `'bidirectional' | 'one-way' | 'outbound-only' | 'unpaired'`:

    | outbound | inbound | status |
    |---|---|---|
    | ok | ok | `bidirectional` |
    | ok | failed / `no-entry` | `one-way` (with which direction failed) |
    | ok | `not-app-host` | `outbound-only` (half-verifiable, D-4) |
    | failed | ok | `one-way` |
    | failed | failed / `no-entry` / `not-app-host` | `unpaired` |
    | pending on either side | — | `checking` |
  - `aliasDrift(entry.alias, verify.self_alias)` → `self_alias` when non-empty and not
    case-insensitively equal, else `''` — the same rule as `aliasDriftField` in `cmd/pdx/peers.go`.

### 5.2 What the page does on mount (selected App host X)

1. `listPeerHosts(X)` → entries.
2. For every App host H in `hostOrder` with runtime status `connected`: `fetchInfo(H)` →
   `host_id`; `fetchPeerSettings(H)` → self alias. Hosts that are not connected are joined by
   nothing and count as `not-app-host` for this render (the row says "<name> is not connected").
3. For each entry E: `Y = matchCounterpart(E, …)`. If `Y` is an App host: `listPeerHosts(Y)` →
   find `E'` whose `host_id === X.host_id` (URL fallback as in D-3) → the return-path entry, or
   `'no-entry'`.
4. Verify **automatically, in parallel**, each direction that has an entry:
   `verifyPeerHost(X, E.alias)` and `verifyPeerHost(Y, E'.alias)`. Results are component state;
   a Refresh button re-runs 1–4. Nothing is persisted or cached across mounts: every verify is a
   live dial, and a stale green is the thing this page exists to remove.

Cost bound: with `n` entries the page issues at most `2n` verify calls, each ≤ 3 s on the daemon
side, in parallel — the same load as one `pdx peers --all` on each of two hosts.

### 5.3 What one row shows

```
air  (App host "Air 2026")                                    [Verify]  [Rename to air26]
  http://100.64.0.4:7860 · wakes-air-2026:oa6drb
  mlab → air     ✓ reachable, daemon 1.0.0-alpha.376        calls itself "air26" (drift)
  air  → mlab    ✓ reachable  (air's entry: mini-lab)
  status: bidirectional
```

- Header: the peer alias as X's config has it, then the App host name when joined (D-2's three
  names, each labelled). Below: URL and host_id.
- Outbound line: result of `verify(X, E.alias)`; the peer's self alias, with a drift marker when
  `aliasDrift` is non-empty, and a **Rename to `<self_alias>`** button → `updatePeerHost(X, E.alias,
  {alias: self_alias})` → refresh. A 409 (name already used on X) or 400 is shown inline with the
  daemon's message; nothing is retried or auto-suffixed (v4 §7.2).
- Return line: result of `verify(Y, E'.alias)` when `Y` is an App host and `E'` exists, with its
  own drift marker and **Rename** button acting on `Y`; "no entry for <X self alias> on <Y name>"
  when `E'` is missing (pairing that direction is D4); "not verifiable — not a host in this App"
  when `Y` is `null` (D-4).
- Status word from `pairStatus`. `outbound-only` is drawn in the neutral colour, not green.
- `has_token: false` shows "no outbound token" on the outbound line without dialling (the daemon
  answers that without a network call, §4.1).

### 5.4 What the page does not do (yet)

No pairing, unpairing, or rotation controls — those are D4 and the buttons do not exist until then,
so the page cannot half-implement them. No `allow_bypass`/`deliver` toggles (D-10).

## 6. Phase D3 — daemon: inbound-token rotation

### 6.1 Config

`PeerHost` gains `InboundTokenPrev string \`toml:"inbound_token_prev" json:"inbound_token_prev"\``.
`Redacted()` blanks it alongside `InboundToken`. `hostRow` gains `RotationPending bool
\`json:"rotation_pending"\`` (= `InboundTokenPrev != ""`); the value itself is never served.

### 6.2 `MatchInboundToken`

Compares the bearer against every entry's non-empty `InboundToken` **and** non-empty
`InboundTokenPrev`, both with `subtle.ConstantTimeCompare`, no early exit, same last-match-wins
shape as today. A match on either field authenticates as that host with the same `Principal`;
nothing downstream can tell which token was used (it does not need to). An entry with a `prev` and
no current token is not a state the API can produce; if a hand-edited config has one, `prev` alone
still authenticates (the field is a token, not a flag).

### 6.3 Routes (all admin-only by `HostRoutePolicy` + `requireAdmin`)

| route | effect | responses |
|---|---|---|
| `POST /api/peers/hosts/{alias}/rotate` | `prev := current; current := mint()` | `200 {alias, inbound_token: <new>}` — the second and last response that carries a live token; `409 rotation already pending` when `prev != ""`; 404 unknown alias |
| `POST /api/peers/hosts/{alias}/rotate/commit` | `prev := ""` | `200 hostRow`; **idempotent** — with no `prev` it is a 200 no-op, so a lost response is safely retried; 404 |
| `POST /api/peers/hosts/{alias}/rotate/cancel` | `current := prev; prev := ""` | `200 hostRow`; `409 no rotation pending` when `prev == ""` (there is nothing safe to restore); 404 |

All three mutate inside one `UpdateConfig` closure, re-finding the entry by alias under the lock;
`rotate` mints with `mintInboundToken(adminToken)` (never the admin token; a 128-bit random
collision with any other entry is not checked, as today at POST).

### 6.4 The flow the App drives (D-7), with what each failure leaves behind

```
X: rotate(E)            → new token tX'; X now accepts tX (prev) and tX' (current)
Y: PUT E' {token: tX'}  → Y verifies Y→X with tX' (X accepts it), stores it
X: commit(E)            → X drops tX; only tX' is valid
```

| step that failed | state | what still works | repair from the page |
|---|---|---|---|
| rotate | nothing changed | everything | retry |
| push (network, or Y's 502 from its verify) | X accepts both; Y still holds tX | Y→X with tX | **Cancel** (restores tX as sole token) or retry the push — but the page no longer has tX' after a reload (D-8), so after a reload the only offer is Cancel |
| commit (lost response) | X accepts both; Y holds tX' | Y→X with tX' | Commit again (idempotent) |
| user cancels after a successful push | X accepts tX only; Y holds tX' | **Y→X is broken** | verify shows it red; rotate again and push |

`rotation_pending` on the row is what the page keys its Commit/Cancel offer on, so a reload lands
on a consistent, if unfinished, state.

### 6.5 Interaction with the existing PUT identity check

`handlePutHost` refuses with 409 `entry changed concurrently` when `InboundToken` differs from its
pre-lock snapshot. A `rotate` landing between a PUT's snapshot and its commit therefore 409s that
PUT; the App retries. Correct and unchanged: the check is there to catch a *different* entry
wearing the same alias, and a rotated entry is not that, but a retry is cheaper than teaching the
check to distinguish them. Noted so a reviewer does not "fix" it.

### 6.6 CLI

```
pdx peers host rotate <alias> [--config <path>]          # prints the new token, like add does
pdx peers host rotate <alias> --commit | --cancel
```

## 7. Phase D4 — SPA: pair, unpair, rotate from the page

### 7.1 Pair (both directions, App-controlled hosts only)

Below the entry rows, "Pair with…" lists App hosts `Y ≠ X` (by `host_id`; a host whose info could
not be fetched is not listed) that have no entry on X. **Pair** runs:

1. `POST Y /api/peers/hosts {alias: <X self alias>, url: <X url>}` (no token; the alias is X's own
   self-report, which is what Phase C would have adopted had a token been available). 201 →
   `tY`. 409 (`alias already used` on Y) → an inline field asks for the alias Y should use for X.
2. `POST X /api/peers/hosts {url: <Y url>, token: tY}` (no alias: X verifies X→Y and adopts Y's self
   alias; a 409 there asks for the alias X should use for Y). 201 → `tX`.
3. `PUT Y /api/peers/hosts/<alias from 1> {token: tX}` → Y verifies Y→X.
4. Refresh (§5.2).

`X url` and `Y url` are `getDaemonBase()` of the App hosts. This assumes the two daemons see each
other at the address the App uses — true on this tailnet, and stated as an assumption: when step 2's
verify fails with a transport error, the row shows it and the entry created in step 1 is deleted
again (so a failed pair leaves nothing behind); when step 3 fails, both entries exist, the row shows
`one-way`, and **Retry return path** re-runs step 3 only after a `rotate` on X to obtain a fresh
pushable token (D3), because `tX` cannot be re-read.

Repair path for the existing one-way case (Y has an entry for X, X has none for Y): Pair is offered
and step 1 is replaced by `rotate` on Y's entry (gives a readable `tY`), steps 2–3 as above, then
`commit` on Y.

### 7.2 Unpair

**Unpair** on a row: confirm dialog naming both sides; `DELETE X …/{E.alias}` and, when `E'` exists
on an App host, `DELETE Y …/{E'.alias}`. Either 404 is treated as already done. A non-App
counterpart is left as is and the dialog says so.

### 7.3 Rotate

Per direction line, **Rotate**: runs §6.4 and shows each step; on `rotation_pending` after a
reload, the line shows "rotation pending" with **Commit** and **Cancel** only (no token is
available to re-push). Only offered when the counterpart is an App host (the push needs its admin
token); for a non-App counterpart the button is absent and a tooltip says why.

### 7.4 Every action ends in a re-verify

Pair, unpair, rename, rotate, commit and cancel each end by re-running §5.2 for the affected rows.
The page's claim is always the result of the last dial, never the result of the last write.

## 8. Testing

Go tests in `package peers` (`internal/module/peers`) and `package config`; SPA tests in Vitest.
**Mutation tests are a deliverable**: the plan lists each mutation below and the test that must go
red; the implementer runs them and records the red in the PR.

### 8.1 D1

| test | mutation that must break it |
|---|---|
| verify 200 `ok:true` with `self_alias`/`daemon_version` bounded from a fake fetch | drop `boundRemoteText` → a 5 KB alias comes back whole |
| verify 200 `ok:false, error:"no outbound token"` with **zero** fetch calls | remove the `Token == ""` short-circuit → the fake fetch is called |
| verify `ok:false` `host_id mismatch` when configured host_id ≠ envelope | drop the mismatch branch → `ok:true` |
| verify never writes: config file bytes identical before/after an `ok:true` call on an entry with `host_id: ""` | add a "learn host_id" write → file differs |
| verify 404 unknown alias; 403 for a host principal (direct handler call) | — |
| rename 200, row and file carry the new alias; old alias 404 afterwards | — |
| rename 409 on another entry's alias (case-insensitive), file unchanged | remove uniqueness check |
| rename to own alias with different case succeeds | make uniqueness exclude only exact matches |
| rename 400 on `ValidateAlias` failure (`..`, local alias, 65 chars) | — |
| rename + token in one PUT: verify ran against the old-alias URL, commit renamed | — |
| concurrent rename: closure re-check 409 when another entry took the name between snapshot and commit | remove the in-closure re-check |
| CLI `verify` / `rename` argument grammar, exit codes, drift wording | — |

### 8.2 D2

- `peer-pairing.ts`: table-driven tests for every row of the `pairStatus` table, `matchCounterpart`
  (host_id wins over URL; URL only when host_id empty; normalized trailing slash), `aliasDrift`
  (empty self alias is not drift; case-insensitive equal is not drift).
- `PeersSection`: with mocked `host-api`, renders the §5.3 row for the §2.1 fixture (mlab/air);
  shows the drift marker and the Rename button with `air26`; clicking calls
  `updatePeerHost('X', 'air', {alias: 'air26'})` and refreshes; a 409 renders the daemon message;
  a non-App counterpart renders `outbound-only` and no Rename on the return line; a disconnected
  App host renders as not joined.
- Mutation: flip `pairStatus`'s `outbound-only` to `bidirectional` → the non-App fixture test fails.

### 8.3 D3

| test | mutation that must break it |
|---|---|
| `MatchInboundToken`: while pending, **both** old and new authenticate as the same alias | remove the `prev` comparison |
| after commit, old is refused, new accepted | commit that does not clear `prev` |
| after cancel, old accepted, new refused | cancel that does not restore |
| rotate 409 when pending; commit 200 no-op when not pending; cancel 409 when not pending | — |
| rotate response carries a fresh `pdxp_` token ≠ old, ≠ admin; `hostRow` never carries either value; `Redacted()` blanks `prev`; `rotation_pending` true/false | drop `prev` from `Redacted` → the config JSON test sees the value |
| `PeerAuth` end-to-end through the real middleware with a pending rotation | — |
| PUT `{token}` during a pending rotation 409s `entry changed concurrently` (documents §6.5) | — |
| CLI `rotate` / `--commit` / `--cancel` | — |

Constant-time-ness is not tested by timing (the inherited draft's "flip a bit, same time" is not a
unit test that can pass or fail deterministically); it is guaranteed by construction —
`subtle.ConstantTimeCompare`, no early exit — and a test asserts that **every** entry's two fields
are compared even after a match (a counting fake or a last-match-wins fixture).

### 8.4 D4

- Pair happy path: the three calls in order with the right bodies, then refresh.
- Step 2 fails → step-1 entry deleted on Y; nothing on X.
- Step 3 fails → both entries exist, status `one-way`, retry path uses `rotate` + `PUT` + `commit`.
- 409 on step 1 → alias prompt; the retry uses the typed alias.
- Unpair: both DELETEs; 404 on either is success; non-App counterpart not deleted.
- Rotate: mint → push → commit call order; push failure leaves Cancel offered; reload with
  `rotation_pending` offers Commit/Cancel only; no token value ever reaches a store
  (assert `useHostStore` state and `localStorage` contain no `pdxp_` after the flow).

## 9. Real-machine acceptance (must be run, not assumed)

Recorded per phase in the PR; the two hosts are mlab (`mini-lab:278cbm`) and air-2026
(`wakes-air-2026:oa6drb`), both on the version under test.

- **D1:** `pdx peers host verify air` on mlab prints `ok`, `self alias: air26`, the drift line.
  `curl -X POST …/api/peers/hosts/air/verify` returns the §4.1 JSON. Stop air's daemon → `ok:false`
  with a transport error, `pdx` exits 1.
- **D2:** open `/hosts/<mlab>/peers` in the App: one row `air`, joined to the App's air host,
  both lines green, status `bidirectional`, drift marker `air26`, Rename button. Click it → row
  header reads `air26`, `pdx peers --all` on mlab prints `air26/…` addresses and no drift.
  Then from a Claude Code session on mlab, `pdx msg send air26/<name> "…"` delivers (the address is
  now the same string the air side prints). This closes the §2.1 case.
- **D3:** `pdx peers host rotate air` on mlab, `pdx peers host set-token mini-lab <new>` on air,
  `pdx peers --all` still green from both sides, `pdx peers host rotate air --commit`; `pdx peers
  host list` on mlab shows no pending. Negative: rotate, do *not* push, `pdx peers --all` on air is
  still green (old token honoured), `--cancel`, still green.
- **D4:** unpair mlab↔air from the page, both `host list`s empty; Pair from the page, both
  `host list`s show the entry with both tokens, `pdx peers --all` green both ways; rotate from the
  page, both ways green after.

## 10. Follow-ups to open as issues when each phase ships

- helper instance names keep the old alias after a rename until the sender's `address_rev`
  advances (§4.3) — either a `helpers.RenameHost(old, new)` or accept as cosmetic and document.
- a lighter probe than `GET /api/peers` for verification (`GET /api/peers/ping` returning the
  envelope header without rows) — verify today costs a full inventory resolve.
- `allow_bypass` and `deliver` toggles on the Peers page.
- a global "all pairs" matrix view over the same data (D-2).
- #1113 grows by four verbs; the split should happen before the next one.

## 11. What the inherited draft got wrong (recorded so it is not re-derived)

| draft (`2026-09-18-phase-d-pairing-ui-spec.md`, deleted) | why it was wrong | now |
|---|---|---|
| D2: SPA presents `inbound_token` to the peer's new `POST /api/peers/verify` | `inbound_token` is the peer's credential *to us*; presenting it outward proves nothing about the return path, and the SPA cannot hold it (never re-readable) | verify = the peer's own outbound probe, run by the App as the peer's admin (D-1) |
| "constant-time compare: flipping a bit takes the same time" as a unit test | timing is not deterministically assertable in a unit test | by construction + a "compares every field" test (§8.3) |
| no `inbound_token_prev` | rotation without a transition field locks one side out whichever side writes first (v4 §10) | D3 (§6) |
| "Section B: peer-listing-only hosts" with no statement of why the split exists | the split is the *premise*: the App is admin on both sides of a pair or it is not, and only the former is fully verifiable | D-4, `outbound-only` status, and the Pair list restricted to App hosts |
| global `/pair` route + `fetch('/api/peers/hosts')` with no host and no auth header | the SPA is multi-host; every call goes through `hostFetch(hostId, …)` | per-host sub-page (D-2), `host-api.ts` wrappers (§5.1) |
| a daemon "coordinator" for multi-host verification | the App is the coordinator — it is the only party holding both admin tokens | no daemon-side orchestration anywhere |
| spec and plan never reviewed by codex | — | this spec goes to `codex review --model gpt-5.5` before the plan is written |

The draft's D1 SPA skeleton (`Pair.tsx`, `usePairStore.ts` and tests) is deleted in the same commit
as this spec; it survives in the branch's WIP commit `3079f2a3` for reference. The main checkout
(not this worktree) still carries uncommitted edits from the same draft — a global `pair` route in
`route-utils.ts`, `register-modules/index.tsx` and `types/tab.ts`, plus an untracked
`components/Pair.tsx` — which this branch does not include and which should be discarded there.
