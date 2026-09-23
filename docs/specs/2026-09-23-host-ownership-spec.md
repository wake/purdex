# Spec — host ownership: hosts belong to the device, their look belongs to the workbench

Status: user decisions 2026-09-23 (below are not to be re-litigated). Supersedes kickoff decision 7 ("host token in
the profile") and the host half of decision 9 ("the master is the only source of hosts"). Terms (zh-TW): profile =
工作台, master = 工作台主檔, slave = 本機工作台, a workbench's settings = 工作台設定檔.

## 1. Decisions (user, 2026-09-23)

1. **The host list is device-level.** Which daemons a device reaches — the list, order, ip, port, token, daemonId —
   is the device's, and is NOT carried by Profile Sync.
2. **Hosts move between devices by a one-time transfer code, relayed through any host both devices share** (not
   necessarily the dev host). The receiver chooses per host and a mode: add new only / overwrite the same daemon /
   replace all. Only the short relayed code — no self-contained long code.
3. **A host's look (name, icon, colours) follows the workbench** — synced by Profile Sync, keyed by the daemon's wire
   id (`d1_…`), exactly as today's user experience.
4. **A workbench has a synced "shown hosts" setting.** Hosts not listed still exist and stay connected; they are only
   hidden in that workbench. Unset = all shown. Ids this device lacks are kept, never pruned.
5. **Daemon-side settings (projects, commands, resume templates, daemon config, hooks, monitor, peers) are already
   shared by every client** through the daemon — no sync, no change.

## 2. Where each piece of host data lives afterwards (measured on alpha.439)

| Data | Owner | Synced? |
|---|---|---|
| host list, `hostOrder`, ip, port, token, daemonId, `activeHostId`, `devHostId` | device (`purdex-hosts`) | no — transfer code only |
| name, colours (`colors`, legacy `color`), icon, iconWeight | workbench (a new settings store keyed by wire id) | yes (`settings`) |
| shown hosts | workbench (same store) | yes (`settings`) |
| `purdex-host-settings` (editor.homePath), New Tab `sessions:` / `headless:` columns | workbench | yes (unchanged) |
| pane host references in `tabs.*` | workbench | yes (unchanged) |
| projects, commands, resume templates, daemon config, hooks | daemon | no (already shared) |

The wire identity (`d1_…` from the daemonId, else the local id) stays: tabs and settings still name hosts across
devices. Only the `hosts` SECTION and everything that exists to carry or reconcile it goes.

## 3. Traps that must be fixed BEFORE hosts stop syncing (they are silent today because lists agree)

1. **`applyTabsSection` brands panes of hosts unknown here as `terminated: 'host-removed'`** and that synced mark is
   pushed back (`apply-to-stores.ts` `markHostRemovedPanes`). With per-device lists one device lacking host X would
   mark every live X pane removed on every device. → An incoming pane naming a host this device lacks is applied
   AS IS; the pane renders a local-only state "this device has no host ‹name›" (name from the workbench host look,
   else the wire id), never writes a mark, and becomes live when the host is added here.
2. **The settings apply and `useNewTabBootstrap` prune New Tab columns of hosts not here, and the prune is pushed.**
   → Columns naming hosts this device lacks are kept in the store and in the build; only rendering skips them.
3. **Gates:** `tabs.*` / `settings` wait for `hosts` settled — the `hosts` half of the gate goes; `settings` keeps
   waiting for `workspaces`.

## 4. Phases (each a PR ≤ 20 files, merged in order)

- **H1 — tolerate hosts that are not here** (§3.1, §3.2; no wire change, safe alone). Tests: a payload naming an
  unknown host keeps the pane and the column byte-for-byte, nothing pushed back; adding the host later makes the
  pane live.
- **H2 — the workbench owns host looks and shown hosts.** New store `purdex-host-looks` (`{ [wireId]: { name?,
  colors?, color?, icon?, iconWeight? } }`) + `purdex-shown-hosts` (`{ ids: wireId[] } | null`), projected in
  `settings` (wire ids, unknown ids kept). The host UI reads the look through one selector (look, else the device's
  HostConfig fields — which stay as the device fallback for hosts without a daemonId). Writes of name / colour / icon
  go to the look store. Shown-hosts UI in Settings › 工作台. Migration: on first run, copy each host's current look
  into the look store under its wire id. `settings` ordinal +1 and a wire marker.
- **H3 — the `hosts` section leaves Profile Sync.** Stop building and applying it; the collector ignores it; the SOT's
  existing `hosts` sections are left alone (never read, never deleted — deleting would need a tombstone push that old
  clients act on). Remove: hosts apply / refusal / cascade path, alias machinery, the #1370 pull guard, the wizard's
  removal list, the `hosts` gate half. Keep: wire identity, `blocked: host-identity-*`, master endpoint. Wizard:
  pull no longer replaces hosts; the attach host must exist on this device (it always does — the wizard talks to it).
  Ordinal / marker so old clients lock (`hosts`, `tabs`, `settings` as needed). `profiles.db` no longer receives
  tokens from new clients (old sections keep theirs until the profile is deleted — note it in the UI copy of
  "delete a workbench on the sync host").
- **H4 — host transfer code** (daemon + SPA; independent of H1–H3, can run in parallel after this spec is approved).
  Daemon: authenticated `POST /api/host-transfer` stores `{hosts payload}` under a fresh 8-character code (Crockford
  base32, no ambiguous letters), TTL 10 min, one-time, in memory (never on disk); authenticated
  `POST /api/host-transfer/redeem {code}` returns and deletes it; failure counter per client like the pairing handler.
  The payload is the sender's chosen hosts: name, ip, port, token, daemonId, looks. SPA: Hosts page "Share hosts…"
  (pick hosts + a shared host to relay through → shows the code), "Receive hosts…" (pick a shared host, enter the
  code → per-host list with the three modes; dedup by daemonId; the receiver verifies each host by `/api/info`).

## 5. Acceptance

H1: A and B with independent host lists (B lacks host X): A opens a tab on X → B shows it as "no host X here", A
still live, nothing marked. H2: change mlab's colour on A → B follows; hide a host in workbench W on A → hidden on
B in W. H3: a device adds a host locally → no other device gets it; a pull never removes a local host. H4: A shares
mlab + air26 through mlab → B enters the code → gets both (add-new mode), connected, looks from the workbench.
