# Assist hosts: one pool, one queue, dispatched to mlab, air26 and mserv (spec)

Status: draft (lead 1f, 2026-10-17). Builds on the host load control spec
(`docs/specs/2026-10-15-host-load-control-spec.md`: the gate §5, queue order §5.2, conflicts §5.3, the watcher §12).

## 0. Why

On 10-16/17 every heavy command of every line queued on mlab: `vitest related` took 55–90 min, the release suite about
60 min, and the heavy queue reached 13 commands with waits up to 2 h, while air26 and mserv sat idle (the host cells showed
the whole queue on mlab). The watcher's member measured the same commit on the other hosts:

| Command | mlab (loaded, 10-16 night) | air26 (M5, 24 GB) | mserv (N150 4 cores, Linux) |
|---|---|---|---|
| spa `vitest run` (983 files) | ~50 min in the release gate | 308 s | 858 s |
| spa `tsc -b --force` | minutes, 1.2 GB node | 10 s | 36 s |
| `go test ./...` | ~10 min (`-p 3`) | 129 s | Linux: 15 packages do not build (#3076) |
| `go test ./internal/module/team/` | 666 s (`-race` subset 962 s) | 78 s (`-race` 88 s) | — |

Assist environments (`~/Assist` on air26 and on mserv: own toolchains, caches and deny rules, nothing outside it) and the
scripts `assist-on` / `assist-off` / `assist-check` exist as a prototype (mlab repo, `purdex-watch` branch,
`watch/assist/`). Turning air26's assist off gave its resources back in 7.2–8.4 s (measured).

## 1. User decisions (2026-10-17, do not reopen)

1. **Assist is for test runs only.** No development session is ever started in an Assist directory; work is sent there
   to be run and comes back.
2. **One pool, one queue.** The gate's slots of mlab, air26 and mserv are one pool and the gate's queue is one queue,
   seen together; work is dispatched by capability, load and the on/off switches. Assist is part of the gate's admission,
   not a separate system.
3. **Dynamic.** Work waits in the one queue and is taken by whichever host can start it and finish it soonest — mserv is
   slow, but when mlab is congested it still finishes sooner than waiting on mlab.
4. **Automatic.** When the conditions hold the gate sends the command by itself; the caller writes the command as always,
   and the output says where it ran.
5. **mlab serves the quality of the development sessions first**; light commands that clog it may be sent too, when assist
   is on.
6. **Each assist host has its own switch** (air26 and mserv), and **nothing is sent to a host whose switch is off** —
   mserv included (user, 10-17). Turning one off gives its resources back within 60 s.
7. **Who may use assist**: a team or a session that is allowed. Until the App panel exists the **watcher assigns** it with
   a command; the panel later reads and writes the same list. A team may use a host when (a) that host's assist is on,
   (b) the team or session is allowed, and (c) the watcher has prepared the repo's environment on that host (including the
   folder trust on air26). The watcher prepares the environment **proactively**: when assist turns on, or when a team is
   allowed.
8. **Every team or session can see which test hosts are available** (`mlab` / `air26 assist` / `mserv assist`), by a
   command, and is **notified when that changes**.
9. **The watcher is woken** when an assist switch turns on or off, and when unattended mode turns on or off. While nobody
   is present the watcher works on shorter intervals (§7).
10. **The user-visible behaviour of §3–§6 is confirmed** (10-17): the `running on <host> (assist)` line; a taken-back
    run goes back to its place in the queue; a heavy ticket prefers a free assist host unless mlab is clearly faster;
    a light one goes out only when it would wait; the allowed list starts empty; one notice per change to the
    sessions of an allowed team; mserv's limits as its admin set them.

11. **While mlab packages or releases, the other lines' tests go to assist** (user, 10-17 13:3x, through the watcher):
    the release has mlab to itself. While the lead has declared mlab reserved (§3), every command that an assist host
    can take goes there first, without the usual preference for mlab; it ends by itself when the reservation ends.

