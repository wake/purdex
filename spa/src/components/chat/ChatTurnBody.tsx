// spa/src/components/chat/ChatTurnBody.tsx — one turn's chat rows (moved out of
// ChatTranscript's turn loop so the worker prelude draws spans the same way):
// bubbles for the prose, one tools line per turn, an edited / failed line each
// at its own position, then the in-flight partial and the footer.
import type { ReactNode } from 'react'
import { Prohibit } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import type { AssistantMessage, ContentBlock, StreamMessage, UserMessage } from '../../lib/nex/message-types'
import { partialBlockKey, partialToolUses, type PartialAssembly } from '../../lib/nex/partial'
import { toolResultText, type BlockKey } from '../../lib/nex/operations'
import { keyAt, rowKey, type MessageIdOf } from '../../lib/nex/message-keys'
import { toolEntryFor, type TurnOperation } from '../../lib/nex/operation-status'
import { utf8Length } from '../../lib/nex/fold'
import { INTERRUPT_TEXT } from '../../lib/nex/turns'
import { searchUnitId } from '../../lib/nex/transcript-search'
import RoomProse from '../room/RoomProse'
import OperationBlock from '../room/OperationBlock'
import { OperationAt } from '../room/MessageRow'
import type { RenderCtx } from '../room/render-message'
import ChatBubble, { ChatUserBubble } from './ChatBubble'
import AttachmentThumbs from '../room/AttachmentThumbs'
import { attachmentsOf } from '../../lib/nex/attachments'
import ChatPartialGroup from './ChatPartialGroup'
import ChatToolsLine from './ChatToolsLine'
import ChatEditedLine from './ChatEditedLine'
import ChatFailedLine from './ChatFailedLine'
import { OmittedMedia, TruncatedHint } from '../room/prelude/Placeholders'
import { blockShownBytes, isOmittedMedia } from '../room/prelude/placeholder-utils'

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
  if (op.kind === 'edited' && facts?.diff) {
    // The edited line holds only the diff, so a cut call or result says so
    // beside it, visible without expanding (spec 5.3: hints go with whatever
    // line represents the operation).
    const result = call ? ctx.index.resultForCall.get(op.key) : undefined
    return (
      <>
        <ChatEditedLine foldKey={op.key} diff={facts.diff} />
        {call && block.truncated && <TruncatedHint shown={blockShownBytes(block)} total={block.total_bytes ?? null} />}
        {result?.truncated && <TruncatedHint shown={utf8Length(result.text)} total={result.totalBytes ?? null} />}
      </>
    )
  }
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
function ChatMessage({ msg, i, interrupted, lineAt, idOf }: {
  msg: StreamMessage
  /** Its position in the transcript's messages (search anchors are named by it). */
  i: number
  interrupted: string
  lineAt: (j: number) => ReactNode
  idOf?: MessageIdOf
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
        rows.push(<ChatBubble key={j} side="agent"><RoomProse content={block.text} searchUnit={searchUnitId(keyAt({ idOf }, i, j), 'text')} /></ChatBubble>)
        // Only a prelude block is ever cut (live frames carry no `truncated`).
        if (block.truncated) rows.push(<TruncatedHint key={`cut-${j}`} shown={blockShownBytes(block)} total={block.total_bytes ?? null} />)
      } else if (isOmittedMedia(block)) {
        rows.push(<ChatBubble key={j} side="agent"><OmittedMedia block={block} /></ChatBubble>)
      } else if (block.type === 'tool_use') {
        line(j)
      }
    })
  } else if (msg.type === 'user' && 'message' in msg) {
    const um = msg as UserMessage
    // A subagent's frame whose Task is off the list: whatever it says as
    // `user`, the human did not say it, so it is not a right-hand bubble.
    const fromSubagent = um.parent_tool_use_id != null
    // Phase E: the images this line carried go inside your bubble, under the
    // text; with no text to hang them on (an image-only message, Review
    // Focus 4) they are a bubble of their own.
    const atts = fromSubagent ? undefined : attachmentsOf(msg)
    // Unlike room (MessageRow), a slash command's text block is not excluded
    // from the search here: below, every text block — slash command
    // included — becomes a ChatUserBubble (the mono face just changes its
    // rendering, not its kind of row), so attaching the thumbnails to that
    // same bubble still reads as "your line, with its images". Room instead
    // pulls a slash command out into its own icon+mono row, which is not a
    // RoomUserLine and so has no attachments slot to hang them on — room
    // gives the images a line of their own in that case (see MessageRow).
    const attsAt = atts ? um.message.content.findIndex((b) => b.type === 'text' && !!b.text && b.text !== INTERRUPT_TEXT) : -1
    um.message.content.forEach((block, j) => {
      // tool_result: its call's line carries it (an orphan has a line of its own); never a bubble.
      if (block.type === 'tool_result') { line(j); return }
      if (!fromSubagent && isOmittedMedia(block)) {
        rows.push(<ChatBubble key={j} side="user"><OmittedMedia block={block} /></ChatBubble>)
        return
      }
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
      rows.push(<ChatUserBubble key={j} text={block.text} searchUnit={searchUnitId(keyAt({ idOf }, i, j), 'text')}
        attachments={atts && j === attsAt ? <AttachmentThumbs items={atts} /> : undefined} />)
      if (block.truncated) rows.push(<TruncatedHint key={`cut-${j}`} shown={blockShownBytes(block)} total={block.total_bytes ?? null} />)
    })
    if (atts && attsAt < 0) rows.push(<ChatUserBubble key="attachments" text="" attachments={<AttachmentThumbs items={atts} />} />)
  }

  return rows.length > 0 ? <>{rows}</> : null
}

export interface ChatTurnBodyProps {
  messages: StreamMessage[]
  turn: { start: number; end: number }
  ops: TurnOperation[]
  ctx: RenderCtx
  toolsKey: string
  interrupted: string
  partial?: PartialAssembly | null
  /** The in-flight message belongs to this turn: draw the partial and its streaming calls. */
  withPartial?: boolean
  footer?: ReactNode
}

export default function ChatTurnBody({ messages, turn, ops, ctx, toolsKey, interrupted, partial, withPartial = false, footer }: ChatTurnBodyProps) {
  const t = useI18nStore((s) => s.t)
  const { index, keyPrefix } = ctx
  const plain = ops.filter((o) => o.kind === 'plain')
  // A call still streaming its input belongs to the running turn's tools.
  const streaming = withPartial ? partialToolUses(partial) : []
  const count = plain.length + streaming.length
  const toolsLine = count > 0 && (
    <ChatToolsLine
      foldKey={toolsKey}
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
    <div className="space-y-3">
      {messages.slice(turn.start, turn.end).map((msg, k) => {
        const i = turn.start + k
        return index.childIndexes.has(i)
          ? null
          : <ChatMessage key={rowKey(ctx, i)} msg={msg} i={i} interrupted={interrupted} lineAt={(j) => lines.get(keyAt(ctx, i, j))} idOf={ctx.idOf} />
      })}
      {withPartial && partial && <ChatPartialGroup key={`${keyPrefix}-partial`} partial={partial} />}
      {/* Only streaming calls so far: the line comes with them, after
          any text streaming ahead of them, so it does not jump from
          above the typing bubble to below it once the text lands. */}
      {plain.length === 0 && toolsLine}
      {/* Spec §7.2: the footer is the last line of a completed turn; a live turn's meta has no endAt, so it draws nothing. */}
      {footer}
    </div>
  )
}
