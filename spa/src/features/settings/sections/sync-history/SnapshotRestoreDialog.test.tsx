import { describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { SnapshotRestoreDialog } from './SnapshotRestoreDialog'

describe('SnapshotRestoreDialog', () => {
  it('dispatches onConfirm when Restore clicked', () => {
    const onConfirm = vi.fn()
    render(<SnapshotRestoreDialog open pendingConflictCount={0} onCancel={() => {}} onConfirm={onConfirm} restoring={false} />)
    fireEvent.click(screen.getByRole('button', { name: /proceed/i }))
    expect(onConfirm).toHaveBeenCalled()
  })

  it('shows pending conflicts warning when count > 0', () => {
    const { container } = render(<SnapshotRestoreDialog open pendingConflictCount={3} onCancel={() => {}} onConfirm={() => {}} restoring={false} />)
    const warningParagraphs = container.querySelectorAll('p.text-status-warn-text')
    expect(warningParagraphs).toHaveLength(1)
  })

  it('disables Restore while restoring', () => {
    render(<SnapshotRestoreDialog open pendingConflictCount={0} onCancel={() => {}} onConfirm={() => {}} restoring={true} />)
    expect(screen.getByRole('button', { name: /proceed/i })).toBeDisabled()
  })
})
