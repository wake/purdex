// spa/src/lib/pool/queue-routes.ts — the lines the task queue tab draws from a queue pool cell to the job card it is running in (monitoring tabs spec M29, M30). Pure geometry over numbers measured by the caller; no DOM.
// A pool in the top row's last line and a worker in the bottom row's first line meet across the gap with one curve. When a row has wrapped, the route never runs through another cell: it drops into the gap under its own
// line, goes to the nearer side gutter, down the gutter, and comes in over the gap above the worker's line (ported from the approved design's `routeD`).

export interface Rect { l: number; r: number; t: number; b: number; w: number }
/** One line of a wrapped row: where its cells start and end vertically. */
export interface Line { t: number; b: number }
/** A job card in the worker pool: the queue pool cell it came from and the worker cell it sits in (both by `host_id`). */
export interface FlowCard { key: string; rect: Rect; pool: string; worker: string }

/** Where everything is, relative to the flow. `top` and `bot` are the two rows; `topLines` and `botLines` are the lines each broke into, top to bottom. */
export interface FlowLayout {
  top: Rect
  bot: Rect
  topLines: Line[]
  botLines: Line[]
  pools: Record<string, Rect>
  workers: Record<string, Rect>
  cards: FlowCard[]
}

export interface Route { key: string; pool: string; worker: string; d: string; from: { x: number; y: number }; to: { x: number; y: number } }
export interface Candidate { worker: string; d: string; from: { x: number; y: number }; to: { x: number; y: number } }

/** Cells whose tops are within 2 px are one line; the line's bottom is the lowest of them. */
export function linesOf(rects: readonly Rect[]): Line[] {
  const out: Line[] = []
  for (const r of rects) {
    const l = out.find((x) => Math.abs(x.t - r.t) < 2)
    if (l) l.b = Math.max(l.b, r.b)
    else out.push({ t: r.t, b: r.b })
  }
  return out.sort((a, b) => a.t - b.t)
}

const P1 = (v: number): string => v.toFixed(1)

/** A polyline with rounded corners. */
export function roundPath(input: ReadonlyArray<readonly [number, number]>, rad = 6): string {
  const pts = input.filter((p, i) => i === 0 || Math.hypot(p[0] - input[i - 1][0], p[1] - input[i - 1][1]) > 0.5)
  let d = `M${P1(pts[0][0])} ${P1(pts[0][1])}`
  for (let i = 1; i < pts.length - 1; i++) {
    const [x0, y0] = pts[i - 1]
    const [x1, y1] = pts[i]
    const [x2, y2] = pts[i + 1]
    const d1 = Math.hypot(x1 - x0, y1 - y0)
    const d2 = Math.hypot(x2 - x1, y2 - y1)
    const r = Math.min(rad, d1 / 2, d2 / 2)
    d += `L${P1(x1 - (x1 - x0) / d1 * r)} ${P1(y1 - (y1 - y0) / d1 * r)}Q${P1(x1)} ${P1(y1)} ${P1(x1 + (x2 - x1) / d2 * r)} ${P1(y1 + (y2 - y1) / d2 * r)}`
  }
  const e = pts[pts.length - 1]
  return `${d}L${P1(e[0])} ${P1(e[1])}`
}

/** What one drawing run counts so that routes sharing a gap or a gutter run side by side. */
interface Tally { lane: number; side: { l: number; r: number } }
const newTally = (): Tally => ({ lane: 0, side: { l: 0, r: 0 } })

