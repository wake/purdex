// spa/src/stores/useDaemonRestartStore.ts — per-host "restart in progress"
// shared by the three restart entry points (daemon restart spec §3.3): while
// a host restarts, every entry point for it shows the spinner and is
// disabled. `settled` bumps after every attempt so views that show daemon
// facts (Overview info, Development status) re-read them.
import { create } from 'zustand'
import { restartDaemon, DaemonRestartError } from '../lib/daemon-restart'
import { useI18nStore } from './useI18nStore'
import { useUndoToast } from './useUndoToast'
import { useHostStore } from './useHostStore'
import { useNexHostStore } from './useNexHostStore'

interface DaemonRestartState {
  restarting: Record<string, true>
  settled: Record<string, number>
  restart: (hostId: string, hostName: string) => Promise<void>
}

function failureText(err: unknown, host: string): string {
  const t = useI18nStore.getState().t
  if (err instanceof DaemonRestartError && err.kind === 'timeout') return t('hosts.restart.timeout', { host })
  if (err instanceof DaemonRestartError && err.kind === 'unsupported') return t('hosts.restart.unsupported', { host })
  return t('hosts.restart.failed', { host, error: err instanceof Error ? err.message : String(err) })
}

export const useDaemonRestartStore = create<DaemonRestartState>()((set, get) => ({
  restarting: {},
  settled: {},
  restart: async (hostId, hostName) => {
    if (get().restarting[hostId]) return
    set((s) => ({ restarting: { ...s.restarting, [hostId]: true } }))
    try {
      const r = await restartDaemon(hostId)
      // Same as the Development page's own restart always did: the IPC hands back the daemon's url/token.
      if (r.ipc) useHostStore.getState().registerLocalHost({ url: r.ipc.url, token: r.ipc.token, hostname: r.ipc.hostname })
      // /api/info re-read: the Nex page's restart_required hint goes away (spec §3.3).
      void useNexHostStore.getState().invalidate(hostId)
      const t = useI18nStore.getState().t
      useUndoToast.getState().show(
        r.shutdownWarnings > 0
          ? t('hosts.restart.done_with_warnings', { host: hostName, count: r.shutdownWarnings })
          : t('hosts.restart.done', { host: hostName }),
      )
    } catch (err) {
      useUndoToast.getState().show(failureText(err, hostName), undefined, undefined, { persistent: true })
    } finally {
      set((s) => {
        const restarting = { ...s.restarting }
        delete restarting[hostId]
        return { restarting, settled: { ...s.settled, [hostId]: (s.settled[hostId] ?? 0) + 1 } }
      })
    }
  },
}))
