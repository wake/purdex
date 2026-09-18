import { TerminalWindow } from '@phosphor-icons/react'
import { useMemo } from 'react'
import { useHostStore } from '../../stores/useHostStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { HostBadge } from '../HostBadge'
import { INLINE_TAB_ROW_CLASSES } from '../../features/workspace/lib/inline-tab-row-classes'
import { hasHostBadge, isIconWeight, isPhosphorIconName, resolveHostColors, type HostColorMode } from '../../lib/host-color'

export interface HostBadgePreviewProps {
  hostId: string
  mode: HostColorMode
}

/**
 * Two mock sidebar rows showing the host badge for `mode` — one inactive, one
 * hovered/active. Same `.group` + `data-active` markup as `InlineTab`, so the
 * global rule in `index.css` switches `--hb-icon` exactly as in the real rows.
 */
export function HostBadgePreview({ hostId, mode }: HostBadgePreviewProps) {
  const t = useI18nStore((s) => s.t)
  const name = useHostStore((s) => s.hosts[hostId]?.name ?? '')
  const colors = useHostStore((s) => s.hosts[hostId]?.colors)
  const legacy = useHostStore((s) => s.hosts[hostId]?.color)
  const rawIcon = useHostStore((s) => s.hosts[hostId]?.icon)
  const rawWeight = useHostStore((s) => s.hosts[hostId]?.iconWeight)
  const enabled = useUISettingsStore((s) => s.hostBadgeSidebarEnabled)
  const lineColor = useUISettingsStore((s) => s.hostBadgeSidebarLineColor)
  const box = useUISettingsStore((s) => s.hostBadgeSidebarBox)
  const inset = useUISettingsStore((s) => s.hostBadgeSidebarInset)
  const radius = useUISettingsStore((s) => s.hostBadgeSidebarRadius)

  const resolved = useMemo(() => resolveHostColors({ colors, color: legacy }, mode), [colors, legacy, mode])
  const badge = {
    colors: resolved,
    icon: isPhosphorIconName(rawIcon) ? rawIcon : undefined,
    iconWeight: isIconWeight(rawWeight) ? rawWeight : undefined,
  }
  const show = enabled && hasHostBadge(badge)

  const rows: { key: 'normal' | 'active'; active: boolean; label: string; cls: string }[] = [
    { key: 'normal', active: false, label: t('hosts.color.preview.normal'), cls: INLINE_TAB_ROW_CLASSES.inactive },
    { key: 'active', active: true, label: t('hosts.color.preview.active'), cls: INLINE_TAB_ROW_CLASSES.active },
  ]

  return (
    <div data-testid="host-badge-preview" className="flex flex-col gap-1.5 min-w-[180px]">
      <span className="text-[11px] text-text-muted">{t('hosts.color.preview.label')}</span>
      {rows.map((r) => (
        <div key={r.key} className="flex items-center gap-2">
          <div
            data-testid={`host-badge-preview-${r.key}`}
            data-active={String(r.active)}
            className={`group flex items-center gap-1.5 pl-2 pr-2 py-1 rounded-md text-xs w-40 ${r.cls}`}
          >
            <TerminalWindow size={14} className="flex-shrink-0" />
            {show && (
              <HostBadge
                testId={`host-badge-preview-badge-${r.key}`}
                colors={badge.colors}
                icon={badge.icon}
                iconWeight={badge.iconWeight}
                box={box}
                inset={inset}
                radius={radius}
                lineColor={lineColor}
              />
            )}
            <span className="truncate">{name}</span>
          </div>
          <span className="text-[11px] text-text-muted">{r.label}</span>
        </div>
      ))}
      {!show && <span className="text-[11px] text-text-muted">{t('hosts.color.preview.none')}</span>}
    </div>
  )
}
