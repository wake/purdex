import { useMemo } from 'react'
import { listContributions } from '../../lib/settings-contribution-registry'
import { listReservedItems } from '../../lib/settings-section-registry'
import type { SettingsContextFor } from '../../lib/settings-contribution-types'
import { useI18nStore } from '../../stores/useI18nStore'

interface Props {
  activeSection: string
  onSelectSection: (section: string) => void
}

// Row shape used by the sidebar. There are three row kinds:
//
//   1. Active, enabled: from `listContributions('purdex')`; clickable.
//   2. Active, disabled-by-ctx (F7): from `listContributions('purdex')`
//      but `disabled(ctx)` returned true. Rendered in its natural `order`
//      slot (NOT pushed below the reserved divider), styled disabled,
//      click is a no-op. Tooltip carries the i18n'd `disabledReasonKey`.
//   3. Reserved (legacy coming-soon): from `listReservedItems()`. These
//      are the rows registered via `registerSettingsSection` with no
//      component; they stay below the divider.
//
// The divider sits before the first reserved row, so disabled-by-ctx rows
// remain intermixed with enabled rows by `order`, which matches the
// spec's "author decides ordering" rule.
interface SidebarRow {
  id: string
  labelKey: string
  order: number
  kind: 'active-enabled' | 'active-disabled' | 'reserved'
  disabledReasonKey?: string
}

export function SettingsSidebar({ activeSection, onSelectSection }: Props) {
  const t = useI18nStore((s) => s.t)

  // F7: ctx construction is the shell's job (§5.3 rule 4). Purdex scope
  // carries no entity id, so it is stable across renders — memoize.
  const ctx = useMemo<SettingsContextFor<'purdex'>>(
    () => ({ scope: 'purdex' as const }),
    [],
  )

  const rows: SidebarRow[] = [
    ...listContributions('purdex').map<SidebarRow>((c) => {
      const isDisabled = c.disabled ? c.disabled(ctx) === true : false
      return {
        id: c.localId,
        labelKey: c.labelKey,
        order: c.order,
        kind: isDisabled ? 'active-disabled' : 'active-enabled',
        disabledReasonKey: c.disabledReasonKey,
      }
    }),
    ...listReservedItems().map<SidebarRow>((r) => ({
      id: r.id,
      labelKey: r.label,
      order: r.order,
      kind: 'reserved',
    })),
  ].sort((a, b) => a.order - b.order)

  // Divider sits above the first reserved row. Disabled-by-ctx rows do
  // NOT participate in the divider pivot (they live inline with their
  // enabled siblings).
  const reservedStart = rows.findIndex((r) => r.kind === 'reserved')

  return (
    <div className="w-48 border-r border-border-subtle bg-surface-primary py-3 pl-2 flex-shrink-0">
      <div className="px-4 mb-2 text-[10px] text-text-muted uppercase tracking-wider">{t('settings.title')}</div>
      {rows.map((row, i) => {
        const isActive = row.id === activeSection
        const showDivider = i === reservedStart && reservedStart > 0
        const clickable = row.kind === 'active-enabled'
        const title =
          row.kind === 'active-disabled' && row.disabledReasonKey
            ? t(row.disabledReasonKey)
            : undefined

        return (
          <div key={row.id}>
            {showDivider && <div className="mx-3 my-2 border-t border-border-subtle" />}
            <button
              data-section={row.id}
              data-active={isActive ? 'true' : undefined}
              data-disabled-ctx={row.kind === 'active-disabled' ? 'true' : undefined}
              title={title}
              onClick={() => {
                if (clickable) onSelectSection(row.id)
              }}
              className={`w-full text-left px-4 py-2 text-sm flex items-center transition-colors ${
                !clickable
                  ? 'text-text-muted cursor-not-allowed'
                  : isActive
                    ? 'bg-surface-elevated text-text-primary border-l-2 border-border-active'
                    : 'text-text-secondary cursor-pointer hover:bg-white/5'
              }`}
            >
              <span>{t(row.labelKey)}</span>
              {row.kind === 'reserved' && (
                <span className="text-[10px] text-text-muted ml-auto">{t('settings.coming_soon')}</span>
              )}
            </button>
          </div>
        )
      })}
    </div>
  )
}
