// spa/src/components/deck/ChatView.tsx — the chat (聊天, U3 spec §5, plan D9–D11): the same conversation as the deck with less
// detail. Bubbles, ONE row per chain of work (click → the right panel shows that chain), a file chip, peer messages as one
// line, a header whose second line is what the agent is doing now. Everything arrives as props; nothing here finds the deck,
// reads a store or switches to the terminal by itself. Open panel and scroll live in modules keyed by the pane (tab-hosted).
import { useEffect, useMemo, type ReactNode } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { buildChat, type ChatEntry } from '../../lib/conversations/chat-model'
import { usePaneFoldStore } from '../../lib/conversations/fold-memory'
import { openPanel } from '../../lib/conversations/panel-memory'
import type { PanelTurn } from '../../lib/conversations/panel-resolve'
import { SCROLL_ANCHOR_CLASS } from '../../lib/nex/transcript-scroll-memory'
import { useTranscriptScroll } from '../../hooks/useTranscriptScroll'
import { FoldContext } from '../room/fold-context'
import { AgentBubble, PeerLine, UserBubble } from './ChatBubbles'
import { ChatHeader } from './ChatHeader'
import { ChatWorkRow, FileChip } from './ChatWorkRow'
import { SessionRightPanel } from './SessionRightPanel'
import { SystemRow } from './ThinkingSystem'
import { UnreadableState, type UnreadableReason } from './UnreadableState'

export interface ChatViewProps {
  /** The pane's key: the panel, fold and scroll memories hang on it. */
  paneKey: string
  title: string
  /** Header status: running | waiting | idle | error | ended | unknown. */
  status: string
  turns: PanelTurn[]
  /** Why the conversation cannot be read (D11); absent when it can. An empty conversation says 「還沒有內容」 by itself. */
  unreadable?: UnreadableReason
  onRetry?: () => void
  onSwitchToTerminal: () => void
  /** The input below the stream (SessionInput), supplied by the caller. */
  input?: ReactNode
  /** False for a pane that is not in front (its Esc must not close another pane's panel). */
  active?: boolean
}

function Entry({ entry, paneKey }: { entry: ChatEntry; paneKey: string }) {
  switch (entry.kind) {
    case 'user': return <UserBubble item={entry.item} />
    case 'peer': return <PeerLine items={entry.items} />
    case 'agent': return <AgentBubble item={entry.item} />
    case 'system': return <SystemRow item={entry.item} />
    case 'work':
      return <ChatWorkRow run={entry.run} onOpen={() => openPanel(paneKey, { kind: 'chain', turnId: entry.turnId, firstStepId: entry.run.stepIds[0] })} />
    case 'files':
      return <FileChip files={entry.files} added={entry.added} removed={entry.removed} onOpen={() => openPanel(paneKey, { kind: 'chain', turnId: entry.turnId, firstStepId: entry.firstStepId })} />
  }
}

export function ChatView({ paneKey, title, status, turns, unreadable, onRetry, onSwitchToTerminal, input, active = true }: ChatViewProps) {
  const t = useI18nStore((s) => s.t)
  const fold = usePaneFoldStore(paneKey)
  const entries = useMemo(() => buildChat(turns), [turns])
  const hasItems = turns.some((x) => x.items.length > 0)
  const reason: UnreadableReason | null = unreadable ?? (hasItems ? null : 'empty')
  const progress = [...entries].reverse().find((e) => e.kind === 'work' && e.run.running)
  const latest = progress?.kind === 'work' ? progress.run.latest : undefined

  // Placed from the pane's memory on the first run (a remount), then follows growth only for a reader at the bottom.
  const scroll = useTranscriptScroll(undefined, false, { paneId: `${paneKey}:chat`, view: 'chat' })
  const { attach, onScroll, follow } = scroll
  useEffect(() => { follow() }, [follow, entries])

  return (
    <FoldContext.Provider value={fold}>
      <div data-testid="chat-view" className="flex h-full min-h-0">
        <div className="flex min-w-0 flex-1 flex-col">
          <ChatHeader title={title} status={status} latest={latest} />
          {reason ? (
            <div className="min-h-0 flex-1"><UnreadableState reason={reason} onRetry={onRetry} onSwitchToTerminal={onSwitchToTerminal} /></div>
          ) : (
            <div ref={attach} onScroll={onScroll} data-testid="chat-scroll" aria-label={t('chat.transcript')} className="min-h-0 flex-1 space-y-3 overflow-y-auto p-4">
              {groupByTurn(entries).map((g) => (
                <div key={g.turnIndex} data-turn-index={g.turnIndex} className={`${SCROLL_ANCHOR_CLASS} space-y-3`}>
                  {g.entries.map((e) => <Entry key={e.key} entry={e} paneKey={paneKey} />)}
                </div>
              ))}
            </div>
          )}
          {input}
        </div>
        {!reason && <SessionRightPanel paneKey={paneKey} turns={turns} active={active} />}
      </div>
    </FoldContext.Provider>
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
