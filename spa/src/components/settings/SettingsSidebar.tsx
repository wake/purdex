import { useMemo } from 'react'
import { PuzzlePiece } from '@phosphor-icons/react'
import { listContributions } from '../../lib/settings-contribution-registry'
import {
  isModuleOwnedContribution,
  type SettingsContextFor,
} from '../../lib/settings-contribution-types'
import { useI18nStore } from '../../stores/useI18nStore'

interface Props {
  activeSection: string
  onSelectSection: (section: string) => void
}

// Row shape used by the sidebar. There are two row kinds after PR-3
// (reserved `coming_soon` rows removed with the last reserved entry —
// `workspace` — in register-modules):
//
//   1. Active, enabled: from `listContributions('purdex')`; clickable.
//   2. Active, disabled-by-ctx (F7): from `listContributions('purdex')`
//      but `disabled(ctx)` returned true. Rendered in its natural `order`
//      slot, styled disabled, click is a no-op. Tooltip carries the
//      i18n'd `disabledReasonKey`.
interface SidebarRow {
  id: string
  labelKey: string
  order: number
  kind: 'active-enabled' | 'active-disabled'
  disabledReasonKey?: string
  moduleOwned: boolean
}

export function SettingsSidebar({ activeSection, onSelectSection }: Props) {
  const t = useI18nStore((s) => s.t)

  // F7: ctx construction is the shell's job (§5.3 rule 4). Purdex scope
  // carries no entity id, so it is stable across renders — memoize.
  const ctx = useMemo<SettingsContextFor<'purdex'>>(
    () => ({ scope: 'purdex' as const }),
    [],
  )

  const rows: SidebarRow[] = listContributions('purdex')
    .map<SidebarRow>((c) => {
      const isDisabled = c.disabled ? c.disabled(ctx) === true : false
      return {
        id: c.localId,
        labelKey: c.labelKey,
        order: c.order,
        kind: isDisabled ? 'active-disabled' : 'active-enabled',
        disabledReasonKey: c.disabledReasonKey,
        moduleOwned: isModuleOwnedContribution(c),
      }
    })
    .sort((a, b) => a.order - b.order)

  return (
    <div className="w-48 border-r border-border-subtle bg-surface-primary py-3 pl-2 flex-shrink-0">
      <div className="px-4 mb-2 text-[10px] text-text-muted uppercase tracking-wider">{t('settings.title')}</div>
      {rows.map((row) => {
        const isActive = row.id === activeSection
        const clickable = row.kind === 'active-enabled'
        const title =
          row.kind === 'active-disabled' && row.disabledReasonKey
            ? t(row.disabledReasonKey)
            : undefined

        return (
          <div key={row.id}>
            <button
              data-section={row.id}
              data-active={isActive ? 'true' : undefined}
              data-disabled-ctx={row.kind === 'active-disabled' ? 'true' : undefined}
              title={title}
              onClick={() => {
                if (clickable) onSelectSection(row.id)
              }}
              className={`w-full text-left px-4 py-2 text-sm flex items-center gap-2 transition-colors ${
                !clickable
                  ? 'text-text-muted cursor-not-allowed'
                  : isActive
                    ? 'bg-surface-elevated text-text-primary border-l-2 border-border-active'
                    : 'text-text-secondary cursor-pointer hover:bg-white/5'
              }`}
            >
              <span className="flex-1">{t(row.labelKey)}</span>
              {row.moduleOwned && (
                <PuzzlePiece
                  size={12}
                  weight="fill"
                  className="flex-shrink-0 rotate-[30deg] text-text-muted"
                  aria-hidden
                />
              )}
            </button>
          </div>
        )
      })}
    </div>
  )
}
