// spa/src/lib/presence/reporter.ts — the Mac window tells each push-capable daemon what it shows and whether the user is
// there (push spec §5.4, plan PU-4 Task 3), so a phone is not pushed what the user is looking at (R6).
//
// To every connected host whose /api/info lists `push.v1`: PUT /api/push/presence {client_id, active, sessions, ttl_ms}
//   - on change of what is shown or of the activity (debounced 300 ms), and every 20 s while active (ttl is 45 s);
//   - once with active:false when the user stops being there (the entry would only expire otherwise);
//   - nothing to a host without the capability; a failed PUT is ignored and retried by the next tick.
// Each host gets only its own sessions. client_id = this device's id + this window's id: every Electron window reports
// for itself.
import { fetchHostInfo, pinnedHostFetch } from '../host-api'
import { getClientId } from '../client-identity'
import { leaderWindowId } from '../profile/leader'
import { hostEndpoint, useHostStore, type HostConfig } from '../../stores/useHostStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useTabStore } from '../../stores/useTabStore'
import { createActivityTracker, type ActivityTracker } from './activity'
import { visibleSessionsByHost, type PresenceSession } from './visible-sessions'

export const PUSH_CAPABILITY = 'push.v1'
export const PRESENCE_PATH = '/api/push/presence'
export const PRESENCE_TTL_MS = 45_000
export const HEARTBEAT_MS = 20_000
export const DEBOUNCE_MS = 300
/** A request that has not answered by then is given up on, so one stuck connection cannot silence a host for good. */
export const REQUEST_TIMEOUT_MS = 10_000

export interface PresenceBody {
  client_id: string
  active: boolean
  sessions: PresenceSession[]
  ttl_ms: number
}

export interface ConnectedHost {
  id: string
  /** Changes when the host is re-pointed (endpoint or token): what the old daemon announced is not the new one's. */
  identity: string
}

export interface ReporterDeps {
  tracker: ActivityTracker
  visible: () => Record<string, PresenceSession[]>
  clientId: () => string
  put: (hostId: string, body: PresenceBody) => Promise<void>
  supportsPush: (hostId: string) => Promise<boolean>
  connectedHosts: () => ConnectedHost[]
  /** Calls fn whenever the tabs, the sessions or the hosts change; returns the unsubscribe. */
  subscribeChanges: (fn: () => void) => () => void
}

const identityOf = (h: HostConfig): string => `${hostEndpoint(h)}:${h.token ?? ''}`

export function defaultReporterDeps(): ReporterDeps {
  return {
    tracker: createActivityTracker(),
    visible: visibleSessionsByHost,
    clientId: () => `${getClientId()}:${leaderWindowId()}`,
    async put(hostId, body) {
      const res = await pinnedHostFetch(hostId, PRESENCE_PATH, {
        method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
        signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
      })
      // A 4xx is the daemon refusing this body for good: sending the same body again cannot change it (the next change
      // or heartbeat sends what is then shown). Only a network failure or a 5xx is worth an early retry.
      if (res.status >= 500) throw new Error(`presence ${res.status}`)
      if (!res.ok) console.warn(`[presence] ${hostId} refused the report (${res.status})`)
    },
    async supportsPush(hostId) {
      const info = await Promise.race([
        fetchHostInfo(hostId),
        new Promise<never>((_, reject) => setTimeout(() => reject(new Error('info timeout')), REQUEST_TIMEOUT_MS)),
      ])
      const caps: unknown = info?.capabilities
      return Array.isArray(caps) && caps.includes(PUSH_CAPABILITY)
    },
    connectedHosts() {
      const { hosts, runtime } = useHostStore.getState()
      return Object.values(hosts).filter((h) => runtime[h.id]?.status === 'connected').map((h) => ({ id: h.id, identity: identityOf(h) }))
    },
    subscribeChanges(fn) {
      const offs = [useTabStore.subscribe(fn), useSessionStore.subscribe(fn), useHostStore.subscribe(fn)]
      return () => offs.forEach((off) => off())
    },
  }
}

interface Sent {
  active: boolean
  signature: string
}

export function startPushPresence(deps: ReporterDeps = defaultReporterDeps()): () => void {
  // push.v1 per host and identity; absent = not asked yet. A re-point forgets it (and what was sent).
  const support = new Map<string, { identity: string; supported: boolean | null }>()
  const sent = new Map<string, Sent>()
  const inflight = new Map<string, unknown>() // host -> the support entry (host identity) the request was made under
  const skipped = new Set<string>() // changes that came while that host's PUT was out: sent when it settles
  let debounce: ReturnType<typeof setTimeout> | undefined
  let stopped = false

  const sync = () => {
    const hosts = deps.connectedHosts()
    const live = new Set(hosts.map((h) => h.id))
    for (const id of [...support.keys()]) {
      if (!live.has(id)) { support.delete(id); sent.delete(id) }
    }
    for (const h of hosts) {
      const known = support.get(h.id)
      if (known?.identity === h.identity) continue
      sent.delete(h.id)
      const entry = { identity: h.identity, supported: null as boolean | null }
      support.set(h.id, entry)
      deps.supportsPush(h.id).then(
        (ok) => { if (!stopped && support.get(h.id) === entry) { entry.supported = ok; schedule() } },
        () => { /* unknown: asked again on the next connect */ },
      )
    }
  }

  const flush = (heartbeat: boolean) => {
    if (stopped) return
    sync()
    const active = deps.tracker.isActive()
    const shown = deps.visible()
    for (const [hostId, s] of support) {
      if (s.supported !== true) continue
      // A request under the host's previous identity is the old daemon's: it does not hold up the new one.
      if (inflight.has(hostId) && inflight.get(hostId) === s) { skipped.add(hostId); continue }
      const last = sent.get(hostId)
      let body: PresenceBody
      let next: Sent
      if (!active) {
        if (last?.active !== true) continue // never reported, or already told: one active:false, not a stream
        body = { client_id: deps.clientId(), active: false, sessions: [], ttl_ms: PRESENCE_TTL_MS }
        next = { active: false, signature: '' }
      } else {
        const sessions = shown[hostId] ?? []
        const signature = JSON.stringify(sessions)
        if (last?.active === true && last.signature === signature && !heartbeat) continue // the 20 s tick is the heartbeat
        body = { client_id: deps.clientId(), active: true, sessions, ttl_ms: PRESENCE_TTL_MS }
        next = { active: true, signature }
      }
      inflight.set(hostId, s)
      const entry = s
      deps.put(hostId, body).then(
        () => { if (!stopped && support.get(hostId) === entry) sent.set(hostId, next) },
        () => { /* ignored: the next tick retries */ },
      ).finally(() => {
        if (inflight.get(hostId) === entry) inflight.delete(hostId)
        if (skipped.delete(hostId)) schedule() // what changed during the request is not lost until the next heartbeat
      })
    }
  }

  const schedule = () => {
    if (stopped) return
    if (debounce !== undefined) clearTimeout(debounce)
    debounce = setTimeout(() => { debounce = undefined; flush(false) }, DEBOUNCE_MS)
  }

  const offChanges = deps.subscribeChanges(schedule)
  const offActivity = deps.tracker.subscribe(schedule)
  const heartbeat = setInterval(() => flush(true), HEARTBEAT_MS)
  sync()
  schedule()

  return () => {
    stopped = true
    if (debounce !== undefined) clearTimeout(debounce)
    clearInterval(heartbeat)
    offChanges()
    offActivity()
    deps.tracker.dispose()
  }
}
