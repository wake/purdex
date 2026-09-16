import type { CSSProperties } from 'react'
import type { IconWeight } from '../types/tab'
import { DEFAULT_HOST_ICON } from '../lib/host-color'
import { WorkspaceIcon } from '../features/workspace/components/WorkspaceIcon'

export interface HostBadgeProps {
  /** Validated `#rrggbb` color, or null when the host has no color. */
  color: string | null
  /** Phosphor icon name; falls back to `DEFAULT_HOST_ICON`. */
  icon: string | undefined
  /** Phosphor weight; falls back to `regular`. */
  iconWeight: IconWeight | undefined
  /** Outer box size in px. */
  box: number
  /** Padding between the box edge and the icon, in px (icon = box − 2×inset). */
  inset: number
  /** Corner radius in px. */
  radius: number
  lineColor: 'host' | 'neutral'
  /** Icon opacity % when `lineColor` is `host`. */
  lineOpacity: number
  /** Background tint opacity %. */
  bgOpacity: number
  testId?: string
}

/**
 * Small tinted square holding the host's own icon. Unlike the old `HostColorMark`
 * this is a real flex child, not an overlay: it shifts the title instead of
 * covering it. The icon inherits the wrapper's `color` through `currentColor`.
 */
export function HostBadge({
  color,
  icon,
  iconWeight,
  box,
  inset,
  radius,
  lineColor,
  lineOpacity,
  bgOpacity,
  testId,
}: HostBadgeProps) {
  const iconColor =
    color === null || lineColor === 'neutral'
      ? 'var(--text-muted)'
      : `color-mix(in srgb, ${color} ${lineOpacity}%, transparent)`

  const style: CSSProperties = {
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    flexShrink: 0,
    width: `${box}px`,
    height: `${box}px`,
    borderRadius: `${radius}px`,
    color: iconColor,
  }
  if (color !== null) {
    style.background = `color-mix(in srgb, ${color} ${bgOpacity}%, transparent)`
  }

  return (
    <span
      data-testid={testId ?? 'host-badge'}
      data-has-color={String(color !== null)}
      aria-hidden="true"
      style={style}
    >
      <WorkspaceIcon
        icon={icon ?? DEFAULT_HOST_ICON}
        name=""
        size={box - inset * 2}
        weight={iconWeight ?? 'regular'}
      />
    </span>
  )
}
