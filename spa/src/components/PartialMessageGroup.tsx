// spa/src/components/PartialMessageGroup.tsx — the trailing in-flight
// assistant group (P-B2 spec §4.4 R1): the partial assembly's blocks in
// ascending index, each with the streaming cursor / spinner. Rendered by
// RoomTranscript inside the last turn, after the durable list and before
// `children`.
import { useMemo } from 'react'
import { useI18nStore } from '../stores/useI18nStore'
import { isPartialBlockVisible, type PartialAssembly, type PartialBlock } from '../lib/nex/partial'
import OperationBlock from './room/OperationBlock'
import RoomProse from './room/RoomProse'
import RoomThinking from './room/RoomThinking'

/** Ascending index, visible blocks only — the same predicate that gates the ThinkingIndicator (R3). */
function visiblePartialBlocks(partial: PartialAssembly): PartialBlock[] {
  return Object.values(partial.blocks).filter(isPartialBlockVisible).sort((a, b) => a.index - b.index)
}

export default function PartialMessageGroup({ partial }: { partial: PartialAssembly }) {
  const t = useI18nStore((s) => s.t)
  const blocks = useMemo(() => visiblePartialBlocks(partial), [partial])

  // The room blocks read the pane's fold memory through FoldContext, which
  // RoomTranscript provides around this group too.
  return (
    <div data-testid="partial-group">
      {blocks.map((block) => {
        switch (block.type) {
          case 'text':
            return <RoomProse key={block.index} content={block.text} streaming />
          case 'thinking':
            return <RoomThinking key={block.index} content={block.thinking} foldKey={`partial-${block.index}`} streaming />
          case 'tool_use':
            return (
              <OperationBlock key={block.index} tool={block.toolName ?? t('execution.tool.unknown')} input={{}}
                activity={{ status: 'streaming', rawInput: block.partialJson }}
                result={null} foldKey={`partial-${block.index}`} />
            )
          default:
            return null
        }
      })}
    </div>
  )
}
