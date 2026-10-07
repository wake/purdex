// spa/src/components/room/WorkerInput.tsx — the worker pane's reply field
// (spec §3.1.1 #6, §4.8): full width, no border box, one hairline separator
// above it, growing with the text up to MAX_INPUT_PX and scrolling past that.
// Attachments (worker-pane theme spec §9.1): the chips live in ExecutionView
// (this input remounts on a restored draft); here they render above the
// textarea, gate the send, and paste / the `+` picker hand files up.
import { useState, useRef, useCallback, useEffect, useLayoutEffect } from 'react'
import { Plus } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { canSend, type Chip } from '../../lib/nex/worker-upload'
import { useActivationFocus, type FocusCause } from '../../hooks/useActivationFocus'
import UploadChips from './UploadChips'
import { shouldNavigate, type HistoryDir, type RecallMark } from '../../hooks/useInputHistory'

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
  /**
   * This pane's own send is in flight. Its end (true → false) is the only
   * thing that refocuses the box after activation; `disabled` also covers
   * stream loss, history load, encoding and take-back (P5 review A2).
   */
  pendingSend?: boolean
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
  /** A turn is live: Esc on an empty box and Ctrl+C with no selection call `onInterrupt`. */
  turnLive?: boolean
  onInterrupt?: () => void
  /**
   * Input-history step (ArrowUp at the very start / ArrowDown at the very end of the box). `current` is the
   * box text; the answer is the text to show instead — `walking` says a recalled entry is shown, so the box
   * parks the caret where the next press in the same direction works — or null for "nothing to recall": the
   * key then behaves as an ordinary caret move.
   */
  onHistoryNav?: (dir: HistoryDir, current: string) => { text: string; walking: boolean } | null
}

const NO_CHIPS: readonly Chip[] = []
const noop = () => {}

