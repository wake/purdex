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
})
