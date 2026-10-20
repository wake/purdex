// spa/src/components/fleet/HostCell.tsx — one host's cell of the sidebar's host row, built from the App's own pieces so it reads like the members beside it:
//   cell   a member bead's box: h-6, rounded-md, gap-1, 3 px each side; hover bg-surface-hover, selected bg-surface-active. No click / focus outline; the card opens on focus.
//   badge  the App's `HostBadge` as beside a member in the session list (the sidebar badge settings, the host's colours resolved as for an agent member: terminal, falling back to console).
//          A host that is not `ok`: the badge dimmed and the indicator's track only.
//   hover  a card (HostCard) with the host's readings or, when it is not ok, its state; no native title.
//   then, in the same square after the badge, 2 px further right than the cell's gap (`ml-0.5`), the host's indicator:
//   ring   the App's `ContextRing`, the badge's size: length = the composite score, tone = the ZONE the daemon sent, nothing in the middle. No reading: the track only.
//   bars   `HostBars`: left CPU %, right the memory pressure level or memory used %, each in its own band. No reading: the grey tracks only.
// Read only from the pool (`CellView`); the per-host shapes and data, the state icon and the item count come with MON-3.
import { useEffect, useMemo, useRef, useState } from 'react'
import { resolveHostBadge } from '../../lib/host-color'
import type { UsageTone } from '../../lib/usage-display'
import type { CellFast, CellView } from '../../lib/pool/cell-model'
import { useSidebarHostBadgeGeometry } from '../../hooks/useSidebarHostBadgeGeometry'
import { useI18nStore } from '../../stores/useI18nStore'
import { HostBadge } from '../HostBadge'
import { ContextRing } from '../team/ModelIcon'
import { HostBars } from './HostBars'
import { HostCard, type HostCardAnchor } from './HostCard'
import { livenessWord } from '../../lib/pool/words'
import { cpuReading, memoryReading, pressureReading } from '../../lib/pool/bar-readings'
import type { PoolZone } from '../../lib/pool/types'

// One card at a time: the close function of the cell whose card is open (hover on B closes the card focus left on A).
let openCell: { owner: object; close: () => void } | null = null

const ZONE_TONE: Record<PoolZone, UsageTone | undefined> = { normal: 'ok', warning: 'warn', danger: 'danger', unknown: undefined }

/** The bars' kind for a host's indicator. Until MON-3b draws the per-host shapes and data, ring (and the single bar) read as the ring, the bars as the old two-bar settings. */
type CellIndicator = 'ring' | 'bars-pressure' | 'bars-memory'
function cellIndicator(look: CellView['look']): CellIndicator {
  const ind = look.indicator
  if (ind.shape !== 'bars') return 'ring'
  return ind.data[1] === 'mem' ? 'bars-memory' : 'bars-pressure'
}

export interface HostCellProps {
  view: CellView
  variant: 'wide' | 'narrow'
  isActive: boolean
  onOpen: () => void
}

/** The cell's accessible name: the host and its state word, and for a host that is ok its zone word. */
function accessibleName(t: (k: string, p?: Record<string, string | number>) => string, v: CellView): string {
  const parts: string[] = [v.name, livenessWord(t, v.state)]
  if (v.state.state === 'ok') parts.push(v.zone ? t(`fleet.zone.${v.zone}`) : t('fleet.no_data'))
  return parts.join(', ')
}

