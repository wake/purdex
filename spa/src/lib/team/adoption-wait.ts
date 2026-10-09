// spa/src/lib/team/adoption-wait.ts — the wait after a REMOTE adopt is approved (cross-host spec §4.3, X3c-App).
// Approving writes a `joining` membership: the person has consented, the member host has not answered yet. The approval
// itself is closed by then, so the dialog is gone; the wait lives here, outside any component (the dialog host can
// unmount), and `AdoptionWaitCard` only shows it. It long-polls the LEAD host's `GET /api/team/adoptions/{id}?wait=30`:
//   joining                       → keep waiting;
//   active (or any later member state: releasing / released / killing / killed / gone — it did join) → joined;
//   failed (+ code)               → could not join;
//   void                          → the member host did not answer for 10 minutes;
//   a request error               → back off and ask again, never give up silently.
// The whole wait is bounded at 11 minutes (one past the daemon's 10 minute void): at the bound it asks once more
// (`wait=0`), and an answer that is still `joining` ends as `timeout`. A card the person closed early ("先關閉") gets
// its outcome as a toast instead.
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS } from '../storage'
import { useI18nStore } from '../../stores/useI18nStore'
import { useUndoToast } from '../../stores/useUndoToast'
import { ApprovalApiError, fetchAdoption } from './approval-api'
import { adoptTargetLabel, clipForDisplay, type AdoptPayload } from './types'

export const ADOPTION_WAIT_BOUND_MS = 11 * 60_000
export const ADOPTION_POLL_S = 30
const MIN_POLL_GAP_MS = 1_000
const BACKOFF_START_MS = 2_000
const BACKOFF_MAX_MS = 30_000
/** How long the green 「已納入」 stays before the card closes by itself ("closes as today"). */
export const ADOPTION_DONE_LINGER_MS = 1_500

export type AdoptionWaitState = 'waiting' | 'active' | 'failed' | 'void' | 'timeout'

export interface AdoptionWaitEntry {
  key: string
  hostId: string
  approvalId: string
  alias: string
  target: string
  /** When the approve was sent (ms): the bound counts from here. */
  startedAt: number
  state: AdoptionWaitState
  code: string
  /** The person closed the card while it waited: the outcome is a toast, not a card. */
  dismissed: boolean
}

interface AdoptionWaitStore {
  entries: Record<string, AdoptionWaitEntry>
  dismiss: (key: string) => void
  /** Test reset. */
  reset: () => void
}

const waitKey = (hostId: string, approvalId: string): string => `${hostId}\u0000${approvalId}`

const STATES: readonly string[] = ['waiting', 'active', 'failed', 'void', 'timeout']
const isRecord = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)
const MAX_PERSISTED = 50

/** Persisted data is untrusted: keep only well-formed entries, rebuilt field by field (the key is derived, never read). */
export function healAdoptionWaits(persisted: unknown): Record<string, AdoptionWaitEntry> {
  const raw = isRecord(persisted) && isRecord(persisted.entries) ? Object.values(persisted.entries) : []
  const out: Record<string, AdoptionWaitEntry> = {}
  for (const v of raw) {
    if (!isRecord(v) || Object.keys(out).length >= MAX_PERSISTED) continue
    const { hostId, approvalId, alias, target, startedAt, state, code, dismissed } = v
    if (typeof hostId !== 'string' || hostId === '' || typeof approvalId !== 'string' || approvalId === '') continue
    if (typeof alias !== 'string' || typeof target !== 'string' || typeof code !== 'string') continue
    if (typeof startedAt !== 'number' || !Number.isFinite(startedAt) || startedAt <= 0) continue
    if (typeof state !== 'string' || !STATES.includes(state) || typeof dismissed !== 'boolean') continue
    // A finished entry the person has dealt with (closed, or toasted) is never kept.
    if (dismissed && state !== 'waiting') continue
    const key = waitKey(hostId, approvalId)
    out[key] = { key, hostId, approvalId, alias: clipForDisplay(alias, 60), target: clipForDisplay(target, 60), startedAt, state: state as AdoptionWaitState, code: clipForDisplay(code, 80), dismissed }
  }
  return out
}

