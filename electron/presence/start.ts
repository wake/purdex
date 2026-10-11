// Wires the presence sampler, reporter and IPC together (PA-2 D1). main.ts passes the real
// `ipcMain`, `powerMonitor`, `fetch` and a window broadcast; nothing here imports `electron`.
import { ensureInstallId, loadAppPrefs, saveAppPrefs } from '../app-prefs'
import { createAwayMinutesStore, registerPresenceIpc, type IpcMainLike } from './ipc'
import { createReporter, type FetchInit, type Reporter, type ResponseLike } from './reporter'
import { createSampler, type PowerMonitorLike } from './sampler'

export interface StartPresenceOptions {
  prefsPath: string
  ipcMain: IpcMainLike
  powerMonitor: PowerMonitorLike
  fetch: (url: string, init: FetchInit) => Promise<ResponseLike>
  /** Called with the host ids main owns whenever that set changes (ids only, never tokens). */
  broadcastOwned: (hostIds: string[]) => void
  logger: { warn: (message: string) => void }
  /** Monotonic milliseconds (default performance.now). */
  now?: () => number
}

export interface PresenceHandle {
  reporter: Reporter
  stop(): void
}

export function startPresence(opts: StartPresenceOptions): PresenceHandle {
  const now = opts.now ?? (() => performance.now())
  const minutes = createAwayMinutesStore({
    load: () => loadAppPrefs(opts.prefsPath).presenceAwayMinutes,
    save: (n) => saveAppPrefs(opts.prefsPath, { ...loadAppPrefs(opts.prefsPath), presenceAwayMinutes: n }),
  })

  let reporter: Reporter | null = null
  const sampler = createSampler({
    powerMonitor: opts.powerMonitor,
    now,
    setInterval: (cb, ms) => setInterval(cb, ms),
    clearInterval: (h) => clearInterval(h as ReturnType<typeof setInterval>),
    onForce: (reason) => reporter?.force(reason),
  })
  const rep = createReporter({
    fetch: opts.fetch,
    now,
    setTimeout: (cb, ms) => setTimeout(cb, ms),
    clearTimeout: (h) => clearTimeout(h as ReturnType<typeof setTimeout>),
    setInterval: (cb, ms) => setInterval(cb, ms),
    clearInterval: (h) => clearInterval(h as ReturnType<typeof setInterval>),
    snapshot: (at) => sampler.snapshot(at),
    getAwayMinutes: () => minutes.get(),
    getInstallId: () => ensureInstallId(opts.prefsPath),
    logger: opts.logger,
    onOwnedChanged: opts.broadcastOwned,
  })
  reporter = rep

  registerPresenceIpc({ ipcMain: opts.ipcMain, reporter: rep, minutes, logger: opts.logger })
  sampler.start()
  rep.start()

  return {
    reporter: rep,
    stop() {
      rep.stop()
      sampler.stop()
    },
  }
}
