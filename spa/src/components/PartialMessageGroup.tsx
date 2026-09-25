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

/**
 * The fold key (and React key) of a streaming block. The pane's fold memory
 * outlives the partial, so the block index alone would hand message A's
 * expansion to message B's block at the same index: the message id scopes it.
 *
 * A null id is an orphan assembly — deltas that arrived without a
 * `message_start` (a stream joined mid-message). It gets its own `orphan`
 * namespace, which no real id can reach because those all sit under `msg:`.
 * That is enough: every `message_start` carries an id and replaces the orphan,
 * so an orphan can only follow an orphan when two id-less messages stream back
 * to back, and at worst that shares one expansion bit between them.
 */
function partialKey(partial: PartialAssembly, block: PartialBlock): string {
  const scope = partial.messageId === null ? 'orphan' : `msg:${partial.messageId}`
  return `partial:${scope}#${block.index}`
}

export default function PartialMessageGroup({ partial }: { partial: PartialAssembly }) {
  const t = useI18nStore((s) => s.t)
  const blocks = useMemo(() => visiblePartialBlocks(partial), [partial])

  // The room blocks read the pane's fold memory through FoldContext, which
  // RoomTranscript provides around this group too.
  return (
    <div data-testid="partial-group">
      {blocks.map((block) => {
        // The React key carries the message too, so a new message's block is a
        // new element rather than the previous message's one re-used.
        const key = partialKey(partial, block)
        switch (block.type) {
          case 'text':
            return <RoomProse key={key} content={block.text} streaming />
          case 'thinking':
            return <RoomThinking key={key} content={block.thinking} foldKey={key} streaming />
          case 'tool_use':
            return (
              <OperationBlock key={key} tool={block.toolName ?? t('execution.tool.unknown')} input={{}}
                activity={{ status: 'streaming', rawInput: block.partialJson }}
                result={null} foldKey={key} />
            )
          default:
            return null
        }
      })}
    </div>
  )
}
