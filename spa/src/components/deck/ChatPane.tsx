// spa/src/components/deck/ChatPane.tsx — what a session pane shows in its chat view (U3 plan D1, D11): the conversation the
// pane holds (`useConversationOfPane`, mounted by SessionPaneContent for every view) as the chat, or why it cannot be read,
// or the loading moment. The twin of DeckPane: same states, the chat's own wording (UnreadableState). Takes focus when the
// pane switches to it (the input first), and hands its footer the same context the deck does.
import { useMemo, useRef } from 'react'
import { useActivationFocus } from '../../hooks/useActivationFocus'
import type { PaneConversation, PaneConversationUnreadable } from '../../hooks/useConversationOfPane'
import { useI18nStore } from '../../stores/useI18nStore'
import { ChatHeader } from './ChatHeader'
import { ChatView } from './ChatView'
import type { DeckViewProps } from './DeckView'
import { footerContext } from './footer-context'
import { UnreadableState, type UnreadableReason } from './UnreadableState'

interface Props {
  paneId: string
  conversation: PaneConversation
  /** The tab's name, for the header until the conversation brings its own title. */
  title: string
  isActive: boolean
  isFocusTarget: boolean
  onSwitchToTerminal: () => void
  footer?: DeckViewProps['footer']
}

const REASON: Record<PaneConversationUnreadable, UnreadableReason> = {
  no_session: 'no_session', not_found: 'not_found', provider_unsupported: 'unsupported', unreachable: 'offline',
}

export function ChatPane({ paneId, conversation, title, isActive, isFocusTarget, onSwitchToTerminal, footer }: Props) {
  const t = useI18nStore((s) => s.t)
  const ref = useRef<HTMLDivElement>(null)
  // Switching to the chat focuses its input when it has one (plan D2), else the frame (loading, unreadable).
  useActivationFocus(isActive, isFocusTarget, () => (ref.current?.querySelector('textarea') ?? ref.current)?.focus(), { raf: true })

  const ready = conversation.state === 'ready' ? conversation : null
  const doc = ready?.entry?.doc
  const items = useMemo(() => (doc ? doc.turns.flatMap((x) => x.items) : []), [doc])

  const frame = (reason: UnreadableReason, onRetry?: () => void) => (
    <div data-testid="chat-view" className="flex h-full min-h-0 flex-col">
      <ChatHeader title={title} status="unknown" />
      <div className="min-h-0 flex-1"><UnreadableState reason={reason} onRetry={onRetry} onSwitchToTerminal={onSwitchToTerminal} /></div>
    </div>
  )

  let body
  if (conversation.state === 'off') {
    // Not a Claude Code session (or its host does not serve conversations): nothing to read, and no auto-switch.
    body = <ChatView paneKey={paneId} hostId="" sessionId={null} title={title} status="unknown" turns={[]} onSwitchToTerminal={onSwitchToTerminal} active={false} />
  } else if (conversation.state === 'unreadable') {
    body = frame(REASON[conversation.reason], conversation.retry)
  } else if (!ready || !ready.entry || ready.entry.status === 'unreadable' || (ready.entry.status === 'loading' && ready.entry.doc.turns.length === 0)) {
    body = <div data-testid="chat-loading" className="flex h-full items-center justify-center text-sm text-text-muted">{t('deck.loading')}</div>
  } else if (ready.entry.status === 'error' && ready.entry.doc.turns.length === 0) {
    body = frame('offline')
  } else {
    const { entry } = ready
    body = (
      <ChatView paneKey={paneId} hostId={ready.hostId} sessionId={ready.sessionId} title={entry.doc.header?.title || title}
        status={entry.doc.header?.status ?? 'unknown'} turns={entry.doc.turns} onSwitchToTerminal={onSwitchToTerminal}
        input={footer?.(footerContext(paneId, ready.hostId, ready.sessionId, entry.doc, items, onSwitchToTerminal))}
        active={isActive && isFocusTarget} />
    )
  }
  return (
    <div ref={ref} tabIndex={-1} data-testid="session-view-chat" className="absolute inset-0 bg-surface-primary outline-none">
      {body}
    </div>
  )
}
