// spa/src/components/SubagentDots.tsx
import { useMemo } from 'react'

interface Props {
  count: number
}

const COLOR = '#60a5fa'
const DOT_SIZES: Record<number, number> = { 1: 4, 2: 3.5, 3: 3 }

// Vertical line positions to the left of the icon center.
const ARC_POSITIONS: Record<number, [number, number][]> = {
  1: [[-9, 0]],
  2: [[-9, -4], [-9, 4]],
  3: [[-9, -5.5], [-9, 0], [-9, 5.5]],
}

export function SubagentDots({ count }: Props) {
  const clamped = Math.min(Math.max(count, 0), 3)

  // Recalculate phase offset when dot count changes so all dots restart
  // with correctly synchronized animation phases.  When count stays the
  // same, useMemo keeps the value stable across re-renders.
  // eslint-disable-next-line react-hooks/purity, react-hooks/exhaustive-deps
  const phaseOffset = useMemo(() => performance.now(), [clamped])

  if (clamped <= 0) return null
  const positions = ARC_POSITIONS[clamped]
  const dotSize = DOT_SIZES[clamped]

  return (
    <>
      {positions.map(([left, top], i) => (
        <span
          key={`${clamped}-${i}`}
          className="absolute rounded-full animate-breathe"
          style={{
            width: dotSize,
            height: dotSize,
            backgroundColor: COLOR,
            left: `calc(50% + ${left}px)`,
            top: `calc(50% + ${top}px)`,
            transform: 'translate(-50%, -50%)',
            animationDelay: `${i * 0.3 - (phaseOffset / 1000) % 2}s`,
          }}
        />
      ))}
    </>
  )
}