/** `p`: the pool's bottom-edge point (`t` its top); `w`: the worker's top-edge point. */
export function routeD(L: FlowLayout, tally: Tally, p: { x: number; t: number; b: number }, w: { x: number; t: number }): string {
  const li = L.topLines.findIndex((l) => Math.abs(l.t - p.t) < 2)
  const wi = L.botLines.findIndex((l) => Math.abs(l.t - w.t) < 2)
  const lastTop = li === L.topLines.length - 1
  const firstBot = wi <= 0
  const lane = tally.lane++
  // routes that share a gap run side by side (a gap between wrapped lines is only 10 px)
  const off = ((lane % 5) - 2) * 3
  const offIn = ((lane % 3) - 1) * 2.5
  const gapY = (L.top.b + L.bot.t) / 2 + off
  if (lastTop && firstBot) {
    const my = (p.b + w.t) / 2
    return `M${P1(p.x)} ${P1(p.b)} C${P1(p.x)} ${P1(my)} ${P1(w.x)} ${P1(my)} ${P1(w.x)} ${P1(w.t)}`
  }
  const left = (p.x + w.x) / 2 < (L.top.l + L.top.r) / 2
  const k = tally.side[left ? 'l' : 'r']++
  const xs = left ? L.top.l - 3 - k * 2.5 : L.top.r + 6 + k * 3
  const yA = lastTop ? gapY : (L.topLines[li].b + L.topLines[li + 1].t) / 2 + offIn
  const yC = firstBot ? gapY : (L.botLines[wi - 1].b + L.botLines[wi].t) / 2 + offIn
  return roundPath([[p.x, p.b], [p.x, yA], [xs, yA], [xs, yC], [w.x, yC], [w.x, w.t]])
}

/**
 * One route per card (a job being prepared or run), from the bottom of the pool it came from to the top of the worker cell it sits in. Those leaving one pool are spread along its bottom edge in the order of their cards
 * (left to right); the ends reaching one worker are spread 34 px apart, starting 18 px in from the card's left edge, in the order `cards` lists them, and never past 6 px short of the card's right edge.
 */
export function routesOf(L: FlowLayout): Route[] {
  const tally = newTally()
  // a card whose pool or worker was not measured has no route and takes no place in the numbering
  const valid = L.cards.filter((c) => L.pools[c.pool] && L.workers[c.worker])
  const intoWorker = new Map<string, number>()
  const slot = new Map<string, number>()
  for (const c of valid) { const n = intoWorker.get(c.worker) ?? 0; slot.set(c.key, n); intoWorker.set(c.worker, n + 1) }
  const byPool = new Map<string, FlowCard[]>()
  for (const c of valid) {
    const list = byPool.get(c.pool)
    if (list) list.push(c)
    else byPool.set(c.pool, [c])
  }
  const out: Route[] = []
  for (const [poolId, list] of byPool) {
    const pr = L.pools[poolId]
    list.sort((a, b) => a.rect.l - b.rect.l).forEach((c, k) => {
      const from = { x: pr.l + pr.w * (k + 1) / (list.length + 1), y: pr.b }
      // the end stays above its own card, however many routes reach the worker
      const to = { x: Math.min(c.rect.l + 18 + (slot.get(c.key) ?? 0) * 34, c.rect.r - 6), y: L.workers[c.worker].t }
      out.push({ key: c.key, pool: c.pool, worker: c.worker, d: routeD(L, tally, { x: from.x, t: pr.t, b: from.y }, { x: to.x, t: to.y }), from, to })
    })
  }
  return out
}

/** The dashed lines to every worker cell a waiting job could go to: from the middle of its pool's bottom edge to the middle of each worker cell's top edge. */
export function candidatesOf(L: FlowLayout, poolId: string, workerIds: readonly string[]): Candidate[] {
  const pr = L.pools[poolId]
  if (!pr) return []
  const tally = newTally()
  const out: Candidate[] = []
  for (const id of workerIds) {
    const wr = L.workers[id]
    if (!wr) continue
    const from = { x: pr.l + pr.w / 2, y: pr.b }
    const to = { x: wr.l + wr.w / 2, y: wr.t }
    out.push({ worker: id, d: routeD(L, tally, { x: from.x, t: pr.t, b: from.y }, { x: to.x, t: to.y }), from, to })
  }
  return out
}
