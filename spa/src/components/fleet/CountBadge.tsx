// spa/src/components/fleet/CountBadge.tsx — the small count badge (fleet spec §8.1): the host cell's queue count and the watcher
// shield's events count share it. Semibold tabular figures, a 1 px ring in the sidebar's background colour. The colour comes from
// the caller (`className`), the position is the top-right corner of a `relative` parent, anchored by the badge's right edge so more
// digits widen it to the left (over the parent), never further out.
//
// Two sizes; the badge must leave an EVEN room around the figures' ink, which lands on whole CSS pixels, to split it equally above
// and below (at 11 px the 5 px left split 3 / 2 and the figures sat a pixel low or high; measured on 2x screenshots, 10-16):
//   md  (default; the watcher shield) 12 px, 8 px figures (6 px ink, 3 / 3), 4 px out of the corner.
//   sm  (the host cell; journal v5 U9: smaller, further to the upper right) 9 px, 7 px figures (~5 px ink, 2 / 2), 5 px out.
export type CountBadgeSize = 'md' | 'sm'

const GEOMETRY: Record<CountBadgeSize, string> = {
  md: '-top-[4px] -right-[4px] min-w-[12px] h-[12px] text-[8px]',
  sm: '-top-[5px] -right-[5px] min-w-[9px] h-[9px] text-[7px]',
}

export function CountBadge({ n, size = 'md', className = '', style, testId, label }: { n: number; size?: CountBadgeSize; className?: string; style?: React.CSSProperties; testId?: string; label?: string }) {
  return (
    <span
      data-testid={testId}
      className={`absolute ${GEOMETRY[size]} z-10 px-[2px] rounded-full inline-flex items-center justify-center leading-none font-semibold tabular-nums ${className}`}
      style={{ boxShadow: '0 0 0 1px var(--surface-tertiary)', ...style }}
    >
      {label ?? (n > 99 ? '99+' : n)}
    </span>
  )
}
