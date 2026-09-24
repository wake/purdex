// spa/src/components/room/ToolDiffView.test.tsx — unified diff with line numbers
// (P-B3 spec §4.4 R5, plan Task 10), now folding by the shared ladder (T3.2).
import type { ReactElement, ReactNode } from 'react'
import { describe, it, expect, beforeEach } from 'vitest'
import { render as rtlRender, screen, cleanup, fireEvent } from '@testing-library/react'
import ToolDiffView from './ToolDiffView'
import { FoldContext, useFoldMemory } from './fold-context'
import { foldPlan } from '../../lib/nex/fold'
import type { DiffHunk, ToolActivity } from '../../lib/nex/tool-activity'
import fixture from '../../lib/nex/__fixtures__/n2-tool-events-06GBBX07.json'

type Diff = NonNullable<ToolActivity['diff']>

beforeEach(() => { cleanup() })

/**
 * The fold memory the pane provides. The view reads its expansion through the
 * context now, so every render needs a provider around it.
 */
function Harness({ children }: { children: ReactNode }) {
  const store = useFoldMemory()
  return <FoldContext.Provider value={store}>{children}</FoldContext.Provider>
}

const render = (ui: ReactElement) => rtlRender(<Harness>{ui}</Harness>)

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

/** A diff that carries a real stat, which `diffOf` deliberately does not. */
const statDiff = (added: number, removed: number, hunks: DiffHunk[], truncated = false): Diff =>
  ({ path: '/srv/app.ts', added, removed, hunks, truncated })

const hunk = (oldStart: number, oldLines: number, newStart: number, newLines: number, lines: string[]): DiffHunk =>
  ({ oldStart, oldLines, newStart, newLines, lines })

/** `n` added rows whose text is `line 1` … `line n` — the same body as an n-line output. */
const addedLines = (n: number): string[] => Array.from({ length: n }, (_, i) => `line ${i + 1}`)
const addedHunk = (n: number): DiffHunk => hunk(1, n, 1, n, addedLines(n).map((l) => `+${l}`))

/** Rows of a hunk: the `data-kind` children after the header. */
const rowsOf = (h: Element) => Array.from(h.querySelectorAll(':scope > [data-kind]'))
/** Every rendered row, across every hunk. */
const allRows = () => Array.from(screen.getByTestId('tool-diff').querySelectorAll('[data-kind]'))
/** [old, new] number-cell texts of a row. */
const numbersOf = (row: Element) => {
  const spans = row.querySelectorAll(':scope > span')
  return [spans[0].textContent, spans[1].textContent]
}
const signOf = (row: Element) => row.querySelectorAll(':scope > span')[2].textContent
const textOf = (row: Element) => row.querySelectorAll(':scope > span')[3].textContent

