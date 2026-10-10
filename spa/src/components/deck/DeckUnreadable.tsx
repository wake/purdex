// spa/src/components/deck/DeckUnreadable.tsx — the deck when the conversation cannot be read (U3 plan D11). It says why, in the
// reader's words, offers 「切到終端機」, and never switches away by itself.
import { useI18nStore } from '../../stores/useI18nStore'
import type { PaneConversationUnreadable } from '../../hooks/useConversationOfPane'

/** The hook's reasons, and the one only the deck can see: a first turn with no items yet. */
export type DeckUnreadableReason = PaneConversationUnreadable | 'empty'

const REASON_KEY: Record<DeckUnreadableReason, string> = {
  empty: 'deck.empty',
  no_session: 'deck.unreadable.no_session',
  not_found: 'deck.unreadable.not_found',
  provider_unsupported: 'deck.unreadable.provider_unsupported',
  unreachable: 'deck.unreadable.unreachable',
}

export function DeckUnreadable({ reason, retry, onSwitchToTerminal }: {
  reason: DeckUnreadableReason
  /** Offered for what may pass: the host answering again, the transcript appearing. */
  retry?: () => void
  onSwitchToTerminal: () => void
}) {
  const t = useI18nStore((s) => s.t)
  const canRetry = retry && (reason === 'unreachable' || reason === 'not_found')
  return (
    <div data-testid="deck-unreadable" data-reason={reason} className="flex h-full flex-col items-center justify-center gap-3 p-6 text-center text-sm text-text-secondary">
      <p>{t(REASON_KEY[reason])}</p>
      <div className="flex gap-2">
        {canRetry && (
          <button type="button" data-testid="deck-retry" onClick={retry} className="cursor-pointer rounded border border-border-subtle px-3 py-1 hover:bg-surface-secondary">{t('deck.retry')}</button>
        )}
        <button type="button" data-testid="deck-to-terminal" onClick={onSwitchToTerminal} className="cursor-pointer rounded border border-border-subtle px-3 py-1 hover:bg-surface-secondary">{t('deck.to_terminal')}</button>
      </div>
    </div>
  )
}
