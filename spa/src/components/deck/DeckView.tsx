// spa/src/components/deck/DeckView.tsx — the deck (指揮台) of one conversation (U3 spec §4, plan D5): every turn's items in
// order, older turns read when the reader reaches the top, the reader's place and unfolded parts kept per pane across a
// tab switch (CLAUDE.md tab-hosted rule: `fold-memory` and the scroll memo are outside the component). The input and
// whatever else lives under the stream is the caller's `footer`, drawn below the scrolling box.
import { memo, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode, type UIEvent } from 'react'
import { FoldContext } from '../room/fold-context'
import { useTranscriptScroll } from '../../hooks/useTranscriptScroll'
import { useOlderTurns } from '../../hooks/useOlderTurns'
import { noteDeckPane, usePaneFoldStore } from '../../lib/conversations/fold-memory'
import { isRevealed, markRevealed } from '../../lib/conversations/deck-reveal-memory'
import { SCROLL_ANCHOR_CLASS } from '../../lib/nex/transcript-scroll-memory'
import type { Turn } from '../../lib/conversations/types'
import { useConversationStore, type ConversationEntry } from '../../stores/useConversationStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { StatusOverlay } from './StatusOverlay'
import { DeckItem } from './DeckItem'
import { DoneTurnLine, LiveTurnLine } from './TurnLines'
import { liveLine } from '../../lib/conversations/turn-line'
import { DeckRevealContext, EAGER_TURNS, useDeckReveal } from './deck-reveal'
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

