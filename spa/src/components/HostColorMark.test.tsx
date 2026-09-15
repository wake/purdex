import { describe, it, expect, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { HostColorMark } from './HostColorMark'

afterEach(cleanup)

describe('HostColorMark', () => {
  it('renders nothing when color is null', () => {
    const { container } = render(<HostColorMark color={null} style="left-line" width={2} />)
    expect(container.firstChild).toBeNull()
  })

  it('renders nothing when style is none', () => {
    const { container } = render(<HostColorMark color="#3b82f6" style="none" width={2} />)
    expect(container.firstChild).toBeNull()
  })

  it('left-line: vertical bar with px width and inherited left radius', () => {
    render(<HostColorMark color="#3b82f6" style="left-line" width={3} />)
    const el = screen.getByTestId('host-color-mark')
    expect(el.tagName).toBe('SPAN')
    expect(el.dataset.style).toBe('left-line')
    expect(el.getAttribute('aria-hidden')).toBe('true')
    expect(el.style.position).toBe('absolute')
    expect(el.style.pointerEvents).toBe('none')
    expect(el.style.width).toBe('3px')
    expect(el.style.left).toBe('0px')
    expect(el.style.top).toBe('0px')
    expect(el.style.bottom).toBe('0px')
    expect(el.style.background).toContain('rgb(59, 130, 246)')
    expect(el.style.borderTopLeftRadius).toBe('inherit')
    expect(el.style.borderBottomLeftRadius).toBe('inherit')
  })

  it('bottom-line: horizontal bar with px height and inherited bottom radius', () => {
    render(<HostColorMark color="#3b82f6" style="bottom-line" width={4} />)
    const el = screen.getByTestId('host-color-mark')
    expect(el.dataset.style).toBe('bottom-line')
    expect(el.style.height).toBe('4px')
    expect(el.style.left).toBe('0px')
    expect(el.style.right).toBe('0px')
    expect(el.style.bottom).toBe('0px')
    expect(el.style.background).toContain('rgb(59, 130, 246)')
    expect(el.style.borderBottomLeftRadius).toBe('inherit')
    expect(el.style.borderBottomRightRadius).toBe('inherit')
  })

  it('gradient: 24px wide gradient using 73 hex alpha', () => {
    render(<HostColorMark color="#3b82f6" style="gradient" width={2} />)
    const el = screen.getByTestId('host-color-mark')
    expect(el.dataset.style).toBe('gradient')
    expect(el.style.width).toBe('24px')
    const bg = el.getAttribute('style') ?? ''
    expect(bg).toContain('linear-gradient')
    expect(bg).toMatch(/#3b82f673|rgba\(59, 130, 246, 0\.45/)
    expect(el.style.borderTopLeftRadius).toBe('inherit')
    expect(el.style.borderBottomLeftRadius).toBe('inherit')
  })

  it('uses custom testId', () => {
    render(<HostColorMark color="#3b82f6" style="left-line" width={2} testId="custom-mark" />)
    expect(screen.getByTestId('custom-mark')).toBeTruthy()
  })

  it('applies zIndex when provided', () => {
    render(<HostColorMark color="#3b82f6" style="bottom-line" width={2} zIndex={1} />)
    expect(screen.getByTestId('host-color-mark').style.zIndex).toBe('1')
  })

  it('omits zIndex by default', () => {
    render(<HostColorMark color="#3b82f6" style="bottom-line" width={2} />)
    expect(screen.getByTestId('host-color-mark').style.zIndex).toBe('')
  })
})
