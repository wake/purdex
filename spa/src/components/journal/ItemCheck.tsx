// spa/src/components/journal/ItemCheck.tsx — the checkbox of one 待確認 item (JV-3, spec U8). `onToggle` undefined = read-only (the host
// does not declare journal.confirm.v1): the same box, drawn without a button. Optimistic writes live in the store (setConfirm).
import { CheckSquare, Square } from '@phosphor-icons/react'
import { keepFocus } from '../../lib/keep-focus'
import { useI18nStore } from '../../stores/useI18nStore'

export function ItemCheck({ done, size, onToggle }: { done: boolean; size: number; onToggle?: (done: boolean) => void }) {
  const t = useI18nStore((s) => s.t)
  const icon = done
    ? <CheckSquare size={size} weight="fill" className="shrink-0 mt-px text-status-success-text" />
    : <Square size={size} className="shrink-0 mt-px text-journal-warning-text" />
  if (!onToggle) return icon
  const label = t(done ? 'journal.confirm.uncheck' : 'journal.confirm.check')
  return (
    <button
      type="button"
      role="checkbox"
      aria-checked={done}
      aria-label={label}
      title={label}
      data-testid="journal-item-check"
      className="relative pointer-events-auto shrink-0 rounded cursor-pointer hover:bg-surface-hover"
      onMouseDown={keepFocus}
      onClick={() => onToggle(!done)}
    >
      {icon}
    </button>
  )
}
