// spa/src/lib/conversations/model.ts — one conversation as the client holds it, and the pure transitions that keep it true
// to the daemon's (U1 spec §8.2 client rules; U3 plan D4). No store, no I/O: `useConversationStore` calls these.
//
// The rules, in one place:
//  - a snapshot (first, after a `conversation.reset`, or a stale cursor's answer) replaces the document;
//  - a changes frame upserts the turn header by id and each item by id, placing it by `index` (its position in the turn's
//    FULL item list); every change is the item's full current state, so applying one twice is harmless;
//  - an item whose `index` is below the turn's `omitted_items` is one of the omitted ones: skipped, it stays counted in
//    「前面還有 N 步未顯示」;
//  - a change for a turn older than the loaded `first_index` is ignored (it arrives again when the client pages back);
//  - the cursor moves only on `conversation.*` live frames, never on a paging (`before=` / `around=`) answer.
import type {
  Capabilities, Change, ConversationApproval, ConversationItem, Header, Increment, Snapshot, Turn, TurnHeader, Window,
} from './types'

export interface ConversationDoc {
  /** Held turns in index order (a window: older ones are paged in, newer ones arrive live). */
  turns: Turn[]
  header: Header | null
  capabilities: Capabilities | null
  /** The live position: only live frames and a replacing snapshot move it. '' = nothing loaded yet. */
  cursor: string
  /** There are older turns than the first one held (drives `loadBefore`). */
  hasMoreBefore: boolean
  /** The window shows a stretch around a jump and does not reach the newest turn: live changes are not merged in. */
  detached: boolean
  totalTurns: number
  /** The open approvals of this conversation (the WebSocket's set; a snapshot replaces it). */
  approvals: ConversationApproval[]
  /**
   * Bumped by every replacing snapshot (a new epoch, a reset). A paging or jump request remembers the generation it was
   * issued under and its answer is dropped if a snapshot has replaced the document since: a late page of the old epoch
   * must not put obsolete turns, items or a stale position into the new one.
   */
  generation: number
}

export function emptyDoc(): ConversationDoc {
  return { turns: [], header: null, capabilities: null, cursor: '', hasMoreBefore: false, detached: false, totalTurns: 0, approvals: [], generation: 0 }
}

const byIndex = (a: { index: number }, b: { index: number }) => a.index - b.index

/** A turn from the wire with its items in index order (the daemon sends them so; this is the cheap guard). */
function normalizeTurn(t: Turn): Turn {
  const items = t.items ?? []
  for (let i = 1; i < items.length; i++) {
    if (items[i].index < items[i - 1].index) return { ...t, items: [...items].sort(byIndex) }
  }
  return t.items ? t : { ...t, items }
}

/** The turn's `omitted_items` boundary: items below it are not in the document. */
const boundary = (t: TurnHeader): number => t.omitted_items ?? 0

/** The snapshot replaces the document: turns, header, capabilities, cursor, approvals kept (a reset does not close them). */
export function applySnapshot(doc: ConversationDoc, snap: Snapshot): ConversationDoc {
  const turns = (snap.conversation.turns ?? []).map(normalizeTurn).sort(byIndex)
  return {
    ...doc,
    turns,
    header: snap.header,
    capabilities: snap.conversation.capabilities ?? doc.capabilities,
    cursor: snap.cursor,
    hasMoreBefore: snap.window.has_more_before,
    detached: false,
    totalTurns: snap.window.total_turns,
    generation: doc.generation + 1,
  }
}

/** An older page (`before=`): its turns go in front. The cursor, header and capabilities stay: they are the live ones. */
export function applyOlderPage(doc: ConversationDoc, snap: Snapshot, generation: number): ConversationDoc {
  if (generation !== doc.generation) return doc // asked under an epoch that a snapshot has since replaced
  const incoming = (snap.conversation.turns ?? []).map(normalizeTurn)
  const have = new Set(doc.turns.map((t) => t.index))
  const merged = [...incoming.filter((t) => !have.has(t.index)), ...doc.turns].sort(byIndex)
  return { ...doc, turns: merged, hasMoreBefore: snap.window.has_more_before, totalTurns: Math.max(doc.totalTurns, snap.window.total_turns) }
}

/**
 * A jump (`around=`): the window around an item replaces the turns, but is NOT the live position — the cursor stays, and
 * while the window does not reach the newest turn live changes are not merged (`detached`) until a snapshot returns.
 */
