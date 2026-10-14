# Name pool — spec (draft)

> Draft 2026-10-14, 88 (interface line; 1f agreed it is B-line work and will review). User decision the same day (§2). Replaces the two-character ref suffix of a peer's virtual name with a short human name.

## 1. Problem

A virtual name today is `<base>-<ref[1:3]>` (`internal/peers/vname.go:30`): `purdex-3f-vq`, `purdex-88-b8`. Two random characters are hard to tell apart and to remember when several sessions of one project run at once.

## 2. User decisions (2026-10-14, do not reopen)

1. A **name pool**: a pre-built list of **30 short names, mixed male and female**.
2. **At session start, scan all addresses once, take the names in use, pick a random unused one.** It replaces the id suffix: `purdex-3f-adam`.
3. **A relay keeps the name.** A name **frees when the session ends** and can be given again.
4. **No duplicates across hosts.** Keep it very simple.

88's defaults (stated to the user, not objected to): a rare collision (two hosts picking the same name at the same moment) is handled as today's `ambiguous`; when all 30 are taken the old two-character suffix is used; conversations that already have a name keep it.

## 3. The pool

```
adam amy ben cleo dan eva finn gwen hugo iris ivy jack june kai leo
lily max mia nina otto pia rex rosa sam tara tom uma vic wen zoe
```

- Lowercase ASCII, 2–4 letters, no two alike in their first three letters' sound when read aloud in a list; none is 6 characters, so no name can be ref-shaped (`RoutableName`, `internal/peers/ref.go:146`).
- Compiled into the daemon (`internal/peers/namepool.go`); the order does not matter.

## 4. Picking

- **When**: where a conversation is named today — the first sighting in `resolveNames` (`internal/module/peers/vnames.go:77`) of a session with no `peer_names` row and no predecessor name to inherit.
- **In use** = the pool names held by **live** conversations: the local live rows' virtual names, plus the last known live virtual names of every other configured host.
  - The remote half comes from an in-process snapshot of each host's `GET /api/peers` rows, refreshed in the background every 30 s and after each `scope=all` call (no fan-out on the naming path, which runs under a 1 s timeout). A host that does not answer keeps its last snapshot for 5 minutes, then counts as holding nothing.
  - A name is "held" only while its conversation is live; a dead conversation's stored row does not reserve it (decision 3, no deletion needed).
- **Pick**: a uniformly random name from pool − in use. None left → today's `<base>-<ref[1:3]>`.
- **Stored** as today (`peer_names`, `Assign`, first writer wins) with `source = 'pool'`; the virtual name is `<base>-<name>`, the base cut to `64 − 1 − len(name)` bytes as `VirtualName` does today.

## 5. Relays, resumes and collisions

- **Relay**: unchanged path — the successor inherits the predecessor's stored name through the lineage (`resolveNames` lines 109–171, `AdoptLineage`); old refs keep routing (`address.go`, `PreviousRefs`). A pool name is inherited like any other.
- **Resume** of a dead conversation (`claude --resume`, same session id) whose stored pool name is now held by another live conversation: it picks a new pool name (row updated, `source = 'pool'`), so a resumed session never shadows a live one. Its ref and old address forms keep working for its own history; the old name now belongs to the other session.
- **Collision** (two hosts pick the same name within one snapshot window): today's `ambiguous` (`resolveTier`, `address.go:485`) with the candidates; the bracket form `<host>/<name> [<ref>]` and `<host>/_<ref>` still reach each one. No repair.
- **Older daemons**: a host without the pool keeps naming `<base>-<xx>`; uniqueness across hosts holds only when both run it — deploy mlab and air26 together (1f bumps and deploys).

## 6. What changes, what does not

- Changes: `internal/peers` (pool, `VirtualName` with a name suffix), `internal/module/peers/vnames.go` (pick, snapshot, resume rule), the `peer_names.source` vocabulary, `pdx msg` usage text (`cmd/pdx/msg.go:40-60`), the project `CLAUDE.md` 「Peer addresses」 section (format and example), `docs/specs/2026-10-08-peer-mailbox-integration-plan.md` where it states the format, and the tests that pin `[1:3]` (`vname_test.go`, `vnames_test.go:170`, `exec_rows_test.go`, `e2e_test.go:1276`, `msg_test.go`, `lineage_path_test.go`).
- Unchanged: the ref and its routing, the lineage, the bracket check (`name_mismatch`), the team roster / `pdx team` / notification texts (they print the address they are given, so they show the new names without code changes), the SPA and iOS (they treat the address as opaque).
- Existing conversations keep their `<base>-<xx>` names; only new conversations get pool names.

## 7. Tests (each with a mutation)

- A new conversation gets `<base>-<pool name>`; the name is not one a local live row or the remote snapshot holds (M: ignore the snapshot → a held name is picked).
- A dead conversation's name is free again (M: count stored rows → never freed).
- All 30 held → `<base>-<xx>` (M: loop forever / empty suffix).
- A relay successor inherits the pool name (regression).
- A resume whose name is held elsewhere gets a new name; one whose name is free keeps it (M: always keep → two live holders).
- An unreachable host's snapshot expires after 5 minutes.
- No pool name is ref-shaped; every name passes `RoutableName` with a 61-byte base.

## 8. Phase

One PR, daemon only (`internal/peers`, `internal/module/peers`, docs, tests), ≤ 800 lines; codex R1 + R2; reviewed by 1f; deployed to both hosts together.
