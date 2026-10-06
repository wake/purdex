import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { RebuildModeChoice } from './RebuildModeChoice'

afterEach(cleanup)

describe('RebuildModeChoice', () => {
  it('marks the preselected mode and switches', () => {
    const onChange = vi.fn()
    render(<RebuildModeChoice value="worker" onChange={onChange} terminalAvailable workerAvailable />)
    expect(screen.getByRole('radiogroup')).toBeInTheDocument()
    expect(screen.getByTestId('rebuild-mode-worker')).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByTestId('rebuild-mode-terminal')).toHaveAttribute('aria-checked', 'false')
    fireEvent.click(screen.getByTestId('rebuild-mode-terminal'))
    expect(onChange).toHaveBeenCalledWith('terminal')
  })

  it('disables an unavailable worker option with its hint', () => {
    const onChange = vi.fn()
    render(<RebuildModeChoice value="terminal" onChange={onChange} terminalAvailable workerAvailable={false} workerUnavailableHint="nope" />)
    const w = screen.getByTestId('rebuild-mode-worker')
    expect(w).toBeDisabled()
    expect(w).toHaveAttribute('title', 'nope')
    fireEvent.click(w)
    expect(onChange).not.toHaveBeenCalled()
  })

  it('shows the hint as visible text referenced by aria-describedby, even with terminal available', () => {
    render(<RebuildModeChoice value="terminal" onChange={vi.fn()} terminalAvailable workerAvailable={false} workerUnavailableHint="nope" />)
    const hint = screen.getByText('nope')
    expect(hint).toBeVisible()
    expect(screen.getByRole('radiogroup')).toHaveAttribute('aria-describedby', hint.id)
  })

  it('no hint text and no aria-describedby when the worker option is available', () => {
    render(<RebuildModeChoice value="worker" onChange={vi.fn()} terminalAvailable workerAvailable workerUnavailableHint="nope" />)
    expect(screen.queryByText('nope')).toBeNull()
    expect(screen.getByRole('radiogroup')).not.toHaveAttribute('aria-describedby')
  })

  it('roving tabindex: only the checked radio is tabbable', () => {
    render(<RebuildModeChoice value="worker" onChange={vi.fn()} terminalAvailable workerAvailable />)
    expect(screen.getByTestId('rebuild-mode-worker')).toHaveAttribute('tabindex', '0')
    expect(screen.getByTestId('rebuild-mode-terminal')).toHaveAttribute('tabindex', '-1')
  })

  it('if the checked radio is disabled the first enabled one is tabbable', () => {
    render(<RebuildModeChoice value="worker" onChange={vi.fn()} terminalAvailable workerAvailable={false} />)
    expect(screen.getByTestId('rebuild-mode-worker')).toHaveAttribute('tabindex', '-1')
    expect(screen.getByTestId('rebuild-mode-terminal')).toHaveAttribute('tabindex', '0')
  })

  it('arrow keys select and focus the other enabled option', () => {
    const onChange = vi.fn()
    render(<RebuildModeChoice value="worker" onChange={onChange} terminalAvailable workerAvailable />)
    const worker = screen.getByTestId('rebuild-mode-worker')
    fireEvent.keyDown(worker, { key: 'ArrowLeft' })
    expect(onChange).toHaveBeenLastCalledWith('terminal')
    expect(screen.getByTestId('rebuild-mode-terminal')).toHaveFocus()
    fireEvent.keyDown(screen.getByTestId('rebuild-mode-terminal'), { key: 'ArrowDown' })
    expect(onChange).toHaveBeenLastCalledWith('worker')
    expect(worker).toHaveFocus()
    fireEvent.keyDown(worker, { key: 'ArrowUp' })
    expect(onChange).toHaveBeenLastCalledWith('terminal')
    fireEvent.keyDown(screen.getByTestId('rebuild-mode-terminal'), { key: 'ArrowRight' })
    expect(onChange).toHaveBeenLastCalledWith('worker')
  })

  it('arrow keys skip a disabled option', () => {
    const onChange = vi.fn()
    render(<RebuildModeChoice value="terminal" onChange={onChange} terminalAvailable workerAvailable={false} />)
    fireEvent.keyDown(screen.getByTestId('rebuild-mode-terminal'), { key: 'ArrowRight' })
    expect(onChange).not.toHaveBeenCalled()
  })

  it('every arrow key is default-prevented even with no other enabled option (the page never scrolls)', () => {
    const onChange = vi.fn()
    render(<RebuildModeChoice value="terminal" onChange={onChange} terminalAvailable workerAvailable={false} />)
    for (const key of ['ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight']) {
      // fireEvent returns false when the event was default-prevented.
      expect(fireEvent.keyDown(screen.getByTestId('rebuild-mode-terminal'), { key })).toBe(false)
    }
    expect(onChange).not.toHaveBeenCalled()
  })
})
