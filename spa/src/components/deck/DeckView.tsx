// spa/src/components/deck/DeckView.tsx — the deck (指揮台) of one conversation (U3 spec §4, plan D5): every turn's items in
// order, older turns read when the reader reaches the top, the reader's place and unfolded parts kept per pane across a
// tab switch (CLAUDE.md tab-hosted rule: `fold-memory` and the scroll memo are outside the component). The input and
// whatever else lives under the stream is the caller's `footer`, drawn below the scrolling box.
import { memo, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode, type UIEvent } from 'react'
import { FoldContext, useFoldStore } from '../room/fold-context'
import { ConversationHostContext } from './conversation-host'
import { useTranscriptScroll } from '../../hooks/useTranscriptScroll'
import { useOlderTurns } from '../../hooks/useOlderTurns'
import { useKeepPlaceOnTypeChange } from '../../hooks/useKeepPlaceOnTypeChange'
import { noteDeckPane, usePaneFoldStore } from '../../lib/conversations/fold-memory'
import { isRevealed, markRevealed } from '../../lib/conversations/deck-reveal-memory'
import { SCROLL_ANCHOR_CLASS } from '../../lib/nex/transcript-scroll-memory'
import type { HeaderBackground, Turn } from '../../lib/conversations/types'
import { useConversationStore, type ConversationEntry } from '../../stores/useConversationStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { StatusOverlay } from './StatusOverlay'
import { DeckItem } from './DeckItem'
import { FindBar } from './FindBar'
import { focusSessionInput } from './focus-input'
import { closeFind, findKey, useFindState } from '../../lib/conversations/find-memory'
import { deckUnits, fullTurnKey } from '../../lib/conversations/find-units'
import type { SearchUnit } from '../../lib/nex/transcript-search'
import { firstUserId } from '../../lib/conversations/deck-format'
import { DoneTurnLine, LiveTurnLine } from './TurnLines'
import { PendingTail } from './PendingTail'
import { queuedSignature, shownHeaderQueue, usePendingEntries } from '../../hooks/usePending'
import { liveLine } from '../../lib/conversations/turn-line'
import { DeckRevealContext, EAGER_TURNS, useDeckReveal } from './deck-reveal'
import { speechStartIds } from '../../lib/conversations/deck-speech'
import type { Seat } from '../../lib/names/seat'
import { footerContext, type DeckFooterContext } from './footer-context'
import type { StepActions } from './StepViews'
import type { PanelContent } from '../../lib/conversations/panel-memory'

export type { DeckFooterContext }

export interface DeckViewProps {
  paneId: string
  hostId: string
  sessionId: string
  entry: ConversationEntry
  /** Absent for a worker (no terminal view). */
  onSwitchToTerminal?: () => void
  footer?: (ctx: DeckFooterContext) => ReactNode
  /** 「顯示全部」 and a subagent line open the pane's right panel on that step (the turn is known here, not to the step). */
  onOpenPanel?: (content: PanelContent) => void
  /**
   * What the backend's own events say runs now, when they speak for the live line (a worker's Nexen events, U4-4a): the running tool (null: none)
   * and the background work. They replace what the transcript's steps and the header say.
   */
  live?: { tool: string | null; background?: HeaderBackground; /** The back-end says nothing runs any more (a worker that ended): no live line, whatever the header still says. */ hidden?: boolean }
  /** Drawn at the very end of the stream, after the live line and what is waiting to be read: a back-end's own system rows (a worker's lifecycle, U4-4a). */
  tail?: ReactNode
  /** The persona behind the agent of this conversation (name pool v2 spec §9.4), or null/absent: today's header, no extra lines. */
  seat?: Seat | null
}

