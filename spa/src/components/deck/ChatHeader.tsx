// spa/src/components/deck/ChatHeader.tsx — the chat's header (U3 spec §5): the agent's icon, the tab's title and one line of
// what it is doing right now. While a chain of work runs that line IS the progress (「正在：…」); otherwise the status word.
import { Robot } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'

const KNOWN = new Set(['running', 'waiting', 'idle', 'error', 'ended'])

export function ChatHeader({ title, status, latest }: { title: string; status: string; latest?: string }) {
  const t = useI18nStore((s) => s.t)
  const line = latest !== undefined ? t('chat.progress', { latest }) : t(`chat.status.${KNOWN.has(status) ? status : 'unknown'}`)
  return (
    <div data-testid="chat-header" className="flex items-center gap-2 border-b border-border-subtle px-3 py-2">
      <Robot size={20} className="shrink-0 text-text-muted" />
      <div className="min-w-0 flex-1">
        <div data-testid="chat-header-title" className="truncate text-sm text-text-primary">{title}</div>
        <div data-testid="chat-header-status" data-status={status} role="status" className="truncate text-xs text-text-muted">{line}</div>
      </div>
    </div>
  )
}
