// spa/src/components/team/TeamHook.tsx — the hook that hangs the bead rows under the lead row.
//
// The hook grows from under the lead's bot icon (the bead container sits at the sidebar's 18px indent, the bot icon
// is centered ~18px further in, so the stem is at HOOK_X inside the container) and the beads start HOOK_X + ARM right
// of it, so every row's first bead shares one left edge. Bead rows are 24px tall with a 2px gap (26px pitch).
// thin / bold: one stem that turns into the last row. rail: a stem with a tick per row. glyph: like Claude Code's
// console, every row has its own ⎿ at the same x (no continuous line) — it needs the wrapped row count.
// "blend" top: the stem also reaches up into the lead row's highlight, fading in from the highlight color so the
// line looks fused with the block; "below" starts exactly at the block's lower edge.
import type { TeamHookStyle, TeamHookTop, TeamRailCorner } from './team-display'

/** Stem x inside the bead container (container left = sidebar + 18, bot icon center = +18 more). */
export const HOOK_X = 17
/** Horizontal arm length from the stem to the first bead column. */
export const HOOK_ARM = 11
/** Padding-left of the bead container: where every row's first bead starts. */
export const HOOK_PAD = HOOK_X + HOOK_ARM + 2
const ROW_H = 24
const ROW_PITCH = 26
const HALF_ROW = 12
/** The block's lower edge sits this far above the container (the block's flex gap). */
const GAP_UP = 2
const BLEND_UP = 8
/** Elbow radius of the rail hook (px), by the 刻度轉角 setting. */
const RAIL_RADIUS: Record<TeamRailCorner, number> = { square: 0, small: 3, large: 6 }

interface Props {
  hookStyle: TeamHookStyle
  rows: number
  hookTop: TeamHookTop
  railCorner?: TeamRailCorner
  /** The lead row is drawn highlighted (only then does a "blend" top have a block to fuse with). */
  leadActive: boolean
}

export function TeamHook({ hookStyle, rows, hookTop, leadActive, railCorner = 'square' }: Props) {
  const base = 'absolute pointer-events-none'
  const blend = hookTop === 'blend' && leadActive && hookStyle !== 'glyph'
  const stemColor = hookStyle === 'bold' ? 'var(--text-secondary)' : 'var(--text-muted)'
  const stemW = hookStyle === 'bold' ? 2 : 1
  const blendStem = blend && (
    <span
      data-testid="team-hook-blend"
      className={base}
      style={{
        left: HOOK_X,
        width: stemW,
        top: -(GAP_UP + BLEND_UP),
        height: BLEND_UP + 1,
        background: `linear-gradient(to bottom, var(--surface-active), ${stemColor})`,
        opacity: hookStyle === 'bold' ? 0.85 : 0.8,
      }}
    />
  )
  if (hookStyle === 'glyph') {
    return (
      <span data-testid="team-hook" data-hook-style="glyph" aria-hidden="true" className="contents">
        {Array.from({ length: rows }, (_, i) => (
          <span
            key={i}
            data-testid="team-hook-glyph"
            className={`${base} text-text-muted text-[13px] select-none`}
            style={{ left: HOOK_X - 3, top: i * ROW_PITCH, height: ROW_H, lineHeight: `${ROW_H}px` }}
          >⎿</span>
        ))}
      </span>
    )
  }
  if (hookStyle === 'rail') {
    // One stem down to where the last row's elbow starts; a tick per upper row; the last row turns with a radius.
    const r = RAIL_RADIUS[railCorner]
    const lastC = (rows - 1) * ROW_PITCH + HALF_ROW
    const elbowTop = lastC - r - 0.5
    const tone = { borderColor: 'var(--text-muted)', opacity: 0.7 }
    return (
      <span data-testid="team-hook" data-hook-style="rail" data-rail-corner={railCorner} aria-hidden="true" className="contents">
        {blendStem}
        <span className={`${base} w-px`} style={{ left: HOOK_X, top: -GAP_UP, height: elbowTop + GAP_UP, background: 'var(--text-muted)', opacity: 0.7 }} />
        {Array.from({ length: rows - 1 }, (_, i) => (
          <span key={i} data-testid="team-hook-tick" className={base} style={{ left: HOOK_X + 1, width: HOOK_ARM - 1, top: i * ROW_PITCH + HALF_ROW - 0.5, height: 1, background: 'var(--text-muted)', opacity: 0.7 }} />
        ))}
        <span
          data-testid="team-hook-elbow"
          className={base}
          style={{ left: HOOK_X, width: HOOK_ARM, top: elbowTop, height: r + 1, borderLeft: '1px solid', borderBottom: '1px solid', borderBottomLeftRadius: r, ...tone }}
        />
      </span>
    )
  }
  const bold = hookStyle === 'bold'
  const line = `${stemW}px solid ${stemColor}`
  return (
    <span data-testid="team-hook" data-hook-style={hookStyle} aria-hidden="true" className="contents">
      {blendStem}
      <span
        className={base}
        style={{
          left: HOOK_X,
          top: -GAP_UP,
          bottom: HALF_ROW - (bold ? 1 : 0.5),
          width: HOOK_ARM,
          borderLeft: line,
          borderBottom: line,
          borderBottomLeftRadius: bold ? 8 : 6,
          opacity: bold ? 0.85 : 0.8,
        }}
      />
    </span>
  )
}
