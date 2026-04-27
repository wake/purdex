import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { HostPickerPopover } from './HostPickerPopover'
import { useHostStore } from '../stores/useHostStore'

function setHosts() {
  useHostStore.setState({
    hosts: {
      h1: { id: 'h1', name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0 },
      h2: { id: 'h2', name: 'air', ip: '100.64.0.1', port: 7860, order: 1 },
    },
    hostOrder: ['h1', 'h2'],
    runtime: {
      h1: { status: 'connected' },
      h2: { status: 'disconnected' },
    },
    activeHostId: 'h1',
  })
}

describe('HostPickerPopover', () => {
  beforeEach(() => {
    setHosts()
  })

  it('does not render when open=false', () => {
    const { container } = render(
      <HostPickerPopover open={false} anchor={{ x: 0, y: 0 }} onSelect={vi.fn()} onCancel={vi.fn()} />,
    )
    expect(container.firstChild).toBeNull()
  })

  it('lists hosts in hostOrder with name + online/offline indicator', () => {
    render(
      <HostPickerPopover open={true} anchor={{ x: 0, y: 0 }} onSelect={vi.fn()} onCancel={vi.fn()} />,
    )
    const items = screen.getAllByRole('option')
    expect(items).toHaveLength(2)
    expect(items[0]).toHaveTextContent('mlab')
    expect(items[1]).toHaveTextContent('air')
    // online indicator on h1
    expect(items[0].textContent?.toLowerCase()).toMatch(/online|connected/)
    // offline indicator on h2
    expect(items[1].textContent?.toLowerCase()).toMatch(/offline/)
  })

  it('Enter on focused item triggers onSelect with that hostId', () => {
    const onSelect = vi.fn()
    render(
      <HostPickerPopover open={true} anchor={{ x: 0, y: 0 }} onSelect={onSelect} onCancel={vi.fn()} />,
    )
    const items = screen.getAllByRole('option')
    items[0].focus()
    fireEvent.keyDown(items[0], { key: 'Enter' })
    expect(onSelect).toHaveBeenCalledWith('h1')
  })

  it('ArrowDown / ArrowUp moves focus through items', () => {
    render(
      <HostPickerPopover open={true} anchor={{ x: 0, y: 0 }} onSelect={vi.fn()} onCancel={vi.fn()} />,
    )
    const items = screen.getAllByRole('option')
    items[0].focus()
    fireEvent.keyDown(items[0], { key: 'ArrowDown' })
    expect(document.activeElement).toBe(items[1])
    fireEvent.keyDown(items[1], { key: 'ArrowUp' })
    expect(document.activeElement).toBe(items[0])
  })

  it('Esc triggers onCancel and not onSelect', () => {
    const onSelect = vi.fn()
    const onCancel = vi.fn()
    render(
      <HostPickerPopover open={true} anchor={{ x: 0, y: 0 }} onSelect={onSelect} onCancel={onCancel} />,
    )
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onCancel).toHaveBeenCalledTimes(1)
    expect(onSelect).not.toHaveBeenCalled()
  })

  it('clicking an offline host still calls onSelect (not disabled)', () => {
    const onSelect = vi.fn()
    render(
      <HostPickerPopover open={true} anchor={{ x: 0, y: 0 }} onSelect={onSelect} onCancel={vi.fn()} />,
    )
    fireEvent.click(screen.getAllByRole('option')[1])
    expect(onSelect).toHaveBeenCalledWith('h2')
  })

  it('shows empty state with close button when hostOrder is empty (codex round-1 B2)', () => {
    useHostStore.setState({ hosts: {}, hostOrder: [], runtime: {}, activeHostId: null })
    const onCancel = vi.fn()
    render(
      <HostPickerPopover open={true} anchor={{ x: 0, y: 0 }} onSelect={vi.fn()} onCancel={onCancel} />,
    )
    expect(screen.getByText(/No hosts available/i)).toBeInTheDocument()
    // close button must exist in empty state — Esc-only is not enough (a11y / mouse users)
    const closeBtn = screen.getByRole('button', { name: /close|cancel|關閉|取消/i })
    expect(closeBtn).toBeInTheDocument()
    fireEvent.click(closeBtn)
    expect(onCancel).toHaveBeenCalledTimes(1)
  })

  it('focus is trapped inside the popover (Tab cycles)', () => {
    render(
      <HostPickerPopover open={true} anchor={{ x: 0, y: 0 }} onSelect={vi.fn()} onCancel={vi.fn()} />,
    )
    const items = screen.getAllByRole('option')
    items[items.length - 1].focus()
    fireEvent.keyDown(items[items.length - 1], { key: 'Tab' })
    // focus should wrap back to first
    expect(document.activeElement).toBe(items[0])
  })

  // codex round-1 B3 — HTMLElement anchor positioning
  it('positions popover below an HTMLElement anchor using getBoundingClientRect (codex round-1 B3)', () => {
    const anchor = document.createElement('button')
    anchor.getBoundingClientRect = () =>
      ({
        top: 100,
        right: 250,
        bottom: 130,
        left: 200,
        width: 50,
        height: 30,
        x: 200,
        y: 100,
        toJSON: () => ({}),
      }) as DOMRect
    document.body.appendChild(anchor)
    const { container } = render(
      <HostPickerPopover open={true} anchor={anchor} onSelect={vi.fn()} onCancel={vi.fn()} />,
    )
    const popover = container.querySelector('[role="listbox"]') as HTMLElement
    expect(popover).not.toBeNull()
    // top = rect.bottom + 4 = 134; left = rect.left = 200
    expect(popover.style.top).toBe('134px')
    expect(popover.style.left).toBe('200px')
    expect(popover.style.position).toBe('fixed')
    document.body.removeChild(anchor)
  })
})
