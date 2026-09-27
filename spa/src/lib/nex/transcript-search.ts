// spa/src/lib/nex/transcript-search.ts — the transcript search index (R3 plan
// T3.1, user decision Q4: search covers everything in the transcript, folded
// content included).
//
// A folded block does not render its hidden text at all (FoldedOutput draws
// only the preview; a subagent, a chat tools line, a raw input draw nothing
// while collapsed), so search cannot run over the DOM. It runs over the data
// instead, and every piece of text it can find — a *unit* — says which fold
// keys must be expanded for that text to be on screen (`reveal`, outermost
// first) and which DOM element will then hold it (`id`, carried by the
// component as `data-search-unit`).
//
// The walk mirrors what each view draws, block for block:
// - room: every message through MessageRow's rules;
// - chat: ChatTranscript's rules at the top level (no thinking, a turn's plain
//   operations inside one tools line drawn where the first of them sits, an
//   edit as a line holding only its diff, a failure as a line holding the
//   room's block), and MessageRow's rules again inside a subagent — chat draws
//   a subagent with the room's renderer, thinking included.
// A unit's text is what is drawn: agent prose is `proseText` of its markdown
// (the rendered text), everything else is drawn verbatim.
// Pure: no React, no store.
import { blockKey, toolResultText, type BlockKey, type OperationIndex } from './operations'
import { classifyTurnOperations, toolEntryFor, type TurnOperation } from './operation-status'
import { groupTurns, INTERRUPT_TEXT } from './turns'
import { pathBasename, showsRawInput, toolSummary } from './tool-summary'
import { diffRows } from './diff-lines'
import { proseText } from './markdown-text'
import type { ContentBlock, StreamMessage } from './message-types'
import type { ToolActivity } from './tool-activity'

/**
 * Which text of a block a unit is. `path` is a diff's full path on its stat
 * line; `file` is the file name in chat's edited-line label (two anchors of
 * one block, so two parts).
 */
export type SearchUnitPart = 'text' | 'thinking' | 'arg' | 'input' | 'output' | 'diff' | 'path' | 'file'

/**
 * The DOM anchor of a unit: `data-search-unit={searchUnitId(...)}`. Built from
 * the block's position (the same `blockKey` the fold memory uses), so room and
 * chat give one block the same id — only one of them is mounted at a time.
 * `row` numbers a diff's rows (hunk headers excluded) in drawing order.
 */
export function searchUnitId(key: BlockKey, part: SearchUnitPart, row?: number): string {
  return row === undefined ? `${key}:${part}` : `${key}:${part}:${row}`
}

export interface SearchUnit {
  id: string
  text: string
  /** Fold keys to expand, outermost first, for `text` to be rendered under `[data-search-unit=id]`. */
  reveal: string[]
}

export interface SearchMatch {
  unitId: string
  /** `[start, end)` in the unit's `text`. */
  start: number
  end: number
  reveal: string[]
}

export interface SearchUnitOptions {
  messages: StreamMessage[]
  index: OperationIndex
  tools?: Record<string, ToolActivity>
  view: 'room' | 'chat'
  /** The transcript's keyPrefix: chat's tools line folds at `${keyPrefix}-turn-${ti}:chat-tools`. */
  keyPrefix: string
  /** ExecutionState.turnStarts — chat groups plain operations per turn. */
  turnStarts: readonly number[]
}

/** The fold key of a chat turn's tools line (ChatTranscript). */
export function chatToolsKey(keyPrefix: string, turnIndex: number): string {
  return `${keyPrefix}-turn-${turnIndex}:chat-tools`
}

function blocksOf(message: StreamMessage | undefined): ContentBlock[] {
  const content = (message as { message?: { content?: unknown } } | undefined)?.message?.content
  return Array.isArray(content) ? (content as ContentBlock[]) : []
}

type Push = (id: string, text: string, reveal: string[]) => void

interface Walk {
  messages: StreamMessage[]
  index: OperationIndex
  tools?: Record<string, ToolActivity>
  push: Push
}

