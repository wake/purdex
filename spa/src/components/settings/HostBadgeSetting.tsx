import {
  type HostBadgeLineColor,
  clampHostBadgeLineOpacity,
  clampHostBadgeBgOpacity,
  clampHostBadgeBox,
  clampHostBadgeInset,
  clampHostBadgeRadius,
  HOST_BADGE_LINE_OPACITY_MIN,
  HOST_BADGE_LINE_OPACITY_MAX,
  HOST_BADGE_BG_OPACITY_MIN,
  HOST_BADGE_BG_OPACITY_MAX,
  HOST_BADGE_BOX_MIN,
  HOST_BADGE_BOX_MAX,
  HOST_BADGE_INSET_MIN,
  HOST_BADGE_INSET_MAX,
  HOST_BADGE_RADIUS_MIN,
  HOST_BADGE_RADIUS_MAX,
} from '../../stores/useUISettingsStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { SettingItem } from './SettingItem'
import { SegmentControl } from './SegmentControl'
import { ToggleSwitch } from './ToggleSwitch'

interface HostBadgeSettingProps {
  label: string
  description: string
  enabled: boolean
  lineColor: HostBadgeLineColor
  lineOpacity: number
  bgOpacity: number
  box: number
  inset: number
  radius: number
  onEnabledChange: (v: boolean) => void
  onLineColorChange: (v: HostBadgeLineColor) => void
  onLineOpacityChange: (pct: number) => void
  onBgOpacityChange: (pct: number) => void
  onBoxChange: (px: number) => void
  onInsetChange: (px: number) => void
  onRadiusChange: (px: number) => void
  testIdPrefix: string
}

const INPUT_CLASS =
  'bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-2 py-1.5 w-16 hover:border-text-muted focus:border-border-active focus:outline-none disabled:opacity-50 disabled:cursor-not-allowed'

export function HostBadgeSetting({
  label,
  description,
  enabled,
  lineColor,
  lineOpacity,
  bgOpacity,
  box,
  inset,
  radius,
  onEnabledChange,
  onLineColorChange,
  onLineOpacityChange,
  onBgOpacityChange,
  onBoxChange,
  onInsetChange,
  onRadiusChange,
  testIdPrefix,
}: HostBadgeSettingProps) {
  const t = useI18nStore((s) => s.t)

  const lineColorOptions: { value: HostBadgeLineColor; label: string }[] = [
    { value: 'host', label: t('settings.terminal.host_badge.line_color.host') },
    { value: 'neutral', label: t('settings.terminal.host_badge.line_color.neutral') },
  ]

  const numbers: {
    id: string
    labelKey: string
    value: number
    min: number
    max: number
    clamp: (n: number) => number
    onChange: (n: number) => void
    suffix?: string
  }[] = [
    {
      id: 'line-opacity',
      labelKey: 'settings.terminal.host_badge.line_opacity',
      value: lineOpacity,
      min: HOST_BADGE_LINE_OPACITY_MIN,
      max: HOST_BADGE_LINE_OPACITY_MAX,
      clamp: clampHostBadgeLineOpacity,
      onChange: onLineOpacityChange,
      suffix: '%',
    },
    {
      id: 'bg-opacity',
      labelKey: 'settings.terminal.host_badge.bg_opacity',
      value: bgOpacity,
      min: HOST_BADGE_BG_OPACITY_MIN,
      max: HOST_BADGE_BG_OPACITY_MAX,
      clamp: clampHostBadgeBgOpacity,
      onChange: onBgOpacityChange,
      suffix: '%',
    },
    {
      id: 'box',
      labelKey: 'settings.terminal.host_badge.box',
      value: box,
      min: HOST_BADGE_BOX_MIN,
      max: HOST_BADGE_BOX_MAX,
      clamp: clampHostBadgeBox,
      onChange: onBoxChange,
    },
    {
      id: 'inset',
      labelKey: 'settings.terminal.host_badge.inset',
      value: inset,
      min: HOST_BADGE_INSET_MIN,
      max: HOST_BADGE_INSET_MAX,
      clamp: clampHostBadgeInset,
      onChange: onInsetChange,
    },
    {
      id: 'radius',
      labelKey: 'settings.terminal.host_badge.radius',
      value: radius,
      min: HOST_BADGE_RADIUS_MIN,
      max: HOST_BADGE_RADIUS_MAX,
      clamp: clampHostBadgeRadius,
      onChange: onRadiusChange,
    },
  ]

  return (
    <SettingItem label={label} description={description}>
      <div className="flex flex-col items-end gap-2">
        <span data-testid={`${testIdPrefix}-enabled`}>
          <ToggleSwitch
            label={`${label}: ${t('settings.terminal.host_badge.enabled')}`}
            checked={enabled}
            onChange={onEnabledChange}
          />
        </span>

        <div
          data-testid={`${testIdPrefix}-line-color`}
          aria-disabled={!enabled}
          {...(!enabled ? { inert: true } : {})}
        >
          <SegmentControl
            options={lineColorOptions}
            value={lineColor}
            onChange={(v) => { if (enabled) onLineColorChange(v) }}
          />
        </div>

        <div className="flex flex-wrap items-center justify-end gap-2">
          {numbers.map((n) => (
            <span key={n.id} className="flex items-center gap-1">
              <input
                type="number"
                data-testid={`${testIdPrefix}-${n.id}`}
                aria-label={`${label}: ${t(n.labelKey)}`}
                min={n.min}
                max={n.max}
                step={1}
                disabled={!enabled}
                value={n.value}
                onChange={(e) => { if (enabled) n.onChange(n.clamp(Number(e.target.value))) }}
                className={INPUT_CLASS}
              />
              <span className="text-xs text-text-muted">
                {n.suffix ?? t('settings.terminal.host_badge.px')}
              </span>
            </span>
          ))}
        </div>
      </div>
    </SettingItem>
  )
}
