// spa/src/components/room/FoldedOutput.tsx — the fold affordance: a preview,
// a button that says what expanding will reveal, and the whole body once it
// is open. Spec §4.2's rule lives in `foldPlan`; this only draws it.
//
// The expansion is NOT held here. A block unmounts whenever its turn is
// re-keyed or virtualised away, and a `useState` in this component would take
// the user's "open" with it (spec §3.2). `expanded` and `onToggle` are props,
// fed by the pane-level memory in `fold-context`.
import type { ReactNode } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import type { FoldPlan } from '../../lib/nex/fold'

export interface FoldedOutputProps {
  text: string
  plan: FoldPlan
  expanded: boolean
  onToggle: () => void
  /** 'error' tints the body text. */
  tone?: 'normal' | 'error'
  /**
   * Drawn inline at the end of the text, and only while the text's end is on
   * screen (shown whole, or expanded) — the typewriter cursor of a streaming
   * thought. A folded preview stops mid-body, so it gets none.
   */
  trailing?: ReactNode
}

const BODY_CLASS = 'text-xs whitespace-pre-wrap break-all overflow-auto max-h-96'
const BUTTON_CLASS =
  'mt-1 text-xs text-text-muted hover:text-text-primary cursor-pointer text-left'

export function FoldedOutput({ text, plan, expanded, onToggle, tone = 'normal', trailing }: FoldedOutputProps) {
  const t = useI18nStore((s) => s.t)
  const bodyClass = `${BODY_CLASS} ${tone === 'error' ? 'text-status-error' : 'text-text-secondary'}`

  // A fact about the payload, not about the fold: the daemon cut the output
  // whether or not anything here is foldable. A truncated body the preview
  // shows whole draws no button at all, so the note cannot live behind one.
  const truncationNote = plan.daemonTruncated ? (
    <span data-testid="fold-daemon-truncated" className="block text-xs text-text-muted">
      {t('room.fold.daemon_truncated')}
    </span>
  ) : null

  // Not collapsible: the body is all there is, so there is nothing for a
  // button to promise. Drawing one anyway would open and close the same text.
  if (!plan.collapsible) {
    return (
      <div>
        <pre data-testid="fold-body" className={bodyClass}>{text}{trailing}</pre>
        {truncationNote}
      </div>
    )
  }

  if (expanded) {
    return (
      <div>
        <pre data-testid="fold-body" className={bodyClass}>{text}{trailing}</pre>
        {truncationNote}
        <button type="button" data-testid="fold-less" className={BUTTON_CLASS} onClick={onToggle}>
          {t('room.fold.less')}
        </button>
      </div>
    )
  }

  // `hiddenLines === 0` with a clamp means the only thing folded away is the
  // tail of a line, which "+0 lines" would misreport as nothing at all.
  const label =
    plan.hiddenLines === 0 && plan.clamped
      ? t('room.fold.show_all')
      : t('room.fold.more', { n: plan.hiddenLines })

  return (
    <div>
      <pre data-testid="fold-body" className={bodyClass}>{plan.previewLines.join('\n')}</pre>
      {truncationNote}
      <button type="button" data-testid="fold-more" className={BUTTON_CLASS} onClick={onToggle}>
        {label}
      </button>
    </div>
  )
}