/**
 * A diff as ToolDiffView draws it: the path on the stat line when `showPath`
 * (outside the diff's fold — the stat is what you read instead of
 * expanding), then the rows once expanded (`${key}:diff`).
 */
function diffUnits(w: Walk, key: BlockKey, diff: NonNullable<ToolActivity['diff']>, reveal: string[], showPath: boolean) {
  if (showPath) w.push(searchUnitId(key, 'path'), diff.path, reveal)
  let row = 0
  for (const hunk of diff.hunks) {
    for (const r of diffRows(hunk)) w.push(searchUnitId(key, 'diff', row++), r.text, [...reveal, `${key}:diff`])
  }
}

/**
 * The room's block for the operation at (mi, bj) — OperationAt + OperationBlock:
 * header argument, raw input, subagent, diff, output, in that order.
 */
function operationUnits(w: Walk, mi: number, bj: number, reveal: string[]) {
  const msg = w.messages[mi]
  const block = blocksOf(msg)[bj]
  if (!block) return
  const key = blockKey(mi, bj)

  if (msg.type === 'assistant' && block.type === 'tool_use') {
    const entry = toolEntryFor(w.tools, block.id)
    const input = block.input ?? {}
    const summary = toolSummary(block.name ?? '', input, entry)
    if (summary) w.push(searchUnitId(key, 'arg'), summary, reveal)
    if (showsRawInput(input, entry)) {
      w.push(searchUnitId(key, 'input'), JSON.stringify(input, null, 2), [...reveal, `${key}:input`])
    }
    const children = w.index.childrenByParent.get(key)
    if (children) {
      for (const ci of children) messageUnits(w, ci, [...reveal, `${key}:subagent`])
    }
    const diff = entry?.diff
    // OperationBlock: the diff names the file unless the header already did.
    if (diff && (diff.hunks.length > 0 || diff.truncated)) diffUnits(w, key, diff, reveal, summary !== diff.path)
    const result = w.index.resultForCall.get(key)
    if (result) w.push(searchUnitId(key, 'output'), result.text, [...reveal, key])
    return
  }

  if (msg.type === 'user' && block.type === 'tool_result' && !w.index.consumedResults.has(key)) {
    // An orphan: no call, no input — so no argument either (toolSummary of `{}` is '').
    const facts = toolEntryFor(w.tools, block.tool_use_id)
    const diff = facts?.diff
    if (diff && (diff.hunks.length > 0 || diff.truncated)) diffUnits(w, key, diff, reveal, true)
    w.push(searchUnitId(key, 'output'), toolResultText(block.content), [...reveal, key])
  }
}

/** One message by MessageRow's rules (the room, and any subagent rail). */
function messageUnits(w: Walk, mi: number, reveal: string[]) {
  const msg = w.messages[mi]
  if (!msg || !('message' in msg)) return
  blocksOf(msg).forEach((block, bj) => {
    const key = blockKey(mi, bj)
    if (msg.type === 'assistant') {
      if (block.type === 'thinking' && block.thinking?.trim()) {
        w.push(searchUnitId(key, 'thinking'), block.thinking, [...reveal, `${key}:thinking`])
      } else if (block.type === 'text' && block.text) {
        w.push(searchUnitId(key, 'text'), proseText(block.text), reveal)
      } else if (block.type === 'tool_use') {
        operationUnits(w, mi, bj, reveal)
      }
    } else if (msg.type === 'user') {
      if (block.type === 'tool_result') operationUnits(w, mi, bj, reveal)
      // The interrupt sentinel is drawn as a localized label, not its text.
      else if (block.type === 'text' && block.text && block.text !== INTERRUPT_TEXT) {
        w.push(searchUnitId(key, 'text'), block.text, reveal)
      }
    }
  })
}

