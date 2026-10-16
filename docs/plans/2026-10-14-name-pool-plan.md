# Name pool — plan (NP-1)

Spec: `docs/specs/2026-10-14-name-pool-spec.md`. One PR, daemon only. Reviewed by 1f (peer addresses are shared by all lines); deployed to mlab and air26 together.
Facts read from origin/main on 2026-10-14 (paths from the repo root).

## 1. Facts

- `internal/peers/vname.go:30` `VirtualName(base, ref)` → `base + "-" + ref[1:3]`; base checked by `vnameBasePattern`, cut to `64 − 3`, trailing `-` trimmed, whole name through `RoutableName` (`internal/peers/ref.go:146`). A virtual name always contains `-`, so it is never ref-shaped.
- `internal/module/peers/vnames.go:77` `resolveNames(ctx, cands)` is the only place a name is first assigned: per candidate, the lineage name (`ByRefs` over `previousRefs`), else the stored row (`Lookup`), else `baseName` (registry name → conversation name → cwd basename, `vnames.go:226`) stored by `Assign` (first writer wins); `AdoptLineage` upgrades a non-lineage row once. Called from the inventory pass (`module.go:826`, every `GET /api/peers`) and from `virtualNamesOf` (`vnames.go:215`: whoami, claim, release, the origin resolver) under `namerTimeout = 1 s`.
- `internal/store/peer_name.go`: `peer_names(session_id PK, ref, name, source, assigned_at)`, never deleted; sources `lineage | registry | conversation_name | dir`; `Assign` refuses an unknown source.
- Remote rows: `allEnvelope` (`module.go:984`) fans out to every configured host through `fetchHostResult` (`module.go:1139`); `normalizeRemoteRows` (`module.go:1050`) settles each row's `Name` (routable, `NameAddressable`) — a remote is treated as attacker-controlled. Only rows pinned to one live entry carry a name (`record.go:~395`); a dead row's address is the ref form.
- Module lifecycle: `internal/module/peers/lifecycle.go:27` `Start`, `:65` `Stop`; wiring in `cmd/pdx/main.go:444` (`WithPeerNames`).
- Tests build `Module` literals without the main wiring; several pin `<base>-<ref[1:3]>` (`internal/peers/vname_test.go`, `internal/module/peers/vnames_test.go:170`, `internal/peers/exec_rows_test.go:46,134`, `internal/module/peers/e2e_test.go:1276`, `cmd/pdx/msg_test.go`, `internal/module/team/lineage_path_test.go`).

## 2. Design choices (for the reviewer)

- **Opt-in wiring**: the pool is enabled by `WithNamePool(picker)` in `cmd/pdx/main.go`. A `Module` without it names as today, so tests built on `Module` literals that pin the ref suffix stay valid; T5 sorts every pinned test into "literal, unchanged" or "real wiring, pool expectation".
- **"Held" is computed, not stored**: a pool name is held by a live conversation whose virtual name ends in `-<pool name>`. Ref suffixes are 2 characters and pool names 3–5 (spec §3), so the two never mix. Dead conversations hold nothing (spec decision 3) — no deletion, no new column.
- **One rule for resumes and collisions — the newcomer yields** (codex plan review, replacing a "not live last pass" test that missed a die-and-resume between passes): when two live conversations outside one lineage hold the same pool name, the one whose **process started later** picks a new name. A resume is a new process, so it always yields to the live holder; two hosts that picked the same name in one snapshot window settle the same way. Process start = the registry entry's `ProcStart` (local, `internal/peers/registry.go:58`) or the remote row's `proc_start` (`internal/peers/record.go:54`), parsed as `Mon Jan _2 15:04:05 2006` in local time; unparsable → the time this daemon first saw it live (in memory).
- **Serialised naming**: picking and storing a pool name, and the newcomer check, run under one module mutex (`poolMu`), reading the live local sessions and their stored names inside it, so concurrent passes (`GET /api/peers`, whoami, claim, release, the origin resolver) never hand one name to two conversations.
- **Source**: a pool name is stored with `source = 'pool'` (spec §4); a rename keeps `lineage` on a lineage row (a lineage row stays final) and writes `pool` on any other.
- **Remote names from a snapshot**, refreshed in the background, so the naming path (1 s budget) never waits on another host; only names that survive `normalizeRemoteRows` on live, name-addressable rows count.

## 3. Tasks (TDD, one commit each)

**T1 Pool and name shape** — `internal/peers/namepool.go`:
- `var NamePool = [...]string{…30 names, spec §3…}`.
- `VirtualNameWith(base, suffix string) (string, bool)`: `VirtualName`'s rules with the cut at `64 − 1 − len(suffix)`; `VirtualName(base, ref)` becomes `VirtualNameWith(base, ref[1:3])` after its `IsRef` check (byte-for-byte the same results).
- `PoolNameOf(virtualName string) (string, bool)`: the segment after the last `-` when it is in the pool.
- Tests: every pool name with a 61-character base gives a routable name; no two names share their first three letters; names are 3–5 lowercase ASCII letters; `PoolNameOf` finds `purdex-3f-adam` and not `purdex-3f-vq`, `adam` (no dash), or a base that merely contains a pool word (`anna-tools-k3`); `VirtualName` results unchanged for the existing table (M: cut at `64 − 3` for every suffix → a long base with `freya` exceeds 64).

