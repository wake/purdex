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
import { useI18nStore } from '../../stores/useI18nStore'
import type { StreamMessage } from '../../lib/nex/message-types'
import { partialVersionOf, type PartialAssembly } from '../../lib/nex/partial'
import type { ToolActivity } from '../../lib/nex/tool-activity'
import { indexOperations } from '../../lib/nex/operations'
import { groupTurns, type RoomTurn } from '../../lib/nex/turns'
import ThinkingIndicator from '../ThinkingIndicator'
import PartialMessageGroup from '../PartialMessageGroup'
import RoomTurnGroup from './RoomTurnGroup'
import { renderMessage, type RenderCtx } from './render-message'
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

  const ctx: RenderCtx = { messages, index, tools, now, keyPrefix, depth: 0 }

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
              {/*
                A subagent's frames are drawn inside the Task that spawned them
                (spec §4.5), so the top level skips exactly those. Indexes stay
                absolute — a skip shifts nothing — and a turn left with only
                skipped frames is the same empty container a declared empty
                turn already is.
              */}
              {messages.slice(turn.start, turn.end).map((msg, k) =>
                index.childIndexes.has(turn.start + k) ? null : renderMessage(msg, turn.start + k, ctx))}
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
