// spa/src/components/deck/SessionInput.tsx — the deck's input (U3 spec §7, plan D7): Enter sends, Shift+Enter breaks a line,
// 中斷 interrupts. Sending goes to the daemon (and through the session's mod); nothing is typed into a terminal. The draft and
// the send queue live in modules keyed by the pane, not in this component (it unmounts on a tab switch).
import { useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore } from 'react'
import { Paperclip, PaperPlaneRight, Stop } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useDeckTypeStore } from '../../stores/useDeckTypeStore'
import { expandPlaceholders, removePlaceholderText } from '../../lib/conversations/image-placeholder'
import { reconcileAttachments, readAttachments, removeAttachment, removeAttachmentText, takeAttachments, visibleAttachments, type Attachment } from '../../lib/conversations/attachment-memory'
import { registerDropTarget } from '../../lib/conversations/pane-drop'
import { startUploads, subscribeUploads, takeUnseenFailures, useUploads } from '../../lib/conversations/attachment-upload'
import { draftKey, readDraft, writeDraft } from '../../lib/conversations/draft-memory'
import { clearDraft, discardCleared, hasCleared, undoClear, useHasCleared } from '../../lib/conversations/draft-clear'
import { uploadFailureText } from '../../lib/upload-failure'
import { DestructiveGuard, hostSendPort, outcomeMessage, type OutcomeMessage } from '../../lib/conversations/send'
import { planSend } from '../../lib/conversations/send-plan'
import { sendQueueFor } from '../../lib/conversations/send-queue'
import { recallHeld, subscribeRecall } from '../../lib/conversations/draft-recall'
import type { Capabilities } from '../../lib/conversations/types'
import { useFsImage } from '../../lib/conversations/fs-image'
import { AttachmentThumb } from './AttachmentThumb'
import { DraftTags } from './DraftTags'
import { QueuedMessages } from './QueuedMessages'

interface Props {
  paneKey: string
  hostId: string
  sessionId: string
  /** The tmux session code the daemon's upload endpoint names the session by (not the Claude session id). No code: no attachments. */
  sessionCode?: string
  capabilities?: Capabilities
  /** A question waits in the dock: the placeholder says to answer there. */
  asking?: boolean
  /** The held and in-flight messages are drawn at the end of the stream (PendingTail), not above this box (the deck and the chat do; a bare input keeps them here). */
  pendingInStream?: boolean
  onSwitchToTerminal: () => void
}

const REFUSAL_KEY = { empty: 'deck.send.empty', too_long: 'deck.send.too_long', needs_terminal: 'deck.send.needs_terminal' } as const

/** Re-keyed by host and session: a pane re-pointed at another session starts that session's own draft, queue and guard. */
export function SessionInput(props: Props) {
  return <SessionInputBody key={draftKey(props.paneKey, props.hostId, props.sessionId)} {...props} />
}

/** The hint for a failed upload (kinds come from `AgentUploadError`; any other failure is a plain HTTP-style one). */
function uploadFailure(err: unknown, name: string): OutcomeMessage {
  return { ...uploadFailureText(err, name), tone: 'error' }
}

const MIN_ROWS = 1
const MAX_ROWS = 8
const px = (v: string): number => parseFloat(v) || 0

/** The box grows with its content between MIN_ROWS and MAX_ROWS lines, then scrolls (`field-sizing` is not everywhere yet). */
function fitHeight(el: HTMLTextAreaElement): void {
  const cs = getComputedStyle(el)
  const line = px(cs.lineHeight) || 20
  const chrome = px(cs.paddingTop) + px(cs.paddingBottom) + px(cs.borderTopWidth) + px(cs.borderBottomWidth)
  // `auto` makes the footer shorter for the moment the text is measured. The scrolling box above is then taller, and the browser
  // clamps its scrollTop to the smaller maximum — a reader at the bottom is left scrolled up by the difference and the footer's
  // growth is no longer pinned (footer-pin). The wrapper holds its height meanwhile.
  const hold = el.parentElement
  if (hold) hold.style.minHeight = `${hold.offsetHeight}px`
  el.style.height = 'auto'
  const wanted = el.scrollHeight + px(cs.borderTopWidth) + px(cs.borderBottomWidth)
  const max = MAX_ROWS * line + chrome
  el.style.height = `${Math.max(MIN_ROWS * line + chrome, Math.min(wanted, max))}px`
  if (hold) hold.style.minHeight = ''
  // scrollHeight is a whole number and a fractional line height (1.65 × 13px) makes `max` one: a full MAX_ROWS is not an overflow
  el.style.overflowY = wanted > max + 1 ? 'auto' : 'hidden'
}

/** A chip's thumbnail: the local file's picture, or (a chip restored without it) the host's copy read through the daemon. */
function ChipThumb({ hostId, a, onRemove, removeLabel }: { hostId: string; a: Attachment; onRemove: () => void; removeLabel: string }) {
  const image = !!a.image
  const remote = useFsImage(hostId, a.path, image && !a.previewUrl)
  const src = a.previewUrl ?? (remote.status === 'ready' ? remote.url : null)
  return <AttachmentThumb name={a.name} image={image} src={src} loading={remote.status === 'loading'} size={a.size} onRemove={onRemove} removeLabel={removeLabel} />
}

