// spa/src/lib/nex/diff-lines.test.ts — diffRows: unified-hunk line numbering
// (P-B3 spec §4.4 R5, plan Task 9).
import { describe, it, expect } from 'vitest'
import { diffRows, type DiffRow } from './diff-lines'
import type { DiffHunk } from './tool-activity'
import fixture from './__fixtures__/n2-tool-events-06GBBX07.json'

/** The Edit tool_result (seq 923) of the fixture, hunk 0, snake_case wire → camelCase. */
function fixtureHunk(): DiffHunk {
  const ev = fixture.items.find((e) => e.seq === 923)
  if (!ev) throw new Error('fixture seq 923 missing')
  const wire = (ev.payload as { diff: { hunks: Array<Record<string, unknown>> } }).diff.hunks[0]
  return {
    oldStart: wire.old_start as number,
    oldLines: wire.old_lines as number,
    newStart: wire.new_start as number,
    newLines: wire.new_lines as number,
    lines: wire.lines as string[],
  }
}

const hunk = (oldStart: number, newStart: number, lines: string[]): DiffHunk =>
  ({ oldStart, oldLines: 0, newStart, newLines: 0, lines })

describe('diffRows', () => {
  it('fixture hunk (seq 923): ctx / del / add / ctx with old and new numbering', () => {
    const expected: DiffRow[] = [
      { kind: 'ctx', old: 1, new: 1, text: 'hello' },
      { kind: 'del', old: 2, new: null, text: 'world' },
      { kind: 'add', old: null, new: 2, text: 'nexen' },
      { kind: 'ctx', old: 3, new: 3, text: 'three' },
    ]
    expect(diffRows(fixtureHunk())).toEqual(expected)
  })

  it('"\\ No newline at end of file" → meta row, text stripped, neither counter advances', () => {
    const rows = diffRows(hunk(1, 1, [' a', '-b', '\\ No newline at end of file', '+c', '\\ No newline at end of file', ' d']))
    expect(rows).toEqual([
      { kind: 'ctx', old: 1, new: 1, text: 'a' },
      { kind: 'del', old: 2, new: null, text: 'b' },
      { kind: 'meta', old: null, new: null, text: 'No newline at end of file' },
      { kind: 'add', old: null, new: 2, text: 'c' },
      { kind: 'meta', old: null, new: null, text: 'No newline at end of file' },
      { kind: 'ctx', old: 3, new: 3, text: 'd' },
    ])
  })

  it('a bare "\\" line is meta with empty text', () => {
    expect(diffRows(hunk(1, 1, ['\\']))).toEqual([{ kind: 'meta', old: null, new: null, text: '' }])
  })

  it('a second hunk starts from its own oldStart / newStart (calls are independent)', () => {
    const first = diffRows(hunk(1, 1, [' a', '+b', ' c']))
    const second = diffRows(hunk(10, 11, [' x', '-y', ' z']))
    expect(first).toEqual([
      { kind: 'ctx', old: 1, new: 1, text: 'a' },
      { kind: 'add', old: null, new: 2, text: 'b' },
      { kind: 'ctx', old: 2, new: 3, text: 'c' },
    ])
    expect(second).toEqual([
      { kind: 'ctx', old: 10, new: 11, text: 'x' },
      { kind: 'del', old: 11, new: null, text: 'y' },
      { kind: 'ctx', old: 12, new: 12, text: 'z' },
    ])
  })

  it('consecutive dels then adds number each side independently', () => {
    expect(diffRows(hunk(5, 7, ['-a', '-b', '+c', '+d', '+e', ' f']))).toEqual([
      { kind: 'del', old: 5, new: null, text: 'a' },
      { kind: 'del', old: 6, new: null, text: 'b' },
      { kind: 'add', old: null, new: 7, text: 'c' },
      { kind: 'add', old: null, new: 8, text: 'd' },
      { kind: 'add', old: null, new: 9, text: 'e' },
      { kind: 'ctx', old: 7, new: 10, text: 'f' },
    ])
  })

  it('unknown first char and empty string are fail-safe ctx (whole line kept, both advance)', () => {
    expect(diffRows(hunk(1, 1, ['?x', '', ' y']))).toEqual([
      { kind: 'ctx', old: 1, new: 1, text: '?x' },
      { kind: 'ctx', old: 2, new: 2, text: '' },
      { kind: 'ctx', old: 3, new: 3, text: 'y' },
    ])
  })

  it('lines: [] → []', () => {
    expect(diffRows(hunk(1, 1, []))).toEqual([])
  })

  it('does not mutate the input hunk or its lines array', () => {
    const lines = [' a', '-b', '+c']
    const input = hunk(1, 1, lines)
    const snapshot = JSON.parse(JSON.stringify(input))
    diffRows(input)
    expect(input).toEqual(snapshot)
    expect(input.lines).toBe(lines)
    expect(lines).toEqual([' a', '-b', '+c'])
  })
})
