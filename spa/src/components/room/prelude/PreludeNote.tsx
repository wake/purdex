// spa/src/components/room/prelude/PreludeNote.tsx — a non-message record of
// the prelude (spec §4.3 `prelude.note`): a slash command's or `!` command's
// output, the `!` input itself, a background-task notice, a peer message.
import { TerminalWindow } from '@phosphor-icons/react'
import { useMemo, type ReactNode } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { foldPlan, utf8Length } from '../../../lib/nex/fold'
import { searchUnitId } from '../../../lib/nex/transcript-search'
import { FoldedOutput } from '../FoldedOutput'
import { useFold } from '../fold-context'
import { TruncatedHint } from './Placeholders'

export interface PreludeNoteProps {
  /** `p<pos>` — fold key `${id}:note`, search anchor `${id}:note:text`. */
  id: string
  source: string
  text: string
  truncated: boolean
  totalBytes: number | null
  /** `bash_output` only: 'stdout' | 'stderr' (spec §4.3). */
  stream: string | null
}

export default function PreludeNote({ id, source, text, truncated, totalBytes, stream }: PreludeNoteProps) {
  const t = useI18nStore((s) => s.t)
  const [expanded, toggle] = useFold(`${id}:note`)
  const plan = useMemo(() => foldPlan({ text }), [text])
  const anchor = searchUnitId(`${id}:note`, 'text')
  const hint: ReactNode = truncated ? <TruncatedHint shown={utf8Length(text)} total={totalBytes ?? 0} /> : null
  if (source === 'bash_input') {
    return (
      <div data-testid="prelude-bash-input">
        <div className="flex items-center gap-1.5 text-[13px] text-status-warning font-mono">
          <TerminalWindow size={14} weight="bold" />
          <span data-search-unit={anchor}>! {text}</span>
        </div>
        {hint}
      </div>
    )
  }
  if (source === 'task_notification') {
    return (
      <div data-testid="prelude-task" className="text-xs text-text-muted">
        <span>{t('worker.prelude.note_task')}: </span><span data-search-unit={anchor}>{text}</span>
        {hint}
      </div>
    )
  }
  const label = source === 'peer_message' ? t('worker.prelude.note_peer') : null
  return (
    <div data-testid={`prelude-note-${source}`} className="space-y-1">
      {label && <div className="text-xs text-text-muted">{label}</div>}
      <FoldedOutput text={text} plan={plan} expanded={expanded} onToggle={toggle} searchUnit={anchor}
        tone={stream === 'stderr' ? 'error' : 'normal'} />
      {hint}
    </div>
  )
}
