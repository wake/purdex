// spa/src/stores/useLaunchPlanStore.ts — the daemon's launch plan per session, shared by the status bar and the pane
// (no-agent launch panel). Outside any component, so it survives a tab unmount / remount, and both readers see one answer.
//
// - A key with no answer yet is `loading`. A re-read keeps the previous ready plan (no flicker) until the new answer lands.
// - Only the latest request for a key may write: an older response resolving after a newer one is dropped.
// - A failed read sets `error` and drops the plan: never a stale panel.
import { create } from 'zustand'
import { fetchLaunchPlan, type LaunchPlan } from '../lib/launch-plan'

export interface LaunchPlanEntry { status: 'loading' | 'ready' | 'error'; plan: LaunchPlan | null }

interface LaunchPlanState {
  entries: Record<string, LaunchPlanEntry>
  load: (hostId: string, code: string) => Promise<void>
}

export const launchPlanKey = (hostId: string, code: string) => `${hostId}\0${code}`

// Per-key request sequence; module-level (not state): it is bookkeeping, nobody renders from it.
const latest = new Map<string, number>()

export const useLaunchPlanStore = create<LaunchPlanState>((set, get) => ({
  entries: {},
  load: async (hostId, code) => {
    const key = launchPlanKey(hostId, code)
    const seq = (latest.get(key) ?? 0) + 1
    latest.set(key, seq)
    const prev = Object.hasOwn(get().entries, key) ? get().entries[key] : undefined
    // Keep a ready plan (and its decision) while the new answer is on its way; anything else is loading.
    if (prev?.status !== 'ready') set((s) => ({ entries: { ...s.entries, [key]: { status: 'loading', plan: null } } }))
    let next: LaunchPlanEntry
    try {
      next = { status: 'ready', plan: await fetchLaunchPlan(hostId, code) }
    } catch {
      next = { status: 'error', plan: null }
    }
    if (latest.get(key) !== seq) return
    set((s) => ({ entries: { ...s.entries, [key]: next } }))
  },
}))

/** The entry for a session, or undefined when nothing was asked yet (`Object.hasOwn`: `toString` & co. are not sessions). */
export const selectLaunchPlanEntry = (hostId: string, code: string) => (s: LaunchPlanState): LaunchPlanEntry | undefined => {
  if (!hostId || !code) return undefined
  const key = launchPlanKey(hostId, code)
  return Object.hasOwn(s.entries, key) ? s.entries[key] : undefined
}