/** ChatTranscript's top level. */
function chatUnits(w: Walk, keyPrefix: string, turnStarts: readonly number[]) {
  const turns = groupTurns(w.messages, turnStarts)
  turns.forEach((turn, ti) => {
    const ops = classifyTurnOperations(w.messages, turn, w.index, w.tools)
    const plain = ops.filter((o) => o.kind === 'plain')
    const lines = new Map<BlockKey, () => void>()
    if (plain.length > 0) {
      const toolsKey = chatToolsKey(keyPrefix, ti)
      lines.set(plain[0].key, () => {
        for (const o of plain) operationUnits(w, o.msgIndex, o.blockIndex, [toolsKey])
      })
    }
    for (const op of ops) {
      if (op.kind !== 'plain') lines.set(op.key, () => chatOperationLine(w, op))
    }

    for (let mi = turn.start; mi < turn.end; mi++) {
      if (w.index.childIndexes.has(mi)) continue
      const msg = w.messages[mi]
      if (!('message' in msg)) continue
      const fromSubagent = (msg as { parent_tool_use_id?: string | null }).parent_tool_use_id != null
      blocksOf(msg).forEach((block, bj) => {
        const key = blockKey(mi, bj)
        if (msg.type === 'assistant') {
          if (block.type === 'text' && block.text?.trim()) w.push(searchUnitId(key, 'text'), proseText(block.text), [])
          else if (block.type === 'tool_use') lines.get(key)?.()
        } else if (msg.type === 'user') {
          if (block.type === 'tool_result') lines.get(key)?.()
          else if (!fromSubagent && block.type === 'text' && block.text && block.text !== INTERRUPT_TEXT) {
            w.push(searchUnitId(key, 'text'), block.text, [])
          }
        }
      })
    }
  })
}

/**
 * An edited line is its label — whose file name is always on screen — and,
 * expanded, only the diff with its full path; a failed line holds the room's
 * whole block.
 */
function chatOperationLine(w: Walk, op: TurnOperation) {
  const block = blocksOf(w.messages[op.msgIndex])[op.blockIndex]
  if (!block) return
  const facts = toolEntryFor(w.tools, block.type === 'tool_use' ? block.id : block.type === 'tool_result' ? block.tool_use_id : undefined)
  if (op.kind === 'edited' && facts?.diff) {
    const diff = facts.diff
    w.push(searchUnitId(op.key, 'file'), pathBasename(diff.path), [])
    // ToolDiffView draws nothing for a diff with no hunks that dropped none.
    if (diff.hunks.length > 0 || diff.truncated) diffUnits(w, op.key, diff, [`${op.key}:chat-edited`], true)
    return
  }
  operationUnits(w, op.msgIndex, op.blockIndex, [`${op.key}:chat-failed`])
}

/** Every searchable text of the transcript, in the order the view draws it. */
export function buildSearchUnits(opts: SearchUnitOptions): SearchUnit[] {
  const units: SearchUnit[] = []
  const w: Walk = {
    messages: opts.messages,
    index: opts.index,
    tools: opts.tools,
    // NFC at index time (A10): the query is NFC too, so a match's offsets are
    // offsets into this text, whatever form the transcript arrived in.
    push: (id, text, reveal) => { if (text) units.push({ id, text: text.normalize('NFC'), reveal }) },
  }
  if (opts.view === 'chat') {
    chatUnits(w, opts.keyPrefix, opts.turnStarts)
  } else {
    for (let mi = 0; mi < opts.messages.length; mi++) {
      if (!opts.index.childIndexes.has(mi)) messageUnits(w, mi, [])
    }
  }
  return units
}

/** The shortest query, in code points, that searches (A10)… */
export const SEARCH_MIN_CHARS = 2
/** …unless it holds a CJK character: one Han character (`錯`, `檔`) already narrows a transcript. */
export const SEARCH_MIN_CHARS_CJK = 1

// Bopomofo (注音) is Taiwan's; the long-vowel mark ー (U+30FC) and its half
// width ｰ (U+FF70) are Script=Common, so they are named on their own (A F8).
const CJK = /[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}\p{Script=Hangul}\p{Script=Bopomofo}ーｰ]/u

/**
 * The query as it is searched: NFC, trimmed. null when that leaves nothing,
 * or fewer code points than the minimum — 1 when it holds a CJK character,
 * else 2 (a single Latin letter matches nearly every line).
 */
