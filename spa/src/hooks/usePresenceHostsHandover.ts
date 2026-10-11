// spa/src/hooks/usePresenceHostsHandover.ts — hands the persisted host list to the Mac App's main-process presence reporter
// (PA-2 D9); mounted once per window in App.tsx. Every window sends; main keeps the highest `hostsRev` and, at an equal
// rev, the last arrival. A send is triggered by a change of the payload FINGERPRINT (rev + the content), not of the rev
// alone: a restore that bypasses the rev, or two windows writing the same rev from different content, still converge —
// after a rehydrate every window resends the stored content with the stored rev, and the last send is the stored one.
// Hosts that main's validation would reject (it refuses the WHOLE payload for one bad entry) are dropped here first, with
// one diagnostic that carries counts and reasons only. A send is "confirmed" only on `true`; `false` or a rejection is
// retried with a bounded backoff. Tokens are only passed through to the bridge: never logged, never in a fingerprint log.
import { useEffect } from 'react'
import { useHostStore } from '../stores/useHostStore'

// Main's limits (electron/presence/ipc.ts).
const MAX_HOSTS = 64
const MAX_HOST_ID = 128
const MAX_TOKEN = 4096
const MAX_URL = 2048
const MAX_PAYLOAD_BYTES = 256 * 1024
const HOST_ID_PATTERN = /^[A-Za-z0-9_-]+$/
export const RETRY_DELAYS_MS = [1_000, 5_000, 30_000] as const

interface PresenceHost { hostId: string; baseUrl: string; token: string | null }
interface Built { payload: { rev: number; hosts: PresenceHost[] }; fingerprint: string; dropped: Record<string, number> }

function isOriginOnly(raw: string): boolean {
  if (raw.length === 0 || raw.length > MAX_URL || raw.includes('?') || raw.includes('#')) return false
  try {
    const u = new URL(raw)
    return (u.protocol === 'http:' || u.protocol === 'https:') && u.username === '' && u.password === ''
      && u.pathname === '/' && u.search === '' && u.hash === '' && u.hostname !== ''
  } catch {
    return false
  }
}

export function buildPresencePayload(): Built {
  const { hosts, hostOrder, hostsRev, getDaemonBase } = useHostStore.getState()
  const ids = hostOrder.filter((id) => hosts[id])
  for (const id of Object.keys(hosts)) if (!ids.includes(id)) ids.push(id)
  const dropped: Record<string, number> = {}
  const drop = (reason: string) => { dropped[reason] = (dropped[reason] ?? 0) + 1 }
  const out: PresenceHost[] = []
  const seen = new Set<string>()
  for (const id of ids) {
    if (id.length === 0 || id.length > MAX_HOST_ID || !HOST_ID_PATTERN.test(id)) { drop('hostId'); continue }
    if (seen.has(id)) { drop('duplicate'); continue }
    const baseUrl = getDaemonBase(id)
    if (!isOriginOnly(baseUrl)) { drop('baseUrl'); continue }
    const token = hosts[id].token ?? null
    if (token !== null && (typeof token !== 'string' || token.length > MAX_TOKEN)) { drop('token'); continue }
    if (out.length >= MAX_HOSTS) { drop('count'); continue }
    seen.add(id)
    out.push({ hostId: id, baseUrl, token })
  }
  const payload = { rev: hostsRev, hosts: out }
  while (out.length > 0 && utf8Bytes(JSON.stringify(payload)) > MAX_PAYLOAD_BYTES) { out.pop(); drop('size') }
  // Order is not content: a reorder alone must not resend.
  const fingerprint = JSON.stringify([payload.rev, [...out].sort((a, b) => (a.hostId < b.hostId ? -1 : 1))])
  return { payload, fingerprint, dropped }
}

const utf8Bytes = (s: string): number => new TextEncoder().encode(s).length

export function usePresenceHostsHandover(): void {
  useEffect(() => {
    const presence = window.electronAPI?.presence
    if (!presence?.setHosts) return
    let confirmed: string | null = null // fingerprint main acknowledged with `true`
    let current: string | null = null // fingerprint being sent or waiting for a retry
    let attempt = 0
    let timer: ReturnType<typeof setTimeout> | undefined
    let lastWarn = ''
    let disposed = false

    const attemptSend = (built: Built) => {
      let result: Promise<boolean>
      try {
        result = Promise.resolve(presence.setHosts!(built.payload))
      } catch {
        result = Promise.resolve(false)
      }
      result.then((ok) => ok === true, () => false).then((ok) => {
        if (disposed || current !== built.fingerprint) return // superseded by a newer payload
        if (ok) { confirmed = built.fingerprint; attempt = 0; return }
        const delay = RETRY_DELAYS_MS[attempt]
        if (delay === undefined) return // bounded: the next fingerprint change tries again
        attempt++
        timer = setTimeout(() => { timer = undefined; if (!disposed && current === built.fingerprint) attemptSend(built) }, delay)
      })
    }

    const evaluate = () => {
      const built = buildPresencePayload()
      const reasons = Object.entries(built.dropped).map(([r, n]) => `${r}=${n}`).join(' ')
      if (reasons !== '' && reasons !== lastWarn) console.warn(`[presence] hosts dropped from the hand-over: ${reasons}`) // counts only
      lastWarn = reasons
      if (built.fingerprint === confirmed || built.fingerprint === current) return
      if (timer !== undefined) { clearTimeout(timer); timer = undefined }
      current = built.fingerprint
      attempt = 0
      attemptSend(built)
    }

    evaluate()
    let seen = useHostStore.getState()
    const off = useHostStore.subscribe((state) => {
      // Runtime / look changes do not touch the list: skip building a payload for them.
      if (state.hosts === seen.hosts && state.hostOrder === seen.hostOrder && state.hostsRev === seen.hostsRev) return
      seen = state
      evaluate()
    })
    return () => {
      disposed = true
      if (timer !== undefined) clearTimeout(timer)
      off()
    }
  }, [])
}
