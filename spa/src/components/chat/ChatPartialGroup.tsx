// spa/src/components/chat/ChatPartialGroup.tsx — chat's in-flight assistant
// group (R2 plan T1.3b). Same input as PartialMessageGroup, chat's renderers:
//
// - text → the typewriter (`RoomProse streaming`) inside an agent bubble;
// - thinking → nothing: chat never shows a thought, streaming or not
//   (spec §5). The pane keeps its dots on meanwhile (partialHasChatContent);
// - tool_use → nothing here: ChatTranscript counts it in the running turn's
//   tools line ("Using N tools…", R2-B) and lists it when that line opens.
import { useMemo } from 'react'
import { isPartialBlockVisible, partialBlockKey, type PartialAssembly, type PartialBlock } from '../../lib/nex/partial'
import RoomProse from '../room/RoomProse'
import ChatBubble from './ChatBubble'

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
        // Message-id scoped, so a new message's block is a new element.
        <ChatBubble key={partialBlockKey(partial, block)} side="agent">
          <RoomProse content={block.text} streaming />
        </ChatBubble>
      ))}
    </div>
  )
}
