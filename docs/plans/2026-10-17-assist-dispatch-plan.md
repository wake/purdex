# Assist dispatch — plan

Spec: `docs/specs/2026-10-17-assist-dispatch-spec.md` (user decisions §1 final). Builds on the host load control plan
(`docs/plans/2026-10-15-host-load-control-plan.md`: GQ queue order, GC conflicts — both in that plan on main since #3060 —
and W3b the watcher).
Coordinator: purdex-1f. X1a / X1b: a new Sonnet member on mlab. X2 / X3: after GQ and GC are merged (lease line order).
X4: B line (lead 88). Test tiers (CLAUDE.md 測試分層) apply to every task: affected packages only, `-race` only where
concurrency changed, `make lint`; the lead runs full suites at release. Every PR: codex R1 + R2 (attack → critic).
Codex plan review 10-17 (task-mvbw38xu-x2yqm3) found the first draft's switch and file protocol unsafe across restarts
and writers, and its requeue lossy; contracts 7–11 below answer it.

## Facts measured (10-17, main 734626615)

- Gate directory `~/.config/pdx/gate` (`PDX_GATE_DIR` overrides; `internal/gate/config.go:50`); the gate's settings are
  the `[gate]` table of `~/.config/pdx/config.toml`. `lockFile` and `writeAtomic` (`internal/gate/configedit.go:379,410`)
  are the lock + temp-and-rename helpers `pdx gate set` uses.
- A ticket is made as `<name>.new`, locked, its `WaiterInfo` written, then renamed to its visible name
  (`internal/gate/slots.go:204`); GQ adds the rank sidecar and makes `Ticket.Drop` remove ticket and rank together under
  the admit lock (load plan GQ.1).
- A decision record (`internal/gate/decision.go:28`) has `Session`, `Exit`, `Duration`; it has no host, transfer time or
  reclaim mark (X2 adds them).
- The gate's classifier has no rule for `xcodebuild` or `swift` (#3111): iOS work on air26 runs outside air26's gate.
  On 10-17 12:0x air26's load1 reached 235 on 10 cores from a mutation run (one `xcodebuild test` and a simulator
  restart per mutant, ~8 cores each) beside two assist runs, with memory fine (pressure 1, 69 % free); between two
  simulator boots load1 fell to 16–67 while load5 stayed 50–86 (watcher, 10-17).
- The assist scripts today (mlab repo, `purdex-watch` branch, `watch/assist/`): `assist-on` (anti-sleep launchd agent
  from `~/Assist/launchd.noindex/`, prints host and gate state, read-only folder-trust report), `assist-off`,
  `assist-check` (an inventory of Assist residue outside `~/Assist`; exit 0 none / 1 some / 2 failed). Each runs on mlab
  and pipes itself to `ssh ${ASSIST_HOST:-air26} bash -s`. None has `--json`; none writes a `.closing` mark; mserv has
  only its environment and bench scripts (`watch/assist/mserv/`).
- The watcher: W3b-5 (A line, not started) adds the daemon service `WakeWatcher(reason, detail)` (detail: one line
  ≤ 500 bytes, asked of the A line 10-17), the wake on the unattended switch, and the patrol intervals of spec §7.
  **They are not in this plan**; X1b calls `WakeWatcher`.
- A notice to a session from its own inbox: the watch module's `peerNotifier` (`internal/module/watch/twoawake.go:289`)
  through `peers.OriginResolverKey` (`InboxOf`, `ResolveOriginBySession`) and `peers.SelfNoticeSenderKey`.
- A ref: `ResolveOriginByRef` (`internal/module/peers/origin_resolver.go:78`) answers only the **current** ref; a ref a
  relay replaced is found through the team store's lineage, `Store.PreviousRefs()` (session id → its earlier refs), as
  `membersByRef` does (`internal/module/team/team_handler.go:498`).
