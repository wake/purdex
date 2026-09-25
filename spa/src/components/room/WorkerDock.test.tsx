import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
import WorkerDock from './WorkerDock'

const mine = (p: string | undefined) => p === 'pdx:mlab/t-me'

describe('WorkerDock', () => {
  it('shows the live state, observers and lease in one row', () => {
    render(<WorkerDock sse="open" observers={2} lease={{ principal_id: 'pdx:mlab/t-me', expires_at: 1 }} isMine={mine} />)
    const dock = screen.getByTestId('worker-dock')
    // Its own hairline top border: it sits between the transcript and the input.
    expect(dock.className).toMatch(/\bborder-t\b/)
    const row = screen.getByTestId('worker-dock-row')
    expect(row).toHaveTextContent('live · 2 observers · lease: you')
    expect(screen.getByTestId('worker-dock-dot').className).toMatch(/\bbg-status-success\b/)
    expect(screen.queryByTestId('worker-dock-table')).toBeNull()
  })

  it('follows the sse state', () => {
    const { rerender } = render(<WorkerDock sse="idle" observers={0} isMine={mine} />)
    expect(screen.getByTestId('worker-dock-row')).toHaveTextContent(/^connecting/)
    rerender(<WorkerDock sse="paused" observers={0} isMine={mine} />)
    expect(screen.getByTestId('worker-dock-row')).toHaveTextContent(/paused/)
    rerender(<WorkerDock sse="closed" observers={0} isMine={mine} />)
    expect(screen.getByTestId('worker-dock-row')).toHaveTextContent(/disconnected/)
    expect(screen.getByTestId('worker-dock-dot').className).toMatch(/\bbg-status-error\b/)
  })

  it('expands into a table', () => {
    render(<WorkerDock sse="open" observers={2} lease={{ principal_id: 'pdx:mlab/t-other', expires_at: 1 }} isMine={mine} />)
    const toggle = screen.getByTestId('worker-dock-toggle')
    expect(toggle.getAttribute('aria-expanded')).toBe('false')
    expect(toggle.getAttribute('aria-label')).toMatch(/expand/i)
    fireEvent.click(toggle)
    expect(toggle.getAttribute('aria-expanded')).toBe('true')
    expect(toggle.getAttribute('aria-label')).toMatch(/collapse/i)
    const table = screen.getByTestId('worker-dock-table')
    const rows = within(table).getAllByRole('row')
    expect(rows).toHaveLength(3)
    expect(rows[0]).toHaveTextContent(/stream.*live/i)
    expect(rows[1]).toHaveTextContent(/observers.*2/i)
    expect(rows[2]).toHaveTextContent(/lease.*pdx:mlab\/t-other/i)
    // The collapsed row gives way to the table.
    expect(screen.queryByTestId('worker-dock-row')).toBeNull()
    fireEvent.click(toggle)
    expect(screen.queryByTestId('worker-dock-table')).toBeNull()
    expect(screen.getByTestId('worker-dock-row')).toBeInTheDocument()
  })

  it('marks the lease as yours', () => {
    render(<WorkerDock sse="open" observers={1} lease={{ principal_id: 'pdx:mlab/t-me', expires_at: 1 }} isMine={mine} />)
    expect(screen.getByTestId('worker-dock-row')).toHaveTextContent('lease: you')
    fireEvent.click(screen.getByTestId('worker-dock-toggle'))
    // The table carries the full principal plus the marker.
    const lease = within(screen.getByTestId('worker-dock-table')).getAllByRole('row')[2]
    expect(lease).toHaveTextContent('pdx:mlab/t-me (you)')
  })

  it('shows another holder by principal in the row', () => {
    render(<WorkerDock sse="open" observers={1} lease={{ principal_id: 'pdx:mlab/t-other', expires_at: 1 }} isMine={mine} />)
    expect(screen.getByTestId('worker-dock-row')).toHaveTextContent('lease: pdx:mlab/t-other')
  })

  it('says no lease when there is none', () => {
    render(<WorkerDock sse="open" observers={0} isMine={mine} />)
    expect(screen.getByTestId('worker-dock-row')).toHaveTextContent('live · 0 observers · no lease')
    fireEvent.click(screen.getByTestId('worker-dock-toggle'))
    expect(within(screen.getByTestId('worker-dock-table')).getAllByRole('row')[2]).toHaveTextContent(/no lease/)
  })
})
