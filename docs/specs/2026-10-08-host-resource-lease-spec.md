# Host resource lease (spec)

Date: 2026-10-08. Coordinator: purdex-1f (`mlab/_vqnjx1`). Status: **user decisions final (§1, incl. R5–R8 of 2026-10-08 23:4x, R9–R10 of 2026-10-09)**; plan pending.
Line: lead/member (A). Order (user, 2026-10-08): U23 → **this** → task/report spec → U24 adopt/release.
Research: `docs/research/2026-10-08-agent-teams-and-workflow.md` §5(a). Facts: §3 below (gathered 2026-10-08, file:line in each item).
Plan: `docs/plans/2026-10-08-host-resource-lease-plan.md`.

> **Edits by member δ (`mlab/_bx5os3`) when the spec entered the repo, 2026-10-08.** Every change to the coordinator's text is marked **〔δ〕**: §3 gains the plan-time measurements M-R1–M-R3 (§3.1); D-1 names its memory source; §5 Mod gains two facts from M-R2; §8 marks the measurements done. Nothing in §1 changed.

## 1. User decisions (2026-10-08, do not reopen)

| # | Decision |
|---|---|
| R1 | 資源池以**整台主機（daemon）**為單位；**每個 session 都能自己申請**（不限 member，不經 lead 核准）。多個 lead 之間自然看得到彼此。 |
| R2 | 模型：**容量假定 100**；申請時先給一個概略估值；之後**觀察實際用量**，剩下的才可分配。 |
| R3 | 限制要正視：只能攔截已知類型、無法判斷細節大小；**強制擋容易誤判**，整體要可調節；這套會增加流程複雜度，要分段。 |
| R4 | 先研究官方 Agent Teams／workflow 的設計再開發（已完成，#1966）。 |
| R5 | **「主機滿了」＝1 分鐘 load 平均等於核心數，或記憶體用到 90%**（取較高者）；記憶體壓力「警告」當 90、「嚴重」當 100。 |
| R6 | **排隊最多等 5 分鐘，到時放行**，並記一筆「超量」（`pdx lease ls` 看得到）。 |
| R7 | **完整 vitest 沒帶 `--maxWorkers` 時，mod 自動補 `--maxWorkers=3`**（不擋，只改小，並告訴 agent 改了什麼）。 |
| R8 | **背景執行（`run_in_background`）的重指令 v1 不攔**，只靠量測擋住後面的申請。 |
| R9 | （2026-10-09，P0 驗收後，選 A）**容量 100 只在重指令之間分配，每個重指令照估值佔額度；主機實際用量只用來判斷「滿了沒」，滿了就排隊，最多等 5 分鐘。**（取代 R2「剩下的才可分配」裡「扣掉主機實際用量」的讀法：平常 session 的零碎用量不扣額度。） |
| R10 | （2026-10-09）「要補個監控紀錄，我們需要事後確認規則是否合適」：每一次准入的判斷與當時的主機狀態都要留紀錄，並能事後彙整成報告。 |

## 2. Why

- 2026-10-08: several sessions ran full test suites at once; mlab (10 cores, 16 GB) reached load 50–67 and the OS killed a full vitest for low memory (memory `feedback_test_resource_limits`). A single worktree's full vitest spawns ~39 node processes.
- Today the only guard is text in briefs ("full vitest only before merge, `--maxWorkers=3`, ask the coordinator first"). With two leads and up to six members on one host, a human-relayed slot does not scale.
- Neither official mechanism (Agent Teams, workflow) controls child-process load; both cap only agent count or tokens (research §4).

## 3. Facts

