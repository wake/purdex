// spa/src/components/deck/QuickReplies.tsx — the quick replies above the deck / chat input, for a terminal session and a worker alike (#2664). One row of
// short texts from the host's list (`lib/quick-replies`, the settings page edits it); a tap sends that text the way the Send button does — it goes
// into the pane's own send queue, so the adapter the pane is bound to decides the rest (a terminal: the typed submit after the App's undo window; a
// worker: Nexen under the pane's lease) and the message can be taken back while the App still holds it. What is typed in the box stays where it is.
// The row steps aside for a dock card (a question or a permission request waits above the input) and is not drawn where the conversation cannot be
// sent to. Collapsing it is a per-device setting (`useWorkerSettingsStore.quickRepliesCollapsed`), so it outlives the component.
import { useMemo, useRef, useState } from 'react'
import { CaretDown, CaretRight } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useWorkerSettingsStore } from '../../stores/useWorkerSettingsStore'
import { useQuickReplies } from '../../lib/quick-replies'
import { pickAdapter } from '../../lib/conversations/deck-adapter'
import { terminalAdapter } from '../../lib/conversations/terminal-adapter'
import { draftKey } from '../../lib/conversations/draft-memory'
import { sendQueueFor } from '../../lib/conversations/send-queue'
import { DestructiveGuard } from '../../lib/conversations/send'
import { planSend } from '../../lib/conversations/send-plan'
import type { Capabilities } from '../../lib/conversations/types'

interface Props {
  paneKey: string
  hostId: string
  sessionId: string
  capabilities: Capabilities | undefined
  /** A question or a permission request waits in the dock: the row steps aside. */
  asking: boolean
  /** The pane cannot take a message now (a worker's stream is gone): no row. */
  locked?: boolean
}

export function QuickReplies({ paneKey, hostId, sessionId, capabilities, asking, locked = false }: Props) {
  const t = useI18nStore((s) => s.t)
  const replies = useQuickReplies(hostId)
  const collapsed = useWorkerSettingsStore((s) => s.quickRepliesCollapsed)
  const setCollapsed = useWorkerSettingsStore((s) => s.setQuickRepliesCollapsed)
  const dKey = draftKey(paneKey, hostId, sessionId)
  // The same choice the input makes: what the pane bound for this key (a worker's), else, for a conversation another back-end owns, nothing,
  // else the terminal's. Only `canSend` is read here; the send itself goes through the queue the pane already holds for the key.
  const terminal = useMemo(() => terminalAdapter(hostId, sessionId), [hostId, sessionId])
  const adapter = pickAdapter(dKey, capabilities, terminal)
  const guard = useRef(new DestructiveGuard())
  const [confirming, setConfirming] = useState(false)

  if (replies.length === 0 || asking || locked || !adapter.canSend(capabilities)) return null

  const send = (text: string) => {
    const plan = planSend(text)
    if (!plan.ok) return
    if (guard.current.check(plan.text) === 'confirm') { setConfirming(true); return }
    setConfirming(false)
    // No box to keep: the message is only its text, so Undo puts the text in front of what is typed
    sendQueueFor(dKey, adapter.port).enqueue(plan.text)
  }

  const label = t(collapsed ? 'worker.quick_replies.expand' : 'worker.quick_replies.collapse')
  const Chevron = collapsed ? CaretRight : CaretDown
  return (
    <div data-testid="quick-replies" className="px-2 py-1.5">
      <div className="flex items-center gap-1">
        <button type="button" data-testid="quick-reply-toggle" aria-expanded={!collapsed} aria-label={label} title={label}
          onClick={() => setCollapsed(!collapsed)}
          className="shrink-0 cursor-pointer rounded p-1 text-text-muted hover:bg-surface-hover hover:text-text-primary">
          <Chevron size={12} />
        </button>
        {!collapsed && (
          <div className="flex min-w-0 flex-1 gap-1.5 overflow-x-auto whitespace-nowrap [scrollbar-width:none]">
            {replies.map((r) => (
              <button key={r.id} type="button" data-testid="quick-reply" title={r.text} onClick={() => send(r.text)}
                className="min-h-9 max-w-[16rem] shrink-0 cursor-pointer truncate rounded-full border border-border-subtle bg-surface-secondary px-3.5 deck-t-down text-text-secondary transition-colors hover:border-border-active hover:text-text-primary">
                {r.text}
              </button>
            ))}
          </div>
        )}
      </div>
      {confirming && !collapsed && <div data-testid="quick-reply-note" className="px-1 pt-1 deck-t-down text-status-warning-text">{t('deck.send.confirm')}</div>}
    </div>
  )
}
