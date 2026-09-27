// spa/src/components/room/TranscriptSearch.tsx — the worker pane's search bar
// (R3 plan T3.3, user decision Q4: search covers the whole conversation,
// folded content included; room and chat alike).
//
// A slim bar at the top of the transcript: input, `n / m`, previous / next,
// close. Enter = next, Shift+Enter = previous, Escape = close (the pane puts
// focus back where it was). It searches the data, not the DOM
// (transcript-search.ts), and marks the DOM (search-highlight.ts):
//
// - **Index** (A11): `buildSearchUnits` runs when the transcript changes —
//   never per keystroke; typing re-runs only `findMatches`.
// - **Current match** (A5): remembered as (unit, ordinal within the unit), so
//   a match landing before it — a message, a chat tools line gaining a call —
//   does not move it; gone, the nearest following one (else the last) takes
//   over. Streaming partials are not indexed: a reply becomes searchable when
//   it lands in `messages`.
// - **Moving** to a match expands its `reveal` keys first; the layout effect
//   that sees them still closed expands them and returns, and the one after
//   that commit marks and scrolls.
// - **Marks** (A8) are Ranges over live text nodes, which collapse when React
//   replaces them, so they are re-applied after every commit that can touch
//   the transcript — without scrolling: only moving scrolls.
// - The pane holds the transcript's bottom-follow while the bar is open (A4,
//   `holdScroll`), so a streaming reply does not pull the reader off a match;
//   every jump also releases it (`onJump`, A F4), since a match on the last
//   screen leaves the box close enough to the end to read as "at the bottom".
import { useEffect, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent, type RefObject } from 'react'
import { CaretDown, CaretUp, MagnifyingGlass, X } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { indexOperations } from '../../lib/nex/operations'
import {
  buildSearchUnits, findCurrent, findMatches, matchIdentity, normalizeQuery, type MatchIdentity,
} from '../../lib/nex/transcript-search'
import { clearSearchHighlights, highlightSearch } from '../../lib/nex/search-highlight'
import type { StreamMessage } from '../../lib/nex/message-types'
import type { ToolActivity } from '../../lib/nex/tool-activity'
import { useFoldStore } from './fold-context'

export interface TranscriptSearchProps {
  /** Whose marks these are (search-highlight owners): the pane id. */
  owner: string
  /** The transcript's scroll container (its `scrollRef`). */
  scrollRef: RefObject<HTMLDivElement | null>
  messages: StreamMessage[]
  tools?: Record<string, ToolActivity>
  view: 'room' | 'chat'
  keyPrefix: string
  turnStarts: readonly number[]
  onClose: () => void
  /** Bumped by the pane on every Mod+F: focus (and select) the input again. */
  focusRequest?: number
  /**
   * Called after every jump to a match: the pane releases the transcript's
   * bottom-follow (A F4), so a match on the last screen stays put.
   */
  onJump?: () => void
}

const BUTTON = 'p-1 rounded text-text-muted hover:text-text-primary hover:bg-surface-hover disabled:opacity-40 disabled:pointer-events-none'

