import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { SegmentControl } from './SegmentControl'

const OPTIONS = [
  { value: 'a', label: 'Alpha' },
  { value: 'b', label: 'Beta' },
  { value: 'c', label: 'Gamma' },
]

describe('SegmentControl', () => {
  it('renders all options', () => {
    render(<SegmentControl options={OPTIONS} value="a" onChange={vi.fn()} />)
    expect(screen.getByText('Alpha')).toBeTruthy()
    expect(screen.getByText('Beta')).toBeTruthy()
    expect(screen.getByText('Gamma')).toBeTruthy()
  })

  it('highlights active option', () => {
    render(<SegmentControl options={OPTIONS} value="b" onChange={vi.fn()} />)
    const btn = screen.getByText('Beta')
    expect(btn.className).toContain('border-border-active')
  })

  it('exposes the active option with aria-pressed', () => {
    render(<SegmentControl options={OPTIONS} value="b" onChange={vi.fn()} />)
    expect(screen.getByText('Beta')).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByText('Alpha')).toHaveAttribute('aria-pressed', 'false')
  })

  it('calls onChange with selected value', () => {
    const onChange = vi.fn()
    render(<SegmentControl options={OPTIONS} value="a" onChange={onChange} />)
    fireEvent.click(screen.getByText('Gamma'))
    expect(onChange).toHaveBeenCalledWith('c')
  })

  it('does not call onChange when clicking active option', () => {
    const onChange = vi.fn()
    render(<SegmentControl options={OPTIONS} value="a" onChange={onChange} />)
    fireEvent.click(screen.getByText('Alpha'))
    expect(onChange).not.toHaveBeenCalled()
  })

  it('applies rounded corners to first and last buttons', () => {
    render(<SegmentControl options={OPTIONS} value="a" onChange={vi.fn()} />)
    expect(screen.getByText('Alpha').className).toContain('rounded-l-md')
    expect(screen.getByText('Gamma').className).toContain('rounded-r-md')
    expect(screen.getByText('Beta').className).not.toContain('rounded-l-md')
    expect(screen.getByText('Beta').className).not.toContain('rounded-r-md')
  })
})

describe('SegmentControl disabled option', () => {
  it('a disabled option cannot be picked', () => {
    const onChange = vi.fn()
    render(<SegmentControl options={[{ value: 'a', label: 'A' }, { value: 'b', label: 'B', disabled: true }]} value="a" onChange={onChange} />)
    const b = screen.getByRole('button', { name: 'B' })
    expect(b).toBeDisabled()
    fireEvent.click(b)
    expect(onChange).not.toHaveBeenCalled()
  })
  it('a disabled option has no hover lighting', () => {
    render(<SegmentControl options={[{ value: 'a', label: 'A' }, { value: 'b', label: 'B', disabled: true }]} value="a" onChange={() => {}} />)
    const cls = screen.getByRole('button', { name: 'B' }).className
    expect(cls).not.toMatch(/(^|\s)hover:/)
    expect(cls).toContain('enabled:hover:')
  })
  it('drops the default focus outline; inactive buttons light up on focus-visible like hover', () => {
    render(<SegmentControl options={[{ value: 'a', label: 'A' }, { value: 'b', label: 'B' }]} value="a" onChange={() => {}} />)
    for (const name of ['A', 'B']) expect(screen.getByRole('button', { name }).className).toContain('outline-none')
    expect(screen.getByRole('button', { name: 'B' }).className).toContain('enabled:focus-visible:border-text-muted')
  })
})
