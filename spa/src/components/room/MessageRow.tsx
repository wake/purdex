// spa/src/components/room/MessageRow.tsx — one durable message of the room
// transcript, as blocks. Reached through `renderMessage` (render-message.tsx),
// which RoomTranscript and a subagent's nested rail both call.
import { Prohibit, TerminalWindow } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import {
  type ContentBlock,
  type StreamMessage,
  type AssistantMessage,
  type UserMessage,
} from '../../lib/nex/message-types'
import { toToolCallActivity } from '../../lib/nex/tool-activity'
import { blockKey, toolResultText } from '../../lib/nex/operations'
import { INTERRUPT_TEXT } from '../../lib/nex/turns'
import { searchUnitId } from '../../lib/nex/transcript-search'
import OperationBlock from './OperationBlock'
import RoomProse from './RoomProse'
import RoomSubagentLine from './RoomSubagentLine'
import RoomThinking from './RoomThinking'
import RoomUserLine from './RoomUserLine'
import AttachmentThumbs from './AttachmentThumbs'
import { attachmentsOf } from '../../lib/nex/attachments'
import SubagentBlock, { SubagentTaskSuffix } from './SubagentBlock'
import type { RenderCtx } from './render-message'

export interface MessageRowProps {
  msg: StreamMessage
  i: number
  ctx: RenderCtx
}

/** tool_use blocks in the given messages — a subagent's own calls, not its children's. */
function countToolCalls(indexes: readonly number[], messages: StreamMessage[]): number {
  let n = 0
  for (const ci of indexes) {
    const content = (messages[ci] as { message?: { content?: unknown } } | undefined)?.message?.content
    if (!Array.isArray(content)) continue
    for (const b of content as ContentBlock[]) if (b?.type === 'tool_use') n++
  }
  return n
}

export interface OperationAtProps {
  /** The message at position `i` of `ctx.messages`. */
  msg: StreamMessage
  i: number
  /** The block's index in the message's content. */
  j: number
  ctx: RenderCtx
}

/**
 * The room's block for the operation at `(i, j)`: an assistant `tool_use`
 * (with its answer, and a Task's subagent), or a `tool_result` no call
 * claimed. null for any other block, and for a result its call already
 * shows. Out of MessageRow so chat's operation lines can expand into exactly
 * the block the room draws (R2 plan T2.1) without rendering the whole row.
 */
export function OperationAt({ msg, i, j, ctx }: OperationAtProps) {
  const t = useI18nStore((s) => s.t)
  const { index, tools, now } = ctx
  const block = (msg as { message?: { content?: ContentBlock[] } }).message?.content?.[j]
  if (!block) return null

  if (msg.type === 'assistant' && block.type === 'tool_use') {
    // R2: a tools entry adds status, timing and the N2 facts. Own-key
    // lookup: block ids are untrusted strings (`constructor` would
    // otherwise hit Object.prototype), and this is the only place that
    // guard lives — OperationBlock takes no `tools` map.
    const entry = tools && block.id && Object.hasOwn(tools, block.id) ? tools[block.id] : undefined
    // The `??` belongs here, not in the block: it fires on an absent
    // name, while the block's own guard fires on an empty one. A
    // nameless call still renders — skipping it would consume its
    // result in the pairing and then show it nowhere.
    //
    // Spec §4.5: a call whose subagent left frames on this list carries
    // them on a nested rail. Looked up by this call's position, not its
    // id: two calls reusing an id each own only their own frames.
    const children = index.childrenByParent.get(blockKey(i, j))
    const subagentType = (block.input as { subagent_type?: unknown } | undefined)?.subagent_type
    const inner: RenderCtx = { ...ctx, depth: ctx.depth + 1 }
    // R4 T3.3: this call's subagent task row (nexen v0.13), by its id — a
    // Map, so an id like `constructor` is just a missing key.
    const task = block.id ? ctx.subagentTasks?.get(block.id) : undefined
    const subagent = children ? (
      <SubagentBlock
        task={task}
        now={now}
        name={typeof subagentType === 'string' && subagentType
          ? subagentType
          : (block.name || t('execution.tool.unknown'))}
        toolCount={countToolCalls(children, ctx.messages)}
        foldKey={blockKey(i, j)}
        depth={inner.depth}
        // Direct children only: a nested subagent's frames are listed
        // under its own Task, which draws them one rail further in.
        // Keys and fold keys stay positional, so they cannot collide
        // with the top level, which skips exactly these indexes.
        renderChildren={() => children.map((ci) => (
          <MessageRow key={`${ctx.keyPrefix}-${ci}`} msg={ctx.messages[ci]} i={ci} ctx={inner} />
        ))}
      />
    ) : undefined
    return (
      <OperationBlock
        tool={block.name ?? t('execution.tool.unknown')}
        input={block.input ?? {}}
        activity={entry ? toToolCallActivity(entry, now ?? 0) : undefined}
        summaryEntry={entry}
        facts={entry}
        result={index.resultForCall.get(blockKey(i, j)) ?? null}
        foldKey={blockKey(i, j)}
        subagent={subagent}
        searchKey={blockKey(i, j)}
        // No children to fold (backgrounded, or frames not here yet): the
        // close-out goes on the call's own header. A call still running as a
        // tool already shows its own clock there, so no second one.
        headerExtra={task && !children && !(task.status === 'running' && entry?.status === 'running')
          ? <SubagentTaskSuffix task={task} now={now} />
          : undefined}
      />
    )
  }

  if (msg.type === 'user' && block.type === 'tool_result') {
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
        tool={facts?.file?.path ?? t('execution.tool.unknown')}
        input={{}}
        facts={facts}
        result={{ text: toolResultText(block.content), isError: block.is_error ?? false }}
        foldKey={blockKey(i, j)}
        searchKey={blockKey(i, j)}
      />
    )
  }

  return null
}