- **The mod can rewrite a tool's input and wait.** `tool.call`'s `next(e)` takes a modified `e` (Bash `command` re-validated by the schema; `tool`, `tool_use_id`, `agentId` are reserved) [types `T:5573-5582`, `T:12570-12594`]. The hook's 10 s budget pauses while `next(e)` or any `$` call is in flight [`T:5070-5100`]; `$.process.run` waits up to **10 minutes** [`T:3470-3487`]. A hook that fails is skipped and the tool runs (fail-open) unless a `.catch` refuses [`T:3908-3925`]. No repo code rewrites `e.command` yet (unmeasured on a real session).
- **Lease shape to copy.** `approval_requests`: create with an idempotency id → long-poll `?wait=25` → each poll renews `lease_until` (`LeaseS=30`, `MAX`-only) → a 1 s sweeper closes expired rows with a CAS (`CloseIfExpired`); boot grace `max(lease_until, boot+30 s)`; origin liveness by `LeadPresence(session_id, pid, proc_start)` (Live／Gone／Unknown; unknown counts as alive) [`internal/module/team/store.go:308-407`, `sweeper.go:14-83`, `internal/module/peers/origin_resolver.go:132-181`, `internal/team/wire.go:46-52`]. The client loop (35 s per attempt, 3 hung polls → unavailable, SIGINT → best-effort cancel) is `cmd/pdx/lead.go:195-228`.
- **Measurement is cheap.** `sysctl -n vm.loadavg hw.ncpu hw.memsize kern.memorystatus_vm_pressure_level` ≈ 3 ms, no root (mlab: `{6.48 10.36 9.78}`, 10, 16 GiB, level 1). `kern.proc.all` (742 processes) ≈ 0.6 ms without a fork; `ps -axo pid,ppid,pcpu,rss` ≈ 2.1 ms per fork [memory `reference_process_snapshot_facts`]. `internal/module/monitor` already samples host CPU (`kern.cp_time`) and memory (`hw.memsize` + `vm_stat`) and per-process `pcpu`／`rss` [`host_system.go:56-98`, `process.go:44`]. Pressure levels: 1 normal, 2 warn, 4 critical (XNU enum; only 1 observed).
- **Per-session attribution is available.** `agent_frames` holds each pane agent's `PID`／`ProcessStartTime` [`internal/store/frames.go:20-30,96`]; team members store `pane_id, pid, proc_start` [`team_store.go:59,71`]. A session's load = its CC process plus all descendants.
- **Hot paths must not grow.** #1777 (per-pane tmux forks on every hook event) and #1794 (2 s sweep forks): sampling runs on its own ticker, never on the hook path.
- **Host settings.** Per-host keys live in `host_config` (`internal/module/hostconfig/store.go:44-60`), either as a generic collection (`PUT /api/hostconfig/<name>`, strict normalizer) or with a dedicated writer (like `unattended`).
- **Heavy commands seen in this repo.** `npx vitest run` (no file args), `go test [-race] ./...`, `make test` (`go test -race -count=1 ./...`), `pnpm run build` (`tsc -b && vite build`), `pnpm run electron:build`. Affected-only runs name files (`*.test.ts`), `-t`, `-run`, or a package path. Agents often prefix `cd <worktree> && `.

### 3.1 Plan-time measurements 〔δ, 2026-10-08 23:45–23:55, mlab, CC 2.1.294〕

**M-R1 — host figures without a fork.**
- One batch of `unix.SysctlUint32/Uint64/Raw` for `vm.loadavg`, `hw.ncpu`, `hw.memsize`, `kern.memorystatus_vm_pressure_level` and `kern.memorystatus_level` takes **20–55 µs** in-process (Go, x/sys, no cgo). `vm.loadavg` is 24 bytes: three `uint32` fixed-point values, 4 bytes padding, then `fscale` as `uint64` (load1 = `ldavg[0] / fscale`).
- **The vm_stat figures D-1 uses (free + inactive + speculative) have no fork-free source.** `host_statistics64` is a Mach call; the repo has no cgo and the daemon ships without it. sysctl gives `vm.page_free_count` and `vm.page_speculative_count` but not inactive: `vm.page_pageable_internal_count + vm.page_pageable_external_count` is active + inactive + speculative together and cannot be split. So the sampler forks `vm_stat` once per tick: **3.4–6.5 ms** at load 17 (the parser already exists, `internal/module/monitor/host_system.go:100`).
- `kern.memorystatus_level` (no fork) is the kernel's free percentage, the same number `memory_pressure -Q` prints. It is **not** D-1's figure: in four samples at load 17, `100 − memorystatus_level` read 50–54 % in use while D-1's vm_stat formula read 75–79 %. D-1 keeps vm_stat (it is the stricter reading, and R5's 90 % threshold is written against "memory in use"); the snapshot also carries `memorystatus_level` so P3 can compare them.
- **Pressure level 2 (warn) observed** once during these samples (level 1 the other times), at `memorystatus_level = 48`, vm_stat in use ≈ 76 %. D-1's floor (level 2 counts as ≥ 90) therefore does fire in normal multi-session use, not only in emergencies.
- **Per-process CPU and memory need a fork.** In `kern.proc.all` (784 records, 0.77 ms) `p_pctcpu` and `e_xrssize` are 0 for every process; `proc_pidinfo` is libproc (cgo). So the sampler forks `ps -axo pid=,ppid=,pcpu=,rss=` once per tick: **16–27 ms at load 17** (the 2.1 ms of memory `reference_process_snapshot_facts` was measured on an idle host). Two forks per 5 s tick in total. Start times for liveness still come from `kern.proc.all` (`agent.SnapshotProcesses`, no fork).

