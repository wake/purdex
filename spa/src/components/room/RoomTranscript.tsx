// spa/src/components/room/RoomTranscript.tsx — the room transcript (spec
// §4.1). Takes over from ConversationMessages with its props, its pairing and
// its fold memory, and changes the frame:
//
// - **One left edge.** Nothing is pushed right and nothing is clamped to a
//   percentage of the pane. Authorship is a gutter mark on the user's line;
//   prose is capped by a reading measure only, and operations take the width.
// - **Turns are containers.** The boundaries are the ones the reducer recorded
//   (`turnStarts`, from execution.message_accepted / execution.delegated),
//   shaped by `groupTurns`; each range is a RoomTurnGroup, which draws no edge
//   of its own and whose hover strip folds everything inside it.
import { Children, isValidElement, useRef, useEffect, useMemo, type ReactNode } from 'react'
import { Prohibit, TerminalWindow } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import {
  type StreamMessage,
  type AssistantMessage,
  type UserMessage,
} from '../../lib/nex/message-types'
import { partialVersionOf, type PartialAssembly } from '../../lib/nex/partial'
import { toToolCallActivity, type ToolActivity } from '../../lib/nex/tool-activity'
import { blockKey, indexOperations, toolResultText, type OperationIndex } from '../../lib/nex/operations'
import { groupTurns, INTERRUPT_TEXT, type RoomTurn } from '../../lib/nex/turns'
import ThinkingIndicator from '../ThinkingIndicator'
import PartialMessageGroup from '../PartialMessageGroup'
import OperationBlock from './OperationBlock'
import RoomProse from './RoomProse'
import RoomThinking from './RoomThinking'
import RoomTurnGroup from './RoomTurnGroup'
import RoomUserLine from './RoomUserLine'
import { FoldContext, useFoldMemory } from './fold-context'

export interface RoomTranscriptProps {
  messages: StreamMessage[]
  keyPrefix: string            // stable per pane; keys are `${keyPrefix}-${i}`
  showThinking: boolean        // ThinkingIndicator visibility
  showEmptyHint: boolean       // "waiting" hint (the caller decides when)
  emptyText?: string           // override for the hint (default t('stream.waiting'))
  scrollKey?: number           // extra auto-scroll dependency (the optimistic line)
  /**
   * The optimistic line (ExecutionView's pendingLocal). It is not in
   * `messages` and has no recorded boundary yet, so it is drawn inside a
   * provisional turn one past the last real one — the same index the real
   * turn takes when execution.message_accepted lands, so nothing jumps.
   */
  children?: ReactNode
  afterThinking?: ReactNode    // rendered AFTER ThinkingIndicator
  /** Turn boundaries the reducer recorded (ExecutionState.turnStarts). Absent → one turn. */
  turnStarts?: readonly number[]
  // P-B2.2 spec §4.4.
  partial?: PartialAssembly | null          // R1: trailing in-flight assistant group
  tools?: Record<string, ToolActivity>      // R2: status/timing for durable tool_use blocks, by block id
  now?: number                              // R2: ticker value for running tools
}

const NO_STARTS: readonly number[] = []

/** `false` / `null` from a `{cond && <x/>}` slot is not a line to hold. */
function hasContent(node: ReactNode): boolean {
  return Children.toArray(node).some((c) => isValidElement(c) || typeof c === 'string' || typeof c === 'number')
}

export default function RoomTranscript({
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
}: RoomTranscriptProps) {
  const t = useI18nStore((s) => s.t)
  const scrollRef = useRef<HTMLDivElement>(null)
  const hasPartial = !!partial && Object.keys(partial.blocks).length > 0
  const hasPending = hasContent(children)
  // Spec §4.2: a tool_use and the tool_result that answers it are one block.
  // The pairing runs over the whole list, not per turn — a result can land
  // after a boundary and still belongs to the call that asked for it.
  const index = useMemo(() => indexOperations(messages), [messages])
  const turns = useMemo(() => groupTurns(messages, turnStarts), [messages, turnStarts])
  // Spec §3.2: one fold memory per pane, above the blocks — a block unmounts
  // whenever its row is re-keyed and would take a local useState with it.
  const foldStore = useFoldMemory()
  // R4: follow the typewriter by a content/structure key, not the assembly's identity.
  const partialVersion: string = useMemo(() => partialVersionOf(partial), [partial])

  // Auto-scroll on new messages, control requests, or partial growth
  useEffect(() => {
    if (scrollRef.current?.scrollTo) {
      scrollRef.current.scrollTo({ top: scrollRef.current.scrollHeight, behavior: 'smooth' })
    }
  }, [messages, scrollKey, partialVersion])

  // The in-flight assistant message belongs to the turn that is running: the
  // last recorded one. With nothing recorded yet it still needs a container,
  // or expand-all could never reach the call it is streaming.
  const shown: RoomTurn[] = turns.length === 0 && hasPartial ? [{ start: 0, end: 0, openerIndex: null }] : turns
  const lastTurn = shown.length - 1

  const renderMessage = (msg: StreamMessage, i: number) => (
    <MessageRow key={`${keyPrefix}-${i}`} msg={msg} i={i} index={index} tools={tools} now={now} />
  )

  return (
    <div ref={scrollRef} className="flex-1 overflow-y-auto p-4 space-y-4">
      <FoldContext.Provider value={foldStore}>
        {showEmptyHint && (
          <div className="flex items-center justify-center h-full text-text-muted text-sm">
            {emptyText ?? t('stream.waiting')}
          </div>
        )}
        {shown.map((turn, ti) => (
          <RoomTurnGroup key={`${keyPrefix}-turn-${ti}`} index={ti}>
            <div className="space-y-4">
              {messages.slice(turn.start, turn.end).map((msg, k) => renderMessage(msg, turn.start + k))}
              {/* R1: the in-flight assistant message, after the durable list and before children */}
              {ti === lastTurn && hasPartial && <PartialMessageGroup key={`${keyPrefix}-partial`} partial={partial} />}
            </div>
          </RoomTurnGroup>
        ))}

        {hasPending && (
          <RoomTurnGroup key={`${keyPrefix}-turn-${shown.length}`} index={shown.length}>
            {children}
          </RoomTurnGroup>
        )}

        {/* Thinking indicator */}
        <ThinkingIndicator visible={showThinking} />

        {afterThinking}
      </FoldContext.Provider>
    </div>
  )
}

