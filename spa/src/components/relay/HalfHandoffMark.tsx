// spa/src/components/relay/HalfHandoffMark.tsx — 「已寫好接力檔」: a quiet mark on a session row whose relay wrote its handoff
// file and did not clear the session (relay v2 contract 5). It sits beside the row's text and does not compete with the
// status light: muted, small, no colour of its own (theme tokens only). The tooltip says where the file is and when it was
// written. `chip` has the words (the team panel's row, with the host chip and the paused mark); `icon` is the icon alone, with
// the words in its aria-label, for the places with no room (a tab, a lead row, a bead). Renders nothing without a handoff.
import { NotePencil } from '@phosphor-icons/react'
import { useCoarseNow } from '../../hooks/useCoarseNow'
import type { FreshHandoffView } from '../../hooks/useFreshHandoff'
import { formatHandoffTime } from '../../lib/relay/handoff-time'
import { useI18nStore } from '../../stores/useI18nStore'

interface Props {
  handoff: FreshHandoffView | null
  variant: 'chip' | 'icon'
  /** The icon's size in px (default 12, the size of the tab's other small icons). */
  size?: number
}

/** Nothing without a handoff, and then no clock subscription: only a drawn mark follows the minute (its tooltip names the day). */
export function HalfHandoffMark({ handoff, variant, size = 12 }: Props) {
  if (!handoff) return null
  return <Mark handoff={handoff} variant={variant} size={size} />
}

function Mark({ handoff, variant, size }: { handoff: FreshHandoffView; variant: 'chip' | 'icon'; size: number }) {
  const t = useI18nStore((s) => s.t)
  const now = useCoarseNow()
  const label = t('relay.half.label')
  const time = formatHandoffTime(handoff.writtenAt, now)
  const tip = time ? t('relay.half.tooltip', { path: handoff.path, time }) : t('relay.half.tooltip_path', { path: handoff.path })
  if (variant === 'chip') {
    return (
      <span data-testid="half-handoff-mark" data-variant="chip" title={tip} className="text-[9.5px] px-1 rounded bg-surface-hover text-text-secondary flex-shrink-0 whitespace-nowrap">
        {label}
      </span>
    )
  }
  return (
    <span data-testid="half-handoff-mark" data-variant="icon" role="img" aria-label={label} title={tip} className="inline-flex items-center flex-shrink-0 text-text-muted">
      <NotePencil size={size} aria-hidden="true" />
    </span>
  )
}
