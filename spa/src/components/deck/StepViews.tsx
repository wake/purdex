// spa/src/components/deck/StepViews.tsx — a step as the deck draws it (U3 spec §4 table): edit and execute are cards, read
// / search / fetch / other / task are one line, a question step is its options card. Every one ends in the fixed status
// slot. A task line hands its step to `onOpenSubagent` (the right panel, U3-3) and its chevron unfolds the agent's own steps in place.
import { CaretDown, CaretUp, FilePlus, FileText, Globe, MagnifyingGlass, PencilSimple, Robot, Terminal, Wrench, Question } from '@phosphor-icons/react'
import { useEffect, type ReactNode } from 'react'
import { useNow } from '../../hooks/useNow'
import { useI18nStore } from '../../stores/useI18nStore'
import type { SubagentState } from '../../stores/useConversationStore'
import { formatDuration } from '../../lib/conversations/turn-line'
import { capDiff, readRange, stepFailed, toActivityDiff } from '../../lib/conversations/deck-format'
import type { ConversationItem, StepItem } from '../../lib/conversations/types'
import ToolDiffView from '../room/ToolDiffView'
import { useFold } from '../room/fold-context'
import { OutputFold } from './OutputFold'
import { StatusSlot, StepDot } from './StatusSlot'

export interface StepActions {
  /** 「顯示全部」 of an output or a diff: the right panel (a placeholder until U3-3). */
  onShowAll?: (step: StepItem) => void
  /** A subagent line was clicked. */
  onOpenSubagent?: (step: StepItem) => void
  /** Inline subagent steps (c9d9c7-54 pt 4): what the conversation holds of each agent (by agent id), the loader, and how to draw its items. */
  subagents?: Record<string, SubagentState>
  loadSubagent?: (agentId: string) => void
  renderSubagent?: (items: ConversationItem[]) => ReactNode
}

const DIFF_PREVIEW_LINES = 3
const ICON = 'shrink-0 text-text-muted'

/** A subagent's time: how long it ran (done), or how long so far (running, ticking). Nothing when the conversation does not say. */
function SubagentTime({ step }: { step: StepItem }) {
  const t = useI18nStore((s) => s.t)
  const running = step.status === 'running'
  const now = useNow(running)
  const ms = running ? (step.started_at > 0 ? now - step.started_at : null) : (typeof step.duration_ms === 'number' ? step.duration_ms : null)
  if (ms === null) return null
  return <span data-testid="subagent-time" className="shrink-0 deck-t-down tabular-nums text-text-muted">{formatDuration(Math.max(0, ms), t)}</span>
}

/** A subagent's own steps, one level in: read on demand the first time it is opened, kept by the conversation. */
function SubagentSteps({ step, actions }: { step: StepItem; actions: StepActions }) {
  const t = useI18nStore((s) => s.t)
  const agentId = step.subagent?.agent_id
  const load = actions.loadSubagent
  useEffect(() => { if (agentId && load) load(agentId) }, [agentId, load])
  const st = agentId ? actions.subagents?.[agentId] : undefined
  const items = st?.state === 'ready' ? st.answer?.items : undefined
  let body: ReactNode
  if (st?.state === 'error') body = <div data-testid="subagent-error" className="deck-t-down text-status-error">{t('panel.subagent.error')}</div>
  else if (items === undefined) body = <div data-testid="subagent-loading" className="deck-t-down text-text-muted">{t('deck.loading')}</div>
  else if (items.length === 0) body = <div data-testid="subagent-empty" className="deck-t-down text-text-muted">{t('panel.subagent.empty')}</div>
  else body = actions.renderSubagent ? actions.renderSubagent(items as ConversationItem[]) : null
  return <div data-testid="subagent-steps" className="ml-3 mt-1 space-y-1 border-l border-border-subtle pl-3">{body}</div>
}

/** 「顯示全部」 only exists when someone can show it: no handler, no button. */
const showAll = (step: StepItem, actions: StepActions) => actions.onShowAll && (() => actions.onShowAll!(step))

