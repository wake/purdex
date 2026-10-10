// spa/src/components/deck/DeckView.tsx — the deck (指揮台) of one conversation (U3 spec §4, plan D5): every turn's items in
// order, older turns read when the reader reaches the top, the reader's place and unfolded parts kept per pane across a
// tab switch (CLAUDE.md tab-hosted rule: `fold-memory` and the scroll memo are outside the component). The input and
// whatever else lives under the stream is the caller's `footer`, drawn below the scrolling box.
import { useEffect, useMemo, type ReactNode, type UIEvent } from 'react'
import { FoldContext } from '../room/fold-context'
import { useTranscriptScroll } from '../../hooks/useTranscriptScroll'
import { noteDeckPane, usePaneFoldStore } from '../../lib/conversations/fold-memory'
import { SCROLL_ANCHOR_CLASS } from '../../lib/nex/transcript-scroll-memory'
import type { Capabilities, ConversationItem, Turn } from '../../lib/conversations/types'
import { useConversationStore, type ConversationEntry } from '../../stores/useConversationStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { DeckItem } from './DeckItem'
import type { StepActions } from './StepViews'

/** What the footer needs to draw an input for this conversation. */
export interface DeckFooterContext {
  paneKey: string
  hostId: string
  sessionId: string
  capabilities: Capabilities | undefined
  items: readonly ConversationItem[]
  /** The header status is idle. */
  idle: boolean
  onSwitchToTerminal: () => void
}

export interface DeckViewProps {
  paneId: string
  hostId: string
  sessionId: string
  entry: ConversationEntry
  onSwitchToTerminal: () => void
  footer?: (ctx: DeckFooterContext) => ReactNode
  actions?: StepActions
}

/** Within this many pixels of the top the next older page is read. */
const TOP_REACH = 120

function TurnView({ turn, actions }: { turn: Turn; actions?: StepActions }) {
  const t = useI18nStore((s) => s.t)
  return (
    <section data-testid="deck-turn" data-turn-index={turn.index} className={`space-y-3 ${SCROLL_ANCHOR_CLASS}`}>
      {turn.omitted_items ? (
        <div data-testid="deck-omitted" className="text-center text-xs text-text-muted">{t('deck.omitted', { n: turn.omitted_items })}</div>
      ) : null}
      {turn.items.map((item) => <DeckItem key={item.id} item={item} actions={actions} />)}
      {turn.error && <div data-testid="deck-turn-error" className="text-xs text-status-error">{turn.error.message}</div>}
    </section>
  )
}

export function DeckView({ paneId, hostId, sessionId, entry, onSwitchToTerminal, footer, actions }: DeckViewProps) {
  const t = useI18nStore((s) => s.t)
  const { doc } = entry
  const foldStore = usePaneFoldStore(`${paneId}\0${sessionId}`)
  useEffect(() => noteDeckPane(paneId), [paneId])
  const scroll = useTranscriptScroll(undefined, false, { paneId, view: 'deck' })
  const { attach, onScroll: onBoxScroll, follow } = scroll

  // Placed from the pane's memory on the first run, then only a reader at the bottom is carried along as turns land.
  useEffect(() => { follow() }, [follow, doc.turns])

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
      <div ref={attach} onScroll={onScroll} data-testid="deck-scroll" className="flex-1 space-y-3 overflow-y-auto p-4">
        <FoldContext.Provider value={foldStore}>
          {entry.paging && <div data-testid="deck-paging" className="text-center text-xs text-text-muted">{t('deck.paging')}</div>}
          {empty ? (
            <div data-testid="deck-empty" className="flex h-full items-center justify-center text-sm text-text-muted">
              {entry.status === 'loading' ? t('deck.loading') : t('deck.empty')}
            </div>
          ) : (
            doc.turns.map((turn) => <TurnView key={turn.id} turn={turn} actions={actions} />)
          )}
        </FoldContext.Provider>
      </div>
      {footer?.({
        paneKey: paneId, hostId, sessionId,
        capabilities: doc.capabilities ?? undefined,
        items, idle: doc.header?.status === 'idle', onSwitchToTerminal,
      })}
    </div>
  )
}
