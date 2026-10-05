// spa/src/lib/nex/prelude-wire.ts — the API boundary of the worker prelude
// (spec §4.2–§4.3): a page from GET /v1/executions/{id}/prelude, checked and
// normalised so nothing downstream ever sees a shape the renderer would
// throw on (a string `content`, a colon in a key). Pure.
import type { StreamMessage } from './message-types'

/** Spec §4.3: opaque, and never holds ':' — it is spliced into colon-separated keys. */
export const PRELUDE_POS_RE = /^[A-Za-z0-9._-]{1,64}$/
const MAX_CURSOR_BYTES = 256

export type PreludeItem =
  | { pos: string; at: number; kind: 'assistant' | 'user'; msg: StreamMessage }
  | { pos: string; at: number; kind: 'tool_use' | 'tool_result'; payload: Record<string, unknown> }
  | { pos: string; at: number; kind: 'prelude.segment'; entrypoint: string }
  | { pos: string; at: number; kind: 'prelude.compaction'; trigger: string }
  | { pos: string; at: number; kind: 'prelude.note'; source: string; text: string; truncated: boolean; totalBytes: number | null; stream: string | null }

export interface PreludePage {
  state: 'ok' | 'none' | 'gone'
  /** Oldest → newest within the page. */
  items: PreludeItem[]
  /** The next `before`; null = this page reached the start of the file. */
  prevCursor: string | null
  totalBytes: number | null
}

function rec(v: unknown): Record<string, unknown> | null {
  return typeof v === 'object' && v !== null && !Array.isArray(v) ? (v as Record<string, unknown>) : null
}

const str = (v: unknown): string | undefined => (typeof v === 'string' ? v : undefined)
const nonNegInt = (v: unknown): number | undefined => (Number.isSafeInteger(v) && (v as number) >= 0 ? (v as number) : undefined)

/** `{type, media_type?, bytes?}`; null when `type` is not a string. */
function cleanSource(raw: unknown): Record<string, unknown> | null {
  const s = rec(raw)
  if (!s || typeof s.type !== 'string') return null
  const out: Record<string, unknown> = { type: s.type }
  const mt = str(s.media_type)
  if (mt !== undefined) out.media_type = mt
  const bytes = nonNegInt(s.bytes)
  if (bytes !== undefined) out.bytes = bytes
  return out
}

/** An element of `tool_result.content` (spec §5.2): text keeps `text`, media follows the media rule, the rest keep `{type}`. */
function cleanNested(raw: unknown): Record<string, unknown> | null {
  const e = rec(raw)
  if (!e || typeof e.type !== 'string') return null
  if (e.type === 'text') {
    const t = str(e.text)
    return t !== undefined ? { type: 'text', text: t } : { type: 'text' }
  }
  if (e.type === 'image' || e.type === 'document') {
    const source = cleanSource(e.source)
    return source ? { type: e.type, source } : null
  }
  return { type: e.type }
}

/**
 * One content block, rebuilt from known fields only so no renderer can throw
 * on it (spec §5.2): null drops it. Nothing is spread from the wire; an
 * unknown type keeps `{type}` alone (renderers return null for it).
 */
function cleanBlock(raw: unknown): Record<string, unknown> | null {
  const b = rec(raw)
  if (!b || typeof b.type !== 'string') return null
  const out: Record<string, unknown> = { type: b.type }
  switch (b.type) {
    case 'text': {
      const t = str(b.text)
      if (t !== undefined) out.text = t
      break
    }
    case 'thinking': {
      const t = str(b.thinking)
      if (t !== undefined) out.thinking = t
      break
    }
    case 'tool_use': {
      const id = str(b.id)
      if (id !== undefined) out.id = id
      const name = str(b.name)
      if (name !== undefined) out.name = name
      out.input = rec(b.input) ?? {}
      break
    }
    case 'tool_result': {
      const id = str(b.tool_use_id)
      if (id !== undefined) out.tool_use_id = id
      const c = b.content
      out.content = typeof c === 'string' ? c
        : Array.isArray(c) ? c.map(cleanNested).filter((x): x is Record<string, unknown> => x !== null)
        : ''
      if (typeof b.is_error === 'boolean') out.is_error = b.is_error
      break
    }
    case 'image':
    case 'document': {
      const source = cleanSource(b.source)
      if (!source) return null
      out.source = source
      break
    }
  }
  if (typeof b.truncated === 'boolean') out.truncated = b.truncated
  const tb = nonNegInt(b.total_bytes)
  if (tb !== undefined) out.total_bytes = tb
  return out
}

