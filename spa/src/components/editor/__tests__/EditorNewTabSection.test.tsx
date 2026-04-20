import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { EditorNewTabSection } from '../EditorNewTabSection'

const createUntitledFile = vi.fn()

vi.mock('../../../lib/fs-backend-inapp', () => ({
  getInAppBackend: () => ({
    createUntitledFile,
  }),
}))

describe('EditorNewTabSection', () => {
  beforeEach(() => {
    createUntitledFile.mockReset()
  })

  it('opens a new in-app editor tab with docId returned by backend', async () => {
    createUntitledFile.mockResolvedValue({ docId: 'doc-1', path: '/untitled.md', version: 1 })
    const onSelect = vi.fn()

    render(<EditorNewTabSection onSelect={onSelect} />)

    fireEvent.click(screen.getByText('New Markdown'))

    await waitFor(() => {
      expect(onSelect).toHaveBeenCalledWith({
        kind: 'editor',
        source: { type: 'inapp' },
        docId: 'doc-1',
        filePath: '/untitled.md',
      })
    })
  })
})
