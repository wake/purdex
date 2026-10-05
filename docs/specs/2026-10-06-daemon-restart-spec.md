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
- **Host page:** `OverviewSection.tsx` has a "daemon config" section (`hosts.daemon_config`, around :279) showing `purdex_version` and `tmux_version`.
- **A restart kills running worker turns.** Nexen is embedded in the daemon. A turn's process does not survive a restart: startup reconcile settles the turn as `orphaned`, puts the execution back to `idle`, and SIGKILLs a still-live orphan (Nexen `capability-matrix.md` #26 and #41). tmux sessions are a separate server and are unaffected. Terminal panes reconnect over WS.
- Dev mode is env-only (`PDX_DEV_MODE=1`), so a restart must keep the environment.

## 3. Behaviour

### 3.1 Daemon: `POST /api/daemon/restart`

- Auth is the same as the other host-admin endpoints (the host token). It works in non-dev mode.
- The daemon replies `202 {"boot_id": "<current>"}`, flushes the reply, and then restarts itself:
  1. Graceful shutdown, the same path `pdx stop` takes: stop accepting, close the stores with a WAL checkpoint, and let embedded Nexen shut down as it does on a normal stop.
  2. **Re-exec in place**: the same executable path (whatever binary is on disk now, so a pending update gets picked up), the same argv, the same environment.
  - Re-exec, not "exit and let something restart it": nothing supervises `serve` (§2).
  - Re-exec keeps the pid, so the App's ownership record and the pid file stay valid. The flock fd is close-on-exec, so the new image re-acquires the lock through the existing retry.
  - If exec fails, log it and exit non-zero. The SPA then sees the host stay down (§3.3).
- While a restart is in progress, a second request gets `409 restart_in_progress`.
- **Boot id:** `/api/health` (or `/api/info`; the plan picks one and says why) gains a `boot_id` that is new on every process start. The SPA uses it to know the restart really happened, not just that the host answered.
- Logged as one line with the requester's address: `daemon restart requested by …`.

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

### 3.3 While restarting, and after

- The button shows a spinner and "重啟中…", and the other two entry points for the same host are disabled; the state is per host.
- **Done** when the host answers health with a **different `boot_id`**: success toast "daemon 已重新啟動". For R3, the `restart_required` hint is gone once `/api/info` is re-read.
- **Timeout 60 s** (the same window `pdx start` uses): error "daemon 沒有在 60 秒內回來" plus where to look: `~/.config/pdx/logs/pdx.log` on that host. Same if the API call itself fails, with its error.
- A host's WS-driven views (terminals, worker panes) reconnect through their existing paths. No new reconnect logic.

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
