// spa/src/components/room/FoldedOutput.test.tsx — the fold affordance.
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { foldPlan } from '../../lib/nex/fold'
import { FoldedOutput } from './FoldedOutput'

const body = (n: number): string =>
  Array.from({ length: n }, (_, i) => `line ${i + 1}`).join('\n')

describe('FoldedOutput', () => {
  it('renders the preview and the count', () => {
    const text = body(20)
    render(<FoldedOutput text={text} plan={foldPlan({ text })} expanded={false} onToggle={() => {}} />)
    const shown = screen.getByTestId('fold-body').textContent ?? ''
    expect(shown).toContain('line 1')
    expect(shown).not.toContain('line 20')
    expect(screen.getByTestId('fold-more')).toHaveTextContent('+14 lines')
  })

  it('renders the body whole when it is not collapsible', () => {
    const text = body(3)
    const plan = foldPlan({ text })
    expect(plan.collapsible).toBe(false)
    render(<FoldedOutput text={text} plan={plan} expanded={false} onToggle={() => {}} />)
    expect(screen.getByTestId('fold-body').textContent).toBe(text)
    expect(screen.queryByTestId('fold-more')).toBeNull()
    expect(screen.queryByTestId('fold-less')).toBeNull()
  })

  it('calls onToggle', () => {
    const text = body(20)
    const plan = foldPlan({ text })
    const onToggle = vi.fn()
    const { rerender } = render(
      <FoldedOutput text={text} plan={plan} expanded={false} onToggle={onToggle} />,
    )
    fireEvent.click(screen.getByTestId('fold-more'))
    expect(onToggle).toHaveBeenCalledTimes(1)

    rerender(<FoldedOutput text={text} plan={plan} expanded onToggle={onToggle} />)
    fireEvent.click(screen.getByTestId('fold-less'))
    expect(onToggle).toHaveBeenCalledTimes(2)
  })

  it('shows the daemon-truncation note only when expanded', () => {
    const text = body(4)
    const plan = foldPlan({ text, truncated: true })
    expect(plan.daemonTruncated).toBe(true)
    const { rerender } = render(
      <FoldedOutput text={text} plan={plan} expanded={false} onToggle={() => {}} />,
    )
    expect(screen.queryByTestId('fold-daemon-truncated')).toBeNull()

    rerender(<FoldedOutput text={text} plan={plan} expanded onToggle={() => {}} />)
    expect(screen.getByTestId('fold-daemon-truncated')).toHaveTextContent(
      'the daemon cut this output at 8 KB',
    )
  })

  it('says "show all" when only a clamp is hiding content', () => {
    // One 5000-character line: there is no second line to reveal, so the count
    // is 0 and "+0 lines" would be a lie about what the button does.
    const text = 'x'.repeat(5000)
    const plan = foldPlan({ text })
    expect(plan.hiddenLines).toBe(0)
    expect(plan.clamped).toBe(true)
    render(<FoldedOutput text={text} plan={plan} expanded={false} onToggle={() => {}} />)
    expect(screen.getByTestId('fold-more')).toHaveTextContent('show all')
    expect(screen.getByTestId('fold-more').textContent).not.toContain('0 lines')
  })

  it('never joins lines with a space', () => {
    // #1265: the preview is a body, not a sentence. Joining its lines with a
    // space turns a three-line file preview into one unreadable run-on.
    const text = body(100)
    const plan = foldPlan({ text })
    expect(plan.previewLines).toHaveLength(3)
    render(<FoldedOutput text={text} plan={plan} expanded={false} onToggle={() => {}} />)
    const shown = screen.getByTestId('fold-body').textContent ?? ''
    expect(shown).toContain('\n')
    expect(shown).toBe('line 1\nline 2\nline 3')
  })
})
