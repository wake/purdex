// spa/src/lib/nex/fold.test.ts — spec §4.2's one folding rule.
import { describe, it, expect } from 'vitest'
import { FOLD_LINE_MAX_CHARS, FOLD_WHOLE_MAX_BYTES, firstLine, foldPlan } from './fold'

const lines = (n: number, prefix = 'line'): string =>
  Array.from({ length: n }, (_, i) => `${prefix} ${i + 1}`).join('\n')

describe('foldPlan', () => {
  it('shows a short body whole', () => {
    const plan = foldPlan({ text: lines(3) })
    expect(plan.collapsible).toBe(false)
    expect(plan.previewLines).toEqual([])
    expect(plan.totalLines).toBe(3)
  })

  it('folds a medium body to six lines', () => {
    const plan = foldPlan({ text: lines(20) })
    expect(plan.collapsible).toBe(true)
    expect(plan.previewLines).toHaveLength(6)
    expect(plan.previewLines[0]).toBe('line 1')
    expect(plan.hiddenLines).toBe(14)
  })

  it('folds a large body to three lines', () => {
    const plan = foldPlan({ text: lines(100) })
    expect(plan.previewLines).toHaveLength(3)
    expect(plan.hiddenLines).toBe(97)
    expect(plan.collapsible).toBe(true)
  })

  it('treats a daemon-truncated body as large', () => {
    const plan = foldPlan({ text: lines(4), truncated: true })
    expect(plan.previewLines).toHaveLength(3)
    expect(plan.daemonTruncated).toBe(true)
    expect(plan.collapsible).toBe(true)
  })

  it('an error folds one step less', () => {
    const plan = foldPlan({ text: lines(100), severity: 'error' })
    expect(plan.previewLines).toHaveLength(6)
    expect(plan.hiddenLines).toBe(94)
  })

  it('an error under the medium cap is shown whole', () => {
    const plan = foldPlan({ text: lines(20), severity: 'error' })
    expect(plan.collapsible).toBe(false)
    expect(plan.previewLines).toEqual([])
  })

  it('a single huge line is clamped', () => {
    const plan = foldPlan({ text: 'x'.repeat(5000) })
    expect(plan.clamped).toBe(true)
    expect(plan.collapsible).toBe(true)
    expect(plan.previewLines).toHaveLength(1)
    expect(plan.previewLines[0]).toHaveLength(FOLD_LINE_MAX_CHARS)
    expect(plan.hiddenLines).toBe(0)
  })

  it('folds a body that is over a kilobyte in six lines', () => {
    // Three 350-character lines: under every line count in the table, over
    // the 1 KB bar (codex plan review #10).
    const plan = foldPlan({ text: [1, 2, 3].map((n) => String(n).repeat(350)).join('\n') })
    expect(plan.collapsible).toBe(true)
    expect(plan.clamped).toBe(true)
    expect(plan.previewLines.join('').length).toBeLessThanOrEqual(FOLD_WHOLE_MAX_BYTES)
  })

  it('counts only the lines it can reveal', () => {
    // N2 says 900 lines, the body carries 3: the affordance may only promise
    // what expanding can show (codex plan review #11).
    const plan = foldPlan({ text: lines(3), totalLines: 900 })
    expect(plan.hiddenLines).toBe(0)
    expect(plan.daemonTruncated).toBe(true)
  })

  it("uses N2's count to pick the fold level", () => {
    const plan = foldPlan({ text: lines(3), totalLines: 900 })
    expect(plan.collapsible).toBe(true)
    expect(plan.totalLines).toBe(900)
  })

  it('hides nothing when it shows the body whole', () => {
    // `hiddenLines` means "how much this affordance is hiding". A body shown
    // whole draws no affordance, so it hides nothing — the field must not
    // return the body's own line count just because the preview is empty.
    const plan = foldPlan({ text: lines(3) })
    expect(plan.collapsible).toBe(false)
    expect(plan.hiddenLines).toBe(0)
  })

  it('handles an empty body', () => {
    const plan = foldPlan({ text: '' })
    expect(plan.totalLines).toBe(0)
    expect(plan.collapsible).toBe(false)
  })

  it('does not count a trailing newline as a line', () => {
    expect(foldPlan({ text: 'a\n' }).totalLines).toBe(1)
  })
})

describe('firstLine', () => {
  it('firstLine takes the first line, not the joined body', () => {
    expect(firstLine('1 # Pane\n2\n3 - x')).toBe('1 # Pane')
  })

  it('firstLine clamps a long first line', () => {
    expect(firstLine(`${'y'.repeat(900)}\nnext`)).toHaveLength(FOLD_LINE_MAX_CHARS)
  })
})
