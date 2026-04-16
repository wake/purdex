import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { FileTreeWorkspaceView } from './FileTreeView'
import { useHostStore } from '../stores/useHostStore'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../features/workspace/store'
import * as FsBackend from '../lib/fs-backend'
import * as FileOpenerRegistry from '../lib/file-opener-registry'

const mockEntries = [
  { name: 'docs', isDir: true, size: 0 },
  { name: 'src', isDir: true, size: 0 },
  { name: 'README.md', isDir: false, size: 1024 },
]

const TEST_WORKSPACE_ID = 'test-ws'

const mockBackend = {
  id: 'daemon',
  label: 'Remote Host',
  available: () => true,
  list: vi.fn(),
  read: vi.fn(),
  write: vi.fn(),
  stat: vi.fn(),
  mkdir: vi.fn(),
  delete: vi.fn(),
  rename: vi.fn(),
}

beforeEach(() => {
  vi.restoreAllMocks()
  mockBackend.list.mockReset()
  // Stub getFsBackend to return mock backend
  vi.spyOn(FsBackend, 'getFsBackend').mockReturnValue(mockBackend)
  // MUST set host state — FileTreeWorkspaceView reads activeHostId from useHostStore
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
  // Set workspace with projectPath configured
  useWorkspaceStore.setState({
    workspaces: [{
      id: TEST_WORKSPACE_ID,
      name: 'Test Workspace',
      tabs: [],
      activeTabId: null,
      moduleConfig: { files: { projectPath: '/home/user' } },
    }],
    activeWorkspaceId: TEST_WORKSPACE_ID,
  })
})

describe('FileTreeWorkspaceView', () => {
  it('renders file entries after fetch', async () => {
    mockBackend.list.mockResolvedValueOnce(mockEntries)

    render(<FileTreeWorkspaceView isActive={true} workspaceId={TEST_WORKSPACE_ID} />)

    await waitFor(() => {
      expect(screen.getByText('docs')).toBeTruthy()
      expect(screen.getByText('src')).toBeTruthy()
      expect(screen.getByText('README.md')).toBeTruthy()
    })
  })

  it('shows directories with folder icon (SVG)', async () => {
    mockBackend.list.mockResolvedValueOnce(mockEntries)

    render(<FileTreeWorkspaceView isActive={true} workspaceId={TEST_WORKSPACE_ID} />)

    await waitFor(() => {
      const docs = screen.getByTestId('file-entry-docs')
      expect(docs.querySelector('svg')).toBeTruthy()
    })
  })

  it('shows error state on fetch failure', async () => {
    mockBackend.list.mockRejectedValueOnce(new Error('Network error'))

    render(<FileTreeWorkspaceView isActive={true} workspaceId={TEST_WORKSPACE_ID} />)

    await waitFor(() => {
      expect(screen.getByText(/error/i)).toBeTruthy()
    })
  })

  it('shows no host connected when no host', () => {
    useHostStore.setState({ hostOrder: [], hosts: {}, activeHostId: null, runtime: {} })
    render(<FileTreeWorkspaceView isActive={true} workspaceId={TEST_WORKSPACE_ID} />)
    expect(screen.getByText(/No host connected/)).toBeTruthy()
  })

  it('shows workspace required message when workspaceId is undefined', () => {
    render(<FileTreeWorkspaceView isActive={true} workspaceId={undefined} />)
    expect(screen.getByText('請先選擇 Workspace')).toBeTruthy()
    expect(screen.queryByPlaceholderText('/home/user/project')).toBeNull()
    expect(mockBackend.list).not.toHaveBeenCalled()
  })

  it('clicking a file opens editor tab via file-opener-registry', async () => {
    mockBackend.list.mockResolvedValueOnce(mockEntries)
    const mockContent = { kind: 'editor' as const, source: { type: 'daemon' as const, hostId: 'test-host' }, filePath: '/home/user/README.md' }
    const mockOpener = {
      id: 'monaco-editor',
      label: 'Text Editor',
      icon: 'File',
      match: () => true,
      priority: 'default' as const,
      createContent: vi.fn().mockReturnValue(mockContent),
    }
    vi.spyOn(FileOpenerRegistry, 'getDefaultOpener').mockReturnValue(mockOpener)
    const openSingletonTab = vi.spyOn(useTabStore.getState(), 'openSingletonTab').mockReturnValue('new-tab-id')
    const insertTab = vi.spyOn(useWorkspaceStore.getState(), 'insertTab').mockImplementation(() => {})

    render(<FileTreeWorkspaceView isActive={true} workspaceId={TEST_WORKSPACE_ID} />)

    await waitFor(() => {
      expect(screen.getByText('README.md')).toBeTruthy()
    })

    fireEvent.click(screen.getByTestId('file-entry-README.md'))

    expect(mockOpener.createContent).toHaveBeenCalledWith(
      { type: 'daemon', hostId: 'test-host' },
      expect.objectContaining({ name: 'README.md', path: '/home/user/README.md', extension: 'md' }),
    )
    expect(openSingletonTab).toHaveBeenCalledWith(mockContent)
    expect(insertTab).toHaveBeenCalledWith('new-tab-id', TEST_WORKSPACE_ID)
  })

  it('shows setup prompt when projectPath is not configured', () => {
    useWorkspaceStore.setState({
      workspaces: [{
        id: TEST_WORKSPACE_ID,
        name: 'Test Workspace',
        tabs: [],
        activeTabId: null,
        moduleConfig: {},
      }],
      activeWorkspaceId: TEST_WORKSPACE_ID,
    })
    render(<FileTreeWorkspaceView isActive={true} workspaceId={TEST_WORKSPACE_ID} />)
    expect(screen.getByText(/設定專案路徑/)).toBeTruthy()
  })
})
