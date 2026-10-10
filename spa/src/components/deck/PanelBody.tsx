// spa/src/components/deck/PanelBody.tsx — what the right panel shows (U3 plan D10): a chain's steps, a step's whole output /
// diff, or a subagent's steps. Steps are drawn by the deck's own `StepView`, so a chain reads like the deck does.
import { useI18nStore } from '../../stores/useI18nStore'
import { toActivityDiff } from '../../lib/conversations/deck-format'
import type { PanelView } from '../../lib/conversations/panel-resolve'
import ToolDiffView from '../room/ToolDiffView'
import { DeckItem } from './DeckItem'
import { StepView, type StepActions } from './StepViews'

export function panelTitle(view: PanelView, t: (k: string, p?: Record<string, string | number>) => string): string {
  switch (view.kind) {
    case 'chain': return t('panel.chain', { turn: view.turnIndex + 1, k: view.position, n: view.count })
    case 'output': return t(view.step.diff && !view.step.output ? 'panel.diff' : 'panel.output')
    case 'subagent': return t('panel.subagent', { name: view.step.subagent?.description ?? view.step.subagent?.type ?? view.step.summary })
  }
}

export function PanelBody({ view, actions }: { view: PanelView; actions: StepActions }) {
  const t = useI18nStore((s) => s.t)
  if (view.kind === 'chain') {
    return <div data-testid="panel-chain" className="space-y-2">{view.steps.map((s) => <StepView key={s.id} step={s} actions={actions} />)}</div>
  }
  if (view.kind === 'output') {
    const { step } = view
    return (
      <div data-testid="panel-output" className="space-y-2">
        {step.diff && <ToolDiffView diff={toActivityDiff(step.diff)} foldKey={`${step.id}:panel`} unfolded />}
        {step.output && (
          <>
            <pre data-testid="panel-output-text" className="whitespace-pre-wrap break-all font-mono text-xs text-text-secondary">{step.output.text}</pre>
            {step.output.truncated && <div className="text-xs italic text-text-muted">{t('deck.output.cut')}</div>}
          </>
        )}
      </div>
    )
  }
  if (view.items.length === 0) return <div data-testid="panel-empty" className="text-sm text-text-muted">{t('panel.subagent.empty')}</div>
  return <div data-testid="panel-subagent" className="space-y-2">{view.items.map((it) => <DeckItem key={it.id} item={it} actions={actions} />)}</div>
}
