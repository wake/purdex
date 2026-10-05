// spa/src/lib/nex/take-to-terminal-registry.ts — shell cleanup spec §9.5.
// The status bar's terminal button must run a worker pane's own "Take to
// terminal" flow, not a copy of it: each mounted ExecutionView registers here
// by pane id (updated on change, removed on unmount), and the status bar reads
// it through `useTakeToTerminal`. Module state, like ExecutionView's
// `findListeners`: one window, one set of mounted panes.
import { useCallback, useSyncExternalStore } from 'react'

export interface TakeToTerminalEntry {
  /** The pane offers Take to terminal now (the header's 終端機 item is there). */
  readonly canTake: boolean
  /** A take is in flight, or a write it must not race; the view refuses a take meanwhile. */
  readonly busy: boolean
  /** Runs the view's own flow: its running-turn confirm, lease handling and busy guards. */
  readonly takeToTerminal: () => void
}

const entries = new Map<string, TakeToTerminalEntry>()
const listeners = new Set<() => void>()

function notify(): void {
  for (const listener of listeners) listener()
}

/**
 * Registers `entry` for `paneId`, replacing what was there. The returned
 * function removes this entry only — a newer one registered for the same pane
 * since is left alone. The entry is stored as given, so reads stay the same
 * object until the pane registers again.
 */
export function registerTakeToTerminal(paneId: string, entry: TakeToTerminalEntry): () => void {
  entries.set(paneId, entry)
  notify()
  return () => unregisterTakeToTerminal(paneId, entry)
}

/** Removes the pane's entry — only when it is `entry`, if one is given. Notifies only when something was removed. */
export function unregisterTakeToTerminal(paneId: string, entry?: TakeToTerminalEntry): void {
  const current = entries.get(paneId)
  if (!current || (entry && current !== entry)) return
  entries.delete(paneId)
  notify()
}

/** The pane's entry, or null when no view is registered for it. */
export function getTakeToTerminal(paneId: string): TakeToTerminalEntry | null {
  return entries.get(paneId) ?? null
}

export function subscribeTakeToTerminal(listener: () => void): () => void {
  listeners.add(listener)
  return () => { listeners.delete(listener) }
}

/** The pane's entry, kept current; null without a pane id or with no view registered for it. */
export function useTakeToTerminal(paneId: string | null | undefined): TakeToTerminalEntry | null {
  const read = useCallback(() => (paneId ? getTakeToTerminal(paneId) : null), [paneId])
  return useSyncExternalStore(subscribeTakeToTerminal, read, read)
}
