// spa/src/components/team/TeamHook.tsx — the tree tick that hangs the bead rows under the lead row (spec P4, P10, §4.3).
// Ported from the prototype at f61748aa with only the final variant (the rail): one stem from the lead block's lower edge,
// down from under the lead's bot icon; a horizontal tick per upper bead row; the last row turns with a 3 px radius. 1 px,
// the muted text colour at 70 %. Pointer-transparent: a click on it lands on the bead area, which collapses (P9).
//
// The bead container sits at the sidebar's 18px indent, the bot icon is centred ~18px further in, so the stem is HOOK_X
// inside the container and the beads start HOOK_X + ARM right of it (every row's first bead shares one left edge). Bead
// rows are 24px tall with a 2px gap (26px pitch).

/** Stem x inside the bead container (container left = sidebar + 18, bot icon centre = +18 more). */
export const HOOK_X = 17
/** Horizontal arm length from the stem to the first bead column. */
export const HOOK_ARM = 11
/** Padding-left of the bead container: where every row's first bead starts. */
export const HOOK_PAD = HOOK_X + HOOK_ARM + 2
const ROW_PITCH = 26
const HALF_ROW = 12
/** The block's lower edge sits this far above the container (the block's flex gap). */
const GAP_UP = 2
/** Corner radius of the last row's turn (px, spec P10). */
const RADIUS = 3

export function TeamHook({ rows }: { rows: number }) {
  const base = 'absolute pointer-events-none'
  const tone = { background: 'var(--text-muted)', opacity: 0.7 }
  const lastC = (rows - 1) * ROW_PITCH + HALF_ROW
  const elbowTop = lastC - RADIUS - 0.5
  return (
    <span data-testid="team-hook" aria-hidden="true" className="contents">
      <span className={`${base} w-px`} style={{ left: HOOK_X, top: -GAP_UP, height: elbowTop + GAP_UP, ...tone }} />
      {Array.from({ length: rows - 1 }, (_, i) => (
        <span key={i} data-testid="team-hook-tick" className={base} style={{ left: HOOK_X + 1, width: HOOK_ARM - 1, top: i * ROW_PITCH + HALF_ROW - 0.5, height: 1, ...tone }} />
      ))}
      <span
        data-testid="team-hook-elbow"
        className={base}
        style={{ left: HOOK_X, width: HOOK_ARM, top: elbowTop, height: RADIUS + 1, borderLeft: '1px solid', borderBottom: '1px solid', borderBottomLeftRadius: RADIUS, borderColor: 'var(--text-muted)', opacity: 0.7 }}
      />
    </span>
  )
}
