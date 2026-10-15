# Host and process probes: measure, then remove the duplicates — plan (rev 2)

Date: 2026-10-16. Owner: A-line lead (purdex-1f). User decision 10-16: "the redundant cost should be consolidated and
de-duplicated as far as possible"; the fleet primary reads its own host from its own daemon (no SSH, no fork) and probes
the other three hosts over SSH; the App takes the fleet from the primary only.

Sources: two read-only audits of origin/main at 10-16 01:3x — `reports/probe-audit-daemon.md` (daemon process) and
`reports/probe-audit-outside.md` (gate, mod, App, fleet plan, host) — and the codex plan review (task-mv9u9s0e-he0p51,
18 findings, all taken; rev 2 drops every item that would have served an older value).

Every number below is a static estimate (code reading × measured unit cost: one tmux ≈ 15–18 ms CPU, one single-pid `ps`
≈ 8 ms, one `ps -A` ≈ 70 ms). D0 measures before anything changes.

## The rule (rev 2)

**Never serve an older value than today.** A probe may only (a) go, when nothing reads its result, or (b) get cheaper for
the same freshness — the same read at the same moment, with fewer forks (2 + N tmux calls folded into one, a `ps` replaced
by the sysctl that returns the same fields). Shared caches, longer intervals and "reuse the last sample" are out: the
review showed each one breaks a contract (session snapshot seq, server-restart brackets, join-pane confirmation, gate tree
escape window, monitor freshness, ProcessDead latency, gate admission freshness).

Every PR: ≤ 800 lines or ≤ 20 files; test tiers as in CLAUDE.md; states D0's per-trigger cost before and after; lists the
contracts it keeps (below, per item) with a regression test for each. Linux paths keep working; darwin fast paths fall back.

## D0 — count every fork, per trigger (A-line, first; three PRs)

- **D0a** `internal/execstat`: count at a **successful `Start`** (a long-lived child — tmux wait-for, PTY relay, peer
  proxy, dev stream — counts when it starts, not when it exits; a command that fails to start counts as a failure, not a
  fork). Helpers `execstat.Run/Output/CombinedOutput/Start(tag, cmd)`. Counters per tag: total, last 1 / 5 / 60 min,
  wall time. **Denominators** in the same registry so costs normalise per trigger: hook events by name, session-list
  builds, peers envelopes, SSE subscribers, armed codex detectors, sessions (N), resources mode. `GET /api/debug/execstat`
  (admin) returns both. A guard test lists today's non-test `exec.Command`/`CommandContext` call sites in daemon packages
  (35 files) as an allowlist that may only shrink; a new call site outside the helpers fails.
- **D0b, D0c**: move the call sites onto the helpers in two batches by package (each ≤ 20 files), shrinking the allowlist.
- **The hook CLI** is its own process: `pdx hook` counts its own forks and sends the count with the event (a field the
  daemon adds under tag `hook-cli`), so D1 can show the CLI's share.
- Baseline: the lead reads the endpoint for an hour on mlab after D0c and records per-trigger costs here.

## D1 — the hook event path (A-line)

1. **Path hint cwd only when the event has none** (`handler.go:283-298`, used only by `path_hint_extractor.go:111-113`).
   Contract: an event without `cwd` still gets the session's cwd (test with and without `cwd`, cc and non-cc).
2. **`ReadProcessInfo` on darwin through sysctl first** (`kern.proc.pid` + `readCommArgs`,
   `process_snapshot_darwin.go:169`): the common path goes 4 → 0 forks; when `kern.procargs2` is unreadable or the argv
   is not printable ASCII it falls back to the existing `ps` path (4 → 2). Contracts: the same `ProcessInfo` fields as
   today for a normal process, a non-ASCII argv, another user's process, a zombie, a permission failure, an exited pid and
   a reused pid; the old implementation stays as the test oracle (`readProcessInfoPS`) in the same package.
3. Dropped from rev 1: reusing verify's pane pid in `projectPane` (the projection re-resolves on purpose,
   `frame_ops.go:1233/1258`).

## D2 — one tmux read per session-list build (A-line)

1. The builder's 2 + N calls (`service.go:72/75/219`: list-sessions, tmux instance, one display-message per session)
   become **one** `tmux list-panes -a -F` carrying the session fields, the active pane's fields
   (`#{window_active}`/`#{pane_active}`) and `#{pid}:#{start_time}`. Every caller still reads fresh, as today; nothing is
   cached beyond the existing 1 s HTTP cache. Contracts: versioned snapshots get one fresh read per seq
   (`versioned.go:44`); an external tmux mutation between two builds shows; the instance read stays bracketed where a
   caller brackets it (it now comes from the same list call, so a restart between two builds still differs); empty server,
   one session without an active pane, names with spaces and tabs (the locale TAB issue).
2. Team boot reconciliation (`team/spawn_resume.go:28-39/88-96`: one list, then `PaneIdentity` per session, then
   `HasSession` per running spawn) reads the same single list (codex finding 17).
3. Fix the wrong comment at `agent/module.go:751`.
4. To investigate, not to change yet: whether the watcher's 5 s read has any consumer when no SSE subscriber exists
   (`watcher.go:155-163`). Change only with evidence that nothing else reads it.

