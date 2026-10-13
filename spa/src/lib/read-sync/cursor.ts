// spa/src/lib/read-sync/cursor.ts — the hook cursor as a read position (app-badge spec §7.2).
//
// T is `{epoch, seq}`: the `(epoch, seq)` the daemon stamps on every `hook` frame. It orders the events of one daemon
// process exactly (no clock), and a new epoch (a daemon restart) makes older ones incomparable. A read covers an
// unread iff they share the epoch and the unread's seq <= T.seq; a different epoch never clears anything. An unread
// with no cursor (an event from before this feature, or a frame not from the emit slot: epoch '') is covered by any
// read of its code.

export interface ReadCursor {
  epoch: string
  seq: number
}

const isSeq = (v: unknown): v is number => typeof v === 'number' && Number.isInteger(v) && v >= 0

/** The cursor an event carries, or undefined (epoch '' / absent: not from the emit slot, or from before the feature). */
export function cursorOf(e: { epoch?: unknown; seq?: unknown }): ReadCursor | undefined {
  if (typeof e.epoch !== 'string' || e.epoch === '' || !isSeq(e.seq)) return undefined
  return { epoch: e.epoch, seq: e.seq }
}

/** The wire `through` (`{epoch, seq}`), or undefined when it is not one. */
export function parseReadCursor(v: unknown): ReadCursor | undefined {
  if (typeof v !== 'object' || v === null || Array.isArray(v)) return undefined
  return cursorOf(v as Record<string, unknown>)
}

/** Does a read through `through` clear an unread set by an event with cursor `unread` (undefined: no cursor)? */
export function covers(unread: ReadCursor | undefined, through: ReadCursor): boolean {
  if (unread === undefined) return true
  return unread.epoch === through.epoch && unread.seq <= through.seq
}

/**
 * The larger of two cursors captured for one session, `b` the later capture. Within an epoch the larger seq; across
 * epochs they are incomparable, so the later capture wins. A captured cursor beats none: sending none means "the newest
 * the daemon has emitted", which can cover an event this device never saw.
 */
export function maxCursor(a: ReadCursor | undefined, b: ReadCursor | undefined): ReadCursor | undefined {
  if (a === undefined) return b
  if (b === undefined) return a
  if (a.epoch === b.epoch) return b.seq >= a.seq ? b : a
  return b
}
