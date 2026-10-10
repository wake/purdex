// spa/src/components/team/NumberField.tsx — the number of a stepper (unattended panel): shown as a value, a click turns it into an input
// (c9d9c7-56). Enter confirms, Esc cancels, leaving the box confirms; a value that is not a whole number or that the caller's `validate`
// refuses stays in the box with the reason beside it and is never sent. `onCommit` only runs for a valid number that is not the one shown.
import { useRef, useState } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'

interface Props {
  value: number
  /** Read-only while a request is out (or the control is otherwise off). */
  disabled?: boolean
  /** The reason a number is not allowed (already in the reader's words), or null. Not called for a non-integer. */
  validate: (n: number) => string | null
  onCommit: (n: number) => void
  testId: string
  ariaLabel: string
}

export function NumberField({ value, disabled, validate, onCommit, testId, ariaLabel }: Props) {
  const t = useI18nStore((s) => s.t)
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState('')
  const [error, setError] = useState<string | null>(null)
  // Esc closes the box, and the box going away must not read as "leaving it" (which confirms)
  const cancelled = useRef(false)

  const open = () => { if (disabled) return; cancelled.current = false; setDraft(String(value)); setError(null); setEditing(true) }
  // however the box closes (Enter, Esc), its unmounting must not run `commit` again as a blur
  const close = () => { cancelled.current = true; setEditing(false); setError(null) }
  const commit = () => {
    if (cancelled.current) return
    const text = draft.trim()
    if (!/^\d+$/.test(text)) { setError(t('unattended.field.integer')); return }
    const n = Number(text)
    if (n === value) { close(); return }
    const why = validate(n)
    if (why !== null) { setError(why); return }
    close()
    onCommit(n)
  }

  if (!editing) {
    return (
      <button
        type="button" data-testid={testId} aria-label={ariaLabel} title={disabled ? undefined : t('unattended.field.edit_hint')} disabled={disabled} onClick={open}
        className="min-w-[2ch] cursor-text rounded px-0.5 text-center tabular-nums text-text-primary hover:bg-surface-hover disabled:cursor-default disabled:hover:bg-transparent"
      >{value}</button>
    )
  }
  return (
    <span className="relative inline-flex items-center gap-1">
      <input
        type="text" inputMode="numeric" autoFocus data-testid={`${testId}-input`} aria-label={ariaLabel} aria-invalid={error !== null} value={draft} size={3}
        onChange={(e) => { setDraft(e.target.value); setError(null) }}
        onKeyDown={(e) => {
          if (e.key === 'Enter') { e.preventDefault(); commit() }
          else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); close() }
        }}
        onBlur={commit}
        className="w-[4ch] rounded border border-border-subtle bg-surface-primary px-1 text-center tabular-nums text-text-primary"
      />
      {/* below the box, out of the row's flow: a long reason must not widen the column the steppers share */}
      {error !== null && <span data-testid={`${testId}-error`} role="alert" className="absolute right-0 top-full z-10 mt-0.5 whitespace-nowrap rounded bg-surface-elevated px-1.5 py-0.5 text-status-error shadow">{error}</span>}
    </span>
  )
}
