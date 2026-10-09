// spa/src/lib/pairing.ts — the Mac side of phone pairing by QR code (QR pairing spec §2 steps 2–4, §3.4, §4), UI-free
// and framework-free: mint a device token on every host, package one pair row per minted host on the relay, poll the
// entry, and settle (revoke or not) when the dialog closes or the countdown ends.
//
// NOTHING HERE THROWS OR REJECTS. Every HTTP exchange is raced against a 10 s timer (an AbortController + setTimeout —
// the host-transfer-api.ts pattern: fake timers cannot advance `AbortSignal.timeout`, and a transport that ignores its
// signal must still end on time).
//
// SECRETS: device tokens live only in the request body of the entry create. They are never logged, never put in an
// error or a returned value; failures are reduced to a reason code.
//
// Revocation rule (spec §4.3): the minted tokens are revoked only after `DELETE entry` answered 204 (an unclaimed entry
// was removed, no phone can claim it any more). 409 → the phone got there first: revoke nothing. 404, any other
// answer, a network error or a timeout → revoke nothing and say the outcome is unknown (the tokens die unused at
// `use_by`; a phone that did pair shows up in the paired-phones list).

import { useHostStore, type HostConfig } from '../stores/useHostStore'
import { isValidDaemonId } from './daemon-id'
import { hostFetch } from './host-api'
import type { HostLook } from './host-look'
import { payloadRowsOf, isTransferHost, parseTransferRows } from './host-transfer-plan'
import type { TransferLook } from './host-transfer-api'

export const PAIRING_TTL_MS = 10 * 60_000
/** How long a token outlives the deadline D (spec §2). */
export const PAIRING_TOKEN_MARGIN_S = 300
/** Below this many seconds left the entry is not created (the daemon's own minimum, spec §4.1). */
export const PAIRING_MIN_ENTRY_S = 60
export const PAIRING_POLL_MS = 2000
export const PAIRING_HTTP_TIMEOUT_MS = 10_000

export interface PairingProfile {
  /** SPA host id of the profile's SOT host. */
  sotHostId: string
  profileId: string
  profileName: string
}

export interface PairingInput {
  profile: PairingProfile
  /** The relay host: holds the pairing entry; its ip:port goes into the QR. */
  relay: HostConfig
  /** Candidate hosts (those with a token). The SOT host must be among them. */
  hosts: readonly HostConfig[]
  /** The phone's name (`label` of every device token). */
  label: string
  /** The workbench look (name + look fields) of a host; default: the host's own fields. */
  lookOf?: (hostId: string) => HostLook
  /** Default `Date.now`. */
  clock?: () => number
  /** Aborting closes the session. */
  signal?: AbortSignal
}

export type LeftOutReason = 'unknown_host' | 'no_token' | 'no_daemon_id' | 'invalid_address' | 'mint_failed'
export interface LeftOut {
  hostId: string
  reason: LeftOutReason
}

export type PairingFailureReason =
  | 'sot_failed' // the profile's SOT host could not be minted on (or has no daemon id / token)
  | 'too_late' //   < 60 s of the 10 minutes left after minting
  | 'cancelled' //  closed while minting
  | 'unknown_host' // the relay is not in the host store; nothing was sent to it
  | 'bad_payload'
  | 'too_large'
  | 'capacity'
  | 'unavailable'
  | 'unauthorized'
  | 'no_token'
  | 'unsupported'
  | 'network'
  | 'timeout'
  | 'malformed'

export interface PairingFailure {
  kind: 'failed'
  reason: PairingFailureReason
  /** Hosts whose tokens could not be revoked after the abort; the caller retries later. */
  revokeFailed: string[]
}

export interface PairingReady {
  kind: 'ok'
  code: string
  /** The entry's expiry on the relay's clock (ms). */
  expiresAt: number
  qrUrl: string
  leftOut: LeftOut[]
  /** SPA host ids whose pair row is in the entry. */
  mintedHostIds: string[]
  pairingId: string
  /** D, ms on this clock. */
  deadline: number
  /** Hosts left out whose mint outcome was unknown and whose cleanup revoke failed. */
  revokeFailed: string[]
}

export type PackageResult = PairingReady | PairingFailure

// ───────────────────────────── HTTP ─────────────────────────────

type Raw = { kind: 'res'; status: number; json: unknown } | { kind: 'failed'; reason: 'network' | 'timeout' }

