// spa/src/components/chat/ChatTranscript.tsx — the chat transcript (spec §5,
// R2 plan T1.3). The same state as the room, drawn with minimum ceremony for a
// phone: the agent's prose in bubbles on the left, your lines in bubbles on
// the right, and nothing else.
//
// - **No thinking**, durable or streaming. While a thought streams the pane
//   keeps its dots on (ExecutionView's chat rule, partialHasVisibleText).
// - **No tool operations yet**: R2-B adds chat's tool lines (T2.1). Until then
//   a tool_use draws nothing and a tool_result never becomes a bubble.
// - **Turns** are still RoomTurnGroups — without their fold strip — so fold
//   memory and the turn index keep working when R2-B adds foldable lines.
// - The root is an `@container`: a narrow pane on a desktop behaves like a
//   phone (the bubble cap is a share of the pane below `@md`).
import { Children, isValidElement, useRef, useEffect, useMemo, type ReactNode } from 'react'
import { Prohibit } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import type { AssistantMessage, StreamMessage, UserMessage } from '../../lib/nex/message-types'
import { partialVersionOf } from '../../lib/nex/partial'
import { indexOperations } from '../../lib/nex/operations'
import { groupTurns, INTERRUPT_TEXT, type RoomTurn } from '../../lib/nex/turns'
import ThinkingIndicator from '../ThinkingIndicator'
import RoomTurnGroup from '../room/RoomTurnGroup'
import RoomProse from '../room/RoomProse'
import { FoldContext, useFoldMemory } from '../room/fold-context'
import type { RoomTranscriptProps } from '../room/RoomTranscript'
import ChatBubble from './ChatBubble'
import ChatPartialGroup from './ChatPartialGroup'

/**
 * RoomTranscript's props, so the pane hands either view the same state.
 * `showThinking` is the ThinkingIndicator (the dots), not thinking blocks —
 * those chat never draws; the caller computes it with chat's rule.
 * `tools` / `now` are accepted and unused until R2-B's tool lines.
 */
export type ChatTranscriptProps = RoomTranscriptProps

const NO_STARTS: readonly number[] = []

/** `false` / `null` from a `{cond && <x/>}` slot is not a line to hold (as RoomTranscript). */
function hasContent(node: ReactNode): boolean {
  return Children.toArray(node).some((c) => isValidElement(c) || typeof c === 'string' || typeof c === 'number')
}

/** One durable top-level message as chat bubbles; null when it has nothing chat draws. */
function ChatMessage({ msg, interrupted }: { msg: StreamMessage; interrupted: string }) {
  const rows: ReactNode[] = []

  if (msg.type === 'assistant' && 'message' in msg) {
    ;(msg as AssistantMessage).message.content.forEach((block, j) => {
      // Thinking and tool_use draw nothing in chat (tools until R2-B).
      if (block.type === 'text' && block.text?.trim()) {
        rows.push(<ChatBubble key={j} side="agent"><RoomProse content={block.text} /></ChatBubble>)
      }
    })
  } else if (msg.type === 'user' && 'message' in msg) {
    const um = msg as UserMessage
    // A subagent's frame whose Task is off the list: whatever it says as
    // `user`, the human did not say it, so it is not a right-hand bubble.
    if (um.parent_tool_use_id != null) return null
    um.message.content.forEach((block, j) => {
      // tool_result: its call's line (R2-B) carries it; never a bubble.
      if (block.type !== 'text' || !block.text) return
      if (block.text === INTERRUPT_TEXT) {
        rows.push(
          <div key={j} data-testid="chat-interrupted"
            className="flex items-center justify-center gap-1.5 text-xs text-status-error italic">
            <Prohibit size={12} />
            <span>{interrupted}</span>
          </div>,
        )
        return
      }
      // Your line is never markdown (same rule as RoomUserLine); a slash
      // command keeps its bubble and gets the mono face.
      rows.push(
        <ChatBubble key={j} side="user" className={block.text.startsWith('/') ? 'font-mono text-[13px]' : 'text-sm'}>
          <p className="whitespace-pre-wrap break-words">{block.text}</p>
        </ChatBubble>,
      )
    })
  }

  return rows.length > 0 ? <>{rows}</> : null
}

export default function ChatTranscript({
  messages,
  keyPrefix,
  showThinking,
  showEmptyHint,
  emptyText,
  scrollKey,
  children,
  afterThinking,
  turnStarts = NO_STARTS,
  partial,
}: ChatTranscriptProps) {
  const t = useI18nStore((s) => s.t)
  const scrollRef = useRef<HTMLDivElement>(null)
  const hasPartial = !!partial && Object.keys(partial.blocks).length > 0
  const hasPending = hasContent(children)
  // Only for `childIndexes`: a subagent's frames stay out of the top level.
  const index = useMemo(() => indexOperations(messages), [messages])
  const turns = useMemo(() => groupTurns(messages, turnStarts), [messages, turnStarts])
  // One fold memory per pane, as in the room: R2-B's lines fold through it.
  const foldStore = useFoldMemory()
  const partialVersion: string = useMemo(() => partialVersionOf(partial), [partial])

  // Same effect and deps as RoomTranscript's auto-scroll.
  useEffect(() => {
    if (scrollRef.current?.scrollTo) {
      scrollRef.current.scrollTo({ top: scrollRef.current.scrollHeight, behavior: 'smooth' })
    }
  }, [messages, scrollKey, partialVersion])

  // As RoomTranscript: the in-flight message belongs to the last turn, and
  // needs one even before any boundary is recorded.
  const shown: RoomTurn[] = turns.length === 0 && hasPartial ? [{ start: 0, end: 0, openerIndex: null }] : turns
  const lastTurn = shown.length - 1
  const interrupted = t('stream.interrupted')

  return (
    <div ref={scrollRef} className="@container flex-1 overflow-y-auto p-4 space-y-3">
      <FoldContext.Provider value={foldStore}>
        {showEmptyHint && (
          <div className="flex items-center justify-center h-full text-text-muted text-sm">
            {emptyText ?? t('stream.waiting')}
          </div>
        )}
        {shown.map((turn, ti) => (
          <RoomTurnGroup key={`${keyPrefix}-turn-${ti}`} index={ti} chrome={false}>
            <div className="space-y-3">
              {messages.slice(turn.start, turn.end).map((msg, k) => {
                const i = turn.start + k
                return index.childIndexes.has(i)
                  ? null
                  : <ChatMessage key={`${keyPrefix}-${i}`} msg={msg} interrupted={interrupted} />
              })}
              {ti === lastTurn && hasPartial && <ChatPartialGroup key={`${keyPrefix}-partial`} partial={partial} />}
            </div>
          </RoomTurnGroup>
        ))}

        {/* The optimistic line, in the provisional turn the room puts it in. */}
        {hasPending && (
          <RoomTurnGroup key={`${keyPrefix}-turn-${shown.length}`} index={shown.length} chrome={false}>
            <ChatBubble side="user" className="text-sm opacity-60">{children}</ChatBubble>
          </RoomTurnGroup>
        )}

        <ThinkingIndicator visible={showThinking} />

        {afterThinking}
      </FoldContext.Provider>
    </div>
  )
}
