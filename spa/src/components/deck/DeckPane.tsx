// spa/src/components/deck/DeckPane.tsx — what a session pane shows in its deck view, given the conversation the pane holds
// (`useConversationOfPane`, mounted by SessionPaneContent for every view): the unreadable state (D11), the loading
// moment, or the deck. Takes focus when the pane switches to it, as the placeholder did.
import { useRef } from 'react'
import { useActivationFocus } from '../../hooks/useActivationFocus'
import type { PaneConversation } from '../../hooks/useConversationOfPane'
import { useI18nStore } from '../../stores/useI18nStore'
import { DeckUnreadable } from './DeckUnreadable'
import { DeckView, type DeckViewProps } from './DeckView'

interface Props {
  paneId: string
  conversation: PaneConversation
  isActive: boolean
  isFocusTarget: boolean
  onSwitchToTerminal: () => void
  footer?: DeckViewProps['footer']
  actions?: DeckViewProps['actions']
}

export function DeckPane({ paneId, conversation, isActive, isFocusTarget, onSwitchToTerminal, footer, actions }: Props) {
  const t = useI18nStore((s) => s.t)
  const ref = useRef<HTMLDivElement>(null)
  useActivationFocus(isActive, isFocusTarget, () => ref.current?.focus(), { raf: true })

  let body
  if (conversation.state === 'off') {
    // The pane is not a Claude Code session (or its host does not serve conversations): nothing to read, and no auto-switch.
    body = <DeckUnreadable reason="no_session" onSwitchToTerminal={onSwitchToTerminal} />
  } else if (conversation.state === 'unreadable') {
    body = <DeckUnreadable reason={conversation.reason} retry={conversation.retry} onSwitchToTerminal={onSwitchToTerminal} />
  } else if (conversation.state === 'resolving' || !conversation.entry || conversation.entry.status === 'unreadable') {
    body = <div data-testid="deck-loading" className="flex h-full items-center justify-center text-sm text-text-muted">{t('deck.loading')}</div>
  } else if (conversation.entry.status === 'error' && conversation.entry.doc.turns.length === 0) {
    body = <DeckUnreadable reason="unreachable" onSwitchToTerminal={onSwitchToTerminal} />
  } else if (conversation.entry.status === 'live' && conversation.entry.doc.turns.length > 0 && conversation.entry.doc.turns.every((tn) => tn.items.length === 0)) {
    // D11 / spec §7: a first turn with no items is unreadable, not a deck with nothing in it. It becomes the deck by itself
    // the moment an item lands.
    body = <DeckUnreadable reason="empty" onSwitchToTerminal={onSwitchToTerminal} />
  } else {
    body = (
      <DeckView paneId={paneId} hostId={conversation.hostId} sessionId={conversation.sessionId} entry={conversation.entry}
        onSwitchToTerminal={onSwitchToTerminal} footer={footer} actions={actions} />
    )
  }
  return (
    <div ref={ref} tabIndex={-1} data-testid="session-view-deck" className="absolute inset-0 bg-surface-primary outline-none">
      {body}
    </div>
  )
}
