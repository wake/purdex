// spa/src/components/chat/ChatTranscript.tsx — the chat transcript (spec §5,
// R2 plan T1.3 / T2.1). The same state as the room, drawn with minimum
// ceremony for a phone: the agent's prose in bubbles on the left, your lines
// in bubbles on the right, and a turn's tool work as a few quiet lines.
//
// - **No thinking**, durable or streaming. While only a thought streams the
//   pane keeps its dots on (ExecutionView's chat rule, partialHasChatContent).
// - **Operations** are sorted by the room's own rule (classifyTurnOperations):
//   a turn's ordinary tools fold into one ChatToolsLine at the position of the
//   first of them (a call still streaming its input counts too); an edit is a
//   ChatEditedLine and a failure a ChatFailedLine, each at its own position.
//   Every line expands in place into the room's blocks — nothing in chat is
//   unreachable. A tool_result is never a bubble.
// - **Turns** are RoomTurnGroups without their fold strip, so the lines'
//   fold memory registers per turn and expand-all still reaches them.
// - The root is an `@container`: a narrow pane on a desktop behaves like a
//   phone (the bubble cap is a share of the pane below `@md`).
import { Children, isValidElement, useEffect, useMemo, type ReactNode } from 'react'
import { Prohibit } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import type { AssistantMessage, ContentBlock, StreamMessage, UserMessage } from '../../lib/nex/message-types'
import { partialBlockKey, partialChatVersionOf, partialToolUses } from '../../lib/nex/partial'
import { blockKey, indexOperations, toolResultText, type BlockKey } from '../../lib/nex/operations'
import { classifyTurnOperations, toolEntryFor, type TurnOperation } from '../../lib/nex/operation-status'
import { groupTurns, INTERRUPT_TEXT, type RoomTurn } from '../../lib/nex/turns'
import { chatToolsKey, searchUnitId } from '../../lib/nex/transcript-search'
import ThinkingIndicator from '../ThinkingIndicator'
import RoomTurnGroup from '../room/RoomTurnGroup'
import RoomProse from '../room/RoomProse'
import OperationBlock from '../room/OperationBlock'
import { OperationAt } from '../room/MessageRow'
import type { RenderCtx } from '../room/render-message'
import { FoldContext, useInheritedFoldMemory } from '../room/fold-context'
import { useScrollControl, useTranscriptScroll } from '../../hooks/useTranscriptScroll'
import type { RoomTranscriptProps } from '../room/RoomTranscript'
import ChatBubble, { ChatUserBubble } from './ChatBubble'
import ChatPartialGroup from './ChatPartialGroup'
import ChatToolsLine from './ChatToolsLine'
import ChatEditedLine from './ChatEditedLine'
import ChatFailedLine from './ChatFailedLine'

/**
 * RoomTranscript's props, so the pane hands either view the same state.
 * `showThinking` is the ThinkingIndicator (the dots), not thinking blocks —
 * those chat never draws; the caller computes it with chat's rule.
 */
export type ChatTranscriptProps = RoomTranscriptProps

const NO_STARTS: readonly number[] = []

/** `false` / `null` from a `{cond && <x/>}` slot is not a line to hold (as RoomTranscript). */
function hasContent(node: ReactNode): boolean {
  return Children.toArray(node).some((c) => isValidElement(c) || typeof c === 'string' || typeof c === 'number')
}

function blockAt(messages: StreamMessage[], op: TurnOperation): ContentBlock | undefined {
  return (messages[op.msgIndex] as { message?: { content?: ContentBlock[] } } | undefined)?.message?.content?.[op.blockIndex]
}

/**
 * An edited or failed operation's own line. The name, facts and result are
 * read the way MessageRow reads them for the room block the line expands into.
 */
