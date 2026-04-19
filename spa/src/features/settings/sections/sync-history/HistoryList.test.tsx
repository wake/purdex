import { describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { HistoryList } from './HistoryList'
import type { SnapshotMetadata } from '../../../../lib/sync/snapshot-types'

const items: SnapshotMetadata[] = [
  { id: 'a', timestamp: 2, device: 'd', trigger: 'manual', bundleSize: 1, contributorIds: [], isSessionPristine: false },
  { id: 'b', timestamp: 1, device: 'd', trigger: 'pre-import', bundleSize: 1, contributorIds: [], isSessionPristine: false },
]

describe('HistoryList', () => {
  it('renders empty state when no items', () => {
    render(<HistoryList items={[]} loading={false} error={null} selectedId={null} onSelect={() => {}} />)
    expect(screen.getByText(/no/i)).toBeInTheDocument()
  })

  it('renders rows and dispatches onSelect', () => {
    const fn = vi.fn()
    render(<HistoryList items={items} loading={false} error={null} selectedId="a" onSelect={fn} />)
    fireEvent.click(screen.getAllByRole('button')[1])
    expect(fn).toHaveBeenCalledWith('b')
  })

  it('shows loading spinner', () => {
    render(<HistoryList items={[]} loading={true} error={null} selectedId={null} onSelect={() => {}} />)
    expect(screen.getByTestId('loading')).toBeInTheDocument()
  })

  it('shows error + Retry', () => {
    const retry = vi.fn()
    render(<HistoryList items={[]} loading={false} error={new Error('boom')} selectedId={null} onSelect={() => {}} onRetry={retry} />)
    fireEvent.click(screen.getByRole('button', { name: /retry/i }))
    expect(retry).toHaveBeenCalled()
  })
})
