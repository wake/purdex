// spa/src/components/room/RoomThinking.tsx — a thinking block in the room
// (spec §4.3). Thinking is optional: most blocks arrive with no text, and one
// with no text draws nothing — not an empty fold. The fact that the agent
// thought is carried by the live typewriter.
//
// With text, it folds by the one rule every block follows (`foldPlan`, spec
// §4.2): a two-line thought is simply shown, a long one folds to six or three
// lines. The expansion lives in the pane's fold memory under
// `${foldKey}:thinking`, so it survives a remount and answers the turn's
// expand-all (codex plan review #4) — ThinkingBlock's local `useState` was the
// third ad-hoc fold spec §3.2 lists.
import { Brain } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { foldPlan } from '../../lib/nex/fold'
import StreamCursor from '../StreamCursor'
import { useFold } from './fold-context'
import { FoldedOutput } from './FoldedOutput'

interface Props {
  content: string
  /** The pane-level fold key of the block this thought belongs to. */
  foldKey: string
  /** The P-B2 typewriter: a cursor after the label, and at the end of the text while it is on screen. */
  streaming?: boolean
}

function wordCount(text: string): number {
  return text.split(/\s+/).filter(Boolean).length
}

export default function RoomThinking({ content, foldKey, streaming }: Props) {
  if (content.trim() === '') return null
  return <Thought content={content} foldKey={foldKey} streaming={streaming} />
}

// Split so the empty case returns before the fold registers itself: an empty
// thought is not a foldable thing, and expand-all should not count it.
function Thought({ content, foldKey, streaming }: Props) {
  const t = useI18nStore((s) => s.t)
  const [expanded, toggle] = useFold(`${foldKey}:thinking`)
  const plan = foldPlan({ text: content })

  return (
    <div data-testid="room-thinking" className="border-l-2 border-border-default pl-2.5 my-1">
      <div data-testid="thinking-header" className="flex items-center gap-2 py-1 text-xs text-text-muted">
        <Brain size={14} />
        <span data-testid="thinking-label">{t('room.thinking', { words: wordCount(content) })}</span>
        {streaming && <StreamCursor />}
      </div>
      <div data-testid="thinking-content" className="pb-1 font-mono">
        <FoldedOutput text={content} plan={plan} expanded={expanded} onToggle={toggle}
          trailing={streaming ? <StreamCursor /> : undefined} />
      </div>
    </div>
  )
}
