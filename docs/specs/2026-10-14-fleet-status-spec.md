# Fleet status: one primary measures every host, the sidebar shows them — spec (rev 2)

> 2026-10-14, 88 (interface line). **Rev 2, 2026-10-16**: rewritten on the user's decisions of 10-15 / 10-16 (§2) — a primary
> and a standby instead of "mlab probes", one second instead of thirty, the App reading the fleet from the primary only, and
> the sidebar host cells of the load-control prototype instead of the rows of rev 1. Rev 1's facts that still hold are kept.
> Facts (paths from the repo root) were read from origin/main on 2026-10-16 (alpha.742).
> Related: host load control `docs/specs/2026-10-15-host-load-control-spec.md` (§12.8 points here), the A line's probe
> de-duplication plan `docs/plans/2026-10-16-probe-dedupe-plan.md` (§F), the watcher area UI spec (B line, to follow).

## 1. Problem

The user runs four machines: **mlab** and **air26** (Purdex daemons), **air-2019** and **mserv** (no daemon, monitor only).
Whether one of them is overloaded, out of memory, low on disk or off is found out only by logging in, and on 10-15 mlab
went to load 66 while nobody could see it coming. The App should show all four, live, at a glance.

## 2. User decisions (do not reopen)

10-14 (rev 1, still in force): the four hosts above; each host with its own icon and colour; the Hosts page can add hosts
that are probed but have no daemon, with name, icon and colour; Mac App first, iOS later.

10-15 / 10-16 (rev 2):

1. **Primary and standby**, in the order `[air26, mlab]`. The primary measures; the standby measures nothing and checks
   every 30 s that the primary is alive. "Alive" = it answers **and** produced a fresh reading in the last 10 s (a primary
   stuck in paging counts as dead). When the primary is dead the standby takes over and becomes primary; the old primary,
   back, sees there is a primary and becomes the standby — **no switch back**. When neither is primary at start, air26 is.
   A gap during a takeover is acceptable.
