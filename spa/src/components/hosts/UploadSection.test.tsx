import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { UploadSection } from './UploadSection'
import { useHostStore } from '../../stores/useHostStore'

const HOST_ID = 'test-host'

const STATS = { dir: '/tmp/uploads', total_size: 1024, file_count: 2 }
const FILES = [
  { session: 'abc', name: 'file1.txt', size: 512, modified: '2025-01-01' },
  { session: 'abc', name: 'file2.txt', size: 512, modified: '2025-01-01' },
]

const CONFIG = {
  upload_dir: '/tmp/purdex-upload',
  bind: '0.0.0.0',
  port: 7860,
  stream: { presets: [] },
  detect: { cc_commands: [], poll_interval: 5 },
}

// Mock the host-api module
vi.mock('../../lib/host-api', () => ({
  hostFetch: vi.fn(),
  fetchUploadStats: vi.fn(),
  fetchUploadFiles: vi.fn(),
  deleteUploadFile: vi.fn(),
  deleteUploadSession: vi.fn(),
  deleteAllUploads: vi.fn(),
}))

import {
  hostFetch,
  fetchUploadStats,
  fetchUploadFiles,
  deleteAllUploads,
} from '../../lib/host-api'

const mockHostFetch = vi.mocked(hostFetch)
const mockFetchUploadStats = vi.mocked(fetchUploadStats)
const mockFetchUploadFiles = vi.mocked(fetchUploadFiles)
const mockDeleteAllUploads = vi.mocked(deleteAllUploads)

function mockOkResponse(data: unknown): Response {
  return { ok: true, json: () => Promise.resolve(data) } as Response
}

beforeEach(() => {
  vi.clearAllMocks()
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'Test', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST_ID],
    runtime: { [HOST_ID]: { status: 'connected' } },
  })
  // Default: mock hostFetch for /api/config
  mockHostFetch.mockImplementation((_hostId, path) => {
    if (path === '/api/config') return Promise.resolve(mockOkResponse(CONFIG))
    return Promise.resolve({ ok: false } as Response)
  })
})

