// spa/src/lib/nex/entity-stints.ts — conversation entity spec §10.3: the
// conversation's earlier worker stints and where each one's transcript begins.
import type { ExecutionSummary } from './types'

/** `summary`: the stint's listed row (Task 32: its prior-history hint). */
export interface Stint { id: string; boundary: number; createdAt: number; summary: ExecutionSummary }

/** How many of the newest stints get a boundary fetch. */
export const MAX_STINTS = 50

/** Earlier stints only (never `currentId`), sorted by boundary asc, then createdAt asc. Rows without a boundary are dropped. */
export function orderStints(rows: Array<{ id: string; created_at: number; boundary: number | null; summary: ExecutionSummary }>, currentId: string): Stint[] {
  const out: Stint[] = []
  for (const r of rows) {
    if (r.id === currentId || r.boundary === null) continue
    out.push({ id: r.id, boundary: r.boundary, createdAt: r.created_at, summary: r.summary })
  }
  return out.sort((a, b) => a.boundary - b.boundary || a.createdAt - b.createdAt)
}

/** The newest MAX_STINTS rows other than currentId (created_at desc, then id desc): the ones boundaries are fetched for. */
export function pickRecentStints(rows: readonly ExecutionSummary[], currentId: string): ExecutionSummary[] {
  return rows
    .filter((r) => r.id !== currentId)
    .sort((a, b) => b.created_at - a.created_at || (a.id < b.id ? 1 : a.id > b.id ? -1 : 0))
    .slice(0, MAX_STINTS)
}
