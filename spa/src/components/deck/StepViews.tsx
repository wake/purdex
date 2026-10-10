// spa/src/components/deck/StepViews.tsx — a step as the deck draws it (U3 spec §4 table): edit and execute are cards, read
// / search / fetch / other / task are one line, a question step is its options card. Every one ends in the fixed status
// slot. Nothing here nests a subagent's steps: a task line hands its step to `onOpenSubagent` (the right panel, U3-3).
import { FilePlus, FileText, Globe, MagnifyingGlass, PencilSimple, Robot, Terminal, Wrench, Question } from '@phosphor-icons/react'
import type { ReactNode } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { readRange, toActivityDiff } from '../../lib/conversations/deck-format'
import type { StepItem } from '../../lib/conversations/types'
import ToolDiffView from '../room/ToolDiffView'
import { OutputFold } from './OutputFold'
import { StatusSlot } from './StatusSlot'

export interface StepActions {
  /** 「顯示全部」 of an output or a diff: the right panel (a placeholder until U3-3). */
  onShowAll?: (step: StepItem) => void
  /** A subagent line was clicked. */
  onOpenSubagent?: (step: StepItem) => void
}

const ICON = 'shrink-0 text-text-muted'

function Line({ step, icon, verb, subject, extra, children, testId = 'deck-step-line' }: {
  step: StepItem; icon: ReactNode; verb: string; subject: string; extra?: ReactNode; children?: ReactNode; testId?: string
}) {
  return (
    <div data-testid={testId} data-kind={step.kind} data-status={step.status} className="text-sm">
      <div className="flex items-center gap-2">
        {icon}
        <span className="shrink-0 text-text-muted">{verb}</span>
        <span className="min-w-0 flex-1 truncate font-mono text-xs text-text-secondary" title={subject}>{subject}</span>
        {extra}
        <StatusSlot step={step} />
      </div>
      {children && <div className="pl-6">{children}</div>}
    </div>
  )
}

function Card({ step, icon, title, children }: { step: StepItem; icon: ReactNode; title: ReactNode; children: ReactNode }) {
  return (
    <div data-testid="deck-step-card" data-kind={step.kind} data-status={step.status} className="rounded-lg border border-border-subtle text-sm">
      <div className="flex items-center gap-2 px-3 py-1.5">
        {icon}
        <span className="min-w-0 flex-1 truncate">{title}</span>
        <StatusSlot step={step} />
      </div>
      <div className="space-y-1 px-3 pb-2">{children}</div>
    </div>
  )
}

function EditCard({ step, actions }: { step: StepItem; actions: StepActions }) {
  const t = useI18nStore((s) => s.t)
  const created = step.diff?.created === true
  const path = step.diff?.path ?? step.summary
  if (!step.diff) {
    return <Line step={step} icon={<PencilSimple className={ICON} size={16} />} verb={t('deck.step.edit')} subject={step.summary} />
  }
  return (
    <Card
      step={step}
      icon={created ? <FilePlus className={ICON} size={16} /> : <PencilSimple className={ICON} size={16} />}
      title={<><span className="text-text-muted">{created ? t('deck.step.create') : t('deck.step.edit')}</span> <span className="font-mono text-xs" title={path}>{path}</span></>}
    >
      <ToolDiffView diff={toActivityDiff(step.diff)} foldKey={step.id} />
      {step.diff.truncated && actions.onShowAll && (
        <button type="button" data-testid="diff-show-all" onClick={() => actions.onShowAll?.(step)} className="cursor-pointer text-xs text-text-muted hover:text-text-primary">
          {t('deck.output.show_all')}
        </button>
      )}
    </Card>
  )
}

function ExecuteCard({ step, actions }: { step: StepItem; actions: StepActions }) {
  const t = useI18nStore((s) => s.t)
  const command = step.command?.text ?? step.summary
  return (
    <Card step={step} icon={<Terminal className={ICON} size={16} />} title={<span className="text-text-muted">{step.command?.description ?? t('deck.step.execute')}</span>}>
      <pre data-testid="exec-command" className="line-clamp-6 overflow-hidden whitespace-pre-wrap break-all rounded bg-black/60 px-2 py-1 font-mono text-xs text-neutral-100">{`$ ${command}`}</pre>
      {step.output && (
        <OutputFold foldKey={`${step.id}:out`} output={step.output} tone={step.status === 'failed' ? 'error' : 'normal'} onShowAll={() => actions.onShowAll?.(step)} />
      )}
    </Card>
  )
}

