import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { FileTreeView } from './FileTreeView'
import { useHostStore } from '../stores/useHostStore'

const mockEntries = [
  { name: 'docs', isDir: true, size: 0 },
  { name: 'src', isDir: true, size: 0 },
  { name: 'README.md', isDir: false, size: 1024 },
]

beforeEach(() => {
  vi.restoreAllMocks()
  // MUST set host state — FileTreeView reads baseUrl from useHostStore
  useHostStore.setState({
    hostOrder: ['test-host'],
    hosts: {
      'test-host': {
        id: 'test-host',
        name: 'Test',
        ip: '127.0.0.1',
        port: 7860,
        order: 0,
      },
    },
    activeHostId: 'test-host',
    runtime: {},
  })
})

describe('FileTreeView', () => {
  it('renders file entries after fetch', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ path: '/home/user', entries: mockEntries }),
    } as Response)

    render(<FileTreeView isActive={true} />)

    await waitFor(() => {
      expect(screen.getByText('docs')).toBeTruthy()
      expect(screen.getByText('src')).toBeTruthy()
      expect(screen.getByText('README.md')).toBeTruthy()
    })
  })

  it('shows directories with folder icon (SVG)', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ path: '/home/user', entries: mockEntries }),
    } as Response)

    render(<FileTreeView isActive={true} />)

    await waitFor(() => {
      const docs = screen.getByTestId('file-entry-docs')
      expect(docs.querySelector('svg')).toBeTruthy()
    })
  })

  it('shows error state on fetch failure', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValueOnce(new Error('Network error'))

    render(<FileTreeView isActive={true} />)

    await waitFor(() => {
      expect(screen.getByText(/error/i)).toBeTruthy()
    })
  })

  it('shows no host connected when no host', () => {
    useHostStore.setState({ hostOrder: [], hosts: {}, activeHostId: null, runtime: {} })
    render(<FileTreeView isActive={true} />)
    expect(screen.getByText(/No host connected/)).toBeTruthy()
  })
})
