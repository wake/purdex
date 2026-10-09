// spa/src/components/team/useHeaderGestures.test.tsx — the header's click arbitration on its own (TI-7): which action
// cancels a pending name-click toggle, and when the edit form is dropped.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { useHeaderGestures } from './useHeaderGestures'
import { NAME_CLICK_DELAY_MS } from './panel-layout'

interface P { teamKey: string; mode: 'full' | 'line'; canEdit: boolean; onSetMode: (m: 'full' | 'line') => void }

function Harness({ teamKey, mode, canEdit, onSetMode }: P) {
  const { rootRef, hdr, editOpen } = useHeaderGestures({ teamKey, mode, onSetMode, canEdit })
  return (
    <div ref={rootRef}>
      <div data-testid="team-panel-header" {...hdr}>
        <span data-testid="team-panel-name">name</span>
        <span data-testid="blank">blank</span>
        <button type="button" data-testid="btn">b</button>
      </div>
      <span data-testid="open">{String(editOpen)}</span>
    </div>
  )
}

const onSetMode = vi.fn()
const base: P = { teamKey: 'a', mode: 'full', canEdit: true, onSetMode }
const advance = (ms: number) => act(() => { vi.advanceTimersByTime(ms) })
const nameClick = () => fireEvent.click(screen.getByTestId('team-panel-name'))

beforeEach(() => { vi.useFakeTimers(); onSetMode.mockReset() })
afterEach(() => { cleanup(); vi.useRealTimers() })

describe('a pending name-click toggle', () => {
  it('fires once after the delay when nothing else happens', () => {
    render(<Harness {...base} />)
    nameClick()
    advance(NAME_CLICK_DELAY_MS)
    expect(onSetMode.mock.calls).toEqual([['line']])
  })

  it('is cancelled by a click on a header button', () => {
    render(<Harness {...base} />)
    nameClick()
    fireEvent.click(screen.getByTestId('btn'))
    advance(NAME_CLICK_DELAY_MS * 2)
    expect(onSetMode).not.toHaveBeenCalled()
  })

  it('is replaced by a click elsewhere on the header, which toggles once at once', () => {
    render(<Harness {...base} />)
    nameClick()
    fireEvent.click(screen.getByTestId('blank'))
    expect(onSetMode).toHaveBeenCalledTimes(1)
    advance(NAME_CLICK_DELAY_MS * 2)
    expect(onSetMode).toHaveBeenCalledTimes(1)
  })

  it('is cancelled by the mode changing from outside', () => {
    const { rerender } = render(<Harness {...base} />)
    nameClick()
    rerender(<Harness {...base} mode="line" />)
    advance(NAME_CLICK_DELAY_MS * 2)
    expect(onSetMode).not.toHaveBeenCalled()
  })

  it('is cancelled by another team coming up', () => {
    const { rerender } = render(<Harness {...base} />)
    nameClick()
    rerender(<Harness {...base} teamKey="b" />)
    advance(NAME_CLICK_DELAY_MS * 2)
    expect(onSetMode).not.toHaveBeenCalled()
  })

  it('is cancelled by unmounting', () => {
    const { unmount } = render(<Harness {...base} />)
    nameClick()
    unmount()
    advance(NAME_CLICK_DELAY_MS * 2)
    expect(onSetMode).not.toHaveBeenCalled()
  })
})

describe('the edit form', () => {
  const open = () => fireEvent.doubleClick(screen.getByTestId('team-panel-name'))
  const isOpen = () => screen.getByTestId('open').textContent

  it('opens on a double-click on the name only when the host can edit', () => {
    const { rerender } = render(<Harness {...base} canEdit={false} />)
    open()
    expect(isOpen()).toBe('false')
    rerender(<Harness {...base} />)
    fireEvent.doubleClick(screen.getByTestId('blank'))
    expect(isOpen()).toBe('false')
    open()
    expect(isOpen()).toBe('true')
  })

  it('does not come back when the panel goes A -> B -> A', () => {
    const { rerender } = render(<Harness {...base} />)
    open()
    expect(isOpen()).toBe('true')
    rerender(<Harness {...base} teamKey="b" />)
    expect(isOpen()).toBe('false')
    rerender(<Harness {...base} teamKey="a" />)
    expect(isOpen()).toBe('false')
  })
})
