import type { CSSProperties } from 'react'
import type { HostColorMarkStyle } from '../stores/useUISettingsStore'

interface Props {
  /** Validated `#rrggbb` color, or null for no mark. */
  color: string | null
  style: HostColorMarkStyle
  /** Line thickness in px (ignored by `gradient`). */
  width: number
  testId?: string
  zIndex?: number
}

/**
 * Absolute-positioned host color indicator. The parent must be `relative`.
 * `color` must already be validated (`isValidHostColor`) — the gradient
 * appends a hex alpha suffix, which is only well-formed for `#rrggbb`.
 */
export function HostColorMark({ color, style, width, testId, zIndex }: Props) {
  if (color === null || style === 'none') return null

  const base: CSSProperties = { position: 'absolute', pointerEvents: 'none' }
  if (zIndex !== undefined) base.zIndex = zIndex

  let css: CSSProperties
  switch (style) {
    case 'left-line':
      css = {
        ...base,
        left: 0,
        top: 0,
        bottom: 0,
        width: `${width}px`,
        background: color,
        borderTopLeftRadius: 'inherit',
        borderBottomLeftRadius: 'inherit',
      }
      break
    case 'bottom-line':
      css = {
        ...base,
        left: 0,
        right: 0,
        bottom: 0,
        height: `${width}px`,
        background: color,
        borderBottomLeftRadius: 'inherit',
        borderBottomRightRadius: 'inherit',
      }
      break
    case 'gradient':
      css = {
        ...base,
        left: 0,
        top: 0,
        bottom: 0,
        width: '24px',
        background: `linear-gradient(to right, ${color}73, transparent)`,
        borderTopLeftRadius: 'inherit',
        borderBottomLeftRadius: 'inherit',
      }
      break
  }

  return <span data-testid={testId ?? 'host-color-mark'} data-style={style} aria-hidden="true" style={css} />
}
