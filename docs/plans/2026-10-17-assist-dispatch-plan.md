# Assist dispatch — plan

Spec: `docs/specs/2026-10-17-assist-dispatch-spec.md` (user decisions §1 final). Builds on the host load control plan
(`docs/plans/2026-10-15-host-load-control-plan.md`: GQ queue order, GC conflicts, W3b the watcher).
Coordinator: purdex-1f. X1a / X1b: a new Sonnet member on mlab. X2 / X3: after GQ and GC are merged (lease line order).
X4: B line (lead 88). Test tiers (CLAUDE.md 測試分層) apply to every task: affected packages only, `-race` only where
concurrency changed, `make lint`; the lead runs full suites at release. Every PR: codex R1 + R2 (attack → critic).

## Facts measured (10-17, main ea3effb03)

- Gate directory `~/.config/pdx/gate` (`PDX_GATE_DIR` overrides; `internal/gate/config.go:50`); the gate's settings are
  the `[gate]` table of `~/.config/pdx/config.toml`. `lockFile` and `writeAtomic` (`internal/gate/configedit.go:379,410`)
  are the lock + temp-and-rename helpers `pdx gate set` uses.
- A decision record (`internal/gate/decision.go:28`) has `Session`, `Exit`, `Duration`; it has no host, transfer time or
  reclaim mark (X2 adds them).
- The assist scripts today (mlab repo, `purdex-watch` branch, `watch/assist/`): `assist-on` (anti-sleep launchd agent
  from `~/Assist/launchd.noindex/`, prints host and gate state, read-only folder-trust report), `assist-off`,
  `assist-check` (an inventory of Assist residue outside `~/Assist`; exit 0 none / 1 some / 2 failed). Each runs on mlab
  and pipes itself to `ssh ${ASSIST_HOST:-air26} bash -s`. None has `--json`; none writes a `.closing` mark; mserv has
  only its environment and bench scripts (`watch/assist/mserv/`).
- The watcher: W3b-5 (A line, not started) adds the daemon service `WakeWatcher(reason, detail)`, the wake on the
  unattended switch, and the patrol intervals of spec §7 (awake: present 30 min, unattended tight 10 / steady 15;
  resting: woken every 10 min; disk < 14 GB at once; numbers in the watch settings). **They are not in this plan**;
  X1b calls `WakeWatcher`.
- A notice to a session from its own inbox: the watch module's `peerNotifier` (`internal/module/watch/twoawake.go:289`)
  through `peers.OriginResolverKey` (`InboxOf`, `ResolveOriginBySession`) and `peers.SelfNoticeSenderKey`. A ref resolves
  with `ResolveOriginByRef` (`internal/module/peers/origin_resolver.go:78`), relay lineage included.
- Teams: `teams.id` is the team's stable key; `team_name` / `team_label` are appearance only (editable, may be empty;
  `internal/module/team/migrate.go:87`). `LiveLocalMembersOfLead` (`internal/module/team/pause_store.go:264`) lists a
  lead's live members on this host; `ModTeamRead` (`internal/module/team/modread.go:14`) gives a session's role.
- An admin-only route: `requireAdmin` as in the watch module's `handleStart` (`internal/module/watch/routes_session.go:98`);
  device tokens are refused, and the route is not in `HostRoutePolicy`.

## Contracts decided here (spec §9 and the gaps the spec leaves)

1. **An allowed entry** is a team or a session. A team is stored by its team id; `allow` accepts the id, a team name that
   exactly one live team has (two → refused, both listed), or the lead's address; the name and label at that moment are
   stored beside the id for display. A session is stored by its ref (`_xxxxxx`) and resolved at every pass through
   `ResolveOriginByRef`, so a relayed session stays allowed. A team that has ended matches nothing and is listed as
   `ended`; nothing removes an entry by itself.
