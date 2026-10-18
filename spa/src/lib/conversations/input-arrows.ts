// spa/src/lib/conversations/input-arrows.ts — Up / Down in the deck's input, as pure decisions (#2687). Three things were tangled in SessionInput's keydown
// handler: whether the key is ours at all (take every held message back, walk the sent history, or leave it to the caret), where the caret is, and
// what a history step does to the walk. Each is a function of plain values here — a state table, unit-tested — and the component keeps the DOM (the
// caret, the IME guard, `preventDefault`) and the writes (draft, chips, the memories). Behaviour is the handler's, unchanged.
//
// Pure: no React, no store, no DOM.

export type ArrowDir = 'up' | 'down'

/** What a key press is, as far as the arrows care. */
export interface KeyFacts {
  key: string
  shiftKey: boolean
  altKey: boolean
  metaKey: boolean
  ctrlKey: boolean
}

/** The direction of a bare arrow press (no modifier), else null: a shifted / modified arrow is the caret's or the browser's. */
export function arrowDir(e: KeyFacts): ArrowDir | null {
  if (e.shiftKey || e.altKey || e.metaKey || e.ctrlKey) return null
  if (e.key === 'ArrowUp') return 'up'
  if (e.key === 'ArrowDown') return 'down'
  return null
}

/**
 * Whether the box is empty in the sense Up cares about: nothing typed, no chip shown, no upload on its way — and no draft that Esc just cleared
 * (its undo window is the box's, so it is not overwritten).
 */
export function isEmptyBox(f: { draft: string; shownChips: number; uploading: number; hasCleared: boolean }): boolean {
  return f.draft === '' && f.shownChips === 0 && f.uploading === 0 && !f.hasCleared
}

/**
 * Whether the caret is where an arrow may leave the box: a collapsed caret on the draft's first line (Up) / last line (Down). A selection is the
 * caret's own business.
 */
export function caretAtEdge(dir: ArrowDir, draft: string, selectionStart: number, selectionEnd: number): boolean {
  if (selectionStart !== selectionEnd) return false
  return dir === 'up' ? !draft.slice(0, selectionStart).includes('\n') : !draft.slice(selectionEnd).includes('\n')
}

/** What an arrow press means for the input. */
export type ArrowDecision =
  /** Up in an empty box or on the first line of the draft: call back every message pending (the queue goes in front of the draft). */
  | 'take_back_all'
  /** Try a history step (it may still decline: nothing to walk to). */
  | 'history'
  /** Not ours: the caret moves as usual. */
  | 'caret'

export interface ArrowFacts {
  dir: ArrowDir
  /** The adapter can take a message back (a terminal does; a worker's pane does not). */
  recall: boolean
  emptyBox: boolean
  /** The box still shows exactly what the last history step put there. */
  walking: boolean
  /** `caretAtEdge` for this press. */
  atEdge: boolean
  /** A draft that Esc just cleared is within its undo window: what comes back must not be written over it. */
  clearedPending: boolean
  /** Asked only when everything before it says yes (it reads the queue). */
  canTakeBackAll: () => boolean
}

/**
 * The order is the rule: taking messages back comes first (Up, in an empty box or on the first line of the draft, with something to take back: MS2-4, L5,
 * the terminal's own Up); then the sent history — a shown history item
 * is a step only with the caret on its first line (Up) / last line (Down), an edited one is an ordinary draft; a walk that is not on starts only
 * with Up in an empty box. Everything else is the caret's.
 */
export function decideArrow(f: ArrowFacts): ArrowDecision {
  if (f.dir === 'up' && f.recall && (f.emptyBox || (f.atEdge && !f.clearedPending)) && f.canTakeBackAll()) return 'take_back_all'
  if (f.walking ? !f.atEdge : !(f.dir === 'up' && f.emptyBox)) return 'caret'
  return 'history'
}

// === The history walk ===

/** The sent messages as the walk sees them: an entry carries every member id of a collapsed run of identical messages. */
export interface HistoryEntry { ids: readonly string[] }

/** `ids` are every member id of the item the box shows (its identity, not a position: the list changes under the walk). */
export interface WalkState { ids: readonly string[]; shown: string; chipIds: readonly string[] }

/** What one history step does, before anything is written. */
export type StepPlan =
  /** The key is not a step. `forgetWalk`: the walk ended (it was edited away, or its item is gone). `dropChips`: the walk's restored chips go with it. */
  | { kind: 'none'; forgetWalk: boolean; dropChips: boolean }
  /** Past the newest: the walk ends in an empty box (its restored chips go). */
  | { kind: 'end' }
  /** Show the entry at `pos` (the walk's earlier chips, if any, go first). */
  | { kind: 'show'; pos: number }

/**
 * One Up / Down over `list` (oldest first). `old` is the stored walk; it is *on* only while the box still shows what it put there (`draft`).
 * `canStart`: the box is empty and nothing else owns Up — it only matters for a walk that is not on.
 */
export function planHistoryStep(
  list: readonly HistoryEntry[], old: WalkState | undefined, draft: string, dir: ArrowDir, canStart: boolean,
): StepPlan {
  const walk = old && old.shown === draft ? old : undefined
  let pos: number
  if (!walk) {
    // an edited-away walk: its restored chips go with it, whether or not a new one starts
    const gone = { forgetWalk: true, dropChips: true }
    if (dir === 'down' || !canStart || list.length === 0) return { kind: 'none', ...gone }
    pos = list.length - 1
  } else {
    // relocate by intersection of member ids: pages, removals and collapses shift positions
    const at = list.findIndex((e) => e.ids.some((id) => walk.ids.includes(id)))
    if (at < 0) return { kind: 'none', forgetWalk: true, dropChips: false } // the item is gone: the walk ends, the box stays as it is
    if (dir === 'up') {
      if (at <= 0) return { kind: 'none', forgetWalk: false, dropChips: false }
      pos = at - 1
    } else pos = at + 1
  }
  return pos >= list.length ? { kind: 'end' } : { kind: 'show', pos }
}
