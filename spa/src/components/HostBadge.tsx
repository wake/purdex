import type { CSSProperties } from 'react'
import type { IconWeight } from '../types/tab'
import type { ResolvedHostColors } from '../lib/host-color'
import { DEFAULT_HOST_ICON } from '../lib/host-color'
import { WorkspaceIcon } from '../features/workspace/components/WorkspaceIcon'

export interface HostBadgeProps {
  /** Resolved per-mode colors (rgba), or null when the host has no color. */
  colors: ResolvedHostColors | null
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
  testId?: string
}

/**
 * Small tinted square holding the host's own icon. A real flex child, not an
 * overlay: it shifts the title instead of covering it.
 *
 * Three states, no JS: the span publishes `--hb-main` / `--hb-middle`; its icon
 * color reads `--hb-icon` and falls back to `--hb-middle` (inactive). The global
 * rule in `index.css` sets `--hb-icon: var(--hb-main)` on a hovered `.group` row or
 * a `[data-active="true"]` row. Background is always the `light` layer.
 */
export function HostBadge({ colors, icon, iconWeight, box, inset, radius, lineColor, testId }: HostBadgeProps) {
  const hostColored = colors !== null && lineColor === 'host'

  const style: CSSProperties & Record<`--hb-${string}`, string> = {
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    flexShrink: 0,
    width: `${box}px`,
    height: `${box}px`,
    borderRadius: `${radius}px`,
    color: hostColored ? 'var(--hb-icon, var(--hb-middle))' : 'var(--text-muted)',
  }
  if (colors !== null) style.background = colors.light
  if (hostColored) {
    style['--hb-main'] = colors.main
    style['--hb-middle'] = colors.middle
  }

  return (
    <span
      data-testid={testId ?? 'host-badge'}
      data-host-badge=""
      data-has-color={String(colors !== null)}
      aria-hidden="true"
      style={style}
    >
      <WorkspaceIcon icon={icon ?? DEFAULT_HOST_ICON} name="" size={box - inset * 2} weight={iconWeight ?? 'regular'} />
    </span>
  )
}
