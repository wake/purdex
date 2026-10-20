// spa/src/components/watch/jobs/QueueRoutes.tsx — the lines of the queue flow (monitoring tabs spec M29, M30): from the queue pool cell a job was sent from to the job's card in the worker pool, one per job being prepared or run, in the colour
// of the host that sent it. An SVG over the flow; it measures the cells (`data-pool`, `data-wk`, `data-j`, the two rows `data-qtop`, `data-qbot`) and draws with `lib/pool/queue-routes`. It measures again when the flow changes size, when
// the fonts arrive, and when the cells or cards change. Static: no <animate>, no transition (M28). Only pointing lights a line: the others stay as they are.
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import type { RefObject } from 'react'
import { candidatesOf, linesOf, routesOf, type FlowLayout, type Rect } from '../../../lib/pool/queue-routes'

/** A job being prepared or run: its card key, the queue pool cell it came from and the worker cell it is in (both `data-pool` / `data-wk` values). */
export interface ActiveCard { key: string; pool: string; worker: string }
/** The dashed lines of a waiting job that is pointed at: from its pool to each worker cell that could take it. */
export interface CandidateSpec { pool: string; workers: readonly string[] }

interface Measured { layout: FlowLayout; w: number; h: number }

function measure(flow: HTMLElement, active: readonly ActiveCard[]): Measured | null {
  const top = flow.querySelector<HTMLElement>('[data-qtop]')
  const bot = flow.querySelector<HTMLElement>('[data-qbot]')
  if (!top || !bot) return null
  const box = flow.getBoundingClientRect()
  const rel = (e: Element): Rect => {
    const r = e.getBoundingClientRect()
    return { l: r.left - box.left, r: r.right - box.left, t: r.top - box.top, b: r.bottom - box.top, w: r.width }
  }
  const byAttr = (attr: string): Record<string, Rect> => {
    const out: Record<string, Rect> = {}
    for (const e of flow.querySelectorAll(`[${attr}]`)) out[e.getAttribute(attr) ?? ''] = rel(e)
    return out
  }
  const cardRects = byAttr('data-j')
  const cards = active.flatMap((a) => (cardRects[a.key] ? [{ key: a.key, rect: cardRects[a.key], pool: a.pool, worker: a.worker }] : []))
  return {
    w: box.width, h: box.height,
    layout: { top: rel(top), bot: rel(bot), topLines: linesOf(Array.from(top.children).map(rel)), botLines: linesOf(Array.from(bot.children).map(rel)), pools: byAttr('data-pool'), workers: byAttr('data-wk'), cards },
  }
}

export function QueueRoutes({ flowRef, active, highlighted, candidate, colorOf }: {
  flowRef: RefObject<HTMLElement | null>
  active: readonly ActiveCard[]
  /** The cards whose routes are lit. */
  highlighted: ReadonlySet<string>
  candidate: CandidateSpec | null
  /** The colour of the lines from a queue pool cell (a `data-pool` value); undefined: the muted text colour. */
  colorOf: (pool: string) => string | undefined
}) {
  const [m, setM] = useState<Measured | null>(null)
  const last = useRef('')
  const redraw = useCallback(() => {
    const flow = flowRef.current
    const next = flow ? measure(flow, active) : null
    const sig = JSON.stringify(next)
    if (sig === last.current) return
    last.current = sig
    setM(next)
  }, [flowRef, active])
  // the cells and cards that exist change with `active` (the flow's own changes); the size and the fonts have their own triggers
  // eslint-disable-next-line react-hooks/set-state-in-effect -- the layout is measured from the DOM after it is laid out
  useLayoutEffect(() => { redraw() }, [redraw])
  useEffect(() => {
    const flow = flowRef.current
    if (!flow) return
    // the first drawing: the layout effect above runs before the parent's ref is set on the first mount
    // eslint-disable-next-line react-hooks/set-state-in-effect
    redraw()
    let frame = 0
    const again = () => { cancelAnimationFrame(frame); frame = requestAnimationFrame(redraw) }
    const ro = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(again)
    ro?.observe(flow)
    let alive = true
    void document.fonts?.ready.then(() => { if (alive) redraw() })
    return () => { alive = false; cancelAnimationFrame(frame); ro?.disconnect() }
  }, [flowRef, redraw])

  const routes = useMemo(() => (m ? routesOf(m.layout) : []), [m])
  const cands = useMemo(() => (m && candidate ? candidatesOf(m.layout, candidate.pool, candidate.workers) : []), [m, candidate])
  if (!m) return null
  // the lit routes are drawn last, so they are on top; nothing else about the others changes
  const ordered = [...routes.filter((r) => !highlighted.has(r.key)), ...routes.filter((r) => highlighted.has(r.key))]
  return (
    <svg data-testid="queue-routes" aria-hidden="true" className="pointer-events-none absolute inset-0" width={m.w} height={m.h} viewBox={`0 0 ${m.w} ${m.h}`}>
      {ordered.map((r) => {
        const hl = highlighted.has(r.key)
        const color = colorOf(r.pool)
        return (
          <g key={r.key} data-r={r.key} data-hl={hl ? 'true' : undefined} className={color ? undefined : 'text-text-muted'} style={color ? { color } : undefined}>
            <path d={r.d} fill="none" stroke="currentColor" strokeWidth={hl ? 2.5 : 1.5} strokeOpacity={hl ? 1 : 0.35} />
            <circle cx={r.from.x} cy={r.from.y} r={2.2} fill="currentColor" fillOpacity={hl ? 1 : 0.5} />
            <circle cx={r.to.x} cy={r.to.y} r={2.2} fill="currentColor" fillOpacity={hl ? 1 : 0.5} />
          </g>
        )
      })}
      <g data-testid="queue-candidates" className="text-text-muted">
        {cands.map((c) => <path key={c.worker} data-c={c.worker} d={c.d} fill="none" stroke="currentColor" strokeWidth={1.5} strokeOpacity={0.6} strokeDasharray="4 3" />)}
      </g>
    </svg>
  )
}
