// spa/src/components/deck/ThinkingSystem.tsx — the deck's two quiet rows: a collapsed 「思考」 disclosure (duration when
// known) and the system notices (small centred grey text; a long machine note folded under 「系統」; compaction and
// interrupt as their own lines). Their open state is in the pane's fold memory.
import { Info } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { systemView, textOutput } from '../../lib/conversations/deck-format'
import type { SystemItem, ThinkingItem } from '../../lib/conversations/types'
import { useFold } from '../room/fold-context'
import { OutputFold } from './OutputFold'

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

/** proto v2 (Collie summary/note): a dashed, faint frame with an Info icon and a small label; always at the left edge. */
function SystemFrame({ label, children }: { label: string | null; children: React.ReactNode }) {
  return (
    <div data-testid="deck-system" className="flex items-start gap-2 rounded-md border border-dashed border-border-default bg-surface-secondary/40 px-3 py-1.5 text-left text-xs text-text-muted">
      <Info size={14} className="mt-px shrink-0" aria-hidden="true" />
      <div className="min-w-0">
        {label && <span className="mr-1.5 font-medium uppercase tracking-wide text-text-secondary">{label}</span>}
        {children}
      </div>
    </div>
  )
}

export function SystemRow({ item }: { item: SystemItem }) {
  const t = useI18nStore((s) => s.t)
  const [open, toggle] = useFold(`${item.id}:note`)
  const view = systemView(item)
  if (view.kind === 'interrupted') {
    return <SystemFrame label={t('deck.system.label')}>{t('deck.system.interrupted')}</SystemFrame>
  }
  if (view.kind === 'compacted') {
    return <SystemFrame label={t('deck.system.label')}>{t('deck.system.compacted', { time: view.time })}</SystemFrame>
  }
  if (view.kind === 'output') {
    return (
      <div data-testid="deck-system" data-output className="pl-6">
        <OutputFold foldKey={`${item.id}:out`} output={textOutput(view.text)} />
      </div>
    )
  }
  if (view.kind === 'notice') {
    return <SystemFrame label={t('deck.system.label')}>{view.text}</SystemFrame>
  }
  return (
    <SystemFrame label={null}>
      <button type="button" data-testid="system-toggle" aria-expanded={open} onClick={toggle} className="cursor-pointer text-left hover:text-text-primary">
        {open ? '▾' : '▸'} {t('deck.system.label')}
      </button>
      {open && <pre data-testid="system-note" className="mt-1 max-w-[90ch] whitespace-pre-wrap break-words text-left">{view.text}</pre>}
    </SystemFrame>
  )
}
