// spa/src/components/deck/DeckView.tsx — the deck (指揮台) of one conversation (U3 spec §4, plan D5): every turn's items in
// order, older turns read when the reader reaches the top, the reader's place and unfolded parts kept per pane across a
// tab switch (CLAUDE.md tab-hosted rule: `fold-memory` and the scroll memo are outside the component). The input and
// whatever else lives under the stream is the caller's `footer`, drawn below the scrolling box.
import { memo, useCallback, useEffect, useMemo, useRef, type ReactNode, type UIEvent } from 'react'
import { FoldContext } from '../room/fold-context'
import { useTranscriptScroll } from '../../hooks/useTranscriptScroll'
import { noteDeckPane, usePaneFoldStore } from '../../lib/conversations/fold-memory'
import { SCROLL_ANCHOR_CLASS } from '../../lib/nex/transcript-scroll-memory'
import type { Turn } from '../../lib/conversations/types'
import { useConversationStore, type ConversationEntry } from '../../stores/useConversationStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { DeckItem } from './DeckItem'
import { footerContext, type DeckFooterContext } from './footer-context'
import type { StepActions } from './StepViews'
import type { PanelContent } from '../../lib/conversations/panel-memory'

export type { DeckFooterContext }

export interface DeckViewProps {
  paneId: string
  hostId: string
  sessionId: string
  entry: ConversationEntry
  onSwitchToTerminal: () => void
  footer?: (ctx: DeckFooterContext) => ReactNode
  /** 「顯示全部」 and a subagent line open the pane's right panel on that step (the turn is known here, not to the step). */
  onOpenPanel?: (content: PanelContent) => void
}

/** Within this many pixels of the top the next older page is read. */
const TOP_REACH = 120
/** Automatic asks for an older page while the content is too short to scroll: how many per first turn, and the first wait. */
const AUTO_PAGE_TRIES = 3
const AUTO_PAGE_RETRY_MS = 1000

// Memoized: the store keeps an unchanged turn's object, so a live update re-draws only the turn that changed.
const TurnView = memo(function TurnView({ turn, onOpenPanel }: { turn: Turn; onOpenPanel?: (content: PanelContent) => void }) {
  const t = useI18nStore((s) => s.t)
  const actions = useMemo<StepActions | undefined>(() => onOpenPanel && ({
    onShowAll: (s) => onOpenPanel({ kind: 'output', turnId: turn.id, stepId: s.id }),
    onOpenSubagent: (s) => onOpenPanel({ kind: 'subagent', turnId: turn.id, stepId: s.id }),
  }), [onOpenPanel, turn.id])
  return (
    <section data-testid="deck-turn" data-turn-index={turn.index} className={`space-y-3 ${SCROLL_ANCHOR_CLASS}`}>
      {turn.omitted_items ? (
        <div data-testid="deck-omitted" className="text-center text-xs text-text-muted">{t('deck.omitted', { n: turn.omitted_items })}</div>
      ) : null}
      {turn.items.map((item) => <DeckItem key={item.id} item={item} actions={actions} />)}
      {turn.error && <div data-testid="deck-turn-error" className="text-xs text-status-error">{turn.error.message}</div>}
    </section>
  )
})

export function DeckView({ paneId, hostId, sessionId, entry, onSwitchToTerminal, footer, onOpenPanel }: DeckViewProps) {
  const t = useI18nStore((s) => s.t)
  const { doc } = entry
  const foldStore = usePaneFoldStore(`${paneId}\0${sessionId}`)
  useEffect(() => noteDeckPane(paneId), [paneId])
  // The place is the pane's AND the session's: a pane that moves to another session (/clear, a relay) starts at the end.
  const scroll = useTranscriptScroll(undefined, false, { paneId: `${paneId}\0${sessionId}`, view: 'deck' })
  const { attach, onScroll: onBoxScroll, follow } = scroll
  const boxRef = useRef<HTMLDivElement | null>(null)
  const setBox = useCallback((node: HTMLDivElement | null) => { boxRef.current = node; attach(node) }, [attach])

  // Placed from the pane's memory on the first run, then only a reader at the bottom is carried along as turns land.
  useEffect(() => { follow() }, [follow, doc.turns])

  // A page that does not fill the box cannot be scrolled, so no scroll event would ever ask for the next one: ask until the
  // box overflows or nothing older is left. A page that lands moves the first turn and starts over at once; one that fails
  // or brings nothing is asked for again after 1 s, then 2 s, and then left to the reader's own scroll to the top.
  const firstTurn = doc.turns[0]?.index ?? null
  const attempt = useRef<{ first: number | null; tries: number }>({ first: null, tries: 0 })
  useEffect(() => {
    const box = boxRef.current
    if (!box || !doc.hasMoreBefore || entry.paging) return
    if (box.scrollHeight > box.clientHeight + TOP_REACH) return
    if (attempt.current.first !== firstTurn) attempt.current = { first: firstTurn, tries: 0 }
    const { tries } = attempt.current
    if (tries >= AUTO_PAGE_TRIES) return
    const run = () => {
      // The live end may have grown the content past the box while the timer waited.
      if (box.scrollHeight > box.clientHeight + TOP_REACH) return
      attempt.current.tries += 1
      void useConversationStore.getState().loadBefore(hostId, sessionId)
    }
    if (tries === 0) { run(); return }
    const timer = setTimeout(run, AUTO_PAGE_RETRY_MS * 2 ** (tries - 1))
    return () => clearTimeout(timer)
    // Keyed by the FIRST turn, not the turns: a live update elsewhere must not restart a pending retry.
  }, [firstTurn, doc.hasMoreBefore, entry.paging, hostId, sessionId])

  const onScroll = (e: UIEvent<HTMLDivElement>) => {
    onBoxScroll(e)
    if (e.currentTarget.scrollTop < TOP_REACH && doc.hasMoreBefore && !entry.paging) {
      void useConversationStore.getState().loadBefore(hostId, sessionId)
    }
  }

  const items = useMemo(() => doc.turns.flatMap((turn) => turn.items), [doc.turns])
  const empty = doc.turns.every((turn) => turn.items.length === 0)

  return (
    <div className="flex h-full flex-col">
      {entry.status === 'reconnecting' && <div data-testid="deck-reconnecting" className="px-4 py-1 text-center text-xs text-text-muted">{t('deck.reconnecting')}</div>}
      {doc.detached && (
        <button type="button" data-testid="deck-back-live" onClick={() => void useConversationStore.getState().returnToLive(hostId, sessionId)}
          className="cursor-pointer bg-surface-secondary px-4 py-1 text-center text-xs text-text-secondary hover:text-text-primary">
          {t('deck.back_live')}
        </button>
      )}
      <div ref={setBox} onScroll={onScroll} data-testid="deck-scroll" className="flex-1 space-y-3 overflow-y-auto p-4">
        <FoldContext.Provider value={foldStore}>
          {entry.paging && <div data-testid="deck-paging" className="text-center text-xs text-text-muted">{t('deck.paging')}</div>}
          {empty ? (
            <div data-testid="deck-empty" className="flex h-full items-center justify-center text-sm text-text-muted">
              {entry.status === 'loading' ? t('deck.loading') : t('deck.empty')}
            </div>
          ) : (
            doc.turns.map((turn) => <TurnView key={turn.id} turn={turn} onOpenPanel={onOpenPanel} />)
          )}
        </FoldContext.Provider>
      </div>
      {footer?.(footerContext(paneId, hostId, sessionId, doc, items, onSwitchToTerminal))}
    </div>
  )
}