2. **The repo of a command** is the basename, without `.git`, of `remote.origin.url` of the command's cwd
   (`git -C <cwd> config --get remote.origin.url`; a worktree answers its main repo's origin); no origin → the basename of
   `git rev-parse --show-toplevel`; not in a repo → no repo, so no assist host. Readiness is per that name
   (`~/Assist/repos.noindex/<name>` on the host).
3. **Who is told and who may ask.** Phase 1 dispatches only the queue of the host whose `assist.json` lists assist hosts
   (mlab; spec §9), so notices go to the allowed team's live sessions **on that host** (its lead when the lead is there,
   and its active members there) and to an allowed session when it is live there. A session on another host runs on its
   own host's gate; `pdx gate hosts` there answers `no assist hosts on <host>`.
4. **The scripts' side** (the watcher's; mlab repo `watch/assist/` and `watch/assist/mserv/`). The daemon runs
   `<scripts>/<name> --json` locally with `ASSIST_HOST=<ssh alias>` (each script keeps sending itself over ssh), with a
   timeout (on 120 s, off 90 s, check 30 s); stdout is one JSON object, stderr free text (written to the daemon log).
   - `assist-on --json` → `{"ok":bool,"switch":"on","detail":"<line>"}`; idempotent; removes `~/Assist/.closing`,
     writes `~/Assist/.on`.
   - `assist-off --apply --json` → writes `~/Assist/.closing` first, then for every `~/Assist/jobs.noindex/<job>.pid`
     writes `<job>.reclaimed` and ends that process group (TERM, KILL after 10 s), removes the anti-sleep agent, then
     removes `.on` and `.closing`; `{"ok":bool,"switch":"off","ended":n,"seconds":s}`. On mserv it ends the
     `systemd-run` scopes named `assist-<job>` instead of process groups.
   - `assist-check --json` → `{"host":"<alias>","assist":bool,"switch":"on|off|closing","repos":{"<name>":
     {"ready":bool,"why":"<line>"}},"residue":n}`. `ready` = the repo is cloned under `repos.noindex`, `assist.env` is
     there and the repo's tools answer, and on air26 the repo directory is trusted (the existing trust report). It also
     removes job leftovers older than 1 h (spec §4.6). Exit 0 when the check ran, whatever it found; 2 when it could not.
5. **Usable** for (host, entry, repo) = the switch asked on in `assist.json` ∧ the last check says `switch: on` ∧ that
   repo `ready` ∧ the check is at most 90 s old ∧ the entry is allowed for that host. Asked on but the host says off
   (someone ran `assist-off` by hand) → not usable, why `the host says off`; nothing re-applies a switch by itself.
6. **Dispatch slots on mlab** (plan decision; spec §2 updated): besides air26's own gate admitting each run, mlab keeps
   per assist host `assist-<host>-<k>` slot files in its gate directory (size per host in `assist.json`, default 2), so
   mlab never has more runs out on a host than that host's size; air26's gate may still make one wait (the person's own
   use of air26 comes first there). mserv's `mserv-<k>` of the spec are these slots for mserv.

## X1a — `assist.json`, switches, the allowed list, readiness, `pdx gate hosts` (one PR)

1. `internal/gate/assist.go`: `AssistFile{Version int; Hosts map[string]AssistHost; Allowed []AssistAllow}`;
   `AssistHost{SSH, Scripts string; Slots int; On bool; By, Reason string; At time.Time}`;
   `AssistAllow{Kind string /* team | session */; ID string /* team id | ref */; Name string /* shown */; Host string
   /* "" = every host */; By, Reason string; At time.Time}`. Path `<gate dir>/assist.json`. Read tolerantly: missing → no
   hosts; malformed → `assist_error`, and no host is usable (a broken file never sends anything). `EditAssist(dir, fn)`
   takes `assist.lock` (`lockFile`), re-reads, calls `fn`, writes with `writeAtomic`; `fn` says changed / unchanged and
   the line to print.
