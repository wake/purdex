// spa/src/components/execution/PermissionRequestCard.tsx — the request a
// `handoff_ask` worker is waiting on, shown above the composer (permission
// channel plan Task 9; spec §5.3). Presentational: the pane decides which
// request to show and does the answering (usePermissionAnswer); the card owns
// only its own UI state — the deny note, the expanded preview, the clock.
//
// No "always allow" (PC4) and no edited input: 同意 sends the request's own
// input. 拒絕 first opens a one-line note, which becomes the deny `message`
// the model reads; Nexen caps it at 2048 **bytes**, so the note is capped by
// UTF-8 bytes, never by characters.
import { useState } from 'react'
import type { KeyboardEvent } from 'react'
import { HandPalm } from '@phosphor-icons/react'
import type { PermissionRequestState } from '../../lib/nex/permissions'
import type { PermissionAnswerError } from '../../hooks/usePermissionAnswer'
import { formatCoarseDuration } from '../../lib/nex/format-duration'
import { useElapsedTicker } from '../../hooks/useElapsedTicker'
import { useI18nStore } from '../../stores/useI18nStore'

/** Nexen's cap on a deny `message` (capability-matrix §1.14), in UTF-8 bytes. */
export const NOTE_MAX_BYTES = 2048
/** The input preview before "show all", in characters. */
export const PREVIEW_CHARS = 400

const encoder = new TextEncoder()

/** The longest prefix of `text` within `maxBytes` UTF-8 bytes, cut on a code point (a surrogate pair stays whole). */
function capUtf8(text: string, maxBytes: number): string {
  if (encoder.encode(text).length <= maxBytes) return text
  let out = ''
  let used = 0
  for (const ch of text) {
    const n = encoder.encode(ch).length
    if (used + n > maxBytes) break
    out += ch
    used += n
  }
  return out
}

/** A Bash-like input reads best as its command; anything else as pretty-printed JSON. */
function inputText(input: unknown): string {
  if (input === undefined || input === null) return ''
  if (typeof input === 'object' && !Array.isArray(input)) {
    const command = (input as Record<string, unknown>).command
    if (typeof command === 'string') return command
  }
  try {
    return JSON.stringify(input, null, 2) ?? String(input)
  } catch {
    return String(input)
  }
}

function preview(text: string): string {
  let cut = text.slice(0, PREVIEW_CHARS)
  const last = cut.charCodeAt(cut.length - 1)
  if (last >= 0xd800 && last <= 0xdbff) cut = cut.slice(0, -1)
  return `${cut}…`
}

export interface PermissionRequestCardProps {
  request: PermissionRequestState
  /** For 「subagent: …」: the asking subagent's task description, else its id. */
  agentLabel?: string
  /** An answer is in flight, or the pane is exiting / taking the worker to a terminal. */
  disabled: boolean
  /** The last failed answer for THIS request. */
  error?: Pick<PermissionAnswerError, 'code' | 'message' | 'field'>
  onAllow(): void
  /** The note as typed ('' = none; the hook trims it). */
  onDeny(note: string): void
}

