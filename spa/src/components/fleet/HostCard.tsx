// spa/src/components/fleet/HostCard.tsx — the card a host cell pops up on hover / focus (the approved prototype's `Tooltip`,
// user 10-17; spec M11): the host's badge, name and zone word (its state word when it is not ok), the readings each in its tone (load with its cause, CPU, system load, memory, pressure,
// paging, swap, disk), the rows the host's indicator draws marked 「環」 or 「柱」, and the pool's rows (worker, headroom, jobs, owner, sessions, wwd, link). A host that is not ok shows its state and the lines that explain it instead of readings.
// Drawn in a portal with `position: fixed` from the anchor's rectangles, so no overflow of the sidebar can clip it; clamped so
// it never leaves the window. `above` (wide bar): over the strip, left-aligned to it. `right` (narrow bar): beside the cell.
import { useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import type { PoolZone } from '../../lib/pool/types'
import type { CellView } from '../../lib/pool/cell-model'
import { clock, driverKey, headroomReasonLines, livenessLines, livenessWord, poolOutageLines } from '../../lib/pool/words'
import { indicatorReading } from '../../lib/pool/indicator'
import type { IndicatorItem } from '../../stores/usePoolHostsStore'
import { resolveHostBadge } from '../../lib/host-color'
import { HostBadge } from '../HostBadge'
import { useSidebarHostBadgeGeometry } from '../../hooks/useSidebarHostBadgeGeometry'
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
const TONE_CLASS: Record<UsageTone, string> = { ok: '', warn: 'text-status-warning-text', danger: 'text-status-error-text' }
const pressureTone = (level: number): UsageTone => (level >= 4 ? 'danger' : level >= 2 ? 'warn' : 'ok')
const pagingTone = (rate: number): UsageTone => (rate > 800 ? 'danger' : rate > 50 ? 'warn' : 'ok')
/** The pressure level word key: 1 / 2 / 4; 3 reads as 2 (the daemon sends 1, 2, 4 or 0 = unknown). */
const levelKey = (level: number): string | null => (level >= 4 ? '4' : level >= 2 ? '2' : level >= 1 ? '1' : null)

/** 'ring' / 'bar' when the host's indicator draws this item (a `bars` indicator reads its first two, the others the first). */
type Mark = 'ring' | 'bar' | null
const markOf = (ind: CellView['look']['indicator'], item: IndicatorItem): Mark => {
  if (!(ind.shape === 'bars' ? ind.data.slice(0, 2) : ind.data.slice(0, 1)).includes(item)) return null
  return ind.shape === 'ring' ? 'ring' : 'bar'
}

function MarkTag({ mark }: { mark: Mark }) {
  const t = useI18nStore((s) => s.t)
  if (!mark) return null
  return <span data-testid="host-card-ring-tag" className="mr-1.5 rounded border border-border-default px-1 text-[10px] font-normal text-text-secondary">{t(mark === 'ring' ? 'fleet.card.ring_tag' : 'fleet.card.bar_tag')}</span>
}

function Row({ id, label, mark, children }: { id: string; label: string; mark: Mark; children: ReactNode }) {
  return (
    <div data-testid={`host-card-row-${id}`} className={`flex items-center gap-3 py-0.5 ${mark ? 'font-semibold' : ''}`}>
      <span className="w-[72px] shrink-0 text-text-secondary">{label}</span>
      <span className="flex-1 text-right tabular-nums text-text-primary">
        <MarkTag mark={mark} />
        {children}
      </span>
    </div>
  )
}

function PoolRow({ id, label, mark = null, children }: { id: string; label: string; mark?: Mark; children: ReactNode }) {
  return (
    <div data-testid={`host-card-pool-${id}`} className={`flex items-start gap-3 py-0.5 ${mark ? 'font-semibold' : ''}`}>
      <span className="w-[72px] shrink-0 text-text-secondary">{label}</span>
      <span className="flex-1 text-right text-text-primary"><MarkTag mark={mark} />{children}</span>
    </div>
  )
}

/** The pool's part of the card: worker, what it takes now (with the reason at 0), the owner, the sessions room. */
function PoolRows({ pool }: { pool: CellView }) {
  const t = useI18nStore((s) => s.t)
  const unknown = t('pool.card.unknown')
  const w = pool.worker
  const reasonLines = headroomReasonLines(pool.headroom, pool.reasons, t)
  return (
    <div className="mt-1.5 pt-1.5 border-t border-border-subtle">
      <PoolRow id="worker" label={t('pool.card.worker')} mark={markOf(pool.look.indicator, 'slots')}>
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
      <PoolRow id="jobs" label={t('pool.card.jobs')}>{t('pool.card.jobs_value', { q: pool.count?.queued ?? 0, r: pool.count?.running ?? 0 })}</PoolRow>
      {pool.owner && (
        <PoolRow id="owner" label={t('pool.card.owner')}>{t(pool.owner.active ? 'pool.card.owner_active' : 'pool.card.owner_idle', { s: pool.owner.idle_s })}</PoolRow>
      )}
      {pool.sessionsRoom !== null && <PoolRow id="sessions" label={t('pool.card.sessions')}>{t('pool.card.sessions_value', { n: pool.sessionsRoom })}</PoolRow>}
      {pool.detail && pool.fast && (
        <>
          <PoolRow id="wwd" label={t('pool.card.wwd')}>{t('pool.card.wwd_value', { v: pool.detail.version, time: clock(pool.fast.at) })}</PoolRow>
          <PoolRow id="link" label={t('pool.card.link')}>
            {pool.detail.local ? t('pool.card.link_local') : pool.detail.checkedAt !== null ? t('pool.card.link_ok', { time: clock(pool.detail.checkedAt) }) : t('pool.card.link_ok_nocheck')}
          </PoolRow>
        </>
      )}
    </div>
  )
}

export interface HostCardProps {
  view: CellView
  placement: HostCardPlacement
  /** Where the cell is on screen; null (tests, before the first layout) leaves the card unplaced. */
  anchor: HostCardAnchor | null
}

export function HostCard({ view, placement, anchor }: HostCardProps) {
  const t = useI18nStore((s) => s.t)
  const ref = useRef<HTMLDivElement>(null)
  const [shift, setShift] = useState({ x: 0, y: 0 })
  // The card lives as long as a hover: "how long ago" is worked out once, when it opens.
  const [openedAt] = useState(() => Date.now())
  const ok = view.state.state === 'ok'
  const f = view.fast
  const zone: PoolZone | 'offline' | 'nodata' = !ok ? 'offline' : f ? (view.zone ?? f.zone) : 'nodata'
  const ind = view.look.indicator
  const { box, inset, radius, lineColor } = useSidebarHostBadgeGeometry()
  const { colors, color, icon, iconWeight } = view.look
  const badge = useMemo(() => resolveHostBadge({ colors, color, icon, iconWeight }, 'terminal'), [colors, color, icon, iconWeight])
  const sysTone = f ? indicatorReading('sysload', view)?.tone : null
  const sysClass = sysTone === 'danger' ? TONE_CLASS.danger : sysTone === 'warn' ? TONE_CLASS.warn : ''
  const causeKey = view.load ? driverKey(view.load.driver) : null
  const driverWord = view.load ? (causeKey ? t(causeKey) : view.load.driver) : null
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
        <HostBadge colors={badge.colors} icon={badge.icon} iconWeight={badge.iconWeight} box={box} inset={inset} radius={radius} lineColor={lineColor} testId="host-card-badge" bright />
        <span className="text-sm font-semibold text-text-primary">{view.name}</span>
        <span data-testid="host-card-zone" className={`ml-auto text-xs ${ZONE_CLASS[zone]}`}>{ok ? (zone === 'nodata' ? t('fleet.no_data') : t(`fleet.zone.${zone}`)) : livenessWord(t, view.state)}</span>
      </div>
      {!ok && view.poolFailure && poolOutageLines(t, view.poolFailure, view.state.state === 'ok' ? null : view.state.lastFactsAt, openedAt).map((line) => <div key={line} data-testid="host-card-pool-outage" className="text-text-muted">{line}</div>)}
      {!ok && !view.poolFailure && livenessLines(t, view.state, openedAt).map((line) => <div key={line} data-testid="host-card-liveness" className="text-text-muted">{line}</div>)}
      {f && (
        <>
          <Row id="load" label={t('pool.card.load')} mark={markOf(ind, 'load')}>
            <span className={ZONE_CLASS[zone]}>{Math.round(f.load)}%</span>
            {driverWord && <span className="text-text-muted">{t('pool.card.load_cause', { d: driverWord })}</span>}
          </Row>
          <Row id="cpu" label={t('fleet.card.cpu')} mark={markOf(ind, 'cpu')}>{pct(f.cpu)}</Row>
          <Row id="sysload" label={t('pool.card.sysload')} mark={markOf(ind, 'sysload')}>
            <span className={sysClass}>{t('pool.card.sysload_value', { l: f.load1.toFixed(1), n: f.ncpu })}</span>
          </Row>
          <Row id="mem" label={t('fleet.card.mem')} mark={markOf(ind, 'mem')}>
            {pct(f.mem)}
            {view.detail && <span className="text-text-muted">{t('pool.card.mem_avail', { v: view.detail.memAvailGb.toFixed(1) })}</span>}
          </Row>
          <Row id="pressure" label={t('fleet.card.pressure')} mark={markOf(ind, 'pressure')}>
            {lvl ? <span className={TONE_CLASS[pressureTone(f.level)]}>{t(`fleet.card.level.${lvl}`)}</span> : '—'}
            {view.psi && <span className="text-text-muted">{t('pool.card.psi', { some: view.psi.some10.toFixed(1), full: view.psi.full10.toFixed(1) })}</span>}
          </Row>
          <Row id="paging" label={t('fleet.card.paging')} mark={null}>
            <span className={TONE_CLASS[pagingTone(f.page_rate)]}>{t('fleet.card.paging_value', { v: Math.round(f.page_rate).toLocaleString() })}</span>
          </Row>
          <Row id="swap" label={t('pool.card.swap')} mark={null}>
            {view.detail?.swapUsedGb != null ? t('pool.card.swap_value', { v: view.detail.swapUsedGb.toFixed(1) }) : '—'}
          </Row>
          <Row id="disk" label={t('fleet.card.disk')} mark={markOf(ind, 'disk')}>
            {pct(f.disk)}
            {f.disk_free_gb > 0 && <span className="text-text-muted">{t('fleet.card.disk_free', { v: Math.round(f.disk_free_gb) })}</span>}
          </Row>
        </>
      )}
      {ok && <PoolRows pool={view} />}
    </div>
  )
  return createPortal(card, document.body)
}
