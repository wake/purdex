// spa/src/components/deck/ChatView.tsx — the chat (聊天, U3 spec §5, plan D9–D11): the same conversation as the deck with less
// detail. Bubbles, ONE row per chain of work (click → the right panel shows that chain), a file chip, peer messages as one
// line, a header whose second line is what the agent is doing now. Everything arrives as props; nothing here finds the deck,
// reads a store or switches to the terminal by itself. Open panel and scroll live in modules keyed by the pane (tab-hosted).
import { useCallback, useEffect, useMemo, useState, type ReactNode, type UIEvent } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { buildChat, type ChatEntry } from '../../lib/conversations/chat-model'
import { usePaneFoldStore } from '../../lib/conversations/fold-memory'
import { chatScrollKey, conversationBinding, openPanel } from '../../lib/conversations/panel-memory'
import type { PanelTurn } from '../../lib/conversations/panel-resolve'
import { SCROLL_ANCHOR_CLASS } from '../../lib/nex/transcript-scroll-memory'
import { useTranscriptScroll } from '../../hooks/useTranscriptScroll'
import { useOlderTurns } from '../../hooks/useOlderTurns'
import { FoldContext } from '../room/fold-context'
import { AgentBubble, UserBubble } from './ChatBubbles'
import { ConversationHostContext } from './conversation-host'
import { PeerLine } from './ChatPeerLine'
import { ChatHeader } from './ChatHeader'
import { FindBar } from './FindBar'
import { focusSessionInput } from './focus-input'
import { closeFind, findKey, useFindState } from '../../lib/conversations/find-memory'
import { chatUnits } from '../../lib/conversations/find-units'
import type { SearchUnit } from '../../lib/nex/transcript-search'
import { ChatWorkRow, FileChip } from './ChatWorkRow'
import { SessionPanelSplit } from './SessionPanelSplit'
import { SystemRow } from './ThinkingSystem'
import { StatusOverlay } from './StatusOverlay'
import { DoneTurnLine, LiveTurnLine } from './TurnLines'
import { liveLine } from '../../lib/conversations/turn-line'
import { PendingTail } from './PendingTail'
import { queuedSignature, shownHeaderQueue, usePendingEntries } from '../../hooks/usePending'
import type { HeaderBackground, HeaderQueued } from '../../lib/conversations/types'
import { UnreadableState, type UnreadableReason } from './UnreadableState'

export interface ChatViewProps {
  /** The pane's key: the panel, fold and scroll memories hang on it. */
  paneKey: string
  /** Which conversation this is (the panel and the scroll memory belong to it; /clear, relay and rebuild change `sessionId`). */
  hostId: string
  sessionId: string | null
  title: string
  /** Header status: running | waiting | idle | error | ended | unknown. */
  status: string
  /** Claude Code's own queue from the header (B-read); only drawn while the status is running / waiting. */
  queued?: readonly HeaderQueued[]
  /** The background work Claude Code still runs, from the header: said on the live line and on the last turn's closing line. */
  background?: HeaderBackground
  turns: PanelTurn[]
  /** Why the conversation cannot be read (D11); absent when it can. An empty conversation says 「還沒有內容」 by itself. */
  unreadable?: UnreadableReason
  onRetry?: () => void
  /** Absent for a worker (no terminal view). */
  onSwitchToTerminal?: () => void
  /** Older turns exist on the daemon (absent: the caller does not page). */
  hasMoreBefore?: boolean
  /** An older page is being read. */
  paging?: boolean
  /** Read the next older page (the caller owns the store). */
  onLoadOlder?: () => void
  /** Changes when `turns` is another window of the conversation (a jump, a replacing snapshot), not older turns added. */
  generation?: number
  /** The input below the stream (SessionInput), supplied by the caller. */
  input?: ReactNode
  /** False for a pane that is not in front (REQUIRED: Esc closes only the focused pane's panel). */
  active: boolean
}

