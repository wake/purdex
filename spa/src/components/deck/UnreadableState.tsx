// spa/src/components/deck/UnreadableState.tsx — what the deck / chat say when the conversation cannot be read (U3 plan D11).
// 「切到終端機」 is always there and is never pressed for the reader: the caller injects it, nothing here switches by itself.
import { useI18nStore } from '../../stores/useI18nStore'

export type UnreadableReason = 'no_session' | 'not_found' | 'unsupported' | 'empty' | 'offline'

export function UnreadableState({ reason, onRetry, onSwitchToTerminal }: { reason: UnreadableReason; onRetry?: () => void; onSwitchToTerminal: () => void }) {
  const t = useI18nStore((s) => s.t)
  const btn = 'cursor-pointer rounded border border-border-subtle px-3 py-1 text-sm text-text-primary hover:bg-surface-secondary'
  return (
    <div data-testid="unreadable" data-reason={reason} className="flex h-full flex-col items-center justify-center gap-3 p-6 text-center">
      <div className="text-sm text-text-muted">{t(`chat.unreadable.${reason}`)}</div>
      <div className="flex gap-2">
        {reason === 'offline' && onRetry && <button type="button" data-testid="unreadable-retry" onClick={onRetry} className={btn}>{t('chat.unreadable.retry')}</button>}
        <button type="button" data-testid="unreadable-terminal" onClick={onSwitchToTerminal} className={btn}>{t('chat.unreadable.terminal')}</button>
      </div>
    </div>
  )
}
