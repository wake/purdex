// spa/src/components/team/TeamHook.tsx — the hook that hangs the bead rows under the lead row.
//
// Drawn with CSS boxes so it stretches over however many bead rows wrap: the line runs from the lead row down
// to the middle of the LAST row, where it turns into the bead row. Bead rows are 24px tall with a 2px gap, so the
// last row's middle sits 12px above the container's bottom edge.
import type { TeamHookStyle } from './team-display'

const HALF_ROW = 12
const OVERLAP_UP = 3

export function TeamHook({ hookStyle }: { hookStyle: TeamHookStyle }) {
  const base = 'absolute pointer-events-none'
  const up = -OVERLAP_UP
  if (hookStyle === 'glyph') {
    return (
      <span data-testid="team-hook" data-hook-style="glyph" aria-hidden="true">
        <span className={`${base} left-[5px] w-px bg-text-muted opacity-70`} style={{ top: up, bottom: HALF_ROW * 2 - 3 }} />
        <span className={`${base} left-[1px] text-text-muted text-[13px] leading-none select-none`} style={{ bottom: HALF_ROW }}>⎿</span>
      </span>
    )
  }
  if (hookStyle === 'rail') {
    return (
      <span data-testid="team-hook" data-hook-style="rail" aria-hidden="true">
        <span className={`${base} left-[3px] w-px`} style={{ top: up, bottom: HALF_ROW, background: 'var(--text-muted)', opacity: 0.7 }} />
        <span
          className={`${base} left-[3px] w-[8px]`}
          style={{
            top: 0,
            bottom: 0,
            backgroundImage: `repeating-linear-gradient(to bottom, transparent 0 ${HALF_ROW - 0.5}px, var(--text-muted) ${HALF_ROW - 0.5}px ${HALF_ROW + 0.5}px, transparent ${HALF_ROW + 0.5}px 26px)`,
            opacity: 0.7,
          }}
        />
      </span>
    )
  }
  const bold = hookStyle === 'bold'
  const line = `${bold ? 2 : 1}px solid ${bold ? 'var(--text-secondary)' : 'var(--text-muted)'}`
  return (
    <span
      data-testid="team-hook"
      data-hook-style={hookStyle}
      aria-hidden="true"
      className={`${base} left-[3px]`}
      style={{
        top: up,
        bottom: HALF_ROW - (bold ? 1 : 0.5),
        width: bold ? 11 : 10,
        borderLeft: line,
        borderBottom: line,
        borderBottomLeftRadius: bold ? 8 : 6,
        opacity: bold ? 0.85 : 0.8,
      }}
    />
  )
}
