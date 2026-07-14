import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { EditableCwdCell } from './EditableCwdCell'

describe('EditableCwdCell', () => {
  it('display mode shows the cwd (font-mono) and an em-dash when undefined', () => {
    const { rerender } = render(<EditableCwdCell cwd="/home/work" onCommit={() => {}} />)
    const cell = screen.getByTestId('snapshot-cwd-cell')
    expect(cell.textContent).toBe('/home/work')
    expect(cell.className).toContain('font-mono')

    rerender(<EditableCwdCell cwd={undefined} onCommit={() => {}} />)
    expect(screen.getByTestId('snapshot-cwd-cell').textContent).toBe('—')
  })

  it('double-click → input appears pre-filled with the current cwd', () => {
    render(<EditableCwdCell cwd="/home/work" onCommit={() => {}} />)
    expect(screen.queryByTestId('snapshot-cwd-input')).toBeNull()

    fireEvent.doubleClick(screen.getByTestId('snapshot-cwd-cell'))
    const input = screen.getByTestId('snapshot-cwd-input') as HTMLInputElement
    expect(input.value).toBe('/home/work')
  })

  it('double-click on an undefined cwd → input pre-filled with empty string', () => {
    render(<EditableCwdCell cwd={undefined} onCommit={() => {}} />)
    fireEvent.doubleClick(screen.getByTestId('snapshot-cwd-cell'))
    expect((screen.getByTestId('snapshot-cwd-input') as HTMLInputElement).value).toBe('')
  })

  it('type + Enter → onCommit called once with the typed value, leaves edit mode', () => {
    const onCommit = vi.fn()
    render(<EditableCwdCell cwd="/old" onCommit={onCommit} />)
    fireEvent.doubleClick(screen.getByTestId('snapshot-cwd-cell'))

    const input = screen.getByTestId('snapshot-cwd-input')
    fireEvent.change(input, { target: { value: '/new/dir' } })
    fireEvent.keyDown(input, { key: 'Enter' })

    expect(onCommit).toHaveBeenCalledTimes(1)
    expect(onCommit).toHaveBeenCalledWith('/new/dir')
    expect(screen.queryByTestId('snapshot-cwd-input')).toBeNull()
    expect(screen.getByTestId('snapshot-cwd-cell')).toBeTruthy()
  })

  it('blur → commits with the current value', () => {
    const onCommit = vi.fn()
    render(<EditableCwdCell cwd="/old" onCommit={onCommit} />)
    fireEvent.doubleClick(screen.getByTestId('snapshot-cwd-cell'))

    const input = screen.getByTestId('snapshot-cwd-input')
    fireEvent.change(input, { target: { value: '/blurred' } })
    fireEvent.blur(input)

    expect(onCommit).toHaveBeenCalledTimes(1)
    expect(onCommit).toHaveBeenCalledWith('/blurred')
  })

  it('Enter does not double-commit on the following blur', () => {
    const onCommit = vi.fn()
    render(<EditableCwdCell cwd="/old" onCommit={onCommit} />)
    fireEvent.doubleClick(screen.getByTestId('snapshot-cwd-cell'))

    const input = screen.getByTestId('snapshot-cwd-input')
    fireEvent.change(input, { target: { value: '/once' } })
    fireEvent.keyDown(input, { key: 'Enter' })
    // The Enter already unmounted the input; simulate the trailing blur explicitly.
    fireEvent.blur(input)

    expect(onCommit).toHaveBeenCalledTimes(1)
  })

  it('Esc → onCommit NOT called, reverts to display showing the original cwd', () => {
    const onCommit = vi.fn()
    render(<EditableCwdCell cwd="/original" onCommit={onCommit} />)
    fireEvent.doubleClick(screen.getByTestId('snapshot-cwd-cell'))

    const input = screen.getByTestId('snapshot-cwd-input')
    fireEvent.change(input, { target: { value: '/discarded' } })
    fireEvent.keyDown(input, { key: 'Escape' })
    // The trailing blur after Esc must not sneak a commit through.
    fireEvent.blur(input)

    expect(onCommit).not.toHaveBeenCalled()
    expect(screen.queryByTestId('snapshot-cwd-input')).toBeNull()
    expect(screen.getByTestId('snapshot-cwd-cell').textContent).toBe('/original')
  })
})