describe('ToolDiffView', () => {
  it('fixture hunk (seq 923): one hunk, header, four rows ctx / del / add / ctx with numbering', () => {
    render(<ToolDiffView diff={diffOf([fixtureHunk()])} foldKey="d" />)
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

  it('tints add and del rows with theme tokens', () => {
    render(<ToolDiffView diff={diffOf([fixtureHunk()])} foldKey="d" />)
    const [ctx, del, add] = rowsOf(screen.getByTestId('diff-hunk'))
    expect(add.className).toContain('bg-status-success/10')
    expect(add.className).not.toContain('bg-status-error/10')
    expect(del.className).toContain('bg-status-error/10')
    expect(del.className).not.toContain('bg-status-success/10')
    expect(ctx.className).not.toContain('bg-status')
    // The two hard-coded hex tints are gone with the TODOs that flagged them.
    for (const row of [ctx, del, add]) expect(row.className).not.toContain('bg-[#')
  })

  it('number cells are right-aligned tabular-nums and not selectable; sign cell not selectable', () => {
    render(<ToolDiffView diff={diffOf([fixtureHunk()])} foldKey="d" />)
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
    ])} foldKey="d" />)
    const hunks = screen.getAllByTestId('diff-hunk')
    expect(hunks).toHaveLength(2)
    const headers = screen.getAllByTestId('diff-hunk-header')
    expect(headers[0]).toHaveTextContent('@@ -1,2 +1,2 @@')
    expect(headers[1]).toHaveTextContent('@@ -10,3 +11,2 @@')
    expect(rowsOf(hunks[1]).map(numbersOf)).toEqual([['10', '11'], ['11', ''], ['12', '12']])
  })

  it('"\\ No newline at end of file" → meta row: italic muted, blank sign, no numbers', () => {
    render(<ToolDiffView diff={diffOf([hunk(1, 1, 1, 1, ['-a', '\\ No newline at end of file', '+b'])])} foldKey="d" />)
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
    render(<ToolDiffView diff={diffOf([fixtureHunk()], true)} foldKey="d" />)
    const marker = screen.getByTestId('diff-truncated')
    expect(marker).toHaveTextContent('diff truncated by the daemon')
    expect(marker.className).toContain('italic')
    expect(marker.className).toContain('text-text-muted')
  })

  it('truncated: false → no diff-truncated row', () => {
    render(<ToolDiffView diff={diffOf([fixtureHunk()], false)} foldKey="d" />)
    expect(screen.queryByTestId('diff-truncated')).toBeNull()
  })

  it('hunks: [] + truncated: false → renders nothing', () => {
    const { container } = render(<ToolDiffView diff={diffOf([], false)} foldKey="d" />)
    expect(container.firstChild).toBeNull()
  })

  it('codex R2 A1: hunks: [] + truncated: true → tool-diff with only the stat and the diff-truncated row (no diff-hunk)', () => {
    render(<ToolDiffView diff={diffOf([], true)} foldKey="d" />)
    const view = screen.getByTestId('tool-diff')
    const marker = screen.getByTestId('diff-truncated')
    expect(marker).toHaveTextContent('diff truncated by the daemon')
    expect(screen.queryByTestId('diff-hunk')).toBeNull()
    expect(screen.queryByTestId('diff-more')).toBeNull()
    // The daemon dropped every hunk, so the stat is the only thing left that
    // says what the edit did — it is the row that must survive here, not go.
    expect(view.children).toHaveLength(2)
    expect(view.firstElementChild).toBe(screen.getByTestId('diff-stat'))
    expect(view.children[1]).toBe(marker)
  })

  it('long line text wraps in place (whitespace-pre-wrap keeps leading spaces)', () => {
    render(<ToolDiffView diff={diffOf([hunk(1, 1, 1, 1, ['+    indented'])])} foldKey="d" />)
    const row = rowsOf(screen.getByTestId('diff-hunk'))[0]
    expect(textOf(row)).toBe('    indented')
  })
})

