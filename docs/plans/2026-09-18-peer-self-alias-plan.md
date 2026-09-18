# Peer self alias (#1196) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the daemon's self alias (`[peers] alias`, effective `PeerAlias()`) settable in place — `PUT /api/peers/settings {alias}`, `pdx peers alias [<name>|--clear]`, an inline editor on the Peers page — with entry-alias validation plus "not a configured peer's alias", and honest old-daemon detection.

**Architecture:** Additive on three existing seams: the settings handler already writes under `UpdateConfig` and answers the effective alias; the CLI already has a parsed invocation and a request helper; the page already reads settings and has the D4 flow runner. One PR (three tasks, ~700 lines incl. tests).

**Spec:** `docs/specs/2026-09-18-peer-self-alias-spec.md` — S-1…S-6, §4, §5, §6.

**Plan review:** codex `task-mu70skcm-180cwu` (gpt-5.6-sol, with the spec): 7 findings, all folded in — F1 clear must validate the derived alias; F2 add-host commit-time revalidation against the live self alias + interleaving test; F3 `selfKey` + orphan rule; F4 acceptance includes a real rename; F5 tighter CLI acceptance; F6 `null` = unchanged, pinned; F7 no Edit when the GET already shows the daemon is too old.

## Global Constraints

- Worktree root: `/Users/wake/Workspace/wake/purdex/.claude/worktrees/peer-self-alias`. Prefix every Bash call with `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/peer-self-alias && `.
- Commit with `git commit --only <files>`; never `-A`, never `-am` (the repo root has a version-controlled `pdx` binary). One task = one commit; messages end with `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`.
- Go: `go test ./internal/config/... ./internal/peers/... ./internal/module/peers/... ./cmd/pdx/...` (not `./...` — `internal/module/agent` is a known flake, #1092); `go vet` on the touched packages; `gofmt -l`.
- SPA: pnpm; `cd spa && npx vitest run <path>`; `pnpm run lint`; `npx tsc --noEmit -p tsconfig.app.json`; `pnpm run build`.
- The self alias is stored verbatim (S-2): no trim, no case fold. The daemon never touches `Peers.Hosts`, helpers or proxies on a change (S-4).
- Old daemon: 200 without `alias_source` is **not** success (S-5) — both the CLI and the page must say so.
- D-8 still applies on the page: the D4 `afterEach` helpers (no `pdx_`/`pdxp_` in store/localStorage/DOM) stay in force; this feature never sees a token.
- `MemoryMonitorDisabled.test.tsx` mocks host-api wholesale: no new module-load reference to a host-api export.

## Facts (measured at `82823d33`)

- `config.ValidateAlias(alias, localAlias)` at `internal/config/config.go:72`, `aliasPattern` at `:68`; `PeerAlias()` at `:154`; `PeerHost.Alias` is the entry name; `FindPeerHostByAlias` is case-insensitive.
- `ipeers.SettingsResponse{Deliver, Alias}` / `PutSettingsRequest{Deliver *bool}` at `internal/peers/wire.go:417–426`.
- `handleGetSettings` / `handlePutSettings` at `internal/module/peers/settings.go:24–70`; `writeAPIError` maps `*apiError{status,msg}` → `{error}`; test helpers `newHostsTestCore(t, hostID, alias, token, hosts)`, `newHostsTestModule`, `doHostsRequest`, `adminPrincipal()`, `hostPrincipal(...)`, `loadCfg` in `hosts_test.go` / `settings_test.go`.
- CLI: `runPeersCmd` → `parsePeersInvocation` (`cmd/pdx/peers.go:80–300`), `peersInvocation{hostMode, verb, positionals, cfgPath…}`, `peersUsage`, `doPeersRequest(method, url, body, token, timeout)` → `{status, body}`, `reportPeersTransportErr`, `reportPeersAPIError`, `sanitizeCell`; tests use `fakePeersDaemon(t, handler)` + `runPeersCmd([]string{"--config", cfgPath, …}, &stdout, &stderr)` (`peers_test.go:383`). `host rename`'s exact-compare at `:940–948` is the old-daemon pattern to copy.
- SPA: `PeerSettings{deliver, alias}` + `fetchPeerSettings` in `host-api.ts` (peer-host section); `PeersSection.tsx:153` renders `peers-self` (`{host.name} · self alias: {self_alias} · {host_id}`); `runFlow`/`BoundRunFlow` in `peers/flow.ts` and `PeersSection.tsx:100`; the loader keeps `self_alias` in `snap.self`. i18n flat keys in `spa/src/locales/{en,zh-TW}.json`, `peers.*`.

## File map

| file | change |
|---|---|
| `internal/config/config.go` (+`config_test.go` or `peers_test.go`) | `ValidateSelfAlias` |
| `internal/peers/wire.go` | `PutSettingsRequest.Alias *string`, `SettingsResponse.AliasSource` |
| `internal/module/peers/settings.go` (+`settings_test.go`) | fill `alias_source`; apply `alias` per S-2/S-6 |
| `cmd/pdx/peers.go` (+`peers_test.go`) | `pdx peers alias [<name>\|--clear]` |
| `spa/src/lib/host-api.ts` (+`host-api.peers.test.ts`) | `alias_source`, `updatePeerSettings` |
| `spa/src/components/hosts/PeersSection.tsx` (+test), maybe `peers/SelfAliasLine.tsx` | inline editor |
| `spa/src/locales/en.json`, `zh-TW.json` | `peers.self_alias_*` keys |

---

### Task 1: daemon — `ValidateSelfAlias`, `alias_source`, `PUT {alias}`

**Files:** `internal/config/config.go`, its test file, `internal/peers/wire.go`, `internal/module/peers/settings.go`, `internal/module/peers/settings_test.go`, `internal/module/peers/hosts.go` (add-closure revalidation, F2) + `hosts_test.go`.

**Interfaces:**

```go
// ValidateSelfAlias checks that alias is safe as this host's own alias: the
// same shape rule as ValidateAlias, and not (case-insensitively) the alias
// of any configured peer host — the local alias and the peer aliases share
// the <alias>/<name> address namespace on this host. Empty is not valid
// input here; callers clear Peers.Alias without validating.
func ValidateSelfAlias(alias string, hosts []PeerHost) error   // collision error text: `alias %q is already used by a peer host`

type SettingsResponse struct { Deliver bool; Alias string; AliasSource string `json:"alias_source"` }  // "config" | "host_id"
type PutSettingsRequest struct { Deliver *bool; Alias *string `json:"alias"` }
```

Handler behaviour (inside the existing `UpdateConfig` closure, in this order): `Deliver` as today; then if `req.Alias != nil` (a JSON `null` decodes to nil = unchanged, pinned by a test — codex F6): `""` → **the derived alias must clear the same bar** (codex F1: clearing makes `host_id`'s prefix effective, and a peer entry may carry exactly that name because a different self alias allowed it) — compute `derived := Config{HostID: cfg.HostID}.PeerAlias()` and run `ValidateSelfAlias(derived, cfg.Peers.Hosts)`; a collision → 409 `clearing the alias would make it %q, which is already used by a peer host`; else `cfg.Peers.Alias = ""`; non-empty → `ValidateSelfAlias(*req.Alias, cfg.Peers.Hosts)` → pattern/reserved error → `&apiError{400, err.Error()}`, collision → `&apiError{409, err.Error()}` (distinguish with a sentinel `ErrSelfAliasCollision` wrapped by `ValidateSelfAlias`, checked with `errors.Is`); ok → `cfg.Peers.Alias = *req.Alias`. `resp` built after the writes with `AliasSource: "config"` when `cfg.Peers.Alias != ""` else `"host_id"`. GET fills `AliasSource` the same way (extract `aliasSource(cfg)` helper).

**The other direction of the same invariant (codex F2):** `handleAddHost` validates `req.Alias` against `localAlias` from a pre-lock snapshot (`hosts.go:296`) and its commit closure (`:381`) re-checks only entry uniqueness — a self-alias PUT landing between the snapshot and the commit (the verify dial sits in between) lets an entry equal to the NEW self alias through. Add `config.ValidateAlias(alias, cfg.PeerAlias())` inside the add closure (→ `&apiError{409, …}` on collision, as the rename closure at `:525` already does). Test with a controlled interleaving: the fake peer's verify handler (the httptest server the add path dials) performs a `PUT /api/peers/settings {alias:<the alias being added>}` on the module — or directly `c.UpdateConfig` — before answering; the add must then 409 and write nothing.

- [ ] **Step 1: failing tests.**
  - config: table for `ValidateSelfAlias`: `air26` with hosts `[{Alias:"air26"}]` → collision error, `errors.Is(err, ErrSelfAliasCollision)`; `AIR26` likewise (case-insensitive); `mlab` with those hosts → nil; `..` → reserved; `bad alias!` → pattern; `x` with no hosts → nil; a 65-char alias → pattern.
  - settings: `GET` on a core with `alias:""` (derived) → `{alias:"mini-lab", alias_source:"host_id"}`; with `alias:"mlab"` → `{alias:"mlab", alias_source:"config"}`. `PUT {alias:"mlab"}` → 200 `mlab/config`, on-disk `Peers.Alias == "mlab"`, in-memory too, `Deliver` unchanged. `PUT {alias:""}` on a configured core → 200 derived/`host_id`, on-disk `""`. `PUT {deliver:true}` (no alias key) on a configured core → alias unchanged, `config`. `PUT {}` → nothing changes. `PUT {alias:".."}` → 400 `{error}` containing `reserved`, nothing written (on-disk still old). `PUT {alias:"bad alias"}` → 400. `PUT {alias:"AIR26"}` with a host `air26` → 409 `{error}` = `alias "AIR26" is already used by a peer host`, nothing written. `PUT {alias:"x", deliver:true}` with `x` colliding → 409 **and `Deliver` not written either** (one closure, one transaction). `PUT {"alias":null}` → unchanged (F6). **Clear collision (F1):** core `host_id:"mini-lab:abc"`, `alias:"mlab"`, hosts `[{Alias:"mini-lab"}]` → `PUT {alias:""}` → 409 with the clearing message, on-disk alias still `mlab`. **Add-host race (F2):** as described above → 409, no entry written. Host principal → 403 (existing test extended to PUT with alias).
- [ ] **Step 2:** red. **Step 3:** implement. **Step 4:** `go test ./internal/config/... ./internal/peers/... ./internal/module/peers/...`, `go vet`, `gofmt -l`. **Step 5:** commit `feat(peers): PUT /api/peers/settings {alias} sets the self alias; alias_source in the response (#1196)`.

### Task 2: CLI — `pdx peers alias [<name>|--clear]`

**Files:** `cmd/pdx/peers.go`, `cmd/pdx/peers_test.go`.

`parsePeersInvocation`: a new top-level verb `alias` (mutually exclusive with `host` and with `--all`/`--json`): `pdx peers alias` (query), `pdx peers alias <name>`, `pdx peers alias --clear`; `<name>` plus `--clear`, or two positionals, → usage (exit 2). `peersInvocation` gains `aliasMode bool`, `aliasSet bool`, `aliasValue string`, `aliasClear bool`. `runPeersAliasCmd`: query → `GET /api/peers/settings`; set/clear → `PUT` with `{"alias": <name>}` / `{"alias": ""}`; on 200 decode `cliSettingsResponse{Deliver, Alias, AliasSource}`; **S-5** (tightened per codex F5): the response is accepted only when `AliasSource ∈ {"config","host_id"}` **and** `Alias != ""`; then set requires `Alias == name` (exact) **and** `AliasSource == "config"`; clear requires `AliasSource == "host_id"`; query accepts either source. Anything else → stderr `pdx peers: daemon did not apply the alias (…; daemon too old?)` naming what was wrong (missing/unknown source, empty alias, echoed value), exit 1. Success → stdout `alias: <alias> (<source>)`, exit 0. 400/409 → `reportPeersAPIError` (exit 1). `peersUsage` gains the three lines.

- [ ] **Step 1: failing tests** (with `fakePeersDaemon`): query form GETs `/api/peers/settings` with the bearer and prints `alias: mini-lab (host_id)`; set form PUTs body exactly `{"alias":"mlab"}` (assert the raw body string, so no `deliver` key sneaks in) and prints `alias: mlab (config)`; clear form PUTs `{"alias":""}` and prints `alias: mini-lab (host_id)`; **old daemon**: PUT answered `200 {"deliver":true,"alias":"mini-lab"}` → exit 1, stderr contains `daemon too old`; echo mismatch (`{"alias":"mini-lab","alias_source":"config"}` for a set of `mlab`) → exit 1; set answered with the right alias but `alias_source:"host_id"` → exit 1; clear answered `{"alias":"","alias_source":"host_id"}` → exit 1; query answered `alias_source:"weird"` → exit 1 (F5); 409 body `{"error":"alias \"air26\" is already used by a peer host"}` → exit 1, stderr contains that text; `pdx peers alias a b` and `pdx peers alias a --clear` → exit 2 with usage; `pdx peers --all alias` → exit 2.
- [ ] **Step 2:** red. **Step 3:** implement. **Step 4:** `go test ./cmd/pdx/...`, vet, gofmt. **Step 5:** commit `feat(pdx): pdx peers alias [<name>|--clear] (#1196)`.

### Task 3: SPA — wrapper + inline editor on the Peers page

**Files:** `spa/src/lib/host-api.ts`, `spa/src/lib/host-api.peers.test.ts`, `spa/src/lib/peer-pairing-load.ts` (+test; the one `self_alias_source` field), `spa/src/components/hosts/peers/flow.ts` (`selfKey`), `spa/src/components/hosts/peers/SelfAliasLine.tsx` (new), `spa/src/components/hosts/PeersSection.tsx`, `spa/src/components/hosts/PeersSection.test.tsx`, `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json`.

```ts
export interface PeerSettings { deliver: boolean; alias: string; alias_source?: 'config' | 'host_id' }  // absent on a daemon < alpha.399
export function updatePeerSettings(hostId: string, patch: { alias?: string; deliver?: boolean }): Promise<PeerSettings>  // PUT, JSON body = exactly the keys given
```

The loader (`peer-pairing-load.ts`) is **not** changed: `snap.self` keeps `{host_id, self_alias}`; the line reads `alias_source` from its own `fetchPeerSettings` result? No — avoid a second call: extend `PairingSnapshot.self` with `self_alias_source?: PeerSettings['alias_source']` (one field, populated from the settings call already made in step 0; loader test asserts it). *(This is the one loader touch; keep it to the type + the one assignment.)*

`SelfAliasLine` props: `{ hostId, hostName, self: { host_id, self_alias, self_alias_source? }, busy, runFlow: BoundRunFlow }` — replaces the current `peers-self` paragraph (keep the `peers-self` test id and its existing text content so D2 tests hold). Rendering: the existing text + `(peers.self_alias_from_host_id)` when `self_alias_source === 'host_id'`, then **Edit** (`peers-self-edit`). Editing: `<input data-testid="peers-self-input">` prefilled, **Save** (`peers-self-save`, disabled when the value equals the current alias or is empty or `busy`), **Cancel** (`peers-self-cancel`), **Clear** (`peers-self-clear`, only when `self_alias_source === 'config'`), and the note `peers.self_alias_note`. Save → `runFlow(async () => { const r = await updatePeerSettings(hostId, {alias}); if (!r.alias_source) return { error: t('peers.self_alias_too_old') }; return {} })` (the runner refreshes); Clear → same with `{alias: ''}`. The error also renders in `peers-self-error` (the flow note key is `self`, so give `SelfAliasLine` its own `FlowNote` with `flowKey="self"` — or render `peers-self-error` from a local state set inside the flow fn, like `RotationControls`' gate error). The `runFlow` key is `'self'`: **add `export const selfKey = 'self'` to `flow.ts`** and extend `PeersSection`'s `orphanFlow` rule so a flow under `selfKey` is owned by the line, not orphaned (codex F3) — `SelfAliasLine` renders `<FlowNote flow={flow} flowKey={selfKey} />` for the step/hint and its own `peers-self-error` for the daemon's text (no duplicate: the flow result's `error` is left empty when the line shows it). **Capability known up front (codex F7):** when `self.self_alias_source` is `undefined` (the GET already lacked it), render no Edit button and show `peers.self_alias_too_old` on the line instead of an editor.

i18n: `peers.self_alias_edit` "Edit"; `peers.self_alias_save` "Save"; `peers.self_alias_cancel` "Cancel"; `peers.self_alias_clear` "Use host_id default"; `peers.self_alias_from_host_id` "(from host_id)"; `peers.self_alias_note` "Peers keep the entry name they have and will show this as drift until they rename it; addresses become {{alias}}/<name>. Refs do not change."; `peers.self_alias_too_old` "This daemon ignored the alias (no alias_source in its answer) — it is older than alpha.399."; `peers.self_alias_saving` "Saving alias…".

- [ ] **Step 1: failing tests.** Wrapper: `updatePeerSettings(H, {alias:'mlab'})` PUTs `/api/peers/settings` with body exactly `{"alias":"mlab"}` and JSON content type, returns the body; `{alias:''}` sends `{"alias":""}`; 409 → `HostApiError{409, detail}`. Loader: `self.self_alias_source` carried from the settings call (`'config'`), absent when the settings body lacks it. Page (D4 fixture; `fetchPeerSettings` for `hM` returns `alias_source:'host_id'`): the line shows `(from host_id)` and no Clear; Edit → input prefilled `mini-lab`; typing `mlab` + Save → `updatePeerSettings(hM, {alias:'mlab'})`, then `fetchPeerSettings` called again (refresh), and the line shows `mlab` once the mock flips; with `alias_source:'config'` → Clear present, click → `updatePeerSettings(hM, {alias:''})`; 409 (`alias "air26" is already used by a peer host`) → `peers-self-error` shows it, the input stays open with the typed value; a PUT response `{deliver, alias}` without `alias_source` → `peers-self-error` shows the too-old text; a GET without `alias_source` → no `peers-self-edit`, the too-old text on the line (F7); a flow under `selfKey` is not rendered as an orphan note (F3); while Save's PUT is parked, `peers-refresh`, `peer-inbound-rotate` and `peer-unpair-air` are disabled (page lock); Save disabled when unchanged/empty; every D4 `afterEach` helper still runs.
- [ ] **Step 2:** red. **Step 3:** implement. **Step 4:** `npx vitest run src/components/hosts src/lib/host-api.peers.test.ts src/lib/peer-pairing-load.test.ts`, locale completeness, full suite, lint, tsc, build. **Step 5:** commit `feat(peers): self alias editable on the Peers page (#1196)`.

### Task 4: PR, review, acceptance, deploy

- [ ] PR `feat(peers): self alias settable — PUT settings {alias}, pdx peers alias, Peers page editor (#1196)`; body: spec, what changes on peers (S-4), old-daemon behaviour (S-5), test counts.
- [ ] codex R1 → attack → critic (one round each); fixes → `--base <sha>` re-review.
- [ ] Mutation (spec §5): (a) collision clause dropped → 409 test red; (b) CLI accepts 200 without `alias_source` → red; (c) page Save bypasses `runFlow` → lock test red. Recorded in the PR body (three rows; no separate record file for a change this size).
- [ ] **Deploy daemon** (this PR changes the daemon, unlike D4): mlab `make build` → `./bin/pdx stop` → `env PDX_DEV_MODE=1 ./bin/pdx start` → `/api/health` 200 → `pdx peers alias` prints `mini-lab (host_id)`. air26: `scp bin/pdx air26:~/.config/pdx/bin/pdx.new` → `ssh air26 'mv … && pdx stop && pdx start'` (check how its `pdx serve` is supervised before killing it) → `ssh air26 pdx peers alias` prints `air26 (config)`.
- [ ] **Acceptance (spec §6)** from the worktree dev server on 5175 with the D4 seed script: mlab set to `mini-lab` via the page → `(config)` on CLI, no drift on air26, `pdx peers --all` green both ways → negative `air26` → 409 inline, `..` → 400 inline → CLI `pdx peers alias --clear` on mlab → `(host_id)`. **A real rename (codex F4), on air26 because its addresses are used by fewer live sessions:** `ssh air26 pdx peers alias air26x` → on air26 `pdx peers` prints `air26x/<name>` addresses; on mlab `pdx peers host verify air26` prints `self alias: air26x` + drift, `pdx peers --all` still green, `pdx msg send air26/_64wca8 "…"` still delivers (mlab's entry name is unchanged — S-4), the mlab Peers page shows the drift marker + Rename (not clicked) → `ssh air26 pdx peers alias air26` → drift gone, addresses back. Recorded in the PR.
- [ ] merge → bump (fetch VERSION first) → main checkout `git pull` → memory.
