import { describe, it, expect, vi } from 'vitest'
import { render, fireEvent } from '@testing-library/react'
import { PaneSplitter } from './PaneSplitter'

describe('PaneSplitter', () => {
  it('renders a horizontal drag handle', () => {
    const { container } = render(<PaneSplitter direction="h" onResize={vi.fn()} />)
    expect(container.firstElementChild?.className).toContain('cursor-col-resize')
  })

  it('renders a vertical drag handle', () => {
    const { container } = render(<PaneSplitter direction="v" onResize={vi.fn()} />)
    expect(container.firstElementChild?.className).toContain('cursor-row-resize')
  })

  it('calls onResize with pixel delta during horizontal drag', () => {
    const onResize = vi.fn()
    const { container } = render(<PaneSplitter direction="h" onResize={onResize} />)
    const handle = container.firstElementChild as HTMLElement
    fireEvent.mouseDown(handle, { clientX: 100, clientY: 100 })
    fireEvent.mouseMove(document, { clientX: 150, clientY: 100 })
    fireEvent.mouseUp(document)
    expect(onResize).toHaveBeenCalledWith(50)
  })

  it('uses clientY delta for vertical direction', () => {
    const onResize = vi.fn()
    const { container } = render(<PaneSplitter direction="v" onResize={onResize} />)
    const handle = container.firstElementChild as HTMLElement
    fireEvent.mouseDown(handle, { clientX: 100, clientY: 200 })
    fireEvent.mouseMove(document, { clientX: 100, clientY: 250 })
    fireEvent.mouseUp(document)
    expect(onResize).toHaveBeenCalledWith(50)
  })
})
