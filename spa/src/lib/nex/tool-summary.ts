// spa/src/lib/nex/tool-summary.ts
// Header summary for a tool call (spec §4.4 R1).
//
// Decision order: the N2 `primary_arg` from the daemon wins; a tool the
// daemon does not know (`known: false`) falls back to the first three input
// keys (R10); everything else — no entry, raw-only entry, or a known tool
// with no primary key (F9, `primaryArg: null`) — uses the pre-N2 client
// table `getSummary`. Truncation is the renderer's job (contract rule 11).
import type { ToolActivity } from './tool-activity'

export function getSummary(tool: string, input: Record<string, unknown>): string {
  switch (tool) {
    case 'Bash':
      return (input.command as string) ?? ''
    case 'Read':
    case 'Write':
    case 'Edit':
      return (input.file_path as string) ?? ''
    case 'WebFetch':
      return (input.url as string) ?? ''
    case 'Grep':
    case 'Glob':
      return (input.pattern as string) ?? ''
    case 'Agent':
      return (input.description as string) ?? ''
    default:
      // No truncation. Spec §4.2 wants the full value in the header — "never
      // truncated to an ellipsis in the middle" — and this slice was worse
      // than the thing that forbids: it cut at 80 without even an ellipsis to
      // admit it. The renderer wraps (`whitespace-pre-wrap break-all`), so
      // length is its problem, not this table's; SUMMARY_LIMIT still bounds
      // the R10 fallback below, which is a different path.
      //
      // An empty input has no argument at all, so say nothing rather than
      // `{}` — that is the serialiser answering, not the call, and it is
      // truthy enough to draw an argument span around it (every orphan
      // result got one). `unknownToolSummary` already returns '' here.
      return Object.keys(input).length === 0 ? '' : JSON.stringify(input)
  }
}

const R10_KEYS = 3

/**
 * Header summary width. The R10 preview never serialises more than this
 * (codex R2 A3). The operation block itself no longer truncates the header
 * (spec §4.2: the argument wraps, it is not cut), so this bounds the preview
 * only.
 */
export const SUMMARY_LIMIT = 80

const PREVIEW_DEPTH = 4
const ELLIPSIS = '…'

/** Thrown internally once the accumulated output passes the limit. */
class PreviewLimit extends Error {}

class PreviewWriter {
  out = ''
  private readonly seen = new WeakSet<object>()
  private readonly limit: number
  constructor(limit: number) {
    this.limit = limit
  }

  /** Appends `s`; keeps only what fits under `limit` and aborts the walk once full. */
  write(s: string): void {
    const room = this.limit - this.out.length
    if (s.length > room) {
      this.out += s.slice(0, room)
      throw new PreviewLimit()
    }
    this.out += s
  }

  value(v: unknown, depth: number): void {
    switch (typeof v) {
      case 'string':
        // A string only ever contributes up to `limit` characters: slice
        // first so a megabyte payload is never escaped in full.
        this.write(JSON.stringify(v.length > this.limit ? v.slice(0, this.limit) : v))
        return
      case 'number':
      case 'boolean':
        this.write(String(v))
        return
      case 'undefined':
        this.write('undefined')
        return
      case 'object':
        if (v === null) { this.write('null'); return }
        if (this.seen.has(v)) { this.write('[Circular]'); return }
        if (depth >= PREVIEW_DEPTH) { this.write(ELLIPSIS); return }
        this.seen.add(v)
        if (Array.isArray(v)) {
          this.write('[')
          for (let i = 0; i < v.length; i++) {
            if (i > 0) this.write(',')
            this.value(v[i], depth + 1)
          }
          this.write(']')
        } else {
          this.write('{')
          // for…in instead of Object.keys: the latter materialises every
          // key up front (O(n) before the first write), while for…in lets
          // the budget stop the walk after a handful of properties. Own
          // keys only, so a prototype-chain entry never leaks in.
          let first = true
          for (const k in v) {
            if (!Object.hasOwn(v, k)) continue
            if (!first) this.write(',')
            first = false
            this.write(JSON.stringify(k) + ':')
            this.value((v as Record<string, unknown>)[k], depth + 1)
          }
          this.write('}')
        }
        this.seen.delete(v)
        return
      default:
        // function / symbol / bigint: JSON.stringify would drop or throw;
        // spell the type so the header still says something.
        this.write(`[${typeof v}]`)
    }
  }
}

/**
 * Bounded, JSON-flavoured preview of one input value for the R10 fallback.
 * Strings come back verbatim (the renderer truncates); scalars via
 * `String`; objects / arrays are walked by hand and the walk stops the
 * moment the output passes `limit` (returning what was written plus `…`),
 * so a 100 000-element array costs O(limit), never O(n). Nesting deeper
 * than four levels prints `…`, cycles print `[Circular]`, and a value whose
 * getter throws yields `[unserializable]`.
 */
export function previewValue(v: unknown, limit: number): string {
  switch (typeof v) {
    case 'string':
      return v
    case 'number':
    case 'boolean':
      return String(v)
    case 'undefined':
      return 'undefined'
    default:
      if (v === null) return 'null'
  }
  const w = new PreviewWriter(limit)
  try {
    w.value(v, 0)
    return w.out
  } catch (e) {
    if (e instanceof PreviewLimit) return w.out + ELLIPSIS
    return '[unserializable]'
  }
}

/** R10: `key: value` for the first three own keys of `input`, `''` when empty. */
function unknownToolSummary(input: Record<string, unknown>): string {
  return Object.keys(input)
    .slice(0, R10_KEYS)
    .map((k) => `${k}: ${previewValue(input[k], SUMMARY_LIMIT)}`)
    .join(', ')
}

export function toolSummary(
  tool: string,
  input: Record<string, unknown>,
  entry?: Pick<ToolActivity, 'primaryArg' | 'known'>,
): string {
  const primary = entry?.primaryArg
  if (primary !== null && primary !== undefined) return primary.value
  if (entry?.known === false) return unknownToolSummary(input)
  return getSummary(tool, input)
}
