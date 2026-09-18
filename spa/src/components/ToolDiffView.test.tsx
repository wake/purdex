// spa/src/components/ToolDiffView.test.tsx — unified diff with line numbers
// (P-B3 spec §4.4 R5, plan Task 10).
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import ToolDiffView from './ToolDiffView'
import type { DiffHunk, ToolActivity } from '../lib/nex/tool-activity'
import fixture from '../lib/nex/__fixtures__/n2-tool-events-06GBBX07.json'

type Diff = NonNullable<ToolActivity['diff']>

beforeEach(() => { cleanup() })

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

const diffOf = (hunks: DiffHunk[], truncated = false): Diff =>
  ({ path: '/x', added: 0, removed: 0, hunks, truncated })

const hunk = (oldStart: number, oldLines: number, newStart: number, newLines: number, lines: string[]): DiffHunk =>
  ({ oldStart, oldLines, newStart, newLines, lines })

/** Rows of a hunk: the `data-kind` children after the header. */
const rowsOf = (h: Element) => Array.from(h.querySelectorAll(':scope > [data-kind]'))
/** [old, new] number-cell texts of a row. */
const numbersOf = (row: Element) => {
  const spans = row.querySelectorAll(':scope > span')
  return [spans[0].textContent, spans[1].textContent]
}
const signOf = (row: Element) => row.querySelectorAll(':scope > span')[2].textContent
const textOf = (row: Element) => row.querySelectorAll(':scope > span')[3].textContent

describe('ToolDiffView', () => {
  it('fixture hunk (seq 923): one hunk, header, four rows ctx / del / add / ctx with numbering', () => {
    render(<ToolDiffView diff={diffOf([fixtureHunk()])} />)
    const view = screen.getByTestId('tool-diff')
    expect(view.className).toContain('font-mono')
    const hunks = screen.getAllByTestId('diff-hunk')
    expect(hunks).toHaveLength(1)
    expect(screen.getByTestId('diff-hunk-header')).toHaveTextContent('@@ -1,3 +1,3 @@')

    const rows = rowsOf(hunks[0])
    expect(rows.map((r) => r.getAttribute('data-kind'))).toEqual(['ctx', 'del', 'add', 'ctx'])
    expect(rows.map(textOf)).toEqual(['hello', 'world', 'nexen', 'three'])
    expect(rows.map(numbersOf)).toEqual([['1', '1'], ['2', ''], ['', '2'], ['3', '3']])
    expect(rows.map(signOf)).toEqual(['', '-', '+', ''])
  })

  it('row backgrounds: add green-tinted, del red-tinted, ctx neither', () => {
    render(<ToolDiffView diff={diffOf([fixtureHunk()])} />)
    const [ctx, del, add] = rowsOf(screen.getByTestId('diff-hunk'))
    expect(add.className).toContain('bg-[#1f2a1f]')
    expect(add.className).not.toContain('bg-[#2a1f1f]')
    expect(del.className).toContain('bg-[#2a1f1f]')
    expect(del.className).not.toContain('bg-[#1f2a1f]')
    expect(ctx.className).not.toContain('bg-[')
  })

  it('number cells are right-aligned tabular-nums and not selectable; sign cell not selectable', () => {
    render(<ToolDiffView diff={diffOf([fixtureHunk()])} />)
    const row = rowsOf(screen.getByTestId('diff-hunk'))[0]
    const spans = row.querySelectorAll(':scope > span')
    for (const cell of [spans[0], spans[1]]) {
      expect(cell.className).toContain('text-right')
      expect(cell.className).toContain('tabular-nums')
      expect(cell.className).toContain('select-none')
    }
    expect(spans[2].className).toContain('select-none')
    expect(spans[3].className).toContain('whitespace-pre-wrap')
    expect(spans[3].className).toContain('break-all')
  })

  it('two hunks → two diff-hunk blocks, each with its own header', () => {
    render(<ToolDiffView diff={diffOf([
      hunk(1, 2, 1, 2, [' a', '+b', ' c']),
      hunk(10, 3, 11, 2, [' x', '-y', ' z']),
    ])} />)
    const hunks = screen.getAllByTestId('diff-hunk')
    expect(hunks).toHaveLength(2)
    const headers = screen.getAllByTestId('diff-hunk-header')
    expect(headers[0]).toHaveTextContent('@@ -1,2 +1,2 @@')
    expect(headers[1]).toHaveTextContent('@@ -10,3 +11,2 @@')
    expect(rowsOf(hunks[1]).map(numbersOf)).toEqual([['10', '11'], ['11', ''], ['12', '12']])
  })

  it('"\\ No newline at end of file" → meta row: italic muted, blank sign, no numbers', () => {
    render(<ToolDiffView diff={diffOf([hunk(1, 1, 1, 1, ['-a', '\\ No newline at end of file', '+b'])])} />)
    const rows = rowsOf(screen.getByTestId('diff-hunk'))
    expect(rows.map((r) => r.getAttribute('data-kind'))).toEqual(['del', 'meta', 'add'])
    const meta = rows[1]
    expect(meta.className).toContain('italic')
    expect(meta.className).toContain('text-text-muted')
    expect(meta.className).not.toContain('bg-[')
    expect(numbersOf(meta)).toEqual(['', ''])
    expect(signOf(meta)).toBe('')
    expect(textOf(meta)).toBe('No newline at end of file')
  })

  it('truncated: true → trailing diff-truncated row with the en copy', () => {
    render(<ToolDiffView diff={diffOf([fixtureHunk()], true)} />)
    const view = screen.getByTestId('tool-diff')
    const marker = screen.getByTestId('diff-truncated')
    expect(marker).toHaveTextContent('diff truncated by the daemon')
    expect(marker.className).toContain('italic')
    expect(marker.className).toContain('text-text-muted')
    expect(view.lastElementChild).toBe(marker)
  })

  it('truncated: false → no diff-truncated row', () => {
    render(<ToolDiffView diff={diffOf([fixtureHunk()], false)} />)
    expect(screen.queryByTestId('diff-truncated')).toBeNull()
  })

  it('hunks: [] → renders nothing (even when truncated)', () => {
    const { container } = render(<ToolDiffView diff={diffOf([], true)} />)
    expect(container.firstChild).toBeNull()
  })

  it('long line text wraps in place (whitespace-pre-wrap keeps leading spaces)', () => {
    render(<ToolDiffView diff={diffOf([hunk(1, 1, 1, 1, ['+    indented'])])} />)
    const row = rowsOf(screen.getByTestId('diff-hunk'))[0]
    expect(textOf(row)).toBe('    indented')
  })
})
