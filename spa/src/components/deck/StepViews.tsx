// spa/src/components/deck/StepViews.tsx — a step as the deck draws it (U3 spec §4 table): edit and execute are cards, read
// / search / fetch / other / task are one line, a question step is its options card. Every one ends in the fixed status
// slot. Nothing here nests a subagent's steps: a task line hands its step to `onOpenSubagent` (the right panel, U3-3).
import { FilePlus, FileText, Globe, MagnifyingGlass, PencilSimple, Robot, Terminal, Wrench, Question } from '@phosphor-icons/react'
import type { ReactNode } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { capDiff, readRange, toActivityDiff } from '../../lib/conversations/deck-format'
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

/** 「顯示全部」 only exists when someone can show it: no handler, no button. */
const showAll = (step: StepItem, actions: StepActions) => actions.onShowAll && (() => actions.onShowAll!(step))

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
  // The deck draws the first 16 lines and sends the rest to the right panel (spec §4); the room's own fold is switched off.
  const capped = capDiff(step.diff)
  const { onShowAll } = actions
  return (
    <Card
      step={step}
      icon={created ? <FilePlus className={ICON} size={16} /> : <PencilSimple className={ICON} size={16} />}
      title={<><span className="text-text-muted">{created ? t('deck.step.create') : t('deck.step.edit')}</span> <span className="font-mono text-xs" title={path}>{path}</span></>}
    >
      <ToolDiffView diff={toActivityDiff(capped.diff)} foldKey={step.id} unfolded />
      {capped.cut && onShowAll && (
        <button type="button" data-testid="diff-show-all" onClick={() => onShowAll(step)} className="cursor-pointer text-xs text-text-muted hover:text-text-primary">
          {t('deck.diff.show_all', { n: capped.totalLines })}
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
        <OutputFold foldKey={`${step.id}:out`} output={step.output} tone={step.status === 'failed' ? 'error' : 'normal'} onShowAll={showAll(step, actions)} />
      )}
    </Card>
  )
}

function QuestionCard({ step }: { step: StepItem }) {
  const t = useI18nStore((s) => s.t)
  const q = step.question!
  const answered = Array.isArray(q.answers)
  return (
    <Card step={step} icon={<Question className={ICON} size={16} />} title={<span className="text-text-muted">{t('deck.step.question')}</span>}>
      {q.questions.map((qq, i) => {
        // Daemon data is not validated at the API edge: a wrong-typed field reads as empty rather than throwing a render.
        const options = Array.isArray(qq?.options) ? qq.options : []
        const given = answered ? q.answers![i] : undefined
        const chosen = Array.isArray(given) ? given.filter((c): c is string => typeof c === 'string') : []
        // An answer that is none of the options is what the user typed.
        const typed = chosen.filter((c) => !options.some((o) => o.label === c))
        return (
          <div key={i} data-testid="deck-question" className="space-y-1">
            {qq?.header && <div className="text-xs text-text-muted">{String(qq.header)}</div>}
            <div className="text-text-primary">{String(qq?.question ?? '')}</div>
            <ul className="space-y-0.5">
              {options.map((o) => {
                const on = chosen.includes(o.label)
                return (
                  <li key={o.label} data-testid="deck-question-option" data-chosen={on} className={on ? 'text-text-primary' : 'text-text-secondary'}>
                    {on ? '✓ ' : '· '}{o.label}{o.description ? <span className="text-xs text-text-muted"> — {o.description}</span> : null}
                  </li>
                )
              })}
            </ul>
            {typed.length > 0 && <div data-testid="deck-question-free" className="text-xs text-text-primary">✓ {typed.join('、')}</div>}
          </div>
        )
      })}
      {!answered && step.status === 'running' && <div data-testid="deck-question-open" className="text-xs text-text-muted">{t('deck.question.open')}</div>}
    </Card>
  )
}

export function StepView({ step, actions = {} }: { step: StepItem; actions?: StepActions }) {
  const t = useI18nStore((s) => s.t)
  if (step.question && Array.isArray(step.question.questions)) return <QuestionCard step={step} />
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
      {step.output && <OutputFold foldKey={`${step.id}:out`} output={step.output} tone={step.status === 'failed' ? 'error' : 'normal'} onShowAll={showAll(step, actions)} />}
    </Line>
  )
}