// T3.2 — the diff folds by the ladder in `foldPlan`, not by a budget of its
// own: a second set of numbers here would be exactly the third truncation
// mechanism spec §3.2 lists as the defect.
describe('ToolDiffView folding (#1227)', () => {
  it('renders every row of a diff of six rows with no button', () => {
    render(<ToolDiffView diff={diffOf([addedHunk(6)])} foldKey="d" />)
    expect(allRows()).toHaveLength(6)
    expect(screen.queryByTestId('diff-more')).toBeNull()
  })

  it('folds a 30-row diff to six rows', () => {
    render(<ToolDiffView diff={diffOf([addedHunk(30)])} foldKey="d" />)
    expect(allRows()).toHaveLength(6)
    expect(screen.getByTestId('diff-more')).toHaveTextContent('+24 lines')
  })

  it('folds a 100-row diff to three rows', () => {
    render(<ToolDiffView diff={diffOf([addedHunk(100)])} foldKey="d" />)
    expect(allRows()).toHaveLength(3)
    expect(screen.getByTestId('diff-more')).toHaveTextContent('+97 lines')
  })

  it('folds a daemon-truncated diff to three rows whatever its size', () => {
    render(<ToolDiffView diff={diffOf([addedHunk(10)], true)} foldKey="d" />)
    expect(allRows()).toHaveLength(3)
    expect(screen.getByTestId('diff-more')).toHaveTextContent('+7 lines')
  })

  it('expands to the full diff', () => {
    render(<ToolDiffView diff={diffOf([addedHunk(30)])} foldKey="d" />)
    fireEvent.click(screen.getByTestId('diff-more'))
    expect(allRows()).toHaveLength(30)
    expect(screen.queryByTestId('diff-more')).toBeNull()
  })

  it('keeps the daemon-truncation note visible while collapsed', () => {
    render(<ToolDiffView diff={diffOf([addedHunk(100)], true)} foldKey="d" />)
    expect(allRows()).toHaveLength(3)
    expect(screen.getByTestId('diff-truncated')).toBeInTheDocument()
  })

  it('keeps a hunk header with the rows of that hunk', () => {
    // Two hunks of five rows: the 6-row preview shows all of hunk 0 and one
    // row of hunk 1, so both headers are on screen and nothing else is.
    render(<ToolDiffView diff={diffOf([addedHunk(5), addedHunk(5)])} foldKey="d" />)
    const hunks = screen.getAllByTestId('diff-hunk')
    expect(hunks).toHaveLength(2)
    expect(rowsOf(hunks[0])).toHaveLength(5)
    expect(rowsOf(hunks[1])).toHaveLength(1)
    expect(screen.getAllByTestId('diff-hunk-header')).toHaveLength(2)
    // A hunk with no visible row draws no header either.
    cleanup()
    render(<ToolDiffView diff={diffOf([addedHunk(30), addedHunk(5)])} foldKey="d" />)
    expect(screen.getAllByTestId('diff-hunk')).toHaveLength(1)
  })

  it('folds a diff and an output of the same size identically', () => {
    const lines = addedLines(30)
    const outputPlan = foldPlan({ text: lines.join('\n') })
    render(<ToolDiffView diff={diffOf([addedHunk(30)])} foldKey="d" />)
    expect(outputPlan.hiddenLines).toBe(24)
    expect(allRows()).toHaveLength(outputPlan.previewLines.length)
    expect(screen.getByTestId('diff-more')).toHaveTextContent(`+${outputPlan.hiddenLines} lines`)
  })
})

// T3.3b / spec §3.1.1 #3 — the right column used to stack duration, size and
// the diff stat in one place. Size went to the fold affordance and duration to
// the header; `+N −M` belongs to the diff, and it went nowhere when
// `ToolResultBlock`'s facts span was dismantled.
describe('ToolDiffView stat (spec §3.1.1 #3)', () => {
  it('shows the +N −M stat, and not the path', () => {
    render(<ToolDiffView diff={statDiff(5, 0, [addedHunk(3)])} foldKey="d" />)
    const stat = screen.getByTestId('diff-stat')
    expect(stat).toHaveTextContent('+5 −0')
    // The path is the header's `primary_arg`, already drawn once above this
    // block. Repeating it here would be the same stacking spec §3.1.1 #3
    // objects to, one column over.
    expect(stat.textContent).not.toContain('/srv/app.ts')
    // U+2212 MINUS, not the hyphen: it is the width of `+` under tabular-nums.
    expect(stat.textContent).toContain('−0')
    expect(stat.textContent).not.toContain('-0')
    // It sits above the hunks, where the diff can be read with it.
    const hunk0 = screen.getByTestId('diff-hunk')
    expect(stat.compareDocumentPosition(hunk0) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('shows +0 −0 for an edit that changed nothing', () => {
    // Contract rule 7: `+0 −0` is a normal edit result, not an absent stat.
    render(<ToolDiffView diff={statDiff(0, 0, [addedHunk(3)])} foldKey="d" />)
    expect(screen.getByTestId('diff-stat')).toHaveTextContent('+0 −0')
  })

  it('keeps the stat visible while the diff is folded', () => {
    render(<ToolDiffView diff={statDiff(80, 12, [addedHunk(100)])} foldKey="d" />)
    expect(allRows()).toHaveLength(3)
    expect(screen.getByTestId('diff-more')).toBeInTheDocument()
    expect(screen.getByTestId('diff-stat')).toHaveTextContent('+80 −12')
  })
})