12. **Which host runs a test** (user, 10-17, through the watcher: 13:4x "compatible tests go to air26 first", 14:0x
    "開 A" for mserv; replaces for these tests the estimate comparison of decisions 3 and 5):
    - **Order air26 → mserv → mlab.** While air26's assist switch is on, every heavy test air26 can run goes to air26
      whenever it can start it, even when mlab has a free slot and would be faster. Compatible with air26 = the repo is
      in air26's `~/Assist/repos.noindex` (purdex, aigora-v2) and the kind is one air26 runs (vitest, tsc, go test / vet,
      lint, `pnpm check`). When air26 cannot take it (full, its own iOS work at full CPU, being taken back) an
      **A-grade** test goes to mserv; anything else, or when mserv cannot take it either, runs on mlab, or keeps its place
      until a slot frees. A repo not in air26's Assist (at-inwin) goes mserv → mlab.
    - **Light tests (single-file tests during development): pending the user's confirmation.** The 13:4x wording
      includes them ("開發中途的單檔測試也包括在內"), which would replace decision 10's "a light one goes out only when
      it would wait"; until the user confirms that change, decision 10 holds for light tickets.
    - **mserv takes the A grade only**: each kind is opened after the watcher has run it on mserv and on a reference host
      at the same commit and found the same results (reference air26 for purdex and aigora-v2, mlab for at-inwin). The
      list is the watcher's, in mserv's README ("已比對、已開放的 A 級"), and the watcher writes it into mserv's
      capability (§2). As of 10-17 16:5x: aigora-v2 tsc and vitest (root, ui, contract); purdex SPA `tsc --noEmit -p
      tsconfig.app.json` and vitest related / small runs (the full SPA suite is B grade: 25 min holding both of mserv's
      cores, not sent); at-inwin PHP unit tests and Jest. at-inwin's `test-wp.sh` (a real WordPress and MySQL) stays on
      mlab. (13:43's "mserv: PHP only, not before its check" was the first step of this; the PHP check passed at 16:5x
      after the at-inwin autoload fix.)
    - **air26's own development comes first** (lead 1f, from §2: air26's gate "also protects the person's own use of
      air26"): air26's `~/Assist/assist.env` sets `PDX_GATE_RANK=back` (#3158), so a run sent there waits behind
      air26's own commands in air26's gate. While air26 is busy (iOS work at full CPU) the watcher may lower its slots
      or take them back (§5); the tests then run on mlab.

## 2. The pool

| Host | Its slots are kept by | Runs | Limits |
|---|---|---|---|
| mlab | mlab's gate (today) | everything the gate runs | the gate's admission (§5 of the load spec) |
| air26 | **air26's own gate** (`pdx gate` in `~/Assist`, which also protects the person's own use of air26), and mlab's dispatch slots `assist-air26-<k>` (how many runs mlab has out there; plan contract 6) | purdex and aigora-v2 test runs: SPA vitest / tsc, Go tests, lint, aigora `pnpm check` | air26's gate slots and admission; sent only while its gate would start the command at once and its load is within the host's limits (load1 and load5 per core, CPU idle of a 2 s sample; plan contracts 9–10) |
| mserv | **mlab's gate** (mserv has no pdx; its slots are held on mlab, `assist-mserv-<k>` lock files in mlab's gate directory) | **the A grade only** (decision 12): the kinds the watcher has checked against a reference host at the same commit — aigora-v2 tsc / vitest, purdex SPA tsc and vitest related, at-inwin PHP (7.4, 8.0 cli, 8.5) unit tests and Jest — taken when air26 cannot take them. Not the full SPA suite (B grade), not the Go packages, not at-inwin's `test-wp.sh`. MySQL dev on `100.64.0.6:3307`. | only while mserv's assist switch is on; 2 parallel; each run wrapped in `sudo systemd-run --scope --uid=wake -p CPUQuota=200% -p MemoryMax=4G nice -n 10 ionice -c3` (mserv admin 9e-f0); admitted only while PSI memory `some10` < 1, MemAvailable > 4 GB and the load limits hold (read over ssh right before the start) |

