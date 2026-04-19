import type { SnapshotMetadata, CompactionResult } from './snapshot-types'

const HOUR_MS = 60 * 60 * 1000
const DAY_MS = 24 * HOUR_MS

const PRE_OP_MAX = 5
const MONTHLY_MAX = 12

function isPreOp(m: SnapshotMetadata): boolean {
  return m.trigger === 'pre-import' || m.trigger === 'pre-restore'
}

function pad(n: number, w = 2): string {
  return String(n).padStart(w, '0')
}

/** YYYY-MM-DDTHHZ */
function hourKeyUTC(ts: number): string {
  const d = new Date(ts)
  return `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())}T${pad(d.getUTCHours())}Z`
}

/** YYYY-MM-DDZ */
function dayKeyUTC(ts: number): string {
  const d = new Date(ts)
  return `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())}Z`
}

/** ISO week YYYY-Www */
function isoWeekKey(ts: number): string {
  const d = new Date(ts)
  const target = new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate()))
  const dayNum = (target.getUTCDay() + 6) % 7
  target.setUTCDate(target.getUTCDate() - dayNum + 3)
  const firstThursday = new Date(Date.UTC(target.getUTCFullYear(), 0, 4))
  const diff = target.getTime() - firstThursday.getTime()
  const week = 1 + Math.round(diff / (7 * DAY_MS))
  return `${target.getUTCFullYear()}-W${pad(week)}`
}

/** YYYY-MM */
function monthKey(ts: number): string {
  const d = new Date(ts)
  return `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}`
}

function classifyTier(ageMs: number):
  | { tier: 'hourly'; key: (ts: number) => string }
  | { tier: 'daily'; key: (ts: number) => string }
  | { tier: 'weekly'; key: (ts: number) => string }
  | { tier: 'monthly'; key: (ts: number) => string } {
  if (ageMs < 24 * HOUR_MS) return { tier: 'hourly', key: hourKeyUTC }
  if (ageMs < 30 * DAY_MS) return { tier: 'daily', key: dayKeyUTC }
  if (ageMs < 90 * DAY_MS) return { tier: 'weekly', key: isoWeekKey }
  return { tier: 'monthly', key: monthKey }
}

export function computeCompaction(all: SnapshotMetadata[], now: number): CompactionResult {
  const kept: string[] = []
  const evicted: string[] = []

  const preOp: SnapshotMetadata[] = []
  const timeTier: SnapshotMetadata[] = []

  for (const m of all) {
    if (isPreOp(m)) preOp.push(m)
    else timeTier.push(m)
  }

  // --- pre-op pool: pristine 先 bypass，其餘按時間 desc 留 max 5 ---
  const pristine = preOp.filter((m) => m.isSessionPristine)
  const regularPreOp = preOp
    .filter((m) => !m.isSessionPristine)
    .sort((a, b) => b.timestamp - a.timestamp)

  for (const m of pristine) kept.push(m.id)
  for (let i = 0; i < regularPreOp.length; i++) {
    if (i < PRE_OP_MAX) kept.push(regularPreOp[i].id)
    else evicted.push(regularPreOp[i].id)
  }

  // --- time-tier: 分桶、每桶留 newest ---
  const buckets = new Map<string, { tier: string; items: SnapshotMetadata[] }>()
  for (const m of timeTier) {
    const age = now - m.timestamp
    const cls = classifyTier(age)
    const bucketKey = `${cls.tier}:${cls.key(m.timestamp)}`
    const entry = buckets.get(bucketKey) ?? { tier: cls.tier, items: [] }
    entry.items.push(m)
    buckets.set(bucketKey, entry)
  }

  // Each non-monthly bucket keeps its newest entry; older duplicates go.
  // Monthly buckets are collected separately so we can cap them at
  // MONTHLY_MAX latest buckets — otherwise a long-lived profile would
  // accumulate one snapshot per historical month forever.
  const monthlyRepresentatives: SnapshotMetadata[] = []
  for (const { tier, items } of buckets.values()) {
    items.sort((a, b) => b.timestamp - a.timestamp)
    const [newest, ...older] = items
    if (tier === 'monthly') {
      monthlyRepresentatives.push(newest)
    } else {
      kept.push(newest.id)
    }
    for (const m of older) evicted.push(m.id)
  }

  monthlyRepresentatives.sort((a, b) => b.timestamp - a.timestamp)
  for (let i = 0; i < monthlyRepresentatives.length; i++) {
    if (i < MONTHLY_MAX) kept.push(monthlyRepresentatives[i].id)
    else evicted.push(monthlyRepresentatives[i].id)
  }

  return { kept, evicted }
}
