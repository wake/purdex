// spa/src/lib/pool/worker-rule.ts — the worker settings as the 設定 page reads them. The owner rule is "" (the host's default) or `hid<N>m`
// (N minutes of keyboard / mouse idle, 1–9999; wwd `ParseOwnerRule`); anything else is shown as the text it is.
import type { PoolWorker } from './types'

const HID = /^hid<([1-9][0-9]{0,3})m$/

/** The minutes of a `hid<N>m` rule, or null for any other text (including ""). */
export function ownerMinutes(rule: string): number | null {
  const m = HID.exec(rule)
  return m ? Number(m[1]) : null
}

export const ownerRuleOf = (minutes: number): string => `hid<${minutes}m`

/** A whole number of minutes wwd accepts, or null. */
export function parseMinutes(text: string): number | null {
  const t = text.trim()
  if (!/^[0-9]{1,4}$/.test(t)) return null
  const n = Number(t)
  return n >= 1 && n <= 9999 ? n : null
}

export const sameWorker = (a: PoolWorker, b: PoolWorker): boolean =>
  a.enabled === b.enabled && a.slots.heavy === b.slots.heavy && a.slots.light === b.slots.light && a.owner_rule === b.owner_rule