2. The daemon module `internal/module/assist`, registered on every host; with no hosts in `assist.json` it does nothing.
   Routes, all admin only (`requireAdmin`; device tokens 403; not in `HostRoutePolicy`):
   - `GET /api/assist` → the file, the last check of each host (answer and time), `assist_error`.
   - `POST /api/assist/host {alias, ssh, scripts, slots}`, `DELETE /api/assist/host/{alias}` (set up once by the lead or
     the watcher; removing a host whose switch is on is refused, `switch_on`).
   - `POST /api/assist/switch {host, on, reason}`. On: run `assist-on`; record `On` only when it answered ok (else 502
     with its detail, the switch stays off), then run a check at once. Off: record `On=false` **first** (so nothing is
     sent from that moment, spec §5), then run `assist-off --apply`; a failure answers 502 with its detail and the switch
     stays recorded off. One switch operation per host at a time (a second → 409 `busy`). `reason` required (one line,
     ≤ 200 bytes, no control characters).
   - `POST /api/assist/allow {kind, id, host?, reason}`, `POST /api/assist/deny {kind, id, host?}`: resolve by contract
     1, then edit; an entry that is there → `already`, a missing one → `no_such`; same reason rule.
   - `GET /api/assist/hosts?session=<id>&cwd=<dir>` → per host `{host, on, ready, allowed, usable, why}` by contract 5
     for that session and the repo of `cwd` (contract 2).