// Device-local (purdex-adoption-waits): a reload mid-wait must not lose the wait or its outcome. The approval is closed
// and the member host answers once, so nothing else would bring the result back.
export const useAdoptionWait = create<AdoptionWaitStore>()(
  persist(
    (set) => ({
      entries: {},
      dismiss: (key) => set((s) => {
        const e = s.entries[key]
        if (!e) return s
        // A finished card just goes; a waiting one stays (dismissed) so the outcome can toast.
        if (e.state !== 'waiting') {
          const { [key]: _gone, ...rest } = s.entries
          return { entries: rest }
        }
        return { entries: { ...s.entries, [key]: { ...e, dismissed: true } } }
      }),
      reset: () => set({ entries: {} }),
    }),
    {
      name: STORAGE_KEYS.ADOPTION_WAITS,
      storage: purdexStorage,
      partialize: (s) => ({ entries: s.entries }),
      merge: (persisted, current) => ({ ...current, entries: healAdoptionWaits(persisted) }),
    },
  ),
)

export function adoptionAlias(p: AdoptPayload): string {
  return clipForDisplay(p.target_host_alias !== '' ? p.target_host_alias : p.target_host_id, 60)
}

/** The sentence for a finished (or waiting) entry. */
export function adoptionWaitText(e: Pick<AdoptionWaitEntry, 'state' | 'alias' | 'code'>): string {
  const t = useI18nStore.getState().t
  return t(`approval.dialog.adopt_wait.${e.state}`, { alias: e.alias, code: e.code })
}

const timers = new Set<ReturnType<typeof setTimeout>>()
const sleep = (ms: number): Promise<void> => new Promise((resolve) => {
  const id = setTimeout(() => { timers.delete(id); resolve() }, ms)
  timers.add(id)
})

/** Test reset: drops every pending sleep (their loops stay parked) and the entries. */
export function resetAdoptionWaitForTests(): void {
  for (const id of timers) clearTimeout(id)
  timers.clear()
  warned.clear()
  running.clear()
  useAdoptionWait.getState().reset()
}

function patch(key: string, p: Partial<AdoptionWaitEntry>): void {
  useAdoptionWait.setState((s) => (s.entries[key] ? { entries: { ...s.entries, [key]: { ...s.entries[key], ...p } } } : s))
}

function finish(key: string, state: Exclude<AdoptionWaitState, 'waiting'>, code = ''): void {
  const e = useAdoptionWait.getState().entries[key]
  if (!e) return
  const done = { ...e, state, code }
  if (e.dismissed) {
    useUndoToast.getState().show(useI18nStore.getState().t('approval.dialog.adopt_wait.toast', { target: e.target, result: adoptionWaitText(done) }))
    useAdoptionWait.setState((s) => {
      const { [key]: _gone, ...rest } = s.entries
      return { entries: rest }
    })
    return
  }
  patch(key, { state, code })
  if (state === 'active') {
    void sleep(ADOPTION_DONE_LINGER_MS).then(() => {
      if (useAdoptionWait.getState().entries[key]?.state === 'active') useAdoptionWait.getState().dismiss(key)
    })
  }
}

/** The answers that mean the member joined (wire_adopt.go `Adoption`: `active`, then the row's later states). */
const JOINED_STATES: ReadonlySet<string> = new Set(['active', 'releasing', 'released', 'killing', 'killed', 'gone'])
const warned = new Set<string>()
function warnUnknownState(key: string, state: string): void {
  if (warned.has(key)) return
  warned.add(key)
  console.warn(`[adoption-wait] unknown adoption state ${JSON.stringify(state)}; still waiting`)
}

/** A request is cut this long after the wait it asked for: a daemon that accepts and never answers must not hold the bound. */
const REQUEST_SLACK_MS = 5_000
const FINAL_ASK_TIMEOUT_MS = 5_000

/** One ask, aborted after `timeoutMs`; an abort reads as a network error like any other transport failure. */
async function ask(hostId: string, approvalId: string, waitS: number, timeoutMs: number) {
  const ctl = new AbortController()
  const timer = setTimeout(() => ctl.abort(), timeoutMs)
  try {
    return await fetchAdoption(hostId, approvalId, waitS, ctl.signal)
  } finally {
    clearTimeout(timer)
  }
}

