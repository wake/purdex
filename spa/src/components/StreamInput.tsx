// spa/src/components/StreamInput.tsx
import { useState, useRef, useCallback } from 'react'
import { Plus, Terminal } from '@phosphor-icons/react'
import { useI18nStore } from '../stores/useI18nStore'

interface Props {
  onSend: (text: string) => void
  onAttach?: () => void
  onHandoffToTerm?: () => void
  disabled?: boolean
  placeholder?: string
}

export default function StreamInput({ onSend, onAttach, onHandoffToTerm, disabled = false, placeholder }: Props) {
  const t = useI18nStore((s) => s.t)
  const resolvedPlaceholder = placeholder ?? t('stream.input.placeholder')
  const [value, setValue] = useState('')
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  const autoGrow = useCallback(() => {
    const ta = textareaRef.current
    if (ta) {
      ta.style.height = 'auto'
      ta.style.height = ta.scrollHeight + 'px'
    }
  }, [])

  function send() {
    const trimmed = value.trim()
    if (!trimmed) return
    onSend(trimmed)
    setValue('')
    if (textareaRef.current) {
      textareaRef.current.style.height = 'auto'
    }
  }

  function handleKeyDown(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      send()
    }
  }

  return (
    <div className={`mx-2 mb-2 border rounded-xl overflow-hidden transition-colors ${
      disabled ? 'opacity-40 border-border-default bg-surface-input' : 'border-border-default bg-surface-input focus-within:border-blue-400'
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
        className="w-full bg-transparent text-text-primary placeholder-text-muted px-3 py-2.5 text-sm outline-none resize-none"
      />
      <div className="flex items-center px-2 pb-1.5">
        <button
          type="button"
          disabled={disabled}
          onClick={onAttach}
          className="w-7 h-7 rounded-md flex items-center justify-center text-text-muted hover:text-text-primary hover:bg-surface-hover transition-colors disabled:opacity-40"
        >
          <Plus size={16} />
        </button>
        <div className="flex-1" />
        {onHandoffToTerm && (
          <button
            type="button"
            onClick={onHandoffToTerm}
            disabled={disabled}
            title={t('stream.handoff_to_term')}
            className="flex items-center gap-1 px-2 py-1 rounded text-xs text-text-muted hover:text-text-primary hover:bg-surface-hover transition-colors disabled:opacity-40"
          >
            <Terminal size={14} />
            <span>{t('stream.handoff_to_term')}</span>
          </button>
        )}
      </div>
    </div>
  )
}