- **Capability** is a table per host of the command kinds it can run (by tool and words, like the intercept rules), kept
  in `assist.json` beside the host (plan contract 11) and written by the watcher from the assist READMEs: e.g. mserv cannot run `go vet ./...`, `go test ./...`,
  `go build ./cmd/pdx` (#3076), cgo or node-gyp builds, browser tests, swift, macOS-only tests. Under decision 12 the
  watcher writes mserv's table as the A-grade kinds only (decision 12), each added after its check. A command no host but mlab
  can run stays on mlab. **#3076 is not needed for this spec**: nothing runs pdx on mserv.
- **Readiness** per (host, repo) comes from that host's `assist-check --json` (run by the dispatcher over ssh, cached for
  60 s): Assist present, the switch on, not `.closing`, the repo cloned and its toolchain ready, and on air26 the folder
  trust accepted for the repo.

## 3. Dispatch

- **The queue** is mlab's gate queue as today: arrival order with the ranks of §5.2 (front / normal / back / hold) and the
  conflict rules of §5.3 (air26's rules are its own gate's; a rule for mserv lives in mlab's file and names `host: mserv`).
- **When a slot frees anywhere** (or a waiting gate polls), the dispatcher walks the serving order from the head and gives
  each free slot the first ticket that (a) may run on that host (allowed team or session, host ready for its repo, kind
  in the host's capability, no conflict there) and (b) **finishes soonest there**: the host's estimated run time for that
  kind plus the transfer (§4), compared with "wait on mlab, then run on mlab". A ticket that fits no free slot keeps its
  place; tickets behind it may take slots it cannot use (as with conflicts), so a mserv-only slot never waits behind a
  macOS-only head.
- **mlab reserved (decision 11).** `pdx assist reserve on --reason "<line>" [--for <dur>]` (default 2 h) / `off`,
  recorded in `assist.json`; the lead's release scripts set it at their start and clear it at their end, and it lapses
  at its `until` if a script is cut off. While it holds, a ticket with a usable assist candidate goes there whatever the
  two rules below say; a ticket no assist host can take still runs on mlab (nothing is refused).
- **Preference for mlab's quality (decision 5).** With an assist host free and the ticket able to run there, a heavy
  ticket goes to the assist host even when mlab has a free slot, unless mlab would finish it clearly sooner (estimate
  below 0.5× the assist host's). A light ticket goes out only when it would otherwise wait on mlab. **Decision 12
  replaces the heavy half for tests**: a heavy test air26 can start goes there with no estimate comparison, and an
  A-grade test air26 cannot take goes to mserv the same way. The light half stays until the user confirms decision 12's
  light point. The rule stays whole for any other kind an assist host's table may list later.
- **Estimates** are per (kind, host): the moving average of the decision records' run times (the gate already writes
  one per job), seeded from the table in §0; the transfer is measured per host the same way. A kind is the command's
  words after the runner words (§5.3's `commandWords`), with paths and file lists dropped (`vitest related <files>` is one
  kind; its time grows with the file count, so the record keeps the count and the estimate scales by it).
- **The caller** sees one line when its command leaves: `pdx gate: running on air26 (assist): <command>` (or mserv), then
  the command's own output, then its exit code. Nothing else changes for it: the same `pdx gate` process waits, streams
  and exits.

## 4. Running on an assist host

1. **Snapshot without touching the caller's tree.** A temporary index: `git add -A` into it, `write-tree`,
   `commit-tree -p HEAD` → a commit with the working tree as it is (uncommitted and untracked files included; ignored
   files such as `node_modules` left out).
2. **Send it.** air26: `git push air26:~/Assist/repos.noindex/<repo> <sha>:refs/assist/<job>`, then on air26
   `git worktree add --detach ~/Assist/tmp.noindex/<job> <sha>`. mserv (no git credentials there, by its admin's rule):
   `git archive <sha> | ssh mserv 'tar -x -C ~/Assist/tmp.noindex/<job>'`.
3. **Dependencies from the host's own caches.** SPA: `pnpm install --frozen-lockfile` from the Assist store when the
   lockfile differs from the last install for that repo (a per-repo `node_modules` keyed by the lockfile hash, linked
   into the job directory; mserv adds `--ignore-scripts` where its README says so). Go: the Assist module cache (mserv's
   private modules copied from air26 by the watcher's preparation).
4. **Run.** air26: `ssh air26 'source ~/Assist/assist.env && cd <job>/<rel cwd> && exec pdx gate -- <command>'` — air26's
   own gate admits it. mserv: mlab holds a `assist-mserv-<k>` slot, checks PSI and MemAvailable, then `ssh mserv 'source
   ~/Assist/assist.env && cd <job>/<rel cwd> && exec sudo systemd-run --scope … <command>'`. The relative cwd is the
   caller's cwd under its repo root.
5. **Stream and exit.** stdout and stderr come back over ssh as they are written; the exit code is the command's. SIGINT,
   SIGTERM and SIGHUP to the mlab gate end the remote process group (the job's pid file on the host), then the ssh.
6. **Clean up always** (success, failure, cancel, reclaim): remove the job worktree or directory and the `refs/assist/<job>`
   ref; a sweep at each `assist-check` removes leftovers older than 1 h.
7. **Failures before the command starts** (ssh, push, archive, install, readiness changed) put the ticket back at its
   place in the queue and mark that host unusable for that repo for 5 min, with the reason in the decision record and on
   stderr. **A failure of the command itself is its result**: a red test on air26 is red; the gate never reruns it
   elsewhere to get another answer.

## 5. Turning a host off (≤ 60 s)

`assist-off` on a host (by the switch, §6; air26 and mserv alike) marks it `.closing`: the dispatcher stops sending at once (readiness is
re-read before every send). The host's assist jobs are ended (`assist-off` ends the process groups it owns — on mserv the
`systemd-run` scopes of the assist jobs; measured 7–8 s on air26). The mlab gate of each such job sees the run end with the reclaim marker, prints `pdx gate: air26 was taken back;
waiting again on mlab`, and **puts its ticket back at its original place** (same ticket name and arrival time) — the
command runs again wherever the pool can take it. A command's partial output from the reclaimed run is left as printed.

## 6. Switches, the allowed list, and who sees what

- `pdx assist on|off <host>` (on mlab) runs that host's `assist-on` / `assist-off --apply` over ssh and records the switch
  in mlab's gate directory (`assist.json`: per host, on/off, by, at, reason). The App panel (phase X4) calls the same.
- `pdx assist allow|deny <team|session> [--host <host>] --reason "<line>"` edits the allowed list in `assist.json` (under
  a lock, re-read, atomic replace). Per decision 7 the watcher runs these until the panel exists; the panel then edits the
  same list. Default: empty (nobody), so turning a switch on sends nothing until a team is allowed.
- `pdx gate hosts [--json]` answers, for the caller's session (or `--session`): each host, whether it is on, ready for
  this repo, and allowed for this session, and why not when it is not.
- **Notices.** When a host becomes usable or unusable for an allowed team (switch, readiness, allowed list), each live
  session of that team **on the dispatching host** (mlab in phase 1: a session elsewhere runs on its own host's gate)
  gets one notice (`[輔測] air26 可用（purdex）` / `…不可用：<reason>`), at most one per change. A team is allowed by its
  team id (a name or the lead's address resolves to it), a session by its ref (relays keep it); plan contracts 1–3.
- **History (user, 10-17).** Every dispatched job's decision record (written on mlab, where the job queued) carries
  `host` (mlab / air26 / mserv), `transfer_s` (snapshot + send + install), `run_s`, `exit`, and `reclaimed` when a
  switch took it back; `pdx gate history [--host] [--since] [--json]` reads them, and the App's queue history tab —
  today disabled (W5b1, `QueueSection.tsx:47`) — shows them in X4: where each command ran, how long, and its result.
  air26's own records of the same run stay on air26 (tagged `assist` with the mlab job id); the pool view uses mlab's.
- **mserv is in the same pool.** It has no pdx, so its slots, holders, queue and history are mlab's records
  (`assist-mserv-<k>`): a command running on mserv shows in the pool's 'now' with host mserv, and in the history afterwards.
  A run started on mserv by hand (ssh without the dispatcher) is outside the pool and is not shown.
- **One pool in view.** `pdx gate status [--json]` lists the pools (mlab heavy/light, air26 through its gate, mserv
  `assist-mserv-<k>`), every holder with its host, and the one queue with each waiter's candidate hosts; the fleet's queue view
  (B line) takes it from the status JSON, so the host cells show mlab's queue draining into air26 and mserv.

## 7. The watcher

- **Woken** through the daemon's `WakeWatcher(reason, detail)` (load plan W3b-5) when an assist switch turns on or off
  and when a team or session is allowed (decision 7: it prepares the environment then), with what changed. On assist on
  it prepares the environments of the allowed teams; on off it confirms the resources came back. The wake on the
  unattended switch is W3b-5's own.
- **Unattended mode shortens its intervals** (load plan W3b-5, not this spec's phases; the watcher's numbers, 10-17;
  the person can change them in the watch settings): its own patrol timer while awake 10 min when the host is tight, 15 min when steady (30 min when the person is
  present); while resting, a timed wake every 10 min (today a resting watcher wakes only on an event). Disk below 14 GB
  wakes it at once, whatever the interval. Reacting to pressure stays with the 30 s sampler (it does not wait for a
  patrol).

## 8. Phases (one PR each, ≤ 800 lines / 20 files)

| Phase | Content | Depends on |
|---|---|---|
| X1a | `assist.json` (hosts, switches, allowed list), `pdx assist …`, readiness from `assist-check --json`, `pdx gate hosts` | — |
| X1b | The notices; waking the watcher on assist switches and allows (`WakeWatcher`) | X1a, W3b-5 |
| X2a | Running on air26: snapshot / push / remote run / stream / cancel / cleanup, failure and reclaim requeue, the history record | GQ (serving order, snapshot), GC (conflicts) |
| X2b | The automatic walk: capability table, estimates, dispatch slots, status JSON with pools | X2a |
| X3 | mserv: mlab-held `assist-mserv-<k>` slots, PSI / MemAvailable admission over ssh, `git archive` transfer, `systemd-run` limits, mserv's capability table (the A grade, decision 12; MySQL dev) | X2b |
| X4 | The App panel (B line, lead 88): switches, the allowed list, the pool view, "what was taken back" | X1a (API), X2b (pool, history) |

The watcher's scripts (`assist-on/off/check`) gain `--json` and the folder-trust check in X1, and **mserv gets its own
`assist-on` / `assist-off` / `assist-check`** (today it has only the environment and the bench scripts); they stay the
watcher's.

## 9. Open questions (answered in the plan's contracts)

- Where `assist.json` lives when mlab is not the gated host of a caller (air26's own sessions): phase 1 dispatches only
  mlab's queue (decision: "mlab's items to air26 / mserv"); air26's own queue stays local.
- The per-repo `node_modules` keyed by lockfile on the assist host: disk budget and eviction (the watcher watches disk).
- How a remote run's resources show in air26's own gate status for a person looking at air26 (it is a normal gate holder
  there, tagged `assist` with the mlab job id).