async function run(key: string, hostId: string, approvalId: string): Promise<void> {
  const deadline = (useAdoptionWait.getState().entries[key]?.startedAt ?? Date.now()) + ADOPTION_WAIT_BOUND_MS
  let backoff = BACKOFF_START_MS
  for (;;) {
    if (!useAdoptionWait.getState().entries[key]) return
    let remaining = deadline - Date.now()
    // Under a second left: wait it out, then the one final ask.
    if (remaining > 0 && remaining < 1_000) { await sleep(remaining); remaining = 0 }
    const last = remaining <= 0
    // The wait never runs past the deadline; the final ask is a plain read (wait=0).
    const waitS = last ? 0 : Math.min(ADOPTION_POLL_S, Math.floor(remaining / 1_000))
    const timeoutMs = last ? FINAL_ASK_TIMEOUT_MS : Math.min(waitS * 1_000 + REQUEST_SLACK_MS, remaining)
    const askedAt = Date.now()
    let answer
    try {
      answer = await ask(hostId, approvalId, waitS, timeoutMs)
    } catch (e: unknown) {
      if (!useAdoptionWait.getState().entries[key]) return
      // Only a transport failure, 429 and 5xx can pass: a 404 (unsupported / not_found), a 409 not_approved, the host
      // forgotten or any other 4xx will answer the same next time, so it ends here with its code.
      if (e instanceof ApprovalApiError && !(e.code === 'network' || e.status === 429 || e.status >= 500)) { finish(key, 'failed', e.code); return }
      if (last) { finish(key, 'timeout'); return }
      // Not yet at the deadline: back off (never past it); at it, the next turn is the final ask.
      if (Date.now() < deadline) {
        await sleep(Math.min(backoff, Math.max(0, deadline - Date.now())))
        backoff = Math.min(backoff * 2, BACKOFF_MAX_MS)
      }
      continue
    }
    if (!useAdoptionWait.getState().entries[key]) return
    backoff = BACKOFF_START_MS
    switch (answer.state) {
      case 'failed': finish(key, 'failed', answer.code ?? ''); return
      case 'void': finish(key, 'void'); return
      default:
        // Joined: `active`, or a state the row only reaches after being a member. Anything else is not read as success.
        if (JOINED_STATES.has(answer.state)) { finish(key, 'active'); return }
        // `joining`, or a state this client does not know (a newer daemon): keep waiting, say so once.
        if (answer.state !== 'joining') warnUnknownState(key, answer.state)
        if (last) { finish(key, 'timeout'); return }
        // A long poll normally holds for the wait; one that came straight back must not spin.
        if (Date.now() - askedAt < MIN_POLL_GAP_MS) await sleep(Math.min(MIN_POLL_GAP_MS, Math.max(0, deadline - Date.now())))
    }
  }
}

/** Starts the wait for a remote adopt just approved on `hostId`. One wait per approval: a second start is ignored. */
export function startAdoptionWait(hostId: string, approvalId: string, p: AdoptPayload): void {
  const key = waitKey(hostId, approvalId)
  if (useAdoptionWait.getState().entries[key]) return
  useAdoptionWait.setState((s) => ({
    entries: { ...s.entries, [key]: { key, hostId, approvalId, alias: adoptionAlias(p), target: clipForDisplay(adoptTargetLabel(p), 60), startedAt: Date.now(), state: 'waiting', code: '', dismissed: false } },
  }))
  launch(key)
}

const running = new Set<string>()
function launch(key: string): void {
  const e = useAdoptionWait.getState().entries[key]
  if (!e || running.has(key)) return
  running.add(key)
  void run(key, e.hostId, e.approvalId).finally(() => running.delete(key))
}

/**
 * Picks the waits up again after a reload: from the persisted entries, by their ORIGINAL deadline (`startedAt` + bound —
 * one already past it goes straight to the final ask); a finished card that was never closed shows again. Idempotent
 * (a wait that is running is left alone), and it waits for the store to rehydrate. Called by the wait card on mount.
 */
export function resumeAdoptionWaits(): void {
  const go = () => {
    for (const e of Object.values(useAdoptionWait.getState().entries)) {
      if (e.state === 'waiting') launch(e.key)
      else if (e.state === 'active' && !running.has(e.key)) {
        running.add(e.key)
        void sleep(ADOPTION_DONE_LINGER_MS).then(() => {
          running.delete(e.key)
          if (useAdoptionWait.getState().entries[e.key]?.state === 'active') useAdoptionWait.getState().dismiss(e.key)
        })
      }
    }
  }
  if (useAdoptionWait.persist.hasHydrated()) go()
  else useAdoptionWait.persist.onFinishHydration(go)
}