export default function PermissionRequestCard({ request, agentLabel, disabled, error, onAllow, onDeny }: PermissionRequestCardProps) {
  const t = useI18nStore((s) => s.t)
  const now = useElapsedTicker(true)
  const [expanded, setExpanded] = useState(false)
  const [noteOpen, setNoteOpen] = useState(false)
  const [note, setNote] = useState('')

  const text = inputText(request.input)
  const long = text.length > PREVIEW_CHARS
  const waited = formatCoarseDuration(Math.max(0, now - request.requestedAt))
  const errorText = !error ? null
    : error.code === 'permission_not_found' ? t('execution.permission.error.not_found', { message: error.message })
    : error.code === 'invalid_permission_answer'
      ? t(error.field === 'message' ? 'execution.permission.error.invalid_message' : 'execution.permission.error.invalid_answer', { message: error.message })
    : t('execution.permission.error.generic', { message: error.message })

  const deny = () => {
    if (disabled) return
    if (!noteOpen) {
      setNoteOpen(true)
      return
    }
    onDeny(note)
  }
  const onNoteKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.nativeEvent.isComposing) return
    if (e.key === 'Enter') {
      e.preventDefault()
      deny()
    } else if (e.key === 'Escape') {
      e.preventDefault()
      setNoteOpen(false)
    }
  }

  return (
    <div data-testid="permission-card" role="group" aria-label={t('execution.permission.heading')}
      className="mx-2 mb-1.5 rounded-md border border-status-warning/40 bg-status-warning/10 px-3 py-2 text-xs">
      <div className="flex items-center gap-1.5 min-w-0">
        <HandPalm size={14} weight="fill" className="shrink-0 text-status-warning" aria-hidden />
        <span className="shrink-0 text-status-warning">{t('execution.permission.heading')}</span>
        <span data-testid="permission-tool" className="min-w-0 truncate font-medium text-text-primary">{request.displayName ?? request.toolName}</span>
        <span data-testid="permission-waited" className="ml-auto shrink-0 tabular-nums text-text-muted">
          {t('execution.permission.waited', { time: waited })}
        </span>
      </div>
      {request.description && <div className="mt-0.5 text-text-secondary">{request.description}</div>}
      {agentLabel && (
        <div data-testid="permission-agent" className="mt-0.5 text-text-muted">{t('execution.permission.subagent', { name: agentLabel })}</div>
      )}
      {text && (
        <pre data-testid="permission-input"
          className="mt-1 max-h-48 overflow-auto whitespace-pre-wrap break-all rounded bg-surface-primary px-2 py-1 font-mono text-[11px] text-text-secondary">
          {long && !expanded ? preview(text) : text}
        </pre>
      )}
      {long && (
        <button type="button" data-testid="permission-expand" onClick={() => setExpanded((v) => !v)}
          className="mt-0.5 text-text-muted hover:text-text-primary">
          {t(expanded ? 'execution.permission.collapse' : 'execution.permission.expand')}
        </button>
      )}
      {request.decisionReason && (
        <div data-testid="permission-reason" className="mt-0.5 text-text-muted">{t('execution.permission.reason', { reason: request.decisionReason })}</div>
      )}
      {request.blockedPath && (
        <div data-testid="permission-blocked-path" className="mt-0.5 break-all text-text-muted">{t('execution.permission.blocked_path', { path: request.blockedPath })}</div>
      )}
      {noteOpen && (
        <input data-testid="permission-deny-note" type="text" autoFocus value={note} disabled={disabled}
          placeholder={t('execution.permission.deny_note')} aria-label={t('execution.permission.deny_note')}
          onChange={(e) => setNote(capUtf8(e.target.value, NOTE_MAX_BYTES))} onKeyDown={onNoteKey}
          className="mt-1.5 w-full rounded border border-border-subtle bg-surface-input px-2 py-1 text-xs text-text-primary placeholder:text-text-muted focus:border-border-active focus:outline-none disabled:opacity-50" />
      )}
      {errorText && <div data-testid="permission-error" role="alert" className="mt-1 text-status-error">{errorText}</div>}
      <div className="mt-1.5 flex justify-end gap-2">
        <button type="button" data-testid="permission-deny" disabled={disabled} onClick={deny}
          className="rounded border border-border-subtle px-3 py-1 text-text-secondary hover:border-status-error/60 hover:text-status-error disabled:pointer-events-none disabled:opacity-40">
          {t(noteOpen ? 'execution.permission.deny_send' : 'execution.permission.deny')}
        </button>
        <button type="button" data-testid="permission-allow" disabled={disabled} onClick={() => { if (!disabled) onAllow() }}
          className="rounded border border-status-success/60 bg-status-success/10 px-3 py-1 font-medium text-status-success hover:bg-status-success/20 disabled:pointer-events-none disabled:opacity-40">
          {t('execution.permission.allow')}
        </button>
      </div>
    </div>
  )
}

/** The muted line an expired request leaves (spec §5.5): 「已逾時自動拒絕（N 分鐘）」, N = timeout_s / 60 rounded. */
export function PermissionExpiredNotice({ timeoutS }: { timeoutS?: number }) {
  const t = useI18nStore((s) => s.t)
  const text = typeof timeoutS === 'number' && Number.isFinite(timeoutS) && timeoutS > 0
    ? t('execution.permission.expired', { n: Math.round(timeoutS / 60) })
    : t('execution.permission.expired_plain')
  return <div data-testid="permission-expired" role="status" className="mx-2 mb-1 text-xs text-text-muted">{text}</div>
}
