// spa/src/components/room/WorkerInput.tsx — the worker pane's reply field
// (spec §3.1.1 #6, §4.8): full width, no border box, one hairline separator
// above it, growing with the text up to MAX_INPUT_PX and scrolling past that.
// Attachments (worker-pane theme spec §9.1): the chips live in ExecutionView
// (this input remounts on a restored draft); here they render above the
// textarea, gate the send, and paste / the `+` picker hand files up.
import { useState, useRef, useCallback, useEffect } from 'react'
import { Plus } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { canSend, type Chip } from '../../lib/nex/worker-upload'
import { useActivationFocus } from '../../hooks/useActivationFocus'
import UploadChips from './UploadChips'

/** Ceiling for the auto-grown textarea, so a long paste can't squeeze the transcript away. */
const MAX_INPUT_PX = 200

/** A field that takes typing: what the reader may be in the middle of. */
const TEXT_FIELD = 'input, textarea, select, [contenteditable]:not([contenteditable="false"])'

/**
 * Whether the reader is typing somewhere other than `self`: a text field
 * holds focus — this pane's search bar, another pane's, a terminal. A send
 * coming back then must not pull them in here, where Enter would send the
 * rest of what they type to the worker (A F5). Focus inside an open panel
 * (`role="dialog"`: the header's overflow menu, the cost panel) is kept too —
 * the reader is using it. Anything else — the body, a clicked tab (dnd-kit's
 * tabIndex=0 keeps focus on it), a stray button — is taken over, so switching
 * to the tab lands in the reply box (#1495 re-review P2-2). A field in an
 * inert subtree (an inactive tab, TabContent) is not being used, even while a
 * browser without focus fixup leaves focus on it.
 */
function typingElsewhere(self: HTMLTextAreaElement | null): boolean {
  const active = document.activeElement
  if (!active || active === self || active.closest('[inert]')) return false
  return active.matches(TEXT_FIELD) || !!active.closest('[role="dialog"]')
}

interface Props {
  /** Returning false means the send was refused before going out (e.g. too large): the text stays in the box. */
  onSend: (text: string) => void | boolean
  disabled?: boolean
  placeholder?: string
  /** The pane's tab is the one on screen (`PaneRendererProps.isActive`). */
  isActive?: boolean
  /** The pane is its tab's focus target (`PaneRendererProps.isFocusTarget`, shell cleanup spec §8.2). */
  isFocusTarget?: boolean
  /** Seeds the textarea (e.g. restoring text after a failed send). */
  initialValue?: string
  /** Files attached to the next message; one uploading or failed blocks the send. */
  chips?: readonly Chip[]
  onRemoveChip?: (key: string) => void
  /** Pasted or picked files; without it there is no `+` button and a paste is plain text. */
  onAddFiles?: (files: File[]) => void
  /** Every change of the typed text (the attachment planner counts it against the request budget). */
  onTextChange?: (text: string) => void
}

const NO_CHIPS: readonly Chip[] = []
const noop = () => {}

