import { describe, it, expect, vi } from 'vitest'
import { render, fireEvent } from '@testing-library/react'
import { RegionResize } from './RegionResize'

describe('RegionResize', () => {
  it('renders a drag handle', () => {
    const { container } = render(<RegionResize onResize={vi.fn()} side="right" />)
    expect(container.firstElementChild).toBeDefined()
  })

  it('calls onResize with delta on mouse drag', () => {
    const onResize = vi.fn()
    const { container } = render(<RegionResize onResize={onResize} side="right" />)
    const handle = container.firstElementChild as HTMLElement

    fireEvent.mouseDown(handle, { clientX: 100 })
    fireEvent.mouseMove(document, { clientX: 150 })
    fireEvent.mouseUp(document)

    expect(onResize).toHaveBeenCalledWith(50)
  })

  it('negates delta for left side', () => {
    const onResize = vi.fn()
    const { container } = render(<RegionResize onResize={onResize} side="left" />)
    const handle = container.firstElementChild as HTMLElement

    fireEvent.mouseDown(handle, { clientX: 200 })
    fireEvent.mouseMove(document, { clientX: 150 })
    fireEvent.mouseUp(document)

    expect(onResize).toHaveBeenCalledWith(50)
  })
})