export default function TranscriptSearch({
  owner, scrollRef, messages, tools, view, keyPrefix, turnStarts, onClose, focusRequest = 0, onJump,
}: TranscriptSearchProps) {
  const t = useI18nStore((s) => s.t)
  const foldStore = useFoldStore()
  const inputRef = useRef<HTMLInputElement>(null)
  const [query, setQuery] = useState('')
  const [sel, setSel] = useState<MatchIdentity | null>(null)
  // Set by whatever moves to a match (typing, next, previous); the layout
  // effect scrolls once and clears it. Every other re-mark stays put.
  const wantScroll = useRef(false)

  const units = useMemo(
    () => buildSearchUnits({ messages, index: indexOperations(messages), tools, view, keyPrefix, turnStarts }),
    [messages, tools, view, keyPrefix, turnStarts],
  )
  const { matches, truncated } = useMemo(() => findMatches(units, query), [units, query])
  const current = useMemo(() => findCurrent(units, matches, sel), [units, matches, sel])
  const searching = normalizeQuery(query) !== null

  useEffect(() => {
    inputRef.current?.focus()
    inputRef.current?.select()
  }, [focusRequest])

  // Mark (A8) after every commit that can have touched the transcript: its
  // data, its folds (foldStore changes with them), the view, the query and
  // the current match. `messages`, `tools` and `view` are listed even though
  // `matches` follows them, because a commit that redraws a unit without
  // changing any match still replaces its text nodes.
  useLayoutEffect(() => {
    const container = scrollRef.current
    if (!container) return
    if (!searching) {
      clearSearchHighlights(owner)
      return
    }
    const match = matches[current]
    if (wantScroll.current && match && match.reveal.some((key) => !foldStore.isExpanded(key))) {
      // Not on screen yet: open what hides it; this runs again after that commit.
      foldStore.expand(match.reveal)
      return
    }
    const jump = wantScroll.current && current >= 0
    highlightSearch(owner, container, query, matches, current, { scroll: wantScroll.current })
    wantScroll.current = false
    if (jump) onJump?.()
  }, [owner, scrollRef, searching, query, matches, current, sel, foldStore, messages, tools, view, onJump])

  useLayoutEffect(() => () => clearSearchHighlights(owner), [owner])

  const onChange = (value: string) => {
    setQuery(value)
    // Pin the identity of the match the new query lands on, so a transcript
    // change before the next keystroke cannot move it (findMatches runs again
    // in the memo; it is linear and bounded).
    const next = findMatches(units, value).matches
    setSel(matchIdentity(units, next, findCurrent(units, next, sel)))
    wantScroll.current = true
  }

  const move = (delta: 1 | -1) => {
    const n = matches.length
    if (n === 0) return
    const i = current < 0 ? 0 : (current + delta + n) % n
    setSel(matchIdentity(units, matches, i))
    wantScroll.current = true
  }

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') {
      // An IME's Enter commits the composition; it is not a search step.
      if (e.nativeEvent.isComposing) return
      e.preventDefault()
      move(e.shiftKey ? -1 : 1)
    } else if (e.key === 'Escape') {
      // Already handled — a dialog above takes Escape in the capture phase
      // and marks it: one Escape closes one thing (R1-3).
      if (e.defaultPrevented) return
      e.preventDefault()
      e.stopPropagation()
      onClose()
    }
  }

  const count = !searching ? null
    : matches.length === 0 ? t('room.search.none')
    : t(truncated ? 'room.search.count_more' : 'room.search.count', { current: current + 1, total: matches.length })

  return (
    <div data-testid="transcript-search" role="search"
      className="shrink-0 flex items-center gap-1.5 px-3 py-1 border-b border-border-subtle bg-surface-secondary">
      <MagnifyingGlass size={14} className="shrink-0 text-text-muted" />
      <input
        ref={inputRef}
        data-testid="transcript-search-input"
        type="search"
        value={query}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={onKeyDown}
        placeholder={t('room.search.placeholder')}
        aria-label={t('room.search.placeholder')}
        className="flex-1 min-w-0 bg-transparent text-sm text-text-primary placeholder:text-text-muted outline-none [&::-webkit-search-cancel-button]:hidden"
      />
      {count !== null && (
        <span data-testid="transcript-search-count" aria-live="polite" className="shrink-0 text-xs text-text-muted tabular-nums">
          {count}
        </span>
      )}
      <button type="button" data-testid="transcript-search-prev" className={BUTTON} disabled={matches.length === 0}
        onClick={() => move(-1)} title={t('room.search.prev')} aria-label={t('room.search.prev')}>
        <CaretUp size={14} />
      </button>
      <button type="button" data-testid="transcript-search-next" className={BUTTON} disabled={matches.length === 0}
        onClick={() => move(1)} title={t('room.search.next')} aria-label={t('room.search.next')}>
        <CaretDown size={14} />
      </button>
      <button type="button" data-testid="transcript-search-close" className={BUTTON}
        onClick={onClose} title={t('room.search.close')} aria-label={t('room.search.close')}>
        <X size={14} />
      </button>
    </div>
  )
}
