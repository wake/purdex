// spa/src/components/journal/KindChip.tsx — the kind chip of a journal row. Theme tokens only (the approved prototype's seven
// kinds, each its own colour): 待確認 is the warning colour (`status-warning-text`), the other six are `journal-kind-*` from
// themes.css (readable on each builtin theme's card; a custom theme falls back to accent / success / secondary text).
// The icon and the label tell the kinds apart as well, so colour is never the only cue.
import { ChatCircleDots, CheckCircle, CheckSquare, Gear, PlusSquare, Question, Warning } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import type { EntryKind } from '../../lib/journal/types'

const ICONS: Record<EntryKind, typeof Gear> = {
  exec: Gear, exec_done: CheckCircle, task_new: PlusSquare, task_done: CheckSquare, comm: ChatCircleDots, request: Question, confirm: Warning,
}

const TONE: Record<EntryKind, string> = {
  confirm: 'text-status-warning-text bg-status-warning/15 border-status-warning/40',
  exec: 'text-journal-kind-exec bg-journal-kind-exec/15 border-journal-kind-exec/40',
  exec_done: 'text-journal-kind-exec-done bg-journal-kind-exec-done/15 border-journal-kind-exec-done/40',
  task_new: 'text-journal-kind-task-new bg-journal-kind-task-new/15 border-journal-kind-task-new/40',
  task_done: 'text-journal-kind-task-done bg-journal-kind-task-done/15 border-journal-kind-task-done/40',
  comm: 'text-journal-kind-comm bg-journal-kind-comm/15 border-journal-kind-comm/40',
  request: 'text-journal-kind-request bg-journal-kind-request/15 border-journal-kind-request/40',
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
