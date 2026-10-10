// spa/src/components/deck/footer-context.ts — what a session view's footer (the input and whatever sits with it) is handed.
// The deck and the chat build it the same way from the conversation's document.
import type { ConversationDoc } from '../../lib/conversations/model'
import { openAsks } from '../../lib/conversations/asks'
import type { Capabilities, ConversationApproval, ConversationItem } from '../../lib/conversations/types'

/** What the footer needs to draw an input for this conversation. */
export interface DeckFooterContext {
  paneKey: string
  hostId: string
  sessionId: string
  capabilities: Capabilities | undefined
  items: readonly ConversationItem[]
  /** The header status is idle. */
  idle: boolean
  /** The header status: running | waiting | idle | error | ended | unknown (the status row's state cell). */
  status: string
  /** The model and effort the conversation itself reports (the status row's fallback when no statusLine snapshot has them). */
  usage: { model?: string; effort?: string } | undefined
  onSwitchToTerminal: () => void
  /** The conversation WebSocket's open approvals (the dock draws the `hook_ask` ones). */
  approvals: readonly ConversationApproval[]
  /** A question is waiting for the person: the input says so in its placeholder. */
  asking: boolean
}

/** `items` is the caller's memo of the document's items (a new array each render would re-run the input's reconcile). */
export function footerContext(paneId: string, hostId: string, sessionId: string, doc: ConversationDoc, items: readonly ConversationItem[], onSwitchToTerminal: () => void): DeckFooterContext {
  return {
    paneKey: paneId, hostId, sessionId,
    capabilities: doc.capabilities ?? undefined,
    items, idle: doc.header?.status === 'idle', status: doc.header?.status ?? 'unknown', usage: doc.header?.usage, onSwitchToTerminal,
    approvals: doc.approvals, asking: openAsks(doc.approvals).length > 0,
  }
}