async function call(hostId: string, method: string, path: string, body?: unknown): Promise<Raw> {
  const controller = new AbortController()
  let timer: ReturnType<typeof setTimeout> | undefined
  const timedOut = new Promise<Raw>((resolve) => {
    timer = setTimeout(() => {
      controller.abort()
      resolve({ kind: 'failed', reason: 'timeout' })
    }, PAIRING_HTTP_TIMEOUT_MS)
  })
  const run = async (): Promise<Raw> => {
    let res: Response
    try {
      res = await hostFetch(hostId, path, {
        method,
        ...(body !== undefined ? { headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) } : {}),
        signal: controller.signal,
      })
    } catch {
      return { kind: 'failed', reason: 'network' }
    }
    let json: unknown
    try {
      const text = await res.text()
      try {
        json = text === '' ? undefined : JSON.parse(text)
      } catch {
        json = undefined
      }
    } catch {
      return { kind: 'failed', reason: 'network' }
    }
    return { kind: 'res', status: res.status, json }
  }
  try {
    return await Promise.race([run(), timedOut])
  } finally {
    clearTimeout(timer)
  }
}

function isPlainObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

const CREATE_STATUS: Record<number, PairingFailureReason> = {
  400: 'bad_payload',
  401: 'unauthorized',
  403: 'no_token',
  404: 'unsupported',
  413: 'too_large',
  429: 'capacity',
  503: 'unavailable',
}

/** Best-effort `DELETE /api/devices?pairing_id=` on each host; the hosts it could not revoke on. */
async function revokeHosts(hostIds: readonly string[], pairingId: string): Promise<string[]> {
  const results = await Promise.all(
    hostIds.map(async (hostId) => {
      const r = await call(hostId, 'DELETE', `/api/devices?pairing_id=${encodeURIComponent(pairingId)}`)
      return r.kind === 'res' && r.status >= 200 && r.status < 300 ? null : hostId
    }),
  )
  return results.filter((h): h is string => h !== null)
}

// ───────────────────────────── mint + package ─────────────────────────────

interface Plan {
  host: HostConfig
  name: string
  ip: string
  port: number
  daemonId: string
  look: TransferLook
}

type MintOutcome =
  | { kind: 'ok'; id: string; token: string }
  | { kind: 'rejected' } //  a definite refusal: nothing was minted
  | { kind: 'unknown' } //   timeout, network, or a 201 we could not read: a token may exist

/** The row skeleton of one host, or why it cannot be a row. Pure; the token is not known yet. */
function planOf(host: HostConfig, lookOf: (id: string) => HostLook): Plan | LeftOutReason {
  if (!useHostStore.getState().hosts[host.id]) return 'unknown_host'
  if (typeof host.token !== 'string' || host.token === '') return 'no_token'
  if (!isValidDaemonId(host.daemonId)) return 'no_daemon_id'
  if (!isTransferHost(host.ip)) return 'invalid_address'
  const shown = payloadRowsOf([host], lookOf)[0]
  // Same guards as a received row: only look fields that pass `sanitizeHostConfig` survive.
  const parsed = parseTransferRows([{ ip: shown.ip, port: shown.port, token: 'x', name: shown.name, look: shown.look }]).rows[0]
  if (!parsed) return 'invalid_address'
  return { host, name: parsed.name, ip: parsed.ip, port: parsed.port, daemonId: host.daemonId, look: parsed.look ?? {} }
}

function defaultLookOf(hosts: readonly HostConfig[]): (id: string) => HostLook {
  const byId = new Map(hosts.map((h) => [h.id, h]))
  return (id) => {
    const h = byId.get(id)
    if (!h) return {}
    const look: HostLook = { name: h.name }
    if (h.colors !== undefined) look.colors = h.colors
    if (h.color !== undefined) look.color = h.color
    if (h.icon !== undefined) look.icon = h.icon
    if (h.iconWeight !== undefined) look.iconWeight = h.iconWeight
    return look
  }
}

function failed(reason: PairingFailureReason, revokeFailed: string[] = []): PairingFailure {
  return { kind: 'failed', reason, revokeFailed }
}

/**
 * Spec §2 steps 2–3: mint on every usable host (in parallel; `use_within_s` computed at each call), abort and revoke
 * when the SOT host fails or too little time is left, create the entry on the relay.
 */