function ChatOperationLine({ op, ctx }: { op: TurnOperation; ctx: RenderCtx }) {
  const t = useI18nStore((s) => s.t)
  const block = blockAt(ctx.messages, op)
  if (!block) return null
  const call = block.type === 'tool_use'
  const facts = toolEntryFor(ctx.tools, call ? block.id : block.tool_use_id)
  if (op.kind === 'edited' && facts?.diff) return <ChatEditedLine foldKey={op.key} diff={facts.diff} />
  const name = (call ? block.name : facts?.file?.path) || t('execution.tool.unknown')
  const message = call ? (ctx.index.resultForCall.get(op.key)?.text ?? '') : toolResultText(block.content)
  return (
    <ChatFailedLine foldKey={op.key} name={name} message={message}
      renderOperation={() => <OperationAt msg={ctx.messages[op.msgIndex]} i={op.msgIndex} j={op.blockIndex} ctx={ctx} />} />
  )
}

/**
 * One durable top-level message as chat rows; null when it has nothing chat
 * draws. `lineAt(j)` is the operation line (if any) that sits at block j.
 */
function ChatMessage({ msg, i, interrupted, lineAt }: {
  msg: StreamMessage
  /** Its position in the transcript's messages (search anchors are named by it). */
  i: number
  interrupted: string
  lineAt: (j: number) => ReactNode
}) {
  const rows: ReactNode[] = []
  const line = (j: number) => {
    const node = lineAt(j)
    if (node) rows.push(<div key={`op-${j}`}>{node}</div>)
  }

  if (msg.type === 'assistant' && 'message' in msg) {
    ;(msg as AssistantMessage).message.content.forEach((block, j) => {
      // Thinking draws nothing in chat.
      if (block.type === 'text' && block.text?.trim()) {
        rows.push(<ChatBubble key={j} side="agent"><RoomProse content={block.text} searchUnit={searchUnitId(blockKey(i, j), 'text')} /></ChatBubble>)
      } else if (block.type === 'tool_use') {
        line(j)
      }
    })
  } else if (msg.type === 'user' && 'message' in msg) {
    const um = msg as UserMessage
    // A subagent's frame whose Task is off the list: whatever it says as
    // `user`, the human did not say it, so it is not a right-hand bubble.
    const fromSubagent = um.parent_tool_use_id != null
    um.message.content.forEach((block, j) => {
      // tool_result: its call's line carries it (an orphan has a line of its own); never a bubble.
      if (block.type === 'tool_result') { line(j); return }
      if (fromSubagent || block.type !== 'text' || !block.text) return
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
      // Your line is never markdown; a slash command gets the mono face (ChatUserBubble).
      rows.push(<ChatUserBubble key={j} text={block.text} searchUnit={searchUnitId(blockKey(i, j), 'text')} />)
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
  tools,
  now,
  subagentTasks,
  scrollRef,
  holdScroll = false,
  scrollControl,
  scrollMemoryKey,
}: ChatTranscriptProps) {
  const t = useI18nStore((s) => s.t)
  const scroll = useTranscriptScroll(scrollRef, holdScroll, scrollMemoryKey ? { paneId: scrollMemoryKey, view: 'chat' } : undefined)
  const { attach, onScroll, follow } = scroll
  useScrollControl(scrollControl, scroll)
  const hasPartial = !!partial && Object.keys(partial.blocks).length > 0
  const hasPending = hasContent(children)
  // The room's pairing: results for the lines' blocks, and `childIndexes`, so
  // a subagent's frames stay out of the top level.
  const index = useMemo(() => indexOperations(messages), [messages])
  const turns = useMemo(() => groupTurns(messages, turnStarts), [messages, turnStarts])
  // One classification per turn, by the rule the room draws its blocks by.
  const opsByTurn = useMemo(
    () => turns.map((turn) => classifyTurnOperations(messages, turn, index, tools)),
    [turns, messages, index, tools],
  )
  // One fold memory per pane, as in the room (the pane's, when it provides one).
  const foldStore = useInheritedFoldMemory()
  // F10 / R2-B: the key moves only for what chat draws — the partial's text
  // and streaming calls, and each turn's lines (how many operations of each
  // kind, and which are running). Not a thought, not a tool's input.
  const partialVersion: string = useMemo(() => partialChatVersionOf(partial), [partial])
  const linesVersion: string = useMemo(
    () => opsByTurn.map((ops) => ops.map((o) => `${o.kind[0]}${o.status === 'running' ? '*' : ''}`).join('')).join('|'),
    [opsByTurn],
  )

  // Same effect as RoomTranscript's auto-scroll: placed at once on the first
  // run (mount / view switch, from the pane's memory), smooth after (F3),
  // and only for a reader at the bottom (§6).
  useEffect(() => { follow() }, [follow, messages, scrollKey, partialVersion, linesVersion])

  // As RoomTranscript: the in-flight message belongs to the last turn, and
  // needs one even before any boundary is recorded.
  const shown: RoomTurn[] = turns.length === 0 && hasPartial ? [{ start: 0, end: 0, openerIndex: null, boundary: null }] : turns
  const lastTurn = shown.length - 1
  const interrupted = t('stream.interrupted')
  const ctx: RenderCtx = { messages, index, tools, now, keyPrefix, depth: 0, subagentTasks }

  return (
    <div ref={attach} onScroll={onScroll} className="@container flex-1 overflow-y-auto p-4 space-y-3">
      <FoldContext.Provider value={foldStore}>
        {showEmptyHint && (
          <div className="flex items-center justify-center h-full text-text-muted text-sm">
            {emptyText ?? t('stream.waiting')}
          </div>
        )}
        {shown.map((turn, ti) => {
          const ops = opsByTurn[ti] ?? []
          const plain = ops.filter((o) => o.kind === 'plain')
          // A call still streaming its input belongs to the running turn's tools.
          const streaming = ti === lastTurn && hasPartial ? partialToolUses(partial) : []
          const count = plain.length + streaming.length
          const toolsLine = count > 0 && (
            <ChatToolsLine
              foldKey={chatToolsKey(keyPrefix, ti)}
              count={count}
              running={streaming.length > 0 || plain.some((o) => o.status === 'running')}
              renderOperations={() => (
                <>
                  {plain.map((o) => (
                    <OperationAt key={o.key} msg={messages[o.msgIndex]} i={o.msgIndex} j={o.blockIndex} ctx={ctx} />
                  ))}
                  {partial && streaming.map((b) => {
                    const key = partialBlockKey(partial, b)
                    return (
                      <OperationBlock key={key} tool={b.toolName ?? t('execution.tool.unknown')} input={{}}
                        activity={{ status: 'streaming', rawInput: b.partialJson }} result={null} foldKey={key} />
                    )
                  })}
                </>
              )}
            />
          )
          const lines = new Map<BlockKey, ReactNode>()
          // The tools line sits where the turn's first ordinary tool was called.
          if (plain.length > 0) lines.set(plain[0].key, toolsLine)
          for (const op of ops) {
            if (op.kind !== 'plain') lines.set(op.key, <ChatOperationLine op={op} ctx={ctx} />)
          }
          return (
            <RoomTurnGroup key={`${keyPrefix}-turn-${ti}`} index={ti} chrome={false}>
              <div className="space-y-3">
                {messages.slice(turn.start, turn.end).map((msg, k) => {
                  const i = turn.start + k
                  return index.childIndexes.has(i)
                    ? null
                    : <ChatMessage key={`${keyPrefix}-${i}`} msg={msg} i={i} interrupted={interrupted} lineAt={(j) => lines.get(blockKey(i, j))} />
                })}
                {ti === lastTurn && hasPartial && <ChatPartialGroup key={`${keyPrefix}-partial`} partial={partial} />}
                {/* Only streaming calls so far: the line comes with them, after
                    any text streaming ahead of them, so it does not jump from
                    above the typing bubble to below it once the text lands. */}
                {plain.length === 0 && toolsLine}
              </div>
            </RoomTurnGroup>
          )
        })}

        {/* The optimistic line (the caller's pending ChatUserBubble), in the provisional turn the room puts it in. */}
        {hasPending && (
          <RoomTurnGroup key={`${keyPrefix}-turn-${shown.length}`} index={shown.length} chrome={false}>
            {children}
          </RoomTurnGroup>
        )}

        <ThinkingIndicator visible={showThinking} />

        {afterThinking}
      </FoldContext.Provider>
    </div>
  )
}
