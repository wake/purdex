import { describe, it, expect, vi } from 'vitest'
import { render, fireEvent } from '@testing-library/react'
import { RegionResize } from './RegionResize'

describe('RegionResize', () => {
  it('renders a drag handle', () => {
    const { container } = render(<RegionResize onResize={vi.fn()} resizeEdge="right" />)
    expect(container.firstElementChild).toBeDefined()
  })

  it('calls onResize with delta on mouse drag', () => {
    const onResize = vi.fn()
    const { container } = render(<RegionResize onResize={onResize} resizeEdge="right" />)
    const hit = container.querySelector('[data-testid="resize-hit"]') as HTMLElement

    fireEvent.mouseDown(hit, { clientX: 100 })
    fireEvent.mouseMove(document, { clientX: 150 })
    fireEvent.mouseUp(document)

    expect(onResize).toHaveBeenCalledWith(50)
  })

  it('negates delta for left resizeEdge', () => {
    const onResize = vi.fn()
    const { container } = render(<RegionResize onResize={onResize} resizeEdge="left" />)
    const hit = container.querySelector('[data-testid="resize-hit"]') as HTMLElement

    fireEvent.mouseDown(hit, { clientX: 200 })
    fireEvent.mouseMove(document, { clientX: 150 })
    fireEvent.mouseUp(document)

    expect(onResize).toHaveBeenCalledWith(50)
  })

  it('uses latest onResize callback during drag (no stale closure)', () => {
    const onResize1 = vi.fn()
    const onResize2 = vi.fn()

    const { container, rerender } = render(<RegionResize onResize={onResize1} resizeEdge="right" />)
    const hit = container.querySelector('[data-testid="resize-hit"]') as HTMLElement

    // Start drag
    fireEvent.mouseDown(hit, { clientX: 100 })

    // First move uses onResize1
    fireEvent.mouseMove(document, { clientX: 110 })
    expect(onResize1).toHaveBeenCalledTimes(1)

    // Re-render with new onResize (simulates parent re-render after width change)
    rerender(<RegionResize onResize={onResize2} resizeEdge="right" />)

    // Second move should use onResize2, not the stale onResize1
    fireEvent.mouseMove(document, { clientX: 120 })
    expect(onResize2).toHaveBeenCalledTimes(1)
    expect(onResize1).toHaveBeenCalledTimes(1) // should NOT have been called again

    fireEvent.mouseUp(document)
  })

  it('renders a wide invisible hit area over a thin visual seam', () => {
    const { container } = render(<RegionResize onResize={vi.fn()} resizeEdge="right" />)
    const root = container.firstChild as HTMLElement
    expect(root.className).toMatch(/relative/)
    const hit = root.querySelector('[data-testid="resize-hit"]') as HTMLElement
    expect(hit).toBeInTheDocument()
    expect(hit.className).toMatch(/cursor-col-resize/)
  })
})