/** A frame as the room reads it: `content` always an array of clean blocks, always top level (spec §4.3). */
function frame(kind: 'assistant' | 'user', payload: Record<string, unknown>): StreamMessage | null {
  const message = rec(payload.message)
  if (!message) return null
  const raw = message.content
  const content = typeof raw === 'string'
    ? [{ type: 'text', text: raw }]
    : Array.isArray(raw) ? raw.map(cleanBlock).filter((b): b is Record<string, unknown> => b !== null) : null
  if (!content) return null
  // Nexen's N2-derivation inputs, never sent to clients (spec §4.3); untrusted if present.
  const { tool_use_result: _r, tool_result_meta: _m, ...rest } = payload
  void _r; void _m
  return {
    ...rest,
    type: kind,
    parent_tool_use_id: null,
    message: { ...message, role: kind, content, stop_reason: message.stop_reason ?? null },
  } as unknown as StreamMessage
}

function item(raw: unknown): PreludeItem | null {
  const r = rec(raw)
  if (!r || typeof r.pos !== 'string' || !PRELUDE_POS_RE.test(r.pos)) return null
  const p = rec(r.payload)
  if (!p) return null
  const at = Number.isSafeInteger(r.at) && (r.at as number) > 0 ? (r.at as number) : 0
  const pos = r.pos
  const kind = r.kind
  if (kind === 'assistant' || kind === 'user') {
    const msg = frame(kind, p)
    return msg ? { pos, at, kind, msg } : null
  }
  if (kind === 'tool_use' || kind === 'tool_result') {
    // Only tool_use_id is checked here; the N2 readers in tool-activity.ts read every other field defensively.
    return typeof p.tool_use_id === 'string' && p.tool_use_id !== '' ? { pos, at, kind, payload: p } : null
  }
  if (kind === 'prelude.segment') return typeof p.entrypoint === 'string' ? { pos, at, kind, entrypoint: p.entrypoint } : null
  if (kind === 'prelude.compaction') return { pos, at, kind, trigger: typeof p.trigger === 'string' ? p.trigger : '' }
  if (kind === 'prelude.note') {
    if (typeof p.source !== 'string' || typeof p.text !== 'string') return null
    const tb = p.total_bytes
    return {
      pos, at, kind, source: p.source, text: p.text, truncated: p.truncated === true,
      totalBytes: Number.isSafeInteger(tb) && (tb as number) >= 0 ? (tb as number) : null,
      // `bash_output` only (spec §4.3): which stream the text came from.
      stream: typeof p.stream === 'string' ? p.stream : null,
    }
  }
  return null
}

/** null = not a page at all (the caller treats it as an error, never as "no prelude"). */
export function sanitizePreludePage(body: unknown): PreludePage | null {
  const b = rec(body)
  if (!b) return null
  const state = b.state
  if (state !== 'ok' && state !== 'none' && state !== 'gone') return null
  // An absent prev_cursor reads as null; a malformed one rejects the whole body, whatever the state.
  const cursor = b.prev_cursor
  let prevCursor: string | null = null
  if (cursor !== null && cursor !== undefined) {
    if (typeof cursor !== 'string' || cursor === '' || new TextEncoder().encode(cursor).length > MAX_CURSOR_BYTES) return null
    prevCursor = cursor
  }
  if (state !== 'ok') return { state, items: [], prevCursor: null, totalBytes: null }
  // Spec §4.2: an ok page's `items` is an array. Anything else is a malformed
  // body (retryable), never an empty finished page that hides the transcript.
  if (!Array.isArray(b.items)) return null
  const items: PreludeItem[] = []
  const seen = new Set<string>()
  for (const raw of b.items) {
    const it = item(raw)
    if (it && !seen.has(it.pos)) { seen.add(it.pos); items.push(it) }
  }
  const tb = b.total_bytes
  return { state, items, prevCursor, totalBytes: Number.isSafeInteger(tb) && (tb as number) >= 0 ? (tb as number) : null }
}
