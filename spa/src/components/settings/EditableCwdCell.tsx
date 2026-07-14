import { useEffect, useRef, useState } from 'react'

/**
 * Inline-editable rebuild-target cwd cell for the Snapshot reconciliation table.
 *
 * Display mode renders the current cwd (or an em-dash placeholder). Double-click
 * swaps to a controlled input pre-filled with the cwd, auto-focused with its text
 * selected. Enter OR blur commits the trimmed-by-the-caller value via `onCommit`;
 * Esc cancels without committing. A `committedRef` latch guards the two ways an
 * edit ends from firing `onCommit` twice: Enter commits then unmounts the input,
 * and Esc's trailing blur would otherwise sneak a commit through.
 *
 * The cell owns only local editing/draft state — the snapshot stays the single
 * source of truth; the parent persists the value and re-reads on `onCommit`.
 */
export function EditableCwdCell({
  cwd,
  onCommit,
}: {
  cwd?: string
  onCommit: (value: string) => void
}) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState('')
  // Once an edit resolves (commit or cancel) this blocks any trailing blur from
  // firing a second onCommit.
  const committedRef = useRef(false)
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (editing && inputRef.current) {
      inputRef.current.focus()
      inputRef.current.select()
    }
  }, [editing])

  const startEditing = () => {
    setDraft(cwd ?? '')
    committedRef.current = false
    setEditing(true)
  }

  const commit = () => {
    if (committedRef.current) return
    committedRef.current = true
    setEditing(false)
    onCommit(draft)
  }

  const cancel = () => {
    // Latch so the input's trailing blur (from unmounting) does not commit.
    committedRef.current = true
    setEditing(false)
  }

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') {
      e.preventDefault()
      commit()
    } else if (e.key === 'Escape') {
      e.preventDefault()
      cancel()
    }
  }

  if (editing) {
    return (
      <input
        ref={inputRef}
        data-testid="snapshot-cwd-input"
        type="text"
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={handleKeyDown}
        onBlur={commit}
        className="w-full min-w-40 rounded border border-border-active bg-bg-input px-1 py-0.5 font-mono text-text-primary outline-none"
      />
    )
  }

  return (
    <span
      data-testid="snapshot-cwd-cell"
      onDoubleClick={startEditing}
      title="Double-click to edit the rebuild directory"
      className="font-mono cursor-text hover:text-text-primary"
    >
      {cwd ?? '—'}
    </span>
  )
}
