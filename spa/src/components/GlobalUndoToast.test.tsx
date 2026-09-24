import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, fireEvent, render, screen } from '@testing-library/react'
import { GlobalUndoToast } from './GlobalUndoToast'
import { useUndoToast } from '../stores/useUndoToast'

describe('GlobalUndoToast — render rules (codex round-1 B4)', () => {
  beforeEach(() => useUndoToast.setState({ toast: null }))

  it('renders default Undo label when action is provided but actionLabel is omitted', () => {
    useUndoToast.getState().show('Deleted host', () => {})
    render(<GlobalUndoToast />)
    expect(screen.getByRole('button')).toHaveTextContent(/Undo/i)
  })

  it('renders custom actionLabel (Retry) when both action and actionLabel are provided', () => {
    useUndoToast.getState().show('Send keys failed', () => {}, 'Retry')
    render(<GlobalUndoToast />)
    expect(screen.getByRole('button')).toHaveTextContent(/Retry/i)
  })

  it('does NOT render any button when action is undefined (create / switch failure path)', () => {
    useUndoToast.getState().show('Failed to start session: 500')
    render(<GlobalUndoToast />)
    // Message still shows
    expect(screen.getByText(/Failed to start session/i)).toBeInTheDocument()
    // No action button at all (codex round-1 B4 — no fake Undo / Retry)
    expect(screen.queryByRole('button')).toBeNull()
  })

  it('toast container has role=status (codex round-1 C14 — a11y live region)', () => {
    useUndoToast.getState().show('Hello')
    render(<GlobalUndoToast />)
    expect(screen.getByRole('status')).toBeInTheDocument()
  })
})

// PR #1413 critic: a failure the user must act on (a host deletion that could not put everything back) stays until
// they close it — it is not gone in 5 s like an undo offer.
describe('GlobalUndoToast — persistent notices', () => {
  beforeEach(() => {
    useUndoToast.setState({ toast: null, notice: null })
    vi.useFakeTimers()
  })
  afterEach(() => { vi.useRealTimers() })

  it('an ordinary toast is dismissed after 5 s', () => {
    useUndoToast.getState().show('Deleted host', () => {})
    render(<GlobalUndoToast />)
    act(() => { vi.advanceTimersByTime(5_000) })
    expect(useUndoToast.getState().toast).toBeNull()
  })

  it('a persistent one is not — it stays until its Close button is pressed', () => {
    useUndoToast.getState().show('Reload and check the host settings', undefined, undefined, { persistent: true })
    render(<GlobalUndoToast />)
    act(() => { vi.advanceTimersByTime(60_000) })
    expect(screen.getByText('Reload and check the host settings')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Close' }))
    expect(useUndoToast.getState().notice).toBeNull()
    expect(screen.queryByText('Reload and check the host settings')).toBeNull()
  })

  // PR #1413 critic 2: a later toast — an ordinary one, an Undo offer — must not replace the persistent notice. They
  // are kept apart and shown together: the notice until closed, the toast as ever (its button works, it times out).
  it('a later ordinary toast and a later Undo toast do not replace it: both are shown, both work', () => {
    useUndoToast.getState().show('Reload and check the host settings', undefined, undefined, { persistent: true })
    render(<GlobalUndoToast />)
    act(() => { useUndoToast.getState().show('Saved') })
    expect(screen.getByText('Reload and check the host settings')).toBeInTheDocument()
    expect(screen.getByText('Saved')).toBeInTheDocument()

    const undo = vi.fn()
    act(() => { useUndoToast.getState().show('B deleted', undo) })
    expect(screen.getByText('Reload and check the host settings')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Undo' }))
    expect(undo).toHaveBeenCalledOnce()
    expect(useUndoToast.getState().toast).toBeNull()
    expect(screen.getByText('Reload and check the host settings')).toBeInTheDocument()

    act(() => { useUndoToast.getState().show('Saved again') })
    act(() => { vi.advanceTimersByTime(5_000) })
    expect(useUndoToast.getState().toast).toBeNull() // the ordinary toast times out …
    expect(screen.getByText('Reload and check the host settings')).toBeInTheDocument() // … the notice does not
  })

  it('dismissing the ordinary toast leaves the notice; closing the notice leaves the toast', () => {
    useUndoToast.getState().show('notice', undefined, undefined, { persistent: true })
    useUndoToast.getState().show('toast')
    useUndoToast.getState().dismiss()
    expect(useUndoToast.getState().notice).toMatchObject({ message: 'notice' })
    useUndoToast.getState().show('toast')
    useUndoToast.getState().dismissNotice()
    expect(useUndoToast.getState().toast).toMatchObject({ message: 'toast' })
    expect(useUndoToast.getState().notice).toBeNull()
  })
})
