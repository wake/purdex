// spa/src/lib/nex/partial.ts — the in-flight assistant message assembled
// from transient stream frames (P-B2 spec §4.1 T1–T8) and the D1 rule that
// finalizes its blocks from durable `assistant` frames. Pure: no React, no
// store.
import { obj } from './content-blocks'
import type { ExecutionState } from './event-reducer'

export interface PartialBlock {
  index: number
  type: 'text' | 'thinking' | 'tool_use' | 'unknown'
  text: string
  thinking: string
  /** Raw byte prefix of the tool input, never parsed. */
  partialJson: string
  toolId?: string
  toolName?: string
}

export interface PartialAssembly {
  messageId: string | null
  /**
   * Number of blocks of this message already finalized by durable
   * `assistant` frames (Nexen's `assembly.finalized`). Deltas / starts for
   * index < finalized are dropped; D1 finalizes exactly index `finalized`.
   */
  finalized: number
  /** Sparse: only unfinalized indices. */
  blocks: Record<number, PartialBlock>
}

/**
 * Spec §4.4 R1/R3 — the ONE visibility predicate shared by the renderer
 * (PartialMessageGroup renders a block iff this is true) and the state
 * (partialHasVisibleContent gates the ThinkingIndicator). Keeping them on
 * the same function is what guarantees the dots and the typewriter / spinner
 * are never shown together, and never both hidden.
 *
 * - text / thinking: something non-whitespace to put in a bubble.
 * - tool_use: always — a started tool is a spinner row with its name even
 *   before the first input_json_delta.
 * - unknown: never.
 */
export function isPartialBlockVisible(b: PartialBlock): boolean {
  switch (b.type) {
    case 'text':
      return b.text.trim().length > 0
    case 'thinking':
      return b.thinking.trim().length > 0
    case 'tool_use':
      return true
    default:
      return false
  }
}

/**
 * Spec §4.4 R3: some block is visible (see isPartialBlockVisible). Gates the
 * ThinkingIndicator — dots while the model is silent, typewriter / spinner
 * once something renders.
 */
export function partialHasVisibleContent(p: PartialAssembly | null): boolean {
  if (!p) return false
  return Object.values(p.blocks).some(isPartialBlockVisible)
}

/**
 * Spec §4.4 R4: a cheap change key that differs whenever the rendered group
 * would differ, so the auto-scroll effect can follow the typewriter without
 * depending on the assembly's identity. It folds in the structure (message
 * id, finalized counter, and each block's index / type / tool name) as well
 * as the content lengths, so a block that appears with no content yet (a
 * started tool_use) or one finalized away still moves the key. '' when there
 * is no partial.
 */
export function partialVersionOf(p: PartialAssembly | null | undefined): string {
  if (!p) return ''
  const blocks = Object.values(p.blocks)
    .map((b) => `${b.index}:${b.type}:${b.toolName ?? ''}:${b.text.length}:${b.thinking.length}:${b.partialJson.length}`)
    .join('|')
  return `${p.messageId ?? ''}#${p.finalized}#${blocks}`
}

function blockIndex(v: unknown): number | null {
  return typeof v === 'number' && Number.isInteger(v) && v >= 0 ? v : null
}

function blockType(v: unknown): PartialBlock['type'] {
  return v === 'text' || v === 'thinking' || v === 'tool_use' ? v : 'unknown'
}

function newBlock(index: number, type: PartialBlock['type']): PartialBlock {
  return { index, type, text: '', thinking: '', partialJson: '' }
}

/**
 * How many blocks of `messageId` this client has already rendered as durable
 * `assistant` frames (F5: one frame per block). This — not the snapshot's
 * lowest index — seeds `finalized`, because the replay that follows a
 * snapshot re-sends exactly those frames and D1 finalizes by index.
 */
export function finalizedFor(s: ExecutionState, messageId: string): number {
  let n = 0
  for (const m of s.messages) {
    if (m.type !== 'assistant') continue
    const raw = m as unknown as Record<string, unknown>
    if (raw.parent_tool_use_id != null) continue
    const message = obj(raw.message)
    if (message && message.id === messageId) n += 1
  }
  return n
}

function withPartial(s: ExecutionState, partial: PartialAssembly, turnLive = s.turnLive): ExecutionState {
  return { ...s, partial, turnLive }
}

function setBlock(s: ExecutionState, partial: PartialAssembly, block: PartialBlock): ExecutionState {
  return withPartial(s, { ...partial, blocks: { ...partial.blocks, [block.index]: block } })
}

