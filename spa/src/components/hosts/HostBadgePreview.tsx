import { TerminalWindow } from '@phosphor-icons/react'
import { useMemo } from 'react'
import { useHostLook } from '../../lib/host-look'
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
 * Three mock sidebar rows showing the host badge for `mode`: normal, hover
 * and active. Same `.group` + `data-active` markup as `InlineTab`, so the
 * global rule in `index.css` switches `--hb-icon` exactly as in the real
 * rows. Rendered over a `bg-surface-tertiary` container so the badge's
 * translucent background reads the same as it does in the real sidebar
 * (which sits on that same surface), not the settings page background.
 */
export function HostBadgePreview({ hostId, mode }: HostBadgePreviewProps) {
  const t = useI18nStore((s) => s.t)
  const look = useHostLook(hostId)
  const name = look.name ?? ''
  const enabled = useUISettingsStore((s) => s.hostBadgeSidebarEnabled)
  const lineColor = useUISettingsStore((s) => s.hostBadgeSidebarLineColor)
  const box = useUISettingsStore((s) => s.hostBadgeSidebarBox)
  const inset = useUISettingsStore((s) => s.hostBadgeSidebarInset)
  const radius = useUISettingsStore((s) => s.hostBadgeSidebarRadius)

  const resolved = useMemo(() => resolveHostColors({ colors: look.colors, color: look.color }, mode), [look, mode])
  const badge = {
    colors: resolved,
    icon: isPhosphorIconName(look.icon) ? look.icon : undefined,
    iconWeight: isIconWeight(look.iconWeight) ? look.iconWeight : undefined,
  }
  const show = enabled && hasHostBadge(badge)

  const rows: { key: 'normal' | 'hover' | 'active'; active: boolean; label: string; cls: string }[] = [
    { key: 'normal', active: false, label: t('hosts.color.preview.normal'), cls: INLINE_TAB_ROW_CLASSES.inactive },
    { key: 'hover', active: true, label: t('hosts.color.preview.hover'), cls: INLINE_TAB_ROW_CLASSES.hoverPreview },
    { key: 'active', active: true, label: t('hosts.color.preview.active'), cls: INLINE_TAB_ROW_CLASSES.active },
  ]

  return (
    <div data-testid="host-badge-preview" className="flex flex-col gap-1.5 min-w-[180px]">
      <span className="text-[11px] text-text-muted">{t('hosts.color.preview.label')}</span>
      <div
        data-testid="host-badge-preview-surface"
        className="bg-surface-tertiary border border-border-subtle rounded-md p-1 flex flex-col gap-0.5"
      >
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
      </div>
      {!show && <span className="text-[11px] text-text-muted">{t('hosts.color.preview.none')}</span>}
    </div>
  )
}
