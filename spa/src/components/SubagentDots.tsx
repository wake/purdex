// spa/src/components/SubagentDots.tsx

interface Props {
  count: number
  isActive: boolean
}

const COLOR = '#60a5fa'
const DOT_SIZES: Record<number, number> = { 1: 4, 2: 3.5, 3: 3 }

// Vertical line positions to the left of the icon center.
const ARC_POSITIONS: Record<number, [number, number][]> = {
  1: [[-9, 0]],
  2: [[-9, -4], [-9, 4]],
  3: [[-9, -5.5], [-9, 0], [-9, 5.5]],
}

export function SubagentDots({ count, isActive }: Props) {
  if (count <= 0) return null
  const clamped = Math.min(count, 3)
  const positions = ARC_POSITIONS[clamped]
  const dotSize = DOT_SIZES[clamped]

  const breatheBg = isActive
    ? 'var(--surface-active)'
    : 'var(--surface-secondary)'

  return (
    <>
      {positions.map(([left, top], i) => (
        <span
          key={i}
          className="absolute rounded-full animate-breathe"
          style={{
            width: dotSize,
            height: dotSize,
            backgroundColor: COLOR,
            left: `calc(50% + ${left}px)`,
            top: `calc(50% + ${top}px)`,
            transform: 'translate(-50%, -50%)',
            '--breathe-color': COLOR,
            '--breathe-bg': breatheBg,
            animationDelay: `${i * 0.3 - (performance.now() / 1000) % 2}s`,
          } as React.CSSProperties}
        />
      ))}
    </>
  )
}
