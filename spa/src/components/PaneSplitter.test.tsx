import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, fireEvent } from '@testing-library/react'
import { PaneSplitter } from './PaneSplitter'

describe('PaneSplitter', () => {
  afterEach(() => {
    document.body.style.cursor = ''
    document.body.style.userSelect = ''
  })

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

  it('calls onResizeEnd once on mouseup, not on mousemove', () => {
    const onResize = vi.fn()
    const onResizeEnd = vi.fn()
    const { container } = render(<PaneSplitter direction="v" onResize={onResize} onResizeEnd={onResizeEnd} />)
    const handle = container.firstElementChild as HTMLElement
    fireEvent.mouseDown(handle, { clientX: 100, clientY: 200 })
    fireEvent.mouseMove(document, { clientX: 100, clientY: 230 })
    fireEvent.mouseMove(document, { clientX: 100, clientY: 260 })
    expect(onResize).toHaveBeenCalledTimes(2)
    expect(onResizeEnd).not.toHaveBeenCalled()
    fireEvent.mouseUp(document)
    expect(onResizeEnd).toHaveBeenCalledTimes(1)
    // Listeners are gone after mouseup: a stray mouseup does not fire it again.
    fireEvent.mouseUp(document)
    expect(onResizeEnd).toHaveBeenCalledTimes(1)
  })

  it('sets the body cursor and userSelect for the drag and restores them on mouseup', () => {
    const { container } = render(<PaneSplitter direction="v" onResize={vi.fn()} />)
    fireEvent.mouseDown(container.firstElementChild as HTMLElement, { clientY: 200 })
    expect(document.body.style.cursor).toBe('row-resize')
    expect(document.body.style.userSelect).toBe('none')
    fireEvent.mouseUp(document)
    expect(document.body.style.cursor).toBe('')
    expect(document.body.style.userSelect).toBe('')
  })

  it('a window blur after mouseup does nothing: the blur listener goes with the drag', () => {
    const onResizeEnd = vi.fn()
    const { container } = render(<PaneSplitter direction="v" onResize={vi.fn()} onResizeEnd={onResizeEnd} />)
    fireEvent.mouseDown(container.firstElementChild as HTMLElement, { clientY: 200 })
    fireEvent.mouseUp(document)
    fireEvent.blur(window)
    expect(onResizeEnd).toHaveBeenCalledTimes(1)
  })

  it('unmount mid-drag removes the listeners and restores the body styles without calling onResizeEnd', () => {
    const onResize = vi.fn()
    const onResizeEnd = vi.fn()
    const { container, unmount } = render(
      <PaneSplitter direction="v" onResize={onResize} onResizeEnd={onResizeEnd} />,
    )
    fireEvent.mouseDown(container.firstElementChild as HTMLElement, { clientY: 200 })
    fireEvent.mouseMove(document, { clientY: 230 })
    expect(onResize).toHaveBeenCalledTimes(1)

    unmount()
    expect(document.body.style.cursor).toBe('')
    expect(document.body.style.userSelect).toBe('')
    fireEvent.mouseMove(document, { clientY: 260 })
    fireEvent.mouseUp(document)
    fireEvent.blur(window)
    expect(onResize).toHaveBeenCalledTimes(1)
    expect(onResizeEnd).not.toHaveBeenCalled()
  })

  // The draft is already on screen when the window loses focus; committing it keeps the screen and the store in step.
  it('a window blur mid-drag ends the drag like a mouseup: onResizeEnd once, listeners gone, styles restored', () => {
    const onResize = vi.fn()
    const onResizeEnd = vi.fn()
    const { container } = render(<PaneSplitter direction="v" onResize={onResize} onResizeEnd={onResizeEnd} />)
    fireEvent.mouseDown(container.firstElementChild as HTMLElement, { clientY: 200 })
    fireEvent.mouseMove(document, { clientY: 230 })

    fireEvent.blur(window)
    expect(onResizeEnd).toHaveBeenCalledTimes(1)
    expect(document.body.style.cursor).toBe('')
    expect(document.body.style.userSelect).toBe('')

    fireEvent.mouseMove(document, { clientY: 260 })
    fireEvent.mouseUp(document)
    fireEvent.blur(window)
    expect(onResize).toHaveBeenCalledTimes(1)
    expect(onResizeEnd).toHaveBeenCalledTimes(1)
  })

  it('a second drag after a blur works from its own start position', () => {
    const onResize = vi.fn()
    const onResizeEnd = vi.fn()
    const { container } = render(<PaneSplitter direction="h" onResize={onResize} onResizeEnd={onResizeEnd} />)
    const handle = container.firstElementChild as HTMLElement
    fireEvent.mouseDown(handle, { clientX: 100 })
    fireEvent.mouseMove(document, { clientX: 120 })
    fireEvent.blur(window)

    fireEvent.mouseDown(handle, { clientX: 300 })
    expect(document.body.style.cursor).toBe('col-resize')
    fireEvent.mouseMove(document, { clientX: 310 })
    expect(onResize).toHaveBeenLastCalledWith(10)
    fireEvent.mouseUp(document)
    expect(onResize).toHaveBeenCalledTimes(2)
    expect(onResizeEnd).toHaveBeenCalledTimes(2)
    expect(document.body.style.cursor).toBe('')
  })

  it('a mousedown while a drag is still active ends that drag first, so listeners never pile up', () => {
    const onResize = vi.fn()
    const onResizeEnd = vi.fn()
    const { container } = render(<PaneSplitter direction="h" onResize={onResize} onResizeEnd={onResizeEnd} />)
    const handle = container.firstElementChild as HTMLElement
    fireEvent.mouseDown(handle, { clientX: 100 })
    fireEvent.mouseDown(handle, { clientX: 200 })
    expect(onResizeEnd).toHaveBeenCalledTimes(1)
    fireEvent.mouseMove(document, { clientX: 210 })
    expect(onResize).toHaveBeenCalledTimes(1)
    expect(onResize).toHaveBeenCalledWith(10)
    fireEvent.mouseUp(document)
    expect(onResizeEnd).toHaveBeenCalledTimes(2)
  })

  it('without onResizeEnd (pane splits) a blur still ends the drag cleanly', () => {
    const onResize = vi.fn()
    const { container } = render(<PaneSplitter direction="h" onResize={onResize} />)
    fireEvent.mouseDown(container.firstElementChild as HTMLElement, { clientX: 100 })
    expect(() => fireEvent.blur(window)).not.toThrow()
    fireEvent.mouseMove(document, { clientX: 150 })
    expect(onResize).not.toHaveBeenCalled()
    expect(document.body.style.userSelect).toBe('')
  })

  it('renders testId as data-testid on the root', () => {
    const { container, getByTestId } = render(
      <PaneSplitter direction="v" onResize={vi.fn()} testId="worker-list-divider" />,
    )
    expect(getByTestId('worker-list-divider')).toBe(container.firstElementChild)
  })
})
