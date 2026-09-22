# Re-reconcile sessions after a profile switch (#1255, SPA half) — spec

Status: draft (2026-09-23) · Owner: purdex-newtab-preset (mlab/purdex-3b) · Coordinator: mlab/purdex-fb
Depends on: the daemon half, `docs/specs/2026-09-23-session-list-fresh-spec.md` (#1292, alpha.423).

## 1. Problem

Session reconciliation (`reconcileHostSessions`, `lib/rebuild/reconcile-host.ts`)
only runs when a host's WS `sessions` frame arrives, and only over the tabs on
screen. A world that was parked while one of its sessions closed comes back on
screen un-reconciled and stays so until that host's next `sessions` frame —
which, with no session change, is never (the daemon pushes on change; the 5 s
ticker also only pushes on change).

Re-running the reconciliation right after a switch was refused so far
(kickoff record, `switch-active.ts` header "WHAT A PARKED WORLD DOES NOT HEAR"):
the lists the SPA had were not evidence. `session-closed` is irreversible and
`tmux-restarted` + revive-by-name re-point panes; a master world pushes those
bindings to the SOT. The last WS payload has no version, `GET /api/sessions`
was a 1 s cache. The daemon now offers a fresh, versioned list (`?fresh=1` →
`{epoch, seq, sessions}`; WS `sessions` frames carry top-level `epoch`/`seq`).

## 2. Goals / non-goals

- G1. After `switchActiveProfile` puts another world on screen, every host whose
  connection is live is reconciled against a list **read after the switch**.
- G2. A list is applied only if it is **newer** than anything already reconciled
  for that host (daemon contract §3.3–§3.4), whichever channel it came on — fetch
  or WS. An older or incomparable list is dropped, never reconciled.
- G3. An unversioned list (old daemon: array body / frame without `epoch`) is never
  used by the new post-switch path; the WS path keeps today's behaviour for it.
- G4. A result that arrives after the world changed again (another switch, in this
  or another window) is dropped.
- G5. Frames of a closed or superseded host-events socket never reach the handler.

- G6. Revive-by-name never acts on a list reconciled for a DIFFERENT world than the one
  on screen (codex plan review #1: today the switch's own operation-lock release runs
  `runRevivePassAll` over the pre-switch list against the new world).

Non-goals: changing the reconciliation itself (`reconcile.ts`, `reconcile-host.ts`
body); fetching on anything but a switch; PRODUCT.md.
- **Profile applies** (`apply-to-stores.ts` `workspaces` / `tabs.*`, and the wizard's
  pull, which ends there) also put panes on screen with no post-apply session evidence.
  Out of scope: that file is the coordinator's (P3d-4b), and reconciling panes that just
  arrived from the SOT — then pushing the verdicts back — is a sync-semantics decision.
  Follow-up issue.
- `promoteToMaster` relabels and restamps; the panes on screen do not change → nothing to do.

## 3. Design

### 3.1 Per-host held version — `lib/rebuild/session-version.ts` (new, small)

State per host: `held: {epoch: string, seq: number} | null` and `conn: number`
(a connection generation). Pure decisions + a tiny module store (no zustand, not
persisted, not synced — it describes this window's connections).

- `parseVersion(x)`: `{epoch, seq}` only when `epoch` is a 16-lowercase-hex string and
  `seq` a safe integer ≥ 1; otherwise `null` (= unversioned).
- `connectionOpened(hostId)` / `connectionClosed(hostId)`: bump `conn`. Called from the
  hook's `onOpen` / `onClose` and on entry teardown.
- `decide(hostId, v, origin)` → `'apply' | 'stale'` where `origin` is
  `{kind: 'ws'}` (a frame of the current socket — the hook only sees those, §3.3) or
  `{kind: 'fetch', conn}` (the `conn` captured when the fetch was sent):
  - `held === null` → apply.
  - same epoch → apply iff `v.seq > held.seq`.
  - different epoch → WS: apply (the current socket speaks for the running process);
    fetch: apply iff `origin.conn === current conn` (sent on the current connection,
    daemon contract §3.4), else stale.
- `note(hostId, v)`: `held = v` — only after `reconcileHostSessions` returned without
  throwing (codex #4). A throw leaves `held` where it was.
- A WS frame without a version sets `held = null` (the host is — again — an old
  daemon; nothing versioned may be compared against a list from before it).

### 3.2 The post-switch refresh — `lib/rebuild/refresh-after-switch.ts` (new)

`refreshSessionsAfterSwitch(): void`, fire-and-forget, called by
`switchActiveProfile` when it returns `{ok: true}` (after both locks are released).

For each host in `hostOrder`, independently (one host's failure costs no other):
1. Skip unless the attach gate is open (`canAttachTerminal(hostId)`): the gate open
   means this window's live connection has reconciled a payload. A closed gate means a
   (re)connect is in progress, and that connection's own first frame — read by the
   daemon after the subscribe, so after the switch — will reconcile the new world.
2. Capture `world = <current world generation>` and `conn`.
3. `GET /api/sessions?fresh=1` (`listSessionsFresh` in `host-api.ts`).
   Array body → unversioned → stop. Non-2xx / network error → retry (§3.2.1).
4. Drop if the world generation changed (G4), the gate closed, or the host left
   `hostOrder` / changed endpoint since step 2.
5. `decide(…, {kind: 'fetch', conn})`; `stale` → drop. `apply` →
   `reconcileHostSessions(hostId, sessions)` (which also refreshes `useSessionStore`,
   the revive snapshot, and runs the revive pass and probes, exactly as a WS frame),
   then `note` (§3.1: only if it did not throw).

#### 3.2.1 Retries, one refresh per host (codex adversarial F3)

With no further session change the host never pushes again, so a refresh that gives up
on one failure leaves the old bindings on screen for good. `refreshHost(hostId, fences)`
therefore retries a failed fetch (network, non-2xx) or a reconciliation that threw up to
3 times, after 1 s, 2 s and 4 s. `fences` = `{world, conn, endpoint, requireGate}`
captured when the refresh starts; before EVERY attempt all of them are re-checked (world
fence unchanged, host still in `hostOrder` at the same endpoint, `conn` unchanged, and —
when `requireGate` — the gate open), and any mismatch ends the refresh without a retry:
whatever moved the fence brings its own evidence. A stale or unversioned answer is never
retried. The post-switch refresh uses `requireGate: true`; the WS recovery (§3.3, a frame
whose reconciliation threw) uses `requireGate: false`, because that frame may have been the
one meant to open the gate, and holds the answer to its connection instead (`conn`
unchanged).

One refresh per host at a time: a new `refreshHost` call **replaces** the running one — its
pending retry is cancelled and its in-flight answer dropped. The newer call carries the newer
fences and its fetch is read later, so the older answer cannot be newer than the newer one's.
Entry teardown in the hook (host removed, endpoint changed, unmount) cancels the host's refresh
(`cancelSessionRefresh`); the `conn` fence would also stop it at its next check.

"World generation" = the world-epoch fence value `switch-active.ts` raises on every
exchange (`lib/storage/world-fence.ts`), read at step 2 and again at step 4 — it
changes on a switch in any window. The plan pins the exact accessor.

### 3.3 The WS handler (`hooks/useMultiHostEventWs.ts`)

- `HostEvent` gains optional `epoch?: string; seq?: number`.
- `sessions` frame: `v = parseVersion(event)`.
  - `v === null` → today's behaviour (reconcile), and `held = null`.
  - `decide(hostId, v, {kind: 'ws'})`:
    - `apply` → reconcile, then `note`.
    - `stale` → **never** reconciled (codex #2). Under the contract a new connection
      cannot legitimately deliver one while its gate is closed: `held` can only come from
      this window's earlier connection (read before the new subscribe → smaller seq) or
      from a fetch, which is applied only while the gate is open (§3.2). A stale frame with
      the gate closed is a contract violation; it is dropped and the gate stays closed until
      the connection's next frame.
- Ordering inside callbacks (codex #5): `onClose` first `connectionClosed(hostId)` (conn
  bump) then closes the gate, in the same synchronous callback; `onOpen` bumps conn. §3.2
  reads the gate and captures `conn` in one synchronous step, so a fetch can only be sent
  while the gate is open AND `conn` names the connection that opened it.

### 3.4 Closed sockets (`lib/host-events.ts`) — G5

Audit result: frames of a *superseded* socket are already dropped (`socketEpoch`,
`myEpoch !== socketEpoch`). `close()` does not bump `socketEpoch`, so frames still
queued on a socket that was `close()`d — a host removed from `hostOrder`, or its
endpoint changed (the hook closes the old connection and creates a new one under the
same `hostId`) — are still delivered to `onEvent`. Fix: `close()` bumps `socketEpoch`
(and clears `onclose` like `supersede()` does) so no frame of that socket is handled
after `close()` returns.

### 3.5 Revive snapshots are bound to a world (G6)

`noteReconciledSessions(hostId, sessions)` also records the world-fence value at that
moment; `runRevivePass(hostId)` (both triggers: inside `reconcileHostSessions`, and the
lock-release `runRevivePassAll`) does nothing when the current fence differs. So:
- the switch's own lock release no longer revives the new world from the old list;
- after a switch no revive happens on a host until a list is reconciled for the new world
  (the post-switch fetch, or the host's next WS frame) — "no evidence, no action";
- in OTHER windows the same holds: their snapshot carries the old fence.
The lock-release trigger still uses the newest list held for the current world; making it
fetch would turn a synchronous trigger inside `releaseOperationLock` into an async one —
out of scope, follow-up issue.

### 3.6 Only the switching window fetches (codex #3)

The tab tree is one persisted store shared by every window: the reconciled bindings the
switching window writes reach the others through the same rehydrate that brought them the
new world. The other windows' session lists stay maintained by their own WS; their revive
is fenced by §3.5. A second fetch per window would only duplicate the same writes.

## 4. Compatibility

| daemon | post-switch refresh | WS path |
|---|---|---|
| old (array / no epoch) | no-op (unversioned) — today's behaviour | today's behaviour |
| new | full | ordered by seq; stale frames dropped (gate-closed valve) |

## 5. Acceptance (real machine, :5176, mlab daemon ≥ alpha.423)

A slave world holds a pane on session `S`; switch to master; kill `S` (tmux,
outside Purdex); switch back to the slave → within one round-trip the pane shows
`session-closed`, without any other session change on the host. And: with no
change, switching back and forth makes no binding change (idempotent). Not attached
to any sync profile unless cleared with the coordinator.
