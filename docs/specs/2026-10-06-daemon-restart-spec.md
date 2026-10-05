# Daemon restart button — spec

Owner (coordinator): `mlab/purdex-9b` (`mlab/_0le0d2`). Implementer: the session this was delegated to. Spec questions go to the coordinator. Do not ask the user; the coordinator derives from these decisions or asks once.

## 1. User decisions (2026-10-06, do not reopen)

| # | Decision |
|---|---|
| R1 | A "restart daemon" button on **each host's own page** (Hosts → that host). |
| R2 | On the **Development settings page**, a restart button for **this machine's own daemon**, the local one. Today it appears only when the binary on disk is newer than the one running. |
| R3 | On the **Nexen config screen** (Hosts → host → Nex), when a save says a restart is owed, a **"restart now"** button next to that message. |

Origin: after enabling Nexen on air26 the user found no way to restart. The only hint is the text `hosts.nex.config.restart_required` ("請在 {{host}} 重啟 daemon 才會生效（pdx stop && pdx start）").

## 2. Facts (measured on origin/main `6cbc29f7`, alpha.486)

- **No restart API.** The daemon has only the dev-mode `POST /api/dev/daemon/rebuild` (builds, then restarts; `internal/module/dev/module.go:176-181`).
- **`pdx start`** (`cmd/pdx/daemon.go:255-310`):
  - spawns `pdx serve` with `Setpgid`, stdout and stderr going to the log file;
  - waits for `/api/health`, then exits.
  - `serve` holds an exclusive flock on `<data_dir>/pdx.pid` (`acquirePidLock`, retried 5 × 50 ms by `mustAcquirePidLock`).
  - **Nothing supervises `serve` afterwards.** booter starts it only at boot, and there is no launchd job on mlab. A daemon that exits stays down.
- **The App-managed local daemon** (Electron, `electron/local-daemon/index.ts:541-552` `restartUnlocked`): stop, then `startDaemon`, then register. It refuses when the daemon is `external`, i.e. not started by the App. Exposed to the SPA as `localDaemonRestart` (`electron/preload.ts:178`). `LocalDaemonSection.tsx:123` shows the button only when `alive && !updateAvailable && (!running || restartPending)`.
- **Nexen config:**
  - `PUT /api/config` persists it, and nothing is applied live.
  - `GET /api/info` reports `nex.restart_required` (`internal/core/info_handler.go:65`).
  - `NexConfigForm.tsx:169` shows the hint.
- **Host page:** `OverviewSection.tsx` has a "daemon config" section (`hosts.daemon_config`, around :279) with sizing mode, detect commands and poll interval. `purdex_version` and `tmux_version` are in the separate "System Info" section (`hosts.system_info`, around :305). The R1 button still goes in "daemon config" (D8).
- **The process env is mutated after boot:** `locale.EnsureUTF8` sets `LANG` (`internal/locale/locale.go:72`), `tmuxenv.Prepare` drops `TMUX`/`TMUX_PANE` and may append to `PATH` (`internal/tmuxenv/tmuxenv.go:113-146`), and nex's PATH policy prepends `path_prepend` (`internal/module/nex/pathpolicy.go:76`).
- **A restart kills running worker turns.** Nexen is embedded in the daemon. A turn's process does not survive a restart: startup reconcile settles the turn as `orphaned`, puts the execution back to `idle`, and SIGKILLs a still-live orphan (Nexen `capability-matrix.md` #26 and #41). tmux sessions are a separate server and are unaffected. Terminal panes reconnect over WS.
- Dev mode is env-only (`PDX_DEV_MODE=1`), so a restart must keep the environment.

## 3. Behaviour

### 3.1 Daemon: `POST /api/daemon/restart`

