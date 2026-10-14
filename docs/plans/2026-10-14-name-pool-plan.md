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

- **Opt-in wiring**: the pool is enabled by `WithNamePool(picker)` in `cmd/pdx/main.go`. A `Module` without it names as today, so existing tests that pin the ref suffix stay valid and the diff stays small; new tests cover the pool.
- **"Held" is computed, not stored**: a pool name is held by a live conversation whose virtual name ends in `-<pool name>`. Ref suffixes are 2 characters and pool names 3–5 (spec §3), so the two never mix. Dead conversations hold nothing (spec decision 3) — no deletion, no new column.
- **Sources unchanged**: `source` still says where the base came from; whether the suffix is a pool name is read from the name. No schema change.
- **Remote names from a snapshot**, refreshed in the background, so the naming path (1 s budget) never waits on another host.

## 3. Tasks (TDD, one commit each)

**T1 Pool and name shape** — `internal/peers/namepool.go`:
- `var NamePool = [...]string{…30 names, spec §3…}`.
- `VirtualNameWith(base, suffix string) (string, bool)`: `VirtualName`'s rules with the cut at `64 − 1 − len(suffix)`; `VirtualName(base, ref)` becomes `VirtualNameWith(base, ref[1:3])` after its `IsRef` check (byte-for-byte the same results).
- `PoolNameOf(virtualName string) (string, bool)`: the segment after the last `-` when it is in the pool.
- Tests: every pool name with a 61-character base gives a routable name; no two names share their first three letters; names are 3–5 lowercase ASCII letters; `PoolNameOf` finds `purdex-3f-adam` and not `purdex-3f-vq`, `adam` (no dash), or a base that merely contains a pool word (`anna-tools-k3`); `VirtualName` results unchanged for the existing table (M: cut at `64 − 3` for every suffix → a long base with `freya` exceeds 64).

**T2 Store rename** — `internal/store/peer_name.go`:
- `RenamePool(ctx, sessionID, name string) (PeerNameEntry, error)`: `UPDATE peer_names SET name = ? WHERE session_id = ?` (source and `assigned_at` kept), then the stored row; error when there is no row.
- Tests: the row's name changes and its source / `assigned_at` do not; a missing row errors (M: upsert → a row appears).

**T3 Remote snapshot** — `internal/module/peers/namepool_snapshot.go`:
- Per host id: the set of pool names its live rows hold and when it was read. Filled from every `fetchHostResult` answer that is OK (inside `allEnvelope` and the refresher), each host's rows passed through `normalizeRemoteRows` first.
- Refresher started in `Start`, stopped in `Stop`: every 30 s, `fetchHostResult` for each configured host (the existing timeout). A host not refreshed for 5 minutes holds nothing.
- `remoteHeld(now) map[string]bool`.
- Tests: an OK answer updates; a failed answer keeps the previous set until 5 minutes, then it is empty (M: drop on the first failure → a held name looks free after one timeout); a remote whose rows claim every pool name makes the picker fall back (T4), and nothing else changes (the snapshot only removes candidates); `Stop` ends the goroutine (no leak under `-race`).

**T4 Picking and the resume rule** — `internal/module/peers/vnames.go`:
- `WithNamePool(p NamePicker)`; `NamePicker{ Pick(free []string) string }` (random by default, `math/rand/v2`; a fixed picker in tests).
- The module keeps, from each inventory pass, `lastLive map[lowercase sid]string` (the virtual names it answered). `virtualNamesOf` reads it and does not replace it.
- **Held** for a pass = pool names in `lastLive` ∪ this pass's resolved names so far ∪ `remoteHeld`.
- **New conversation** (no row, no lineage name): `free = NamePool − held`; non-empty → `baseName` with `VirtualNameWith(base, Pick(free))`, the picked name added to this pass's held set; empty → today's `VirtualName(base, ref)` (one log line per pass).
- **Resume rule**: a candidate with a stored pool-name row that is **not in `lastLive`** (it just became live: a resume, or the first pass after a daemon restart), whose pool name is held by another live conversation **outside its lineage** (not one of its `previousRefs`, not a session whose `previousRefs` contain its ref), gets a new pool name: pick as above, `RenamePool`. Applied in the inventory pass only (it owns `lastLive`); `virtualNamesOf` answers the stored name until the next pass (≤ one listing).
- Lineage inheritance and `AdoptLineage` unchanged (a successor inherits the whole name, pool part included).
- Tests: a new conversation gets `<base>-<pool>` not held locally or remotely (M: ignore `remoteHeld`); two new conversations in one pass get different names (M: held set not updated within the pass); all held → `<base>-<xx>` (M: empty suffix / panic); a dead conversation's name is free again (M: count stored rows); resume with the name held elsewhere → renamed, resume with the name free → kept (M: always keep → two live holders; M: always rename → a free name changes); a relay successor inherits the pool name and is not renamed while its predecessor is still live (M: lineage not excluded → the successor flips); a store error in `RenamePool` keeps the old name for this pass and logs.

**T5 Wiring and docs**:
- `cmd/pdx/main.go`: `WithNamePool(randomPicker)` next to `WithPeerNames`.
- `cmd/pdx/msg.go` usage text (lines 40–60): the format is `<base>-<name>` (a name from the pool, or the ref's first two characters when the pool is used up); the example becomes `mlab/purdex-b0-kenji`.
- Project `CLAUDE.md` 「Peer addresses」: the virtual-name format, the pool, the resume rule, the fallback; keep the rest.
- `docs/specs/2026-10-08-peer-mailbox-integration-plan.md`: a dated note where it states `<base>-<ref[1:3]>`.
- Tests that pin the usage text (`cmd/pdx/msg_test.go`) follow.

## 4. Verification

- `go test -race ./internal/peers/... ./internal/module/peers/... ./internal/store/... ./cmd/pdx/...` (the touched packages; the plugin sub-packages of `cmd/pdx` included), `make lint`. Not the full `go test ./...` (1f runs it before the release).
- Mutations listed per task, applied and reverted, results in the PR body.
- codex R1 → R2 attacker → critic (gpt-5.6-sol, low; prompts say not to run the full vitest, `tsc -b` or `go test ./...`).
- 1f reviews before merge; deploy both hosts together (1f).
