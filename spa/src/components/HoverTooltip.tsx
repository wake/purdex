import type { ReactNode } from 'react'

export type HoverTooltipPlacement = 'top' | 'right'

interface Props {
  children: ReactNode
  placement?: HoverTooltipPlacement
  'data-testid'?: string
}

const PLACEMENTS: Record<HoverTooltipPlacement, string> = {
  right: 'left-full ml-2 top-1/2 -translate-y-1/2',
  top: 'bottom-full mb-2 left-1/2 -translate-x-1/2',
}

// HoverTooltip is a CSS-only tooltip that fades in when its parent has the
// `.group` class and the user hovers. Extracted from the ActivityBarNarrow
// ws-tooltip pattern; parent MUST have `class="relative group"`.
export function HoverTooltip({ children, placement = 'right', 'data-testid': testId }: Props) {
  const pos = PLACEMENTS[placement]
  return (
    <span
      role="tooltip"
      data-testid={testId}
      className={`pointer-events-none absolute ${pos} whitespace-nowrap rounded bg-surface-secondary border border-border-default px-2 py-1 text-xs text-text-primary shadow-lg opacity-0 group-hover:opacity-100 transition-opacity z-50`}
    >
      {children}
    </span>
  )
}
