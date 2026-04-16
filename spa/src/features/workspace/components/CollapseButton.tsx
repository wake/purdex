import { CaretDoubleLeft, CaretDoubleRight } from '@phosphor-icons/react'
import { useLayoutStore } from '../../../stores/useLayoutStore'
import { useI18nStore } from '../../../stores/useI18nStore'

export function CollapseButton() {
  const width = useLayoutStore((s) => s.activityBarWidth)
  const tabPosition = useLayoutStore((s) => s.tabPosition)
  const toggle = useLayoutStore((s) => s.toggleActivityBarWidth)
  const t = useI18nStore((s) => s.t)

  const locked = tabPosition === 'left'
  const isWide = width === 'wide'
  const Icon = isWide ? CaretDoubleLeft : CaretDoubleRight
  const label = locked
    ? t('nav.collapse_locked_tooltip')
    : isWide
      ? t('nav.collapse_activity_bar')
      : t('nav.expand_activity_bar')

  return (
    <button
      type="button"
      disabled={locked}
      title={label}
      aria-label={label}
      aria-pressed={isWide}
      onClick={toggle}
      className={`w-[30px] h-[30px] rounded-md flex items-center justify-center ${
        locked
          ? 'text-text-muted/50 cursor-not-allowed'
          : 'cursor-pointer text-text-secondary hover:text-text-primary hover:bg-surface-secondary'
      }`}
    >
      <Icon size={14} />
    </button>
  )
}
