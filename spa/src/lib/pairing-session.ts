// spa/src/lib/pairing-session.ts — the pairing session state machine: poll the entry, settle when the dialog closes or the
// countdown ends (moved verbatim out of pairing.ts, #2238).
import { mintAndPackage } from './pairing-mint'
import { call, isPlainObject, revokeHosts } from './pairing-transport'
import { PAIRING_POLL_MS, type LeftOut, type PairingFailure, type PairingInput, type PairingReady } from './pairing-types'

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
  /** The pairing those tokens belong to; set together with `revokeFailed`, so a retry knows what to revoke. */
  pairingId?: string
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
      set({ phase: kind, revokeFailed: [...state.revokeFailed, ...revokeFailed], pairingId: result.pairingId })
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
      if (closeRequested) set({ phase: 'closed', revokeFailed: res.revokeFailed, pairingId: res.pairingId })
      else set({ phase: 'failed', failure: res, revokeFailed: res.revokeFailed, pairingId: res.pairingId })
      return
    }
    set({ phase: 'ready', result: res, leftOut: res.leftOut, revokeFailed: res.revokeFailed, pairingId: res.pairingId })
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
