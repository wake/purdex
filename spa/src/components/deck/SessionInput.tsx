// spa/src/components/deck/SessionInput.tsx — the deck's input (U3 spec §7, plan D7): Enter sends, Shift+Enter breaks a line,
// 中斷 interrupts. Sending goes to the daemon (and through the session's mod); nothing is typed into a terminal. The draft and
// the send queue live in modules keyed by the pane, not in this component (it unmounts on a tab switch).
import { useCallback, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore } from 'react'
import { Paperclip, PaperPlaneRight, Stop, X } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { readAttachments, removeAttachment, removeAttachmentText, forgetAttachmentsWhere, visibleAttachments, type Attachment } from '../../lib/conversations/attachment-memory'
import { startUploads, subscribeUploads, takeUnseenFailures, uploadsOf } from '../../lib/conversations/attachment-upload'
import { draftKey, readDraft, writeDraft } from '../../lib/conversations/draft-memory'
import { DestructiveGuard, hostSendPort, outcomeMessage, type OutcomeMessage } from '../../lib/conversations/send'
import { planSend } from '../../lib/conversations/send-plan'
import { sendQueueFor } from '../../lib/conversations/send-queue'
import type { Capabilities } from '../../lib/conversations/types'
import { QueuedMessages } from './QueuedMessages'

interface Props {
  paneKey: string
  hostId: string
  sessionId: string
  /** The tmux session code the daemon's upload endpoint names the session by (not the Claude session id). No code: no attachments. */
  sessionCode?: string
  capabilities?: Capabilities
  onSwitchToTerminal: () => void
}

const REFUSAL_KEY = { empty: 'deck.send.empty', too_long: 'deck.send.too_long', needs_terminal: 'deck.send.needs_terminal' } as const

/** Re-keyed by host and session: a pane re-pointed at another session starts that session's own draft, queue and guard. */
export function SessionInput(props: Props) {
  return <SessionInputBody key={draftKey(props.paneKey, props.hostId, props.sessionId)} {...props} />
}

/** The hint for a failed upload (kinds come from `AgentUploadError`; any other failure is a plain HTTP-style one). */
function uploadFailure(err: unknown, name: string): OutcomeMessage {
  const e = err as { kind?: string; status?: number }
  switch (e?.kind) {
    case 'too_large': return { key: 'deck.attach.too_large', params: { name }, tone: 'error' }
    case 'not_found': return { key: 'deck.attach.not_found', tone: 'error' }
    case 'network': return { key: 'deck.attach.network', params: { name }, tone: 'error' }
    default: return { key: 'deck.attach.http', params: { name, status: e?.status || '-' }, tone: 'error' }
  }
}

const MIN_ROWS = 2
const MAX_ROWS = 8
const px = (v: string): number => parseFloat(v) || 0

/** The box grows with its content between MIN_ROWS and MAX_ROWS lines, then scrolls (`field-sizing` is not everywhere yet). */
function fitHeight(el: HTMLTextAreaElement): void {
  const cs = getComputedStyle(el)
  const line = px(cs.lineHeight) || 20
  const chrome = px(cs.paddingTop) + px(cs.paddingBottom) + px(cs.borderTopWidth) + px(cs.borderBottomWidth)
  el.style.height = 'auto'
  const wanted = el.scrollHeight + px(cs.borderTopWidth) + px(cs.borderBottomWidth)
  const max = MAX_ROWS * line + chrome
  el.style.height = `${Math.max(MIN_ROWS * line + chrome, Math.min(wanted, max))}px`
  el.style.overflowY = wanted > max ? 'auto' : 'hidden'
}

