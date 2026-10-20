// spa/src/components/fleet/QueueTooltip.tsx — the list a queue strip (and, with MON-4, the narrow sidebar's two counts) pops up on hover (monitoring tabs M5). Drawn in a portal with `position: fixed` from the anchor's
// rectangle so no overflow of the sidebar clips it, as `HostCard` is: `above` over the strip, left-aligned to it, or `right` beside the anchor; kept inside the window.
import { useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useI18nStore } from '../../stores/useI18nStore'
import type { QueueStrip } from '../../lib/pool/queue-strip'
import type { PoolJob } from '../../lib/pool/jobs-types'

const W = 260
const GAP = 8
const MARGIN = 8

const mmss = (s: number): string => `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`
const jobText = (j: PoolJob): string => [j.repo.name, j.task, ...j.args].filter((x) => x !== '').join(' ')

export interface QueueTooltipProps {
  /** The id the anchor's `aria-describedby` points at. */
  id?: string
  strip: QueueStrip
  placement: 'above' | 'right'
  anchor: DOMRect | null
}

export function QueueTooltip({ id, strip, placement, anchor }: QueueTooltipProps) {
  const t = useI18nStore((s) => s.t)
  const ref = useRef<HTMLDivElement>(null)
  const [shift, setShift] = useState({ x: 0, y: 0 })
  const total = strip.counts.heavy + strip.counts.light

  let style: React.CSSProperties = {}
  if (anchor) {
    style = placement === 'above' ? { left: anchor.left, bottom: window.innerHeight - anchor.top + GAP } : { left: anchor.right + GAP, top: anchor.top }
  }
  useLayoutEffect(() => {
    const el = ref.current
    if (!el || !anchor) return
    const r = el.getBoundingClientRect()
    if (r.width === 0 && r.height === 0) return
    const x = Math.min(0, window.innerWidth - MARGIN - (r.right - shift.x)) + Math.max(0, MARGIN - (r.left - shift.x))
    const y = Math.min(0, window.innerHeight - MARGIN - (r.bottom - shift.y)) + Math.max(0, MARGIN - (r.top - shift.y))
    if (x !== shift.x || y !== shift.y) setShift({ x, y })
  }, [anchor, placement, strip, shift])

  const body = !strip.readable ? (
    <div data-testid="queue-unreadable" className="text-text-muted">{t('pool.strip.unreadable')}</div>
  ) : total === 0 ? (
    <div className="text-text-muted">{t('pool.strip.empty')}</div>
  ) : (
    <>
      <div className="mb-1 font-semibold text-text-primary">{t('pool.strip.header', { n: total, heavy: strip.counts.heavy, light: strip.counts.light })}</div>
      {strip.top.map((e, i) => (
        <div key={`${e.job.origin}/${e.job.id}`} data-testid="queue-row" className="flex items-center gap-1.5 py-px tabular-nums text-text-secondary">
          <span data-testid="queue-row-dot" className={`inline-block size-1.5 shrink-0 rounded-full ${e.job.slot === 'light' ? 'bg-queue-light' : 'bg-queue-heavy'}`} />
          <span className="truncate">{t('pool.strip.row', { seq: i + 1, job: jobText(e.job), origin: e.job.origin, wait: mmss(e.waitS) })}</span>
        </div>
      ))}
      {total > strip.top.length && <div className="text-text-muted">{t('pool.strip.more', { n: total - strip.top.length })}</div>}
    </>
  )

  return createPortal(
    <div
      ref={ref}
      id={id}
      data-testid="queue-tooltip"
      data-placement={placement}
      role="tooltip"
      className="fixed z-50 rounded-lg border border-border-default bg-surface-elevated shadow-xl px-3 py-2.5 text-xs pointer-events-none"
      style={{ ...style, width: W, transform: shift.x || shift.y ? `translate(${shift.x}px, ${shift.y}px)` : undefined }}
    >
      {body}
      <div className="mt-1.5 text-text-muted">{t('pool.strip.hint')}</div>
    </div>,
    document.body,
  )
}
