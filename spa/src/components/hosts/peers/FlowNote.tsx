// spa/src/components/hosts/peers/FlowNote.tsx — the step / error / hint line
// of the page's one flow, rendered by whichever row or candidate owns its key
// (and by the page itself when nothing on screen does any more — an unpaired
// row is gone by the time its error lands).
import { useI18nStore } from '../../../stores/useI18nStore'
import type { FlowState } from './flow'

export function FlowNote({ flow, flowKey }: { flow: FlowState | null; flowKey: string }) {
  const t = useI18nStore((s) => s.t)
  if (!flow || flow.key !== flowKey) return null
  const stepText = flow.running && flow.step ? t(`peers.step.${flow.step}`) : ''
  if (!stepText && !flow.error && !flow.hint) return null
  return (
    <div data-testid="peer-flow" data-key={flowKey} className="mt-1.5 flex flex-col gap-0.5 text-xs">
      {stepText && <span data-testid="peer-flow-step" className="text-text-muted">{stepText}</span>}
      {flow.error && <span data-testid="peer-flow-error" className="text-status-error whitespace-pre-wrap">{flow.error}</span>}
      {flow.hint && <span data-testid="peer-flow-hint" className="text-text-secondary">{flow.hint}</span>}
    </div>
  )
}
