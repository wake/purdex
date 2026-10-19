// spa/src/lib/names/persona-hash.ts — canonical JSON and the hash two hosts' persona items are compared by (name pool v2
// spec §4.3). Backfill compares content, not stamps: a stamp moves on every write, the hash only when the items do.
import { sha256Hex } from '../crypto-hash'
import type { Persona } from './persona-types'

/** JSON with object keys sorted, so two values that differ only in key order compare equal. */
export function canonical(v: unknown): string {
  if (Array.isArray(v)) return `[${v.map(canonical).join(',')}]`
  if (typeof v === 'object' && v !== null) {
    const o = v as Record<string, unknown>
    return `{${Object.keys(o).sort().map((k) => `${JSON.stringify(k)}:${canonical(o[k])}`).join(',')}}`
  }
  return JSON.stringify(v)
}

/** SHA-256 of the items by code, so the order a daemon happens to store them in does not matter. */
export async function itemsHash(items: readonly Persona[]): Promise<string> {
  const sorted = [...items].sort((a, b) => (a.code < b.code ? -1 : a.code > b.code ? 1 : 0))
  return sha256Hex(new TextEncoder().encode(canonical(sorted)))
}
