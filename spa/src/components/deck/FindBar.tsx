// spa/src/components/deck/FindBar.tsx — the ⌘F search bar of the deck and of the chat (#2665): a slim bar above the stream with the input,
// `n / m`, previous / next and close. Enter = next, Shift+Enter = previous, Esc = close. It searches the data (`units`, find-units.ts)
// and marks the DOM (search-highlight.ts); the matching core is lib/nex/transcript-search.ts, the same one the worker room used.
//
// - **State is outside** (find-memory.ts, CLAUDE.md tab-hosted rule): the word, the match the reader stands on and the window of kept matches
//   survive a tab switch and a deck ⇄ chat switch. The current match is remembered as (unit, ordinal), so a message landing before it does not
//   move it; gone, the nearest following one takes over. The units differ between the two views: `relocate` re-seats it on the new ones.
// - **Index**: the caller builds `units` only while the bar is open (an open bar with an empty query still builds none that matter: nothing
//   searches below the minimum length). Typing re-runs `findMatches` once per keystroke.
// - **Moving** to a match expands its `reveal` keys first (a far-off turn that is drawn as plain text, a fold); the layout effect that sees
//   them still closed expands them and returns, and the one after that commit marks and scrolls.
// - **Marks** are Ranges over live text nodes, which collapse when React replaces them, so they are re-applied after every commit that can
//   touch the units — without scrolling: only moving scrolls.
// - The stream holds its bottom-follow while the bar is open (`useTranscriptScroll` hold), so a streaming reply cannot pull the reader off a
//   match; every jump releases it too (`onJump`), since a match on the last screen leaves the box close enough to the end to read as "at the bottom".
import { useEffect, useLayoutEffect, useMemo, useRef, type KeyboardEvent } from 'react'
import { CaretDown, CaretUp, MagnifyingGlass, X } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import {
  ANCHOR_END, ANCHOR_START, anchorOrdinal, anchorPosition, findCurrent, findMatches, firstAtOrAfter, matchIdentity, normalizeQuery, relocate,
  SEARCH_MATCH_LIMIT, unitAnchor, type SearchResult, type SearchUnit, type UnitAnchor,
} from '../../lib/nex/transcript-search'
import { clearSearchHighlights, firstUnitInView, highlightSearch } from '../../lib/nex/search-highlight'
import { patchFind, useFindState } from '../../lib/conversations/find-memory'
import type { FoldStore } from '../room/fold-context'

export interface FindBarProps {
  /** The find-memory key (`findKey(paneId, sessionId)`). */
  memoryKey: string
  /** Whose marks these are (search-highlight owners): the pane id. */
  owner: string
  /** The stream's scroll box, as state: a view switch mounts a new one and the marks must follow it. */
  container: HTMLElement | null
  units: readonly SearchUnit[]
  fold: FoldStore
  onClose: () => void
  /** Called after every jump to a match: the stream releases its bottom-follow, so a match on the last screen stays put. */
  onJump?: () => void
}

const NO_RESULT: SearchResult = { matches: [], truncated: false, truncatedBefore: false, truncatedAfter: false }

/** Where a stream opens: its end, at once. */
function scrollToEnd(el: HTMLElement): void {
  el.scrollTop = el.scrollHeight
}

/** `findMatches` remembering its last answer: a keystroke computes it once in onChange, and the render that follows asks again. */
function finder(units: readonly SearchUnit[]) {
  let last: { query: string; at: number; ordinal: number; result: SearchResult } | null = null
  return (query: string, anchor: UnitAnchor): SearchResult => {
    const at = anchorPosition(units, anchor)
    const ordinal = anchorOrdinal(units, anchor)
    if (last && last.query === query && last.at === at && last.ordinal === ordinal) return last.result
    last = { query, at, ordinal, result: findMatches(units, query, SEARCH_MATCH_LIMIT, at, ordinal) }
    return last.result
  }
}