- Teams: `teams.id` is the team's stable key; `team_name` / `team_label` are appearance only (editable, may be empty;
  `internal/module/team/migrate.go:87`). `LiveLocalMembersOfLead` (`internal/module/team/pause_store.go:264`) lists a
  lead's live members on this host; `ModTeamRead` (`internal/module/team/modread.go:14`) gives a session's role.
- An admin-only route: the watch module's `requireAdmin` (`internal/module/watch/routes_session.go:37`) is a method on
  that module; its check is `devices.PrincipalFrom(ctx)` (a device token → refused) and `devices.IsAdmin(ctx)`. The
  route is not in `HostRoutePolicy`.

## Contracts decided here (spec §9 and the gaps the spec leaves)

1. **An allowed entry** is a team or a session. A team is stored by its team id; `allow` accepts the id, a team name that
   exactly one live team has (two → refused, both listed), or the lead's address; the name and label at that moment are
   stored beside the id for display. A session is stored by its ref (`_xxxxxx`) and resolved at every pass: the live
   session whose current ref it is (`ResolveOriginByRef`), else the live session whose earlier refs contain it (a new
   team-module registry service `RefSuccessor(ref) (sessionID string, ok bool, err error)` over `PreviousRefs`), so a
   relayed session stays allowed. A team that has ended matches nothing and is listed as `ended`; nothing removes an
   entry by itself.
2. **The repo of a command** is the basename, without `.git`, of `remote.origin.url` of the command's cwd
   (`git -C <cwd> config --get remote.origin.url`; a worktree answers its main repo's origin); no origin → the basename of
   `git rev-parse --show-toplevel`; not in a repo → no repo, so no assist host. Readiness is per that name
   (`~/Assist/repos.noindex/<name>` on the host).
3. **Who is told and who may ask.** Phase 1 dispatches only the queue of the host whose `assist.json` lists assist hosts
   (mlab; spec §9), so notices go to the allowed team's live sessions **on that host** (its lead when the lead is there,
   and its active members there) and to an allowed session when it is live there. A session on another host runs on its
   own host's gate; `pdx gate hosts` there answers `no assist hosts on <host>`.
4. **The scripts' side** (the watcher's; mlab repo `watch/assist/` and `watch/assist/mserv/`). The daemon runs
   `<scripts>/<name> --json` locally with `ASSIST_HOST=<ssh alias>` (each script keeps sending itself over ssh), in its
   own process group, with a timeout (on 120 s, off 90 s, check 30 s; at the timeout the group is ended); stdout is one
   JSON object, stderr free text (written to the daemon log).
   - `assist-on` and `assist-off` take a **host-side switch lock** for their whole run (`mkdir ~/Assist/.switch.lock`
     with the pid and the time inside; a lock whose pid is gone, or that is older than 3 min — longer than either
     script's timeout, so a pid reused after a reboot cannot keep it — is taken over), so two runs on one host — also one
     left by a daemon that died and the one its restart starts — never interleave. Both are idempotent.
   - `assist-on --json` → `{"ok":bool,"switch":"on","detail":"<line>"}`; removes `~/Assist/.closing`, writes
     `~/Assist/.on`.
   - `assist-off --apply --json` → writes `~/Assist/.closing` first, then for every `~/Assist/jobs.noindex/<job>.pid`
     writes `<job>.reclaimed` and ends that process group (TERM, KILL after 10 s), removes the anti-sleep agent, then
     removes `.on` and `.closing`; `{"ok":bool,"switch":"off","ended":n,"seconds":s}`. On mserv it ends the
     `systemd-run` scopes named `assist-<job>` instead of process groups.
   - `assist-check --json` (no lock; read only, but for the sweep) → `{"host":"<alias>","assist":bool,"switch":
     "on|off|closing","repos":{"<name>":{"ready":bool,"why":"<line>"}},"residue":n}`. `ready` = the repo is cloned under
     `repos.noindex`, `assist.env` is there and the repo's tools answer, and on air26 the repo directory is trusted (the
     existing trust report). It also removes job leftovers older than 1 h (spec §4.6). Exit 0 when the check ran,
     whatever it found; 2 when it could not.
