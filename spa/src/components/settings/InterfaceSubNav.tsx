import { useI18nStore } from '../../stores/useI18nStore'
import type { InterfaceSubsection } from '../../lib/interface-subsection-registry'

interface Props {
  items: InterfaceSubsection[]
  active: string
  onSelect: (id: string) => void
}

export function InterfaceSubNav({ items, active, onSelect }: Props) {
  const t = useI18nStore((s) => s.t)
  return (
    <div className="w-40 border-r border-border-subtle py-3 pl-2 flex-shrink-0">
      {items.map((item) => {
        const enabled = !item.disabled
        const isActive = enabled && item.id === active
        return (
          <button
            key={item.id}
            type="button"
            data-testid={`interface-subnav-${item.id}`}
            data-active={isActive ? 'true' : undefined}
            onClick={() => { if (enabled) onSelect(item.id) }}
            disabled={!enabled}
            className={[
              'w-full text-left px-3 py-2 text-sm flex items-center transition-colors',
              !enabled ? 'text-text-muted cursor-not-allowed'
                      : isActive ? 'bg-surface-elevated text-text-primary border-l-2 border-border-active'
                                 : 'text-text-secondary cursor-pointer hover:bg-white/5',
            ].join(' ')}
          >
            <span>{t(item.label)}</span>
            {!enabled && (
              <span className="text-[10px] text-text-muted ml-auto">
                {t(item.disabledReason ?? 'settings.coming_soon')}
              </span>
            )}
          </button>
        )
      })}
    </div>
  )
}
