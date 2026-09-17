// spa/src/components/PartialMessageGroup.tsx — the trailing in-flight
// assistant group (P-B2 spec §4.4 R1): the partial assembly's blocks in
// ascending index, each with the streaming cursor / spinner. Rendered by
// ConversationMessages after the durable list and before `children`; Stream
// mode never passes a partial and never mounts this.
import { useMemo } from 'react'
import { useI18nStore } from '../stores/useI18nStore'
import { isPartialBlockVisible, type PartialAssembly, type PartialBlock } from '../lib/nex/partial'
import MessageBubble from './MessageBubble'
import ThinkingBlock from './ThinkingBlock'
import ToolCallBlock from './ToolCallBlock'

/** Ascending index, visible blocks only — the same predicate that gates the ThinkingIndicator (R3). */
function visiblePartialBlocks(partial: PartialAssembly): PartialBlock[] {
  return Object.values(partial.blocks).filter(isPartialBlockVisible).sort((a, b) => a.index - b.index)
}

export default function PartialMessageGroup({ partial }: { partial: PartialAssembly }) {
  const t = useI18nStore((s) => s.t)
  const blocks = useMemo(() => visiblePartialBlocks(partial), [partial])

  return (
    <div data-testid="partial-group">
      {blocks.map((block) => {
        switch (block.type) {
          case 'text':
            return <MessageBubble key={block.index} role="assistant" content={block.text} streaming />
          case 'thinking':
            return <ThinkingBlock key={block.index} content={block.thinking} streaming />
          case 'tool_use':
            return (
              <ToolCallBlock key={block.index} tool={block.toolName ?? t('execution.tool.unknown')} input={{}}
                status="streaming" rawInput={block.partialJson} />
            )
          default:
            return null
        }
      })}
    </div>
  )
}
