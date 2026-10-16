// spa/src/components/fleet/CountBadge.tsx — the small count badge (fleet spec §8.1): the host cell's queue count and, later,
// the watcher shield share it. 12 px, 8 px semibold tabular figures, a 1 px ring in the sidebar's background colour. The
// colour comes from the caller (`className`), the position is the top-right corner of a `relative` parent.
//
// 12 px, not 11: the figures' ink is 6 px tall and the text lands on whole CSS pixels, so the room left in the badge must be
// even to split equally above and below. At 11 px the 5 px left split 3 / 2 and the figures sat a pixel low (or high, by
// where the badge fell); at 12 px it is 3 / 3 at every sub-pixel position (measured on 2x screenshots, 10-16).
export function CountBadge({ n, className = '', style, testId }: { n: number; className?: string; style?: React.CSSProperties; testId?: string }) {
  return (
    <span
      data-testid={testId}
      className={`absolute -top-[4px] -right-[4px] z-10 min-w-[12px] h-[12px] px-[2px] rounded-full inline-flex items-center justify-center text-[8px] leading-none font-semibold tabular-nums ${className}`}
      style={{ boxShadow: '0 0 0 1px var(--surface-tertiary)', ...style }}
    >
      {n > 99 ? '99+' : n}
    </span>
  )
}
