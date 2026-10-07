// spa/src/components/room/QuickReplyDock.tsx — the quick replies above the
// worker pane's input (R3 T2.1). One row of pills that scrolls sideways and
// never wraps; a tap sends that reply at once (Q1) and leaves whatever is
// typed in the input alone. Part of the input (§4.8), so it shows in room and
// chat alike — it is not the §4.6 state dock that chat drops. An empty list
// draws nothing (Q3: emptied means no dock).
import { CaretDown, CaretRight } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useWorkerSettingsStore } from '../../stores/useWorkerSettingsStore'
import type { QuickReply } from '../../lib/host-config-api'

export interface QuickReplyDockProps {
  replies: readonly QuickReply[]
  onSend: (text: string) => void
  disabled: boolean
}

export default function QuickReplyDock({ replies, onSend, disabled }: QuickReplyDockProps) {
  const t = useI18nStore((s) => s.t)
  const collapsed = useWorkerSettingsStore((s) => s.quickRepliesCollapsed)
  const setCollapsed = useWorkerSettingsStore((s) => s.setQuickRepliesCollapsed)
  if (replies.length === 0) return null
  const label = t(collapsed ? 'worker.quick_replies.expand' : 'worker.quick_replies.collapse')
  const Chevron = collapsed ? CaretRight : CaretDown
  return (
    <div className="flex items-center gap-1 px-2 py-1.5">
      <button type="button" data-testid="quick-reply-toggle" aria-expanded={!collapsed} aria-label={label} title={label}
        onClick={() => setCollapsed(!collapsed)}
        className="shrink-0 p-1 rounded text-text-muted hover:text-text-primary hover:bg-surface-hover cursor-pointer">
        <Chevron size={12} />
      </button>
      {collapsed ? null : (
    <div className="flex min-w-0 flex-1 gap-1.5 overflow-x-auto whitespace-nowrap [scrollbar-width:none]">
      {replies.map((r) => (
        <button
          key={r.id}
          type="button"
          data-testid="quick-reply"
          disabled={disabled}
          onClick={() => onSend(r.text)}
          title={r.text}
          className="shrink-0 max-w-[16rem] truncate min-h-9 px-3.5 rounded-full border border-border-subtle bg-surface-secondary text-xs text-text-secondary hover:text-text-primary hover:border-border-active transition-colors disabled:opacity-40 disabled:pointer-events-none"
        >
          {r.text}
        </button>
      ))}
    </div>
      )}
    </div>
  )
}
