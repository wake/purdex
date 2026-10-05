// spa/src/components/room/render-message.tsx — the one way a durable message
// of the room transcript is drawn. Out of RoomTranscript so a subagent's
// nested rail (SubagentBlock, spec §4.5) can render the child's own messages
// with the very same renderer, without importing the transcript around it.
import type { StreamMessage } from '../../lib/nex/message-types'
import type { ToolActivity } from '../../lib/nex/tool-activity'
import type { OperationIndex } from '../../lib/nex/operations'
import type { WorkerTask } from '../../lib/nex/types'
import type { MessageIdOf } from '../../lib/nex/message-keys'
import { rowKey } from '../../lib/nex/message-keys'
import MessageRow from './MessageRow'

/** What every message of one transcript shares. */
export interface RenderCtx {
  /** The whole list `index` was built from; a Task's children are read from it by index. */
  messages: StreamMessage[]
  index: OperationIndex
  tools?: Record<string, ToolActivity>
  now?: number
  /** Stable per pane; a row's key is `${keyPrefix}-${i}`. */
  keyPrefix: string
  /** 0 at the top level; each subagent rail adds one. */
  depth: number
  /** R4 T3.3: subagent task rows by their Task call's tool_use_id (`subagentTasksByToolUse`). */
  subagentTasks?: ReadonlyMap<string, WorkerTask>
  /**
   * How this list names its messages in keys (`lib/nex/message-keys`). Absent
   * = by position (the live list). The prelude passes its stable ids.
   */
  idOf?: MessageIdOf
}

/**
 * The message at position `i` of `ctx.messages`. `preludePos`: a prelude
 * row's entry pos, put on the row's root (scroll anchor, #1534).
 */
export function renderMessage(msg: StreamMessage, i: number, ctx: RenderCtx, preludePos?: string) {
  return <MessageRow key={rowKey(ctx, i)} msg={msg} i={i} ctx={ctx} preludePos={preludePos} />
}