export function normalizeQuery(query: string): string | null {
  const q = query.normalize('NFC').trim()
  if (!q) return null
  const min = CJK.test(q) ? SEARCH_MIN_CHARS_CJK : SEARCH_MIN_CHARS
  return [...q].length < min ? null : q
}

/**
 * The query (normalised by `normalizeQuery`) as a case-insensitive,
 * **literal** pattern: every regex metacharacter is escaped, so `a.b` does
 * not match `axb`. A RegExp rather than `toLowerCase` + `indexOf` because
 * lower-casing can change a string's length (`İ` → `i̇`), which would shift
 * every offset after it. null when the query does not search.
 */
export function searchPattern(query: string): RegExp | null {
  const q = normalizeQuery(query)
  if (q === null) return null
  return new RegExp(q.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'giu')
}

/** The most matches `findMatches` returns by default; past it the UI shows `10000+`. */
export const SEARCH_MATCH_LIMIT = 10_000

export interface SearchResult {
  matches: SearchMatch[]
  /** More occurrences exist than `matches` holds (the limit was reached). */
  truncated: boolean
}

/**
 * Every occurrence of `query` in `units`, in unit order then position;
 * non-overlapping. Stops after `limit` — a two-letter query over a long
 * session can occur hundreds of thousands of times, and nobody steps through
 * those one by one.
 */
export function findMatches(units: readonly SearchUnit[], query: string, limit = SEARCH_MATCH_LIMIT): SearchResult {
  const pattern = searchPattern(query)
  if (!pattern) return { matches: [], truncated: false }
  const matches: SearchMatch[] = []
  for (const unit of units) {
    pattern.lastIndex = 0
    for (const m of unit.text.matchAll(pattern)) {
      if (matches.length === limit) return { matches, truncated: true }
      matches.push({ unitId: unit.id, start: m.index, end: m.index + m[0].length, reveal: unit.reveal })
    }
  }
  return { matches, truncated: false }
}

/**
 * Which match is current, in terms that survive a recompute (A5): its unit,
 * its ordinal among that unit's matches, and the unit's position (to find
 * what follows it once it is gone). A list index would not do — a match
 * landing before it (a chat tools line gaining an operation) shifts it.
 */
export interface MatchIdentity {
  unitId: string
  ordinal: number
  unitPos: number
}

function unitPositions(units: readonly SearchUnit[]): Map<string, number> {
  const pos = new Map<string, number>()
  units.forEach((u, i) => { if (!pos.has(u.id)) pos.set(u.id, i) })
  return pos
}

/** The identity of `matches[i]`; null when there is no such match. */
export function matchIdentity(units: readonly SearchUnit[], matches: readonly SearchMatch[], i: number): MatchIdentity | null {
  const match = matches[i]
  if (!match) return null
  let ordinal = 0
  for (let k = i - 1; k >= 0 && matches[k].unitId === match.unitId; k--) ordinal++
  return { unitId: match.unitId, ordinal, unitPos: unitPositions(units).get(match.unitId) ?? -1 }
}

/**
 * The index of the match `identity` names in a recomputed list: the same
 * match when it still exists, else the nearest one after where it was, else
 * the last. No identity → the first; no matches → -1.
 */
export function findCurrent(units: readonly SearchUnit[], matches: readonly SearchMatch[], identity: MatchIdentity | null): number {
  if (matches.length === 0) return -1
  if (!identity) return 0
  const pos = unitPositions(units)
  // Where its unit is now, when it is still there (units may have landed before it).
  const at = pos.get(identity.unitId) ?? identity.unitPos
  let ordinal = 0
  for (let i = 0; i < matches.length; i++) {
    const m = matches[i]
    ordinal = i > 0 && matches[i - 1].unitId === m.unitId ? ordinal + 1 : 0
    const p = pos.get(m.unitId) ?? -1
    // `>=`: when its unit is gone, `at` is where it was, and the unit that
    // moved up into that place is the one that followed it (R1-4).
    if (m.unitId === identity.unitId ? ordinal >= identity.ordinal : p >= at) return i
  }
  return matches.length - 1
}