**M-R2 — a mod rewrite of `e.command` reaches the Bash tool.** Throwaway plugin (`--plugin-dir`, name `mr2probe`, one module registering `tool.call {tool:'Bash'}`), `claude -p --model haiku` in a throwaway tmux session, the installed purdex mod loaded beside it (its `events.js` unmatched `tool.call` included). The hook, for a command containing a marker, awaited `$.process.run(['/bin/sleep','12'])`, then `next({...e, command: rewritten + ' && echo MR2-APPENDED'})`, then returned `{...r, context: [...]}`.
- The tool ran the **rewritten** command (output `MR2-REWRITTEN` / `MR2-APPENDED`); the model's own `tool_use.input.command` in the transcript stays the **original** text.
- The 12 s wait before `next(e)` did not trip the 10 s hook budget (`waited=12005ms`, then the tool ran): a `$` call in flight stops the budget, as `T:5070-5100` says.
- The returned `context` reached the model as `tool.call hook additional context: <text>`, after the tool result.
- **The model notices an unannounced rewrite.** Both runs answered that the output "does not match the command I ran" and that "something other than my command" produced it. The context must say plainly that the Purdex mod changed the command and how (§5 Mod).
- `e` keys for a foreground call: `command, description, tool, tool_use_id` (no `run_in_background` key at all). With `run_in_background: true` the key is present and `true`, and `next(e)` resolves at once with "Command running in background with ID …" while the command keeps running — a lease released in `finally` would be released immediately (R8's reason, now measured).

**M-R3 — a session's process tree from its recorded pid.**
- All 27 non-ended `agent_frames` rows on mlab had `pid` = the agent binary itself (24 `claude`, 3 the native `codex` binary under its node wrapper) and `ppid` = the pane's shell (`#{pane_pid}`); no root frame pointed at a proxy or shell. Team members' `team_members.pid` is the same CC pid (e.g. this member: frame pid 62419 = member pid 62419 = `claude`).
- A CC session's live children are its stdio MCP servers (here a Python `workspace-mcp`, 17 MB), `caffeinate`, and each Bash tool's shell with everything it starts; so "the CC process plus all descendants" is the session's load, as §3 says. The CC process alone is 115–378 MB RSS across the 24 sessions.
- The peers registry (`~/.claude/sessions/<pid>.json`, `internal/peers/registry.go:50-62`) lists every CC session on the host, including ones outside tmux, with `PID`, `SessionID` and `ProcStart`; its `Liveness` hooks (`registry.go:87-101`) can be served from one process snapshot, so reading it costs no fork. Proxy entries (`IsProxy`) are skipped. Codex frames are not CC sessions; per D-6 codex counts only through measurement.

### 3.2 P0 acceptance numbers 〔δ, 2026-10-09 01:13–01:23, mlab, alpha.607〕

61 polls of `GET /api/resources`, one every 10 s for 602 s, on a host running 24–26 CC sessions (10 cores, 16 GiB). Raw lines: member δ's scratchpad (`p0-accept.jsonl`); the figures below are what the decisions rest on.

| figure | min | p50 | p90 | max |
|---|---|---|---|---|
| load1 | 6.70 | 9.46 | 10.60 | 11.57 |
| `host.cpu` (load1 / ncpu, %) | 67 | 95 | 106 | 116 |
| `host.mem` (vm_stat formula, %) | 67.1 | 68.7 | 69.9 | 70.7 |
| `host.measured` (D-1) | 69 | 95 | 100 | 100 |
| `sample_ms` | 16 | 24 | 39 | 65 |

- **`full` was true in 20 of 61 polls (33 %)**, all of them through `load1 >= ncpu`; memory ≥ 90 % and pressure ≥ warn never occurred (pressure level 1 throughout). Runs of consecutive `full` polls: 50 s, 10 s, 60 s, 30 s, 50 s. `measured >= 100` in 23 polls, `>= 90` in 35, `>= 80` in 47; `measured <= 55` (room for a weight-45 request under D-2's additive `unleased`) in **0**, `<= 70` in 4.
- Memory: the vm_stat formula read 69 % on average where `100 − memorystatus_level` read 37 % (31 points apart). Neither is near R5's 90 %: memory was not the binding limit that night.
- CPU cross-check: `iostat` showed us 26 / sy 46 / id 28 (busy ≈ 72 %, mostly kernel time); the sum of `ps` pcpu was only 47 % (kernel time is missing from it); load1 / ncpu read 79–94 %. load1 runs a little high but moves with real CPU use. **`sysctl kern.cp_time` does not exist on macOS 26** (`unknown oid`), so there is no fork-free CPU-tick source; D-1's CPU term stays load1 (issue #2013 covers the monitor module, which reads that oid).
- Cost: `sample_ms` median 24, max 65 per 5 s tick (≈ 0.5 % of one core). The daemon itself ran at `ps` pcpu 0.6–222 %; a 3 s `sample` of its stacks showed no `resources` frame, the time was in the agent module (hook events, tmux forks, trace pruning — the #1777／#1794 family). That is an absence of evidence, not a before/after comparison.
- Per-session cross-check (hand sum of the `ps` tree against the API): two quiet sessions within 0.2 %; δ's own, 7.8 % (a process joined its tree between the two reads).
- **Consequence for D-2.** The baseline of a busy host (`measured` 69–100 with nothing leased) is above the room a `test-full` needs, so with `unleased` as an additive term every heavy request would wait its whole deadline and be released as an overrun. The harm that started this work was load 50–67 and ~32 % free memory, five to six times the core count, while R5's line (load = cores) was crossed a third of the time. Whether `unleased` stays additive is the lead's question to the user (it changes R2's "what is left may be allocated" from "minus the host's real use" to "minus other heavy commands"); the two candidate formulas are in the plan's decision list.

**M-R4 — the P2 mod in a real interactive session 〔η, 2026-10-09, mlab, CC 2.1.295〕.** Throwaway tmux server (`-L res-p2-accept`), `claude --model haiku --plugin-dir <copy of the built mod + the installed pdx.json> --allowedTools Bash` in a scratch go module; `mode=lease`, `kinds.lint-full=95` for the run (restored). Script: `mr4-accept.sh` (scratchpad).
- A narrow command (`go vet ./cmd/x`) was not intercepted: no lease row.
- With `pdx lease run --weight 90 -- sleep 40` held from another shell, the session's `go vet ./...` (lint-full) showed up in `ls` as a waiter, ran when the holder ended, and its lease ended `released` after 34.4 s of waiting. Exactly one acquire by the session (no second copy of the mod, though `CLAUDE_CODE_PLUGIN_DIRS` also names the installed dir).
- The model received the context line and acted on it: it answered "這次指令等了約 34 秒才執行，不是卡住，所以沒有重試" (the line itself is not drawn in the terminal UI, so the transcript grep found nothing; the model's reply is the evidence).
- Note for the script: the folder-trust prompt defaults to "No, exit", so a bare Enter exits the session; send `Down Enter`.

## 4. Model (derived from R1–R3)

**D-1 · Units.** 100 = the host fully busy. A lease's **weight** is its estimated share of the host, as the larger of its CPU share and its memory share. The host's **measured use** is the larger of `load1 / ncpu` and memory in use (from `vm_stat`: `1 − (free + inactive + speculative) / total`), each as a percentage; memory pressure level 2 (warn) counts as at least 90, level 4 (critical) as 100. 〔δ〕 Sources (M-R1): load, ncpu, memsize and pressure from sysctl without a fork; free / inactive / speculative from one `vm_stat` fork per tick (no fork-free source exists without cgo). 〔δ, lead-approved 2026-10-09〕 The published **`full`** flag has a hysteresis: it turns on exactly as R5 says and turns off only once load1 < 0.9 × ncpu, memory in use < 0.9 × 90 % and pressure is normal (P0 acceptance: load1 wobbled between 9.5 and 10.5 for ten minutes). A session's (or lease's) measured use is the larger of `Σ pcpu / ncpu` and `Σ rss / memsize` over its process tree, from one `ps` fork per tick.

**D-2 · Admission (R9).** A request of weight `w` is granted when **both**
1. `Σ charge(active leases) + w ≤ 100`, and
2. the host is **not full** (D-1's `full`, with its hysteresis: entered at `load1 ≥ ncpu`, memory ≥ 90 % or pressure ≥ warn; left only when `load1 < 0.9 × ncpu`, memory < 81 % and pressure is normal).

- `charge(lease)` = its weight until it has run `warmup` seconds (default 20 s), then the EWMA of its own measured use (its process tree), but never below `floor × weight` (default 0.5).
- **Unleased use is not an additive term.** It is still measured (`unleased` = host measured use − Σ measured use of active leases, never negative) and recorded with every decision (D-8), so the rule can be judged afterwards. Unknown or un-intercepted heavy work holds new grants back only through `full` (R3).
- A request heavier than the whole capacity is granted only when no other lease is active (and the host is not full). "Active" is counted **dynamically** during one admission pass: the leases held when the pass starts plus every grant the pass has already made (normal or overrun), so two such requests are never granted together.
- Why (P0, §3.2): with `unleased` additive, a busy host's baseline (`measured` 69–100 with nothing leased) left no room for a `test-full`; every heavy request would have waited its full deadline. Under R9, on the same night, about two thirds of the time a request is granted at once, and a `full` run lasted at most 60 s.

**D-3 · Queue, never refuse.** Requests wait FIFO (a later light request may pass an earlier heavy one only if the heavy one still does not fit — no starvation guard beyond FIFO in v1). Each waiter has a deadline (**default 5 min**, R6; configurable up to the mod's 10 min maximum). **On the deadline the request is granted anyway** with `overrun: true` (logged, counted, visible). Admission is advice that becomes a wait, never a refusal (R3: no misjudged blocks).

**D-4 · Holding and release.** A lease belongs to an **origin** `{session_id, pid, proc_start}` and optionally a `tool_use_id`. It ends when the holder releases it, when the origin process is Gone (`LeadPresence` rule, unknown = alive), or at `max_hold` (default 60 min, then logged as `expired`). Waiters long-poll and renew like approvals; holders are checked by liveness on the sweeper's tick, not by polling. Boot: leases and waiters persist (SQLite), with the 30 s boot grace.

**D-5 · Fail open everywhere.** daemon unreachable, a bad response, or a crashed wrapper → the command runs. The pool is advice; correctness never depends on it.

**D-6 · Scope.** Per host, per daemon; not in Profile Sync; a cross-host member uses its own host's pool. Codex review sandboxes, Nexen executions and processes outside Purdex are never intercepted; they count only through measurement (D-2's `full`, and the `unleased` figure D-8 records).

**D-7 · Kinds and default weights** (host setting `resources`, editable):

| kind | matches (P2) | weight |
|---|---|---|
| `test-full` | `vitest run` with no file／`-t` argument; `go test … ./...`; `make test` | 35 〔δ: was 45; R7 caps vitest at 3 workers ≈ 3 cores ≈ 30 % plus ≈ 10 % memory; lead-approved 2026-10-09〕 |
| `build` | `pnpm run build`, `tsc -b`, `vite build`, `electron:build`, `electron-vite build` | 35 |
| `test-pkg` | `go test -race <pkg>` (one package path) | 15 |
| `lint-full` | `go vet ./...`, `eslint .`, `pnpm run lint` | 10 |
| (explicit) | `pdx lease run --weight N` | N |

Affected-only runs (file names, `-run`, `-t`) are not intercepted.

**D-8 · Monitoring record (R10).** Three parts, all in `resources.db`, all written outside `stateMu`'s slow paths (the decision row is the same single-row write the grant already does).

1. **Every decision.** When a lease is granted (at once, after waiting, or as an overrun), its row also records the host state the decision saw: `dec_load1`, `dec_ncpu`, `dec_mem`, `dec_measured`, `dec_full`, `dec_sum_charge`, `dec_unleased`, `dec_weight` (= w), `dec_path` (`immediate`｜`waited`｜`overrun`), `waited_ms`, `dec_recorded` (0 until the grant writes the snapshot, then 1: rows granted before this field existed stay 0 and **the report counts only `dec_recorded = 1`**, listing the others as "not recorded"), and **`would_wait_r2`** — whether the pre-R9 formula (`Σ charge + unleased + w > 100`) would have held it back at that moment. With the lease's own measured peak／mean／EWMA and `end_reason` (already on the row), one row answers "was this wait needed, and was the weight right".
2. **Host timeline.** One row per minute in a new table `host_minutes`: `at`, `load1`, `ncpu`, `mem`, `measured`, `full`, `full_ticks` (samples of the minute with `full` on), `full_starts` (off→on transitions in the minute), `full_longest_s` (longest `full` run in progress or ended in the minute, seconds), `held` (count), `heavy_held` (held leases with weight ≥ `heavy_min_weight`, host setting, default 30: the report asks whether the host holds up when two or more big leases — `test-full` and `build`, both 35 — run together, and 15 would count a single-package `go test -race` too), `sum_charge`, `waiting` (count), `unleased`. Written by the sampler goroutine. **Retention is 14 days for this table and for ended leases alike** (ended leases moving from their former shorter keep to 14 days in P1-2c); pruned with the existing hourly prune. About 20 000 rows at most.
3. **Report.** `pdx lease report [--since 24h] [--json]` and `GET /api/resources/report?since=<duration>` (`since` is at most 14 days, a longer one is a 400). The answer carries `coverage {from, to}` (the earliest stored minute row and the latest) and the count of leases "not recorded", so a partial period is visible:
   - requests per kind; how many were granted at once / after waiting / as overruns; wait p50／p90／max (nearest-rank over the stored `waited_ms` of recorded grants);
   - how many grants the pre-R9 formula would have held back (`would_wait_r2`);
   - share of time the host was `full` (Σ `full_ticks` × sample interval over the covered minutes), number of full runs (Σ `full_starts`) and the longest (max `full_longest_s`); a missing minute row ends nothing and adds nothing;
   - highest `load1` and memory over minutes with `heavy_held ≥ 2`;
   - per kind: measured peak and mean versus its weight (is 35 right for `test-full`?).

   The report reads only stored rows; it never samples.

**Known limit (session-new baseline).** A `session-new` lease's baseline is the processes under the session's agent pid at the sampler's last reading, up to one sampling interval (5 s) before the grant, with their start times; it is written by the grant's own statement, so a held lease always has it, and the process table is never read under `stateMu`. A child that started in that last interval is not in the baseline and is charged to the lease: the error is on the high side, which is the safe one for admission.

**Review point.** After P2 (mod interception) has run in `advise` mode for one day and then in `lease` mode for about three days, the coordinator runs the report and brings the user the numbers in plain words with a recommendation (keep the rule, move a threshold, change a weight).

## 5. Surfaces

- **CLI.**
  - `pdx lease run [--kind K | --weight N] [--wait 5m] -- <cmd…>`: acquire (long-poll), run the child, release on exit; forwards signals; prints one line to stderr when it waited (`waited 37s for the host (load 72/100)`); fail-open (D-5).
  - `pdx lease acquire …` / `pdx lease release <id>`: the primitives the mod uses.
  - `pdx lease ls`: capacity, measured use, holders (session, kind, weight, charge, age), waiters (position, waited).
  - `pdx team` gains CPU and MEM columns per member (from D-1 attribution).
- **API.** `POST /api/resources/leases` (create; idempotency id), `GET /api/resources/leases/{id}?wait=25` (long-poll, renews), `DELETE …/{id}` (release／cancel), `GET /api/resources` (snapshot: capacity, measured, leases, waiters, per-session use). WS: `resources.changed` at most once per 5 s while anything is active or waiting.
- **Mod (P2).** `tool.call{tool:'Bash'}` (matcher; M-U1-3 rules; fail-open `.catch`): classify the command (D-7); not heavy, already wrapped in `pdx lease run`, or `run_in_background` (R8) → `next(e)` unchanged. Heavy → (a) **rewrite** a full vitest without `--maxWorkers` to add `--maxWorkers=3` (R7); (b) `$.process.run(['pdx','lease','acquire',…])` (waits up to the deadline); (c) `next(e)`; (d) release in `finally`. When it waited, tell the model in the tool's context ("waited 37 s for host capacity"), so it does not retry or assume a hang. 〔δ, M-R2〕 The model sees only its original command in its own transcript, and an unannounced rewrite made it report tampering; so whenever the command was rewritten the context says so in plain words (e.g. "Purdex mod 把 `--maxWorkers=3` 加進這個指令（主機資源規則），實際執行的是：<command>"). `run_in_background` is absent on a foreground call and `true` on a background one.
- **App.** A host-level indicator and a list (who holds, who waits) — placement is the interface lead's call (§7).
- **Skill.** Members and leads use `pdx lease run` for heavy commands not covered by the mod (e.g. when they script them); the "ask the coordinator for a full-vitest slot" rule is retired once P2 ships.

## 6. Phases (each phase is useful alone; each PR ≤ 800 lines or ≤ 20 files)

| Phase | Content | Deploy |
|---|---|---|
| **P0 measure** | Sampler (5 s, own ticker): host D-1 values via sysctl (no fork) and one process-table read with cpu/rss; per-session attribution; `GET /api/resources` (no leases yet); `pdx team` CPU/MEM; `pdx lease ls` (measured only) | daemon |
| **P1 voluntary leases** | `resource_leases` table, admission D-2, queue D-3, decision record (D-8.1), host timeline (D-8.2), `pdx lease report` (D-8.3), liveness D-4, `pdx lease run／acquire／release／ls`, host setting `resources` (D-7 weights, deadline, warmup, floor, max_hold), WS event, skill text | daemon + CLI + setup |
| **P2 mod interception** | D-7 classifier, flag rewrite, acquire／release around `next(e)`, waited-context; guard tests | daemon + mod (`pdx setup`) |
| **P3 learning** | Per-kind EWMA of observed charge feeds the default weight (bounded, shown in `pdx lease ls`); optional per-kind concurrency cap (e.g. one `test-full` per host) | daemon |
| **App** | Indicator + holders／waiters list | SPA (interface lead) |

Each phase can be switched off in the host setting (`resources.mode`: `off | measure | advise | lease`).

## 7. Coordination

- **mod**: P2 hooks `tool.call{tool:'Bash'}` with a matcher; `events.js` already registers `tool.call` without one (guard test is fail-closed; `$` never crosses an import — M-U1-2／M-U1-5). The interception lives in its own file registered from `register.js` (tell the interface lead before touching `register.js`).
- **App UI** belongs to the interface lead (`mlab/_b84f5i`): ask where the indicator goes (status bar is U3's).
- **`pdx team`** columns: A line (this spec).
- **Hot paths** (#1777, #1794): sampling never runs on hook events.

## 8. Open questions

- None for the user. Weights in D-7 are defaults; P3 tunes them from observed charge (the user did not object to starting with this set).
- 〔δ〕 Done — results in §3.1. Original text: Plan-time measurements: (M-R1) vm_stat-equivalent memory figures without a fork (`host_statistics64`); (M-R2) whether a mod `tool.call` rewrite of `e.command` reaches the Bash tool as rewritten (types say yes, no repo sample); (M-R3) attribution of a session's process tree from `agent_frames.PID` (CC pid vs proxy pid).
