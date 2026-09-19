// spa/src/lib/profile/hash.ts — the canonical form of a profile section
// payload and its content hash (Profile Sync spec §4.4; P2a plan Task 1).
// Two clients holding the same section must produce the same hash, whatever
// order their objects were built in — so the form is JSON with object keys
// sorted at every depth and arrays left in order. No stores, no clocks, no
// fetching: the single environment-dependent call in `lib/profile/` is
// `sha256Hex` (`crypto.subtle`), and it lives here.
//
// A payload that cannot round-trip JSON must not get a hash the other side
// can never reproduce, so everything JSON would silently drop or rewrite
// (NaN → null, a function member → gone, `undefined` in an array → null,
// Date → string) throws instead, naming the path. The one silent case kept is
// an `undefined` object member, which is dropped: optional fields come and go
// across clients, and `{icon: undefined}` has to hash like `{}`.
import { sha256Hex } from '../crypto-hash'

const ROOT = '(root)'
const IDENTIFIER = /^[A-Za-z_$][A-Za-z0-9_$]*$/

function childPath(path: string, key: string): string {
  if (!IDENTIFIER.test(key)) return `${path === ROOT ? '' : path}[${JSON.stringify(key)}]`
  return path === ROOT ? key : `${path}.${key}`
}

function indexPath(path: string, index: number): string {
  return `${path === ROOT ? '' : path}[${index}]`
}

function reject(path: string, what: string): never {
  throw new Error(`structuralKey: ${path} is ${what} — a section payload must be plain JSON data`)
}

function serialize(v: unknown, path: string, ancestors: object[]): string {
  if (v === null) return 'null'
  switch (typeof v) {
    case 'string':
      return JSON.stringify(v)
    case 'boolean':
      return v ? 'true' : 'false'
    case 'number':
      if (!Number.isFinite(v)) reject(path, String(v))
      // JSON.stringify(-0) is "0": -0 and 0 are the same payload.
      return JSON.stringify(v)
    case 'undefined':
      return reject(path, 'undefined')
    case 'function':
      return reject(path, 'a function')
    case 'symbol':
      return reject(path, 'a symbol')
    case 'bigint':
      return reject(path, 'a bigint')
  }

  if (ancestors.includes(v)) reject(path, 'a circular reference')
  const inside = [...ancestors, v]

  if (Array.isArray(v)) {
    // Indexed loop, not `map`: a hole in a sparse array must be seen (and
    // rejected as undefined), and `map` skips holes.
    const items: string[] = []
    for (let i = 0; i < v.length; i++) items.push(serialize(v[i], indexPath(path, i), inside))
    return `[${items.join(',')}]`
  }

  const proto: unknown = Object.getPrototypeOf(v)
  if (proto !== Object.prototype && proto !== null) {
    const ctor = (proto as { constructor?: { name?: unknown } }).constructor
    const name = typeof ctor?.name === 'string' && ctor.name !== '' ? ctor.name : 'a non-plain object'
    reject(path, `not a plain object (${name})`)
  }

  // The string is built here rather than by JSON.stringify of a key-sorted
  // copy: a copy would re-order integer-like keys ("9" before "10" before
  // "a", whatever the insertion order) and would turn an own "__proto__" key
  // into a prototype assignment.
  const src = v as Record<string, unknown>
  const members: string[] = []
  for (const key of Object.keys(src).sort()) {
    const member = src[key]
    if (member === undefined) continue
    members.push(`${JSON.stringify(key)}:${serialize(member, childPath(path, key), inside)}`)
  }
  return `{${members.join(',')}}`
}

/**
 * Canonical JSON of `value`: object keys sorted recursively (by UTF-16 code
 * unit, the default `sort()`), arrays in their own order, `undefined` object
 * members dropped. Never mutates `value`.
 *
 * Throws — with the offending path in the message, e.g. `hosts.h1.port` — on
 * anything that is not JSON data: `undefined` at the top level or inside an
 * array (including a sparse array's hole), `NaN`/`±Infinity`, a function, a
 * symbol, a bigint, or an object whose prototype is neither `Object.prototype`
 * nor `null` (Date, Map, Set, class instances), or a circular reference.
 */
export function structuralKey(value: unknown): string {
  return serialize(value, ROOT, [])
}

/** SHA-256 (64 lowercase hex chars) of `structuralKey(payload)`; rejects where that throws. */
export async function hashSection(payload: unknown): Promise<string> {
  return sha256Hex(new TextEncoder().encode(structuralKey(payload)))
}