interface MessageRowProps {
  msg: StreamMessage
  i: number
  index: OperationIndex
  tools?: Record<string, ToolActivity>
  now?: number
}

function MessageRow({ msg, i, index, tools, now }: MessageRowProps) {
  const t = useI18nStore((s) => s.t)

  // --- Assistant messages ---
  if (msg.type === 'assistant' && 'message' in msg) {
    const am = msg as AssistantMessage
    return (
      <div>
        {am.message.content.map((block, j) => {
          // Spec §4.3: a thought with no text draws nothing (RoomThinking
          // returns null for it too; this keeps the list free of the slot).
          if (block.type === 'thinking' && block.thinking?.trim()) {
            return <RoomThinking key={j} content={block.thinking} foldKey={blockKey(i, j)} />
          }
          if (block.type === 'text' && block.text) {
            return <RoomProse key={j} content={block.text} />
          }
          if (block.type === 'tool_use') {
            // R2: a tools entry adds status, timing and the N2 facts. Own-key
            // lookup: block ids are untrusted strings (`constructor` would
            // otherwise hit Object.prototype), and this is the only place that
            // guard lives — OperationBlock takes no `tools` map.
            const entry = tools && block.id && Object.hasOwn(tools, block.id) ? tools[block.id] : undefined
            // The `??` belongs here, not in the block: it fires on an absent
            // name, while the block's own guard fires on an empty one. A
            // nameless call still renders — skipping it would consume its
            // result in the pairing and then show it nowhere.
            return (
              <OperationBlock
                key={j}
                tool={block.name ?? t('execution.tool.unknown')}
                input={block.input ?? {}}
                activity={entry ? toToolCallActivity(entry, now ?? 0) : undefined}
                summaryEntry={entry}
                facts={entry}
                result={index.resultForCall.get(blockKey(i, j)) ?? null}
                foldKey={blockKey(i, j)}
              />
            )
          }
          return null
        })}
      </div>
    )
  }

  // --- User messages ---
  if (msg.type === 'user' && 'message' in msg) {
    const um = msg as UserMessage
    return (
      <div>
        {um.message.content.map((block, j) => {
          if (block.type === 'tool_result') {
            // Its call already showed it (spec §4.2) — drawing it again
            // would be the second card the operation block replaces.
            if (index.consumedResults.has(blockKey(i, j))) return null
            // An orphan: no call on this list claimed it, so it carries the
            // whole operation on its own. P-B3 R4: the N2 entry for this
            // result's tool_use_id, by own key (ids are untrusted strings).
            const facts = tools && block.tool_use_id && Object.hasOwn(tools, block.tool_use_id)
              ? tools[block.tool_use_id]
              : undefined
            return (
              <OperationBlock
                key={j}
                tool={facts?.file?.path ?? t('execution.tool.unknown')}
                input={{}}
                facts={facts}
                result={{ text: toolResultText(block.content), isError: block.is_error ?? false }}
                foldKey={blockKey(i, j)}
              />
            )
          }

          if (block.type === 'text' && block.text) {
            // The interrupt keeps its meaning, loses the bubble: an error-toned
            // line at the edge, on theme tokens.
            if (block.text === INTERRUPT_TEXT) {
              return (
                <div key={j} data-testid="interrupted-msg"
                  className="flex items-center gap-1.5 text-sm text-status-error italic">
                  <Prohibit size={14} />
                  <span>{t('stream.interrupted')}</span>
                </div>
              )
            }

            // A slash command: the human's line, told apart by its icon and face.
            if (block.text.startsWith('/')) {
              return (
                <div key={j} data-testid="room-command"
                  className="flex items-center gap-1.5 text-[13px] text-status-warning font-mono">
                  <TerminalWindow size={14} weight="bold" />
                  <span>{block.text}</span>
                </div>
              )
            }

            return <RoomUserLine key={j} text={block.text} />
          }

          return null
        })}
      </div>
    )
  }

  return null
}
