import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { HistoryRow } from './HistoryRow'
import type { SnapshotMetadata } from '../../../../lib/sync/snapshot-types'

const base: SnapshotMetadata = {
  id: 'r1',
  timestamp: Date.now(),
  device: 'mlab',
  trigger: 'manual',
  bundleSize: 1234,
  contributorIds: ['workspaces', 'hosts'],
  isSessionPristine: false,
}

describe('HistoryRow', () => {
  it('renders trigger tag and device', () => {
    render(<HistoryRow meta={base} selected={false} onSelect={() => {}} />)
    expect(screen.getByText(/manual/i)).toBeInTheDocument()
    expect(screen.getByText('mlab')).toBeInTheDocument()
  })

  it('highlights when selected', () => {
    const { container } = render(<HistoryRow meta={base} selected onSelect={() => {}} />)
    expect(container.firstChild).toHaveAttribute('data-selected', 'true')
  })

  it('shows pristine badge when isSessionPristine', () => {
    render(<HistoryRow meta={{ ...base, isSessionPristine: true, trigger: 'pre-restore' }} selected={false} onSelect={() => {}} />)
    expect(screen.getByTestId('pristine-badge')).toBeInTheDocument()
  })
})
