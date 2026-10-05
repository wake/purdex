// spa/src/lib/nex/prelude.ts — the worker prelude's state and its render view
// (spec §5.2). The state is the pages as fetched (oldest first, growing only
// at the front); the view is derived from it on render and is what both
// transcripts draw. Nothing here reads or writes the execution's own
// messages, seq, turns or tools. Pure.
import type { StreamMessage } from './message-types'
import { isOpeningLine } from './turns'
import type { PreludeItem, PreludePage } from './prelude-wire'
import { recordN2ToolResultIn, recordN2ToolUseIn, type ToolActivity } from './tool-activity'

export interface PreludeState {
  status: 'idle' | 'loading' | 'ok' | 'none' | 'gone' | 'error'
  /** Oldest → newest; only ever grows at the front. */
  items: PreludeItem[]
  /** The next `before`; null before the first page and once `done`. */
  cursor: string | null
  done: boolean
  error: string | null
  totalBytes: number | null
  /**
   * The page request in flight (spec §5.2). An answer lands only while its
   * id is still this one, so a late answer cannot land on an entry that
   * was cleared and recreated in between (Review Focus 3).
   */
  request: number | null
  /**
   * Pages applied so far. The sentinel re-arms on it, not on the item count:
   * a page may legally hold no items and still have a cursor (spec §4.3).
   */
  pages: number
  /** Every cursor handed back so far: a cursor seen twice is a cycle (c1 -> c2 -> c1), not progress. */
  seenCursors: string[]
}

export function defaultPreludeState(): PreludeState {
  return { status: 'idle', items: [], cursor: null, done: false, error: null, totalBytes: null, request: null, pages: 0, seenCursors: [] }
}

// Overwrites `request` unconditionally; the lock (refusing while status is
// 'loading') lives in the caller, useExecutionPrelude.
export function preludeLoading(p: PreludeState, request: number): PreludeState {
  return { ...p, status: 'loading', error: null, request }
}

export function preludeFailed(p: PreludeState, message: string, request: number): PreludeState {
  if (p.request !== request) return p
  return { ...p, status: 'error', error: message, request: null }
}

/**
 * Fold one page in, if `request` is the one in flight. `sentBefore` is the
 * cursor the request carried (null for the first page):
 * - a page that hands back the same cursor, or any cursor already seen,
 *   made no progress (a server bug) and becomes an error the reader can
 *   retry, never a loop (Review Focus 2);
 * - `none` is only ever the first page's answer (spec §4.2); on an older
 *   page it is a contract violation, so an error;
 * - `gone` ends the prelude and keeps what was loaded (the D5 line is drawn
 *   above it).
 */
export function applyPreludePage(p: PreludeState, page: PreludePage, sentBefore: string | null, request: number): PreludeState {
  if (p.request !== request) return p
  if (page.state === 'none' && sentBefore !== null) {
    return { ...p, status: 'error', error: 'prelude: none on an older page', request: null }
  }
  if (page.state !== 'ok') return { ...p, status: page.state, cursor: null, done: true, error: null, request: null }
  if (page.prevCursor !== null && (page.prevCursor === sentBefore || p.seenCursors.includes(page.prevCursor))) {
    return { ...p, status: 'error', error: 'prelude cursor did not advance', request: null }
  }
  const known = new Set(p.items.map((i) => i.pos))
  const fresh = page.items.filter((i) => !known.has(i.pos))
  return {
    status: 'ok',
    items: fresh.length > 0 ? [...fresh, ...p.items] : p.items,
    cursor: page.prevCursor,
    done: page.prevCursor === null,
    error: null,
    totalBytes: page.totalBytes ?? p.totalBytes,
    request: null,
    pages: p.pages + 1,
    seenCursors: page.prevCursor !== null ? [...p.seenCursors, page.prevCursor] : p.seenCursors,
  }
}

export type PreludeEntry =
  | { pos: string; kind: 'message'; m: number }
  | { pos: string; kind: 'segment'; entrypoint: string }
  | { pos: string; kind: 'compaction'; trigger: string }
  | { pos: string; kind: 'note'; source: string; text: string; truncated: boolean; totalBytes: number | null; stream: string | null }

export interface PreludeView {
  /** In drawing order. A message entry points into `messages` by `m`. */
  entries: PreludeEntry[]
  messages: StreamMessage[]
  /** Index-aligned with `messages`: the stable name every key is built from. */
  ids: string[]
  tools: Record<string, ToolActivity>
}

/** The prelude's message names: `p<pos>` never collides with the live list's `${i}`. */
export const preludeId = (pos: string): string => `p${pos}`

export function derivePrelude(items: readonly PreludeItem[]): PreludeView {
  const entries: PreludeEntry[] = []
  const messages: StreamMessage[] = []
  const ids: string[] = []
  let tools: Record<string, ToolActivity> = {}
  for (const it of items) {
    switch (it.kind) {
      case 'assistant':
      case 'user':
        entries.push({ pos: it.pos, kind: 'message', m: messages.length })
        messages.push(it.msg)
        ids.push(preludeId(it.pos))
        break
      case 'tool_use':
        tools = recordN2ToolUseIn(tools, it.payload, it.at)
        break
      case 'tool_result':
        tools = recordN2ToolResultIn(tools, it.payload, it.at)
        break
      case 'prelude.segment':
        entries.push({ pos: it.pos, kind: 'segment', entrypoint: it.entrypoint })
        break
      case 'prelude.compaction':
        entries.push({ pos: it.pos, kind: 'compaction', trigger: it.trigger })
        break
      case 'prelude.note':
        entries.push({ pos: it.pos, kind: 'note', source: it.source, text: it.text, truncated: it.truncated, totalBytes: it.totalBytes, stream: it.stream })
        break
    }
  }
  // Pages load newest first, so everything newer than any loaded call is
  // loaded too: a call still 'running' has no answer anywhere — it was cut
  // off (the session exited mid-call). Never a live clock (spec §5.2).
  // Copy once, only if something is running. defineProperty, not assignment:
  // a tool id may be '__proto__', which assignment would turn into the prototype.
  let copied = false
  for (const id of Object.keys(tools)) {
    const t = tools[id]
    if (t.status !== 'running') continue
    if (!copied) { tools = { ...tools }; copied = true }
    Object.defineProperty(tools, id, { value: { ...t, status: 'aborted' }, enumerable: true, writable: true, configurable: true })
  }
  return { entries, messages, ids, tools }
}

export type PreludeBlock = { kind: 'span'; start: number; end: number } | { kind: 'entry'; entry: PreludeEntry }

/**
 * Chat's grouping of the prelude (spec §5.3): runs of consecutive messages,
 * cut at every line that opens a turn (the human's own line) and closed by
 * any marker or note, so drawing order stays entry order. Search walks the
 * same blocks (transcript-search).
 */
export function preludeBlocks(view: PreludeView): PreludeBlock[] {
  const out: PreludeBlock[] = []
  let span: { start: number; end: number } | null = null
  const close = () => { if (span) out.push({ kind: 'span', ...span }); span = null }
  for (const e of view.entries) {
    if (e.kind !== 'message') { close(); out.push({ kind: 'entry', entry: e }); continue }
    if (span && isOpeningLine(view.messages[e.m])) close()
    span = span ? { start: span.start, end: e.m + 1 } : { start: e.m, end: e.m + 1 }
  }
  close()
  return out
}