5. **Usable** for (host, entry, repo) = the switch asked on in `assist.json` with no switch operation pending ∧ a check
   of the host's **current generation** (contract 7) says `switch: on` ∧ that repo `ready` ∧ that check is at most
   90 s old ∧ the entry is allowed for that host. Asked on but the host says off (someone ran `assist-off` by hand) → not
   usable, why `the host says off`; nothing turns a host on by itself. Asked off but the host says on or closing → the
   daemon runs `assist-off --apply` again (the safe direction: spec §5's 60 s holds even after a lost operation).
6. **Dispatch slots on mlab** (plan decision; spec §2 updated): besides the host's own gate admitting each run (air26),
   mlab keeps per assist host `assist-<host>-<k>` slot files in its gate directory (size per host in `assist.json`,
   default 2), so mlab never has more runs out on a host than that size. Contract 9 says when one is held and let go.
7. **The switch is a persisted operation.** Per host `assist.json` keeps `On` (what was asked), `Gen` and `Op` (`{kind:
   on|off, id, at}` while a switch runs); the file keeps `NextGen`. Every change of a host's record (switch, ssh,
   scripts, slots, capability, limits, add, remove) takes a fresh `Gen` from `NextGen`, which only grows, so a host
   removed and added again never meets its old readiness. A switch: under `assist.lock` refuse when `Op` is set
   (409 `busy` — also across a restart, until the reconciler ends it), else write `On` = asked, `Op`, a new `Gen`; run the
   script; under the lock clear `Op` (on that failed → `On=false`, reason kept). Off is therefore recorded before its
   script runs (nothing is sent from that moment), and on counts only after a check of the new generation says on.
   **One runner per host in the daemon.** Every switch script — a route's and the reconciler's — runs through the
   module's per-host runner: a mutex held from claiming the `Op` to clearing it, and a set of the `Op.ID`s running in
   this process. **The reconciler** runs at daemon start and at every poll: an `Op` whose id is not running here (left
   by a daemon that died) is run again once — after taking the runner and re-reading `assist.json` under the lock to
   confirm the same `Op.ID` is still there (on: `assist-on`, then concluded by its answer; off: `assist-off --apply`);
   asked off with no `Op` but the last check says on or closing → a new off `Op` (contract 5). A route never waits on
   the runner: an `Op` present answers 409 `busy`.
