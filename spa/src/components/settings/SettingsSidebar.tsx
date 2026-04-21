import { listContributions } from '../../lib/settings-contribution-registry'
import { listReservedItems } from '../../lib/settings-section-registry'
import { useI18nStore } from '../../stores/useI18nStore'

interface Props {
  activeSection: string
  onSelectSection: (section: string) => void
}

// Row shape used by the sidebar. Active rows come from `listContributions`;
// reserved rows come from `listReservedItems` with a missing component so the
// UI still renders a disabled "coming soon" entry.
interface SidebarRow {
  id: string         // localId for active; reserved def.id for reserved
  labelKey: string
  order: number
  enabled: boolean
}

export function SettingsSidebar({ activeSection, onSelectSection }: Props) {
  const t = useI18nStore((s) => s.t)

  const rows: SidebarRow[] = [
    ...listContributions('purdex').map((c) => ({
      id: c.localId,
      labelKey: c.labelKey,
      order: c.order,
      enabled: true,
    })),
    ...listReservedItems().map((r) => ({
      id: r.id,
      labelKey: r.label,
      order: r.order,
      enabled: false,
    })),
  ].sort((a, b) => a.order - b.order)

  // Reserved rows are exactly the disabled rows in the merged list. Keep the
  // divider logic until PR-3 clears the remaining reserved entry.
  const reservedStart = rows.findIndex((r) => !r.enabled)

  return (
    <div className="w-48 border-r border-border-subtle bg-surface-primary py-3 pl-2 flex-shrink-0">
      <div className="px-4 mb-2 text-[10px] text-text-muted uppercase tracking-wider">{t('settings.title')}</div>
      {rows.map((row, i) => {
        const isActive = row.id === activeSection
        const showDivider = i === reservedStart && reservedStart > 0

        return (
          <div key={row.id}>
            {showDivider && <div className="mx-3 my-2 border-t border-border-subtle" />}
            <button
              data-section={row.id}
              data-active={isActive ? 'true' : undefined}
              onClick={() => {
                if (row.enabled) onSelectSection(row.id)
              }}
              className={`w-full text-left px-4 py-2 text-sm flex items-center transition-colors ${
                !row.enabled
                  ? 'text-text-muted cursor-not-allowed'
                  : isActive
                    ? 'bg-surface-elevated text-text-primary border-l-2 border-border-active'
                    : 'text-text-secondary cursor-pointer hover:bg-white/5'
              }`}
            >
              <span>{t(row.labelKey)}</span>
              {!row.enabled && (
                <span className="text-[10px] text-text-muted ml-auto">{t('settings.coming_soon')}</span>
              )}
            </button>
          </div>
        )
      })}
    </div>
  )
}
