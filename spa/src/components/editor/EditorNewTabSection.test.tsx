import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { EditorNewTabSection } from './EditorNewTabSection'
import { useEditorStore } from '../../stores/useEditorStore'
import { useTabStore } from '../../stores/useTabStore'
import { useRecentFilesStore, type RecentFileEntry } from '../../stores/useRecentFilesStore'
import { usePlaceholderFilesStore } from '../../stores/usePlaceholderFilesStore'
import { useHostStore } from '../../stores/useHostStore'
import * as openMod from '../../lib/recent-files/open-recent-entry'

const createUniqueInAppFileMock = vi.hoisted(() => vi.fn())

vi.mock('../../lib/inapp-namer', () => ({
  createUniqueInAppFile: createUniqueInAppFileMock,
}))

describe('EditorNewTabSection (eager reserved files — T1b-2)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useEditorStore.getState().clearAllBuffers()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    usePlaceholderFilesStore.setState({ paths: [] })
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

  // T5.1 — reservation site 1/3: what this button mints is a file the user has
  // not touched yet, and the registry is the only durable record of that fact.
  it('registers the reserved path in the placeholder registry', async () => {
    createUniqueInAppFileMock.mockResolvedValue('/buffer/Untitled.md')
    render(<EditorNewTabSection onSelect={vi.fn()} />)
    fireEvent.click(screen.getByRole('button', { name: 'New Markdown' }))

    await waitFor(() => {
      expect(
        usePlaceholderFilesStore.getState().isPlaceholder({ type: 'inapp' }, '/buffer/Untitled.md'),
      ).toBe(true)
    })
  })

  it('registers nothing when the reservation fails', async () => {
    createUniqueInAppFileMock.mockRejectedValue(new Error('InApp backend unavailable'))
    render(<EditorNewTabSection onSelect={vi.fn()} />)
    fireEvent.click(screen.getByRole('button', { name: 'New Markdown' }))

    await waitFor(() => expect(createUniqueInAppFileMock).toHaveBeenCalled())
    expect(usePlaceholderFilesStore.getState().paths).toEqual([])
  })
})

const seed = (files: RecentFileEntry[]) => useRecentFilesStore.setState({ files })

const mk = (over: Partial<RecentFileEntry>): RecentFileEntry => ({
  source: { type: 'inapp' }, path: '/a.md', name: 'a.md', kind: 'editor', openedAt: 1, ...over,
})

describe('EditorNewTabSection — recent list', () => {
  beforeEach(() => {
    seed([])
    useHostStore.setState({ hosts: {}, hostOrder: [] })
  })

  it('hides the recent section when empty', () => {
    render(<EditorNewTabSection onSelect={() => {}} />)
    expect(screen.queryByText('Recently opened')).toBeNull()
  })

  it('renders rows and the fixed chips when non-empty', () => {
    seed([mk({ path: '/x.md', name: 'x.md', kind: 'editor' })])
    render(<EditorNewTabSection onSelect={() => {}} />)
    expect(screen.getByText('Recently opened')).toBeInTheDocument()
    expect(screen.getByText('All')).toBeInTheDocument()
    expect(screen.getByText('x.md')).toBeInTheDocument()
  })

  it('filters rows by kind chip', () => {
    seed([
      mk({ path: '/t.md', name: 't.md', kind: 'editor' }),
      mk({ path: '/i.png', name: 'i.png', kind: 'image-preview' }),
    ])
    render(<EditorNewTabSection onSelect={() => {}} />)
    fireEvent.click(screen.getByText('Image'))
    expect(screen.queryByText('t.md')).toBeNull()
    expect(screen.getByText('i.png')).toBeInTheDocument()
  })

  it('shows host badge only for daemon rows', () => {
    useHostStore.setState({
      hosts: { h1: { id: 'h1', name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0 } },
      hostOrder: ['h1'],
    })
    seed([mk({ source: { type: 'daemon', hostId: 'h1' }, path: '/d.md', name: 'd.md' })])
    render(<EditorNewTabSection onSelect={() => {}} />)
    expect(screen.getByTestId('recent-host-badge')).toBeInTheDocument()
  })

  it('row click calls openRecentEntry with the entry + onSelect', () => {
    const spy = vi.spyOn(openMod, 'openRecentEntry').mockResolvedValue()
    const onSelect = vi.fn()
    const entry = mk({ path: '/x.md', name: 'x.md' })
    seed([entry])
    render(<EditorNewTabSection onSelect={onSelect} />)
    fireEvent.click(screen.getByText('x.md'))
    expect(spy).toHaveBeenCalledWith(expect.objectContaining({ path: '/x.md' }), onSelect)
  })
})