- Auth is the same as the other host-admin endpoints (the host token). It works in non-dev mode.
- The daemon replies `202 {"boot_id": "<current>"}`, flushes the reply, and then restarts itself:
  1. Graceful shutdown, the same path `pdx stop` takes: stop accepting, close the stores with a WAL checkpoint, and let embedded Nexen shut down as it does on a normal stop.
  2. **Re-exec in place**: the same executable path (whatever binary is on disk now, so a pending update gets picked up), the same argv, the same environment.
  - Re-exec, not "exit and let something restart it": nothing supervises `serve` (§2).
  - Re-exec keeps the pid, so the App's ownership record and the pid file stay valid.
  - The pid lock is **handed across the exec**, so no concurrent `pdx start` can take the data dir in between (PR #1568 review):
    - its fd stays open, not close-on-exec, and is named in `PDX_PIDLOCK_FD`;
    - the new image adopts the lock it already holds, since a flock lives on the open file description;
    - if the hand-off fails, the new image re-acquires the lock through the existing retry.
  - If exec fails, log it and exit non-zero. The SPA then sees the host stay down (§3.3).
- While a restart is in progress, a second request gets `409 restart_in_progress`.
- Once the shutdown sequence has started for any other reason (a signal, or a Serve failure), the endpoint answers `503 {"error":"shutting_down"}`. It never gives a 202 that nothing will honour.
  - A restart accepted just before that point is still performed if a Serve failure started the shutdown.
  - If a signal started it, the signal wins (D4).
  - (PR #1568 review.)
- **Boot id:** `/api/health` (or `/api/info`; the plan picks one and says why) gains a `boot_id` that is new on every process start. The SPA uses it to know the restart really happened, not just that the host answered.
- Logged as one line with the requester's address: `daemon restart requested by …`.

**Coordinator-approved derivations (2026-10-06):**

- **D1:** `boot_id` goes on **`/api/health`**. Health is unauthenticated, so it also answers in pairing mode. It is cheap, while `/api/info` execs `tmux -V`. It is also already the liveness probe the SPA and `pdx start` use.
- **D2:** `boot_id` is 16 hex chars from `crypto/rand`, generated in `core.New`, so every process start (re-exec included) gets a new one.
- **D3:** "The same argv and environment" means the ones the process **started with**. They are captured at the top of `serve`, before the env mutations listed in §2, together with the executable path. If the exec read `os.Environ()` at exec time instead, every restart would stack another `path_prepend`, and an old `path_prepend` would survive the very config change the restart applies.
- **D4:** A SIGINT/SIGTERM that arrives while a restart's shutdown runs (for example `pdx stop`) turns the restart into a plain stop, with no re-exec. A second signal still exits immediately.
- **D5:** The `409` body also carries `boot_id`. A client that gets 409 follows the restart already under way to the same finish line.
- **D12:** When no restart hook is installed (for example `os.Executable` failed at boot), the endpoint answers `503 {"error":"restart_unavailable"}`. It must not give a 202 that nothing will honour.
- **D13: cleanup errors during a restart do not stop the re-exec, and they are not hidden** (coordinator-approved 2026-10-06, after the PR #1568 review). Cases: `StopModules` overruns the budget, a module's Stop or Close returns an error, or the HTTP shutdown is forced.
  - The daemon still re-execs. That has the same outcome as `pdx stop` followed by `pdx start`: the stop path also just logs these errors and exits. SQLite recovers its WAL on open, and nex's startup reconcile settles orphaned turns. Aborting would leave a remote host down until someone logs in.
  - It logs one warning line: `restart: continuing despite N cleanup error(s)`.
  - Before the exec it writes `<data_dir>/last-shutdown.json` as `{"at": "<RFC3339>", "errors": ["…", …]}` (mode 0600, written as tmp then renamed), and only when there were errors. If the write fails, the failure is logged and the restart goes ahead.
  - On start, the daemon reads the file once and deletes it. That happens after the pid lock, since only the owner of `data_dir` touches it. `GET /api/info` then reports `"last_shutdown": {"at", "errors", "boot_id": <this process's boot_id>}`, or `null` when there is nothing to report.
  - A corrupt file is deleted and logged.

### 3.2 SPA: one restart action, three entry points

One shared action, `restartDaemon(hostId)`:

- Local host whose daemon the App manages (`LocalDaemonSection` reports `managed`): use the Electron IPC `localDaemonRestart`.
- Every other host, including a local daemon that is `external`: use `POST /api/daemon/restart`.

**Confirm first** (`ConfirmDialog`). The body says:

- terminal connections on this host drop for a few seconds and come back by themselves; tmux sessions are not affected;
- **if the host has running workers** (`state === 'running'` in the per-host execution list): "N 個 worker 正在執行，重啟會中斷它們這一輪（之後可以繼續對話）". With no running workers this line is absent.

Entry points:

1. **R1, host page:** a "重新啟動 daemon" button in the Overview "daemon config" section, for any connected host.
2. **R2, Development page:** in the local daemon section (`LocalDaemonSection`), the restart button shows whenever the local daemon is alive and managed, not only when `restartPending`. When the local daemon is `external` but a configured host points at it (same bind:port), show the button using the API path. Otherwise do not show it, and give the existing external reason. The existing "update" button and `restartPending` text stay as they are.
3. **R3, Nex config:** next to the `restart_required` hint, a "立即重啟" button for that host.

**Coordinator-approved derivations (2026-10-06):**

- **D6:** A `404` from the endpoint means the daemon predates this feature. The SPA says: "這台 daemon 版本不支援遠端重啟，請在該主機執行 pdx stop && pdx start". This matters for Air26 and older hosts until they are updated.
- **D7:** Running workers are counted fresh when the confirm opens: `GET /api/nex/v1/executions?state=running`, following `next_cursor`, within a 3 s budget.
  - Nex is reported but not ready → 0.
  - No Nex info, or the list fails or times out → unknown. The dialog then shows "無法確認是否有 worker 正在執行；若有，重啟會中斷它們這一輪（之後可以繼續對話）".
  - Unknown is never shown as 0. The cached per-host list exists only while some view subscribes, so it cannot be relied on.
- **D8:** The R1 button sits in the "Daemon 設定" section. It shows while the host is connected **or** while a restart of that host is in progress, so the spinner stays when the host drops to `reconnecting` mid-restart.
- **D9:** R2: when the daemon is managed and alive, restart always shows, even next to "Update". This replaces the old "Update, else Restart — never both" rule.
- **D10:** R2: when the daemon is managed and alive but **not in the host list**, R2 keeps today's direct IPC restart, with no confirm and no boot-id check. There is no `hostId`, so there is no per-host state and no health to poll, and this app has no terminals or workers on it.

### 3.3 While restarting, and after

- The button shows a spinner and "重啟中…", and the other two entry points for the same host are disabled; the state is per host.
- **Done** when the host answers health with a **different `boot_id`**: success toast "daemon 已重新啟動". For R3, the `restart_required` hint is gone once `/api/info` is re-read.
- **Timeout 60 s** (the same window `pdx start` uses): error "daemon 沒有在 60 秒內回來" plus where to look: `~/.config/pdx/logs/pdx.log` on that host. Same if the API call itself fails, with its error.
- A host's WS-driven views (terminals, worker panes) reconnect through their existing paths. No new reconnect logic.

**Coordinator-approved derivation (2026-10-06):**

- **D11:** The success toast and the failure notices name the host, for example "mlab：daemon 已重新啟動". The toast is global, and more than one host can be restarting at once.
- **D13 (SPA half):** after the new `boot_id` answers, the SPA reads `/api/info` once (coordinator-approved 2026-10-06).
  - If `last_shutdown` is present, its `boot_id` equals the new boot id, and `errors` is non-empty, the success toast becomes "<主機>：daemon 已重新啟動，但關閉時有 N 個警告（見 ~/.config/pdx/logs/pdx.log）".
  - If that read fails, the plain success toast stays.

## 4. Tests

- **Daemon:**
  - the endpoint replies 202 before shutting down;
  - a second request gets 409;
  - `boot_id` differs across starts;
  - re-exec is called with the same path, argv and env. Inject the exec function; no real exec in unit tests.
  - Plus one integration test on a temp data dir: start, restart, and health comes back with a new `boot_id` and the same pid.
- **SPA:**
  - the action picks IPC for a managed local daemon and the API otherwise;
  - the confirm text includes the running-worker line only when there are running workers;
  - the per-host in-progress state disables all three entry points;
  - success needs a new `boot_id`;
  - timeout at 60 s;
  - the R2 button shows for managed and alive regardless of `restartPending`;
  - R3 appears only with `restart_required`.
- **Mutation is a deliverable:**
  - drop the boot-id comparison → the success test turns red;
  - drop the running-worker count → the confirm test turns red.
- **Real acceptance:** restart mlab's daemon from the host page. Expect: terminals reconnect, the pid is unchanged, dev mode is still on if it was, and `/api/health` reports a new `boot_id`.
  - Coordinate the moment with the user, because the restart interrupts running workers on mlab.
  - Air26's App-managed path is checked by the user.

## 5. Not in scope

- Hot-reloading the Nex config without a restart.
- Restarting tmux.
- Restarting a host the App cannot reach.
- Changing booter or adding a launchd job.