2. **What the primary measures, for all four hosts**: every **1 s** five items — real CPU use, memory use, memory pressure
   level, page rate, disk space; every **5 s** two lists — the top processes (by CPU and by memory) and, for the two daemon
   hosts, the heavy-command queue. **Fixed rates**: never faster or slower because a host is busy ("raising the rate when it
   is busy is pain on pain").
3. **How**: the primary's own host is read from its own daemon (no SSH, no fork); the other three hosts — the other daemon
   host (normally mlab), air-2019, mserv — over SSH, **one long-lived connection per host** (a loop on the remote side),
   never a new handshake per sample. The cost accepted: the primary's own host has no outside view until a takeover.
4. **The App takes the whole fleet from the primary only**, pushed; it does not read each daemon's fleet.
5. **Results are used and dropped**: pushed to the App, never written to a database, never logged per sample; the existing
   per-minute `host_minutes` is unchanged. Any history the UI shows is kept in the App's memory only; a newly started App
   starts empty.
6. **Zone**: one rule for all four hosts — the memory signal of the host load control (L2): warning when the pressure level
   is ≥ 2 or the page rate ≥ 50 pages/s, danger when the level is ≥ 4; CPU load is informational; plus rev 1's disk latch.
   Words: 正常 / 偏高 / 過載 / 離線.
7. **The sidebar shows the fleet as one row of host cells at the bottom of the sidebar** (replaces rev 1's rows and compact
   strip; the 10-14 decisions 1 and 3 on the expanded / collapsed looks are withdrawn, the user's 10-15 call): per host a
   cell = the host's icon box (its colour) with the heavy-queue count as a badge at its top-right corner (daemon hosts only,
   only while someone waits, its colour by how bad the queue is), and a ring beside it = the **composite load**; the
   BottomNav buttons above keep their own expanded / collapsed looks. A click opens the watcher area on that host's tab.
   Offline: the box keeps a faint host colour under a hatch.

## 3. Facts this rests on

- **`internal/fleet` (F-1a, merged)**: `sshconfig.go` (literal `Host` aliases, one `Include` level, re-read per use), `script.go`
  (one fixed script, marker sections, `@@end`), `parse.go` (Darwin / Linux), `run.go` (`Runner.Run`: one call with
  `BatchMode`, `ConnectTimeout=5`, `StrictHostKeyChecking=yes`, `RemoteCommand=none`; status `ok / offline / error` and the
  codes `timeout / unreachable / host_key / auth / remote_exit / parse / alias_gone`, stderr never on the wire),
  `zone.go` (`ZoneLatch`-based zone + disk latch), `disk*.go` / `diskmount.go` (the Data volume on Darwin, `/` on Linux).
- **`internal/hostmem` (G-D1a/b, merged 742)**: one `vm_stat` parser, the pressure level, the page rate over
  `RateWindow`, the gate's `pressure.json` cache.
- **The daemon's own sampler**: `internal/resources` samples every 5 s (`ps` + `vm_stat`). The A line adds, for the fleet, a
  **1 s light tick** that reads only fork-free sources (sysctl, `host_statistics64`, `host_processor_info` → real CPU %), run
  only while this daemon is the fleet primary; `ps` stays at 5 s; the local gate summary comes from the watch pass
  (probe-dedupe plan §F.1). Role and term belong to the fleet module (this spec).
- **The gate's status** `internal/gate/gate.go` `Status` (`Heavy`, `Light` holders; `WaitHeavy`, `WaitLight`; `QueueHeavy`,
  `QueueLight` waiters, first in line first; `Settings`), printed by `pdx gate status --json` without the daemon, ≈ 0.06 s
  CPU per call (mostly `pdx` start-up, #2925).
- **Measured probe cost (10-15)**: one sample of the base items on the remote side ≈ 5 ms CPU (macOS) / 17 ms (mserv); with
  the process list 25–47 ms; one long-lived stream's SSH cost ≈ 0.09 s CPU per minute on each end.
- **Peer calls between daemons**: `HostCaller` (`to_host_id`) and `peers.HostRoutePolicy` (the watcher's `/api/peers/watch/*`
  routes use the same path).
- **App**: `HostConfig` and the host look (`useHostLook`, `resolveHostColors`, `HostBadge`); BottomNav in
  `ActivityBarWide.tsx` / `ActivityBarNarrow.tsx`; the team strip cells `components/team/TeamCell.tsx` (`h-6`, `ContextRing`,
  `CellSep`) — the host cells follow their sizes.

## 4. Roles

- **Setting** (each daemon's host config, `fleet` row): `order` (default `[air26, mlab]`), `peer_ssh_alias` (the SSH alias
  this daemon uses for the other daemon host), `enabled` (default true). `order`: 1–4 entries, each of the peer alias
  shape (`config.ValidateSelfAlias`'s rule), unique case-insensitively, one of them **this daemon's own alias**
  (`Cfg.Peers.Alias`), the others resolvable to a peer host id (`HostCaller.HostIDOf`). An own alias that is empty or not
  in `order` → the fleet is **off** on this daemon (role `off`, with the reason; it measures nothing and never becomes
  primary). When the two daemons' `order` differ, each logs one warning per hour (no other effect).
- **State** (`~/.config/pdx/fleet/state.json`, temp file + rename, no fsync): role (`primary | standby | off`), the highest
  **term** seen, when the other was last heard. A torn or missing file = standby, term 0; a term lost to a power cut is
  harmless (start adopts the other's). A failed write is logged and the role stays in memory.
- **Peer calls** go through `HostCaller` (POST, the reply must carry the callee's `host_id`), both routes in
  `peers.HostRoutePolicy` (POST, exact path):
  - `POST /api/peers/fleet/role` body `{to_host_id, from_alias, role, term}` → `{host_id, alias, role, term, order,
    newest_age_ms}` (`newest_age_ms` null when this daemon has no reading yet). The callee checks `to_host_id` is its own
    and applies the caller's `role` / `term` like an announcement (below).
  - `POST /api/peers/fleet/announce` body `{to_host_id, from_alias, term}` → `{host_id}`.
- **Heartbeat**: **both** roles call the other's `role` every 30 s (timeout 5 s, or the peers inter-daemon timeout if longer).
  For the standby, the primary is alive when it answers and `newest_age_ms` is null or ≤ 10 000; `newest_age_ms` is the
  age of the primary's newest reading of **any** host it measures (its own or one over SSH). Only an answer whose newest
  reading is older than 10 s counts as stuck; null (nothing measured yet, or nothing to measure) is alive — a takeover would
  not help then and would only make the two daemons swap back and forth (1f's review of F-1b2, 10-16). No answer, or `off`,
  is dead.
- **Takeover**: the standby that finds the primary dead becomes primary with `term = highest seen + 1`, persists it, starts
  measuring, and announces (best effort; the next heartbeat repeats the information anyway).
- **Two primaries** (a partition, a start with the other unreachable, or a takeover from a primary that was slow, not gone):
  any daemon that learns of a higher term — a heartbeat answer, an announcement, or the role / term carried in the other's
  heartbeat call — steps down to standby and stops measuring. **Equal terms, both primary**: the one later in `order` steps
  down. Because both roles heartbeat, two primaries converge within one heartbeat of the two seeing each other again.
- **Start**: a daemon calls the other's `role`. Other is primary → standby (adopt its term). Other unreachable → primary
  with `term = persisted + 1`, and an announcement once the other answers. Other answers standby, off or starting → the one
  first in `order` becomes primary with `term = max + 1`, the other standby.
- **`enabled = false` or off**: answers `off`, measures nothing; a primary turned off stops at once (the standby takes over
  at its next heartbeat, the `off` answer counting as dead).
- **Standby work**: the heartbeat only. It answers the App's `GET /api/fleet` with its role and the primary's alias (§6).

## 5. Measuring (the primary)

- **Its own host**: from the A line's local source (§7), every 1 s; the disk from the fleet module's own `statfs` of the
  §3 mount at the same tick.
- **The other three hosts**: one SSH session each, started when the daemon becomes primary and stopped when it steps down:
  `ssh -T -o BatchMode=yes -o ConnectTimeout=5 -o StrictHostKeyChecking=yes -o RemoteCommand=none -o ControlMaster=no
  -o ControlPath=none -o ServerAliveInterval=5 -o ServerAliveCountMax=2 -- <alias> <fixed loop script>`, in its own process
  group (a stop signals the whole group, so a `ProxyJump` / `ProxyCommand` child does not hold the pipe). The loop is a
  compile-time constant (no byte from the App or the alias), prints one frame per second — the five items, between markers,
  ending `@@end` — and every fifth frame also the two process lists and, on the other daemon host only,
  `pdx gate status --json` between its own markers (looked up as `$HOME/.local/bin/pdx`, then `$HOME/.config/pdx/bin/pdx`;
  absent → no queue, not an error). The loop ends when a write fails (the session is gone) and takes its long-running
  children with it (a `trap` on exit).
  - Timestamps are the primary's receive time, not the remote clock. Each frame is capped at 64 KiB; a gate section that
    does not parse (or is over its share) only blanks that host's queue; five fast frames in a row that do not parse →
    `error: parse` and a restart (the count resets on a good frame). The reader hands frames on through a newest-wins slot,
    never blocking the remote side; a frame older than 2 s when taken is dropped.
  - macOS: CPU from a streaming `iostat` (one long-running process, no fork per second), level from
    `sysctl kern.memorystatus_vm_pressure_level`, page counters from `vm_stat`, disk from `df -k` of the §3 mount, the lists
    from one `ps` every 5 s.
  - Linux: CPU from `/proc/stat` deltas, memory from `/proc/meminfo`, **level** from `/proc/pressure/memory` (`full avg10`
    ≥ 10 % → 4, `some avg10` ≥ 10 % → 2, else 1 — first values, tuned on the live record; no PSI → level unknown and the
    zone uses the page rate only), page counters `pswpin + pswpout` from `/proc/vmstat`, disk from `df -kP /`; read with
    shell built-ins where possible.
  - **Page rate** for every host over the same window as the own host (`hostmem.RateWindow`), from the counters of the
    frames in that window — never a one-frame delta, so the 50 pages/s threshold means the same on all four.
  - The plan measures the loop's cost on each OS (target ≤ 15 ms CPU per second per host on the remote side) and reports
    it; going over is the lead's call, never an automatic change of rate (§2.2).
- **Status per host**: `ok`; `offline` (the session could not connect, or no frame for 5 s); `error` with rev 1's codes
  (stderr read locally, never on the wire; the daemon log gets at most one line per host and code per hour). A host
  without frames for 5 s shows `offline` while its session is kept; only after 30 s without frames is the session killed
  and restarted (a host that is paging is not hammered with handshakes). A session that ends is restarted after 5 s, then
  with backoff to 60 s, reset by a good frame; every wait is cut short by a stop.
- **Composite load** (the ring), 0–100 = the largest of: CPU %; memory %; the level (1 → 30, 2 → 75, 4 → 100); page rate
  (pages/s, capped at 100); disk % only when ≥ 85. The **ring's colour is the zone** (§2.6), not the score: a host at CPU
  95 % with no memory pressure shows a long neutral ring ("busy, fine").
- **Zone latch** (memory part, so the ring does not flicker): warning entered at level ≥ 2 or page rate ≥ 50, left after
  10 s with level 1 and rate < 40; danger entered at level 4, left after 10 s below 4; one level at a time; plus rev 1's
  disk latch; the worse of the two.
- **Queue summary** (daemon hosts): from the gate status — heavy and light slots used / total, the waiters (place, command,
  project, since), the longest wait, and **admission held**: the gate status's `Admission` (the A line adds it), or, from a
  gate without it, the level / page rate against that host's effective `admission.*` settings in `Status.Settings`.

## 6. API and push (capability `fleet.v1`)

- `GET /api/fleet` on every daemon. The standby answers `{role: "standby", term, primary: <alias>}`. The primary answers
  `{role: "primary", term, at, hosts: [Host]}`; `Host` = `{id, kind: daemon|probe, alias, name, icon, colors, icon_weight,
  os, status, error, last_ok_at, fast: {at, cpu, mem, level, page_rate, disk, disk_free_gb, load1, ncpu, zone, load},
  slow: {at, top_cpu: [Proc], top_mem: [Proc], queue?: Queue}}` (`load` = the composite score; `Proc` = `{pid, name, cpu,
  rss_mb}`; `Queue` = the §5 summary). `id`: a daemon host's own daemon host id (the App maps it to its `HostConfig` through
  `lib/host-daemon-id.ts` and takes the name and look from there); a probe host's config row id (it carries its own look).
  An `off` daemon answers `{role: "off", reason}`.
- Host event **`fleet.changed`** from the primary, **once per second**, opt-in: subscribers that ask `?fleet=v1` on
  `/ws/host-events` (a `FeatureFleetV1` in `featuresOf`; older SPAs and iOS never get it); delivered best-effort,
  newest-wins per subscriber (a slow subscriber loses old pushes, it is not disconnected). Payload `{term, primary,
  hosts}`: the `fast` part of every host; the `slow` part of a host only in the push right after it changed (every 5 s);
  `status` / `error` changes in the next push. Never sent by a standby.
- The App connects to both daemons already. It reads `GET /api/fleet` from each, follows the one that says `primary`
  (two primaries → the higher term; equal → the earlier in `order`), and merges `fleet.changed` from that one only; a push
  from another daemon carrying a higher term makes it re-read both and switch. No push for 10 s, or the primary
  disconnects → the cells keep their last values, the row shows 「等待主方」 (tooltip), and the App re-reads both daemons'
  `GET /api/fleet` every 5 s until a primary answers. An App that knows only the standby shows 「主方不在這台 App」.
- `probeHosts` (with each row's `ssh_alias`) is readable with a device token like the rest of `GET /api/hostconfig`: alias
  names are not secrets.
- `GET /api/hostconfig` carries `probeHosts` (`{items, revision}`; rows `{id, name, ssh_alias, icon, colors, icon_weight}`)
  and the `fleet` row; `PUT /api/hostconfig/probe-hosts` and `PUT /api/hostconfig/fleet` as rev 1 (whole list, `baseRevision`,
  409 with the current copy, 400 with a plain-text reason; an alias not in the SSH config → `unknown_ssh_alias: <alias>`);
  admin only. `GET /api/fleet/ssh-aliases` lists the eligible aliases (admin only).
- **`POST /api/fleet/probe-check`** `{ssh_alias}` (admin only; device 403; not a peer route; F-2c): this daemon probes the
  alias once with the one-shot `fleet.Runner` (`internal/fleet/run.go`: the fixed argv, `BatchMode`, strict host key, its
  own timeout) after `ValidAlias`, and answers `{ssh: "ok"|"failed"|"unknown", measure: "ok"|"failed"|"unknown", error:
  <Code>|null, reading: {cpu, mem, level, disk}|null}`. `ok` → both ok with the reading; `auth`, `host_key`, `alias_gone`
  → ssh failed; `timeout`, `unreachable` → both unknown (the host could not be reached); `remote_exit`, `parse` → ssh ok,
  measure failed. Never the stderr. One check per alias at a time (a second one while the first runs gets the first's
  answer).
- Device tokens may read `GET /api/fleet` and receive `fleet.changed` (iOS later).

## 7. The local source (interface with the A line)

The A line's lease member implements, in the resources sampler, a `fleet.LocalSource` the fleet module reads its own host
from (probe-dedupe plan §F.1). Defined in `internal/fleet` (this spec), implemented outside it:

```go
type LocalFast struct {
	At       time.Time
	CPU      float64 // real CPU use of the whole host since the previous tick, 0–100
	Mem      float64 // used memory, 0–100
	Level    int     // kern.memorystatus_vm_pressure_level: 1, 2 or 4
	PageRate float64 // pageouts + swapouts per second over hostmem.RateWindow
	Load1    float64
	NCPU     int
}
type LocalSlow struct {
	At     time.Time
	TopCPU []Proc       // 5, by CPU
	TopMem []Proc       // 5, by RSS
	Gate   *gate.Status // the local gate summary (watch pass HeldSlots); nil when no gate is installed
}
type LocalSource interface {
	Start()                   // begin the 1 s tick: called when this daemon becomes primary
	Stop()                    // end it: called when it steps down
	Fast() (LocalFast, bool)  // the newest 1 s reading; false before the first
	Slow() (LocalSlow, bool)  // the newest 5 s reading; false before the first
	Changed() <-chan struct{} // signalled after each new fast reading (coalesced, never blocks the sampler)
}
```

The remote frames parse into the same two types, so the composite, the zone and the push treat all four hosts alike. Until
the A line's implementation lands, the fleet module runs with a fake that reports nothing for its own host (the cell shows
「沒有資料」). The resources module registers its implementation under `fleet.LocalSourceKey` in its `Init`; the fleet module
depends on `peers`, `hostconfig` and `resources` (so it resolves the key after it is registered) and announces `fleet.v1`
through `capabilityList()` with `moduleReady("fleet")`.

The fleet module's role is read by the measuring side through `fleet.RoleView` (`State() RoleState`, `OnChange(func(RoleState))`;
`RoleState{Role, Term, Primary, Self, Reason}`), defined with the §7 types so the two daemon PRs can be built in parallel.

## 8. Mac App

### 8.1 The sidebar host cells (decision 7)

- One row at the very bottom of the wide sidebar, under the BottomNav (whose own expanded / collapsed looks are unchanged),
  the four hosts in the fleet order (daemon hosts in the App's host order, then the probe hosts in their stored order).
  It fits one row at the default sidebar width (each daemon host's cell ≈ 46 × 24 px).
- A cell, sized like a `TeamCell` (`h-6`, `rounded-md`, hover `bg-surface-hover`, selected `bg-surface-active`, `CellSep`
  between cells):
  - **The host box**, 18 × 18, `rounded-[4px]`, the host's colours (`resolveHostColors`), the icon white (90 %, 100 % when
    hovered or selected). Offline: the host colour at 35 % under a fine hatch, the icon at 35 %.
  - **The queue badge** (daemon hosts, only while someone waits; not while offline) on the box's top-right corner, ≈ 11 px,
    the count in 8 px semibold tabular figures, a 1 px ring in the sidebar's background colour. Colour: neutral grey while
    the longest wait is under 2 min; `status-warning` from 2 min or 3 waiters; `status-error` while admission is held.
  - **The composite load ring**, 16 px, the `ContextRing` geometry (2.5 stroke, `--border-default` track, round cap, from
    12 o'clock counter-clockwise): length = the composite score, colour = the zone (neutral / warning / error); offline =
    a dashed track, no arc.
- Tooltip: name, the zone word, the five values, the queue summary (daemon hosts), and 「等待主方」 when stale.
- Click: opens the watcher area on that host's tab (watcher area spec).
- **Narrow activity bar**: the four cells stacked vertically (box + ring only), same click and tooltip.
- The badge is one component (`CountBadge`, ≈ 11 px, 8 px figures, ring in the background colour); the watcher shield in
  the HomeRow reuses it (watcher area spec, not this one).
- **Click, until the watcher area lands**: a daemon host's cell opens that host on the Hosts page; a probe host's opens the
  Hosts page's probe-host section with that row highlighted once.

**Restyle (user, 10-16: 「太刺眼」; F-2b2)**, replacing the box and ring above:
- the host icon is the App's host badge (`HostBadge` with the user's badge settings, the same small badge as beside a
  member in the session list: dark background, the icon in the host's colour), the same size;
- the ring is `ContextRing` itself (the title bar's member ring, same thickness and colours; the middle empty — its shape is an agent's model, which a host has not; user 10-16): length =
  the composite score, tone from the zone (normal = the ok green, warning, danger); offline = the track only;
- the cells are laid out like the session list's member beads (`TeamMemberBeads`: `gap-0.5`, no separators, wrapping
  with the sidebar's width): up to five on one line at the default width.

### 8.2 The Hosts page

The probe hosts are edited once and written to **both** daemons (each daemon's row carries its own `ssh_alias`, picked from
that daemon's `GET /api/fleet/ssh-aliases`); a 409 from either reloads both and says the list changed elsewhere. The `fleet`
row (order, the alias for the other daemon host) is edited on each daemon host's page.

**Probe hosts have their own entries (user, 10-16; F-2d)**, replacing the editing dialog in the overview:
- The Hosts page's sidebar lists them under a heading 「觀察主機」, after the paired hosts: the badge, the name and a status
  dot from the fleet (ok / offline / error); a `+` on the heading adds one. A probe host has one page, no sub-pages.
- The page: a header (badge, name, the zone word or 離線, 「量測：{primary}（主）」, the last good reading's time when offline);
  **樣式與連線** inline — name, icon, colour (the existing pickers) and the SSH alias per daemon host; 儲存 writes both
  daemons as before; 刪除 with a confirmation.
- **連線檢查**: one line per daemon host (each reaches the host through its own alias): 「SSH 金鑰」 and 「量測讀數」, each
  ✓, ✗ with the reason (金鑰被拒／主機金鑰不符／SSH 設定裡沒有這個別名；連不到主機；讀不到量測), or — while unknown, from
  that daemon's `POST /api/fleet/probe-check`. It runs when the page opens, after a save that changed an alias, and on
  「重新檢查」. A daemon host that is offline shows 「{host} 離線，無法檢查」.
- The overview's section becomes a short list of the probe hosts (badge, name, status) whose rows open their entries, with
  「新增觀察主機」; it no longer edits.
- No pairing: a probe host is reached by SSH only.

## 9. Out of scope

iOS (later); alerts or pushes on zone changes (the watcher's job); any history on the daemon side; probing hosts the
primary cannot reach.

## 10. Phases (each ≤ 800 lines / 20 files)

| phase | side | content |
|---|---|---|
| F-1a | daemon | merged (rev 1 leaf package) |
| F-0 | daemon | the §7 types, `Proc`, `LocalSourceKey`, `RoleView` / `RoleState`, `FakeLocal` (#2969) |
| F-1b1 | daemon | the `fleet` and `probe_hosts` host config rows (normalisers, lenient read, GET fields, PUT routes, readers) |
| F-1b2 | daemon | module skeleton and registration; `state.json`; the role engine (§4); the two peer routes and their policy entries |
| F-1c1 | daemon | `internal/fleet` leaf: the loop scripts, the frame parser, composite load, zone latch, queue summary, captured fixtures |
| F-1c2 | daemon | sessions, the aggregator, `GET /api/fleet`, `fleet.changed` (opt-in, 1 s), `GET /api/fleet/ssh-aliases`, `fleet.v1`, device routes; wiring to `RoleView` |
| A line | daemon | `LocalSource` implementation (probe-dedupe plan §F.1), registered in resources' `Init` |
| F-2a1 | SPA | types / parse, `useFleetStore` (primary choice, re-read on stale, 「等待主方」), the in-memory history, `fleet.v1` probe, the `fleet=v1` event opt-in and branch (reworks #2879) |
| F-2a2 | SPA | the Hosts page probe-host section writing both daemons, the `fleet` row editor (reworks #2881; #2880, the editor extraction, stands) |
| F-2b | SPA | `CountBadge`, the sidebar host cells, the narrow column, interim click targets; i18n; tests |
| F-2b2 | SPA | the cells' restyle (§8.1, user 10-16) |
| F-2c | daemon | `POST /api/fleet/probe-check` (§6) |
| F-2d | SPA | probe hosts' own entries on the Hosts page, the connection check, the overview list (§8.2) |