function SessionInputBody({ paneKey, hostId, sessionId, sessionCode, capabilities, asking, pendingInStream, onSwitchToTerminal }: Props) {
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
  const uploading = useUploads(dKey)
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
  // a new type size or line height changes how tall the same text is: the box is fitted again
  const typeFont = useDeckTypeStore((s) => s.fontSize)
  const typeLine = useDeckTypeStore((s) => s.lineHeight)
  useLayoutEffect(() => { if (box.current) fitHeight(box.current) }, [draft, typeFont, typeLine])
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
  // the draft is the truth for what is shown and sent: a marker the reader deleted or rewrote hides its chip. The chip is NOT
  // destroyed (the browser's undo can bring the marker back, and the chip and its picture with it): the memory only keeps the clock
  // (reconcileAttachments), and a hidden chip is let go by a send, a release, its remove button or a later attach.
  const shown = visibleAttachments(draft, chips)

  // The queue is driven by the pane (`useSendQueueDriver`, under every view); this input only types and shows.
  // the live capability is the only authority: a 409 no_mod fails that one message and disables nothing by itself
  const noMod = capabilities?.send !== 'prompt'
  const change = (v: string) => { setDraft(v); writeDraft(dKey, v); reconcileAttachments(dKey, v); setHint(null); if (v !== '') discardCleared(dKey) } // typing something new ends the undo window
  const canUndoClear = useHasCleared(dKey)
  const restoreCleared = () => {
    const text = undoClear(dKey)
    if (text === null) return
    setDraft(text)
    setChips(readAttachments(dKey))
    setHint(null)
    box.current?.focus()
  }

  // Files come from a paste, a drop or the attach button. Each is saved by the daemon (no paste into the pane) and its path goes
  // into the draft when it lands (see attachment-upload); the box stays editable meanwhile.
  const attach = (files: readonly File[]) => { if (sessionCode) startUploads(dKey, hostId, sessionCode, files) }
  // the pane takes a drop anywhere on it and hands the files to this input (usePaneFileDrop)
  const dropEl = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!sessionCode || !dropEl.current) return
    return registerDropTarget(dropEl.current, (files) => startUploads(dKey, hostId, sessionCode, files))
  }, [dKey, hostId, sessionCode])
  const dropChip = (a: Attachment) => {
    removeAttachment(dKey, a.id)
    setChips(readAttachments(dKey))
    const current = readDraft(dKey) ?? ''
    const next = a.seq === undefined ? removeAttachmentText(current, a.text) : removePlaceholderText(current, a.text)
    writeDraft(dKey, next)
    setDraft(next)
  }

  const send = () => {
    // a message sent while a file is still on its way would leave without it
    if (uploading.length > 0) { setHint({ key: 'deck.attach.busy', tone: 'warn' }); return }
    // the draft holds the short `[Image #N]`; what is sent is the marker the agent reads (the draft itself stays as it is until it is sent)
    const plan = planSend(expandPlaceholders(draft, shown))
    if (!plan.ok) { if (plan.refusal !== 'empty') setHint({ key: REFUSAL_KEY[plan.refusal], tone: 'warn' }); return }
    if (guard.current.check(plan.text) === 'confirm') { setHint({ key: 'deck.send.confirm', tone: 'warn' }); return }
    // the queue keeps the box as typed (placeholders) and its chips (previews alive) while the App holds the message: Undo and Up bring it back whole
    queue.enqueue(plan.text, { draft, chips: takeAttachments(dKey) })
    discardCleared(dKey)
    setChips([])
    change('')
  }
  // Messages the queue gave back (the Up key here, or an Undo wherever the held messages are drawn) land in the memories (draft-recall); read them again.
  useEffect(() => subscribeRecall(dKey, () => {
    setDraft(readDraft(dKey) ?? '')
    setChips(readAttachments(dKey))
    setHint(null)
    box.current?.focus()
  }), [dKey])
  const undo = (id: string) => { recallHeld(dKey, queue, id) }
  const interrupt = async () => setHint(outcomeMessage(await queue.interrupt()))

  const hintText = hint ? t(hint.key, hint.params) : ''
  return (
    <div data-testid="session-input">
      {!pendingInStream && <QueuedMessages entries={entries} onUndo={undo} onResend={(id) => { queue.resend(id) }} onDismiss={(id) => queue.dismiss(id)} />}
      {noMod ? (
        <div data-testid="session-input-disabled" className="flex items-center gap-2 border-t border-border-subtle px-3 py-2 deck-t-up text-text-muted">
          <span>{t('deck.send.no_mod')}</span>
          <button type="button" className="rounded border border-border-subtle px-2 py-0.5 text-text-primary" onClick={onSwitchToTerminal}>{t('deck.send.switch_terminal')}</button>
        </div>
      ) : (
        <div>
          {hintText && <div data-testid="session-input-hint" role="status" className={`mb-1 px-3 pt-1 deck-t-down ${hint?.tone === 'error' ? 'text-status-error' : 'text-text-muted'}`}>{hintText}</div>}
          {(shown.length > 0 || uploading.length > 0) && (
            // v3: tinted only, apart from the messages above (a top line would double the input row's own line)
            <div data-testid="attachment-area" className="flex flex-wrap gap-2 bg-text-primary/5 px-3 py-2">
              {shown.map((a) => (
                <span key={a.id} data-testid="attachment-chip" className="inline-flex">
                  <ChipThumb hostId={hostId} a={a} removeLabel={t('deck.attach.remove', { name: a.name })} onRemove={() => dropChip(a)} />
                </span>
              ))}
              {uploading.map((u) => (
                <span key={u.id} className="inline-flex">
                  <AttachmentThumb name={u.name} image={u.image} src={u.previewUrl} size={u.size} percent={u.percent} progressLabel={t('deck.attach.uploading', { name: u.name, percent: u.percent })} />
                </span>
              ))}
            </div>
          )}
          <div
            ref={dropEl}
            data-testid="session-input-drop"
            data-dragging={dragging}
            className={`flex items-end gap-2 border-t px-3 py-1.5 ${dragging ? 'border-dashed border-accent' : 'border-[#808080]'}`} /* proto v2: terminal-style input, a 1px #808080 rule above only (the session status row below is a block of its own) */
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
            <div className="relative min-w-0 flex-1">
              <DraftTags draft={draft} seqs={new Set(shown.flatMap((a) => (a.seq === undefined ? [] : [a.seq])))} box={box} />
            <textarea
              ref={box}
              value={draft}
              rows={1}
              placeholder={t(asking ? 'deck.dock.input_placeholder' : 'deck.send.placeholder')}
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
                // a key pressed while an IME is composing belongs to the IME (Esc cancels the composition, not the draft)
                if (e.nativeEvent.isComposing || e.keyCode === 229) return
                if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send(); return }
                // Esc clears the draft and its attachments (undo for a few seconds); with nothing to clear it does nothing at all
                if (e.key === 'Escape' && (draft !== '' || uploading.length > 0)) { // what is on screen (hidden chips ride along with a clear, but do not by themselves take an Esc)
                  e.preventDefault(); e.stopPropagation()
                  clearDraft(dKey)
                  setDraft(''); setChips([]); setHint(null)
                  return
                }
                if ((e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey && e.key.toLowerCase() === 'z' && hasCleared(dKey)) { e.preventDefault(); restoreCleared() }
                // Up in an empty box (nothing typed, no chip, no upload on its way) calls back every message the App still holds, as the terminal does;
                // anywhere else Up is the caret's
                if (e.key === 'ArrowUp' && !e.shiftKey && !e.altKey && !e.metaKey && !e.ctrlKey && draft === '' && shown.length === 0 && uploading.length === 0 && !hasCleared(dKey) && queue.heldCount() > 0) { // (a draft Esc just cleared is not overwritten: its undo window is the box's)
                  e.preventDefault()
                  recallHeld(dKey, queue)
                }
              }}
              style={{ overflowWrap: 'anywhere' }}
              className={`relative block w-full resize-none rounded-none border-0 px-0 pb-1.5 pt-0 deck-t-base text-text-primary placeholder:text-text-secondary outline-none focus:outline-none focus:ring-0 focus-visible:outline-none ${
                // proto v2: no border, radius, outline or ring (focused or not); a drag over tints the row
                dragging ? 'bg-accent/10' : 'bg-transparent'
              }`}
            />
            </div>
            {sessionCode && (
              <>
                <input ref={fileInput} data-testid="attach-file-input" type="file" multiple hidden onChange={(e) => { attach([...(e.target.files ?? [])]); e.target.value = '' }} />
                <button type="button" aria-label={t('deck.attach.add')} title={t('deck.attach.add')} onClick={() => fileInput.current?.click()} className="flex h-8 w-8 items-center justify-center rounded text-text-muted hover:text-text-primary"><Paperclip size={18} /></button>
              </>
            )}
            <button type="button" aria-label={t('deck.send.interrupt')} title={t('deck.send.interrupt')} onClick={() => void interrupt()} className="flex h-8 w-8 items-center justify-center rounded text-text-muted hover:text-text-primary"><Stop size={18} /></button>
            <button type="button" aria-label={t('deck.send.send')} title={t('deck.send.send')} onClick={send} className="flex h-8 w-8 items-center justify-center rounded text-accent hover:opacity-80"><PaperPlaneRight size={18} /></button>
          </div>
          {canUndoClear && (
            <button type="button" data-testid="draft-cleared" onClick={restoreCleared} className="block w-full cursor-pointer px-3 pb-1 text-left deck-t-down text-text-muted hover:text-text-primary">
              {t('deck.draft.cleared')}
            </button>
          )}
        </div>
      )}
    </div>
  )
}
