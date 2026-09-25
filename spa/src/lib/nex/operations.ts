// spa/src/lib/nex/operations.ts — pairs a tool_use block with the
// tool_result that answers it (spec §4.2: one call is one block), across the
// message boundary that separates them. Pure: no React, no store.
import type { ContentBlock, StreamMessage } from './message-types'

export interface OperationResult {
  /** The result body as text; a structured content array is flattened to its text blocks. */
  text: string
  isError: boolean
}

/** `${messageIndex}:${blockIndex}` — a block's position, which is unique even when a tool_use_id is not. */
export type BlockKey = string
export const blockKey = (m: number, b: number): BlockKey => `${m}:${b}`

export interface OperationIndex {
  /** The call block's own position → the result that answers THAT call. */
  resultForCall: Map<BlockKey, OperationResult>
  /** Result blocks already shown by a call; the renderer skips exactly these. */
  consumedResults: Set<BlockKey>
}

/** `string` → itself; `[{type:'text',text}]` → the texts joined by '\n'; anything else → JSON. */
export function toolResultText(content: unknown): string {
  if (content == null) return ''
  if (typeof content === 'string') return content
  if (Array.isArray(content)) {
    const texts: string[] = []
    const allText = content.every((item) => {
      if (!item || typeof item !== 'object') return false
      const block = item as { type?: unknown; text?: unknown }
      if (block.type !== 'text' || typeof block.text !== 'string') return false
      texts.push(block.text)
      return true
    })
    // A subagent hand-back is a content array, and JSON.stringify of it is
    // exactly the raw-JSON leak #1263 is about — but only a text-block array
    // can be flattened without losing what the other shapes carry.
    if (allText) return texts.join('\n')
  }
  return JSON.stringify(content) ?? ''
}

function blocksOf(message: StreamMessage): ContentBlock[] {
  const content = (message as { message?: { content?: unknown } }).message?.content
  return Array.isArray(content) ? (content as ContentBlock[]) : []
}

/**
 * Pairing is one-to-one, positional and **forward-only**. An id-keyed map
 * would pair every call sharing an id with the same result, showing one
 * result twice while the other vanishes; instead each id keeps a FIFO queue
 * of calls still waiting for an answer, and each `tool_result` goes to the
 * oldest unanswered call with that id.
 *
 * Nothing waits the other way. History is paged in full from `after=0`
 * before the SSE opens (`useExecutionSubscription.ts:173-192`) and
 * `applyDurableEvent` only accepts a strictly increasing seq, so a
 * `tool_result` is never in this array ahead of its own `tool_use`. A result
 * that does precede a same-id call is the tail of a page whose call is off
 * the list; letting the later call claim it hands that call a stale body and
 * floats its real answer off as an orphan. So a result that arrives with no
 * waiting call is an orphan, and stays one.
 *
 * Queues are also per **scope**: a subagent's frames carry the
 * `parent_tool_use_id` of the Task call that spawned them, and a result from
 * inside a subagent may name the same `tool_use_id` as a main-flow call still
 * waiting for its own answer. A call and a result pair only when their
 * messages share a scope (null/absent = the main flow), so a subagent result
 * can never eat the main flow's call; with no same-scope call it is an orphan.
 *
 * A call with no result is absent from `resultForCall`; an orphan result is
 * absent from `consumedResults` and renders on its own.
 */
export function indexOperations(messages: StreamMessage[]): OperationIndex {
  const resultForCall = new Map<BlockKey, OperationResult>()
  const consumedResults = new Set<BlockKey>()
  // scope (parent_tool_use_id, '' for the main flow) → tool_use_id → FIFO of calls.
  const waitingCalls = new Map<string, Map<string, BlockKey[]>>()

  messages.forEach((message, mi) => {
    const scope = (message as { parent_tool_use_id?: string | null }).parent_tool_use_id ?? ''
    const waiting = waitingCalls.get(scope) ?? new Map<string, BlockKey[]>()
    waitingCalls.set(scope, waiting)
    blocksOf(message).forEach((block, bi) => {
      if (block.type === 'tool_use') {
        const id = block.id
        if (!id) return
        const queue = waiting.get(id)
        if (queue) queue.push(blockKey(mi, bi))
        else waiting.set(id, [blockKey(mi, bi)])
        return
      }
      if (block.type === 'tool_result') {
        const id = block.tool_use_id
        if (!id) return
        const callKey = waiting.get(id)?.shift()
        if (callKey === undefined) return
        resultForCall.set(callKey, {
          text: toolResultText((block as { content?: unknown }).content),
          isError: block.is_error === true,
        })
        consumedResults.add(blockKey(mi, bi))
      }
    })
  })

  return { resultForCall, consumedResults }
}