**T2 Store** — `internal/store/peer_name.go`:
- `PeerNameSourcePool = "pool"`, accepted by `Assign`.
- `RenamePool(ctx, sessionID, oldName, newName string) (PeerNameEntry, bool, error)`: one statement `UPDATE peer_names SET name = ?, source = CASE source WHEN 'lineage' THEN 'lineage' ELSE 'pool' END WHERE session_id = ? AND name = ?` (compare-and-set on the old name, so an `AdoptLineage` or another rename that landed first wins), then the stored row; `false` when nothing matched.
- Tests: the name changes, `assigned_at` does not, a lineage row stays `lineage`, any other becomes `pool`; a stale `oldName` (another write landed first) changes nothing and answers `false` (M: drop the `AND name = ?` → a lineage name is overwritten); a missing row answers `false`.

**T3 Remote snapshot** — `internal/module/peers/namepool_snapshot.go`:
- Per host id: the set of pool names its live rows hold and when it was read. Filled from every `fetchHostResult` answer that is OK (inside `allEnvelope` and the refresher), each host's rows passed through `normalizeRemoteRows` first.
- Refresher started in `Start`, stopped in `Stop`: every 30 s, `fetchHostResult` for each configured host (the existing timeout). A host not refreshed for 5 minutes holds nothing.
- `remoteHeld(now) map[string]bool`.
- Each held name keeps its holder's process start (the row's `proc_start`, else the first time the snapshot saw it).
- Tests: an OK answer updates; a failed answer keeps the previous set until 5 minutes, then it is empty (M: drop on the first failure → a held name looks free after one timeout); rows that must not hold a name — a dead row carrying a `Name`, a row that is not name-addressable, an unroutable name, a pre-v5 registry name, a row whose name `normalizeRemoteRows` clears (M: read the raw envelope → a forged row holds a name); a remote whose rows claim every pool name makes the picker fall back (T4), and nothing else changes (the snapshot only removes candidates); `Stop` ends the goroutine (no leak under `-race`).

**T4 Picking and the newcomer rule** — `internal/module/peers/vnames.go`, `namepool.go`:
- `WithNamePool(p NamePicker)`; `NamePicker{ Pick(free []string) string }` (random by default, `math/rand/v2`; a fixed picker in tests).
- Under `poolMu`, for a pass that has a candidate needing a pool decision:
  - **Live local holders**: the local live registry entries (read inside the lock), their stored rows (`Lookup`), their process starts.
  - **Held** = their pool names ∪ the remote snapshot's ∪ names picked earlier in this critical section.
  - **New conversation** (no row, no lineage name): `free = NamePool − held`; non-empty → `VirtualNameWith(base, Pick(free))` stored by `Assign` with `source = 'pool'`; empty → today's `VirtualName(base, ref)` (one log line per pass).
  - **Newcomer rule**: for each local live candidate holding a pool name that another live conversation outside its lineage also holds (local or remote; lineage = its `previousRefs`, or a session whose `previousRefs` contain its ref): if its process started later than that holder's, pick a new name as above and `RenamePool(sid, old, new)`; a `false` answer (the row changed meanwhile) keeps the stored row for this pass. Equal starts → the larger session id yields.
- Lineage inheritance and `AdoptLineage` unchanged (a successor inherits the whole name, pool part included).
- Tests:
  - a new conversation gets `<base>-<pool>` not held locally or remotely (M: ignore the snapshot);
  - two new conversations in one pass get different names, and so do two **concurrent** passes (two goroutines calling `resolveNames` and `virtualNamesOf` at once, `-race`; M: drop `poolMu` → a duplicate shows up under a fixed picker);
  - all held → `<base>-<xx>` (M: empty suffix / panic);
  - a dead conversation's name is free again (M: count stored rows);
  - resume: the resumed session (later process start) yields to the live holder; the holder keeps its name; a resumed session whose name is free keeps it (M: always keep → two live holders; M: older yields → the holder flips);
  - die-and-resume between two passes is caught (no dependency on the previous pass; regression for the codex finding);
  - restart fixture: a store with rows, a fresh `Module`, two live sessions sharing a pool name with different starts → only the later one renames; a lineage pair sharing a name → neither renames;
  - a cross-host collision: the local conversation yields only when the remote holder started earlier;
  - `RenamePool` answering `false` keeps the stored name and logs; a store error keeps the old name for this pass.

**T5 Wiring and docs**:
- `cmd/pdx/main.go`: `WithNamePool(randomPicker)` next to `WithPeerNames`.
- `cmd/pdx/msg.go` usage text (lines 40–60): the format is `<base>-<name>` (a name from the pool, or the ref's first two characters when the pool is used up); the example becomes `mlab/purdex-b0-kenji`.
- Project `CLAUDE.md` 「Peer addresses」: the virtual-name format, the pool, the resume rule, the fallback; keep the rest.
- `docs/specs/2026-10-08-peer-mailbox-integration-plan.md`: a dated note where it states `<base>-<ref[1:3]>`.
- **Pinned tests**: list every test that pins `<base>-<ref[1:3]>` (grep `[1:3]`, `RefID(` + `-`, and literal `-xx` names in `internal/peers`, `internal/module/peers`, `internal/module/team`, `cmd/pdx`), and in the PR body sort each into "`Module` literal, unchanged" or "real wiring, now a pool name (fixed picker)"; update the second group and the usage-text test (`cmd/pdx/msg_test.go`).

## 4. Verification

- `go test -race ./internal/peers/... ./internal/module/peers/... ./internal/store/... ./cmd/pdx/...` (the touched packages; the plugin sub-packages of `cmd/pdx` included), `make lint`. Not the full `go test ./...` (1f runs it before the release).
- Mutations listed per task, applied and reverted, results in the PR body.
- codex R1 → R2 attacker → critic (gpt-5.6-sol, low; prompts say not to run the full vitest, `tsc -b` or `go test ./...`).
- 1f reviews before merge; deploy both hosts together (1f).
