import { useState, useRef, useEffect, useLayoutEffect } from 'react'
import { useClickOutside } from '../hooks/useClickOutside'
import { useI18nStore } from '../stores/useI18nStore'

interface Props {
  anchorRect: DOMRect
  currentName: string
  onConfirm: (name: string) => Promise<void>
  onCancel: () => void
  error?: string
  onClearError?: () => void
}

const POPOVER_WIDTH = 240
const PADDING = 4

export function RenamePopover({ anchorRect, currentName, onConfirm, onCancel, error, onClearError }: Props) {
  const t = useI18nStore((s) => s.t)
  const [draft, setDraft] = useState(currentName)
  const [submitting, setSubmitting] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)
  const containerRef = useRef<HTMLDivElement>(null)

  useClickOutside(containerRef, onCancel)

  // Focus + select all on mount
  useEffect(() => {
    const input = inputRef.current
    if (input) {
      input.focus()
      input.select()
    }
  }, [])

  // Position: centered below anchor, clamped to viewport
  useLayoutEffect(() => {
    const el = containerRef.current
    if (!el) return
    let left = anchorRect.left + anchorRect.width / 2 - POPOVER_WIDTH / 2
    left = Math.max(PADDING, Math.min(left, window.innerWidth - POPOVER_WIDTH - PADDING))
    const top = anchorRect.bottom + PADDING
    el.style.left = `${left}px`
    el.style.top = `${top}px`
  }, [anchorRect])

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.preventDefault()
      onCancel()
    }
    if (e.key === 'Enter') {
      e.preventDefault()
      const trimmed = draft.trim()
      if (!trimmed || trimmed === currentName || submitting) return
      setSubmitting(true)
      onConfirm(trimmed).finally(() => setSubmitting(false))
    }
  }

  return (
    <div
      ref={containerRef}
      className="fixed z-50 bg-surface-elevated border border-border-default rounded-lg shadow-xl p-2"
      style={{ width: POPOVER_WIDTH }}
    >
      <input
        ref={inputRef}
        type="text"
        value={draft}
        onChange={(e) => { setDraft(e.target.value); onClearError?.() }}
        onKeyDown={handleKeyDown}
        disabled={submitting}
        placeholder={t('tab.rename_placeholder')}
        className="w-full bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-3 py-1.5 focus:border-border-active focus:outline-none disabled:opacity-50"
      />
      {error && (
        <p className="text-xs text-red-400 mt-1 px-1">{error}</p>
      )}
    </div>
  )
}