export default function WorkerInput({
  onSend, disabled = false, pendingSend = false, placeholder, isActive = false, isFocusTarget = false, initialValue, chips = NO_CHIPS, onRemoveChip, onAddFiles,
  onTextChange, turnLive = false, onInterrupt, onHistoryNav,
}: Props) {
  const t = useI18nStore((s) => s.t)
  const resolvedPlaceholder = placeholder ?? t('worker.input.placeholder')
  const [value, setValue] = useState(initialValue ?? '')
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  // Where the last recall parked the caret (see `RecallMark`); applied after the render that shows the text.
  const recallMark = useRef<RecallMark | null>(null)
  const [recallTick, setRecallTick] = useState(0)
  const pickerRef = useRef<HTMLInputElement>(null)
  const gate = canSend(chips)
  const hasAttachment = chips.some((c) => c.status === 'done')

  useEffect(() => { onTextChange?.(value) }, [value, onTextChange])

  // Shell cleanup spec §8.2: the reply box focuses itself only at activation
  // (its tab shown, or mounting in the active tab), as the tab's focus target.
  // `force`: an explicit request for this pane (a notification click, #1840 A1) — the reader asked to come here, so
  // a field they were typing in elsewhere does not keep the focus.
  const focusInput = useCallback((force = false) => {
    const ta = textareaRef.current
    if (ta && (force || !typingElsewhere(ta))) ta.focus()
  }, [])
  // A disabled box cannot take focus (history still loading, the stream not
  // back yet), so an activation that finds it disabled stays pending: the
  // first time the box is enabled, it takes focus unless the reader is typing
  // elsewhere (P5 review follow-up). Leaving the tab or losing the target
  // cancels it, so a pending focus always belongs to the active tab's focus
  // target. No other enabling of the box focuses it; a send coming back does
  // (below).
  const pendingActivationRef = useRef<FocusCause | null>(null)
  const focusAtActivation = useCallback((cause: FocusCause) => {
    // The box is readOnly + aria-disabled (not `disabled`, see the textarea), so ask the attribute.
    if (textareaRef.current?.getAttribute('aria-disabled') === 'true') pendingActivationRef.current = cause
    else focusInput(cause === 'request')
  }, [focusInput])
  useActivationFocus(isActive, isFocusTarget, focusAtActivation, { raf: true })
  useEffect(() => {
    if (!isActive || !isFocusTarget) pendingActivationRef.current = null
  }, [isActive, isFocusTarget])
  // Read when a scheduled focus frame runs (here and after a send, below).
  const isActiveRef = useRef(isActive)
  const isFocusTargetRef = useRef(isFocusTarget)
  const disabledRef = useRef(disabled)
  // Declared before the effects that schedule those frames, so the refs are current when they run.
  useEffect(() => {
    isActiveRef.current = isActive
    isFocusTargetRef.current = isFocusTarget
    disabledRef.current = disabled
  })
  const prevDisabledRef = useRef(disabled)
  useEffect(() => {
    const enabled = prevDisabledRef.current && !disabled
    prevDisabledRef.current = disabled
    const pending = pendingActivationRef.current
    if (!enabled || pending === null) return
    // This first enabling uses it up now, focused or not: if the box is
    // disabled again before the frame runs, the cleanup cancels this one
    // focus and a later enabling is not the activation (P5 re-review).
    pendingActivationRef.current = null
    // Checked again when the frame runs: a click on another pane before then
    // cancels it (P5 review A1); the reader typing elsewhere keeps their field, unless they asked for this pane.
    const id = requestAnimationFrame(() => {
      if (isActiveRef.current && isFocusTargetRef.current && !disabledRef.current) focusInput(pending === 'request')
    })
    return () => cancelAnimationFrame(id)
  }, [disabled, focusInput])

  // After this pane's own send comes back (`pendingSend` true → false; P5
  // review A2): refocus, but only when this pane is the focus target of the
  // active tab — the reader sent from here — and the box is enabled, all
  // checked when the frame runs. A worker whose send returns after the reader
  // moved to another pane stays put; so does one still disabled for another
  // reason (the worker ended, the stream died), and its enabling later is
  // not this send. The aggregated `disabled` never refocuses here: its other
  // causes (stream back, history loaded, encoding or take-back done) are not
  // the reader's send — only a pending activation (above) waits on it. Mount
  // is not a transition (the ref starts at the first value).
  const prevPendingSendRef = useRef(pendingSend)
  useEffect(() => {
    const sendCameBack = prevPendingSendRef.current && !pendingSend
    prevPendingSendRef.current = pendingSend
    if (!sendCameBack) return
    const id = requestAnimationFrame(() => {
      if (isActiveRef.current && isFocusTargetRef.current && !disabledRef.current) focusInput()
    })
    return () => cancelAnimationFrame(id)
  }, [pendingSend, focusInput])

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
    recallMark.current = null
    setValue('')
    if (textareaRef.current) {
      textareaRef.current.style.height = 'auto'
      textareaRef.current.style.overflowY = 'hidden'
    }
  }

  // The caret goes where the next press in the walking direction works: the start after Up, the end after Down.
  useLayoutEffect(() => {
    const ta = textareaRef.current
    const mark = recallMark.current
    if (!ta || !mark || ta.value !== mark.text) return
    ta.setSelectionRange(mark.pos, mark.pos)
  }, [recallTick])

  function handleKeyDown(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    // An IME commit (Enter that picks a candidate) is not a send; 229 is what
    // Safari / older Chromium report for it even with isComposing already false.
    if (e.nativeEvent.isComposing || e.keyCode === 229) return
    if ((e.key === 'ArrowUp' || e.key === 'ArrowDown') && onHistoryNav && !disabled && !e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey) {
      const dir: HistoryDir = e.key === 'ArrowUp' ? 'up' : 'down'
      const ta = e.currentTarget
      if (shouldNavigate(dir, ta, false, recallMark.current)) {
        const next = onHistoryNav(dir, value)
        if (next !== null) {
          e.preventDefault()
          recallMark.current = next.walking ? { text: next.text, pos: dir === 'up' ? 0 : next.text.length } : null
          setValue(next.text)
          setRecallTick((n) => n + 1)
          requestAnimationFrame(autoGrow)
          return
        }
      }
    }
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      if (!disabled) send()
      return
    }
    if (!turnLive || !onInterrupt) return
    const ta = e.currentTarget
    if (e.key === 'Escape' && value === '') {
      e.preventDefault()
      onInterrupt()
    } else if (e.ctrlKey && !e.metaKey && !e.altKey && !e.shiftKey && e.key.toLowerCase() === 'c' && ta.selectionStart === ta.selectionEnd) {
      e.preventDefault()
      onInterrupt()
    }
  }

  function handlePaste(e: React.ClipboardEvent<HTMLTextAreaElement>) {
    if (!onAddFiles) return
    if (disabled) { e.preventDefault(); return }
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
      disabled ? 'opacity-40 cursor-default' : 'focus-within:border-border-active'
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
          onChange={e => { if (disabled) return; setValue(e.target.value); autoGrow() }}
          onKeyDown={handleKeyDown}
          onPaste={handlePaste}
          // readOnly + aria-disabled, not `disabled`: a disabled control swallows
          // drag events, so a file dropped on it can skip the pane's drop handlers
          // and Electron's default (open the file) fires. readOnly keeps them.
          readOnly={disabled}
          aria-disabled={disabled}
          placeholder={resolvedPlaceholder}
          rows={1}
          className="block w-full bg-transparent text-text-primary placeholder-text-muted px-3 py-2.5 text-sm outline-none resize-none"
        />
      </div>
    </div>
  )
}
