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

export const useAdoptionWait = create<AdoptionWaitStore>()((set) => ({
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
}))

const waitKey = (hostId: string, approvalId: string): string => `${hostId}\u0000${approvalId}`

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

async function run(key: string, hostId: string, approvalId: string): Promise<void> {
  const deadline = Date.now() + ADOPTION_WAIT_BOUND_MS
  let backoff = BACKOFF_START_MS
  for (;;) {
    if (!useAdoptionWait.getState().entries[key]) return
    const last = Date.now() >= deadline
    const askedAt = Date.now()
    let answer
    try {
      answer = await fetchAdoption(hostId, approvalId, last ? 0 : ADOPTION_POLL_S)
    } catch (e: unknown) {
      if (!useAdoptionWait.getState().entries[key]) return
      if (e instanceof ApprovalApiError && e.code === 'host_removed') { finish(key, 'timeout'); return }
      if (last) { finish(key, 'timeout'); return }
      await sleep(backoff)
      backoff = Math.min(backoff * 2, BACKOFF_MAX_MS)
      continue
    }
    if (!useAdoptionWait.getState().entries[key]) return
    backoff = BACKOFF_START_MS
    switch (answer.state) {
      case 'failed': finish(key, 'failed', answer.code ?? ''); return
      case 'void': finish(key, 'void'); return
      case 'joining': case '':
        if (last) { finish(key, 'timeout'); return }
        // A long poll normally holds for the wait; one that came straight back must not spin.
        if (Date.now() - askedAt < MIN_POLL_GAP_MS) await sleep(MIN_POLL_GAP_MS)
        break
      default: finish(key, 'active'); return // active, or a later member state: it did join
    }
  }
}

/** Starts the wait for a remote adopt just approved on `hostId`. One wait per approval: a second start is ignored. */
export function startAdoptionWait(hostId: string, approvalId: string, p: AdoptPayload): void {
  const key = waitKey(hostId, approvalId)
  if (useAdoptionWait.getState().entries[key]) return
  useAdoptionWait.setState((s) => ({
    entries: { ...s.entries, [key]: { key, hostId, approvalId, alias: adoptionAlias(p), target: clipForDisplay(adoptTargetLabel(p), 60), state: 'waiting', code: '', dismissed: false } },
  }))
  void run(key, hostId, approvalId)
}