export function HostCell({ view, variant, isActive, onOpen }: HostCellProps) {
  const t = useI18nStore((s) => s.t)
  const { box, inset, radius, lineColor } = useSidebarHostBadgeGeometry()
  const indicator = cellIndicator(view.look)
  const { colors, color, icon, iconWeight } = view.look
  // Resolved as the badge beside an agent member is (KnownHostBadge: terminal for an agent), so the two read the same.
  const badge = useMemo(() => resolveHostBadge({ colors, color, icon, iconWeight }, 'terminal'), [colors, color, icon, iconWeight])
  const off = view.state.state !== 'ok'
  const f = view.fast
  const zone: PoolZone | 'offline' | 'nodata' = off ? 'offline' : f ? (view.zone ?? f.zone) : 'nodata'
  // A breathing dot while this host's worker runs something.
  const running = view.worker ? view.worker.running.heavy + view.worker.running.light > 0 : false
  const narrow = variant === 'narrow'
  // The hover card (user 10-17): on enter / focus, off on leave / blur / click. Its anchor is measured when it opens.
  const ref = useRef<HTMLButtonElement>(null)
  const [anchor, setAnchor] = useState<HostCardAnchor | null>(null)
  const [cardOpen, setCardOpen] = useState(false)
  const [owner] = useState(() => ({}))
  useEffect(() => () => { if (openCell?.owner === owner) openCell = null }, [owner])
  const openCard = () => {
    if (openCell && openCell.owner !== owner) openCell.close()
    openCell = { owner, close: () => setCardOpen(false) }
    const el = ref.current
    const strip = el?.closest('[data-testid="fleet-cells"]')
    setAnchor(el ? { cell: el.getBoundingClientRect(), strip: strip ? strip.getBoundingClientRect() : null } : null)
    setCardOpen(true)
  }
  function closeCard() {
    if (openCell?.owner === owner) openCell = null
    setCardOpen(false)
  }
  // The anchor is measured once, so close the card when anything scrolls (capture: any ancestor) or the window resizes.
  useEffect(() => {
    if (!cardOpen) return
    const close = () => { if (openCell?.owner === owner) openCell = null; setCardOpen(false) }
    window.addEventListener('scroll', close, true)
    window.addEventListener('resize', close)
    return () => {
      window.removeEventListener('scroll', close, true)
      window.removeEventListener('resize', close)
    }
  }, [cardOpen, owner])

  return (
    <button
      type="button"
      data-testid={`host-tile-${view.hostId}`}
      data-active={String(isActive)}
      data-variant={variant}
      aria-label={accessibleName(t, view)}
      ref={ref}
      onMouseEnter={openCard}
      onMouseLeave={closeCard}
      onFocus={openCard}
      onBlur={closeCard}
      onClick={() => { closeCard(); onOpen() }}
      className={`group relative flex items-center h-6 rounded-md cursor-pointer shrink-0 outline-none transition-colors ${narrow ? 'gap-0.5 px-0.5' : 'gap-1 px-[3px]'} ${isActive ? 'bg-surface-active [--cell-bg:var(--surface-active)]' : '[--cell-bg:var(--surface-tertiary)] hover:bg-surface-hover hover:[--cell-bg:var(--surface-hover)]'}`}
    >
      <span className="relative inline-flex shrink-0">
        <span data-testid={`host-badge-dim-${view.hostId}`} className={`inline-flex ${off ? 'opacity-40' : ''}`}>
          <HostBadge colors={badge.colors} icon={badge.icon} iconWeight={badge.iconWeight} box={box} inset={inset} radius={radius} lineColor={lineColor} testId={`host-badge-${view.hostId}`} bright />
        </span>
        {running && <span data-testid={`worker-running-${view.hostId}`} className="pointer-events-none absolute -bottom-px -right-px size-[6px] rounded-full bg-status-success animate-breathe" />}
      </span>
      {indicator === 'ring' ? (
        <span className="inline-flex shrink-0 ml-0.5" data-testid={`load-ring-${view.hostId}`} data-zone={zone}>
          <ContextRing pct={f?.load} model={undefined} size={box} tone={f ? ZONE_TONE[view.zone ?? f.zone] : undefined} muted={!!f && view.zone === 'unknown'} noShape />
        </span>
      ) : (
        <span className="inline-flex shrink-0 ml-0.5" data-testid={`load-bars-${view.hostId}`} data-zone={zone}>
          <HostBars left={f ? cpuReading(f.cpu) : null} right={f ? memoryBar(indicator, f) : null} size={box} />
        </span>
      )}
      {cardOpen && <HostCard view={view} placement={narrow ? 'right' : 'above'} anchor={anchor} />}
    </button>
  )
}

/** The right bar: the pressure level (memory used % when it is unknown), or memory used %. */
function memoryBar(indicator: Exclude<CellIndicator, 'ring'>, f: CellFast) {
  return indicator === 'bars-pressure' ? pressureReading(f.level, f.mem) : memoryReading(f.mem)
}