/** A ⌘F that came from the right panel (or its overlay) is the panel's, not the stream's: the stream may be inert behind a modal one. */
export function fromRightPanel(target: EventTarget | null): boolean {
  return target instanceof Element && target.closest('[data-testid="session-right-panel"], [data-testid="split-overlay"]') !== null
}

const BUTTON = 'p-1 rounded text-text-muted hover:text-text-primary hover:bg-surface-hover disabled:opacity-40 disabled:pointer-events-none'

export function FindBar({ memoryKey, owner, container, units, fold, onClose, onJump }: FindBarProps) {
  const t = useI18nStore((s) => s.t)
  const { query, sel, win, focus } = useFindState(memoryKey)
  const inputRef = useRef<HTMLInputElement>(null)
  // Set by whatever moves to a match (typing, next, previous, coming back to the bar); the layout effect scrolls once and clears it.
  // A bar that mounts with a search already in it (a tab or view switch) takes the reader to the match again.
  const wantScroll = useRef(true)
  const searching = normalizeQuery(query) !== null
  const search = useMemo(() => finder(units), [units])

  // The units changed (a message landing): re-seat the current match and the window on these units. (After the commit: the memory is an
  // external store, which a render must not write.)
  const prevUnits = useRef(units)
  useLayoutEffect(() => {
    const prev = prevUnits.current
    if (prev === units) return
    prevUnits.current = units
    patchFind(memoryKey, sel ? { sel: relocate(prev, units, sel), win: relocate(prev, units, win) } : { win: relocate(prev, units, win) })
  }, [units, memoryKey, sel, win])
  const { matches, truncated, truncatedBefore, truncatedAfter } = searching ? search(query, win) : NO_RESULT
  const current = useMemo(() => findCurrent(units, matches, sel), [units, matches, sel])

  useEffect(() => {
    inputRef.current?.focus()
    inputRef.current?.select()
  }, [focus])

  // A mount with no current match takes the reader to the bottom, where a stream opens (it does not place itself while the bar holds it).
  const toBottom = useRef(true)
  useLayoutEffect(() => {
    if (!container?.isConnected) return
    const match = matches[current]
    if (toBottom.current && (!searching || !match)) scrollToEnd(container)
    toBottom.current = false
    if (!searching) {
      clearSearchHighlights(owner)
      return
    }
    if (wantScroll.current && match && match.reveal.some((key) => !fold.isExpanded(key))) {
      // Not on screen yet: open what hides it; this runs again after that commit.
      fold.expand(match.reveal)
      return
    }
    const jump = wantScroll.current && current >= 0
    highlightSearch(owner, container, query, matches, current, { scroll: wantScroll.current })
    wantScroll.current = false
    if (jump) onJump?.()
  }, [owner, container, searching, query, matches, current, fold, units, onJump])

  useLayoutEffect(() => () => clearSearchHighlights(owner), [owner])

  // A query refining one that had a match stays on it (or the next one); a new search starts at the first match at or below the top of the
  // viewport, else wraps to the first, like a browser's find.
  const onChange = (value: string) => {
    wantScroll.current = true
    if (normalizeQuery(value) === null) {
      patchFind(memoryKey, { query: value, sel: null })
      return
    }
    if (sel) {
      const { matches: refined } = search(value, win)
      patchFind(memoryKey, { query: value, sel: matchIdentity(units, refined, findCurrent(units, refined, sel)) })
      return
    }
    let anchor = unitAnchor(units, container?.isConnected ? firstUnitInView(container) : null)
    let result = search(value, anchor)
    let i = firstAtOrAfter(units, result.matches, anchorPosition(units, anchor))
    if (i < 0) {
      // Nothing from the viewport down: the very first match.
      if (result.truncatedBefore) {
        anchor = ANCHOR_START
        result = search(value, anchor)
      }
      i = 0
    }
    patchFind(memoryKey, { query: value, win: anchor, sel: matchIdentity(units, result.matches, i) })
  }

  // One step. Past an end of the kept matches it re-centres them on where it goes: past the last, onto the ones that follow when some were
  // cut, else round to the very first; before the first, likewise backwards.
  const move = (delta: 1 | -1) => {
    const n = matches.length
    if (n === 0) return
    wantScroll.current = true
    const i = current < 0 ? 0 : current + delta
    if (i >= 0 && i < n) {
      patchFind(memoryKey, { sel: matchIdentity(units, matches, i) })
      return
    }
    const edge = matches[i < 0 ? 0 : n - 1]
    const cut = i < 0 ? truncatedBefore : truncatedAfter
    let anchor: UnitAnchor
    let result: SearchResult
    let j: number
    if (cut) {
      // Onwards: re-centre on the edge match itself — not its unit, which may hold more matches than the window — and step from it there.
      anchor = { ...unitAnchor(units, edge.unitId), ordinal: edge.ordinal }
      result = search(query, anchor)
      const at = findCurrent(units, result.matches, matchIdentity(units, [edge], 0))
      j = Math.max(0, Math.min(result.matches.length - 1, at + delta))
    } else {
      // Round: the very first (or last) match, recomputing only if it was cut.
      anchor = i < 0 ? (truncatedAfter ? ANCHOR_END : win) : (truncatedBefore ? ANCHOR_START : win)
      result = search(query, anchor)
      j = i < 0 ? result.matches.length - 1 : 0
    }
    patchFind(memoryKey, { win: anchor, sel: matchIdentity(units, result.matches, j) })
  }

  const onInputKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key !== 'Enter') return
    // An IME's Enter commits the composition; it is not a search step.
    if (e.nativeEvent.isComposing) return
    e.preventDefault()
    move(e.shiftKey ? -1 : 1)
  }

  // Escape anywhere in the bar — the input or a button.
  const onBarKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key !== 'Escape') return
    // An IME's Escape cancels the composition.
    if (e.nativeEvent.isComposing) return
    // Already handled — a dialog above takes Escape in the capture phase and marks it: one Escape closes one thing.
    if (e.defaultPrevented) return
    e.preventDefault()
    e.stopPropagation()
    onClose()
  }

  const count = !searching ? ''
    : matches.length === 0 ? t('room.search.none')
    : t(truncated ? 'room.search.count_more' : 'room.search.count', { current: current + 1, total: matches.length })

  return (
    <div data-testid="find-bar" role="search" onKeyDown={onBarKeyDown} className="shrink-0 border-b border-border-subtle bg-surface-secondary">
      <div className="flex items-center gap-1.5 px-3 py-1">
        <MagnifyingGlass size={14} className="shrink-0 text-text-muted" />
        <input
          ref={inputRef}
          data-testid="find-input"
          type="search"
          value={query}
          onChange={(e) => onChange(e.target.value)}
          onKeyDown={onInputKeyDown}
          placeholder={t('room.search.placeholder')}
          aria-label={t('room.search.placeholder')}
          className="min-w-0 flex-1 bg-transparent text-sm text-text-primary outline-none placeholder:text-text-muted [&::-webkit-search-cancel-button]:hidden"
        />
        {/* Always mounted: a live region added with its text is not announced. */}
        <span data-testid="find-count" aria-live="polite" className="shrink-0 text-xs tabular-nums text-text-muted">{count}</span>
        <button type="button" data-testid="find-prev" className={BUTTON} disabled={matches.length === 0} onClick={() => move(-1)}
          title={t('room.search.prev')} aria-label={t('room.search.prev')}>
          <CaretUp size={14} />
        </button>
        <button type="button" data-testid="find-next" className={BUTTON} disabled={matches.length === 0} onClick={() => move(1)}
          title={t('room.search.next')} aria-label={t('room.search.next')}>
          <CaretDown size={14} />
        </button>
        <button type="button" data-testid="find-close" className={BUTTON} onClick={onClose}
          title={t('room.search.close')} aria-label={t('room.search.close')}>
          <X size={14} />
        </button>
      </div>
    </div>
  )
}