export function applyAround(doc: ConversationDoc, snap: Snapshot, generation: number): ConversationDoc {
  if (generation !== doc.generation) return doc
  const turns = (snap.conversation.turns ?? []).map(normalizeTurn).sort(byIndex)
  const w: Window = snap.window
  return {
    ...doc,
    turns,
    hasMoreBefore: w.has_more_before,
    detached: w.last_index < w.total_turns - 1,
    totalTurns: w.total_turns,
  }
}

function upsertItems(turn: Turn, incoming: ConversationItem[], bound: number): Turn {
  let items = turn.items
  let changed = false
  for (const it of incoming) {
    if (it.index < bound) continue // an omitted item
    const at = items.findIndex((x) => x.id === it.id)
    if (at >= 0) {
      if (items === turn.items) items = [...items]
      items[at] = it // an item's index is stable within an epoch: it keeps its place
      changed = true
      continue
    }
    if (items === turn.items) items = [...items]
    // insert by index, keeping the order the daemon defines
    let pos = items.length
    while (pos > 0 && items[pos - 1].index > it.index) pos--
    items.splice(pos, 0, it)
    changed = true
  }
  return changed ? { ...turn, items } : turn
}

function applyChange(turns: Turn[], ch: Change, firstIndex: number | null): Turn[] {
  const header = ch.turn
  if (firstIndex !== null && header.index < firstIndex) return turns // older than what is loaded
  let at = turns.findIndex((t) => t.id === header.id)
  if (at < 0) at = turns.findIndex((t) => t.index === header.index) // same slot, a re-keyed turn
  if (at >= 0) {
    const cur = turns[at]
    // Rebuilt from the incoming header (a reopened turn must lose its old `ended_at`, a cleared `error` must go), keeping
    // only what is view-local: the items held and the omitted boundary the snapshot gave
    const merged: Turn = { ...header, omitted_items: header.omitted_items ?? cur.omitted_items, items: cur.items }
    // the boundary of what the document holds: an increment usually does not repeat `omitted_items` (a view-only field),
    // and the spread above keeps the one the snapshot gave
    const next = upsertItems(merged, ch.items, boundary(merged))
    const out = turns.slice()
    out[at] = next
    return out
  }
  const fresh = upsertItems({ ...header, items: [] }, ch.items, boundary(header))
  const out = turns.slice()
  let pos = out.length
  while (pos > 0 && out[pos - 1].index > fresh.index) pos--
  out.splice(pos, 0, fresh)
  return out
}

/** A live changes frame (or a catch-up increment): merged into the window, the cursor and header taken. */
export function applyChanges(doc: ConversationDoc, inc: Increment): ConversationDoc {
  let turns = doc.turns
  if (!doc.detached) {
    const first = turns.length > 0 ? turns[0].index : null
    for (const ch of inc.changes) turns = applyChange(turns, ch, first)
  }
  const last = turns.length > 0 ? turns[turns.length - 1].index : -1
  return { ...doc, turns, header: inc.header, cursor: inc.cursor, totalTurns: Math.max(doc.totalTurns, last + 1) }
}

/** `conversation.header`: only the header (title, status, usage) changed. */
export function applyHeader(doc: ConversationDoc, header: Header, cursor: string): ConversationDoc {
  return { ...doc, header, cursor }
}

export function applyCapabilities(doc: ConversationDoc, capabilities: Capabilities): ConversationDoc {
  return { ...doc, capabilities }
}

/** `approvals.snapshot` replaces the set (client rule §8.2). */
export function applyApprovals(doc: ConversationDoc, approvals: ConversationApproval[]): ConversationDoc {
  return { ...doc, approvals }
}

/** `approval {op}`: opened adds (or replaces by id), closed removes. */
export function applyApprovalOp(doc: ConversationDoc, op: 'opened' | 'closed', approval: ConversationApproval): ConversationDoc {
  const rest = doc.approvals.filter((a) => a.id !== approval.id)
  return { ...doc, approvals: op === 'opened' ? [...rest, approval] : rest }
}

/** Where a client resumes a reading position after a reset: the turn and item ids it was looking at, if still there. */
export function hasItem(doc: ConversationDoc, itemId: string): boolean {
  return doc.turns.some((t) => t.items.some((i) => i.id === itemId))
}

/** The lowest turn index held (the paging edge), or null. */
export function firstIndex(doc: ConversationDoc): number | null {
  return doc.turns.length > 0 ? doc.turns[0].index : null
}
