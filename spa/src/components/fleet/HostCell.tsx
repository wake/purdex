// spa/src/components/fleet/HostCell.tsx — one host's cell of the sidebar's fleet row (fleet spec rev 2 §8.1, the approved
// round-5 prototype). Sized like a TeamCell: h-6, rounded-md, hover bg-surface-hover, selected bg-surface-active.
//   box    18×18 rounded-[4px] in the host's colour, the icon white (90 %, 100 % hovered / selected). Offline: the colour at
//          35 % under a fine hatch, the icon at 35 %.
//   badge  the queue count on the box's top-right corner: daemon hosts only, only while someone waits, never offline.
//          Neutral grey; status-warning from 2 min or 3 waiting; status-error while admission is held.
//   ring   16 px, the ContextRing geometry: length = the composite score, colour = the ZONE the daemon sent (never derived
//          from the score here). Offline or no reading: a dashed track, no arc.
import { useMemo } from 'react'
import { ringGeometry, ringTransform } from '../../lib/usage-display'
import { DEFAULT_HOST_ICON, isPhosphorIconName, resolveHostColors } from '../../lib/host-color'
import type { HostLook } from '../../lib/host-look'
import type { FleetHostView } from '../../lib/fleet/store'
import type { FleetZone } from '../../lib/fleet/types'
import { WorkspaceIcon } from '../../features/workspace/components/WorkspaceIcon'
import { useI18nStore } from '../../stores/useI18nStore'
import { CountBadge } from './CountBadge'

export const WAIT_LONG_MS = 120_000
const BLOCK = 18
const RING = 16
const RING_R = RING / 2 - 2
const RING_C = 2 * Math.PI * RING_R
const OFFLINE_STRIPES = 'repeating-linear-gradient(135deg, color-mix(in srgb, var(--text-muted) 45%, transparent) 0 1px, transparent 1px 4px)'
const ZONE_STROKE: Record<FleetZone, string> = { normal: 'stroke-text-secondary', warning: 'stroke-status-warning', danger: 'stroke-status-error' }

export interface HostCellProps {
  view: FleetHostView
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

export function isOffline(v: FleetHostView): boolean {
  return v.status !== 'ok'
}

function queueTone(n: number, longestMs: number, held: boolean): string {
  if (held) return 'bg-status-error text-white'
  return longestMs >= WAIT_LONG_MS || n >= 3 ? 'bg-status-warning text-text-inverse' : 'bg-text-muted text-white'
}

function tooltip(t: (k: string, p?: Record<string, string | number>) => string, v: FleetHostView, p: { waitingPrimary: boolean; primaryElsewhere: boolean }): string {
  const lines: string[] = [v.name]
  const f = v.fast
  if (isOffline(v)) lines.push(t('fleet.zone.offline'))
  else if (!f) lines.push(t('fleet.no_data'))
  else {
    lines.push(t(`fleet.zone.${f.zone}`))
    lines.push(t('fleet.cpu', { v: Math.round(f.cpu) }), t('fleet.mem', { v: Math.round(f.mem) }))
    lines.push(t('fleet.pressure', { v: f.level }), t('fleet.page_rate', { v: Math.round(f.page_rate) }), t('fleet.disk', { v: Math.round(f.disk) }))
  }
  const q = v.kind === 'daemon' && !isOffline(v) ? v.slow?.queue : undefined
  if (q && q.waiters.length > 0) {
    lines.push(t('fleet.queue', { n: q.waiters.length, s: Math.round(q.longest_wait_ms / 1000) }))
    if (q.admission_held) lines.push(t('fleet.queue_held'))
  }
  if (p.primaryElsewhere) lines.push(t('fleet.primary_elsewhere'))
  else if (p.waitingPrimary) lines.push(t('fleet.waiting_primary'))
  return lines.join('\n')
}

export function HostCell({ view, look, variant, isActive, waitingPrimary, primaryElsewhere, onOpen }: HostCellProps) {
  const t = useI18nStore((s) => s.t)
  const off = isOffline(view)
  const main = useMemo(() => resolveHostColors({ colors: look.colors, color: look.color }, 'console')?.main ?? 'var(--text-muted)', [look.colors, look.color])
  const icon = look.icon && isPhosphorIconName(look.icon) ? look.icon : DEFAULT_HOST_ICON
  const f = off ? null : view.fast
  const geo = f ? ringGeometry(f.load, 'used') : null
  const zone: FleetZone | 'offline' | 'nodata' = off ? 'offline' : f ? f.zone : 'nodata'
  const q = view.kind === 'daemon' && !off ? view.slow?.queue : undefined
  const waiting = q?.waiters.length ?? 0
  const boxStyle: React.CSSProperties = off
    ? { width: BLOCK, height: BLOCK, backgroundColor: `color-mix(in srgb, ${main} 35%, transparent)`, backgroundImage: OFFLINE_STRIPES }
    : { width: BLOCK, height: BLOCK, backgroundColor: main }
  const narrow = variant === 'narrow'

  return (
    <button
      type="button"
      data-testid={`host-tile-${view.id}`}
      data-active={String(isActive)}
      data-variant={variant}
      aria-label={view.name}
      title={tooltip(t, view, { waitingPrimary, primaryElsewhere })}
      onClick={onOpen}
      className={`group relative flex items-center h-6 rounded-md cursor-pointer shrink-0 ${narrow ? 'gap-0.5 px-0.5' : 'gap-1 pl-1 pr-1'} ${isActive ? 'bg-surface-active' : 'hover:bg-surface-hover'}`}
    >
      <span className="relative inline-flex shrink-0">
        <span data-testid={`host-block-${view.id}`} className="inline-flex items-center justify-center overflow-hidden rounded-[4px]" style={boxStyle}>
          <span data-testid={`host-block-icon-${view.id}`} className={`inline-flex text-white/90 group-hover:text-white group-data-[active=true]:text-white ${off ? 'opacity-35' : ''}`}>
            <WorkspaceIcon icon={icon} name="" size={14} weight="fill" />
          </span>
        </span>
        {q && waiting > 0 && <CountBadge n={waiting} className={queueTone(waiting, q.longest_wait_ms, q.admission_held)} testId={`queue-badge-${view.id}`} />}
      </span>
      <span className="relative inline-grid shrink-0" style={{ width: RING, height: RING }} data-testid={`load-ring-${view.id}`} data-zone={zone}>
        <svg width={RING} height={RING} viewBox={`0 0 ${RING} ${RING}`} className="absolute inset-0 block">
          <circle cx={RING / 2} cy={RING / 2} r={RING_R} fill="none" stroke="var(--border-default)" strokeWidth={2.5} strokeDasharray={geo ? undefined : '1.6 1.9'} />
          {f && geo && geo.sharePct > 0 && (
            <circle
              className={ZONE_STROKE[f.zone]} cx={RING / 2} cy={RING / 2} r={RING_R} fill="none" strokeWidth={2.5} strokeLinecap="round"
              strokeDasharray={`${((geo.sharePct / 100) * RING_C).toFixed(2)} ${RING_C.toFixed(2)}`} transform={ringTransform(RING, geo.direction)}
            />
          )}
        </svg>
      </span>
    </button>
  )
}
