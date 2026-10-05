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
import { useI18nStore } from '../../stores/useI18nStore'
import { partialChatVersionOf } from '../../lib/nex/partial'
import { indexOperations } from '../../lib/nex/operations'
import { classifyTurnOperations } from '../../lib/nex/operation-status'
import { groupTurns, type RoomTurn } from '../../lib/nex/turns'
import { chatToolsKey } from '../../lib/nex/transcript-search'
import ThinkingIndicator from '../ThinkingIndicator'
import RoomTurnGroup from '../room/RoomTurnGroup'
import TurnFooter from '../room/TurnFooter'
import type { RenderCtx } from '../room/render-message'
import { FoldContext, useInheritedFoldMemory } from '../room/fold-context'
import PreludeAnchor from '../room/prelude/PreludeAnchor'
import { useScrollControl, useTranscriptScroll } from '../../hooks/useTranscriptScroll'
import type { RoomTranscriptProps } from '../room/RoomTranscript'
import ChatTurnBody from './ChatTurnBody'

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
  turnMeta,
  partial,
  tools,
  now,
  subagentTasks,
  scrollRef,
  holdScroll = false,
  scrollControl,
  scrollMemoryKey,
  prelude,
  preludeVersion,
}: ChatTranscriptProps) {
  const t = useI18nStore((s) => s.t)
  const scroll = useTranscriptScroll(scrollRef, holdScroll, scrollMemoryKey ? { paneId: scrollMemoryKey, view: 'chat' } : undefined)
  const { attach, onScroll, follow, shiftBy } = scroll
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
    <div ref={attach} onScroll={onScroll} className={`@container flex-1 overflow-y-auto p-4 space-y-3${prelude !== undefined ? ' [overflow-anchor:none]' : ''}`}>
      <FoldContext.Provider value={foldStore}>
        {showEmptyHint && (
          <div className="flex items-center justify-center h-full text-text-muted text-sm">
            {emptyText ?? t('stream.waiting')}
          </div>
        )}
        {prelude !== undefined && (
          <PreludeAnchor version={preludeVersion ?? ''} onGrow={shiftBy}>{prelude}</PreludeAnchor>
        )}
        {shown.map((turn, ti) => {
          return (
            <RoomTurnGroup key={`${keyPrefix}-turn-${ti}`} index={ti} chrome={false}>
              <ChatTurnBody messages={messages} turn={turn} ops={opsByTurn[ti] ?? []} ctx={ctx}
                toolsKey={chatToolsKey(keyPrefix, ti)} interrupted={interrupted}
                partial={partial} withPartial={ti === lastTurn && hasPartial}
                footer={turn.boundary !== null && turnMeta?.[turn.boundary] ? <TurnFooter meta={turnMeta[turn.boundary]} /> : null} />
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
