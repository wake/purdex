// spa/src/lib/nex/send-buffer.ts — the send buffer's registry (the undo window
// after pressing Enter). A message sent from the reply box is held here for a
// few seconds before it is posted, so Esc can take it back. Module state, like
// the draft memory: the pane that sent it may unmount (a tab switch) before
// the window ends, and a timer owned by the component would take the message
// with it. Memory only — a reload drops a message still held (the reader saw
// it as "sent" for at most the window's length; nothing persists).
//
// One held message per execution at most: holding another commits the earlier
// one first (`holdSend`). The entries are closures built by useExecutionActions;
// this file only owns the timer and the order of events.

export interface HeldSend {
  /** The window ended (or was cut short): issue the POST. */
  fire: () => void
  /** The reader took it back: undo the optimistic line and restore the text. */
  undo: () => void
}

const held = new Map<string, { entry: HeldSend; timer: ReturnType<typeof setTimeout> }>()

/** Commits the execution's held message right now; false when nothing was held. */
export function flushSend(key: string): boolean {
  const h = held.get(key)
  if (!h) return false
  clearTimeout(h.timer)
  held.delete(key)
  h.entry.fire()
  return true
}

/** Holds `entry` for `ms`, then fires it. Any earlier held message of the execution is committed first. */
export function holdSend(key: string, entry: HeldSend, ms: number): void {
  flushSend(key)
  held.set(key, { entry, timer: setTimeout(() => flushSend(key), ms) })
}

/** Takes the held message back; false when nothing was held (the window already ended). */
export function cancelSend(key: string): boolean {
  const h = held.get(key)
  if (!h) return false
  clearTimeout(h.timer)
  held.delete(key)
  h.entry.undo()
  return true
}

export function hasHeldSend(key: string): boolean {
  return held.has(key)
}

/** Tests only: module state outlives a test. */
export function clearAllHeldSends(): void {
  for (const h of held.values()) clearTimeout(h.timer)
  held.clear()
}
