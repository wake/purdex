// spa/src/components/chat/ChatEditedLine.tsx — an operation whose result
// carries a diff, as one line: `Edited notes.md (+3 −0)` (spec §5, R2 plan
// T2.1). Not counted in the turn's tools line. Expanded, it is the room's
// own diff view, keyed by the operation's fold key so the diff's own
// "show more" is the same memory the room reads.
import { CaretDown, CaretRight, PencilSimple } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import type { ToolActivity } from '../../lib/nex/tool-activity'
import { pathBasename } from '../../lib/nex/tool-summary'
import { searchUnitId } from '../../lib/nex/transcript-search'
import { useFold } from '../room/fold-context'
import ToolDiffView from '../room/ToolDiffView'

export interface ChatEditedLineProps {
  /**
   * The operation's fold key (its block key); the line folds at
   * `${foldKey}:chat-edited`, and the search anchors are named by it.
   */
  foldKey: string
  diff: NonNullable<ToolActivity['diff']>
}

/** Stands in for the file name in the translated label, which is then split around it. */
const FILE_SLOT = '\u0000'

export default function ChatEditedLine({ foldKey, diff }: ChatEditedLineProps) {
  const t = useI18nStore((s) => s.t)
  const [expanded, toggle] = useFold(`${foldKey}:chat-edited`)
  const Caret = expanded ? CaretDown : CaretRight
  // The file name is its own element: search anchors it (`${foldKey}:file`).
  // A translation without `{{file}}` puts it at the end rather than dropping it.
  const label = t('chat.edited', { file: FILE_SLOT, added: diff.added, removed: diff.removed })
  const slot = label.indexOf(FILE_SLOT)
  const before = slot < 0 ? `${label} ` : label.slice(0, slot)
  const after = slot < 0 ? '' : label.slice(slot + FILE_SLOT.length)

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
        <span className="break-all">{before}<span data-search-unit={searchUnitId(foldKey, 'file')}>{pathBasename(diff.path)}</span>{after}</span>
        <Caret size={10} weight="bold" className="shrink-0" aria-hidden="true" />
      </button>
      {expanded && (
        <div data-testid="chat-edited-diff" className="mt-1">
          {/* The full path: the line only names the file. */}
          <ToolDiffView diff={diff} foldKey={foldKey} showPath searchKey={foldKey} />
        </div>
      )}
    </div>
  )
}