3. The readiness poller: every 60 s, for each host whose switch is on (and once right after a switch on), run
   `assist-check --json`; keep the answer and its time in memory and write `<gate dir>/assist-ready.json` (temp +
   rename; X2's gate reads it without the daemon). A timeout, exit 2 or bad JSON → that host `ready: false`, why
   `check failed: <line>`.
4. CLI (talks to the local daemon with the admin token, as `pdx watch start` does): `pdx assist [status] [--json]`,
   `pdx assist host add <alias> --ssh <alias> --scripts <dir> [--slots n]` / `remove <alias>`, `pdx assist on|off <host>
   --reason "<line>"`, `pdx assist allow|deny (--team <id|name|address> | --session <ref>) [--host <h>] --reason
   "<line>"`, and `pdx gate hosts [--session <id>] [--json]` (session from the environment as the gate takes it, cwd =
   the current directory). `by` = the session id from the environment, else `person`.
Tests (fake scripts in `testdata/` that print fixed JSON or sleep; no ssh): `assist.json` round trip; malformed → error
and nothing usable; two allows at once both land; allow and deny of one entry at once end as the lock order leaves them
and both print what they found; switch on with the script ok / failing / timing out; switch off is on disk before the
script runs (a fake script that blocks, the file read meanwhile); a second switch of the same host → 409, of another
host → runs; admin only (device token 403); allow by team id, by a unique name, by an ambiguous name (refused, both
listed), by the lead's address; a session allowed by ref still allowed after its relay; readiness: good JSON, bad JSON,
exit 2, timeout, a check older than 90 s, asked on but the host says off, repo not ready (why passed through);
`pdx gate hosts` text and JSON; repo name from an origin URL with and without `.git`, from a worktree, with no origin,
outside a repo. Mutation: the record-off-before-script order, the 90 s staleness, the allowed-for-host check.
Live acceptance (mlab, after deploy, with the scripts' `--json` in place): `pdx assist host add air26 …`, `pdx assist on
air26` → `pdx gate hosts` in a purdex session shows air26 on, ready, not allowed; allow that team → usable; `pdx assist
off air26` → air26's resources back within 60 s (timed), `pdx gate hosts` shows off.

## X1b — notices and waking the watcher (one PR, after X1a and W3b-5)

1. After each poll and right after every switch, allow and deny, the module computes usable (contract 5) for every
   allowed entry × assist host × repo that host lists, and compares it with the last computed set, kept in
   `assist-ready.json` (`notified`), so a daemon restart with nothing changed sends nothing.
2. Recipients (contract 3): a team entry → the team's lead when it is live on this host, and its active members on this
   host (a new read in the team module, `LocalLiveSessionsOfTeam(teamID) []sessionID`, beside `LiveLocalMembersOfLead`);
   a session entry → that session when it is live here. Each recipient gets one notice per pass with one line per
   change: `[輔測] <host> 可用（<repo>）` / `[輔測] <host> 不可用（<repo>）：<why>`. Sent from the recipient's own inbox,
   like the watcher's notices: the body of the watch module's `peerNotifier.Notify` moves to
   `peersmod.NotifySelf(reg, sessionID, text)` and the watch module calls it (a pure move, shown by a byte comparison of
   the moved body).
3. The watcher (decisions 7 and 9): `WakeWatcher("assist_changed", "<host> 輔測 開→關 by <by>：<reason>")` on every
   switch change, and `WakeWatcher("assist_allowed", "<entry> 可用 <host|全部> by <by>：<reason>")` on an allow (the
   watcher prepares that team's environment). The detail is one line ≤ 500 bytes (W3b-5's contract; asked of the A line
   10-17).
4. The watcher's skill (`skills/pdx-watch/SKILL.md`, W3b-3's file) gains: on `assist_changed` to on, prepare the repos
   of the allowed entries on that host (clone or fetch into `~/Assist/repos.noindex`, tools, and when the check says a
   directory is not trusted on air26, ask the person to trust it), then `pdx assist status` until ready; to off, confirm
   with `assist-check` that the resources came back and tell the person; on `assist_allowed`, prepare that entry's repos.
Tests: one notice per change; nothing on an unchanged pass or a restart with nothing changed; a team's members on another
host are not told; an ended team tells nobody; a session entry found by ref after a relay; several changes for one
recipient → one notice; the exact texts; `WakeWatcher` called with reason and detail on on, off and allow, not on deny
or a check (fake service); the moved `NotifySelf` still serves the watch module's stand-down and event notices.

## X2 — the dispatcher with air26 (two PRs; after GQ and GC are merged)

X2a — running on an assist host (mechanics; reached by a hidden `pdx gate --assist-host <h>` used by tests and the lead's
manual checks, not a user-facing choice):
1. Snapshot (spec §4.1): `GIT_INDEX_FILE=<tmp> git add -A`, `git write-tree`, `git commit-tree -p HEAD`; the caller's
   index and tree are never touched.
2. Send and prepare (§4.2–4.3): push `<sha>:refs/assist/<job>` to `air26:~/Assist/repos.noindex/<repo>`, `git worktree add
   --detach`, dependencies keyed by the lockfile hash (`~/Assist/deps.noindex/<repo>/<hash>/node_modules`, linked into
   the job; at most the two most recently used per repo are kept).
3. Run (§4.4): `ssh air26 'source ~/Assist/assist.env && cd <job>/<rel cwd> && echo $$ > ~/Assist/jobs.noindex/<job>.pid
   && printf "\036pdx-assist-start\n" >&2 && exec pdx gate -- <argv>'`, with `PDX_GATE_TAG=assist:<job>` so air26's gate
   shows the run as an assist holder (a `tag` field in `WaiterInfo` and `Holder`, both hosts' gates).
4. Stream and exit (§4.5): stdout / stderr as they come, the start marker taken out; the exit code is the command's.
   SIGINT / SIGTERM / SIGHUP → `ssh air26 kill -TERM -<pgid>` (KILL 10 s later), then the ssh ends.
5. Failures (§4.7): before the start marker (ssh, push, worktree, install) → back in the queue at its place, the host
   unusable for that repo for 5 min (in `assist-ready.json`, reason in the record and on stderr); after it → the
   command's result. A run ended with `<job>.reclaimed` present → `pdx gate: <host> was taken back; waiting again on
   mlab`, back at its place (§5).
6. Back at its place: a gate API `Requeue(name, info)` that makes the ticket again under its original name (names sort by
   arrival time, so the place is kept), in the admit lock.
7. Clean up always (§4.6): `git worktree remove --force`, `git update-ref -d refs/assist/<job>`, the pid and reclaim
   files.
8. The record (§6 history): `host`, `transfer_s`, `run_s`, `exit`, `reclaimed`; `pdx gate history [--host] [--since]
   [--json]`.

X2b — the automatic walk (§3):
1. Capability per host in `assist.json` (`can` / `cannot`: command-word prefixes, GC's `matchesPrefix`), written from the
   assist READMEs by the watcher; a command no assist host can run stays on mlab.
2. Estimates (§3): kind = GC's `commandWords` with the arguments that name files dropped (their count kept); per (kind,
   host) a moving average (α 0.3) of `run_s` and of `transfer_s`, seeded from spec §0, in `<gate dir>/assist-est.json`,
   updated with each record; a `vitest related` estimate scales by its file count.
3. The walk in `Snapshot`, after GQ's order and GC's conflicts: each ticket's candidates = mlab ∪ the assist hosts usable
   for its session and repo (`assist.json` + `assist-ready.json`), whose capability takes its kind, with a free
   `assist-<host>-<k>` slot and no conflict there (a rule names its host, default the host it was measured on). Heavy:
   to the assist host unless mlab's estimate (its would-wait + run) is below 0.5× the host's (run + transfer). Light: out
   only when it would wait on mlab. A ticket that fits no free slot keeps its place; those behind it may take slots it
   cannot use.
4. Status JSON: the pools (mlab heavy / light, each assist host's dispatch slots), each holder with its host, each waiter
   with its candidate hosts; the fleet's queue view (B line) reads it.
Tests (X2a): a snapshot with staged, unstaged and untracked changes equals the working tree, and the caller's index is
unchanged; failure before the marker → requeued at its place, host unusable 5 min; after it → the exit code; reclaim →
requeued at its place with the message; signal → remote group ended; cleanup on success, failure, cancel and reclaim.
(X2b): the walk's choice table (heavy prefers a free assist host; mlab clearly faster keeps it; light only when it would
wait; no capability → mlab; a not-usable host is never a candidate; a head that fits nothing lets later tickets take
slots); estimates update and scale. A fake ssh (a script on PATH that runs the remote command locally in a temp `~/Assist`)
keeps the tests off the network.

## X3 — mserv (one PR, after X2)

Dispatch slots `assist-mserv-<k>` (size 2); right before a start, `ssh mserv 'cat /proc/pressure/memory; grep
MemAvailable /proc/meminfo'` → a candidate only while `some avg10` < 1 and MemAvailable > 4 GB (else not this round; the
ticket stays). Transfer `git archive <sha> | ssh mserv 'mkdir -p … && tar -x -C …'`. Run `sudo systemd-run --scope
--unit=assist-<job> --uid=wake -p CPUQuota=200% -p MemoryMax=4G nice -n 10 ionice -c3 <argv>` after `source
~/Assist/assist.env`. Dependencies `pnpm install --frozen-lockfile` (`--ignore-scripts` where mserv's README says).
Capability from mserv's README (no `go vet ./...`, `go test ./...`, `go build ./cmd/pdx`, cgo or node-gyp builds, browser
tests, swift, macOS-only tests). Clean up `rm -rf` the job directory.
Tests: the admission numbers (just under / over each), the scope command line, archive transfer with a fake ssh, the
capability table.

## X4 — the App panel (B line, lead 88; after X1a for switches and the list, after X2 for the pool and history)

The API is X1a's routes and `GET /api/assist`, the status JSON of X2b.4 and `pdx gate history --json` (X2a.8, served by
a route added with X4 if the App needs it). The panel: one switch per assist host, the allowed list (add with a reason,
remove), the one pool and its queue, the history tab (W5b1's disabled tab, `QueueSection.tsx:47`) and "what was taken
back". The routes are admin only like `POST /api/watch/start`; the App reaches them the same way as that button.

## Order

X1a (new Sonnet member, mlab) → X1b (after W3b-5) → X2a → X2b (after GQ and GC) → X3 → X4 (88; X1a's part may start when
X1a is merged). The scripts' `--json` (contract 4) go to the watcher when X1a starts; X1a merges without them (fake
scripts in tests) and its live acceptance waits for them.
