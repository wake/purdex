// spa/src/components/chat/ChatPartialGroup.tsx — chat's in-flight assistant
// group (R2 plan T1.3b). Same input as PartialMessageGroup, chat's renderers:
//
// - text → the typewriter (`RoomProse streaming`) inside an agent bubble;
// - thinking → nothing: chat never shows a thought, streaming or not
//   (spec §5). The pane keeps its dots on meanwhile (partialHasVisibleText);
// - tool_use → nothing in R2-A; R2-B counts it in the turn's tools line.
import { useMemo } from 'react'
import { isPartialBlockVisible, type PartialAssembly, type PartialBlock } from '../../lib/nex/partial'
import RoomProse from '../room/RoomProse'
import ChatBubble from './ChatBubble'

/**
 * The React key of a streaming block, message-id scoped so a new message's
 * block is a new element. Same rule as PartialMessageGroup's `partialKey`
 * (components/PartialMessageGroup.tsx), which is not exported; see its
 * comment for why a null id gets the `orphan` namespace.
 */
function partialKey(partial: PartialAssembly, block: PartialBlock): string {
  const scope = partial.messageId === null ? 'orphan' : `msg:${partial.messageId}`
  return `partial:${scope}#${block.index}`
}

/** Ascending index, text with something to show — exactly what partialHasVisibleText counts. */
function textBlocks(partial: PartialAssembly): PartialBlock[] {
  return Object.values(partial.blocks)
    .filter((b) => b.type === 'text' && isPartialBlockVisible(b))
    .sort((a, b) => a.index - b.index)
}

export default function ChatPartialGroup({ partial }: { partial: PartialAssembly }) {
  const blocks = useMemo(() => textBlocks(partial), [partial])
  return (
    <div data-testid="chat-partial-group" className="space-y-3">
      {blocks.map((block) => (
        <ChatBubble key={partialKey(partial, block)} side="agent">
          <RoomProse content={block.text} streaming />
        </ChatBubble>
      ))}
    </div>
  )
}
