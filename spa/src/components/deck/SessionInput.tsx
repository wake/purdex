// spa/src/components/deck/SessionInput.tsx — the deck's input (U3 spec §7, plan D7): Enter sends, Shift+Enter breaks a line,
// 中斷 interrupts. Sending goes to the daemon (and through the session's mod); nothing is typed into a terminal. The draft and
// the send queue live in modules keyed by the pane, not in this component (it unmounts on a tab switch).
import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react'
import { PaperPlaneRight, Stop } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { draftKey, readDraft, writeDraft } from '../../lib/conversations/draft-memory'
import { DestructiveGuard, hostSendPort, outcomeMessage, type OutcomeMessage } from '../../lib/conversations/send'
import { planSend } from '../../lib/conversations/send-plan'
import { sendQueueFor } from '../../lib/conversations/send-queue'
import type { Capabilities, ConversationItem, UserItem } from '../../lib/conversations/types'
import { QueuedMessages } from './QueuedMessages'

interface Props {
  paneKey: string
  hostId: string
  sessionId: string
  capabilities?: Capabilities
  items: readonly ConversationItem[]
  /** The header status is idle (a message the mod refused as busy is resent on the edge to idle). */
  idle: boolean
  onSwitchToTerminal: () => void
}

const REFUSAL_KEY = { empty: 'deck.send.empty', too_long: 'deck.send.too_long', needs_terminal: 'deck.send.needs_terminal' } as const

/** Re-keyed by host and session: a pane re-pointed at another session starts that session's own draft, queue and guard. */
export function SessionInput(props: Props) {
  return <SessionInputBody key={draftKey(props.paneKey, props.hostId, props.sessionId)} {...props} />
}

function SessionInputBody({ paneKey, hostId, sessionId, capabilities, items, idle, onSwitchToTerminal }: Props) {
  const t = useI18nStore((s) => s.t)
  const dKey = draftKey(paneKey, hostId, sessionId)
  const queue = sendQueueFor(dKey, () => hostSendPort(hostId, sessionId))
  const entries = useSyncExternalStore(queue.subscribe, queue.entries)
  const [draft, setDraft] = useState(() => readDraft(dKey) ?? '')
  const [hint, setHint] = useState<OutcomeMessage | null>(null)
  const guard = useRef(new DestructiveGuard())
  const users = useMemo(() => items.filter((i): i is UserItem => i.type === 'user'), [items])

  useEffect(() => { queue.setIdle(idle) }, [queue, idle])
  useEffect(() => { queue.reconcile(users) }, [queue, users, entries])

  const noMod = capabilities?.send !== 'prompt' || queue.blocked === 'no_mod'
  const change = (v: string) => { setDraft(v); writeDraft(dKey, v); setHint(null) }

  const send = () => {
    const plan = planSend(draft)
    if (!plan.ok) { if (plan.refusal !== 'empty') setHint({ key: REFUSAL_KEY[plan.refusal], tone: 'warn' }); return }
    if (guard.current.check(plan.text) === 'confirm') { setHint({ key: 'deck.send.confirm', tone: 'warn' }); return }
    queue.enqueue(plan.text)
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
      <QueuedMessages entries={entries} onUndo={undo} onRetry={(id) => queue.retry(id)} onDismiss={(id) => queue.dismiss(id)} />
      {noMod ? (
        <div data-testid="session-input-disabled" className="flex items-center gap-2 px-3 pb-2 text-sm text-text-muted">
          <span>{t('deck.send.no_mod')}</span>
          <button type="button" className="rounded border border-border-subtle px-2 py-0.5 text-text-primary" onClick={onSwitchToTerminal}>{t('deck.send.switch_terminal')}</button>
        </div>
      ) : (
        <div className="px-3 pb-2">
          {hintText && <div data-testid="session-input-hint" role="status" className={`mb-1 text-xs ${hint?.tone === 'error' ? 'text-status-error' : 'text-text-muted'}`}>{hintText}</div>}
          <div className="flex items-end gap-2">
            <textarea
              value={draft}
              rows={2}
              placeholder={t('deck.send.placeholder')}
              onChange={(e) => change(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) { e.preventDefault(); send() }
              }}
              className="min-h-10 flex-1 resize-none rounded border border-border-subtle bg-surface-primary px-2 py-1 text-sm text-text-primary"
            />
            <button type="button" aria-label={t('deck.send.interrupt')} title={t('deck.send.interrupt')} onClick={() => void interrupt()} className="rounded p-2 text-text-muted hover:text-text-primary"><Stop size={18} /></button>
            <button type="button" aria-label={t('deck.send.send')} title={t('deck.send.send')} onClick={send} className="rounded p-2 text-accent hover:opacity-80"><PaperPlaneRight size={18} /></button>
          </div>
        </div>
      )}
    </div>
  )
}
