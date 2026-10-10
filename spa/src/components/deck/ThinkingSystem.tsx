// spa/src/components/deck/ThinkingSystem.tsx — the deck's two quiet rows: a collapsed 「思考」 disclosure (duration when
// known) and the system notices (small centred grey text; a long machine note folded under 「系統」; compaction and
// interrupt as their own lines). Their open state is in the pane's fold memory.
import { useI18nStore } from '../../stores/useI18nStore'
import { systemView } from '../../lib/conversations/deck-format'
import type { SystemItem, ThinkingItem } from '../../lib/conversations/types'
import { useFold } from '../room/fold-context'

export function ThinkingRow({ item }: { item: ThinkingItem }) {
  const t = useI18nStore((s) => s.t)
  const [open, toggle] = useFold(`${item.id}:think`)
  const secs = item.duration_ms !== undefined ? Math.max(1, Math.round(item.duration_ms / 1000)) : null
  return (
    <div data-testid="deck-thinking" className="text-xs text-text-muted">
      <button type="button" data-testid="thinking-toggle" aria-expanded={open} onClick={toggle} className="cursor-pointer hover:text-text-primary">
        {open ? '▾' : '▸'} {secs === null ? t('deck.thinking') : t('deck.thinking.for', { s: secs })}
      </button>
      {open && item.text && (
        <div data-testid="thinking-body" className="mt-1 whitespace-pre-wrap break-words border-l border-border-subtle pl-3 text-text-secondary">
          {item.text}{item.truncated && '…'}
        </div>
      )}
    </div>
  )
}

export function SystemRow({ item }: { item: SystemItem }) {
  const t = useI18nStore((s) => s.t)
  const [open, toggle] = useFold(`${item.id}:note`)
  const view = systemView(item)
  if (view.kind === 'interrupted') {
    return <div data-testid="deck-system" className="text-center text-xs text-text-muted">{t('deck.system.interrupted')}</div>
  }
  if (view.kind === 'compacted') {
    return <div data-testid="deck-system" className="text-center text-xs text-text-muted">{t('deck.system.compacted', { time: view.time })}</div>
  }
  if (view.kind === 'notice') {
    return <div data-testid="deck-system" className="text-center text-xs text-text-muted">{view.text}</div>
  }
  return (
    <div data-testid="deck-system" className="text-center text-xs text-text-muted">
      <button type="button" data-testid="system-toggle" aria-expanded={open} onClick={toggle} className="cursor-pointer hover:text-text-primary">
        {open ? '▾' : '▸'} {t('deck.system.label')}
      </button>
      {open && <pre data-testid="system-note" className="mx-auto mt-1 max-w-[90ch] whitespace-pre-wrap break-words text-left">{view.text}</pre>}
    </div>
  )
}