function QuestionCard({ step }: { step: StepItem }) {
  const t = useI18nStore((s) => s.t)
  const q = step.question!
  const answered = q.answers !== undefined
  return (
    <Card step={step} icon={<Question className={ICON} size={16} />} title={<span className="text-text-muted">{t('deck.step.question')}</span>}>
      {q.questions.map((qq, i) => {
        const chosen = q.answers?.[i] ?? []
        return (
          <div key={i} data-testid="deck-question" className="space-y-1">
            {qq.header && <div className="text-xs text-text-muted">{qq.header}</div>}
            <div className="text-text-primary">{qq.question}</div>
            <ul className="space-y-0.5">
              {qq.options.map((o) => {
                const on = chosen.includes(o.label)
                return (
                  <li key={o.label} data-testid="deck-question-option" data-chosen={on} className={on ? 'text-text-primary' : 'text-text-secondary'}>
                    {on ? '✓ ' : '· '}{o.label}{o.description ? <span className="text-xs text-text-muted"> — {o.description}</span> : null}
                  </li>
                )
              })}
            </ul>
            {answered && chosen.length > 0 && chosen.some((c) => !qq.options.some((o) => o.label === c)) && (
              <div data-testid="deck-question-free" className="text-xs text-text-primary">✓ {chosen.filter((c) => !qq.options.some((o) => o.label === c)).join('、')}</div>
            )}
          </div>
        )
      })}
      {!answered && step.status === 'running' && <div data-testid="deck-question-open" className="text-xs text-text-muted">{t('deck.question.open')}</div>}
    </Card>
  )
}

export function StepView({ step, actions = {} }: { step: StepItem; actions?: StepActions }) {
  const t = useI18nStore((s) => s.t)
  if (step.question) return <QuestionCard step={step} />
  switch (step.kind) {
    case 'edit': return <EditCard step={step} actions={actions} />
    case 'execute': return <ExecuteCard step={step} actions={actions} />
    case 'read': {
      const r = readRange(step)
      return (
        <Line step={step} icon={<FileText className={ICON} size={16} />} verb={t('deck.step.read')} subject={step.summary}
          extra={r ? <span data-testid="read-range" className="shrink-0 text-xs text-text-muted">{t('deck.read.range', { a: r.from, b: r.to })}</span> : null} />
      )
    }
    case 'task': {
      const sub = step.subagent
      return (
        <button type="button" data-testid="deck-step-task" data-status={step.status} onClick={() => actions.onOpenSubagent?.(step)}
          className="flex w-full cursor-pointer items-center gap-2 text-left text-sm hover:bg-surface-secondary">
          <Robot className={ICON} size={16} />
          <span className="shrink-0 text-text-muted">{sub?.type ? t('deck.step.task_type', { type: sub.type }) : t('deck.step.task')}</span>
          <span className="min-w-0 flex-1 truncate text-xs text-text-secondary">{sub?.description ?? step.summary}</span>
          <StatusSlot step={step} />
        </button>
      )
    }
    case 'search':
      return <OutputLine step={step} icon={<MagnifyingGlass className={ICON} size={16} />} verb={t('deck.step.search')} actions={actions} />
    case 'fetch':
      return <OutputLine step={step} icon={<Globe className={ICON} size={16} />} verb={t('deck.step.fetch')} actions={actions} />
    default:
      return <OutputLine step={step} icon={<Wrench className={ICON} size={16} />} verb={step.tool} actions={actions} />
  }
}

function OutputLine({ step, icon, verb, actions }: { step: StepItem; icon: ReactNode; verb: string; actions: StepActions }) {
  const where = step.search?.where
  return (
    <Line step={step} icon={icon} verb={verb} subject={step.summary}
      extra={where ? <span data-testid="search-where" className="shrink-0 truncate text-xs text-text-muted">{where}</span> : null}>
      {step.output && <OutputFold foldKey={`${step.id}:out`} output={step.output} tone={step.status === 'failed' ? 'error' : 'normal'} onShowAll={() => actions.onShowAll?.(step)} />}
    </Line>
  )
}