describe('UploadSection', () => {
  it('shows loading initially, then stats after fetch', async () => {
    mockFetchUploadStats.mockResolvedValue(mockOkResponse(STATS))
    mockFetchUploadFiles.mockResolvedValue(mockOkResponse(FILES))

    render(<UploadSection hostId={HOST_ID} />)

    await waitFor(() => {
      // Should show config's upload_dir, not stats.dir
      expect(screen.getByText('/tmp/purdex-upload')).toBeInTheDocument()
      expect(screen.getByText('2')).toBeInTheDocument()
    })
  })

  it('renders EditableField with config upload_dir', async () => {
    mockFetchUploadStats.mockResolvedValue(mockOkResponse(STATS))
    mockFetchUploadFiles.mockResolvedValue(mockOkResponse(FILES))

    render(<UploadSection hostId={HOST_ID} />)

    await waitFor(() => {
      expect(screen.getByText('/tmp/purdex-upload')).toBeInTheDocument()
    })

    // Click to edit the dir
    fireEvent.click(screen.getByText('/tmp/purdex-upload'))

    // Should show an input with the current dir value
    const input = screen.getByDisplayValue('/tmp/purdex-upload')
    expect(input).toBeInTheDocument()
  })

  it('saves new upload dir via PUT /api/config', async () => {
    mockFetchUploadStats.mockResolvedValue(mockOkResponse(STATS))
    mockFetchUploadFiles.mockResolvedValue(mockOkResponse(FILES))

    const updatedConfig = { ...CONFIG, upload_dir: '/new/upload/path' }
    mockHostFetch.mockImplementation((_hostId, path, init) => {
      if (path === '/api/config' && init?.method === 'PUT') {
        return Promise.resolve(mockOkResponse(updatedConfig))
      }
      if (path === '/api/config') return Promise.resolve(mockOkResponse(CONFIG))
      return Promise.resolve({ ok: false } as Response)
    })

    render(<UploadSection hostId={HOST_ID} />)

    await waitFor(() => {
      expect(screen.getByText('/tmp/purdex-upload')).toBeInTheDocument()
    })

    // Click to edit
    fireEvent.click(screen.getByText('/tmp/purdex-upload'))

    const input = screen.getByDisplayValue('/tmp/purdex-upload')
    fireEvent.change(input, { target: { value: '/new/upload/path' } })
    fireEvent.keyDown(input, { key: 'Enter' })

    await waitFor(() => {
      expect(mockHostFetch).toHaveBeenCalledWith(
        HOST_ID,
        '/api/config',
        expect.objectContaining({
          method: 'PUT',
          body: JSON.stringify({ upload_dir: '/new/upload/path' }),
        }),
      )
    })
  })

  it('falls back to stats.dir when config has no upload_dir', async () => {
    mockHostFetch.mockImplementation((_hostId, path) => {
      if (path === '/api/config') return Promise.resolve(mockOkResponse({ ...CONFIG, upload_dir: undefined }))
      return Promise.resolve({ ok: false } as Response)
    })
    mockFetchUploadStats.mockResolvedValue(mockOkResponse(STATS))
    mockFetchUploadFiles.mockResolvedValue(mockOkResponse(FILES))

    render(<UploadSection hostId={HOST_ID} />)

    await waitFor(() => {
      expect(screen.getByText('/tmp/uploads')).toBeInTheDocument()
    })
  })

  it('shows "No upload files" when file list is empty', async () => {
    mockFetchUploadStats.mockResolvedValue(mockOkResponse(STATS))
    mockFetchUploadFiles.mockResolvedValue(mockOkResponse([]))

    render(<UploadSection hostId={HOST_ID} />)

    await waitFor(() => {
      expect(screen.getByText('No upload files')).toBeInTheDocument()
    })
  })

  it('renders files grouped by session', async () => {
    mockFetchUploadStats.mockResolvedValue(mockOkResponse(STATS))
    mockFetchUploadFiles.mockResolvedValue(mockOkResponse(FILES))

    render(<UploadSection hostId={HOST_ID} />)

    await waitFor(() => {
      expect(screen.getByText('abc')).toBeInTheDocument()
      expect(screen.getByText('file1.txt')).toBeInTheDocument()
      expect(screen.getByText('file2.txt')).toBeInTheDocument()
    })
  })

  it('Clear All button shows confirmation dialog', async () => {
    mockFetchUploadStats.mockResolvedValue(mockOkResponse(STATS))
    mockFetchUploadFiles.mockResolvedValue(mockOkResponse(FILES))

    render(<UploadSection hostId={HOST_ID} />)

    await waitFor(() => {
      expect(screen.getByText('file1.txt')).toBeInTheDocument()
    })

    // Click Clear All
    fireEvent.click(screen.getByText('Clear All'))

    // Confirmation dialog should appear
    expect(screen.getByText('Are you sure you want to delete all upload files?')).toBeInTheDocument()
  })

  it('Clear All confirmation calls deleteAllUploads', async () => {
    mockFetchUploadStats.mockResolvedValue(mockOkResponse(STATS))
    mockFetchUploadFiles.mockResolvedValue(mockOkResponse(FILES))
    mockDeleteAllUploads.mockResolvedValue(mockOkResponse({}))

    render(<UploadSection hostId={HOST_ID} />)

    await waitFor(() => {
      expect(screen.getByText('file1.txt')).toBeInTheDocument()
    })

    // Click Clear All, then confirm
    fireEvent.click(screen.getByText('Clear All'))

    // The confirmation has two buttons: "Clear All" (confirm) and "Cancel"
    const clearButtons = screen.getAllByText('Clear All')
    // The second "Clear All" is the confirm button inside the dialog
    fireEvent.click(clearButtons[clearButtons.length - 1])

    await waitFor(() => {
      expect(mockDeleteAllUploads).toHaveBeenCalledWith(HOST_ID)
    })
  })

  it('offline host disables buttons', async () => {
    useHostStore.setState({
      runtime: { [HOST_ID]: { status: 'disconnected' } },
    })
    mockFetchUploadStats.mockResolvedValue(mockOkResponse(STATS))
    mockFetchUploadFiles.mockResolvedValue(mockOkResponse(FILES))

    render(<UploadSection hostId={HOST_ID} />)

    await waitFor(() => {
      expect(screen.getByText('file1.txt')).toBeInTheDocument()
    })

    // Refresh button should be disabled
    expect(screen.getByText('Refresh').closest('button')).toBeDisabled()

    // Clear All button should be disabled
    expect(screen.getByText('Clear All').closest('button')).toBeDisabled()
  })

  it('returns null when host does not exist', () => {
    const { container } = render(<UploadSection hostId="nonexistent" />)
    expect(container.innerHTML).toBe('')
  })
})
