// spa/src/components/journal/KindChip.tsx — the kind chip of a journal row. Theme tokens only, and a theme offers few hues
// (error / warning / success / accent), so the kinds are told apart by icon and label as much as by colour:
// 待確認 is the only warning chip; the two "done" kinds are success; exec / request / task_new accent; comm neutral.
import { ChatCircleDots, CheckCircle, CheckSquare, Gear, PlusSquare, Question, Warning } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import type { EntryKind } from '../../lib/journal/types'

const ICONS: Record<EntryKind, typeof Gear> = {
  exec: Gear, exec_done: CheckCircle, task_new: PlusSquare, task_done: CheckSquare, comm: ChatCircleDots, request: Question, confirm: Warning,
}

const TONE: Record<EntryKind, string> = {
  confirm: 'text-status-warning-text bg-status-warning/15 border-status-warning/40',
  exec_done: 'text-status-success bg-status-success/15 border-status-success/40',
  task_done: 'text-status-success bg-status-success/15 border-status-success/40',
  exec: 'text-accent bg-accent-muted border-accent/40',
  request: 'text-accent bg-accent-muted border-accent/40',
  task_new: 'text-accent bg-accent-muted border-accent/40',
  comm: 'text-text-secondary bg-surface-hover border-border-default',
}

export function KindChip({ kind }: { kind: EntryKind }) {
  const t = useI18nStore((s) => s.t)
  const Icon = ICONS[kind]
  return (
    <span data-testid="journal-kind" data-kind={kind} className={`inline-flex items-center gap-1 px-1.5 rounded border text-[10px] font-medium shrink-0 ${TONE[kind]}`}>
      <Icon size={10} weight="bold" />{t(`journal.kind.${kind}`)}
    </span>
  )
}