8. **One writer per file.** `assist.json`: the daemon's assist module only (the CLI and the panel go through its API),
   under `assist.lock`. `assist-ready.json` (`{host: {gen, at, answer}}`): the poller only. `assist-notified.json` (X1b):
   the notifier only. `assist-hold/<host>@<repo>.json` and `assist-hold/<host>.json` (`{until, why}`, X2; `@` cannot be
   in a host or repo name, so a pair never shares a file with another pair or with a host's own hold): written by gates (many processes) under
   `assist-hold.lock`, keeping the later `until`. Readers take each file as one atomic read and combine them only through
   `Gen` (a readiness whose `gen` differs from the host's `Gen` counts as no check), so no read order can pair a new
   switch with an old check.
9. **The dispatch slot and the host's own gate.** A host is a candidate only when, read in one ssh right before the send,
   its gate would start this command now (`pdx gate would-wait --auto --json -- <argv>` on air26, the command's own argv
   so its class and air26's conflict rules apply; mserv has no gate) and its
   load is within its limits (contract 10). The mlab gate holds the `assist-<host>-<k>` slot (an ordinary slot lock,
   so the OS lets it go if the gate dies) from the send until the run ends, and lets it go **before** its ticket returns
   to the queue. The remote `pdx gate` runs with a new option `--wait-max 60` (X2a): admitted within 60 s, else it exits
   before running with code 75; it prints `\036pdx-gate-admitted` on stderr right before it starts the command
   (`PDX_GATE_ADMITTED_MARK=1`). Before that mark nothing ran: an exit there (75 or any failure) is a failure before the
   start (spec §4.7; after a 75 the host is held off for 2 min, not 5).
10. **Load limits** (watcher, 10-17): per host in `assist.json` `LoadLimit` (per core; default 1.0), `IdleMin` (CPU idle
    %; default 30), `HoldMin` (default 5). In the same ssh as contract 9: load1 / cores and load5 / cores both at or
    under `LoadLimit` (load1 alone misses periodic work: between simulator boots load1 fell while load5 stayed high),
    and the CPU idle of a ~2 s sample at or above `IdleMin` (a boot not yet in load1) — air26 `sysctl -n vm.loadavg
    hw.ncpu` and `top -l 2 -s 1 -n 0`'s last `CPU usage` line; mserv `/proc/loadavg`, `nproc` and two `/proc/stat` reads
    a second apart, beside its PSI and MemAvailable (X3). Over any limit → not a candidate, and `assist-hold/<host>.json`
    until now + `HoldMin` (the ticket keeps its place). The watcher changes the numbers with `pdx assist host set`.
11. **The schema is whole from X1a.** `AssistHost` carries every field X2 and X3 read (`Slots`, `Can`, `Cannot`,
    `LoadLimit`, `IdleMin`, `HoldMin`), empty or default until they are used, so an edit by X1a's code never drops them;
    capability lives here, not in the gate config (spec §2 updated). A file whose `Version` is newer than the code knows
    is read but never written (edits refused, `assist_version`), so an older daemon cannot drop a newer field.

12. **mlab reserved** (spec decision 11, user 10-17): `AssistFile.Reserved *{By, Reason string; At, Until time.Time}`
    (added with X1a-2, before anything is deployed, so the schema stays whole); `POST /api/assist/reserve {on, reason,
    for}` (admin only; `for` default 2 h, at most 12 h) and `pdx assist reserve on|off` (X1a-4); a reservation past
    its `Until` reads as none. The release scripts call it at their start and clear it in their exit trap. X2b reads it.

## X1a — `assist.json`, switches, the allowed list, readiness, `pdx gate hosts` (one PR)

1. `internal/gate/assist.go`: `AssistFile{Version, NextGen int; Hosts map[string]AssistHost; Allowed []AssistAllow}`;
   `AssistHost{SSH, Scripts string; Slots int; Can, Cannot []string; LoadLimit float64; IdleMin, HoldMin int; On bool;
   Gen int; Op *AssistOp; By, Reason string; At time.Time}`; `AssistOp{Kind, ID string; At time.Time}`;
   `AssistAllow{Kind string /* team | session */; ID string /* team id | ref */; Name string /* shown */; Host string
   /* "" = every host */; By, Reason string; At time.Time}`. Path `<gate dir>/assist.json`. Read tolerantly: missing →
   no hosts; malformed → `assist_error`, and no host is usable (a broken file never sends anything); newer `Version` →
   read only (contract 11). `EditAssist(dir, fn)` takes `assist.lock` (`lockFile`), re-reads, calls `fn`, writes with
   `writeAtomic`; `fn` says changed / unchanged and the line to print. `ReadyFile` read / write for `assist-ready.json`
   (contract 8), and `Usable(file, ready, now, host, entry, repo) (bool, why)` (contract 5) — the one rule every reader
   uses.
2. The daemon module `internal/module/assist`, registered on every host; with no hosts in `assist.json` it does nothing.
   Routes, all admin only (its own `requireAdmin` with the two `devices` checks of the watch module's; device tokens
   403; not in `HostRoutePolicy`):
   - `GET /api/assist` → the file, the last check of each host (answer, gen and time), `assist_error`.
   - `POST /api/assist/host {alias, ssh, scripts, slots?, can?, cannot?, load_limit?, idle_min?, hold_min?}` (add or
     set), `DELETE /api/assist/host/{alias}` (refused while asked on or `Op` set: `switch_on` / `busy`). Set up by the
     lead or the watcher.
   - `POST /api/assist/switch {host, on, reason}` → contract 7; 502 with the script's detail when it fails; 409 `busy`.
     `reason` required (one line, ≤ 200 bytes, no control characters).
   - `POST /api/assist/allow {kind, id, host?, reason}`, `POST /api/assist/deny {kind, id, host?}`: resolve by contract
     1, then edit; an entry that is there → `already`, a missing one → `no_such`; same reason rule.
   - `GET /api/assist/hosts?session=<id>&cwd=<dir>` → per host `{host, on, ready, allowed, usable, why}` by `Usable` for
     that session and the repo of `cwd` (contract 2).
3. The poller and reconciler: at start and every 60 s, the reconciler (contract 7), then `assist-check --json` for each
   host that is asked on, has an `Op`, or whose last check did not say off (and once right after every switch); the
   answers go to `assist-ready.json` with the `Gen` they were run for. A timeout, exit 2 or bad JSON → that host
   `ready: false`, why `check failed: <line>`.
4. The team module's `RefSuccessor` service (contract 1) and its test.
5. CLI (talks to the local daemon with the admin token, as `pdx watch start` does): `pdx assist [status] [--json]`,
   `pdx assist host add|set <alias> [--ssh <alias>] [--scripts <dir>] [--slots n] [--load-limit x] [--idle-min n]
   [--hold-min n]` / `remove <alias>`, `pdx assist on|off <host> --reason "<line>"`, `pdx assist allow|deny (--team
   <id|name|address> | --session <ref>) [--host <h>] --reason "<line>"`, and `pdx gate hosts [--session <id>] [--json]`
   (session from the environment as the gate takes it, cwd = the current directory). `by` = the session id from the
   environment, else `person`.
Tests (fake scripts in `testdata/` that print fixed JSON, sleep, or exit 2; no ssh): `assist.json` round trip with every
field; malformed → error and nothing usable; a newer `Version` → read, edits refused; two allows at once both land;
allow and deny of one entry at once end as the lock order leaves them and both print what they found. Switch: on with
the script ok / failing / timing out (the group ended); off is on disk before its script runs (a fake script that blocks,
the file read meanwhile); a second switch of the same host → 409, of another host → runs; **restart**: a file with an
`Op` on (and with one off) left behind → the reconciler runs that script once and concludes; while it runs a new switch
is 409; a route's switch still running when a poll comes → the reconciler runs nothing (its id is running here), and a
poll between the route's clearing of `Op` and a new opposite switch runs nothing either; a host lock left by a dead pid,
and one older than 3 min with a live unrelated pid, are taken over; asked off with the last check on or closing → off
runs again; asked on with the host off → not usable, nothing
run. Generation: a check of an older gen does not count (switch on, then the old check still in the ready file → not
usable until the new check); a host removed and added again does not meet its old check; changing ssh or scripts takes a
new gen. Admin only (device token 403). Allow by team id, by a unique name, by an ambiguous name (refused, both listed),
by the lead's address; a session allowed by ref still allowed after its relay (through `RefSuccessor`). Readiness: good
JSON, bad JSON, exit 2, timeout, a check older than 90 s, repo not ready (why passed through). `pdx gate hosts` text and
JSON; repo name from an origin URL with and without `.git`, from a worktree, with no origin, outside a repo. Mutation:
off recorded before its script, the `Op` busy check, the gen comparison, the 90 s staleness, the allowed-for-host check.
Live acceptance (mlab, after deploy, with the scripts' contract 4 in place): `pdx assist host add air26 …`, `pdx assist on
air26` → `pdx gate hosts` in a purdex session shows air26 on, ready, not allowed; allow that team → usable; `pdx assist
off air26` → air26's resources back within 60 s (timed), `pdx gate hosts` shows off; the daemon killed during `pdx assist
off air26` → after its restart air26 is off within 60 s.

## X1b — notices and waking the watcher (one PR, after X1a and W3b-5)

1. After each poll and right after every switch, allow and deny, the module computes `Usable` for every allowed entry ×
   assist host × repo that host lists, and compares it with the last computed set in `assist-notified.json` (the
   notifier is its only writer, contract 8; written after the notices are handed over), so a daemon restart with nothing
   changed sends nothing. Passes run one at a time (a module mutex).
2. Recipients (contract 3): a team entry → the team's lead when it is live on this host, and its active members on this
   host (a new team-module read, `LocalLiveSessionsOfTeam(teamID) []sessionID`, beside `LiveLocalMembersOfLead`); a
   session entry → that session when it is live here. Each recipient gets one notice per pass with one line per change:
   `[輔測] <host> 可用（<repo>）` / `[輔測] <host> 不可用（<repo>）：<why>`. Sent from the recipient's own inbox, like the
   watcher's notices: the body of the watch module's `peerNotifier.Notify` moves to `peersmod.NotifySelf(reg,
   sessionID, text)` and the watch module calls it (a pure move, shown by a byte comparison of the moved body).
3. The watcher (decisions 7 and 9): `WakeWatcher("assist_changed", "<host> 輔測 開→關 by <by>：<reason>")` on every
   switch change, and `WakeWatcher("assist_allowed", "<entry> 可用 <host|全部> by <by>：<reason>")` on an allow (the
   watcher prepares that team's environment).
4. The watcher's skill (`skills/pdx-watch/SKILL.md`, W3b-3's file) gains: on `assist_changed` to on, prepare the repos
   of the allowed entries on that host (clone or fetch into `~/Assist/repos.noindex`, tools, and when the check says a
   directory is not trusted on air26, ask the person to trust it), then `pdx assist status` until ready; to off, confirm
   with `assist-check` that the resources came back and tell the person; on `assist_allowed`, prepare that entry's repos;
   the load limits (contract 10) are its numbers to tune.
Tests: one notice per change; nothing on an unchanged pass or a restart with nothing changed; a poll and an allow at once
→ one pass after the other, each change told once; a team's members on another host are not told; an ended team tells
nobody; a session entry found by ref after a relay; several changes for one recipient → one notice; the exact texts;
`WakeWatcher` called with reason and detail on on, off and allow, not on deny or a check (fake service); the moved
`NotifySelf` still serves the watch module's stand-down and event notices.

## X2 — the dispatcher with air26 (two PRs; after GQ and GC are merged)

X2a — running on an assist host (mechanics; reached by a hidden `pdx gate --assist-host <h>` used by tests and the lead's
manual checks, not a user-facing choice):
1. **Out, not dropped.** Serving a ticket to an assist host (in the admit lock) takes the dispatch slot and marks the
   ticket out: a sidecar `<ticket>.out` (`{host, job, at}`), written and removed like GQ's rank sidecar. The ticket keeps
   its name, its flock (held by the same gate process) and its rank. `Snapshot` skips an out ticket (never ahead of
   anyone, never served) and lists it as running on its host; `SetRank` on it is kept for when it comes back.
   **Back at its place** = remove `.out` in the admit lock (no ticket is made again, so the `.new`-then-rename rule and
   GQ's order are untouched); done = `Ticket.Drop` as today, which now removes the `.out` too; a dead gate's ticket is
   cleaned with its rank and out sidecars together (GQ.1's scan).
2. Snapshot (spec §4.1): `GIT_INDEX_FILE=<tmp> git add -A`, `git write-tree`, `git commit-tree -p HEAD`; the caller's
   index and tree are never touched.
3. Send and prepare (§4.2–4.3): push `<sha>:refs/assist/<job>` to `air26:~/Assist/repos.noindex/<repo>`, `git worktree add
   --detach`, dependencies keyed by the lockfile hash (`~/Assist/deps.noindex/<repo>/<hash>/node_modules`, linked into
   the job; at most the two most recently used per repo are kept).
4. Run (§4.4): `ssh air26 'source ~/Assist/assist.env && cd <job>/<rel cwd> && echo $$ > ~/Assist/jobs.noindex/<job>.pid
   && PDX_GATE_ADMITTED_MARK=1 exec pdx gate --wait-max 60 -- <argv>'` with `PDX_GATE_TAG=assist:<job>` so air26's gate
   shows the run as an assist holder (a `tag` field in `WaiterInfo` and `Holder`). The gate side of contract 9
   (`--wait-max`, exit 75, the admitted mark) is part of this PR.
5. Stream and exit (§4.5): stdout / stderr as they come, the mark taken out; the exit code is the command's. SIGINT /
   SIGTERM / SIGHUP → `ssh air26 kill -TERM -<pgid>` (KILL 10 s later), then the ssh ends.
6. Failures (§4.7, contract 9): before the admitted mark (ssh, push, worktree, install, exit 75) → the dispatch slot let
   go, the ticket back at its place, `assist-hold/<host>@<repo>.json` for 5 min (2 min after a 75), the reason in the
   record and on stderr; after it → the command's result. A run ended with `<job>.reclaimed` present → `pdx gate: <host>
   was taken back; waiting again on mlab`, the slot let go, back at its place (§5).
7. Clean up always (§4.6): `git worktree remove --force`, `git update-ref -d refs/assist/<job>`, the pid and reclaim
   files.
8. The record (§6 history): `host`, `transfer_s`, `run_s`, `exit`, `reclaimed`; `pdx gate history [--host] [--since]
   [--json]`.

X2b — the automatic walk (§3, with spec decision 12's host order):
1. Capability per host from `assist.json` (`Can` / `Cannot`: command-word prefixes with GC's `matchesPrefix`), written by
   the watcher from the assist READMEs (`pdx assist host set`); a command no assist host can run stays on mlab. Under
   decision 12 the watcher writes air26's table as the test kinds it runs (vitest, tsc, go test / vet, lint, `pnpm
   check`) and mserv's as the PHP kinds only, empty until its check against mlab — so a non-PHP test never has mserv as
   a candidate, and the code needs no host-specific rule for it.
2. ~~Estimates~~ — **not built** (lead 1f, 10-17): decision 12 chooses by host order, not by expected finish, so nothing
   reads an estimate. The record already keeps `run_s` and `transfer_s` (X2a.8); an estimate can be built from the
   history later if a choice ever needs one.
3. The walk in `Snapshot`, after GQ's order and GC's conflicts: each ticket's candidates = mlab ∪ the assist hosts
   `Usable` for its session and repo, whose capability takes its kind, with a free `assist-<host>-<k>` slot, no
   `assist-hold` for it (the pair's or the host's own), and no conflict there. Conflicts on air26 are air26's own gate's
   rules (its `conflicts.json`, asked through would-wait in contract 9); for mserv, a rule in mlab's `conflicts.json`
   with `host: mserv` against mlab's `assist-mserv-<k>` holders (GC's `Conflict` gains `Host`, empty = the gate's own
   host). **The choice (decision 12)**: a heavy ticket with an assist candidate goes to it — air26 before mserv — even
   when mlab has a free slot; no estimate comparison. A light ticket goes out only when it would wait on mlab, as
   X2b-1 (#3173) built it; decision 12's light point (single-file tests go out too) waits for the user's confirmation,
   and is a one-line change in `PickAssist` once confirmed. Then the one ssh
   of contracts 9–10; a host that fails it (would wait, over its limits, unreachable) is held off and the walk goes on,
   so the ticket takes mlab's free slot or keeps its place until a slot frees on either. While mlab is reserved
   (contract 12) the choice is the same; the reservation only matters for a kind an assist host lists outside decision
   12 (spec §3's rule, kept for those). A ticket that fits no free slot keeps its place; those behind it may take slots
   it cannot use.
4. Status JSON: the pools (mlab heavy / light, each assist host's dispatch slots), each holder with its host, each waiter
   with its candidate hosts, out tickets with their host; the fleet's queue view (B line) reads it.
Tests (X2a): out → back keeps the name and a front / back rank (and `SetRank` while out applies on return); `Snapshot`,
`SetRank` and `Drop` racing out / back under `-race`; a dead gate's out ticket cleaned with its sidecars; a snapshot with
staged, unstaged and untracked changes equals the working tree, and the caller's index is unchanged; air26's gate queued
past 60 s → exit 75, slot let go, back at its place, host held 2 min; failure before the mark → back, held 5 min; after
it → the exit code; reclaim (also while queued on air26) → back with the message; every path lets the dispatch slot go
(success, failure, 75, reclaim, cancel, the gate killed); signal → remote group ended; cleanup on every path. (X2b): the
walk's choice table (heavy goes to a free air26 even when mlab has a free slot; light only when it would wait on mlab,
until decision 12's light point is confirmed; air26 before mserv; no
capability → mlab, and a non-PHP test never goes to mserv; a not-usable host, or one held for the pair or as a host, is
never a candidate; a head that fits nothing lets later tickets take slots); load limits (load1 under but load5 over →
held; idle under `IdleMin` → held; all under → sent). A fake ssh (a script on PATH that runs the remote command locally in a temp `~/Assist`) keeps the tests off
the network.

## X3 — mserv (one PR, after X2)

Dispatch slots `assist-mserv-<k>` (size 2); right before a start, one `ssh mserv` reads PSI, MemAvailable and the load
of contract 10 → a candidate only while `some avg10` < 1, MemAvailable > 4 GB and the load limits hold (else held, the
ticket stays). Transfer `git archive <sha> | ssh mserv 'mkdir -p … && tar -x -C …'`. Run `sudo systemd-run --scope
--unit=assist-<job> --uid=wake -p CPUQuota=200% -p MemoryMax=4G nice -n 10 ionice -c3 <argv>` after `source
~/Assist/assist.env`, printing the admitted mark itself (mserv has no gate). Dependencies `pnpm install
--frozen-lockfile` (`--ignore-scripts` where mserv's README says). Capability from mserv's README (no `go vet ./...`,
`go test ./...`, `go build ./cmd/pdx`, cgo or node-gyp builds, browser tests, swift, macOS-only tests), and under spec
decision 12 the watcher writes only the PHP kinds, after its check against mlab. Clean up `rm -rf`
the job directory.
Tests: the admission numbers (just under / over each), the scope command line, archive transfer with a fake ssh, the
capability table, a `host: mserv` conflict rule.

## X4 — the App panel (B line, lead 88; after X1a for switches and the list, after X2 for the pool and history)

The API is X1a's routes and `GET /api/assist`, the status JSON of X2b.4 and `pdx gate history --json` (X2a.8, served by
a route added with X4 if the App needs it). The panel: one switch per assist host (busy while an `Op` runs), the allowed
list (add with a reason, remove), the one pool and its queue, the history tab (W5b1's disabled tab, `QueueSection.tsx:47`)
and "what was taken back". The routes are admin only like `POST /api/watch/start`; the App reaches them the same way as
that button.

## Order

X1a (new Sonnet member, mlab) → X1b (after W3b-5) → X2a → X2b (after GQ and GC) → X3 → X4 (88; X1a's part may start when
X1a is merged). #3111 (xcodebuild / swift in the gate) on the lease line, independent. The scripts' contract 4 goes to
the watcher when X1a starts; X1a merges without them (fake scripts in tests) and its live acceptance waits for them.
