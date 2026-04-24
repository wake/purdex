// spa/src/components/SubagentDots.tsx
import { useMemo, type CSSProperties } from 'react'
import type { SubagentRef } from '../stores/useAgentStore'

interface Props {
  refs: SubagentRef[]
  // Literal `left` value (in px) overriding the default calc(50% + offset).
  // Used by badge mode to nudge dots outside the icon box.
  left?: number
}

// Phase 2 PR-2b: dot color per agent family, derived inline (plan §5 — don't
// abstract the palette elsewhere until a second consumer needs it).
// Fallback to the cc blue keeps legacy / unknown types visible.
const TYPE_COLOR: Record<string, string> = {
  cc: '#60a5fa',
  codex: '#facc15',
  opencode: '#f97316',
}
const FALLBACK_COLOR = TYPE_COLOR.cc
const colorFor = (type: string) => TYPE_COLOR[type] ?? FALLBACK_COLOR

// Proxy refs (cross-agent hook collapsed onto a parent frame) render as a
// hollow ring — transparent background + 1px ring in the type color — to
// distinguish from native subagents (solid fill of the same color).
const dotStyle = (ref: SubagentRef): CSSProperties => {
  const color = colorFor(ref.type)
  if (ref.is_proxy) {
    return {
      backgroundColor: 'transparent',
      border: `1px solid ${color}`,
      boxSizing: 'border-box',
    }
  }
  return { backgroundColor: color }
}

const DOT_SIZES: Record<number, number> = { 1: 4, 2: 3.5, 3: 3 }

// Vertical line positions to the left of the icon center.
const ARC_POSITIONS: Record<number, [number, number][]> = {
  1: [[-9, 0]],
  2: [[-9, -4], [-9, 4]],
  3: [[-9, -5.5], [-9, 0], [-9, 5.5]],
}

export function SubagentDots({ refs, left: leftOverride }: Props) {
  const clamped = Math.min(Math.max(refs.length, 0), 3)

  // Recalculate phase offset when dot count changes so all dots restart
  // with correctly synchronized animation phases.  When count stays the
  // same, useMemo keeps the value stable across re-renders.
  // eslint-disable-next-line react-hooks/purity, react-hooks/exhaustive-deps
  const phaseOffset = useMemo(() => performance.now(), [clamped])

  if (clamped <= 0) return null
  const positions = ARC_POSITIONS[clamped]
  const dotSize = DOT_SIZES[clamped]
  const visibleRefs = refs.slice(0, clamped)

  return (
    <>
      {positions.map(([left, top], i) => {
        const ref = visibleRefs[i]
        return (
          <span
            key={`${clamped}-${i}`}
            data-testid="subagent-dot"
            data-subagent-type={ref.type}
            data-is-proxy={ref.is_proxy ? 'true' : 'false'}
            className="absolute rounded-full animate-breathe"
            style={{
              width: dotSize,
              height: dotSize,
              ...dotStyle(ref),
              left: leftOverride !== undefined ? `${leftOverride}px` : `calc(50% + ${left}px)`,
              top: `calc(50% + ${top}px)`,
              transform: 'translate(-50%, -50%)',
              animationDelay: `${i * 0.3 - (phaseOffset / 1000) % 2}s`,
            }}
          />
        )
      })}
    </>
  )
}
