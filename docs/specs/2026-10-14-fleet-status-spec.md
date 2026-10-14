# Fleet status in the sidebar, and probe-only hosts — spec

> 2026-10-14, 88 (interface line). The user's request and answers are §2; the rest is 88's design on those answers.
> Facts (paths from the repo root) were read from origin/main on 2026-10-14.

## 1. Problem

The sidebar's bottom block (`spa/src/features/workspace/components/BottomNav.tsx`: 新工作區 / Workers / 主機 / 設定, and the rows ↔ one-row toggle) says nothing about the machines. The user runs four: **mlab** and **air26** (Purdex daemons), **air-2019** and **mserv** (no daemon). Whether one of them is overloaded, out of memory or off is found out only by logging in.

## 2. User decisions (2026-10-14, do not reopen)

1. The bottom block gains **host fleet status**: **expanded → full information; collapsed → the four buttons are not shown, only the fleet's compact information** (「展開時顯示完整資訊，收合時四顆按鈕不必顯示，只要選是主機群的精簡資訊」).
2. **air-2019 and mserv are collected and probed from mlab** (asked 「a19 和 mserv 的資訊能統一從 mlab 去拿/探勘嗎？」; answer: yes, over the SSH access mlab already has).
3. Answers to 88's five proposals, all 「ok」:
   1. Hosts shown: mlab, air26, air-2019, mserv.
   2. Expanded, one row per host: a zone light (normal / warning / danger / offline), CPU, memory, disk, and a rising / falling arrow. E.g. 「● mlab　CPU 210%　記憶體 72%　磁碟 64% ↑」.
   3. Collapsed: one row of small lights with short names; the most loaded host also shows its highest number. E.g. 「● mlab ● a26 ● a19 ● mserv　mlab CPU 210%」.
   4. Collapsed, the compact information is clickable and expands the block; expanded, the four buttons work as today.
   5. Mac App first; iOS later.
