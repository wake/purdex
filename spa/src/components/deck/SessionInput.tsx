// spa/src/components/deck/SessionInput.tsx — the deck's input (U3 spec §7, plan D7): Enter sends, Shift+Enter breaks a line,
// 中斷 interrupts. Sending goes to the daemon (and through the session's mod); nothing is typed into a terminal. The draft and
// the send queue live in modules keyed by the pane, not in this component (it unmounts on a tab switch).
import { useEffect, useRef, useState, useSyncExternalStore } from 'react'
import { Paperclip, PaperPlaneRight, Stop, X } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { agentUploadToPath } from '../../lib/host-api'
import { addAttachment, attachmentText, insertAttachmentText, isImageFile, readAttachments, removeAttachment, removeAttachmentText, forgetAttachmentsWhere, trackUpload, visibleAttachments, type Attachment } from '../../lib/conversations/attachment-memory'
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

interface Uploading { id: string; name: string; percent: number }

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

let attachSeq = 0

function SessionInputBody({ paneKey, hostId, sessionId, sessionCode, capabilities, onSwitchToTerminal }: Props) {
  const t = useI18nStore((s) => s.t)
  const dKey = draftKey(paneKey, hostId, sessionId)
  const queue = sendQueueFor(dKey, () => hostSendPort(hostId, sessionId))
  const entries = useSyncExternalStore(queue.subscribe, queue.entries)
  const [draft, setDraft] = useState(() => readDraft(dKey) ?? '')
  const [hint, setHint] = useState<OutcomeMessage | null>(null)
  const guard = useRef(new DestructiveGuard())
  const [chips, setChips] = useState<readonly Attachment[]>(() => readAttachments(dKey))
  const [uploading, setUploading] = useState<readonly Uploading[]>([])
  const fileInput = useRef<HTMLInputElement>(null)
  // uploads in flight belong to this mounted input: leaving the pane cancels them (nothing half-done lands in the draft)
  const inflight = useRef(new Set<AbortController>())
  useEffect(() => { const set = inflight.current; return () => { for (const c of set) c.abort() } }, [])
  // the draft is the truth: a marker line the reader deleted or rewrote takes its chip (in the memory too) with it
  const shown = visibleAttachments(draft, chips)
  useEffect(() => {
    if (shown.length === chips.length) return
    for (const a of chips) if (!shown.includes(a)) removeAttachment(dKey, a.id)
    setChips(readAttachments(dKey))
  }, [shown, chips, dKey])

  // The queue is driven by the pane (`useSendQueueDriver`, under every view); this input only types and shows.
  // the live capability is the only authority: a 409 no_mod fails that one message and disables nothing by itself
  const noMod = capabilities?.send !== 'prompt'
  const change = (v: string) => { setDraft(v); writeDraft(dKey, v); setHint(null) }

  // Files come from a paste, a drop or the attach button. Each is saved by the daemon (no paste into the pane) and its path goes
  // into the draft; the box stays editable meanwhile, so the landing reads the draft from the memory, not from this render.
  const attach = (files: readonly File[]) => {
    if (!sessionCode) return
    for (const file of files) {
      const id = `att-${++attachSeq}`
      const ctl = new AbortController()
      inflight.current.add(ctl)
      const untrack = trackUpload(dKey, ctl)
      setUploading((u) => [...u, { id, name: file.name, percent: 0 }])
      const settle = () => { inflight.current.delete(ctl); untrack(); setUploading((u) => u.filter((x) => x.id !== id)) }
      agentUploadToPath(hostId, file, sessionCode, {
        signal: ctl.signal,
        onProgress: (percent) => setUploading((u) => u.map((x) => (x.id === id ? { ...x, percent } : x))),
      }).then(({ path }) => {
        settle()
        // the input was left or the pane released while the answer was on its way: nothing may be rebuilt
        if (ctl.signal.aborted) return
        const text = attachmentText(path, isImageFile(file))
        const next = insertAttachmentText(readDraft(dKey) ?? '', text)
        writeDraft(dKey, next)
        setDraft(next)
        addAttachment(dKey, { id, name: file.name, path, text })
        setChips(readAttachments(dKey))
      }, (err) => {
        settle()
        if ((err as { kind?: string })?.kind !== 'aborted') setHint(uploadFailure(err, file.name))
      })
    }
  }
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
            className="flex items-end gap-2"
            onDragOver={(e) => { if (sessionCode && e.dataTransfer.types.includes('Files')) e.preventDefault() }}
            onDrop={(e) => {
              const files = [...e.dataTransfer.files]
              if (!sessionCode || files.length === 0) return
              e.preventDefault()
              attach(files)
            }}
          >
            <textarea
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
              className="min-h-10 flex-1 resize-none rounded border border-border-subtle bg-surface-primary px-2 py-1 text-sm text-text-primary"
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
