import { SidebarSimple } from '@phosphor-icons/react'
import { useLayoutStore } from '../../../stores/useLayoutStore'
import { useI18nStore } from '../../../stores/useI18nStore'

type Variant = 'header-right' | 'divider' | 'topbar'

interface Props {
  variant?: Variant
}

// header-right and divider have explicit w/h so the icon needs flex centering;
// topbar only wraps the icon in p-1, which matches the region-toggle buttons
// byte-for-byte — leave flex classes off so class strings are identical.
const VARIANT_CLASSES: Record<Variant, string> = {
  'header-right': 'w-6 h-6 rounded-md flex items-center justify-center',
  divider: 'absolute top-3 right-[-11px] w-[22px] h-[22px] rounded-full bg-surface-tertiary border border-border-subtle shadow-sm opacity-0 group-hover/narrow-bar:opacity-100 focus:opacity-100 transition-opacity z-10 flex items-center justify-center',
  topbar: 'p-1 rounded',
}

const ICON_SIZE: Record<Variant, number> = {
  'header-right': 12,
  divider: 12,
  topbar: 14,
}

// topbar mirrors the region-toggle buttons in TitleBar's right cluster: accent
// tint when the activity bar is "visible as wide", neutral secondary styling
// otherwise. Other variants keep the original hover-only treatment.
function stateClasses(variant: Variant, locked: boolean, isWide: boolean): string {
  if (locked) return 'text-text-muted/50 cursor-not-allowed'
  if (variant === 'topbar' && isWide) {
    return 'cursor-pointer text-accent-base bg-accent-base/10 hover:bg-accent-base/20'
  }
  return 'cursor-pointer text-text-secondary hover:text-text-primary hover:bg-surface-hover'
}

export function CollapseButton({ variant = 'header-right' }: Props) {
  const width = useLayoutStore((s) => s.activityBarWidth)
  const tabPosition = useLayoutStore((s) => s.tabPosition)
  const toggle = useLayoutStore((s) => s.toggleActivityBarWidth)
  const t = useI18nStore((s) => s.t)

  // Only `left` forces wide (tabs live exclusively in the activity bar there).
  // `both` keeps tabs reachable via the top tab bar, so collapse is allowed.
  const locked = tabPosition === 'left'
  const isWide = width === 'wide'
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
      data-variant={variant}
      onClick={toggle}
      className={`${VARIANT_CLASSES[variant]} transition-colors ${stateClasses(variant, locked, isWide)}`}
    >
      <SidebarSimple size={ICON_SIZE[variant]} />
    </button>
  )
}
