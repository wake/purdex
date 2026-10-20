// spa/src/components/fleet/HostCell.tsx — one host's cell of the sidebar's fleet row (fleet spec rev 2 §8.1), built from the
// App's own pieces so it reads like the members beside it (the user's 10-16 call: the first look was too bright and too wide).
//   cell   a member bead's box: h-6, rounded-md, gap-1, 3 px each side (a bead's wider left padding is room for the subagent
//          dots, which a host has not); hover bg-surface-hover, selected bg-surface-active.
//          No click / focus outline (the browser's default ring is off; the user 10-17) — the card still opens on focus.
//   badge  the App's `HostBadge` as beside a member in the session list: the sidebar badge settings (size, inset, corner, line
//          colour), the host's colours resolved as for an agent member (terminal, falling back to console) — the light
//          layer behind, the icon in the middle layer (main on hover). Offline: the badge dimmed.
//   hover  a card (HostCard) with the host's readings, the ring's driver and the queue; no native title. The badge icon is always
//          the highlighted look (`bright`: main colour, or text-secondary when plain).
//   queue  the count on the badge's top-right corner, `CountBadge` at its small size (9 px, 5 px out of the corner; journal v5 U9):
//          daemon hosts only, only while someone waits, never offline. Neutral grey; status-warning from 2 min or 3 waiting;
//          status-error while admission is held. More digits widen it to the left, so it reaches no closer to the indicator.
//          It takes no clicks: in the narrow column (no gap) it reaches 1 px over the cell above, which keeps that pixel.
//   then, by the 「主機格指示」 setting (`fleetCellIndicator`, the user 10-16), in the same square after the badge, 2 px further
//   right than the cell's gap (`ml-0.5`, U9: room for the queue count; 6 px from the badge in the wide bar, 4 px in the narrow,
//   whose 40 px cell still fits the narrow bar's 41 px):
//   ring   (default) the App's `ContextRing` (the member status ring), the badge's size: length = the composite score, tone =
//          the ZONE the daemon sent (normal ok / warning warn / danger danger — never derived from the score here), nothing
//          in the middle (a host has no model). Offline or no reading: the track only.
//   bars   `HostBars`: left CPU %, right the memory pressure level (`bars-pressure`, memory used % when the level is unknown)
//          or memory used % (`bars-memory`), each in its own band. Offline or no reading: the grey tracks only.
import { useEffect, useMemo, useRef, useState } from 'react'
import { LinkBreak } from '@phosphor-icons/react'
import { resolveHostBadge } from '../../lib/host-color'
import type { HostLook } from '../../lib/host-look'
import type { UsageTone } from '../../lib/usage-display'
import type { FleetHostView } from '../../lib/fleet/store'
import type { FleetFast } from '../../lib/fleet/types'
import { useSidebarHostBadgeGeometry } from '../../hooks/useSidebarHostBadgeGeometry'
import { useI18nStore } from '../../stores/useI18nStore'
import { useUISettingsStore, isFleetCellIndicator, FLEET_CELL_INDICATOR_DEFAULT, type FleetCellIndicator } from '../../stores/useUISettingsStore'
import { HostBadge } from '../HostBadge'
import { ContextRing } from '../team/ModelIcon'
import { CountBadge } from './CountBadge'
import { HostBars } from './HostBars'
import { HostCard, type HostCardAnchor } from './HostCard'
import { fallbackWord } from '../../lib/pool/words'
import { cpuReading, memoryReading, pressureReading } from '../../lib/fleet/bar-readings'
import type { PoolCellExtra } from '../../lib/pool/fleet-bridge'
import type { PoolZone } from '../../lib/pool/types'

// One card at a time: the close function of the cell whose card is open (hover on B closes the card focus left on A).
let openCell: { owner: object; close: () => void } | null = null

export const WAIT_LONG_MS = 120_000
const ZONE_TONE: Record<PoolZone, UsageTone | undefined> = { normal: 'ok', warning: 'warn', danger: 'danger', unknown: undefined }

export interface HostCellProps {
  view: FleetHostView
  /** What the resource pool adds (worker, headroom, the zone `unknown`); null while the pool is not the source: the cell is then the fleet's. */
  pool?: PoolCellExtra | null
  /** The host's look (name excluded): from the App's host config for a daemon host, from the wire for a probe host. */
  look: Pick<HostLook, 'colors' | 'color' | 'icon' | 'iconWeight'>
  variant: 'wide' | 'narrow'
  isActive: boolean
  /** The primary is gone or silent: the values shown are the last ones. */
  waitingPrimary: boolean
  /** Only a standby answered: the primary is on a daemon this App has no connection to. */
  primaryElsewhere: boolean
  onOpen: () => void
}

function isOffline(v: FleetHostView): boolean {
  return v.status !== 'ok'
}

function queueTone(n: number, longestMs: number, held: boolean): string {
  if (held) return 'bg-status-error text-white'
  return longestMs >= WAIT_LONG_MS || n >= 3 ? 'bg-status-warning text-text-inverse' : 'bg-text-muted text-white'
}