function Line({ step, icon, verb, subject, extra, children, testId = 'deck-step-line' }: {
  step: StepItem; icon: ReactNode; verb: string; subject: string; extra?: ReactNode; children?: ReactNode; testId?: string
}) {
  return (
    <div data-testid={testId} data-kind={step.kind} data-status={step.status} className="rounded-md border border-border-default bg-surface-secondary/50 px-3 py-1.5 deck-t-up">
      <div className="flex items-center gap-2">
        <StepDot step={step} />
        {icon}
        <span className="shrink-0 font-mono deck-t-down font-medium text-text-secondary">{verb}</span>
        <span className="min-w-0 flex-1 truncate font-mono deck-t-down text-text-secondary" title={subject}>{subject}</span>
        {extra}
        <StatusSlot step={step} />
      </div>
      {children && <div className="pl-6">{children}</div>}
    </div>
  )
}

function Card({ step, icon, title, headerExtra, children }: { step: StepItem; icon: ReactNode; title: ReactNode; headerExtra?: ReactNode; children: ReactNode }) {
  return (
    <div data-testid="deck-step-card" data-kind={step.kind} data-status={step.status} className="rounded-md border border-border-default bg-surface-secondary/50 deck-t-up">
      <div className="flex items-center gap-2 px-3 py-1.5">
        <StepDot step={step} />
        {icon}
        <span className="min-w-0 flex-1 truncate text-text-secondary"><span className="mr-2 font-mono deck-t-down font-medium text-text-secondary">{step.tool}</span>{title}</span>
        <StatusSlot step={step} />
        {headerExtra}
      </div>
      <div className="space-y-1 px-3 pb-2">{children}</div>
    </div>
  )
}

function EditCard({ step, actions }: { step: StepItem; actions: StepActions }) {
  const t = useI18nStore((s) => s.t)
  if (!step.diff) {
    return <Line step={step} icon={<PencilSimple className={ICON} size={16} />} verb={t('deck.step.edit')} subject={step.summary} />
  }
  return <DiffCard step={step} diff={step.diff} actions={actions} />
}

function DiffCard({ step, diff, actions }: { step: StepItem; diff: NonNullable<StepItem['diff']>; actions: StepActions }) {
  const t = useI18nStore((s) => s.t)
  // The key deliberately equals ToolDiffView's own `${foldKey}:diff` registration, so the room's expand-all / search expand opens this card too.
  const [open, toggle] = useFold(`${step.id}:diff`)
  const created = diff.created === true
  const path = diff.path ?? step.summary
  // The deck draws the first 16 lines and sends the rest to the right panel (spec §4); the room's own fold is switched off.
  const capped = capDiff(diff, open ? undefined : DIFF_PREVIEW_LINES)
  // The caret only when there is more than the 3-line preview to show; a daemon-truncated short diff just offers 「顯示全部」.
  const expandable = open || capped.totalLines > DIFF_PREVIEW_LINES
  const { onShowAll } = actions
  const showAll = onShowAll && capped.cut && (open || !expandable)
  return (
    <Card
      step={step}
      icon={created ? <FilePlus className={ICON} size={16} /> : <PencilSimple className={ICON} size={16} />}
      headerExtra={expandable && (
        <button type="button" data-testid="diff-expand" aria-expanded={open} aria-label={t(open ? 'deck.diff.collapse' : 'deck.diff.expand')} onClick={toggle}
          className="shrink-0 cursor-pointer rounded text-text-muted hover:text-text-primary">
          {open ? <CaretUp size={14} /> : <CaretDown size={14} />}
        </button>
      )}
      title={<><span className="text-text-muted">{created ? t('deck.step.create') : t('deck.step.edit')}</span> <span className="font-mono deck-t-down" title={path}>{path}</span></>}
    >
      <div data-testid="diff-body" className="opacity-60 transition-opacity hover:opacity-100 focus-within:opacity-100">
        <ToolDiffView diff={toActivityDiff(capped.diff)} foldKey={step.id} unfolded />
      </div>
      {showAll && (
        <button type="button" data-testid="diff-show-all" onClick={() => onShowAll(step)} className="cursor-pointer deck-t-down text-text-muted hover:text-text-primary">
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
      <pre data-testid="exec-command" className="line-clamp-6 overflow-hidden whitespace-pre-wrap break-all rounded bg-black/60 px-2 py-1 font-mono deck-t-down text-neutral-400 transition-colors hover:text-neutral-200">{`$ ${command}`}</pre>
      {step.output && (
        <OutputFold foldKey={`${step.id}:out`} output={step.output} tone={stepFailed(step) ? 'error' : 'normal'} onShowAll={showAll(step, actions)} />
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
        const options = (Array.isArray(qq?.options) ? qq.options : [])
          .filter((o) => o && typeof o.label === 'string')
          .map((o) => ({ label: o.label, description: typeof o.description === 'string' ? o.description : undefined }))
        const given = answered ? q.answers![i] : undefined
        const chosen = Array.isArray(given) ? given.filter((c): c is string => typeof c === 'string') : []
        // An answer that is none of the options is what the user typed.
        const typed = chosen.filter((c) => !options.some((o) => o.label === c))
        return (
          <div key={i} data-testid="deck-question" className="space-y-1">
            {qq?.header && <div className="deck-t-down text-text-muted">{String(qq.header)}</div>}
            <div className="text-text-primary">{String(qq?.question ?? '')}</div>
            <ul className="space-y-0.5">
              {options.map((o) => {
                const on = chosen.includes(o.label)
                return (
                  <li key={o.label} data-testid="deck-question-option" data-chosen={on} className={on ? 'text-text-primary' : 'text-text-secondary'}>
                    {on ? '✓ ' : '· '}{o.label}{o.description ? <span className="deck-t-down text-text-muted"> — {o.description}</span> : null}
                  </li>
                )
              })}
            </ul>
            {typed.length > 0 && <div data-testid="deck-question-free" className="deck-t-down text-text-primary">✓ {typed.join('、')}</div>}
          </div>
        )
      })}
      {!answered && step.status === 'running' && <div data-testid="deck-question-open" className="deck-t-down text-text-muted">{t('deck.question.open')}</div>}
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
          extra={r ? <span data-testid="read-range" className="shrink-0 deck-t-down text-text-muted">{t('deck.read.range', { a: r.from, b: r.to })}</span> : null} />
      )
    }
    case 'task': return <TaskStep step={step} actions={actions} />
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
      extra={where ? <span data-testid="search-where" className="shrink-0 truncate deck-t-down text-text-muted">{where}</span> : null}>
      {step.output && <OutputFold foldKey={`${step.id}:out`} output={step.output} tone={stepFailed(step) ? 'error' : 'normal'} onShowAll={showAll(step, actions)} />}
    </Line>
  )
}