// Memoized: the store keeps an unchanged turn's object, so a live update re-draws only the turn that changed.
// `eager`: one of the newest turns, always drawn in full. An older one draws its agent text as plain text until it comes near the
// viewport (#2469), then in full for good — the swap is remembered outside the component (`deck-reveal-memory`), so a deck that
// remounts with its tab does not start the far-off turns over.
const TurnView = memo(function TurnView({ turn, eager, onOpenPanel, agentModel, subagents, loadSubagent }: { turn: Turn; eager: boolean; onOpenPanel?: (content: PanelContent) => void; agentModel?: string; subagents?: ConversationEntry['subagents']; loadSubagent?: (agentId: string) => void }) {
  const t = useI18nStore((s) => s.t)
  const reveal = useContext(DeckRevealContext)
  const [near, setNear] = useState(false)
  const full = eager || near || !reveal || isRevealed(reveal.memKey, turn.id)
  const ref = useRef<HTMLElement | null>(null)
  useEffect(() => {
    if (eager && reveal) markRevealed(reveal.memKey, turn.id)
  }, [eager, reveal, turn.id])
  // A turn without agent text looks the same either way: nothing to watch.
  const hasProse = turn.items.some((item) => item.type === 'agent_text')
  const firstAgentId = turn.items.find((item) => item.type === 'agent_text')?.id
  useEffect(() => {
    const el = ref.current
    if (full || !reveal || !el || !hasProse) return
    return reveal.observe(el, () => { markRevealed(reveal.memKey, turn.id); setNear(true) })
  }, [full, reveal, turn.id, hasProse])
  const actions = useMemo<StepActions | undefined>(() => onOpenPanel && ({
    onShowAll: (s) => onOpenPanel({ kind: 'output', turnId: turn.id, stepId: s.id }),
    onOpenSubagent: (s) => onOpenPanel({ kind: 'subagent', turnId: turn.id, stepId: s.id }),
    subagents,
    loadSubagent,
    // a subagent's own steps, drawn the deck's way one level in (no nested unfolding: they carry no `actions`)
    renderSubagent: (items) => items.map((it) => <DeckItem key={it.id} item={it} />),
  }), [onOpenPanel, turn.id, subagents, loadSubagent])
  return (
    <section ref={ref} data-testid="deck-turn" data-turn-index={turn.index} className={`space-y-3 ${SCROLL_ANCHOR_CLASS}`}>
      {turn.omitted_items ? (
        <div data-testid="deck-omitted" className="text-xs text-text-muted">{t('deck.omitted', { n: turn.omitted_items })}</div>
      ) : null}
      {turn.items.map((item) => <DeckItem key={item.id} item={item} actions={actions} light={!full} agentModel={agentModel} agentHeader={item.id === firstAgentId} />)}
      <DoneTurnLine turn={turn} />
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
  const scroll = useTranscriptScroll(undefined, false, { paneId: `${paneId}\0${sessionId}`, view: 'deck', detached: doc.detached, totalTurns: doc.totalTurns })
  const { attach, onScroll: onBoxScroll, follow } = scroll
  const boxRef = useRef<HTMLDivElement | null>(null)
  // The box is also held in state: the older-turns hook reads it while rendering.
  const [boxEl, setBoxEl] = useState<HTMLDivElement | null>(null)
  const setBox = useCallback((node: HTMLDivElement | null) => { boxRef.current = node; setBoxEl(node); attach(node) }, [attach])
  const reveal = useDeckReveal(`${paneId}\0${sessionId}`, boxRef)

  // Placed from the pane's memory on the first run, then only a reader at the bottom is carried along as turns land. Not while
  // there is nothing in the box (a conversation re-opened and still loading): that first run would read the memory against
  // an empty box and spend the placement, and the turns arriving next would slide down from the top.
  const empty = doc.turns.every((turn) => turn.items.length === 0)
  // The live line appearing or going is a change of the end of the stream: a reader at the bottom stays there (no jump).
  const liveOn = liveLine(doc.turns, doc.header?.status) !== null
  useEffect(() => { if (!empty) follow() }, [follow, doc.turns, empty, liveOn])

  // Older pages (see useOlderTurns): asked for at the top, again when a page landed and the reader is still there, with a back-off
  // when one fails; the place kept across the page landing above.
  const firstTurn = doc.turns[0]?.index ?? null
  const load = useCallback(() => { void useConversationStore.getState().loadBefore(hostId, sessionId) }, [hostId, sessionId])
  const older = useOlderTurns({ box: boxEl, hasMore: doc.hasMoreBefore, paging: entry.paging, firstTurn, generation: doc.generation, placed: scroll.placed, load, shiftBy: scroll.shiftBy })

  const onScroll = (e: UIEvent<HTMLDivElement>) => {
    onBoxScroll(e)
    older.onScroll()
  }

  const items = useMemo(() => doc.turns.flatMap((turn) => turn.items), [doc.turns])
  const loadSubagent = useCallback((agentId: string) => { void useConversationStore.getState().loadSubagent(hostId, sessionId, agentId) }, [hostId, sessionId])

  return (
    <div className="flex h-full flex-col">
      {entry.status === 'reconnecting' && <div data-testid="deck-reconnecting" className="px-4 py-1 text-xs text-text-muted">{t('deck.reconnecting')}</div>}
      {doc.detached && (
        <button type="button" data-testid="deck-back-live" onClick={() => void useConversationStore.getState().returnToLive(hostId, sessionId)}
          className="cursor-pointer bg-surface-secondary px-4 py-1 text-left text-xs text-text-secondary hover:text-text-primary">
          {t('deck.back_live')}
        </button>
      )}
      <div ref={setBox} onScroll={onScroll} data-testid="deck-scroll" style={{ overflowAnchor: 'none' }} className="flex-1 space-y-3 overflow-y-auto p-4">
        <FoldContext.Provider value={foldStore}>
          <DeckRevealContext.Provider value={reveal}>
            <StatusOverlay>
              {entry.paging && <div data-testid="deck-paging">{t('deck.paging')}</div>}
              {!empty && !doc.hasMoreBefore && !entry.paging && <div data-testid="deck-history-start">{t('deck.history_start')}</div>}
            </StatusOverlay>
            {empty ? (
              <div data-testid="deck-empty" className="flex h-full items-center justify-center text-sm text-text-muted">
                {entry.status === 'loading' ? t('deck.loading') : t('deck.empty')}
              </div>
            ) : (
              doc.turns.map((turn, i) => <TurnView key={turn.id} turn={turn} eager={i >= doc.turns.length - EAGER_TURNS} onOpenPanel={onOpenPanel} agentModel={doc.header?.usage?.model} subagents={entry.subagents} loadSubagent={loadSubagent} />)
            )}
            {!empty && <LiveTurnLine turns={doc.turns} status={doc.header?.status} />}
          </DeckRevealContext.Provider>
        </FoldContext.Provider>
      </div>
      {footer?.(footerContext(paneId, hostId, sessionId, doc, items, onSwitchToTerminal))}
    </div>
  )
}
