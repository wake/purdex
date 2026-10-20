// spa/src/features/workspace/components/TaskGroupHeaderMenu.test.tsx — the right-click menu of a manual group's header (task groups spec §6.1): rename, colour (team palette and the default), delete.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { TaskGroupHeaderMenu } from './TaskGroupHeaderMenu'
import { TEAM_COLORS } from '../../../components/team/team-display'
import { useI18nStore } from '../../../stores/useI18nStore'

function mount(over: Partial<React.ComponentProps<typeof TaskGroupHeaderMenu>> = {}) {
  const props = { position: { x: 10, y: 60 }, color: undefined, onRename: vi.fn(), onColor: vi.fn(), onDelete: vi.fn(), onClose: vi.fn(), ...over }
  render(<TaskGroupHeaderMenu {...props} />)
  return props
}

beforeEach(() => { cleanup(); useI18nStore.getState().setLocale('zh-TW') })
afterEach(() => { cleanup() })

describe('TaskGroupHeaderMenu', () => {
  it('lists rename, the colours and delete', () => {
    mount()
    expect(screen.getByTestId('task-group-menu-rename')).toHaveTextContent('重新命名')
    expect(screen.getByTestId('task-group-menu-delete')).toHaveTextContent('刪除任務編組')
    expect(screen.getByTestId('task-group-menu-color-default')).toBeInTheDocument()
    for (const c of TEAM_COLORS) expect(screen.getByTestId(`task-group-menu-color-${c}`)).toBeInTheDocument()
  })

  it('rename and delete call back and close the menu', () => {
    const p = mount()
    fireEvent.click(screen.getByTestId('task-group-menu-rename'))
    expect([p.onRename.mock.calls.length, p.onClose.mock.calls.length]).toEqual([1, 1])
    fireEvent.click(screen.getByTestId('task-group-menu-delete'))
    expect([p.onDelete.mock.calls.length, p.onClose.mock.calls.length]).toEqual([1, 2])
  })

  it('a colour is chosen from the team palette; the default swatch clears it; the current one is pressed', () => {
    const p = mount({ color: TEAM_COLORS[2] })
    expect(screen.getByTestId(`task-group-menu-color-${TEAM_COLORS[2]}`)).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByTestId(`task-group-menu-color-${TEAM_COLORS[0]}`)).toHaveAttribute('aria-pressed', 'false')
    fireEvent.click(screen.getByTestId(`task-group-menu-color-${TEAM_COLORS[0]}`))
    expect(p.onColor).toHaveBeenLastCalledWith(TEAM_COLORS[0])
    fireEvent.click(screen.getByTestId('task-group-menu-color-default'))
    expect(p.onColor).toHaveBeenLastCalledWith(undefined)
  })

  it('the default swatch is the pressed one when the group has no colour', () => {
    mount({ color: undefined })
    expect(screen.getByTestId('task-group-menu-color-default')).toHaveAttribute('aria-pressed', 'true')
  })

  it('Escape and a press on the backdrop close it', () => {
    const p = mount()
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(p.onClose).toHaveBeenCalledTimes(1)
    fireEvent.mouseDown(screen.getByTestId('task-group-menu-backdrop'))
    expect(p.onClose).toHaveBeenCalledTimes(2)
  })

  it('takes focus on open (the first item), moves with the arrows, Home and End, and Tab stays inside', () => {
    mount()
    const items = Array.from(screen.getByTestId('task-group-menu').querySelectorAll('button'))
    expect(document.activeElement).toBe(items[0])
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowDown' })
    expect(document.activeElement).toBe(items[1])
    fireEvent.keyDown(document.activeElement!, { key: 'End' })
    expect(document.activeElement).toBe(items[items.length - 1])
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowDown' }) // wraps
    expect(document.activeElement).toBe(items[0])
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowUp' }) // wraps back
    expect(document.activeElement).toBe(items[items.length - 1])
    fireEvent.keyDown(document.activeElement!, { key: 'Tab' })
    expect(document.activeElement).toBe(items[0])
    fireEvent.keyDown(document.activeElement!, { key: 'Tab', shiftKey: true })
    expect(document.activeElement).toBe(items[items.length - 1])
    fireEvent.keyDown(document.activeElement!, { key: 'Home' })
    expect(document.activeElement).toBe(items[0])
  })

  it('Escape closes only the menu (it does not reach a handler further out), and closing hands focus back', () => {
    const outer = vi.fn()
    document.addEventListener('keydown', outer)
    const opener = document.createElement('button')
    document.body.appendChild(opener)
    opener.focus()
    const p = mount()
    expect(document.activeElement).not.toBe(opener)
    fireEvent.keyDown(document.activeElement!, { key: 'Escape' })
    expect(p.onClose).toHaveBeenCalledTimes(1)
    expect(outer).not.toHaveBeenCalled()
    cleanup()
    expect(document.activeElement).toBe(opener)
    document.removeEventListener('keydown', outer)
    opener.remove()
  })
})

