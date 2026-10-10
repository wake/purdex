import { describe, it, expect } from 'vitest'
import { formatClock, outputLineCount, outputTail, readRange, stepChip, systemView, toActivityDiff, userCaption } from './deck-format'
import type { StepOutput } from './types'

const out = (text: string, over: Partial<StepOutput> = {}): StepOutput => ({
  text, total_lines: text === '' ? 0 : text.split('\n').length, total_bytes: text.length, truncated: false, ...over,
})
const lines = (n: number) => Array.from({ length: n }, (_, i) => `l${i + 1}`).join('\n')

describe('userCaption', () => {
  const at = new Date(2026, 9, 10, 9, 5).getTime()
  it('names the source', () => {
    expect(userCaption({ source: 'user', at })).toEqual({ kind: 'you', time: '09:05' })
    expect(userCaption({ source: 'slash', at })).toEqual({ kind: 'you', time: '09:05' })
    expect(userCaption({ source: 'queued', at })).toEqual({ kind: 'queued' })
    expect(userCaption({ source: 'peer', from: { kind: 'peer', name: 'host/x' }, at })).toEqual({ kind: 'from', name: 'host/x' })
    expect(userCaption({ source: 'task', at })).toEqual({ kind: 'background' })
    expect(userCaption({ source: 'scheduled', at })).toEqual({ kind: 'schedule' })
    expect(userCaption({ source: 'schedule', at })).toEqual({ kind: 'schedule' })
  })
  it('reads a source it does not know as the user', () => {
    expect(userCaption({ source: 'from-the-future', at }).kind).toBe('you')
  })
  it('formats the clock with a zero-padded 24-hour time', () => {
    expect(formatClock(new Date(2026, 0, 1, 0, 7).getTime())).toBe('00:07')
    expect(formatClock(new Date(2026, 0, 1, 23, 59).getTime())).toBe('23:59')
  })
})

describe('stepChip', () => {
  it('is empty for a finished step', () => {
    expect(stepChip({ status: 'done' })).toBeNull()
  })
  it('shows a running dot', () => {
    expect(stepChip({ status: 'running' })).toEqual({ kind: 'running' })
  })
  it('splits interrupted from every other denial', () => {
    expect(stepChip({ status: 'denied', denial: 'interrupted' })).toEqual({ kind: 'interrupted' })
    for (const d of ['user-rejected', 'permission-rule', 'cancelled', undefined, 'brand-new']) {
      expect(stepChip({ status: 'denied', denial: d })).toEqual({ kind: 'denied' })
    }
  })
  it('says exit N for a failed command with a code, 失敗 otherwise', () => {
    expect(stepChip({ status: 'failed', command: { text: 'x', exit_code: 2 } })).toEqual({ kind: 'exit', code: 2 })
    expect(stepChip({ status: 'failed', command: { text: 'x' } })).toEqual({ kind: 'failed' })
    expect(stepChip({ status: 'failed' })).toEqual({ kind: 'failed' })
  })
  it('does not call exit 0 a problem', () => {
    expect(stepChip({ status: 'done', command: { text: 'x', exit_code: 0 } })).toBeNull()
  })
})

describe('outputTail', () => {
  it('keeps a short output whole', () => {
    expect(outputTail(out(lines(10)))).toEqual({ text: lines(10), totalLines: 10, cut: false })
  })
  it('opens to the last 10 lines of a longer one', () => {
    const tail = outputTail(out(lines(25)))
    expect(tail.text.split('\n')).toHaveLength(10)
    expect(tail.text.startsWith('l16')).toBe(true)
    expect(tail.text.endsWith('l25')).toBe(true)
    expect(tail).toMatchObject({ totalLines: 25, cut: true })
  })
  it('uses the daemon line count, and says cut when the daemon truncated the payload', () => {
    expect(outputTail(out(lines(3), { total_lines: 900 }))).toMatchObject({ totalLines: 900, cut: true })
    expect(outputTail(out(lines(3), { truncated: true }))).toMatchObject({ cut: true })
  })
  it('ignores one trailing newline', () => {
    expect(outputTail(out('a\nb\n', { total_lines: 2 }))).toMatchObject({ text: 'a\nb', cut: false })
  })
  it('counts the lines of an output', () => {
    expect(outputLineCount(out(lines(4)))).toBe(4)
    expect(outputLineCount(out(''))).toBe(0)
    expect(outputLineCount(out(lines(2), { total_lines: 50 }))).toBe(50)
  })
})

describe('toActivityDiff', () => {
  it('maps hunks to the room camelCase form and makes truncated a boolean', () => {
    const d = toActivityDiff({
      path: '/a', added: 1, removed: 0, exact: true,
      hunks: [{ old_start: 3, old_lines: 1, new_start: 3, new_lines: 2, lines: [' x', '+y'] }],
    })
    expect(d).toEqual({
      path: '/a', added: 1, removed: 0, truncated: false,
      hunks: [{ oldStart: 3, oldLines: 1, newStart: 3, newLines: 2, lines: [' x', '+y'] }],
    })
  })
  it('carries truncation and tolerates no hunks', () => {
    expect(toActivityDiff({ path: '/a', added: 9, removed: 9, exact: false, truncated: true })).toMatchObject({ hunks: [], truncated: true })
  })
})

describe('readRange', () => {
  it('names a bounded read', () => {
    expect(readRange({ read: { offset: 10, limit: 20 } })).toEqual({ from: 10, to: 29 })
    expect(readRange({ read: { limit: 5 } })).toEqual({ from: 1, to: 5 })
  })
  it('says nothing of an open-ended or absent range', () => {
    expect(readRange({ read: { offset: 40 } })).toBeNull()
    expect(readRange({})).toBeNull()
  })
})

describe('systemView', () => {
  const at = new Date(2026, 9, 10, 14, 3).getTime()
  it('gives interrupt and compaction their lines', () => {
    expect(systemView({ kind: 'interrupted', at })).toEqual({ kind: 'interrupted' })
    expect(systemView({ kind: 'compacted', at, detail: { summary: 'long long' } })).toEqual({ kind: 'compacted', time: '14:03' })
  })
  it('keeps a short notice small and strips terminal colour codes', () => {
    expect(systemView({ kind: 'command_output', at, detail: { text: '\u001b[2mCompacted\u001b[22m' } })).toEqual({ kind: 'notice', text: 'Compacted' })
  })
  it('folds a long or multi-line note', () => {
    expect(systemView({ kind: 'notice', at, detail: { text: 'a\nb' } }).kind).toBe('note')
    expect(systemView({ kind: 'notice', at, detail: { text: 'x'.repeat(200) } }).kind).toBe('note')
  })
  it('falls back to the kind when there is no text', () => {
    expect(systemView({ kind: 'handoff', at })).toEqual({ kind: 'notice', text: 'handoff' })
  })
})
