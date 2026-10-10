// spa/src/lib/conversations/fold-memory.ts — which folds the reader opened in a pane's deck / chat (U3 plan D5). The deck
// and the chat are tab-hosted views that unmount when the tab is switched away, so a `useState` would close every output
// and thought again; the expansion lives here, keyed by pane, and `usePaneFoldStore` hands it to the room's `FoldContext`
// consumers (`useFold`, `ToolDiffView`). Memory only: a reload folds everything again.
import { useMemo, useSyncExternalStore } from 'react'
import type { FoldStore } from '../../components/room/fold-context'

const panes = new Map<string, Set<string>>()
const listeners = new Map<string, Set<() => void>>()
// A frozen snapshot per pane, replaced on every change: `useSyncExternalStore` compares by identity.
const snapshots = new Map<string, ReadonlySet<string>>()
const EMPTY: ReadonlySet<string> = new Set()

function emit(paneId: string): void {
  snapshots.set(paneId, new Set(panes.get(paneId) ?? []))
  listeners.get(paneId)?.forEach((fn) => fn())
}

export function isFolded(paneId: string, key: string): boolean {
  return panes.get(paneId)?.has(key) ?? false
}

export function setOpen(paneId: string, key: string, open: boolean): void {
  let set = panes.get(paneId)
  if (!set) { set = new Set(); panes.set(paneId, set) }
  if (set.has(key) === open) return
  if (open) set.add(key)
  else set.delete(key)
  emit(paneId)
}

export function toggleOpen(paneId: string, key: string): void {
  setOpen(paneId, key, !isFolded(paneId, key))
}

export function forgetFolds(paneId: string): void {
  if (!panes.delete(paneId)) return
  snapshots.delete(paneId)
  listeners.get(paneId)?.forEach((fn) => fn())
}

function subscribe(paneId: string, fn: () => void): () => void {
  let set = listeners.get(paneId)
  if (!set) { set = new Set(); listeners.set(paneId, set) }
  set.add(fn)
  return () => {
    set.delete(fn)
    if (set.size === 0) listeners.delete(paneId)
  }
}

/**
 * A `FoldStore` backed by the pane's memory. The deck has no turn-wide expand strip, so `register` / `unregister` /
 * `setTurn` are no-ops; `expand` opens keys (a search reveal, later).
 */
export function usePaneFoldStore(paneId: string): FoldStore {
  const open = useSyncExternalStore(
    (fn) => subscribe(paneId, fn),
    () => snapshots.get(paneId) ?? EMPTY,
  )
  return useMemo<FoldStore>(() => ({
    isExpanded: (key) => open.has(key),
    toggle: (key) => toggleOpen(paneId, key),
    register: () => {},
    unregister: () => {},
    setTurn: () => {},
    expand: (keys) => keys.forEach((k) => setOpen(paneId, k, true)),
  }), [paneId, open])
}
