// spa/src/stores/useLaunchPlanStore.ts — the daemon's launch plan per session, shared by the status bar and the pane
// (no-agent launch panel). Outside any component, so it survives a tab unmount / remount, and both readers see one answer.
//
// - The key carries the tmux generation (`hostId\0code\0tmuxInstance`): a code reused by a new generation is a new key, so it is
//   `loading` by construction and never shows the old generation's plan. An entry whose plan names another generation is not ready.
// - A key with no answer yet is `loading`. A re-read may keep the previous ready plan (`keep`, no flicker) until the new answer lands.
// - One request in flight per key: an unforced load joins it; a forced load aborts it and replaces it. Only the newest may write,
//   and an aborted one never writes (not even `error`).
// - A failed read sets `error` and drops the plan: never a stale panel.
import { create } from 'zustand'
import { fetchLaunchPlan, type LaunchPlan } from '../lib/launch-plan'

export interface LaunchPlanEntry { status: 'loading' | 'ready' | 'error'; plan: LaunchPlan | null }

export interface LoadOptions {
  /** Replace a request already in flight for this key (abort it) instead of joining it. */
  force?: boolean
  /** Keep a ready plan on screen while the new answer is on its way (default true). False drops it first. */
  keep?: boolean
}

interface LaunchPlanState {
  entries: Record<string, LaunchPlanEntry>
  load: (hostId: string, code: string, tmuxInstance: string, opts?: LoadOptions) => Promise<void>
}

export const launchPlanKey = (hostId: string, code: string, tmuxInstance = '') => `${hostId}\0${code}\0${tmuxInstance}`

const LOADING: LaunchPlanEntry = { status: 'loading', plan: null }

// Per-key bookkeeping; module-level (not state): nobody renders from it.
interface Flight { seq: number; ctrl: AbortController; promise: Promise<void> }
const flights = new Map<string, Flight>()
const seqs = new Map<string, number>()

export const useLaunchPlanStore = create<LaunchPlanState>((set, get) => ({
  entries: {},
  load: (hostId, code, tmuxInstance, opts = {}) => {
    const key = launchPlanKey(hostId, code, tmuxInstance)
    const inflight = flights.get(key)
    // Unforced: join the request already asked for (a second reader of the same session must not fetch again). Nothing aborts a
    // shared request on unmount: another reader may still want it.
    if (inflight && !opts.force) return inflight.promise
    inflight?.ctrl.abort()
    const seq = (seqs.get(key) ?? 0) + 1
    seqs.set(key, seq)
    const ctrl = new AbortController()
    const prev = Object.hasOwn(get().entries, key) ? get().entries[key] : undefined
    // Keep a ready plan (and its decision) while the new answer is on its way, unless asked to drop it; anything else is loading.
    if (prev?.status !== 'ready' || opts.keep === false) set((s) => ({ entries: { ...s.entries, [key]: LOADING } }))
    const promise = (async () => {
      let next: LaunchPlanEntry
      try {
        next = { status: 'ready', plan: await fetchLaunchPlan(hostId, code, ctrl.signal) }
      } catch {
        next = { status: 'error', plan: null }
      }
      if (ctrl.signal.aborted || flights.get(key)?.seq !== seq) return
      flights.delete(key)
      set((s) => ({ entries: { ...s.entries, [key]: next } }))
    })()
    flights.set(key, { seq, ctrl, promise })
    return promise
  },
}))

/** The entry for a session generation, or undefined when nothing was asked yet (`Object.hasOwn`: `toString` & co. are not
 *  sessions). A ready plan that names another generation than the key's is not ready (defence in depth). */
export const selectLaunchPlanEntry = (hostId: string, code: string, tmuxInstance = '') => (s: LaunchPlanState): LaunchPlanEntry | undefined => {
  if (!hostId || !code) return undefined
  const key = launchPlanKey(hostId, code, tmuxInstance)
  if (!Object.hasOwn(s.entries, key)) return undefined
  const e = s.entries[key]
  if (e.status === 'ready' && tmuxInstance !== '' && e.plan && e.plan.tmuxInstance !== '' && e.plan.tmuxInstance !== tmuxInstance) return LOADING
  return e
}

/** Test seam: forget every request in flight (aborting them), so one test's pending read is not joined by the next. */
export function resetLaunchPlanFlights(): void {
  for (const f of flights.values()) f.ctrl.abort()
  flights.clear()
  seqs.clear()
}
