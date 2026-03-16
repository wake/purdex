// spa/src/components/TopBar.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import TopBar from './TopBar'

beforeEach(() => {
  cleanup()
})

describe('TopBar', () => {
  it('shows session name', () => {
    render(<TopBar sessionName="my-project" mode="term" onModeSwitch={vi.fn()} onInterrupt={vi.fn()} />)
    expect(screen.getByText('my-project')).toBeInTheDocument()
  })

  it('shows current mode', () => {
    render(<TopBar sessionName="test" mode="stream" onModeSwitch={vi.fn()} onInterrupt={vi.fn()} />)
    expect(screen.getByText('stream')).toBeInTheDocument()
  })

  it('calls onModeSwitch when toggled', () => {
    const onSwitch = vi.fn()
    render(<TopBar sessionName="test" mode="term" onModeSwitch={onSwitch} onInterrupt={vi.fn()} />)
    fireEvent.click(screen.getByTestId('mode-switch'))
    expect(onSwitch).toHaveBeenCalled()
  })

  it('shows interrupt button only in stream mode', () => {
    const { rerender } = render(
      <TopBar sessionName="test" mode="term" onModeSwitch={vi.fn()} onInterrupt={vi.fn()} />
    )
    expect(screen.queryByTestId('interrupt-btn')).toBeNull()

    rerender(
      <TopBar sessionName="test" mode="stream" onModeSwitch={vi.fn()} onInterrupt={vi.fn()} />
    )
    expect(screen.getByTestId('interrupt-btn')).toBeInTheDocument()
  })
})
