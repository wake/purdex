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
    expect(btn.className).toContain('border-[#7a6aaa]')
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