/** The cell's accessible name: the host, its zone word, the queue summary when one is shown, and the waiting-primary notice. */
function accessibleName(t: (k: string, p?: Record<string, string | number>) => string, v: FleetHostView, p: { waitingPrimary: boolean; primaryElsewhere: boolean }, zoneOf?: PoolZone | null, pool?: PoolCellExtra | null): string {
  const parts: string[] = [v.name]
  if (isOffline(v)) parts.push(t('fleet.zone.offline'))
  else if (!v.fast) parts.push(t('fleet.no_data'))
  else parts.push(t(`fleet.zone.${zoneOf ?? v.fast.zone}`))
  if (pool?.state === 'fallback') parts.push(fallbackWord(t, pool.reason, null))
  const q = v.kind === 'daemon' && !isOffline(v) ? v.slow?.queue : undefined
  if (q && q.waiters.length > 0) {
    parts.push(t('fleet.queue', { n: q.waiters.length, s: Math.round(q.longest_wait_s) }))
    if (q.admission_held) parts.push(t('fleet.queue_held'))
  }
  if (p.primaryElsewhere) parts.push(t('fleet.primary_elsewhere'))
  else if (p.waitingPrimary) parts.push(t('fleet.waiting_primary'))
  return parts.join(', ')
}

export function HostCell({ view, pool = null, look, variant, isActive, waitingPrimary, primaryElsewhere, onOpen }: HostCellProps) {
  const t = useI18nStore((s) => s.t)
  const { box, inset, radius, lineColor } = useSidebarHostBadgeGeometry()
  const stored = useUISettingsStore((s) => s.fleetCellIndicator)
  const indicator: FleetCellIndicator = isFleetCellIndicator(stored) ? stored : FLEET_CELL_INDICATOR_DEFAULT
  const { colors, color, icon, iconWeight } = look
  // Resolved as the badge beside an agent member is (KnownHostBadge: terminal for an agent), so the two read the same.
  const badge = useMemo(() => resolveHostBadge({ colors, color, icon, iconWeight }, 'terminal'), [colors, color, icon, iconWeight])
  const off = isOffline(view)
  const f = off ? null : view.fast
  const zone: PoolZone | 'offline' | 'nodata' = off ? 'offline' : f ? (pool?.zone ?? f.zone) : 'nodata'
  // A breathing dot while this host's worker runs something; the count at the badge's top right is left as it is today (the user, WWD-7 Q2: reworked as a whole later).
  const running = !off && pool?.worker ? pool.worker.running.heavy + pool.worker.running.light > 0 : false
  const q = view.kind === 'daemon' && !off ? view.slow?.queue : undefined
  const waiting = q?.waiters.length ?? 0
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
      data-testid={`host-tile-${view.id}`}
      data-active={String(isActive)}
      data-variant={variant}
      aria-label={accessibleName(t, view, { waitingPrimary, primaryElsewhere }, pool?.zone, pool)}
      ref={ref}
      onMouseEnter={openCard}
      onMouseLeave={closeCard}
      onFocus={openCard}
      onBlur={closeCard}
      onClick={() => { closeCard(); onOpen() }}
      className={`group relative flex items-center h-6 rounded-md cursor-pointer shrink-0 outline-none transition-colors ${narrow ? 'gap-0.5 px-0.5' : 'gap-1 px-[3px]'} ${isActive ? 'bg-surface-active [--cell-bg:var(--surface-active)]' : '[--cell-bg:var(--surface-tertiary)] hover:bg-surface-hover hover:[--cell-bg:var(--surface-hover)]'}`}
    >
      <span className="relative inline-flex shrink-0">
        <span data-testid={`host-badge-dim-${view.id}`} className={`inline-flex ${off ? 'opacity-40' : ''}`}>
          <HostBadge colors={badge.colors} icon={badge.icon} iconWeight={badge.iconWeight} box={box} inset={inset} radius={radius} lineColor={lineColor} testId={`host-badge-${view.id}`} bright />
        </span>
        {/* wwd does not answer (or its numbers stopped) but the fleet still reads the host over SSH: the readings stay, the break shows (spec §10) */}
        {pool?.state === 'fallback' && (
          <LinkBreak data-testid={`wwd-link-${view.id}`} weight="bold" size={8} aria-hidden className="pointer-events-none absolute -bottom-px -left-px rounded-full text-status-warning" style={{ boxShadow: '0 0 0 1px var(--cell-bg)' }} />
        )}
        {running && <span data-testid={`worker-running-${view.id}`} className="pointer-events-none absolute -bottom-px -right-px size-[6px] rounded-full bg-status-success animate-breathe" />}
        {q && waiting > 0 && <CountBadge n={waiting} size="sm" className={`pointer-events-none ${queueTone(waiting, q.longest_wait_s * 1000, q.admission_held)}`} testId={`queue-badge-${view.id}`} />}
      </span>
      {indicator === 'ring' ? (
        <span className="inline-flex shrink-0 ml-0.5" data-testid={`load-ring-${view.id}`} data-zone={zone}>
          <ContextRing pct={f?.load} model={undefined} size={box} tone={f ? ZONE_TONE[pool?.zone ?? f.zone] : undefined} muted={!!f && pool?.zone === 'unknown'} noShape />
        </span>
      ) : (
        <span className="inline-flex shrink-0 ml-0.5" data-testid={`load-bars-${view.id}`} data-zone={zone}>
          <HostBars left={f ? cpuReading(f.cpu) : null} right={f ? memoryBar(indicator, f) : null} size={box} />
        </span>
      )}
      {cardOpen && <HostCard view={view} pool={pool} waitingPrimary={waitingPrimary} primaryElsewhere={primaryElsewhere} placement={narrow ? 'right' : 'above'} anchor={anchor} />}
    </button>
  )
}

/** The right bar: the pressure level (memory used % when it is unknown), or memory used %. */
function memoryBar(indicator: Exclude<FleetCellIndicator, 'ring'>, f: FleetFast) {
  return indicator === 'bars-pressure' ? pressureReading(f.level, f.mem) : memoryReading(f.mem)
}
