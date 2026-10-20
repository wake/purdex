// spa/src/components/fleet/HostCard.tsx — the card a host cell pops up on hover / focus (the approved prototype's `Tooltip`,
// user 10-17): the host's name and zone word, the five readings each in its tone, the reading that sets the ring's length
// marked 「環」, the gate's queue for a daemon host, and the waiting notices as the last line.
// Drawn in a portal with `position: fixed` from the anchor's rectangles, so no overflow of the sidebar can clip it; clamped so
// it never leaves the window. `above` (wide bar): over the strip, left-aligned to it. `right` (narrow bar): beside the cell.
import { useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import type { FleetHostView } from '../../lib/fleet/store'
import type { PoolZone } from '../../lib/pool/types'
import type { PoolCellExtra } from '../../lib/pool/fleet-bridge'
import { fallbackWord, headroomReasonLines } from '../../lib/pool/words'
import { ringDriver, type RingDriver } from '../../lib/fleet/ring-driver'
import { usageTone, type UsageTone } from '../../lib/usage-display'
import { useI18nStore } from '../../stores/useI18nStore'

export type HostCardPlacement = 'above' | 'right'
export interface HostCardAnchor { cell: DOMRect; strip: DOMRect | null }

const CARD_W = 220
const GAP = 8
const MARGIN = 8

const ZONE_CLASS: Record<PoolZone | 'offline' | 'nodata', string> = {
  normal: 'text-status-success-text', warning: 'text-status-warning-text', danger: 'text-status-error-text', unknown: 'text-text-muted', offline: 'text-text-muted', nodata: 'text-text-muted',
}
const RING_DRIVERS: readonly string[] = ['cpu', 'mem', 'pressure', 'paging', 'disk']
const TONE_CLASS: Record<UsageTone, string> = { ok: '', warn: 'text-status-warning-text', danger: 'text-status-error-text' }
const pressureTone = (level: number): UsageTone => (level >= 4 ? 'danger' : level >= 2 ? 'warn' : 'ok')
const pagingTone = (rate: number): UsageTone => (rate > 800 ? 'danger' : rate > 50 ? 'warn' : 'ok')
/** The pressure level word key: 1 / 2 / 4; 3 reads as 2 (the daemon sends 1, 2, 4 or 0 = unknown). */
const levelKey = (level: number): string | null => (level >= 4 ? '4' : level >= 2 ? '2' : level >= 1 ? '1' : null)

function mmss(seconds: number): string {
  const s = Math.max(0, Math.round(seconds))
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`
}

function Row({ id, label, driver, children }: { id: RingDriver; label: string; driver: RingDriver | null; children: ReactNode }) {
  const t = useI18nStore((s) => s.t)
  const marked = driver === id
  return (
    <div data-testid={`host-card-row-${id}`} className={`flex items-center gap-3 py-0.5 ${marked ? 'font-semibold' : ''}`}>
      <span className="w-[72px] shrink-0 text-text-secondary">{label}</span>
      <span className="flex-1 text-right tabular-nums text-text-primary">
        {marked && <span data-testid="host-card-ring-tag" className="mr-1.5 rounded border border-border-default px-1 text-[10px] font-normal text-text-secondary">{t('fleet.card.ring_tag')}</span>}
        {children}
      </span>
    </div>
  )
}

function PoolRow({ id, label, children }: { id: string; label: string; children: ReactNode }) {
  return (
    <div data-testid={`host-card-pool-${id}`} className="flex items-start gap-3 py-0.5">
      <span className="w-[72px] shrink-0 text-text-secondary">{label}</span>
      <span className="flex-1 text-right text-text-primary">{children}</span>
    </div>
  )
}

/** The pool's part of the card: worker, what it takes now (with the reason at 0), the owner, the sessions room. */
function PoolRows({ pool }: { pool: PoolCellExtra }) {
  const t = useI18nStore((s) => s.t)
  const unknown = t('pool.card.unknown')
  const w = pool.worker
  const reasonLines = headroomReasonLines(pool.headroom, pool.reasons, t)
  return (
    <div className="mt-1.5 pt-1.5 border-t border-border-subtle">
      <PoolRow id="worker" label={t('pool.card.worker')}>
        {!w ? unknown : w.enabled
          ? t('pool.card.worker_on', { hr: w.running.heavy, hs: w.slots.heavy, lr: w.running.light, ls: w.slots.light })
          : t('pool.card.worker_off')}
      </PoolRow>
      <PoolRow id="headroom" label={t('pool.card.headroom')}>
        {pool.headroom ? (
          <>
            {t('pool.card.headroom_value', { h: pool.headroom.heavy, l: pool.headroom.light })}
            {reasonLines.map((w) => <span key={w} data-testid="host-card-pool-reason" className="block text-text-muted">{w}</span>)}
          </>
        ) : unknown}
      </PoolRow>
      {pool.owner && (
        <PoolRow id="owner" label={t('pool.card.owner')}>{t(pool.owner.active ? 'pool.card.owner_active' : 'pool.card.owner_idle', { s: pool.owner.idle_s })}</PoolRow>
      )}
      {pool.sessionsRoom !== null && <PoolRow id="sessions" label={t('pool.card.sessions')}>{t('pool.card.sessions_value', { n: pool.sessionsRoom })}</PoolRow>}
    </div>
  )
}

export interface HostCardProps {
  view: FleetHostView
  /** What the resource pool adds; null while the pool is not the source: the card is then the fleet's. */
  pool?: PoolCellExtra | null
  waitingPrimary: boolean
  primaryElsewhere: boolean
  placement: HostCardPlacement
  /** Where the cell is on screen; null (tests, before the first layout) leaves the card unplaced. */
  anchor: HostCardAnchor | null
}

export function HostCard({ view, pool = null, waitingPrimary, primaryElsewhere, placement, anchor }: HostCardProps) {
  const t = useI18nStore((s) => s.t)
  const ref = useRef<HTMLDivElement>(null)
  const [shift, setShift] = useState({ x: 0, y: 0 })
  const off = view.status !== 'ok'
  const f = off ? null : view.fast
  const zone: PoolZone | 'offline' | 'nodata' = off ? 'offline' : f ? (pool?.zone ?? f.zone) : 'nodata'
  const q = view.kind === 'daemon' && !off ? view.slow?.queue : undefined
  // The ring's driver is the daemon's own when it sent a load; otherwise worked out from the readings as before.
  const poolDriver = pool?.load && RING_DRIVERS.includes(pool.load.driver) ? (pool.load.driver as RingDriver) : null
  const driver = f ? (poolDriver ?? ringDriver(f)) : null
  const pct = (v: number) => <span className={TONE_CLASS[usageTone(Math.round(v))]}>{Math.round(v)}%</span>
  const lvl = f ? levelKey(f.level) : null

  let style: React.CSSProperties = {}
  if (anchor) {
    const base = placement === 'above' ? (anchor.strip ?? anchor.cell) : anchor.cell
    style = placement === 'above'
      ? { left: base.left, bottom: window.innerHeight - base.top + GAP }
      : { left: anchor.cell.right + GAP, top: anchor.cell.top }
  }

  // Clamp: keep the whole card inside the window (measured after layout).
  useLayoutEffect(() => {
    const el = ref.current
    if (!el || !anchor) return
    const r = el.getBoundingClientRect()
    if (r.width === 0 && r.height === 0) return
    const x = Math.min(0, window.innerWidth - MARGIN - (r.right - shift.x)) + Math.max(0, MARGIN - (r.left - shift.x))
    const y = Math.min(0, window.innerHeight - MARGIN - (r.bottom - shift.y)) + Math.max(0, MARGIN - (r.top - shift.y))
    if (x !== shift.x || y !== shift.y) setShift({ x, y })
  }, [anchor, placement, view, shift])

  const card = (
    <div
      ref={ref}
      data-testid="host-card"
      data-placement={placement}
      role="tooltip"
      className="fixed z-50 w-[220px] rounded-lg border border-border-default bg-surface-elevated shadow-xl px-3 py-2.5 text-xs pointer-events-none"
      style={{ ...style, width: CARD_W, transform: shift.x || shift.y ? `translate(${shift.x}px, ${shift.y}px)` : undefined }}
    >
      <div className="flex items-center gap-1.5 mb-1.5">
        <span className="text-sm font-semibold text-text-primary">{view.name}</span>
        <span data-testid="host-card-zone" className={`ml-auto text-xs ${ZONE_CLASS[zone]}`}>{zone === 'nodata' ? t('fleet.no_data') : t(`fleet.zone.${zone}`)}</span>
      </div>
      {off && view.last_ok_at !== null && (
        <div className="text-text-muted">{t('fleet.card.last_ok', { t: new Date(view.last_ok_at).toLocaleString() })}</div>
      )}
      {f && (
        <>
          <Row id="cpu" label={t('fleet.card.cpu')} driver={driver}>{pct(f.cpu)}</Row>
          <Row id="mem" label={t('fleet.card.mem')} driver={driver}>{pct(f.mem)}</Row>
          <Row id="pressure" label={t('fleet.card.pressure')} driver={driver}>
            {lvl ? <span className={TONE_CLASS[pressureTone(f.level)]}>{t(`fleet.card.level.${lvl}`)}</span> : '—'}
            {pool?.psi && <span className="text-text-muted">{t('pool.card.psi', { some: pool.psi.some10.toFixed(1), full: pool.psi.full10.toFixed(1) })}</span>}
          </Row>
          <Row id="paging" label={t('fleet.card.paging')} driver={driver}>
            <span className={TONE_CLASS[pagingTone(f.page_rate)]}>{t('fleet.card.paging_value', { v: Math.round(f.page_rate).toLocaleString() })}</span>
          </Row>
          <Row id="disk" label={t('fleet.card.disk')} driver={driver}>
            {pct(f.disk)}
            {f.disk_free_gb > 0 && <span className="text-text-muted">{t('fleet.card.disk_free', { v: Math.round(f.disk_free_gb) })}</span>}
          </Row>
        </>
      )}
      {pool && pool.state !== 'offline' && <PoolRows pool={pool} />}
      {pool?.state === 'fallback' && (
        <div data-testid="host-card-pool-fallback" className="mt-1 text-text-muted">
          {fallbackWord(t, pool.reason, pool.since)}
        </div>
      )}
      {q && (
        <div className="mt-1.5 pt-1.5 border-t border-border-subtle text-text-secondary">
          <div data-testid="host-card-slots">{t('fleet.card.slots', { hu: q.heavy_used, ht: q.heavy_total, lu: q.light_used, lt: q.light_total })}</div>
          <div data-testid="host-card-queue">
            {t('fleet.card.queue_pre')}<span className={q.waiters.length > 0 ? 'text-status-warning-text' : ''}>{q.waiters.length}</span>
            {q.waiters.length > 0 && t('fleet.card.queue_longest', { t: mmss(q.longest_wait_s) })}
          </div>
          {q.admission_held && <div data-testid="host-card-held" className="text-status-error-text">{t('fleet.card.held')}</div>}
        </div>
      )}
      {(primaryElsewhere || waitingPrimary) && (
        <div className="mt-1.5 text-text-muted">{t(primaryElsewhere ? 'fleet.primary_elsewhere' : 'fleet.waiting_primary')}</div>
      )}
    </div>
  )
  return createPortal(card, document.body)
}
