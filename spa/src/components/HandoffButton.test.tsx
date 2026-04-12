// spa/src/components/HandoffButton.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import HandoffButton from './HandoffButton'

beforeEach(() => {
  cleanup()
})

describe('HandoffButton', () => {
  it('renders button with Handoff label when idle', () => {
    render(<HandoffButton inProgress={false} agentStatus="idle" onHandoff={() => {}} />)
    expect(screen.getByText('Handoff')).toBeInTheDocument()
  })

  it('shows connecting state when in progress', () => {
    render(<HandoffButton inProgress={true} agentStatus="idle" onHandoff={() => {}} />)
    expect(screen.getByText('Connecting...')).toBeInTheDocument()
  })

  it('disables button during handoff', () => {
    render(<HandoffButton inProgress={true} agentStatus="idle" onHandoff={() => {}} />)
    expect(screen.getByRole('button')).toBeDisabled()
  })

  it('calls onHandoff when clicked with agent running', () => {
    const fn = vi.fn()
    render(<HandoffButton inProgress={false} agentStatus="running" onHandoff={fn} />)
    fireEvent.click(screen.getByRole('button'))
    expect(fn).toHaveBeenCalled()
  })

  it('disables button when no agent active', () => {
    render(<HandoffButton inProgress={false} agentStatus={undefined} onHandoff={() => {}} />)
    expect(screen.getByRole('button')).toBeDisabled()
    expect(screen.getByText('No CC running')).toBeInTheDocument()
  })

  it('shows progress label for detecting', () => {
    render(<HandoffButton inProgress={true} progress="detecting" agentStatus="idle" onHandoff={() => {}} />)
    expect(screen.getByText('Detecting CC...')).toBeInTheDocument()
  })

  it('shows progress label for stopping-cc', () => {
    render(<HandoffButton inProgress={true} progress="stopping-cc" agentStatus="idle" onHandoff={() => {}} />)
    expect(screen.getByText('Stopping CC...')).toBeInTheDocument()
  })

  it('shows progress label for launching', () => {
    render(<HandoffButton inProgress={true} progress="launching" agentStatus="idle" onHandoff={() => {}} />)
    expect(screen.getByText('Launching relay...')).toBeInTheDocument()
  })

  it('shows progress label for extracting-id', () => {
    render(<HandoffButton inProgress={true} progress="extracting-id" agentStatus="idle" onHandoff={() => {}} />)
    expect(screen.getByText('Extracting session...')).toBeInTheDocument()
  })

  it('shows progress label for exiting-cc', () => {
    render(<HandoffButton inProgress={true} progress="exiting-cc" agentStatus="idle" onHandoff={() => {}} />)
    expect(screen.getByText('Exiting CC...')).toBeInTheDocument()
  })

  it('enables button when agentStatus is error', () => {
    const fn = vi.fn()
    render(<HandoffButton inProgress={false} agentStatus="error" onHandoff={fn} />)
    const button = screen.getByRole('button')
    expect(button).not.toBeDisabled()
    fireEvent.click(button)
    expect(fn).toHaveBeenCalled()
  })

  it('enables button when agentStatus is waiting', () => {
    render(<HandoffButton inProgress={false} agentStatus="waiting" onHandoff={() => {}} />)
    expect(screen.getByRole('button')).not.toBeDisabled()
  })

  it('falls back to Connecting... with empty progress', () => {
    render(<HandoffButton inProgress={true} progress="" agentStatus="idle" onHandoff={() => {}} />)
    expect(screen.getByText('Connecting...')).toBeInTheDocument()
  })
})