/** An Agent/Task step: its type, description, time and state; the line opens the right panel, the chevron unfolds the agent's own steps in place. */
function TaskStep({ step, actions }: { step: StepItem; actions: StepActions }) {
  const t = useI18nStore((s) => s.t)
  const sub = step.subagent
  const [open, toggle] = useFold(`${step.id}:sub`)
  // Only a step that names its agent and a deck that can load it has anything to unfold.
  const unfoldable = Boolean(sub?.agent_id && actions.loadSubagent)
  return (
    <div data-testid="deck-step-task-wrap">
      <div className="flex items-stretch gap-1">
        {unfoldable && (
          <button type="button" data-testid="deck-step-task-toggle" aria-expanded={open} aria-label={t('deck.step.task_toggle')} onClick={toggle}
            className="flex w-6 shrink-0 cursor-pointer items-center justify-center rounded-md deck-t-down text-text-muted hover:bg-surface-secondary hover:text-text-primary">
            {open ? '▾' : '▸'}
          </button>
        )}
        <button type="button" data-testid="deck-step-task" data-status={step.status} onClick={() => actions.onOpenSubagent?.(step)}
          className="flex min-w-0 flex-1 cursor-pointer items-center gap-2 rounded-md border border-border-default bg-surface-secondary/50 px-3 py-1.5 text-left deck-t-up hover:bg-surface-secondary">
          <StepDot step={step} />
          <Robot className={ICON} size={16} />
          <span className="shrink-0 text-text-muted">{sub?.type ? t('deck.step.task_type', { type: sub.type }) : t('deck.step.task')}</span>
          <span className="min-w-0 flex-1 truncate deck-t-down text-text-secondary">{sub?.description ?? step.summary}</span>
          <SubagentTime step={step} />
          <StatusSlot step={step} />
        </button>
      </div>
      {open && unfoldable && <SubagentSteps step={step} actions={actions} />}
    </div>
  )
}