function Entry({ entry, paneKey, binding }: { entry: ChatEntry; paneKey: string; binding: string }) {
  switch (entry.kind) {
    case 'user': return <UserBubble item={entry.item} midTurn={entry.midTurn} />
    case 'peer': return <PeerLine items={entry.items} />
    case 'agent': return <AgentBubble item={entry.item} />
    case 'system': return <SystemRow item={entry.item} />
    case 'work':
      return <ChatWorkRow run={entry.run} onOpen={() => openPanel(paneKey, binding, { kind: 'chain', turnId: entry.turnId, firstStepId: entry.run.stepIds[0] })} />
    case 'files':
      return <FileChip files={entry.files} added={entry.added} removed={entry.removed} onOpen={() => openPanel(paneKey, binding, { kind: 'chain', turnId: entry.turnId, firstStepId: entry.firstStepId })} />
  }
}

/**
 * Re-keyed by the conversation: a pane whose session changes (/clear, relay, rebuild) mounts a fresh chat that reads no
 * scroll, fold or panel state of the old one (all three memories are keyed by pane AND binding).
 */
export function ChatView(props: ChatViewProps) {
  // No session id (provenance cleared it while /clear, relay or rebuild is under way) is unreadable whatever turns are still
  // held (plan D3): nothing of the old transcript is drawn and no memory is read or written under an empty binding.
  if (!props.sessionId) {
    return (
      <div data-testid="chat-view" className="flex h-full min-h-0 flex-col">
        <ChatHeader title={props.title} status={props.status} />
        <div className="min-h-0 flex-1"><UnreadableState reason="no_session" onSwitchToTerminal={props.onSwitchToTerminal} /></div>
      </div>
    )
  }
  const binding = conversationBinding(props.hostId, props.sessionId)
  return <ChatViewBody key={binding} binding={binding} {...props} />
}

const NO_UNITS: SearchUnit[] = []

