import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { EditorNewTabSection } from './EditorNewTabSection'
import { useEditorStore } from '../../stores/useEditorStore'
import { useTabStore } from '../../stores/useTabStore'

const createUniqueInAppFileMock = vi.hoisted(() => vi.fn())

vi.mock('../../lib/inapp-namer', () => ({
  createUniqueInAppFile: createUniqueInAppFileMock,
}))

describe('EditorNewTabSection (eager reserved files — T1b-2)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useEditorStore.getState().clearAllBuffers()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  })

  // T2-5: New Markdown reserves a REAL path (not an `untitled:` virtual path).
  it('New Markdown reserves a real /buffer path and opens it (no untitled:)', async () => {
    createUniqueInAppFileMock.mockResolvedValue('/buffer/Untitled.md')
    const onSelect = vi.fn()

    render(<EditorNewTabSection onSelect={onSelect} />)
    fireEvent.click(screen.getByRole('button', { name: 'New Markdown' }))

    await waitFor(() => {
      expect(onSelect).toHaveBeenCalledWith({
        kind: 'editor',
        source: { type: 'inapp' },
        filePath: '/buffer/Untitled.md',
      })
    })
    expect(createUniqueInAppFileMock).toHaveBeenCalledWith('/buffer', 'md')
    const content = onSelect.mock.calls[0][0]
    expect(content.filePath.startsWith('untitled:')).toBe(false)
    expect(content.untitled).toBeUndefined()
  })

  // T2-5: New File maps the button label to the bare `txt` ext.
  it('New File reserves a real .txt path via the bare txt extension', async () => {
    createUniqueInAppFileMock.mockResolvedValue('/buffer/Untitled.txt')
    const onSelect = vi.fn()

    render(<EditorNewTabSection onSelect={onSelect} />)
    fireEvent.click(screen.getByRole('button', { name: 'New File' }))

    await waitFor(() => {
      expect(onSelect).toHaveBeenCalledWith({
        kind: 'editor',
        source: { type: 'inapp' },
        filePath: '/buffer/Untitled.txt',
      })
    })
    expect(createUniqueInAppFileMock).toHaveBeenCalledWith('/buffer', 'txt')
  })

  // T2-5: rapid double-click → two distinct reserved paths, never a shared key.
  it('rapid double new-file gets distinct reserved paths (no shared key)', async () => {
    createUniqueInAppFileMock
      .mockResolvedValueOnce('/buffer/Untitled.md')
      .mockResolvedValueOnce('/buffer/Untitled-1.md')
    const onSelect = vi.fn()

    render(<EditorNewTabSection onSelect={onSelect} />)
    const btn = screen.getByRole('button', { name: 'New Markdown' })
    fireEvent.click(btn)
    fireEvent.click(btn)

    await waitFor(() => {
      expect(onSelect).toHaveBeenCalledTimes(2)
    })
    const paths = onSelect.mock.calls.map((c) => c[0].filePath)
    expect(new Set(paths).size).toBe(2)
  })

  it('does not call onSelect when the namer fails (backend unavailable)', async () => {
    createUniqueInAppFileMock.mockRejectedValue(new Error('InApp backend unavailable'))
    const onSelect = vi.fn()

    render(<EditorNewTabSection onSelect={onSelect} />)
    fireEvent.click(screen.getByRole('button', { name: 'New Markdown' }))

    await waitFor(() => {
      expect(createUniqueInAppFileMock).toHaveBeenCalled()
    })
    expect(onSelect).not.toHaveBeenCalled()
  })
})
