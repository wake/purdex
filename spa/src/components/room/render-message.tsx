// spa/src/components/room/render-message.tsx — the one way a durable message
// of the room transcript is drawn. Out of RoomTranscript so a subagent's
// nested rail (SubagentBlock, spec §4.5) can render the child's own messages
// with the very same renderer, without importing the transcript around it.
import type { StreamMessage } from '../../lib/nex/message-types'
import type { ToolActivity } from '../../lib/nex/tool-activity'
import type { OperationIndex } from '../../lib/nex/operations'
import MessageRow from './MessageRow'

/** What every message of one transcript shares. */
export interface RenderCtx {
  index: OperationIndex
  tools?: Record<string, ToolActivity>
  now?: number
  /** Stable per pane; a row's key is `${keyPrefix}-${i}`. */
  keyPrefix: string
}

/** The message at position `i` of the list `ctx.index` was built from. */
export function renderMessage(msg: StreamMessage, i: number, ctx: RenderCtx) {
  return <MessageRow key={`${ctx.keyPrefix}-${i}`} msg={msg} i={i} index={ctx.index} tools={ctx.tools} now={ctx.now} />
}
