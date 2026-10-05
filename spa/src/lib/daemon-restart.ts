// spa/src/lib/daemon-restart.ts — the one restart action behind the three
// entry points (daemon restart spec §3.2): the App-managed local daemon goes
// through the Electron IPC, every other host through POST /api/daemon/restart.
// Done means health answers with a DIFFERENT boot_id — the old process keeps
// answering through its shutdown budget, so "the host answered" proves nothing.
// Every request goes through pinnedHostFetch: a host removed mid-restart must
// not have its POST or probes land on whatever host is active instead.
import { pinnedHostFetch } from './host-api'
import { listExecutions } from './nex/nex-api'
import { findHostByEndpoint, useHostStore } from '../stores/useHostStore'
import { useNexHostStore } from '../stores/useNexHostStore'
import { isNexReady } from '../components/hosts/nex/nex-ready'

export const RESTART_TIMEOUT_MS = 60_000 // the window `pdx start` waits for health
export const RESTART_POLL_MS = 1_000
export const HEALTH_PROBE_TIMEOUT_MS = 3_000
export const WORKER_COUNT_TIMEOUT_MS = 3_000
export const MAX_WORKER_PAGES = 20

export type RestartFailure = 'timeout' | 'unsupported' | 'request'

export class DaemonRestartError extends Error {
  kind: RestartFailure
  constructor(kind: RestartFailure, message = '') {
    super(message)
    this.name = 'DaemonRestartError'
    this.kind = kind
  }
}

const errText = (err: unknown) => (err instanceof Error ? err.message : String(err))
const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms))

export async function readBootId(hostId: string): Promise<string | null> {
  const ctl = new AbortController()
  const timer = setTimeout(() => ctl.abort(), HEALTH_PROBE_TIMEOUT_MS)
  try {
    const res = await pinnedHostFetch(hostId, '/api/health', { signal: ctl.signal })
    if (!res.ok) return null
    const body = (await res.json()) as { boot_id?: unknown }
    return typeof body.boot_id === 'string' && body.boot_id !== '' ? body.boot_id : null
  } catch {
    return null
  } finally {
    clearTimeout(timer)
  }
}

/** `/api/info.last_shutdown` of the boot that just answered: its error count, if it belongs to `bootId`. Any failure → 0. */
export async function readShutdownWarnings(hostId: string, bootId: string): Promise<number> {
  const ctl = new AbortController()
  const timer = setTimeout(() => ctl.abort(), HEALTH_PROBE_TIMEOUT_MS)
  try {
    const res = await pinnedHostFetch(hostId, '/api/info', { signal: ctl.signal })
    if (!res.ok) return 0
    const ls = ((await res.json()) as { last_shutdown?: { boot_id?: unknown; errors?: unknown } | null }).last_shutdown
    return ls && ls.boot_id === bootId && Array.isArray(ls.errors) ? ls.errors.length : 0
  } catch {
    return 0
  } finally {
    clearTimeout(timer)
  }
}

/** POST /api/daemon/restart → the boot id the restart replaces. */
export async function postRestart(hostId: string): Promise<string> {
  let res: Response
  try {
    res = await pinnedHostFetch(hostId, '/api/daemon/restart', { method: 'POST' })
  } catch (err) {
    throw new DaemonRestartError('request', errText(err))
  }
  // A daemon older than the endpoint: the mux has no such route (spec D6).
  if (res.status === 404 || res.status === 405) throw new DaemonRestartError('unsupported', `HTTP ${res.status}`)
  const body = (await res.json().catch(() => null)) as { boot_id?: unknown; error?: unknown } | null
  // 409: another client's restart is already under way — follow it to the same finish line (spec D5).
  if ((res.status === 202 || res.status === 409) && typeof body?.boot_id === 'string') return body.boot_id
  throw new DaemonRestartError('request', typeof body?.error === 'string' ? body.error : `HTTP ${res.status}`)
}

/** The host is the local daemon the App manages (same bind:port), and the IPC can restart it. */
export async function isManagedLocal(hostId: string): Promise<boolean> {
  const api = window.electronAPI
  if (!api?.localDaemonStatus || !api.localDaemonRestart) return false
  const st = await api.localDaemonStatus().catch(() => null)
  if (!st || st.managed !== 'managed' || !st.config) return false
  return findHostByEndpoint(useHostStore.getState().hosts, st.config.bind, st.config.port)?.id === hostId
}

export interface RestartDeps {
  readBootId: (hostId: string) => Promise<string | null>
  postRestart: (hostId: string) => Promise<string>
  isManagedLocal: (hostId: string) => Promise<boolean>
  localRestart: () => Promise<ElectronLocalDaemonResult>
  readShutdownWarnings: (hostId: string, bootId: string) => Promise<number>
}