// Memoized: the store keeps an unchanged turn's object, so a live update re-draws only the turn that changed.
// `eager`: one of the newest turns, always drawn in full. An older one draws its agent text as plain text until it comes near the
// viewport (#2469), then in full for good — the swap is remembered outside the component (`deck-reveal-memory`), so a deck that
// remounts with its tab does not start the far-off turns over.
const TurnView = memo(function TurnView({ turn, eager, onOpenPanel, agentModel, subagents, loadSubagent, background, seat }: { turn: Turn; seat?: Seat | null; eager: boolean; /** the header's background work, for the last turn only */ background?: HeaderBackground; onOpenPanel?: (content: PanelContent) => void; agentModel?: string; subagents?: ConversationEntry['subagents']; loadSubagent?: (agentId: string) => void }) {
  const t = useI18nStore((s) => s.t)
  const reveal = useContext(DeckRevealContext)
  const [near, setNear] = useState(false)
  // a search that stands on this turn's prose asks for the real markdown (the unit is its text, not the plain source)
  const found = useFoldStore().isExpanded(fullTurnKey(turn.id))
  const full = eager || near || found || !reveal || isRevealed(reveal.memKey, turn.id)
  const ref = useRef<HTMLElement | null>(null)
  useEffect(() => {
    if (eager && reveal) markRevealed(reveal.memKey, turn.id)
  }, [eager, reveal, turn.id])
  // A turn without agent text looks the same either way: nothing to watch.
  const hasProse = turn.items.some((item) => item.type === 'agent_text')
  const firstAgentId = turn.items.find((item) => item.type === 'agent_text')?.id
  const firstUser = firstUserId(turn.items, turn.omitted_items)
  // Where the agent starts speaking again after a tool or a thought (spec §9.4): only a persona's conversation draws a line for it.
  const speechStarts = useMemo(() => (seat ? speechStartIds(turn.items, { afterOmitted: !!turn.omitted_items }) : null), [seat, turn.items, turn.omitted_items])
  // The turn's head line: today's rule (the first agent text in view) for a conversation that is no persona; a persona's goes to the first speech start that is CERTAIN (a turn that left its first items out may
  // open mid-run, and a head on that continuation would claim a start it does not know).
  const headerId = speechStarts ? turn.items.find((item) => speechStarts.has(item.id))?.id : firstAgentId
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
        <div data-testid="deck-omitted" className="deck-t-down text-text-muted">{t('deck.omitted', { n: turn.omitted_items })}</div>
      ) : null}
      {turn.items.map((item) => <DeckItem key={item.id} item={item} actions={actions} light={!full} agentModel={agentModel} agentHeader={item.id === headerId} seat={seat} speechStart={speechStarts?.has(item.id) ?? false} midTurn={item.type === 'user' && item.id !== firstUser} />)}
      <DoneTurnLine turn={turn} background={background} />
      {turn.error && <div data-testid="deck-turn-error" className="deck-t-down text-status-error">{turn.error.message}</div>}
    </section>
  )
})

const NO_UNITS: SearchUnit[] = []

