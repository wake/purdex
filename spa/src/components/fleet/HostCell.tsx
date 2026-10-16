// spa/src/components/fleet/HostCell.tsx — one host's cell of the sidebar's fleet row (fleet spec rev 2 §8.1), built from the
// App's own pieces so it reads like the members beside it (the user's 10-16 call: the first look was too bright and too wide).
//   cell   a member bead's box: h-6, rounded-md, gap-1, 3 px each side (a bead's wider left padding is room for the subagent
//          dots, which a host has not); hover bg-surface-hover, selected bg-surface-active.
//   badge  the App's `HostBadge` as beside a member in the session list: the sidebar badge settings (size, inset, corner, line
//          colour), the host's colours resolved as for an agent member (terminal, falling back to console) — the light
//          layer behind, the icon in the middle layer (main on hover). Offline: the badge dimmed.
//   queue  the count on the badge's top-right corner: daemon hosts only, only while someone waits, never offline. Neutral grey;
//          status-warning from 2 min or 3 waiting; status-error while admission is held.
//   ring   the App's `ContextRing` (the member status ring), the badge's size: length = the composite score, tone = the ZONE
//          the daemon sent (normal ok / warning warn / danger danger — never derived from the score here), nothing in the
//          middle (a host has no model). Offline or no reading: the track only.
import { useMemo } from 'react'
import { resolveHostBadge } from '../../lib/host-color'
import type { HostLook } from '../../lib/host-look'
import type { UsageTone } from '../../lib/usage-display'
import type { FleetHostView } from '../../lib/fleet/store'
import type { FleetZone } from '../../lib/fleet/types'
import { useSidebarHostBadgeGeometry } from '../../hooks/useSidebarHostBadgeGeometry'
import { useI18nStore } from '../../stores/useI18nStore'
import { HostBadge } from '../HostBadge'
import { ContextRing } from '../team/ModelIcon'
import { CountBadge } from './CountBadge'

export const WAIT_LONG_MS = 120_000
const ZONE_TONE: Record<FleetZone, UsageTone> = { normal: 'ok', warning: 'warn', danger: 'danger' }

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

function isOffline(v: FleetHostView): boolean {
  return v.status !== 'ok'
}

function queueTone(n: number, longestMs: number, held: boolean): string {
  if (held) return 'bg-status-error text-white'
  return longestMs >= WAIT_LONG_MS || n >= 3 ? 'bg-status-warning text-text-inverse' : 'bg-text-muted text-white'
}

/** The cell's accessible name: the host, its zone word, the queue summary when one is shown, and the waiting-primary notice. */
function accessibleName(t: (k: string, p?: Record<string, string | number>) => string, v: FleetHostView, p: { waitingPrimary: boolean; primaryElsewhere: boolean }): string {
  const parts: string[] = [v.name]
  if (isOffline(v)) parts.push(t('fleet.zone.offline'))
  else if (!v.fast) parts.push(t('fleet.no_data'))
  else parts.push(t(`fleet.zone.${v.fast.zone}`))
  const q = v.kind === 'daemon' && !isOffline(v) ? v.slow?.queue : undefined
  if (q && q.waiters.length > 0) {
    parts.push(t('fleet.queue', { n: q.waiters.length, s: Math.round(q.longest_wait_s) }))
    if (q.admission_held) parts.push(t('fleet.queue_held'))
  }
  if (p.primaryElsewhere) parts.push(t('fleet.primary_elsewhere'))
  else if (p.waitingPrimary) parts.push(t('fleet.waiting_primary'))
  return parts.join(', ')
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
    lines.push(t('fleet.queue', { n: q.waiters.length, s: Math.round(q.longest_wait_s) }))
    if (q.admission_held) lines.push(t('fleet.queue_held'))
  }
  if (p.primaryElsewhere) lines.push(t('fleet.primary_elsewhere'))
  else if (p.waitingPrimary) lines.push(t('fleet.waiting_primary'))
  return lines.join('\n')
}

export function HostCell({ view, look, variant, isActive, waitingPrimary, primaryElsewhere, onOpen }: HostCellProps) {
  const t = useI18nStore((s) => s.t)
  const { box, inset, radius, lineColor } = useSidebarHostBadgeGeometry()
  const { colors, color, icon, iconWeight } = look
  // Resolved as the badge beside an agent member is (KnownHostBadge: terminal for an agent), so the two read the same.
  const badge = useMemo(() => resolveHostBadge({ colors, color, icon, iconWeight }, 'terminal'), [colors, color, icon, iconWeight])
  const off = isOffline(view)
  const f = off ? null : view.fast
  const zone: FleetZone | 'offline' | 'nodata' = off ? 'offline' : f ? f.zone : 'nodata'
  const q = view.kind === 'daemon' && !off ? view.slow?.queue : undefined
  const waiting = q?.waiters.length ?? 0
  const narrow = variant === 'narrow'

  return (
    <button
      type="button"
      data-testid={`host-tile-${view.id}`}
      data-active={String(isActive)}
      data-variant={variant}
      aria-label={accessibleName(t, view, { waitingPrimary, primaryElsewhere })}
      title={tooltip(t, view, { waitingPrimary, primaryElsewhere })}
      onClick={onOpen}
      className={`group relative flex items-center h-6 rounded-md cursor-pointer shrink-0 transition-colors ${narrow ? 'gap-0.5 px-0.5' : 'gap-1 px-[3px]'} ${isActive ? 'bg-surface-active' : 'hover:bg-surface-hover'}`}
    >
      <span className="relative inline-flex shrink-0">
        <span data-testid={`host-badge-dim-${view.id}`} className={`inline-flex ${off ? 'opacity-40' : ''}`}>
          <HostBadge colors={badge.colors} icon={badge.icon} iconWeight={badge.iconWeight} box={box} inset={inset} radius={radius} lineColor={lineColor} testId={`host-badge-${view.id}`} />
        </span>
        {q && waiting > 0 && <CountBadge n={waiting} className={queueTone(waiting, q.longest_wait_s * 1000, q.admission_held)} testId={`queue-badge-${view.id}`} />}
      </span>
      <span className="inline-flex shrink-0" data-testid={`load-ring-${view.id}`} data-zone={zone}>
        <ContextRing pct={f?.load} model={undefined} size={box} tone={f ? ZONE_TONE[f.zone] : undefined} noShape />
      </span>
    </button>
  )
}
