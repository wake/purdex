// spa/src/components/room/prelude/PreludeNote.tsx — a non-message record of
// the prelude (spec §4.3 `prelude.note`): a slash command's or `!` command's
// output, the `!` input itself, a background-task notice, a peer message.
import { TerminalWindow } from '@phosphor-icons/react'
import { useMemo, type ReactNode } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { foldPlan, utf8Length } from '../../../lib/nex/fold'
import { searchUnitId } from '../../../lib/nex/transcript-search'
import { SCROLL_ANCHOR_CLASS } from '../../../lib/nex/transcript-scroll-memory'
import { FoldedOutput } from '../FoldedOutput'
import { useFold } from '../fold-context'
import RoomProse from '../RoomProse'
import { TruncatedHint } from './Placeholders'

export interface PreludeNoteProps {
  /** `p<pos>` — fold key `${id}:note`, search anchor `${id}:note:text`. */
  id: string
  /** The entry's pos, on the root as `data-prelude-pos` (scroll anchor, #1534). */
  pos: string
  source: string
  text: string
  truncated: boolean
  totalBytes: number | null
  /** `bash_output` only: 'stdout' | 'stderr' (spec §4.3). */
  stream: string | null
}

export default function PreludeNote({ id, pos, source, text, truncated, totalBytes, stream }: PreludeNoteProps) {
  const t = useI18nStore((s) => s.t)
  const [expanded, toggle] = useFold(`${id}:note`)
  const plan = useMemo(() => foldPlan({ text }), [text])
  // Search skips an empty unit, so an empty note draws no anchor either.
  const anchor = text ? searchUnitId(`${id}:note`, 'text') : undefined
  const hint: ReactNode = truncated ? <TruncatedHint shown={utf8Length(text)} total={totalBytes} /> : null
  if (source === 'bash_input') {
    return (
      <div data-testid="prelude-bash-input" data-prelude-pos={pos} className={SCROLL_ANCHOR_CLASS}>
        <div className="flex items-center gap-1.5 text-[13px] text-status-warning font-mono">
          <TerminalWindow size={14} weight="bold" />
          <span>! </span><span data-search-unit={anchor}>{text}</span>
        </div>
        {hint}
      </div>
    )
  }
  if (source === 'task_notification') {
    return (
      <div data-testid="prelude-task" data-prelude-pos={pos} className={`text-xs text-text-muted ${SCROLL_ANCHOR_CLASS}`}>
        <span>{t('worker.prelude.note_task')}: </span><span data-search-unit={anchor}>{text}</span>
        {hint}
      </div>
    )
  }
  if (source === 'peer_message') {
    // Spec §5.3: a labelled block whose body is drawn like agent prose
    // (markdown, never folded).
    return (
      <div data-testid="prelude-note-peer_message" data-prelude-pos={pos} className={`space-y-1 ${SCROLL_ANCHOR_CLASS}`}>
        <div className="text-xs text-text-muted">{t('worker.prelude.note_peer')}</div>
        <RoomProse content={text} searchUnit={anchor} />
        {hint}
      </div>
    )
  }
  // command_output / bash_output fold like tool output; any other source
  // is unknown to this build and drawn muted.
  const known = source === 'command_output' || source === 'bash_output'
  return (
    <div data-testid={`prelude-note-${source}`} data-prelude-pos={pos} className={SCROLL_ANCHOR_CLASS}>
      <FoldedOutput text={text} plan={plan} expanded={expanded} onToggle={toggle} searchUnit={anchor}
        tone={stream === 'stderr' ? 'error' : known ? 'normal' : 'muted'} />
      {hint}
    </div>
  )
}
