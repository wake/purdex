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
 * Pairing is one-to-one and positional. An id-keyed map would pair every
 * call sharing an id with the same result, showing one result twice while
 * the other vanishes; instead each id keeps a FIFO queue of calls waiting
 * for an answer and of results waiting for a caller (a history page can be
 * applied around a live frame, so a result can precede its call). A call
 * with no result is absent from `resultForCall`; a result with no call is
 * absent from `consumedResults` and renders as an orphan.
 */
export function indexOperations(messages: StreamMessage[]): OperationIndex {
  const resultForCall = new Map<BlockKey, OperationResult>()
  const consumedResults = new Set<BlockKey>()
  const waitingCalls = new Map<string, BlockKey[]>()
  const waitingResults = new Map<string, { key: BlockKey; result: OperationResult }[]>()

  const queue = <T>(map: Map<string, T[]>, id: string): T[] => {
    const existing = map.get(id)
    if (existing) return existing
    const created: T[] = []
    map.set(id, created)
    return created
  }

  messages.forEach((message, mi) => {
    blocksOf(message).forEach((block, bi) => {
      if (block.type === 'tool_use') {
        const id = block.id
        if (!id) return
        const pending = waitingResults.get(id)
        const answer = pending?.shift()
        if (answer) {
          resultForCall.set(blockKey(mi, bi), answer.result)
          consumedResults.add(answer.key)
          return
        }
        queue(waitingCalls, id).push(blockKey(mi, bi))
        return
      }
      if (block.type === 'tool_result') {
        const id = block.tool_use_id
        if (!id) return
        const key = blockKey(mi, bi)
        const result: OperationResult = {
          text: toolResultText((block as { content?: unknown }).content),
          isError: block.is_error === true,
        }
        const callKey = waitingCalls.get(id)?.shift()
        if (callKey !== undefined) {
          resultForCall.set(callKey, result)
          consumedResults.add(key)
          return
        }
        queue(waitingResults, id).push({ key, result })
      }
    })
  })

  return { resultForCall, consumedResults }
}
