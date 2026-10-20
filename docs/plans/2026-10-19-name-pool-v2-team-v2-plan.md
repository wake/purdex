# Name pool v2 + team interface v2 — plan (daemon + Mac)

> B line, 2026-10-19. This plan implements `docs/specs/2026-10-19-name-pool-v2-team-v2-spec.md` (the user's decisions
> are U1–U19 and the lead's are L1–L13). Code facts are from main `7af59cc75`. The contract (§1) is fixed first, so
> that the daemon and the App can be built in parallel. iOS follows in a later plan (U19).

## 0. Decisions taken here (lead)

| # | Decision | Why |
|---|---|---|
| P1 | **The name service is one Go interface**, registered in core by the peers module and used by the team module (§1.3). The team module never touches `peer_names` or the history tables directly. | `internal/peers` must not depend on `internal/team`, and the reverse already holds through core keys (`team.LineageRootKey`, `module.go:565`) |
| P2 | **Photo sources that block CORS** (PlaceCats and PlaceDog do; Picsum allows it, measured 10-19) are fetched by the **Electron main process** through one IPC call. It only accepts `https` URLs from an allowlist of `picsum.photos`, `placecats.com` and `placedog.net`, and caps the response at 2 MB of `image/*`. In other cases the form asks for an uploaded file. | No daemon fetch, so there is no new server-side request surface. The SPA runs only in Electron now (web version withdrawn). |
| P3 | **DiceBear is loaded lazily, with the persona page only.** It uses `@dicebear/core@10.7.0` (MIT, 461 KB unpacked) plus the six styles in use (raw JSON: Lorelei 118 KB, Notionists 373 KB, Open Peeps 253 KB, Micah 29 KB, Shapes 12 KB, Clay 65 KB). It never enters the main chunk. | Bundle size. Everywhere else the App shows the stored raster. |
| P4 | **A persona change is pushed to the App by a host event**, `hostconfig.personas` (`{revision}`). It fires on a local PUT and when a sync from a peer is applied. The App re-reads `GET /api/hostconfig/personas` on that event and on reconnect. | Today the hostconfig PUT emits nothing (`internal/module/hostconfig/handler.go`), and the App reads on demand. Persona sync needs a push. |
| P5 | **NP-0 measures** how a spawned session's first naming pass can learn its spawn op. Two candidates: (a) the op already has `session_id` when the session is first named; (b) the pass reads the pane tag `@pdx_spawn_op` via the owner map (`internal/module/peers/module.go:360,725`). NP-4 builds on whichever holds. | Spec §5.2 leaves this to the plan. Today `nameCandidate` has no pane data (`vnames.go:49-53`). |

## 1. Contract (fixed before any track starts)

### 1.1 Wire

| Item | Shape |
|---|---|
| `GET /api/hostconfig/personas` | `{items: Persona[], stamp: {at, host}, revision}` (spec §4.1 shape) |
| `PUT /api/hostconfig/personas` | `{items, baseRevision}` → 200 `{revision}`. A stale base gives 409 with the current entry (as every collection). A bad item gives 400 `{error, index, field}`. The body cap is 4 MiB. |
| `GET /api/hostconfig` | no longer has `personas` |
| host event `hostconfig.personas` | `{revision}` (P4) |
| envelope `names` (`internal/peers/envelope.go`) | `{familiar: [code]}`, optional, added by NP-3. (The `personas` member of rev 2 is dropped with NP-2, 10-19.) |
| ~~`PUT /api/hostconfig/task-groups` / key `task_groups`~~ **(retired in TG-6)** | `{items: [{id, name, color, roots, created_at, updated_at}], baseRevision}`. The GET comes with the collections. |
| `LeadPayload.lead_names` | `[{code, name, kind: "keep"|"former"|"lead"|"other", last_at}]`. It is always present from a daemon with `team.lead_name.v1`; `[]` means that no persona exists. |
| `Grant.lead_name` | an optional code. Absent means keep the current name. |
| decide error | 409 `{error: "name_taken", lead_names: [...]}` (the refreshed list) |
| `GET /api/team/approvals/{id}/lead-names` | `{lead_names: [...]}`, computed now. 404 for an unknown id; 409 `not_open` for a closed approval (codex #11) |
| person token request (`POST team.PersonRoute`) | optional `lead_name` |
| `SpawnAnswer.pool_name` / team command `spawn.pool_name` | a code, or `""` |
| `GET /api/team/unattended` `log` | `[{at, type: "decision", kind, origin, decided_by: {kind, label}, state: "approved"|"denied"|"timeout"} | {at, type: "toggle", on, by: {kind, label}}]`, newest first, ≤ 30; `at` = `deadline_at` for `timeout`, else `decided_at` (spec §8) |

### 1.2 Capabilities (appended in this order; the exact-list test is in `internal/core/info_handler_test.go:419`)

`personas.v1` (NP-1), `team.max_members.v2` and `team.lead_name.v1` (NP-4), ~~`task_groups.v1` (NP-5)~~ (retired in TG-6),
`team.decision_log.v1` (NP-6). Each one gets the real-module `/api/info` test from the 743 lesson.

### 1.3 Name service (Go, `internal/peers` interface, implemented in `internal/module/peers`)

```go
type NameService interface {
    // Reservations (spec §5.2): in memory, under the pool lock that assignPooled and settlePoolCollisions take,
    // so reserving, releasing and counting held names are one atomic section (codex #2).
    // Rule 2 for the lead's team; "" = nothing free.
    // tmuxName is the op's tmux session name (spawn_ops.tmux_name): the first naming pass finds the reservation by it
    // (NP-0 result, path b′).
    ReserveMember(ctx context.Context, leadSessionID, opID, tmuxName string, at time.Time) (code string, err error)
    // A remote spawn: the lead host chose. ErrNameTaken when the name is held here → the member host uses rule 3.
    ReserveExact(ctx context.Context, code, opID, tmuxName string, at time.Time) error
    ReleaseReservation(opID string) // registered, failed, voided, timed out, remote command expired; idempotent

    LeadCandidates(ctx context.Context, sessionID string) ([]LeadCandidate, error) // rule 1, "keep" first

    // Spec §5.3 order: RenameForLead (meta CAS + alias; ErrNameTaken when held) → team row → RecordLead.
    // UndoRenameForLead reverses a rename whose close failed OR was lost (CAS back, delete the alias).
    RenameForLead(ctx context.Context, sessionID, code string) (oldName string, err error)
    UndoRenameForLead(ctx context.Context, sessionID, oldName string) error
    RecordLead(ctx context.Context, sessionID string) error

    // Pairs by code (codex #3): a spawned member is recorded at registration with the op's pool_name, before the
    // member session is named; adopt resolves both codes from the sessions. No-op unless both are persona codes.
    RecordPairCodes(ctx context.Context, leadCode, memberCode string) error
    CodeOf(ctx context.Context, sessionID string) (code string, ok bool)
}

// Registered by the team module (codex #2): the open spawn ops that carry a pool_name, read once by the
// name service after boot to rebuild its reservations. The peers module never opens team.db.
type SpawnReservationReader interface {
    OpenReservations(ctx context.Context) ([]Reservation, error) // {OpID, Code, TmuxName, CreatedAt}
}
```

- The registry keys are `peers.NameServiceKey` and `team.SpawnReservationsKey`. Both are resolved at use time, so the
  start order of the modules does not matter.
- The team module tolerates the name service's absence (a test build without peers): no reservations, no lead names,
  and today's behaviour.
- The name service tolerates the reader's absence: it then starts with no reservations.

## 2. Phases (one PR each, ≤ 800 lines / 20 files with tests; TDD; codex R1 + R2)

| Phase | Who | Content | Depends on |
|---|---|---|---|
| NP-0 | lead | Measurement only (P5). **Done 10-19**: the result is under「NP-0 measurement」 | — |
| NP-1 | daemon member A | personas key, routes, validation, event, pool registry, picker, `journal.DisplayName` | §1 |
| ~~NP-2~~ | — | **Dropped 10-19 (user):** no daemon-to-daemon persona sync. One source host, and the App copies it to the others (spec §4.3, NA-1d) | — |
| NP-3 | daemon member B | history tables, rules 1–3, aliases and routing, bracket check, backfill, name service (minus spawn), `familiar` in the envelope, `CLAUDE.md` | NP-1 |
| NP-4 | daemon member B | reservations (local and remote), lead names at approval, `/team` parse and card path, adopt pair, cap 9 | NP-0, NP-3 |
| NP-5 | daemon member A | ~~`task_groups` key and capability~~ — retired in TG-6 | §1 |
| NP-6 | daemon member A | decision log, `unattended_events`, `log` field | §1 |
| NA-0 | lead (Opus) | Design: team panel v2 layout on a real screen, persona page, task-group view, deck speech line. A docs PR with screenshots of a prototype, not a vite server on mlab (the 10-17 rule) | — |
| NA-1 | interface member | persona store, `seatOf`, `<Avatar>`, persona page, import, Electron image IPC (P2) | §1, NA-0 (page) |
| NA-1d | interface member | the source host setting (「正本主機」, per device, default the main host), copying on save and catching up on connect by hash, the dropped-code adoption, read-only when the source is offline (spec §4.3) | NA-1a, NA-1b |
| NA-2 | interface member | identity display in the places of spec §9.3 | NA-1 |
| NA-3 | interface member | deck speech starts and header | NA-1 |
| NA-4 | Mac member (olga) | team panel v2 with settings on the panel; close #3187 / #3188 | NA-0, NA-1 |
| NA-5 | interface member | sidebar avatar, home-row toggle split, team icon setting | NA-1 |
| NA-6 | interface member | sidebar task-group view | NA-0, NA-5, §1 |
| NA-7 | Mac member (olga) | lead name card, cap 9 | NA-1, §1 |
| NA-8 | Mac member (olga) | unattended panel v2 | NA-4, §1 |

- **Member allocation** within the B-line cap of 5:
  - Daemon member A is ivan once #3393 lands.
  - Daemon member B is omar once MS2-1 and MS2-2 land.
  - olga takes NA-4, NA-7 and NA-8 after MS2-3 and MS2-4.
  - The interface member is the new fifth member (Sonnet).
- **App phases can merge before their daemon phases**, because they are gated by the capabilities. A fixture per
  contract item lives in `spa/src/lib/names/__fixtures__/`.

### NP-0 measurement (≈ 1 h)

- Use a throwaway tmux server only: `unset TMUX; tmux -L np0 …`, and at the end `tmux -L np0 kill-server` only.
- Start a team with one spawn in a scratch directory. Log the order of: `spawn_ops` `accepted`, then `session_created`,
  then `launched`, then `registered`, then the first `resolveNames` pass that sees the new session id.
- Record whether `spawn_ops.session_id` is set at that pass (P5 a), and whether the owner map gives the pane, so that
  the tag can be read (P5 b).
- Output: a short table appended to §2 of this plan, plus the chosen path.

**Result (2026-10-19, 88; read-only on mlab's live DBs).** The measured spawn is the B-line member spawned at 04:5x,
op `a8e64ed8…`, tmux `tm-a8e64ed82e`.

| Event | Time (ms, from `team.db` / `meta.db`) |
|---|---|
| `spawn_ops.created_at` (accepted) | …939105 |
| `spawn_ops.launched_at` | …939458 |
| **`peer_names.assigned_at` (first naming pass, `pool`)** | **…971939** |
| `spawn_ops.updated_at` (`registered`, `session_id` written) | …972033 |

- **(a) does not hold.** The session was named 94 ms *before* its op recorded the `session_id`, so the first naming
  pass cannot find the op by session id.
- **Chosen path: (b′), by tmux session name instead of the pane tag.**
  - The inventory pass already has each tmux session's name and the owner of its pane, the session id
    (`internal/module/peers/module.go:725-735`, `sessions` and `owners`).
  - Each reservation also carries the op's `tmux_name`, unique per spawn (`tm-<op id prefix>`,
    `spawn_ops.tmux_name`).
  - `nameCandidate` gains `tmuxName`, and the pooled pick first asks the name service for a reservation with that
    tmux name.
  - No tmux call is added to the naming path.
- **Contract change:** `ReserveMember` and `ReserveExact` take the op's `tmuxName` as well (§1.3). The reservation
  reader returns it too.

### NP-1 (`internal/module/hostconfig`, `internal/peers`, `internal/module/peers`, `internal/journal`, `internal/core`)

- `personas.go` in hostconfig:
  - key `personas`;
  - `normalizePersonas` (spec §4.1 checks; the image check decodes the base64 and reads the WebP, PNG or JPEG header
    for the size);
  - a 4 MiB body cap for this route only;
  - the stamp set on PUT;
  - leaving it out of the GET-all;
  - the event (P4).
- `internal/peers/poolreg.go`:
  - `SetPool(recognised map[string]string /*code→display*/, pickable []string)`, held in an `atomic.Pointer`;
  - `PoolNameOf` and a new `PoolDisplayName` read it;
  - the default is the built-in 30, pickable and recognised.
- The peers module subscribes to the personas reader (start and every change) and calls `SetPool`. The picker
  (`namepool.go:128-138`) picks from the pickable list.
- `journal.DisplayName` uses `PoolDisplayName`.

**Tests**: as spec §11 NP-1, plus these:
- The pool registry is race-free (`-race` on `internal/peers`, because it adds an atomic swap).
- `journal.DisplayName("mlab/purdex-3f-hiro")` → "Hiro", the display name (M: capitalise the code).

### NP-2 — dropped 2026-10-19

The user chose one source host copied by the App (spec §4.3) as the interim model, pending a review of all of Purdex's
sync mechanisms. Nothing of NP-2 merged. The text below is kept only as a record of the dropped design.


- The envelope field.
- `namepool_snapshot.go` keeps `names` per host (`fetchHostResult` passes the envelope's `names` on).
- `personasync.go`: after each refresh, it picks the newest stamp. If that stamp is not ours and the hash differs, it
  calls `HostCaller.Call(..., "/api/peers/personas", ...)` and stores the result through a hostconfig method that keeps
  the given stamp (`PutSynced`).
- One sync runs at a time, and at most one fetch per host per refresh.
- The stamp of a local save is `max(now, highest at seen + 1)` (spec §4.3, codex #7). The highest `at` seen is kept in
  memory, updated from each envelope and each store, and taken from the stored stamp at boot.

**Tests**: as spec §11 NP-2, plus these:
- A fetch that fails leaves the local set untouched (M: clear on error).
- The peer route refuses a request without a peer token.

### NP-3 (`internal/store`, `internal/module/peers`, `internal/peers/address.go`, docs)

- Migrations in the meta DB: `name_leads`, `name_pairs`, `peer_name_aliases` (`internal/store/meta.go:67`, one
  `migrateX` each).
- `RenamePool` gains the alias insert in its transaction. `Assign` deletes an alias row for the name it assigns.
- Routing (codex #4):
  - `ResolveSnapshot` gains `Aliases map[string]string` (full alias name → ref).
  - `Resolve` (`address.go:182`) checks it after tier 1 misses (`:321-333`) and goes on with that ref through
    `resolveRefHead`. The combined form's bracket check accepts a name found in the map for that ref.
  - `send.go:451`, the only production caller, fills the map from `PeerNameStore.Aliases()` for a local target, and
    `AliasSessions` beside it (alias → session id, for the combined form). **Read on every local send, not cached**
    (decided in NP-3a: at most a few hundred rows, a send is rare beside a rename, and a cache would need dropping on
    every rename path). A read that fails sets `Partial`, so a miss is `not_ready`.
  - Tests at both levels: `Resolve` (a pure table test) and a send through `send.go` to an alias.
- The rules (spec §6.2) live in `internal/module/peers/namerules.go` as pure functions over `(free, history, personas)`,
  with table tests. `assignPooled` uses rule 3.
- Backfill (spec §6.4) is a pass after `settlePoolCollisions` (`module.go:866`) that renames under the pool lock.
  - A live lead is known from the seat reader (`team.SeatRole`, `internal/team/seat.go:17-21`), through core.
  - Without the team module, everything uses rule 3.
  - It skips a session that is in `relays_active`, through a new `team.RelayGuard` (`InRelay(sessionID)`, registered by the
    team module under `team.RelayGuardKey`): any relay op that is not done, failed or cancelled, with the session as the
    old session or as the new one. A role or relay state that cannot be read leaves the session for the next pass.
  - The role comes from the existing `team.SeatReader`; `Seat.LeadSessionID` is now also filled for a local member (the lead
    of its team), which rule 2 needs to read that lead's pairs.
  - **The notice channel (decided in NP-3c-3).** It sends each renamed session the notice through the peers module's
    in-process `SendSelfNotice` (`internal/module/peers/sender.go`), the sender of the `[pdx team]` notices that skips the
    self-target refusal: from the session's own inbox (its live registry entry) to itself, by ref. It does not need a team
    seat, so it reaches any live session. The text is `renameNoticeFmt` (`namebackfill.go`). It goes out from a goroutine after
    the rename, so the naming pass never waits for the sends; a failure is only logged (the old address keeps working).
- Alias retention (spec §6.3) runs once per hour: lineage dead for 30 days, and a cap of 512 rows.
- **Last task, after NP-2 has merged:** the envelope's `familiar` (the last 64 codes from `name_leads` ∪
  `name_pairs.member_code`), recorded per host by the snapshot, and the union used by rules 2 and 3 (codex #9).
- The name service (§1.3) is implemented, except the reservations, which come in NP-4.
- `CLAUDE.md` 「Peer addresses」: the persona names, aliases and backfill; the sentence 「已經有名字的舊對話維持原名」 is
  replaced.

**Tests**: as spec §11 NP-3, plus these:
- An alias of a relayed session reaches the successor.
- The backfill of a live member uses its lead's pairs.
- The backfill does not run when there are no personas (M: run it → built-in holders churn).

### NP-4 (`internal/module/team`, `internal/team`, `cmd/pdx`, the mod, the skill)

- `spawn_ops.pool_name` (`spawnCols`). `ReserveMember` at `accepted`. The answer carries the name.
- `ReleaseReservation` at every end of an op: registered, failed, voided, timed out, and the expiry of a remote command.
- The team module registers `SpawnReservationReader` (open ops with a `pool_name`) for the boot rebuild.
- The remote `spawn` command carries `pool_name`, and the member host calls `ReserveExact`. `ErrNameTaken` → no
  reservation, and the spawn goes on.
- Naming a spawned session uses the NP-0 path (b′): the inventory's tmux session name matched against the
  reservations' `tmuxName`.
- Pairs (codex #3):
  - a local op reaching `registered` → `RecordPairCodes(lead code, op.pool_name)`;
  - a remote spawn → the same on the lead host, when `FinishRemoteSpawn` records the registration
    (`spawn_remote.go:185-217`);
  - failures are logged.
- `LeadPayload.lead_names` is filled at request creation. `GET /api/team/approvals/{id}/lead-names` answers a fresh
  list (codex #11).
- **Per-approval lock** (codex #1): an in-process keyed mutex held across rename, close and undo. The sweeper's
  `closeExpired` (`sweeper.go:197-207`) takes it for lead approvals.
- On decide with `lead_name`, the order follows spec §5.3. `BecomeLead` is split into `RenameForLead` (meta CAS plus
  alias) and `RecordLead` (history), with `UndoRenameForLead` (CAS back plus deleting the alias) between them:
  1. `RenameForLead`. `ErrNameTaken` → 409 `name_taken`, and the approval stays open.
  2. `CloseLeadApproved`. **An error or a lost CAS (`won = false`, `team_store.go:460-480`)** → `UndoRenameForLead`.
     An error is returned as such; a lost CAS answers as a lost decide does today.
  3. `RecordLead`. A failure is only logged.
  4. The rename notice (spec §6.4).
  - The §1.3 interface is amended to match.
- Person token: `lead_name` is stored on the token and applied at approve.
- Mod `member.js` `parseTeamArgs`: the first word is checked against the persona list. The mod reads it through
  `pdx` (a new `pdx names --json`, listing the active codes and display names, and whether the daemon has
  `team.lead_name.v1`). A match goes into `--as`. No match → the `card` kind, which creates a plain lead approval with
  the person decider.
  - `/team` with no words: a non-lead → the `card` kind; a lead → the roster as today (`register.js:1057`) (codex #5).
  - Fallback (codex #10): when `pdx names` fails (an older `pdx`), or the daemon lacks `team.lead_name.v1`, the parser
    gives today's kinds (`lead` with the words as the note). A mixed-version test runs the mod against a stub `pdx`
    that has no `names` subcommand.
- Skill text: one line on `--as`.
- `RecordPair` on adopt approval.
- Cap 9: every place in spec §5.1, plus the capabilities.
- **Mod change**: needs `go test ./cmd/pdx/...` (the plugin sub-package included) and `pdx setup` on deploy.

**Tests**: as spec §11 NP-4, plus these:
- A remote spawn to an older member host still names the session there (the field is ignored).
- `/team hiro fix the build` → `lead_name` "hiro", note "fix the build".
- `/team fix the build` → the card kind.

### NP-5 (`internal/module/hostconfig`) — retired in TG-6 (`taskgroups.go`, the route and the capability are deleted; see the task groups spec §8)

- `taskgroups.go`: the key, the normaliser (spec §7 limits, one group per root, the name rule `peers.ValidateTitle`),
  the route `PUT /api/hostconfig/task-groups`, and the capability.

**Tests**: as spec §11 NP-5.

### NP-6 (`internal/module/hostconfig`, `internal/module/team`)

- `unattended_events` in `host_config.db` (`internal/module/hostconfig/store.go:44`):
  - The store's `Put` gains an optional in-transaction callback: `PutWith(key, value, base, func(q execQuerier) error)`.
  - `q` is the **same dedicated `*sql.Conn`** that `Put` has under `BEGIN IMMEDIATE` (`store.go:97-151`), behind a
    small interface with `ExecContext` and `QueryRowContext`. It is not a `*sql.Tx`: switching to `db.Begin` would
    turn concurrent CAS writes into `SQLITE_BUSY_SNAPSHOT` instead of 409 (codex #8).
  - A callback error rolls the whole write back.
  - `SetUnattended` (`unattended.go:112`) uses it to insert the event row and trim the table to 100. Its retry on a
    stale entry (`:120-151`) is unchanged.
- A reader is registered in core for the team module.
- `store_unattended.go`: `ListRecentDecisions(limit)` over the kinds in L11 and the states `approved`, `denied` and
  `timeout`, any decider.
  - It orders by `CASE state WHEN 'timeout' THEN deadline_at ELSE decided_at END` desc.
  - It keeps `ListAutoApproved` for the old field.
- `unattended_handler.go` merges both into `log`.

**Tests**: as spec §11 NP-6, plus this:
- The migration on a copy of a live `host_config.db` (`.backup` from mlab) opens cleanly (the 10-14 "deployed schema"
  rule).

### NA-1 (`spa/src/lib/names/`, `spa/src/components/settings/personas/`, `electron/`)

- `usePersonaStore`:
  - keyed per host;
  - read on connect and on `hostconfig.personas`;
  - `current()` = **the source host's collection** (spec §4.3; NA-1d replaces NA-1a's highest-stamp rule), or, with the
    source unreachable, any connected host's copy read-only. Never a union by code (spec §9.2, codex #6).
- `seatOf(addr)` in `lib/names/seat.ts`, as spec §9.2. It replaces the four inline slicers.
- `<Avatar size code fallbackColor>`.
- The persona page:
  - the list;
  - the side form (text fields with the spec limits as counters);
  - the avatar picker: a source dropdown; DiceBear options as raw JSON for now, with a live preview; a photo URL; an
    upload;
  - retire / restore;
  - import `personas.json` (a dry-run summary first: N new, M changed, K skipped);
  - the credits block.
- The save path rasterises to 128 px WebP (canvas `toDataURL('image/webp', 0.85)`, PNG when WebP is unsupported).
- The Electron IPC `avatar:fetch` (P2): allowlist, size cap, content-type check. A test for each refusal.

**Tests**:
- `seatOf` cases.
- The store picks the highest-stamp collection whole: a code that only the older host has is not shown (M: union by
  code).
- The import dry-run.
- A rasterised image under 32 KB for each style (vitest with a canvas stub: assert that the call is made at 128 px; the
  real size is checked once on the App by the lead).
- The IPC allowlist (M: drop the host check).

### NA-0 design (done 2026-10-19, 88, Opus)

- The mock-ups are at https://pages.mlab.host/wake/purdex-b-line/team-v2.html (tailnet only). The page is not in the
  repo, because it reads the user's private `personas.json`.
- The rules below are the contract. The page shows them.
- **Team panel v2 (NA-4)**
  - Each row is a grid: a 44 px avatar spanning lines 1–2, then the body. The light moves to the avatar's bottom-right
    corner (11 px, with a 2 px ring in the row's background).
  - Line 1:
    - the agent icon, kept at 13–14 px, muted, without a light;
    - the host badge;
    - the **name**, 13 px semibold;
    - the title, muted, 11.5 px, the first thing to truncate;
    - the lead / 未開 chips, the context ring and the workbook button, as today.
  - Line 2:
    - a member: the task line;
    - the lead: the lead's own last journal line.
  - Line 3 (the last exchange) follows in the body column; it does not indent under the avatar.
  - **The settings block sits at the panel's bottom, always shown, on the lead's own team only.** It is a top border,
    then three lines at 11.5 px:
    - 「成員上限」 stepper and 「用了 N」;
    - 「自動接力」 stepper | 「成員接力池」 stepper;
    - 「資料夾」 chips (truncated) and a dashed `+`.
    - Bottom rather than top, because members are the content and settings change rarely.
  - Narrow panels (300 px): the title truncates first; the name never does.
  - No persona: initials on the host colour in the avatar's place, same size, so the layout does not move.
- **Sidebar (NA-5, NA-6)**
  - The view toggle is a 30 px square button left of the home button: the profile icon in the workspace view; in the
    task-group view, the team icon (`UsersThree` by default) with a pink outline.
  - Session rows: an 18 px avatar replaces the agent icon, the light is at its top-right corner, then the host badge,
    the **name** (semibold) and the title.
  - Task-group view:
    - a group header has a colour square (the team colour), the name (semibold), a count and a pencil on hover;
    - a manual group's header is a lighter weight and carries the host badge (L9);
    - members hang under the lead with the tree line of today's beads;
    - 「未分組」 is last, after a divider.
    - Double-click a header to rename it.
- **Deck (NA-3, NA-2)**
  - The turn header is `[16 px avatar] **Name** · model · time`.
  - A speech start after a tool row or a thinking line gets `[14 px avatar] Name` in muted text, with no time.
  - The peer caption is `來自 [16 px avatar] **Wei** · 04:12`; the full address is in its tooltip.
- **Lead name card (NA-7)**
  - A 「名字」 row in the lead approval dialog shows pills (a 22 px avatar and the name).
  - 「保留目前的名字（X）」 comes first and is preselected.
  - Then the groups 「當過 lead」 and 「預設 lead」. The rest sits behind 「更多…（N 位）」.
  - A `name_taken` refresh shows a one-line notice above the pills.
- **Persona page (NA-1b)**
  - A 330 px list and a form.
  - List rows: a 30 px avatar, the name (with a crown when `lead`), the code (muted), the source on the right.
    Retired rows are at 45 % opacity, last.
  - Form: a 96 px preview with the source dropdown and 「重畫」, then code (locked once created) / display name,
    sex / nationality, personality, method, interests / 預設擔任 lead. Each field has a counter at the top right.
    Buttons are 退休 and 儲存.
- **Unattended log (NA-8)**
  - Rows: an 18 px avatar, the name (the team name for a lead request), the kind, and a status tag: 自動通過 green,
    你核准 blue, 你拒絕 red, 逾時 yellow. The time is on the right.
  - Toggle events are a full-width pink-tinted row in bold: `無人值守開啟 · 由你 · 04:58`.

### NA-2 … NA-8

- Each one follows spec §9.3–9.7. The places and files are listed there.
- NA-4 keeps the tab-switch regression test (the CLAUDE.md checklist): the settings block is part of a tab-hosted panel.
- NA-6 stores the toggle per device (`localStorage` through the existing device prefs).
- NA-7 reads `GET /api/team/approvals/{id}/lead-names` when the dialog opens, and falls back to the payload's list
  when that read fails. It shows the names with avatars. On `name_taken` it refreshes from the 409 body.
- NA-8 drops the cap column and the folders, and shows `log` with toggles in bold.

## 3. Review, release, tests

- Test tiers as CLAUDE.md:
  - Go and SPA PR tiers go to air26 through `assist-verify.sh` / `assist-run.sh`.
  - Before each PR tier: lucia is asked, and the number of members is reported to her.
- **Daemon PRs**:
  - NP-4 and NP-6 touch the team module, and 1f releases them.
  - NP-1, NP-2, NP-3 and NP-5 touch peers and hostconfig only. 88 sends them to 1f as daemon PRs, by the B-line rule
    that daemon changes go to 1f.
  - 1f deploys.
  - NP-4 changes the mod, so `pdx setup` runs on both hosts.
- **SPA PRs** (NA-1…NA-8): 88 releases them.
- **Migrations**:
  - NP-3 creates the meta DB tables, and NP-6 creates the host_config DB table, with `CREATE TABLE IF NOT EXISTS`.
  - NP-4 adds `spawn_ops.pool_name` by an `ALTER TABLE` migration through `ensureColumn`
    (`internal/module/team/migrate.go:28`). It can be run again, and it has a test that runs it twice.
  - **Before the deploy**, on both hosts: `.backup` copies of `team.db`, `meta.db` and `host_config.db`. 1f's deploy
    script does this. The live DB is checked before deploy (the 10-14 rule).
- **One release**: everything in this plan and message stages step 2 ship in one version on both Macs, then the user
  tests once.

## 4. Out of scope

- iOS (a later plan; iOS shows the new addresses as opaque strings until then).
- Journal persona style (U17).
- Structured journal seats (spec NP-7).
- Monitoring (WWD-7, 1f).
- Mac persistence of unconfirmed messages (message stages L8).