export async function mintAndPackage(input: PairingInput, opts: { isCancelled?: () => boolean } = {}): Promise<PackageResult> {
  const clock = input.clock ?? Date.now
  const { profile, relay } = input
  if (!useHostStore.getState().hosts[relay.id]) return failed('unknown_host')
  const deadline = clock() + PAIRING_TTL_MS
  const lookOf = input.lookOf ?? defaultLookOf(input.hosts)

  const plans: Plan[] = []
  const leftOut: LeftOut[] = []
  const seen = new Set<string>()
  for (const host of input.hosts) {
    if (seen.has(host.id)) continue
    seen.add(host.id)
    const p = planOf(host, lookOf)
    if (typeof p === 'string') leftOut.push({ hostId: host.id, reason: p })
    else plans.push(p)
  }
  if (!plans.some((p) => p.host.id === profile.sotHostId)) return failed('sot_failed')

  const pairingId = crypto.randomUUID()
  const outcomes = await Promise.all(
    plans.map(async (plan): Promise<MintOutcome> => {
      const body: Record<string, unknown> = {
        pairing_id: pairingId,
        label: input.label,
        client: { kind: 'app', label: 'Purdex.app' },
        use_within_s: Math.ceil((deadline - clock()) / 1000) + PAIRING_TOKEN_MARGIN_S,
      }
      if (plan.host.id === profile.sotHostId) body.profile_id = profile.profileId
      const r = await call(plan.host.id, 'POST', '/api/devices', body)
      if (r.kind === 'failed') return { kind: 'unknown' }
      if (r.status === 201) {
        const j = r.json
        if (isPlainObject(j) && typeof j.id === 'string' && j.id !== '' && typeof j.token === 'string' && j.token !== '') {
          return { kind: 'ok', id: j.id, token: j.token }
        }
        return { kind: 'unknown' }
      }
      return { kind: 'rejected' }
    }),
  )

  // Every host a token may exist on (a definite refusal means none).
  const touched = plans.filter((_, i) => outcomes[i].kind !== 'rejected').map((p) => p.host.id)
  const abort = async (reason: PairingFailureReason): Promise<PairingFailure> => failed(reason, await revokeHosts(touched, pairingId))

  const rows: Record<string, unknown>[] = []
  const mintedHostIds: string[] = []
  const strayed: string[] = []
  let sotOk = false
  plans.forEach((plan, i) => {
    const o = outcomes[i]
    if (o.kind === 'ok') {
      rows.push({
        v: 1,
        kind: 'pair',
        name: plan.name,
        ip: plan.ip,
        port: plan.port,
        daemonId: plan.daemonId,
        look: plan.look,
        token: o.token,
        deviceId: o.id,
        pairingId,
        profile: { hostDaemonId: '', profileId: profile.profileId, name: profile.profileName },
      })
      mintedHostIds.push(plan.host.id)
      if (plan.host.id === profile.sotHostId) sotOk = true
    } else {
      leftOut.push({ hostId: plan.host.id, reason: 'mint_failed' })
      if (o.kind === 'unknown') strayed.push(plan.host.id)
    }
  })
  if (!sotOk) return abort('sot_failed')
  const sotPlan = plans.find((p) => p.host.id === profile.sotHostId)!
  for (const row of rows) (row.profile as { hostDaemonId: string }).hostDaemonId = sotPlan.daemonId

  if (opts.isCancelled?.()) return abort('cancelled')
  const expiresInS = Math.floor((deadline - clock()) / 1000)
  if (expiresInS < PAIRING_MIN_ENTRY_S) return abort('too_late')

  const created = await call(relay.id, 'POST', '/api/host-transfer/pairings', { rows, expires_in_s: expiresInS })
  if (created.kind === 'failed') return abort(created.reason)
  if (created.status !== 200) return abort(CREATE_STATUS[created.status] ?? 'malformed')
  const j = created.json
  if (!isPlainObject(j) || typeof j.code !== 'string' || j.code === '' || typeof j.expiresAt !== 'number' || !Number.isFinite(j.expiresAt)) {
    return abort('malformed')
  }

  // A left-out host whose mint outcome was unknown may hold a token nobody will use: clean it up now.
  const revokeFailed = strayed.length > 0 ? await revokeHosts(strayed, pairingId) : []
  const relayAt = encodeURIComponent(`${relay.ip}:${relay.port}`)
  return {
    kind: 'ok',
    code: j.code,
    expiresAt: j.expiresAt,
    qrUrl: `purdex://pair?v=1&relay=${relayAt}&code=${encodeURIComponent(j.code)}`,
    leftOut,
    mintedHostIds,
    pairingId,
    deadline,
    revokeFailed,
  }
}

// ───────────────────────────── session ─────────────────────────────

export type PairingPhase =
  | 'idle'
  | 'minting'
  | 'ready' //    the entry is open; polling
  | 'claimed' //  a phone took it (seen by a poll, or answered 409 to the delete)
  | 'expired' //  the countdown ran out, the entry was removed (204) and the tokens revoked
  | 'closed' //   closed by the person, the entry was removed (204) and the tokens revoked
  | 'gone' //     the entry is gone or the outcome is unknown (`unknownOutcome`): nothing was revoked
  | 'failed'