## D3 — peers envelope and process-info reads within one call (A-line)

1. `localEnvelope` (`peers/module.go:619-665`) gets D2's single read (≈ 17 of its ≈ 20 forks). The owner pass keeps its
   two `ListAllPanes` (the second one confirms no join-pane happened, `owner_pass.go:45/52/182/206`).
2. `FirstAliveAgentInTree`/`IsAliveFor` (`probe/liveness.go:105-139`): within **one call**, read each pid's process info
   once and reuse it for every identifier (up to 132 `ps` → 11 reads; with D1, sysctl). Nothing crosses calls.
3. Delete the dead `tmux/executor.go:743` `paneProcessCommands`.
4. Dropped from rev 1: moving every whole-table reader onto the 2 s `SharedProcessSnapshot` (owner pass, sweep and the
   probe's 250 ms tree cache need fresher data); the monitor reading the sampler's `lastProcs` (staleness, no wiring).

## D4 — tmux instance (A-line)

The instance is read from 8+ places with a raw `exec.Command` (`config/hostid.go:33`). Where a caller already lists tmux
in the same operation, take the instance from that list's `#{pid}:#{start_time}` (one call instead of two); keep every
bracket read that exists to detect a restart (`provenance_handler.go:42`, peers, `watcher_test.go:367/417`). No cache.
Dropped from rev 1: the shared `HasPane` cache (ProcessDead latency) and caching the instance.

## G-D1 — the gate and the mod (lease member, after L2c)

1. One host-memory package (`internal/hostmem`): pressure level, `vm_stat` counters, page rate (gate's `RateOver`); the
   gate, resources and monitor use it (three `parseVMStat` copies today).
2. While the daemon runs, each sample also writes the gate's `pressure.json` with that package (same format, same
   flock). **The gate's reuse window stays 5 s**: it reuses a file younger than 5 s, whoever wrote it, and reads for itself
   otherwise — so its freshness is unchanged, it forks less while the daemon writes, and it works without the daemon.
   Contracts: two writers (daemon and a gate) never interleave; a stale or corrupt file is never used.
3. `pdx gate would-wait --auto` takes several commands in one call; `gate.js` asks once per Bash call (up to six forks →
   one). Contract: the answer for each command is the same as asking one by one.
4. mod polling: `/mod/v1/team` every 15 s per session → carried on the existing events ack when it changes.
5. Dropped from rev 1: a 2 s gate tree watch (the escape window is `TreeEvery`, `run.go:174/184`); kqueue `NOTE_TRACK`
   is a separate idea, not in this plan.

## F — the fleet primary does not probe itself (B line with the lease member)

User decision (10-15, confirmed 10-16): primary / standby in the order `[air26, mlab]`; **the App takes all four hosts'
data from the primary only**; the standby measures nothing, checks every 30 s that the primary is alive and takes over
when it is not. The only duplicate the fleet adds is the primary measuring its own host, which its daemon already
samples; that one goes. The primary's external view of the other daemon host (normally mlab) is wanted and stays.

1. Daemon side (lease member, after G-D1): a 1 s light tick in the resources sampler that reads only fork-free sources
   (sysctl, `host_statistics64` page counters, `host_processor_info` CPU ticks → real CPU %), run only while this daemon
   is the fleet primary; the `ps` stays at 5 s; the local gate summary comes from the watch pass's `HeldSlots` read. The
   fleet module takes its own host's row from these, never from SSH or a fork. Role and sequence number belong to the
   fleet module (B line).
2. Fleet side (B line): SSH for the three other hosts (air-2019, mserv and the other daemon host), one long-lived
   connection per host (a remote loop), five items each second, the process lists and — for the other daemon host —
   `pdx gate status --json` every 5 s; never a new handshake per second. The App reads the fleet from the primary only
   (push). The cost the user accepted: the primary's own host has no outside view until the standby takes over.

## App side (B line, low priority)

- Electron `app.getAppMetrics()` from three places (`main.ts:313` via `browserViewManager.getMetrics()`,
  `browser-view-manager.ts:336/362`, the memory monitor's `metrics:get`): one 5 s cache is acceptable here only if the
  monitor page tolerates 5 s (it polls every 5 s today); otherwise share within one tick.
- SPA `useSessionWatch` polls `/api/sessions` every second per host: check whether it is still mounted; if yes, move it to
  the host events push.

## Not changed

- The lead's W0 sampler (`ps` every 10 s, ends by itself 10-17 19:3x): kept for the watcher thresholds; it is temporary.
- Four `statfs` readers: cheap; unify on `fleet.DiskMount` only when one of them is touched anyway.

## Order and acceptance

D0a → D0b → D0c → baseline → D1 → D2 → D3 → D4 on the A line (the watcher's W3/W4 wait); G-D1 → F daemon side on the
lease member after L2c; the B line's fleet and App items in their own queue. Each PR shows its per-trigger cost before
and after from D0 (forks per hook event, per list build, per envelope …), so the saving is attributable regardless of how
busy the host was. Done when every item's per-trigger cost dropped as claimed, idle CPU (three 30 s profiles, as for
740/741) did not rise, and every listed contract has its regression test.
