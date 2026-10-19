// spa/src/features/workspace/components/TaskGroupHeader.test.tsx — a group's header in the task-group view (name pool v2 spec §7, NA-6c): a manual group's name is renamed in place, a group with nothing live offers 「清除」,
// and when the host's groups cannot be edited both stay, inert, with the reason as tooltip and accessible description.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react'
import { TaskGroupHeader } from './TaskGroupHeader'
import { useI18nStore } from '../../../stores/useI18nStore'

const BLOCKED = '這台主機有 2 列讀不了，暫時不能編輯'
const name = () => screen.getByTestId('task-group-name')
const input = () => screen.getByTestId('task-group-name-input') as HTMLInputElement

beforeEach(() => { cleanup(); useI18nStore.getState().setLocale('zh-TW') })
afterEach(() => { cleanup() })

describe('rename in place', () => {
  it('a double-click opens the name; Enter saves the trimmed draft and closes once the answer is in', async () => {
    let done!: () => void
    const onCommit = vi.fn(() => new Promise<void>((r) => { done = r }))
    render(<TaskGroupHeader color="#000" name="Old" count={1} edit={{ blocked: null, onCommit }} />)
    fireEvent.doubleClick(name())
    expect(input()).toHaveValue('Old')
    fireEvent.change(input(), { target: { value: '  New  ' } })
    fireEvent.keyDown(input(), { key: 'Enter' })
    expect(onCommit).toHaveBeenCalledWith('New')
    expect(input()).toBeDisabled() // waiting for the answer
    await act(async () => { done() })
    expect(screen.queryByTestId('task-group-name-input')).toBeNull()
  })

  it('the name wears a focus-visible ring and no default focus outline', () => {
    render(<TaskGroupHeader color="#000" name="Old" count={1} edit={{ blocked: null, onCommit: vi.fn() }} />)
    expect(name().className).toContain('focus-visible:ring-1')
    expect(name().className).toContain('focus:outline-none')
  })

  it('an Enter that picks an IME candidate is not a save', () => {
    const onCommit = vi.fn().mockResolvedValue(undefined)
    render(<TaskGroupHeader color="#000" name="Old" count={1} edit={{ blocked: null, onCommit }} />)
    fireEvent.doubleClick(name())
    fireEvent.change(input(), { target: { value: 'ㄅㄧ' } })
    fireEvent.keyDown(input(), { key: 'Enter', isComposing: true })
    fireEvent.keyDown(input(), { key: 'Enter', keyCode: 229 })
    expect(onCommit).not.toHaveBeenCalled()
    expect(input()).not.toBeDisabled()
  })

  it('Esc and a click away drop the draft and send nothing; an unchanged name sends nothing', () => {
    const onCommit = vi.fn().mockResolvedValue(undefined)
    render(<TaskGroupHeader color="#000" name="Old" count={1} edit={{ blocked: null, onCommit }} />)
    fireEvent.doubleClick(name())
    fireEvent.change(input(), { target: { value: 'Other' } })
    fireEvent.keyDown(input(), { key: 'Escape' })
    expect(screen.queryByTestId('task-group-name-input')).toBeNull()
    fireEvent.doubleClick(name())
    fireEvent.change(input(), { target: { value: 'Other' } })
    fireEvent.blur(input())
    expect(screen.queryByTestId('task-group-name-input')).toBeNull()
    fireEvent.doubleClick(name())
    fireEvent.keyDown(input(), { key: 'Enter' })
    expect(onCommit).not.toHaveBeenCalled()
  })

  it('a host that cannot be edited: the name does not open, and says why', () => {
    const onCommit = vi.fn()
    render(<TaskGroupHeader color="#000" name="Old" count={1} edit={{ blocked: BLOCKED, onCommit }} />)
    fireEvent.doubleClick(name())
    expect(screen.queryByTestId('task-group-name-input')).toBeNull()
    expect(name()).toHaveAttribute('title', BLOCKED)
    expect(onCommit).not.toHaveBeenCalled()
  })

  it('the keyboard opens it too: the name is focusable and Enter or F2 starts the edit (not while an IME is composing)', () => {
    const onCommit = vi.fn().mockResolvedValue(undefined)
    render(<TaskGroupHeader color="#000" name="Old" count={1} edit={{ blocked: null, onCommit }} />)
    expect(name()).toHaveAttribute('role', 'button')
    name().focus()
    expect(document.activeElement).toBe(name())
    fireEvent.keyDown(name(), { key: 'Enter', isComposing: true })
    fireEvent.keyDown(name(), { key: 'Enter', keyCode: 229 })
    expect(screen.queryByTestId('task-group-name-input')).toBeNull()
    fireEvent.keyDown(name(), { key: 'F2' })
    expect(input()).toHaveValue('Old')
  })

  it('Space opens it as well, as a button does', () => {
    render(<TaskGroupHeader color="#000" name="Old" count={1} edit={{ blocked: null, onCommit: vi.fn() }} />)
    fireEvent.keyDown(name(), { key: ' ' })
    expect(input()).toHaveValue('Old')
  })

  it('a blocked name is still focusable, aria-disabled, and carries the reason as its description', () => {
    render(<TaskGroupHeader color="#000" name="Old" count={1} edit={{ blocked: BLOCKED, onCommit: vi.fn() }} />)
    expect(name()).toHaveAttribute('aria-disabled', 'true')
    expect(name()).toHaveAccessibleDescription(BLOCKED)
    expect(name()).toHaveAccessibleName('Old')
    fireEvent.keyDown(name(), { key: 'Enter' })
    expect(screen.queryByTestId('task-group-name-input')).toBeNull()
  })

  it('a team header hands the keyboard the same way', () => {
    const onDoubleClickName = vi.fn()
    render(<TaskGroupHeader color="#000" name="Team" count={3} strong onDoubleClickName={onDoubleClickName} />)
    fireEvent.keyDown(name(), { key: 'Enter' })
    expect(onDoubleClickName).toHaveBeenCalledTimes(1)
  })

  it('a name nobody can edit is plain text', () => {
    render(<TaskGroupHeader color="#000" name="未分組" count={2} muted />)
    expect(name()).not.toHaveAttribute('role')
    expect(name()).not.toHaveAttribute('tabindex')
  })

  it('a team header hands the double-click to the team edit instead', () => {
    const onDoubleClickName = vi.fn()
    render(<TaskGroupHeader color="#000" name="Team" count={3} strong onDoubleClickName={onDoubleClickName} />)
    fireEvent.doubleClick(name())
    expect(onDoubleClickName).toHaveBeenCalledTimes(1)
    expect(screen.queryByTestId('task-group-name-input')).toBeNull()
  })
})

describe('清除', () => {
  it('is offered only when asked for, and runs on click', () => {
    const { rerender } = render(<TaskGroupHeader color="#000" name="G" count={0} />)
    expect(screen.queryByTestId('task-group-clear')).toBeNull()
    const onClick = vi.fn()
    rerender(<TaskGroupHeader color="#000" name="G" count={0} clear={{ blocked: null, onClick }} />)
    fireEvent.click(screen.getByTestId('task-group-clear'))
    expect(onClick).toHaveBeenCalledTimes(1)
  })

  it('blocked: inert, focusable, its name stays 「清除」 and the reason is its description and tooltip', () => {
    const onClick = vi.fn()
    render(<TaskGroupHeader color="#000" name="G" count={0} clear={{ blocked: BLOCKED, onClick }} />)
    const b = screen.getByTestId('task-group-clear')
    fireEvent.click(b)
    expect(onClick).not.toHaveBeenCalled()
    expect(b).toHaveAttribute('aria-disabled', 'true')
    expect(b).toHaveAttribute('title', BLOCKED)
    expect(b).toHaveAccessibleName('清除')
    expect(b).toHaveAccessibleDescription(BLOCKED)
    b.focus()
    expect(document.activeElement).toBe(b)
  })
})