export interface PairingState {
  phase: PairingPhase
  seenClaim: boolean
  /** The delete answered 404 / failed / timed out, or a poll found the entry gone: the UI points to the paired-phones list. */
  unknownOutcome: boolean
  result?: PairingReady
  failure?: PairingFailure
  leftOut: LeftOut[]
  /** Hosts whose tokens must still be revoked (retry later). */
  revokeFailed: string[]
}

export interface PairingSession {
  start(): Promise<void>
  /** Idempotent; resolves when the close has settled. */
  close(): Promise<void>
  subscribe(listener: (state: PairingState) => void): () => void
  getState(): PairingState
}

export function createPairingSession(input: PairingInput): PairingSession {
  const clock = input.clock ?? Date.now
  let state: PairingState = { phase: 'idle', seenClaim: false, unknownOutcome: false, leftOut: [], revokeFailed: [] }
  const listeners = new Set<(s: PairingState) => void>()
  let startP: Promise<void> | undefined
  let closeP: Promise<void> | undefined
  let closeRequested = false
  let pollTimer: ReturnType<typeof setTimeout> | undefined
  let deadlineTimer: ReturnType<typeof setTimeout> | undefined

  const set = (patch: Partial<PairingState>) => {
    state = { ...state, ...patch }
    for (const l of [...listeners]) l(state)
  }
  const stopTimers = () => {
    clearTimeout(pollTimer)
    clearTimeout(deadlineTimer)
    pollTimer = deadlineTimer = undefined
  }

  const schedulePoll = (result: PairingReady) => {
    pollTimer = setTimeout(async () => {
      const r = await call(input.relay.id, 'GET', `/api/host-transfer/pairings/${encodeURIComponent(result.code)}`)
      if (closeRequested || state.phase !== 'ready') return
      if (r.kind === 'res' && r.status === 200 && isPlainObject(r.json) && r.json.claimed === true) {
        stopTimers()
        set({ phase: 'claimed', seenClaim: true })
        return
      }
      if (r.kind === 'res' && r.status === 404) {
        stopTimers()
        set({ phase: 'gone', unknownOutcome: true })
        return
      }
      schedulePoll(result)
    }, PAIRING_POLL_MS)
  }

  const requestClose = (kind: 'closed' | 'expired'): Promise<void> => (closeP ??= doClose(kind))

  const doClose = async (kind: 'closed' | 'expired'): Promise<void> => {
    closeRequested = true
    stopTimers()
    if (startP) await startP
    const result = state.result
    if (state.phase === 'idle') return set({ phase: 'closed' })
    if (state.phase !== 'ready' || !result) return // claimed / gone / failed / closed: settled already
    if (state.seenClaim) return set({ phase: 'claimed' })
    const r = await call(input.relay.id, 'DELETE', `/api/host-transfer/pairings/${encodeURIComponent(result.code)}`)
    if (r.kind === 'res' && r.status === 204) {
      const revokeFailed = await revokeHosts(result.mintedHostIds, result.pairingId)
      set({ phase: kind, revokeFailed: [...state.revokeFailed, ...revokeFailed] })
    } else if (r.kind === 'res' && r.status === 409) {
      set({ phase: 'claimed', seenClaim: true })
    } else {
      set({ phase: 'gone', unknownOutcome: true })
    }
  }

  const run = async (): Promise<void> => {
    set({ phase: 'minting' })
    if (input.signal) {
      if (input.signal.aborted) void requestClose('closed')
      else input.signal.addEventListener('abort', () => void requestClose('closed'), { once: true })
    }
    const res = await mintAndPackage(input, { isCancelled: () => closeRequested })
    if (res.kind === 'failed') {
      if (closeRequested) set({ phase: 'closed', revokeFailed: res.revokeFailed })
      else set({ phase: 'failed', failure: res, revokeFailed: res.revokeFailed })
      return
    }
    set({ phase: 'ready', result: res, leftOut: res.leftOut, revokeFailed: res.revokeFailed })
    if (closeRequested) return // doClose is waiting for this and settles the entry next
    schedulePoll(res)
    deadlineTimer = setTimeout(() => void requestClose('expired'), Math.max(0, res.deadline - clock()))
  }

  return {
    start() {
      return (startP ??= run())
    },
    close: () => requestClose('closed'),
    subscribe(listener) {
      listeners.add(listener)
      return () => void listeners.delete(listener)
    },
    getState: () => state,
  }
}