function SessionInputBody({ paneKey, hostId, sessionId, sessionCode, capabilities, onSwitchToTerminal }: Props) {
  const t = useI18nStore((s) => s.t)
  const dKey = draftKey(paneKey, hostId, sessionId)
  const queue = sendQueueFor(dKey, () => hostSendPort(hostId, sessionId))
  const entries = useSyncExternalStore(queue.subscribe, queue.entries)
  const [draft, setDraft] = useState(() => readDraft(dKey) ?? '')
  const [hint, setHint] = useState<OutcomeMessage | null>(null)
  const guard = useRef(new DestructiveGuard())
  const [chips, setChips] = useState<readonly Attachment[]>(() => readAttachments(dKey))
  const fileInput = useRef<HTMLInputElement>(null)
  const box = useRef<HTMLTextAreaElement>(null)
  // The uploads run in a module (they outlive this input, which unmounts on a tab switch); this input shows them and, when one
  // lands or fails, takes the result from the memories.
  const uploading = useSyncExternalStore(useCallback((cb: () => void) => subscribeUploads(dKey, cb), [dKey]), () => uploadsOf(dKey))
  useEffect(() => {
    const sync = () => {
      setChips(readAttachments(dKey))
      setDraft(readDraft(dKey) ?? '')
      const failed = takeUnseenFailures(dKey)
      const last = failed[failed.length - 1]
      if (last) setHint(uploadFailure(last.error, last.name))
    }
    sync() // a landing or a failure while this input was away
    return subscribeUploads(dKey, sync)
  }, [dKey])
  useLayoutEffect(() => { if (box.current) fitHeight(box.current) }, [draft])
  // dragging files over the input: counted enter / leave (moving over a child fires leave on the parent)
  const [dragging, setDragging] = useState(false)
  const dragDepth = useRef(0)
  const endDrag = () => { dragDepth.current = 0; setDragging(false) }
  useEffect(() => {
    if (!dragging) return
    const off = (e: Event) => { if (e.type !== 'keydown' || (e as KeyboardEvent).key === 'Escape') endDrag() }
    window.addEventListener('keydown', off)
    window.addEventListener('dragend', off)
    window.addEventListener('drop', off)
    return () => { window.removeEventListener('keydown', off); window.removeEventListener('dragend', off); window.removeEventListener('drop', off) }
  }, [dragging])
  const carriesFiles = (e: React.DragEvent) => !!sessionCode && e.dataTransfer.types.includes('Files')
  // the draft is the truth: a marker line the reader deleted or rewrote takes its chip (in the memory too) with it
  const shown = visibleAttachments(draft, chips)
  useEffect(() => {
    if (shown.length === chips.length) return
    for (const a of chips) if (!shown.includes(a)) removeAttachment(dKey, a.id)
    // eslint-disable-next-line react-hooks/set-state-in-effect -- mirrors the module memory just pruned
    setChips(readAttachments(dKey))
  }, [shown, chips, dKey])

  // The queue is driven by the pane (`useSendQueueDriver`, under every view); this input only types and shows.
  // the live capability is the only authority: a 409 no_mod fails that one message and disables nothing by itself
  const noMod = capabilities?.send !== 'prompt'
  const change = (v: string) => { setDraft(v); writeDraft(dKey, v); setHint(null) }

  // Files come from a paste, a drop or the attach button. Each is saved by the daemon (no paste into the pane) and its path goes
  // into the draft when it lands (see attachment-upload); the box stays editable meanwhile.
  const attach = (files: readonly File[]) => { if (sessionCode) startUploads(dKey, hostId, sessionCode, files) }
  const dropChip = (a: Attachment) => {
    removeAttachment(dKey, a.id)
    setChips(readAttachments(dKey))
    const next = removeAttachmentText(readDraft(dKey) ?? '', a.text)
    writeDraft(dKey, next)
    setDraft(next)
  }

  const send = () => {
    // a message sent while a file is still on its way would leave without it
    if (uploading.length > 0) { setHint({ key: 'deck.attach.busy', tone: 'warn' }); return }
    const plan = planSend(draft)
    if (!plan.ok) { if (plan.refusal !== 'empty') setHint({ key: REFUSAL_KEY[plan.refusal], tone: 'warn' }); return }
    if (guard.current.check(plan.text) === 'confirm') { setHint({ key: 'deck.send.confirm', tone: 'warn' }); return }
    queue.enqueue(plan.text)
    forgetAttachmentsWhere((key) => key === dKey)
    setChips([])
    change('')
  }
  const undo = (id: string) => {
    const text = queue.undo(id)
    if (text !== undefined) change(draft ? `${text}\n${draft}` : text)
  }
  const interrupt = async () => setHint(outcomeMessage(await queue.interrupt()))

  const hintText = hint ? t(hint.key, hint.params) : ''
  return (
    <div data-testid="session-input" className="border-t border-border-subtle pt-2">
      <QueuedMessages entries={entries} onUndo={undo} onResend={(id) => { queue.resend(id) }} onDismiss={(id) => queue.dismiss(id)} />
      {noMod ? (
        <div data-testid="session-input-disabled" className="flex items-center gap-2 px-3 pb-2 text-sm text-text-muted">
          <span>{t('deck.send.no_mod')}</span>
          <button type="button" className="rounded border border-border-subtle px-2 py-0.5 text-text-primary" onClick={onSwitchToTerminal}>{t('deck.send.switch_terminal')}</button>
        </div>
      ) : (
        <div className="px-3 pb-2">
          {hintText && <div data-testid="session-input-hint" role="status" className={`mb-1 text-xs ${hint?.tone === 'error' ? 'text-status-error' : 'text-text-muted'}`}>{hintText}</div>}
          {shown.length > 0 && (
            <div className="mb-1 flex flex-wrap gap-1">
              {shown.map((a) => (
                <span key={a.id} data-testid="attachment-chip" className="inline-flex items-center gap-1 rounded border border-border-subtle bg-surface-secondary px-2 py-0.5 text-xs text-text-primary">
                  {t('deck.attach.attached', { name: a.name })}
                  <button type="button" aria-label={t('deck.attach.remove', { name: a.name })} title={t('deck.attach.remove', { name: a.name })} onClick={() => dropChip(a)} className="text-text-muted hover:text-text-primary"><X size={12} /></button>
                </span>
              ))}
            </div>
          )}
          {uploading.length > 0 && (
            <div className="mb-1 flex flex-col gap-0.5">
              {uploading.map((u) => (
                <div key={u.id} data-testid="attachment-uploading" role="status" className="text-xs text-text-muted">{t('deck.attach.uploading', { name: u.name, percent: u.percent })}</div>
              ))}
            </div>
          )}
          <div
            data-testid="session-input-drop"
            data-dragging={dragging}
            className={`flex items-end gap-2 rounded border ${dragging ? 'border-dashed border-accent' : 'border-transparent'}`}
            onDragEnter={(e) => { if (carriesFiles(e)) { dragDepth.current++; setDragging(true) } }}
            onDragLeave={(e) => { if (carriesFiles(e) && --dragDepth.current <= 0) endDrag() }}
            onDragOver={(e) => { if (carriesFiles(e)) e.preventDefault() }}
            onDrop={(e) => {
              endDrag()
              const files = [...e.dataTransfer.files]
              if (!sessionCode || files.length === 0) return
              e.preventDefault()
              attach(files)
            }}
          >
            <textarea
              ref={box}
              value={draft}
              rows={2}
              placeholder={t('deck.send.placeholder')}
              onChange={(e) => change(e.target.value)}
              onPaste={(e) => {
                // only files are taken over; a plain-text paste goes into the box as usual
                const files = [...e.clipboardData.files]
                if (!sessionCode || files.length === 0) return
                // a payload that also carries text keeps its text: the browser pastes it, the files are uploaded beside it
                if (!e.clipboardData.getData('text/plain')) e.preventDefault()
                attach(files)
              }}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) { e.preventDefault(); send() }
              }}
              style={{ overflowWrap: 'anywhere' }}
              className={`flex-1 resize-none rounded border px-2 py-1 text-sm text-text-primary ${
                // while files are dragged over, the drop style takes the focus ring's place (a focused box would hide the dashes)
                dragging ? 'border-dashed border-accent bg-accent/10 outline-none' : 'border-border-subtle bg-surface-primary'
              }`}
            />
            {sessionCode && (
              <>
                <input ref={fileInput} data-testid="attach-file-input" type="file" multiple hidden onChange={(e) => { attach([...(e.target.files ?? [])]); e.target.value = '' }} />
                <button type="button" aria-label={t('deck.attach.add')} title={t('deck.attach.add')} onClick={() => fileInput.current?.click()} className="rounded p-2 text-text-muted hover:text-text-primary"><Paperclip size={18} /></button>
              </>
            )}
            <button type="button" aria-label={t('deck.send.interrupt')} title={t('deck.send.interrupt')} onClick={() => void interrupt()} className="rounded p-2 text-text-muted hover:text-text-primary"><Stop size={18} /></button>
            <button type="button" aria-label={t('deck.send.send')} title={t('deck.send.send')} onClick={send} className="rounded p-2 text-accent hover:opacity-80"><PaperPlaneRight size={18} /></button>
          </div>
        </div>
      )}
    </div>
  )
}
