// spa/src/components/chat/ChatEditedLine.tsx — an operation whose result
// carries a diff, as one line: `Edited notes.md (+3 −0)` (spec §5, R2 plan
// T2.1). Not counted in the turn's tools line. Expanded, it is the room's
// own diff view, keyed by the operation's fold key so the diff's own
// "show more" is the same memory the room reads.
import { CaretDown, CaretRight, PencilSimple } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import type { ToolActivity } from '../../lib/nex/tool-activity'
import { useFold } from '../room/fold-context'
import ToolDiffView from '../room/ToolDiffView'

export interface ChatEditedLineProps {
  /** The operation's fold key (its block key); the line folds at `${foldKey}:chat-edited`. */
  foldKey: string
  diff: NonNullable<ToolActivity['diff']>
}

/** The last path segment; the whole path when it has none (or ends in a separator). */
function basename(path: string): string {
  const last = path.split(/[\\/]/).pop()
  return last ? last : path
}

export default function ChatEditedLine({ foldKey, diff }: ChatEditedLineProps) {
  const t = useI18nStore((s) => s.t)
  const [expanded, toggle] = useFold(`${foldKey}:chat-edited`)
  const Caret = expanded ? CaretDown : CaretRight

  return (
    <div>
      <button
        type="button"
        data-testid="chat-edited-line"
        aria-expanded={expanded}
        title={diff.path}
        className="flex items-center gap-1.5 text-xs text-text-muted hover:text-text-primary cursor-pointer text-left min-w-0 max-w-full"
        onClick={toggle}
      >
        <PencilSimple size={12} className="shrink-0" aria-hidden="true" />
        <span className="break-all">{t('chat.edited', { file: basename(diff.path), added: diff.added, removed: diff.removed })}</span>
        <Caret size={10} weight="bold" className="shrink-0" aria-hidden="true" />
      </button>
      {expanded && (
        <div data-testid="chat-edited-diff" className="mt-1">
          {/* The full path: the line only names the file. */}
          <ToolDiffView diff={diff} foldKey={foldKey} showPath />
        </div>
      )}
    </div>
  )
}
