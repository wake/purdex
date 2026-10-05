// spa/src/components/room/prelude/placeholder-utils.ts — the non-component
// helpers behind Placeholders.tsx (kept apart: react-refresh wants component
// files to export components only).
import type { ContentBlock } from '../../../lib/nex/message-types'
import { utf8Length } from '../../../lib/nex/fold'
import { toolResultText } from '../../../lib/nex/operations'

/** An image / document block whose data the daemon omitted. */
export function isOmittedMedia(block: ContentBlock): boolean {
  return (block.type === 'image' || block.type === 'document') && block.source?.type === 'omitted'
}

/** How many bytes of a cut block are shown — what the daemon kept (the whole block's, for a part `splitPasted` made). */
export function blockShownBytes(block: ContentBlock): number {
  if (block.shown_bytes !== undefined) return block.shown_bytes
  switch (block.type) {
    case 'thinking': return utf8Length(block.thinking ?? '')
    case 'tool_use': return utf8Length(JSON.stringify(block.input ?? {}))
    case 'tool_result': return utf8Length(toolResultText(block.content))
    default: return utf8Length(block.text ?? '')
  }
}
