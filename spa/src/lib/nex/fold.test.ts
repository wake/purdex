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
    // Ten lines locally would be a medium body (six preview lines). N2 says
    // 900, which is a large one, so the preview is three.
    const plan = foldPlan({ text: lines(10), totalLines: 900 })
    expect(plan.totalLines).toBe(900)
    expect(plan.previewLines).toHaveLength(3)
    expect(plan.collapsible).toBe(true)
  })

  it('hides nothing when it shows the body whole', () => {
    // `hiddenLines` means "how much this affordance is hiding". A body shown
    // whole draws no affordance, so it hides nothing — the field must not
    // return the body's own line count just because the preview is empty.
    const plan = foldPlan({ text: lines(3) })
    expect(plan.collapsible).toBe(false)
    expect(plan.hiddenLines).toBe(0)
  })

  it('does not offer to expand an empty daemon-truncated body', () => {
    // The daemon cut the payload down to nothing. There is no preview, no
    // hidden line and no clamp, so a button would open and close the same
    // emptiness and advertise it as `+0 lines` (attack A1).
    const plan = foldPlan({ text: '', truncated: true })
    expect(plan.daemonTruncated).toBe(true)
    expect(plan.collapsible).toBe(false)
    expect(plan.hiddenLines).toBe(0)
  })

  it('does not offer to expand a truncated body the preview shows whole', () => {
    // Four lines, folded one step less for being an error: the six-line
    // preview already carries every line the body has (attack A1).
    const plan = foldPlan({ text: 'a\nb\nc\nd', truncated: true, severity: 'error' })
    expect(plan.daemonTruncated).toBe(true)
    expect(plan.collapsible).toBe(false)
  })

  it('measures a Han body in UTF-8 bytes', () => {
    // 400 Han characters are ~1200 bytes. `String.length` calls them 400 and
    // shows the whole 1.2 KB body (attack A5), on a machine whose agent
    // output is largely Chinese.
    const plan = foldPlan({ text: '\u4e2d'.repeat(400) })
    expect(plan.collapsible).toBe(true)
    expect(plan.clamped).toBe(true)
  })

  it('measures an emoji body in UTF-8 bytes', () => {
    // 300 astral code points: 600 UTF-16 units, 1200 UTF-8 bytes.
    const plan = foldPlan({ text: '\u{1f600}'.repeat(300) })
    expect(plan.collapsible).toBe(true)
    expect(plan.clamped).toBe(true)
  })

  it('never cuts a surrogate pair', () => {
    // The cut lands exactly where the emoji sits. Slicing by UTF-16 index
    // leaves its high surrogate alone and the browser draws a replacement
    // glyph (attack A5).
    const text = `${'a'.repeat(399)}\u{1f600}${'x'.repeat(10)}`
    const plan = foldPlan({ text, totalBytes: 2000 })
    expect(plan.clamped).toBe(true)
    const preview = plan.previewLines[0]
    const tail = preview.charCodeAt(preview.length - 1)
    expect(tail >= 0xd800 && tail <= 0xdbff).toBe(false)
    expect([...preview].join('')).toBe(preview)
    expect(preview.endsWith('\u{1f600}')).toBe(true)
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
