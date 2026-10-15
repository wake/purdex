# Host and process probes: measure, then remove the duplicates — plan

Date: 2026-10-16. Owner: A-line lead (purdex-1f). User decision 10-16: "the redundant cost should be consolidated and
de-duplicated as far as possible"; the fleet reads the daemon's own samples for the hosts that run a daemon (mlab, air26),
and probes only air-2019 and mserv over SSH.

Sources: two read-only audits of origin/main at 10-16 01:3x — `reports/probe-audit-daemon.md` (daemon process) and
`reports/probe-audit-outside.md` (gate, mod, App, fleet plan, host). The lead spot-checked the two biggest claims
(`internal/module/agent/verify.go:74` reads `ReadProcessInfo`, four `ps` forks on darwin; `handler.go:283-298` fetches a
session's cwd with three tmux forks on every Pre/PostToolUse although it is only a fallback for an event without `cwd`).

Every number below is a static estimate (code reading × measured unit cost: one tmux ≈ 15–18 ms CPU, one single-pid `ps`
≈ 8 ms, one `ps -A` ≈ 70 ms). Nothing has measured the daemon's real fork rate yet, so D0 comes first and every later PR
reports its before / after from D0.

## Rules for every PR here

- Behaviour stays the same: a cache must never serve a value its consumers could not have read before (same freshness
  bound or tighter), and a removed probe must be one whose result is provably unused or already held.
- One PR ≤ 800 lines or ≤ 20 files. Test tiers as in CLAUDE.md. Each PR states its D0 counts before and after (same host,
  same hour of activity, at least 30 min each).
- No behaviour change for Linux hosts unless the PR says so; darwin-only fast paths keep the Linux path.

## D0 — count every fork the daemon makes (A-line, first)

- Route every `exec.Command`/`exec.CommandContext` the daemon runs through `internal/execstat` with a caller tag
  (tmux.Executor, agent.runPS, `config/hostid.go`'s raw tmux, resources `ps`/`vm_stat`, monitor `ps`/`vm_stat`/`iostat`,
  probe `ps -Ao`, codexbroker `ps -eo`, lsof, ssh of the fleet, …). A test fails when a non-test `exec.Command` in the
  daemon's packages bypasses it (grep guard, like the existing embed guards).
- Expose `GET /api/debug/execstat` (admin): per tag, total count, count in the last 1 / 5 / 60 min, total wall time.
- Baseline: the lead reads it for an hour of normal activity on mlab before D1 lands; numbers go into this plan.

## D1 — the hook event path (A-line; the biggest, scales with use)

Per Pre/PostToolUse the daemon makes ≈ 6–7 tmux + 4 `ps` forks (≈ 15 events/min average, 103/min peak → ≈ 4 % of a core
average, ≈ 28 % peak, CLI included).
1. Path hint: fetch the session cwd only when the event has no `cwd` (`handler.go:283-298` → `path_hint_extractor.go:111`).
2. `ReadProcessInfo` on darwin without forks: `kern.proc.pid` + the existing `readCommArgs`
   (`process_snapshot_darwin.go:162`), the same fields as `ProcessSnapshot.Read` (`process_snapshot.go:41` says they agree).
   This fixes every caller at once (`verify.go:74`, `ancestor.go:224`, `frame_ops.go:862/2238`, `probe/liveness.go:139/178`,
   `peers/registry.go:169/175`). A table test compares the two implementations on live pids.
3. One pane pid per event: verify's `resolvePanePID` result is reused by `projectPane`/`filterPaneFramesBy` within the
   same event.

## D2 — one tmux list for the session list (A-line)

Today the list costs 2 + N forks (list-sessions, tmux instance, one display-message per session), every 5 s from the
watcher whether or not anyone listens (N = 15 → ≈ 204 forks/min idle), and again for each SSE subscriber, each peers
envelope, each wait-for event and agent's snapshot branch; the 1 s cache serves only `GET /api/sessions` and the watcher
invalidates it.
1. One `tmux list-panes -a -F` with the session fields, the active pane's fields (`#{pane_active}`/`#{window_active}`)
   and `#{pid}:#{start_time}` (the tmux instance; verified on tmux 3.6a) replaces the 2 + N.
2. The watcher's result is the shared copy (TTL ≤ the watcher period): peers, agent, SSE subscribe, `getSession` and
   `GET /api/sessions` read it; `?fresh=1` still forces a read.
3. Fix the wrong comment at `agent/module.go:751` ("ListSessions has a 1 s cache").

## D3 — peers envelope and process table (A-line)

1. `localEnvelope` (`peers/module.go:619-665`): the session list from D2; the owner pass's two `ListAllPanes`
   (`owner_pass.go:206/280`, the second one detects join-pane) read one `list-panes -a` result plus a short-TTL second
   read only when the first one shows a candidate; the process table from `SharedProcessSnapshot`.
2. Everything that reads the whole process table goes through `SharedProcessSnapshot` (TTL 2 s): agent sweep
   (`sweep.go:591`, period = TTL), probe `listProcessTree` (`liveness.go:274`, `ps -Ao`), team/owner callers. The
   sampler's deliberate before/after pair (`readRoots`) stays as it is.
3. `FirstAliveAgentInTree`/`IsAliveFor`: one process-info read per pid per call (today identifiers × pids, up to 132
   `ps` for one call).
4. Monitor's top processes read the sampler's last `ps` (`resources` `lastProcs`) instead of their own `ps -axo …command=`
   (`monitor/process.go:245`); the sampler's `ps` gains `command=` if the monitor needs it (measure the cost difference).
5. Delete the dead `tmux/executor.go:743` `paneProcessCommands`.

## D4 — tmux instance and pane existence (A-line)

1. The tmux instance (`config/hostid.go:33`, read from 8+ places, never cached, raw `exec.Command`): cached, invalidated
   by the session module's server-state / wait-for signal; folded into D2's list output where a list is read anyway.
2. `HasPane` (`executor.go:550`, one `list-panes -a` per armed codex pane per second): a shared ≤ 1 s cache, the same one
   codexbroker's `realPaneChecker` uses.

## G-D1 — the gate and the mod (lease member, after L2c)

1. One host-memory package (`internal/hostmem`): pressure level, `vm_stat` counters, page rate (gate's `RateOver`). The
   gate, resources and monitor all use it (three `parseVMStat` copies today).
2. While the daemon runs, each sample also writes the gate's `pressure.json` (same format, same flock); the gate's reuse
   window widens to 7–8 s, so it stops forking `vm_stat` while the daemon is alive and keeps reading for itself when the
   daemon is not (the gate still does not depend on the daemon).
3. The gate's tree watch (`run.go:184`, the whole process table every 500 ms per running gated command): every 2 s
   (reaping at the end does not need 500 ms); kqueue `NOTE_TRACK` is a later option, not in this plan.
4. `pdx gate would-wait --auto` accepts several commands in one call; `gate.js` asks once per Bash call instead of up to
   six forks.
5. mod polling: `/mod/v1/team` every 15 s per session → carried on the existing events ack when it changes.

## F — the fleet on daemon hosts (B line with the lease member)

1. Daemon side (lease member, after G-D1): a 1 s light tick in the resources sampler that reads only fork-free sources
   (sysctl, `host_statistics64` page counters, `host_processor_info` CPU ticks → real CPU %); the `ps` stays at 5 s; the
   gate summary (held / waiting per class) comes from the watch pass's `HeldSlots` read. `/api/fleet` `self` carries them;
   the fleet primary reads `/api/fleet` of mlab and air26 instead of probing them.
2. Fleet side (B line): SSH only for air-2019 and mserv, over one long-lived connection per host (a remote loop or
   ControlMaster), never a new handshake per second. The fleet spec is updated by the B line to say: daemon hosts are
   never probed over SSH and never forked for the fleet.

## App side (B line, low priority)

- Electron `app.getAppMetrics()` from three places (`main.ts:312`, `browser-view-manager.ts:59`, the memory monitor's
  `metrics:get`): one 5 s cache.
- SPA `useSessionWatch` polls `/api/sessions` every second per host: check whether it is still mounted; if yes, move it to
  the host events push.

## Not changed

- The lead's W0 sampler (`ps` every 10 s, ends by itself 10-17 19:3x): kept for the watcher thresholds; it is temporary.
- Four `statfs` readers: cheap; unify on `fleet.DiskMount` only when one of them is touched anyway.

## Order and acceptance

D0 → D1 → D2 → D3 → D4 on the A line (the watcher's W3/W4 wait); G-D1 → F daemon side on the lease member after L2c; the
B line's fleet and App items in their own queue. Done when D0 shows, for an hour of comparable activity on mlab, the
daemon's forks per minute and its idle CPU (three 30 s profiles, as for 740/741) both below the D0 baseline by the amounts
each PR claimed, and no consumer reads a value older than before.