export default function MessageRow({ msg, i, ctx }: MessageRowProps) {
  const t = useI18nStore((s) => s.t)
  // A subagent's own frame (#1263): whatever it says as `user`, the human did not say it.
  const fromSubagent = (msg as { parent_tool_use_id?: string | null }).parent_tool_use_id != null

  // --- Assistant messages ---
  if (msg.type === 'assistant' && 'message' in msg) {
    const am = msg as AssistantMessage
    return (
      <div>
        {am.message.content.map((block, j) => {
          // Spec §4.3: a thought with no text draws nothing (RoomThinking
          // returns null for it too; this keeps the list free of the slot).
          if (block.type === 'thinking' && block.thinking?.trim()) {
            return <RoomThinking key={j} content={block.thinking} foldKey={blockKey(i, j)}
              searchUnit={searchUnitId(blockKey(i, j), 'thinking')} />
          }
          if (block.type === 'text' && block.text) {
            return <RoomProse key={j} content={block.text} searchUnit={searchUnitId(blockKey(i, j), 'text')} />
          }
          if (block.type === 'tool_use') return <OperationAt key={j} msg={msg} i={i} j={j} ctx={ctx} />
          return null
        })}
      </div>
    )
  }

  // --- User messages ---
  if (msg.type === 'user' && 'message' in msg) {
    const um = msg as UserMessage
    // Phase E: the images this line carried (the reducer's side field; a
    // subagent's frame never has any). They go under the human's plain text
    // line, or on a line of their own when there is none to hang them on —
    // an image-only message (Review Focus 4) or a slash command.
    const atts = fromSubagent ? undefined : attachmentsOf(msg)
    const attsAt = atts
      ? um.message.content.findIndex((b) => b.type === 'text' && !!b.text && b.text !== INTERRUPT_TEXT && !b.text.startsWith('/'))
      : -1
    return (
      <div>
        {um.message.content.map((block, j) => {
          if (block.type === 'tool_result') return <OperationAt key={j} msg={msg} i={i} j={j} ctx={ctx} />

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

            // The prompt an agent wrote for its subagent: not the human's
            // line, whatever its first character is.
            const anchor = searchUnitId(blockKey(i, j), 'text')
            if (fromSubagent) return <RoomSubagentLine key={j} text={block.text} searchUnit={anchor} />

            // A slash command: the human's line, told apart by its icon and face.
            if (block.text.startsWith('/')) {
              return (
                <div key={j} data-testid="room-command"
                  className="flex items-center gap-1.5 text-[13px] text-status-warning font-mono">
                  <TerminalWindow size={14} weight="bold" />
                  <span data-search-unit={anchor}>{block.text}</span>
                </div>
              )
            }

            return <RoomUserLine key={j} text={block.text} searchUnit={anchor}
              attachments={atts && j === attsAt ? <AttachmentThumbs items={atts} /> : undefined} />
          }

          return null
        })}
        {atts && attsAt < 0 && <RoomUserLine text="" attachments={<AttachmentThumbs items={atts} />} />}
      </div>
    )
  }

  return null
}