export default function WorkerInput({
  onSend, disabled = false, placeholder, isActive = false, isFocusTarget = false, initialValue, chips = NO_CHIPS, onRemoveChip, onAddFiles,
  onTextChange,
}: Props) {
  const t = useI18nStore((s) => s.t)
  const resolvedPlaceholder = placeholder ?? t('worker.input.placeholder')
  const [value, setValue] = useState(initialValue ?? '')
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const pickerRef = useRef<HTMLInputElement>(null)
  const gate = canSend(chips)
  const hasAttachment = chips.some((c) => c.status === 'done')

  useEffect(() => { onTextChange?.(value) }, [value, onTextChange])

  // Shell cleanup spec §8.2: the reply box focuses itself only at activation
  // (its tab shown, or mounting in the active tab), as the tab's focus target.
  // A disabled box cannot take focus (focus() is a no-op on it); the refocus
  // below covers it once enabled.
  const focusInput = useCallback(() => {
    const ta = textareaRef.current
    if (ta && !typingElsewhere(ta)) ta.focus()
  }, [])
  useActivationFocus(isActive, isFocusTarget, focusInput, { raf: true })

  // After a send comes back (disabled true → false): refocus, but only when
  // this pane is the focus target of the active tab — the reader sent from
  // here. A worker whose send returns after the reader moved to another pane
  // stays put. Mount is not a transition (the ref starts at the first value).
  const prevDisabledRef = useRef(disabled)
  const isActiveRef = useRef(isActive)
  const isFocusTargetRef = useRef(isFocusTarget)
  // Declared before the refocus effect, so the refs are current when it reads them.
  useEffect(() => {
    isActiveRef.current = isActive
    isFocusTargetRef.current = isFocusTarget
  })
  useEffect(() => {
    const reenabled = prevDisabledRef.current && !disabled
    prevDisabledRef.current = disabled
    if (!reenabled || !isActiveRef.current || !isFocusTargetRef.current) return
    const id = requestAnimationFrame(focusInput)
    return () => cancelAnimationFrame(id)
  }, [disabled, focusInput])

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
    if (!trimmed && !hasAttachment) return
    if (!gate.ok) return
    if (onSend(trimmed) === false) return
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

  function handlePaste(e: React.ClipboardEvent<HTMLTextAreaElement>) {
    if (!onAddFiles) return
    const files = Array.from(e.clipboardData?.files ?? [])
    if (files.length === 0) return
    // A rich-text app (e.g. a chat client) puts an image rendition alongside
    // the text on copy. With non-empty text present, let the ordinary text
    // paste happen and skip only the image files (that rendition); any other
    // file on the clipboard is still attached (PR #1522 A2). Files-only
    // takes over the paste and attaches everything, images included.
    if ((e.clipboardData?.getData('text/plain') ?? '') !== '') {
      const others = files.filter((f) => !f.type.startsWith('image/'))
      if (others.length > 0) onAddFiles(others)
      return
    }
    e.preventDefault()
    onAddFiles(files)
  }

  return (
    <div className={`w-full border-t border-border-subtle bg-surface-input transition-colors ${
      disabled ? 'opacity-40' : 'focus-within:border-border-active'
    }`}>
      <UploadChips chips={chips} onRemove={onRemoveChip ?? noop} />
      {!gate.ok && (
        <div data-testid="upload-block" role="status" aria-live="polite" className={`px-3 pt-1 text-xs ${gate.reason === 'failed' ? 'text-status-error' : 'text-text-muted'}`}>
          {t(gate.reason === 'failed' ? 'worker.upload.failed_remove' : 'worker.upload.wait')}
        </div>
      )}
      <div className="flex items-start">
        {onAddFiles && (
          <>
            <button type="button" onClick={() => pickerRef.current?.click()} disabled={disabled}
              aria-label={t('worker.upload.attach')} title={t('worker.upload.attach')}
              className="shrink-0 ml-1.5 mt-1.5 p-1 rounded text-text-muted hover:text-text-primary hover:bg-surface-hover cursor-pointer disabled:cursor-default">
              <Plus size={14} />
            </button>
            {/* The OS picker can outlive the input being enabled (a send went
                out, the worker ended); what it returns then is dropped (PR #1522 A3). */}
            <input ref={pickerRef} data-testid="attach-input" type="file" multiple hidden tabIndex={-1} disabled={disabled}
              onChange={(e) => {
                const files = Array.from(e.target.files ?? [])
                e.target.value = ''
                if (disabled) return
                if (files.length > 0) onAddFiles(files)
              }} />
          </>
        )}
        <textarea
          ref={textareaRef}
          role="textbox"
          value={value}
          onChange={e => { setValue(e.target.value); autoGrow() }}
          onKeyDown={handleKeyDown}
          onPaste={handlePaste}
          disabled={disabled}
          placeholder={resolvedPlaceholder}
          rows={1}
          className="block w-full bg-transparent text-text-primary placeholder-text-muted px-3 py-2.5 text-sm outline-none resize-none"
        />
      </div>
    </div>
  )
}
