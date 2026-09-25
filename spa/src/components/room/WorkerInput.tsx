// spa/src/components/room/WorkerInput.tsx — the worker pane's reply field
// (spec §3.1.1 #6, §4.8): full width, no border box, one hairline separator
// above it, growing with the text up to MAX_INPUT_PX and scrolling past that.
import { useState, useRef, useCallback, useEffect } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'

/** Ceiling for the auto-grown textarea, so a long paste can't squeeze the transcript away. */
const MAX_INPUT_PX = 200

interface Props {
  onSend: (text: string) => void
  disabled?: boolean
  placeholder?: string
  focused?: boolean
  /** Seeds the textarea (e.g. restoring text after a failed send). */
  initialValue?: string
}

export default function WorkerInput({ onSend, disabled = false, placeholder, focused = false, initialValue }: Props) {
  const t = useI18nStore((s) => s.t)
  const resolvedPlaceholder = placeholder ?? t('worker.input.placeholder')
  const [value, setValue] = useState(initialValue ?? '')
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  useEffect(() => {
    if (focused && !disabled) {
      requestAnimationFrame(() => textareaRef.current?.focus())
    }
  }, [focused, disabled])

  const autoGrow = useCallback(() => {
    const ta = textareaRef.current
    if (ta) {
      ta.style.height = 'auto'
      ta.style.height = Math.min(ta.scrollHeight, MAX_INPUT_PX) + 'px'
      ta.style.overflowY = ta.scrollHeight > MAX_INPUT_PX ? 'auto' : 'hidden'
    }
  }, [])

  function send() {
    const trimmed = value.trim()
    if (!trimmed) return
    onSend(trimmed)
    setValue('')
    if (textareaRef.current) {
      textareaRef.current.style.height = 'auto'
      textareaRef.current.style.overflowY = 'hidden'
    }
  }

  function handleKeyDown(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      send()
    }
  }

  return (
    <div className={`w-full border-t border-border-subtle bg-surface-input transition-colors ${
      disabled ? 'opacity-40' : 'focus-within:border-border-active'
    }`}>
      <textarea
        ref={textareaRef}
        role="textbox"
        value={value}
        onChange={e => { setValue(e.target.value); autoGrow() }}
        onKeyDown={handleKeyDown}
        disabled={disabled}
        placeholder={resolvedPlaceholder}
        rows={1}
        className="block w-full bg-transparent text-text-primary placeholder-text-muted px-3 py-2.5 text-sm outline-none resize-none"
      />
    </div>
  )
}
