// spa/src/components/fleet/HostCell.tsx — one host's cell of the sidebar's host row (monitoring tabs M10, M17–M19), built from the App's own pieces so it reads like the members beside it:
//   cell   a member bead's box: h-6, rounded-md, gap-1, 3 px each side; hover bg-surface-hover, selected bg-surface-active. No click / focus outline; the card opens on focus.
//   badge  the App's `HostBadge` as beside a member in the session list (the sidebar badge settings, the host's colours resolved as for an agent member: terminal, falling back to console).
//          wwd_down: lit as ever; host_down: 40 %; unknown: 60 %.
//   count  the number at the badge's top right (M6, M7): grey, yellow from a long wait, red when no host can take a job; only for a host that is ok and has something to count; 99+ above 99.
//   then, in the same square after the badge, 2 px further right than the cell's gap (`ml-0.5`), the host's indicator (its own shape and data, M9):
//   ring   the App's `ContextRing`, the badge's size: length = the item's share, tone = its band.
//   bar / bars   `HostBars`: one bar, or two (left and right item), each in its own band.
//   A host that is not ok has no readings: the indicator's place holds a bold icon instead (M17–M19): wwd_down a yellow broken link, host_down a red power symbol, unknown a grey question mark.
// Read only from the pool (`CellView`).
import { useEffect, useId, useMemo, useRef, useState } from 'react'
import { LinkBreak, Power, Question } from '@phosphor-icons/react'
import { resolveHostBadge } from '../../lib/host-color'
import type { CellView } from '../../lib/pool/cell-model'
import { indicatorReading } from '../../lib/pool/indicator'
import { useSidebarHostBadgeGeometry } from '../../hooks/useSidebarHostBadgeGeometry'
import { useI18nStore } from '../../stores/useI18nStore'
import { HostBadge } from '../HostBadge'
import { ContextRing } from '../team/ModelIcon'
import { CountBadge } from './CountBadge'
import { HostBars } from './HostBars'
import { HostCard, type HostCardAnchor } from './HostCard'
import { livenessWord } from '../../lib/pool/words'
import type { PoolZone } from '../../lib/pool/types'

// One card at a time: the close function of the cell whose card is open (hover on B closes the card focus left on A).
let openCell: { owner: object; close: () => void } | null = null

const COUNT_TONE = { neutral: 'bg-text-muted text-white', warn: 'bg-status-warning text-text-inverse', error: 'bg-status-error text-white' } as const
/** How far a badge is dimmed: wwd_down is lit as ever, host_down 40 %, unknown 60 % (M17–M19). */
const DIM = { ok: '', wwd_down: '', host_down: 'opacity-40', unknown: 'opacity-60' } as const

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
  const cardId = useId()
  const { box, inset, radius, lineColor } = useSidebarHostBadgeGeometry()
  const shape = view.look.indicator
  const { colors, color, icon, iconWeight } = view.look
  // Resolved as the badge beside an agent member is (KnownHostBadge: terminal for an agent), so the two read the same.
  const badge = useMemo(() => resolveHostBadge({ colors, color, icon, iconWeight }, 'terminal'), [colors, color, icon, iconWeight])
  const off = view.state.state !== 'ok'
  const f = view.fast
  const zone: PoolZone | 'offline' | 'nodata' = off ? 'offline' : f ? (view.zone ?? f.zone) : 'nodata'
  const first = indicatorReading(shape.data[0], view)
  const second = shape.shape === 'bars' ? indicatorReading(shape.data[1], view) : undefined
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
      aria-describedby={cardOpen ? cardId : undefined}
      ref={ref}
      onMouseEnter={openCard}
      onMouseLeave={closeCard}
      onFocus={openCard}
      onBlur={closeCard}
      onClick={() => { closeCard(); onOpen() }}
      className={`group relative flex items-center h-6 rounded-md cursor-pointer shrink-0 outline-none transition-colors ${narrow ? 'gap-0.5 px-0.5' : 'gap-1 px-[3px]'} ${isActive ? 'bg-surface-active [--cell-bg:var(--surface-active)]' : '[--cell-bg:var(--surface-tertiary)] hover:bg-surface-hover hover:[--cell-bg:var(--surface-hover)]'}`}
    >
      <span className="relative inline-flex shrink-0">
        <span data-testid={`host-badge-dim-${view.hostId}`} className={`inline-flex ${DIM[view.state.state]}`}>
          <HostBadge colors={badge.colors} icon={badge.icon} iconWeight={badge.iconWeight} box={box} inset={inset} radius={radius} lineColor={lineColor} testId={`host-badge-${view.hostId}`} bright />
        </span>
        {view.count && <CountBadge n={view.count.n} size="sm" className={`pointer-events-none ${COUNT_TONE[view.count.tone]}`} testId={`count-badge-${view.hostId}`} />}
      </span>
      {off ? (
        <span className="inline-flex shrink-0 ml-0.5 items-center justify-center" style={{ width: box, height: box }} data-testid={`state-icon-${view.hostId}`} data-state={view.state.state} data-zone="offline">
          {view.state.state === 'wwd_down' ? <LinkBreak weight="bold" size={box - 2} className="text-status-warning" />
            : view.state.state === 'host_down' ? <Power weight="bold" size={box - 2} className="text-status-error" />
            : <Question weight="bold" size={box - 2} className="text-text-muted" />}
        </span>
      ) : shape.shape === 'ring' ? (
        <span className="inline-flex shrink-0 ml-0.5" data-testid={`load-ring-${view.hostId}`} data-zone={zone}>
          <ContextRing pct={first?.pct} model={undefined} size={box} tone={first && first.tone !== 'muted' ? first.tone : undefined} muted={first?.tone === 'muted'} noShape />
        </span>
      ) : (
        <span className="inline-flex shrink-0 ml-0.5" data-testid={`load-bars-${view.hostId}`} data-zone={zone}>
          <HostBars left={first} right={second} size={box} />
        </span>
      )}
      {cardOpen && <HostCard id={cardId} view={view} placement={narrow ? 'right' : 'above'} anchor={anchor} />}
    </button>
  )
}