const DELTA_FIELD: Record<string, { type: PartialBlock['type']; key: 'text' | 'thinking' | 'partial_json'; field: 'text' | 'thinking' | 'partialJson' }> = {
  text_delta: { type: 'text', key: 'text', field: 'text' },
  thinking_delta: { type: 'thinking', key: 'thinking', field: 'thinking' },
  input_json_delta: { type: 'tool_use', key: 'partial_json', field: 'partialJson' },
}

function applyStreamEvent(s: ExecutionState, event: Record<string, unknown>): ExecutionState {
  const orphan: PartialAssembly = { messageId: null, finalized: 0, blocks: {} }
  switch (event.type) {
    case 'message_start': {
      const id = obj(event.message)?.id
      if (typeof id !== 'string') return s
      if (s.partial?.messageId === id) return s
      return withPartial(s, { messageId: id, finalized: finalizedFor(s, id), blocks: {} }, true)
    }
    case 'content_block_start': {
      const index = blockIndex(event.index)
      const cb = obj(event.content_block)
      if (index === null || !cb) return s
      const partial = s.partial ?? orphan
      if (index < partial.finalized) return s
      const block = newBlock(index, blockType(cb.type))
      if (block.type === 'tool_use') {
        if (typeof cb.id === 'string') block.toolId = cb.id
        if (typeof cb.name === 'string') block.toolName = cb.name
      }
      return setBlock(s, partial, block)
    }
    case 'content_block_delta': {
      const index = blockIndex(event.index)
      const delta = obj(event.delta)
      if (index === null || !delta) return s
      const spec = typeof delta.type === 'string' ? DELTA_FIELD[delta.type] : undefined
      if (!spec) return s
      const piece = delta[spec.key]
      if (typeof piece !== 'string') return s
      const partial = s.partial ?? orphan
      if (index < partial.finalized) return s
      const prev = partial.blocks[index] ?? newBlock(index, spec.type)
      return setBlock(s, partial, { ...prev, [spec.field]: prev[spec.field] + piece })
    }
    default:
      return s
  }
}

function applySnapshot(s: ExecutionState, payload: Record<string, unknown>): ExecutionState {
  const messageId = payload.message_id
  if (typeof messageId !== 'string' || !Array.isArray(payload.blocks)) return s
  const blocks: Record<number, PartialBlock> = {}
  for (const raw of payload.blocks) {
    const b = obj(raw)
    const index = b ? blockIndex(b.index) : null
    if (!b || index === null) continue
    const text = typeof b.text === 'string' ? b.text : ''
    const thinking = typeof b.thinking === 'string' ? b.thinking : ''
    const partialJson = typeof b.partial_json === 'string' ? b.partial_json : ''
    const inferred: PartialBlock['type'] = thinking ? 'thinking' : partialJson ? 'tool_use' : text ? 'text' : 'unknown'
    const type = b.type === undefined ? inferred : blockType(b.type)
    blocks[index] = { index, type, text, thinking, partialJson }
  }
  return withPartial(s, { messageId, finalized: finalizedFor(s, messageId), blocks }, true)
}

/**
 * Transient frames (no SSE id) → the partial assembly (spec §4.1 T1–T8).
 * Returns `s` itself whenever nothing changes so the store can skip
 * materialising an entry. Never throws on malformed payloads.
 */
export function applyTransientFrame(s: ExecutionState, kind: string, payload: Record<string, unknown>): ExecutionState {
  const p = obj(payload)
  if (!p || p.parent_tool_use_id != null) return s
  if (kind === 'stream_event') {
    const event = obj(p.event)
    return event && typeof event.type === 'string' ? applyStreamEvent(s, event) : s
  }
  if (kind === 'stream_snapshot') return applySnapshot(s, p)
  return s
}

export function finalizeBlock(s: ExecutionState, p: Record<string, unknown>): ExecutionState {
  const partial = s.partial
  if (!partial) return s
  // Only a PRESENT id that disagrees supersedes the assembly; a frame with
  // no message.id (older history, bare payload) is taken as the next block
  // of whatever is streaming, like a match.
  const id = obj(p.message)?.id
  if (partial.messageId !== null && typeof id === 'string' && id !== partial.messageId) return { ...s, partial: null }
  // By counter, not lowest index: after a snapshot {blocks:[1]} the replayed
  // assistant 0 must be a no-op that leaves block 1 streaming (spec D1).
  const { [partial.finalized]: _finalized, ...blocks } = partial.blocks
  return { ...s, partial: { ...partial, finalized: partial.finalized + 1, blocks } }
}
