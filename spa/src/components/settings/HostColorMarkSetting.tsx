import {
  type HostColorMarkStyle,
  HOST_COLOR_LINE_WIDTH_MIN,
  HOST_COLOR_LINE_WIDTH_MAX,
  clampHostColorLineWidth,
} from '../../stores/useUISettingsStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { SettingItem } from './SettingItem'
import { SegmentControl } from './SegmentControl'

interface HostColorMarkSettingProps {
  label: string
  description: string
  style: HostColorMarkStyle
  width: number
  onStyleChange: (style: HostColorMarkStyle) => void
  onWidthChange: (px: number) => void
  testIdPrefix: string
}

export function HostColorMarkSetting({
  label,
  description,
  style,
  width,
  onStyleChange,
  onWidthChange,
  testIdPrefix,
}: HostColorMarkSettingProps) {
  const t = useI18nStore((s) => s.t)

  const options: { value: HostColorMarkStyle; label: string }[] = [
    { value: 'gradient', label: t('settings.terminal.host_color_mark.style.gradient') },
    { value: 'left-line', label: t('settings.terminal.host_color_mark.style.left_line') },
    { value: 'bottom-line', label: t('settings.terminal.host_color_mark.style.bottom_line') },
    { value: 'none', label: t('settings.terminal.host_color_mark.style.none') },
  ]

  const isLine = style === 'left-line' || style === 'bottom-line'

  return (
    <SettingItem label={label} description={description}>
      <div className="flex items-center gap-2">
        <div data-testid={`${testIdPrefix}-style`}>
          <SegmentControl options={options} value={style} onChange={onStyleChange} />
        </div>
        {isLine && (
          <>
            <input
              type="number"
              data-testid={`${testIdPrefix}-width`}
              aria-label={`${label}: ${t('settings.terminal.host_color_mark.width_aria')}`}
              min={HOST_COLOR_LINE_WIDTH_MIN}
              max={HOST_COLOR_LINE_WIDTH_MAX}
              step={1}
              value={width}
              onChange={(e) => onWidthChange(clampHostColorLineWidth(Number(e.target.value)))}
              className="bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-3 py-1.5 w-20 hover:border-text-muted focus:border-border-active focus:outline-none"
            />
            <span className="text-xs text-text-muted">{t('settings.terminal.host_color_mark.px')}</span>
          </>
        )}
      </div>
    </SettingItem>
  )
}