const defaultDeps: RestartDeps = {
  readBootId,
  postRestart,
  isManagedLocal,
  localRestart: () => {
    const fn = window.electronAPI?.localDaemonRestart
    return fn ? fn() : Promise.reject(new Error('local daemon IPC unavailable'))
  },
  readShutdownWarnings,
}

export interface RestartResult {
  /** The IPC result on the managed-local path (re-register with it), else null. */
  ipc: ElectronLocalDaemonResult | null
  /** Cleanup errors the previous image recorded for THIS boot (spec D13); 0 when none or unreadable. */
  shutdownWarnings: number
}

const TIMED_OUT = Symbol('timed-out')

/**
 * Restart `hostId`'s daemon and wait for it to come back. Resolves with the
 * IPC result on the managed-local path (the caller re-registers the host
 * with it, as the Development page always has) plus the new boot's shutdown
 * warnings. Rejects with DaemonRestartError: 'request' (the call failed),
 * 'unsupported' (daemon too old), 'timeout' (no new boot id within 60 s).
 * The 60 s cover the whole action — a hung status IPC, POST or IPC restart included.
 */
export async function restartDaemon(hostId: string, over: Partial<RestartDeps> = {}): Promise<RestartResult> {
  const d = { ...defaultDeps, ...over }
  let expired = false
  let timer: ReturnType<typeof setTimeout> | undefined
  const deadline = new Promise<typeof TIMED_OUT>((r) => { timer = setTimeout(() => r(TIMED_OUT), RESTART_TIMEOUT_MS) })
  // Only "a new boot id was seen" races the deadline; the warnings read comes after it (D13).
  const attempt = (async (): Promise<{ ipc: ElectronLocalDaemonResult | null; bootId: string } | typeof TIMED_OUT> => {
    let before: string | null
    let ipc: ElectronLocalDaemonResult | null = null
    // A restart is a side effect: past the deadline the caller was already told it failed, so never send one then.
    const managed = await d.isManagedLocal(hostId)
    if (expired) return TIMED_OUT
    if (managed) {
      before = await d.readBootId(hostId)
      if (expired) return TIMED_OUT
      ipc = await d.localRestart().catch((err) => { throw new DaemonRestartError('request', errText(err)) })
    } else {
      before = await d.postRestart(hostId)
    }
    while (!expired) {
      await sleep(RESTART_POLL_MS)
      if (expired) break
      const now = await d.readBootId(hostId)
      if (now !== null && now !== before) return { ipc, bootId: now }
    }
    return TIMED_OUT
  })()
  attempt.catch(() => {}) // a failure after the deadline won the race is not unhandled
  let r: Awaited<typeof attempt>
  try {
    r = await Promise.race([attempt, deadline])
  } finally {
    expired = true
    clearTimeout(timer)
  }
  if (r === TIMED_OUT) throw new DaemonRestartError('timeout')
  // Bounded by its own HEALTH_PROBE_TIMEOUT_MS and never rejects: a slow or failed read stays a plain success.
  return { ipc: r.ipc, shutdownWarnings: await d.readShutdownWarnings(hostId, r.bootId) }
}

/**
 * Workers in `running` on the host, for the confirm text (spec §3.2, D7).
 * Nex reported but not ready → 0 (no turn can run). No Nex info, the list
 * failed, more than MAX_WORKER_PAGES pages, or over the budget → null:
 * unknown, which the dialog words as a warning rather than hiding it.
 */
export async function countRunningWorkers(hostId: string, timeoutMs = WORKER_COUNT_TIMEOUT_MS): Promise<number | null> {
  let abandoned = false // `work` cannot be cancelled: past the budget it must not keep paging
  const work = (async (): Promise<number | null> => {
    await useNexHostStore.getState().ensure(hostId)
    const info = useNexHostStore.getState().byHost[hostId]?.info ?? null
    if (info === null) return null
    if (!isNexReady(info)) return 0
    let n = 0
    let cursor: string | undefined
    for (let page = 0; page < MAX_WORKER_PAGES; page++) {
      if (abandoned) return null
      const p = await listExecutions(hostId, { state: 'running', cursor })
      n += p.items.filter((e) => e.state === 'running').length
      if (!p.next_cursor) return n
      cursor = p.next_cursor
    }
    return null
  })().catch(() => null)
  let timer: ReturnType<typeof setTimeout> | undefined
  const timeout = new Promise<null>((r) => { timer = setTimeout(() => r(null), timeoutMs) })
  try {
    return await Promise.race([work, timeout])
  } finally {
    abandoned = true
    clearTimeout(timer)
  }
}
