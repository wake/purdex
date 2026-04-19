import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { SnapshotDetail } from './SnapshotDetail'
import type { StoredSnapshot } from '../../../../lib/sync/snapshot-types'

const snap: StoredSnapshot = {
  id: 's1',
  timestamp: Date.now(),
  device: 'd',
  trigger: 'manual',
  bundleSize: 512,  // formatBytes returns "512 B" — test can match /512/
  contributorIds: ['w', 'h'],
  isSessionPristine: false,
  bundle: { version: 1, timestamp: 0, device: 'd', collections: {} },
}

describe('SnapshotDetail', () => {
  it('shows empty placeholder when no snapshot selected', () => {
    render(<SnapshotDetail snapshot={null} diff={null} onRestore={() => {}} restoring={false} />)
    // i18n key literal "settings.sync.history.detail.selectPrompt" contains "select"
    expect(screen.getByText(/select/i)).toBeInTheDocument()
  })

  it('shows metadata when snapshot given', () => {
    render(<SnapshotDetail snapshot={snap} diff={null} onRestore={() => {}} restoring={false} />)
    expect(screen.getByText(/512/)).toBeInTheDocument()
  })

  it('shows diff list from contributors', () => {
    render(
      <SnapshotDetail
        snapshot={snap}
        diff={[
          { id: 'w', status: 'changed' },
          { id: 'h', status: 'identical' },
        ]}
        onRestore={() => {}}
        restoring={false}
      />,
    )
    expect(screen.getByText('w')).toBeInTheDocument()
    expect(screen.getByText('h')).toBeInTheDocument()
  })

  it('disables Restore when restoring', () => {
    const fn = vi.fn()
    render(<SnapshotDetail snapshot={snap} diff={[]} onRestore={fn} restoring={true} />)
    const btn = screen.getByRole('button', { name: /restore/i })
    expect(btn).toBeDisabled()
  })
})