function ChatViewBody({ binding, paneKey, hostId, sessionId, title, status, queued, background, turns, unreadable, onRetry, onSwitchToTerminal, input, active, hasMoreBefore, paging, onLoadOlder, generation }: ChatViewProps & { binding: string }) {
  const t = useI18nStore((s) => s.t)
  const fold = usePaneFoldStore(`${paneKey}\0${binding}`)
  const entries = useMemo(() => buildChat(turns), [turns])
  const hasItems = turns.some((x) => x.items.length > 0)
  const reason: UnreadableReason | null = unreadable ?? (hasItems ? null : 'empty')
  const progress = [...entries].reverse().find((e) => e.kind === 'work' && e.run.running)
  const latest = progress?.kind === 'work' ? progress.run.latest : undefined

  // Placed from the pane's memory on the first run (a remount), then follows growth only for a reader at the bottom.
  const findMemory = findKey(paneKey, sessionId ?? '')
  const find = useFindState(findMemory)
  const scroll = useTranscriptScroll(undefined, find.open, { paneId: chatScrollKey(paneKey, binding), view: 'chat' })
  const { attach, onScroll: onBoxScroll, follow, shiftBy } = scroll
  const liveOn = liveLine(turns, status) !== null
  // What waits to be read ends the stream: it coming and going moves the end like the live line does, so a reader at the bottom follows it.
  const pending = usePendingEntries(paneKey, hostId, sessionId ?? '')
  const headerQueued = shownHeaderQueue(queued, status)
  const queuedSig = queuedSignature(headerQueued)
  useEffect(() => { follow() }, [follow, entries, liveOn, pending, queuedSig])
  const [boxEl, setBoxEl] = useState<HTMLDivElement | null>(null)
  const setBox = useCallback((node: HTMLDivElement | null) => { setBoxEl(node); attach(node) }, [attach])
  // Older pages as the reader goes up (the deck's rule, useOlderTurns); the place is kept when one lands above.
  const load = useCallback(() => onLoadOlder?.(), [onLoadOlder])
  const older = useOlderTurns({ box: boxEl, hasMore: !!hasMoreBefore && !!onLoadOlder, paging: !!paging, firstTurn: turns[0]?.index ?? null, generation: generation ?? 0, placed: scroll.placed, load, shiftBy })
  const onScroll = (e: UIEvent<HTMLDivElement>) => { onBoxScroll(e); older.onScroll() }
  // The search index exists only while the bar is open (#2665).
  const findUnits = useMemo<SearchUnit[]>(() => (find.open ? chatUnits(turns) : NO_UNITS), [find.open, turns])
  const closeBar = useCallback(() => {
    closeFind(findMemory)
    focusSessionInput(boxEl?.closest<HTMLElement>('[data-testid="session-view-chat"]') ?? null, { request: true })
  }, [findMemory, boxEl])

  return (
    <ConversationHostContext.Provider value={hostId}>
    <FoldContext.Provider value={fold}>
      <div data-testid="chat-view" className="h-full min-h-0">
        {reason ? (
          // Nothing to read, so nothing a panel could show: no split, whatever the panel memory holds.
          <div className="flex h-full min-h-0 flex-col">
            <ChatHeader title={title} status={status} latest={latest} />
            <div className="min-h-0 flex-1"><UnreadableState reason={reason} onRetry={onRetry} onSwitchToTerminal={onSwitchToTerminal} /></div>
            {input}
          </div>
        ) : (
          <SessionPanelSplit paneKey={paneKey} binding={binding} turns={turns} active={active}>
            <ChatHeader title={title} status={status} latest={latest} />
            {find.open && <FindBar memoryKey={findMemory} owner={paneKey} container={boxEl} units={findUnits} fold={fold} onClose={closeBar} onJump={scroll.release} />}
            <div ref={setBox} onScroll={onScroll} data-testid="chat-scroll" aria-label={t('chat.transcript')} style={{ overflowAnchor: 'none' }} className="min-h-0 flex-1 space-y-3 overflow-y-auto p-4">
              <StatusOverlay>
                {paging && <div data-testid="chat-paging">{t('deck.paging')}</div>}
              </StatusOverlay>
              {/* In the flow, on top of the content: it scrolls away with it, and arrives in the commit of the last page. */}
              {hasMoreBefore === false && <div data-testid="chat-history-start" className="deck-t-down text-text-muted">{t('deck.history_start')}</div>}
              {groupByTurn(entries).map((g) => (
                <div key={g.turnIndex} data-turn-index={g.turnIndex} className={`${SCROLL_ANCHOR_CLASS} space-y-3`}>
                  {g.entries.map((e) => <Entry key={e.key} entry={e} paneKey={paneKey} binding={binding} />)}
                  {(() => { const tn = turns.find((x) => x.index === g.turnIndex); return tn ? <DoneTurnLine turn={tn} background={tn === turns[turns.length - 1] ? background : undefined} /> : null })()}
                </div>
              ))}
              <LiveTurnLine turns={turns} status={status} background={background} />
              <PendingTail paneKey={paneKey} hostId={hostId} sessionId={sessionId ?? ''} entries={pending} queued={headerQueued} />
            </div>
            {input}
          </SessionPanelSplit>
        )}
      </div>
    </FoldContext.Provider>
    </ConversationHostContext.Provider>
  )
}

/** Entries grouped by their turn (a merged peer line sits in the turn it started in), in order. */
function groupByTurn(entries: ChatEntry[]): Array<{ turnIndex: number; entries: ChatEntry[] }> {
  const out: Array<{ turnIndex: number; entries: ChatEntry[] }> = []
  for (const e of entries) {
    const last = out[out.length - 1]
    if (last && last.turnIndex === e.turnIndex) last.entries.push(e)
    else out.push({ turnIndex: e.turnIndex, entries: [e] })
  }
  return out
}
