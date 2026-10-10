// spa/src/components/deck/DeckPane.tsx — what a session pane shows in its deck view, given the conversation the pane holds
// (`useConversationOfPane`, mounted by SessionPaneContent for every view): the unreadable state (D11), the loading
// moment, or the deck. Takes focus when the pane switches to it, as the placeholder did.
import { useCallback, useRef } from 'react'
import { useActivationFocus } from '../../hooks/useActivationFocus'
import type { PaneConversation } from '../../hooks/useConversationOfPane'
import { useI18nStore } from '../../stores/useI18nStore'
import { conversationBinding, openPanel, type PanelContent } from '../../lib/conversations/panel-memory'
import { DeckUnreadable } from './DeckUnreadable'
import { DeckView, type DeckViewProps } from './DeckView'
import { SessionPanelSplit } from './SessionPanelSplit'

interface Props {
  paneId: string
  conversation: PaneConversation
  isActive: boolean
  isFocusTarget: boolean
  onSwitchToTerminal: () => void
  footer?: DeckViewProps['footer']
}

export function DeckPane({ paneId, conversation, isActive, isFocusTarget, onSwitchToTerminal, footer }: Props) {
  const t = useI18nStore((s) => s.t)
  const ref = useRef<HTMLDivElement>(null)
  // Switching to the deck focuses its input when it has one (plan D2), else the frame (loading, unreadable).
  useActivationFocus(isActive, isFocusTarget, () => (ref.current?.querySelector('textarea') ?? ref.current)?.focus(), { raf: true })

  // Stable, so a live update re-draws only the turn that changed (DeckView memoizes its turns).
  const binding = conversation.state === 'ready' ? conversationBinding(conversation.hostId, conversation.sessionId) : ''
  const onOpenPanel = useCallback((content: PanelContent) => openPanel(paneId, binding, content), [paneId, binding])

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
    const { hostId, sessionId, entry } = conversation
    body = (
      <SessionPanelSplit paneKey={paneId} binding={binding} turns={entry.doc.turns} active={isActive && isFocusTarget}>
        <DeckView key={sessionId} paneId={paneId} hostId={hostId} sessionId={sessionId} entry={entry}
          onSwitchToTerminal={onSwitchToTerminal} footer={footer} onOpenPanel={onOpenPanel} />
      </SessionPanelSplit>
    )
  }
  return (
    <div ref={ref} tabIndex={-1} data-testid="session-view-deck" className="absolute inset-0 bg-surface-primary outline-none">
      {body}
    </div>
  )
}
