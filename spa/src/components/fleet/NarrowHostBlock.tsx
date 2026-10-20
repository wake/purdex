// spa/src/components/fleet/NarrowHostBlock.tsx — a host in the narrow sidebar (monitoring tabs M14, M16, W5): no host icon, one small block of four upright bars (CPU, memory, memory pressure, disk — each in its band, M8)
// on the tint of the host's colour (the badge's `light` layer; a host without a colour: the neutral `surface-hover`). The count (M6, M7) sits on its top right corner. A host that is not ok: the state's icon in the
// middle instead of the bars, the whole block (its tint too) dimmed as the badge is (wwd_down not, host_down 40 %, unknown 60 %), no count. The hover card and the click are the cell's (`HostCell` draws this inside its button).
import { useMemo } from 'react'
import { resolveHostBadge } from '../../lib/host-color'
import type { CellView } from '../../lib/pool/cell-model'
import { indicatorReading } from '../../lib/pool/indicator'
import { CountBadge } from './CountBadge'
import { BarColumns } from './HostBars'
import { HostStateIcon } from './HostStateIcon'
import { COUNT_TONE, DIM } from './host-cell-style'

/** The four items, in order (M14). */
const ITEMS = ['cpu', 'mem', 'pressure', 'disk'] as const
const W = 32
const H = 24
const BAR_W = 4
const BAR_GAP = 3
const BAR_H = 16

export function NarrowHostBlock({ view }: { view: CellView }) {
  const { colors, color, icon, iconWeight } = view.look
  const tint = useMemo(() => resolveHostBadge({ colors, color, icon, iconWeight }, 'terminal').colors?.light ?? null, [colors, color, icon, iconWeight])
  const state = view.state.state
  return (
    <span
      data-testid={`narrow-block-${view.hostId}`}
      data-tinted={String(tint !== null)}
      className={`relative inline-flex shrink-0 items-center justify-center rounded-md ${DIM[state]} ${tint === null ? 'bg-surface-hover' : ''}`}
      style={{ width: W, height: H, ...(tint !== null ? { background: tint } : {}) }}
    >
      {state === 'ok'
        ? <BarColumns bars={ITEMS.map((item) => indicatorReading(item, view))} width={ITEMS.length * BAR_W + (ITEMS.length - 1) * BAR_GAP} height={BAR_H} barWidth={BAR_W} gap={BAR_GAP} />
        : <span data-testid={`state-icon-${view.hostId}`} data-state={state} data-zone="offline" className="inline-flex items-center justify-center"><HostStateIcon state={state} size={H - 8} /></span>}
      {view.count && <CountBadge n={view.count.n} size="sm" className={`pointer-events-none ${COUNT_TONE[view.count.tone]}`} testId={`count-badge-${view.hostId}`} />}
    </span>
  )
}
