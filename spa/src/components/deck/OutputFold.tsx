// spa/src/components/deck/OutputFold.tsx — 「輸出 · N 行」 that opens to the last 10 lines (U3 spec §4), with 「…已截斷」 when
// lines were cut and 「顯示全部」 handing the whole output to the right panel. Whether it is open lives in the pane's fold
// memory (`useFold`), not here: the deck unmounts with its tab.
import { useI18nStore } from '../../stores/useI18nStore'
import { outputTail } from '../../lib/conversations/deck-format'
import type { StepOutput } from '../../lib/conversations/types'
import { useFold } from '../room/fold-context'

interface Props {
  /** The fold key; unique within the pane (the step's id plus a suffix). */
  foldKey: string
  output: StepOutput
  tone?: 'normal' | 'error'
  onShowAll?: () => void
}

export function OutputFold({ foldKey, output, tone = 'normal', onShowAll }: Props) {
  const t = useI18nStore((s) => s.t)
  const [open, toggle] = useFold(foldKey)
  const tail = outputTail(output)
  const textless = typeof output?.text !== 'string' || output.text === ''
  // An output that is only pictures has no lines to fold: say so, as the user block does (the apps draw a notice).
  if (textless) {
    const n = output?.images?.length ?? 0
    return n > 0 ? <div data-testid="output-images" className="text-xs text-text-muted">{t('deck.output.images', { n })}</div> : null
  }
  return (
    <div data-testid="output-fold" className="text-xs">
      <button type="button" data-testid="output-toggle" aria-expanded={open} onClick={toggle} className="cursor-pointer text-text-muted hover:text-text-primary">
        {open ? '▾' : '▸'} {t('deck.output', { n: tail.totalLines })}
      </button>
      {open && (
        <div className="mt-1">
          {tail.cut && <div data-testid="output-cut" className="text-text-muted italic">{t('deck.output.cut')}</div>}
          <pre data-testid="output-body" className={`max-h-72 overflow-auto whitespace-pre-wrap break-all ${tone === 'error' ? 'text-status-error' : 'text-text-secondary'}`}>{tail.text}</pre>
          {onShowAll && tail.cut && (
            <button type="button" data-testid="output-show-all" onClick={onShowAll} className="mt-1 cursor-pointer text-text-muted hover:text-text-primary">
              {t('deck.output.show_all')}
            </button>
          )}
        </div>
      )}
    </div>
  )
}
