// spa/src/components/room/prelude/PastedBlock.tsx — text pasted in the
// terminal (worker prelude U3, spec §5.3 "Pasted text"): a muted title with
// its line count over the body, folded like tool output. Room and chat draw
// the same block. It never draws a truncation hint: a cut paste's one hint
// comes from the caller's usual decoration, so the fold plan is not told the
// block was cut either (FoldedOutput would add a second note).
import { useMemo } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { foldPlan } from '../../../lib/nex/fold'
import { FoldedOutput } from '../FoldedOutput'
import { useFold } from '../fold-context'

export interface PastedBlockProps {
  text: string
  lines: number
  /** The closing tag was cut off: the title says N+. */
  cut: boolean
  /** `${keyAt(ctx, i, j)}:paste` — search's reveal for this block. */
  foldKey: string
  /** `searchUnitId(keyAt(ctx, i, j), 'text')`. */
  searchUnit: string
}

export default function PastedBlock({ text, lines, cut, foldKey, searchUnit }: PastedBlockProps) {
  const t = useI18nStore((s) => s.t)
  const [expanded, toggle] = useFold(foldKey)
  const plan = useMemo(() => foldPlan({ text }), [text])
  // The i18n store has no plural rules: `_one` / `_other` are picked here (ChatToolsLine).
  const title = cut
    ? t('worker.prelude.pasted_cut', { count: lines })
    : t(`worker.prelude.pasted_${lines === 1 ? 'one' : 'other'}`, { count: lines })
  return (
    <div data-testid="prelude-pasted">
      <div data-testid="prelude-pasted-title" className="text-xs text-text-muted">{title}</div>
      {/* Search skips an empty unit, so an empty body draws no anchor either. */}
      <FoldedOutput text={text} plan={plan} expanded={expanded} onToggle={toggle} searchUnit={text ? searchUnit : undefined} />
    </div>
  )
}