4. **Each host appears with its icon and colour** (the host's existing look) (「可以使用主機 icon、顏色來搭配顯示」).
5. **The Hosts page can add hosts that can be probed but have no daemon connection**, with the same basic settings (name, icon, colour) (「在 host 那裡補上『可以探查但是沒有 daemon 連線的主機』，一樣可以設定基本資訊」).

## 3. Facts this rests on

- **Daemon hosts in the App**: `HostConfig` (`spa/src/stores/useHostStore.ts:~30`: `id, name, ip, port, token, order`, look fields `colors / color / icon / iconWeight`), persisted `purdex-hosts`; looks also in `useHostLookStore` (synced). Rendering: `useHostLook` → `resolveHostColors` (`lib/host-color.ts:286`) → `<HostBadge>` (`components/HostBadge.tsx`), as `HostBadgePreview.tsx` does. Icon / colour editors: `components/hosts/HostIconField.tsx`, `HostColorField.tsx` (`HOST_COLOR_PRESETS`, Phosphor icon names). Nothing models a host without a daemon.
- **BottomNav**: mounted in `ActivityBarWide.tsx:379` (with the toggle) and `ActivityBarNarrow.tsx:192` (no toggle); `bottomNavCompact` in `useLayoutStore` (`purdex-layout`, device-local). Three looks: wide rows, wide one-row compact, narrow icon column.
- **Daemon resources**: `GET /api/resources` (`internal/module/resources/module.go:328`) → `HostUse` (`internal/resources/wire.go`: `cpu` = load1 / ncpu %, `mem` %, `load1/5/15`, `ncpu`, `pressure`, `zone`, `trend`, `zone_since`), sampled every 5 s. Zone rules `internal/resources/hostzone.go` `ZoneLatch.Update` (danger: pressure ≥ 4 or mem ≥ MemDanger; warning: pressure ≥ 2, mem ≥ MemWarn, or load1 ≥ ncpu; hysteresis), trend `internal/resources/trend.go`. **No disk**: disk is in the monitor module (`GET /api/monitor/snapshot`, `unix.Statfs("/")`). `resources.changed` is pushed only while leases exist. Device tokens may read neither (`cmd/pdx/device_scope.go:13`).
- **No SSH anywhere in the daemon**; nothing parses `~/.ssh/config`. Measured today (J-1d, 53-pe): from the daemon's launch context on mlab, SSH with key files works without an agent. mlab's `~/.ssh/config` has `air19` (key `id_air2019`, the remote accepts only from 100.64.0.2) and `mserv` (key `id_mserv`, `from=`-restricted).
- **Admin write APIs**: `hostconfig` module `PUT /api/hostconfig/<key>` with `putHandler(Key, normalizeX)` (`internal/module/hostconfig/module.go:61-71`); device tokens refused (only `GET /api/hostconfig` is allowed).

## 4. Hosts in the fleet

- **Daemon hosts**: every host in the App's host list (today mlab, air26). Status from that daemon's own `GET /api/fleet` (§6) while connected; **offline** when the App cannot reach it.
- **Probe-only hosts** (decision 5): defined on a daemon host — the **prober** (mlab by default) — as `{id, name, ssh_alias, icon, colors, icon_weight}`; the prober probes them over SSH (§5). Their definition (look included) lives in the prober's host config, so every App connected to mlab sees the same names, icons and colours.
- Order: the App's host order, then each prober's probe-only hosts in their stored order.

## 5. Probing (daemon, the prober)

- Every 60 s per probe-only host, one call: `ssh -o BatchMode=yes -o ConnectTimeout=5 -o StrictHostKeyChecking=yes <alias> <fixed script>`, killed after 10 s. No other command, no argument from the App reaches the remote side.
- `ssh_alias` must be a literal `Host` name in the daemon user's `~/.ssh/config` (no wildcard entries), matching `^[A-Za-z0-9._-]{1,64}$`; anything else is refused when it is saved.
- The fixed script prints, in one round trip: `uname -s`; on Darwin `sysctl -n hw.ncpu hw.memsize vm.loadavg`, `vm_stat`, `df -k /System/Volumes/Data` (fallback `/`); on Linux `nproc`, `cat /proc/loadavg`, `cat /proc/meminfo`, `df -kP /`. Parsed into `{os, ncpu, load1, cpu (load1 / ncpu %), mem %, disk %}`.
- **Zone**: the resources rule (`ZoneLatch`, pressure 0 for these hosts) plus disk: disk ≥ 85 % → warning, ≥ 95 % → danger (both apply to daemon hosts too, so every host is judged the same way). Trend from a ring of the last 10 samples, the resources trend rule.
- **Status**: `ok`; `offline` (SSH could not connect or timed out — `last_ok_at` kept); `error` (connected but the output did not parse — the reason kept, one log line per hour).

## 6. API and events (capability `fleet.v1`)

- `GET /api/fleet` on every daemon host → `{self: {cpu, mem, disk, load1, ncpu, zone, trend, sampled_at}, probes: [{id, name, ssh_alias, icon, colors, icon_weight, os, status, cpu, mem, disk, load1, ncpu, zone, trend, sampled_at, last_ok_at, error}]}`. `self` combines the resources sample and the monitor's disk.
- Host event `fleet.changed` (the same payload) when a zone, a status or a rounded number changes, at most once per 5 s; the App never polls.
- `GET /api/hostconfig` carries `probe_hosts`; `PUT /api/hostconfig/probe-hosts` writes them (admin only, device tokens refused). `GET /api/fleet/ssh-aliases` lists the eligible `Host` names (admin only) for the Hosts page's picker.
- Device tokens may read `GET /api/fleet` (for iOS later).

## 7. Mac App

### 7.1 The bottom block (decisions 1, 3, 4)

- **Expanded (the rows look)**: the fleet's rows sit above the four labelled buttons. A row: the host's `HostBadge` (icon + colour), its name, the zone light, `CPU n%`, `記憶體 n%`, `磁碟 n%`, the trend arrow (↑ rising, ↓ falling, nothing when flat). Offline: the light grey and 「離線（n 分鐘前）」 instead of the numbers. A row's click opens that host on the Hosts page.
- **Collapsed (the one-row look)**: the four icon buttons are replaced by the compact strip: each host's small badge with its zone light, then the most loaded host's short name and its highest number (「mlab CPU 210%」; nothing when all are normal). A click expands the block; the toggle stays where it is.
- **Narrow activity bar** (icon column, no toggle): one fleet light above the four buttons, coloured by the worst zone; hovering lists the hosts and their numbers.
- Most loaded = the worst zone; ties broken by the highest of CPU, memory, disk.

### 7.2 The Hosts page (decision 5)

- Under each daemon host with `fleet.v1`, a section 「只探查的主機」 lists its probe-only hosts and has 「新增」.
- The add / edit dialog: name, SSH host (a picker of `GET /api/fleet/ssh-aliases`), icon and colour (the existing `HostIconField` / `HostColorField`); saved with `PUT /api/hostconfig/probe-hosts`. Remove asks once.
- A probe-only host's look renders with the same `HostBadge`; it is not a daemon host — no terminal, no sessions, not selectable as a tab's host.

## 8. Out of scope

iOS (later); probing hosts not reachable from the prober; alerts or pushes on zone changes; history graphs.

## 9. Phases (each ≤ 800 lines / 20 files)

| phase | side | content |
|---|---|---|
| F-1a | daemon | `fleet` module: probe-host config (`hostconfig` key, normaliser, `~/.ssh/config` alias reader), the prober (ssh call, Darwin / Linux parsers, zone with disk, trend, status) |
| F-1b | daemon | `GET /api/fleet` (self from resources + monitor disk, probes), `fleet.changed`, `GET /api/fleet/ssh-aliases`, `fleet.v1`, device route |
| F-2a | SPA | `useFleetStore` (per daemon host: self + probes; offline from the connection state), `fleet.v1` probe, `fleet.changed` branch; the Hosts page section and dialog |
| F-2b | SPA | the bottom block: expanded rows, collapsed strip, narrow light; i18n; tests |