export function DeckView({ paneId, hostId, sessionId, entry, onSwitchToTerminal, footer, onOpenPanel, live, tail, seat }: DeckViewProps) {
  const background = live ? live.background : entry.doc.header?.background
  const t = useI18nStore((s) => s.t)
  const { doc } = entry
  const foldStore = usePaneFoldStore(`${paneId}\0${sessionId}`)
  useEffect(() => noteDeckPane(paneId), [paneId])
  // The place is the pane's AND the session's: a pane that moves to another session (/clear, a relay) starts at the end.
  const findMemory = findKey(paneId, sessionId)
  const find = useFindState(findMemory)
  const scroll = useTranscriptScroll(undefined, find.open, { paneId: `${paneId}\0${sessionId}`, view: 'deck', detached: doc.detached, totalTurns: doc.totalTurns })
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
  // What waits to be read is the last thing in the stream: it coming and going moves the end like the live line does, so a reader at the bottom follows it.
  const pending = usePendingEntries(paneId, hostId, sessionId)
  const headerQueued = shownHeaderQueue(doc.header?.queued, doc.header?.status)
  const queuedSig = queuedSignature(headerQueued)
  useEffect(() => { if (!empty) follow() }, [follow, doc.turns, empty, liveOn, pending, queuedSig])

  // Older pages (see useOlderTurns): asked for at the top, again when a page landed and the reader is still there, with a back-off
  // when one fails; the place kept across the page landing above.
  const firstTurn = doc.turns[0]?.index ?? null
  const load = useCallback(() => { void useConversationStore.getState().loadBefore(hostId, sessionId) }, [hostId, sessionId])
  const older = useOlderTurns({ box: boxEl, hasMore: doc.hasMoreBefore, paging: entry.paging, firstTurn, generation: doc.generation, placed: scroll.placed, load, shiftBy: scroll.shiftBy })

  useKeepPlaceOnTypeChange(boxEl, scroll.shiftBy)

  const onScroll = (e: UIEvent<HTMLDivElement>) => {
    onBoxScroll(e)
    older.onScroll()
  }

  const items = useMemo(() => doc.turns.flatMap((turn) => turn.items), [doc.turns])
  // The search index exists only while the bar is open (#2665).
  const findUnits = useMemo<SearchUnit[]>(() => (find.open ? deckUnits(doc.turns, EAGER_TURNS, entry.subagents) : NO_UNITS), [find.open, doc.turns, entry.subagents])
  const closeBar = useCallback(() => {
    closeFind(findMemory)
    focusSessionInput(boxEl?.closest<HTMLElement>('[data-testid="session-view-deck"]') ?? null, { request: true })
  }, [findMemory, boxEl])
  const loadSubagent = useCallback((agentId: string) => { void useConversationStore.getState().loadSubagent(hostId, sessionId, agentId) }, [hostId, sessionId])

  return (
    <div className="flex h-full flex-col">
      {entry.status === 'reconnecting' && <div data-testid="deck-reconnecting" className="px-4 py-1 deck-t-down text-text-muted">{t('deck.reconnecting')}</div>}
      {doc.detached && (
        <button type="button" data-testid="deck-back-live" onClick={() => void useConversationStore.getState().returnToLive(hostId, sessionId)}
          className="cursor-pointer bg-surface-secondary px-4 py-1 text-left deck-t-down text-text-secondary hover:text-text-primary">
          {t('deck.back_live')}
        </button>
      )}
      {find.open && !empty && <FindBar memoryKey={findMemory} owner={paneId} container={boxEl} units={findUnits} fold={foldStore} onClose={closeBar} onJump={scroll.release} />}
      <div ref={setBox} onScroll={onScroll} data-testid="deck-scroll" style={{ overflowAnchor: 'none' }} className="flex-1 space-y-3 overflow-y-auto p-4">
        <ConversationHostContext.Provider value={hostId}>
        <FoldContext.Provider value={foldStore}>
          <DeckRevealContext.Provider value={reveal}>
            <StatusOverlay>
              {entry.paging && <div data-testid="deck-paging">{t('deck.paging')}</div>}
            </StatusOverlay>
            {/* In the flow, on top of the content: it scrolls away with it, and arrives in the commit of the last page (the place is kept for it). */}
            {!empty && !doc.hasMoreBefore && <div data-testid="deck-history-start" className="deck-t-down text-text-muted">{t('deck.history_start')}</div>}
            {empty ? (
              // a first message the App still holds is something to read: no "nothing here" block beside it
              pending.some((e) => e.state !== 'superseded') || headerQueued.length > 0 ? null : <div data-testid="deck-empty" className="flex h-full items-center justify-center deck-t-up text-text-muted">
                {entry.status === 'loading' ? t('deck.loading') : t('deck.empty')}
              </div>
            ) : (
              doc.turns.map((turn, i) => <TurnView key={turn.id} turn={turn} eager={i >= doc.turns.length - EAGER_TURNS} onOpenPanel={onOpenPanel} agentModel={doc.header?.usage?.model} subagents={entry.subagents} loadSubagent={loadSubagent} background={i === doc.turns.length - 1 ? background : undefined} seat={seat} />)
            )}
            {!empty && !live?.hidden && <LiveTurnLine turns={doc.turns} status={doc.header?.status} background={background} tool={live?.tool} />}
            <PendingTail paneKey={paneId} hostId={hostId} sessionId={sessionId} entries={pending} queued={headerQueued} />
            {tail}
          </DeckRevealContext.Provider>
        </FoldContext.Provider>
        </ConversationHostContext.Provider>
      </div>
      {footer?.(footerContext(paneId, hostId, sessionId, doc, items, onSwitchToTerminal))}
    </div>
  )
}
