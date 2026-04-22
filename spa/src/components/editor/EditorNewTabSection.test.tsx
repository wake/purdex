import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { EditorNewTabSection } from './EditorNewTabSection'
import { useEditorStore } from '../../stores/useEditorStore'
import { useTabStore } from '../../stores/useTabStore'

const getFsBackendMock = vi.hoisted(() => vi.fn())

vi.mock('../../lib/fs-backend', () => ({
  getFsBackend: getFsBackendMock,
}))

describe('EditorNewTabSection', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useEditorStore.getState().clearAllBuffers()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  })

  it('creates an untitled markdown document without writing to backend', async () => {
    const backend = {
      list: vi.fn(async () => []),
      write: vi.fn(),
    }
    const onSelect = vi.fn()
    getFsBackendMock.mockReturnValue(backend)

    render(<EditorNewTabSection onSelect={onSelect} />)

    fireEvent.click(screen.getByRole('button', { name: 'New Markdown' }))

    await waitFor(() => {
      expect(onSelect).toHaveBeenCalledWith({
        kind: 'editor',
        source: { type: 'inapp' },
        filePath: 'untitled:Untitled',
        untitled: {
          name: 'Untitled',
          suggestedExtension: '.md',
          hasBeenRenamed: false,
        },
      })
    })

    expect(backend.write).not.toHaveBeenCalled()
  })

  it('uses the next readable untitled name when a draft with the same template exists', async () => {
    const backend = {
      list: vi.fn(async () => []),
    }
    const onSelect = vi.fn()
    getFsBackendMock.mockReturnValue(backend)

    useTabStore.setState({
      tabs: {
        'tab-1': {
          id: 'tab-1',
          pinned: false,
          locked: false,
          createdAt: 1,
          layout: {
            type: 'leaf',
            pane: {
              id: 'pane-1',
              content: {
                kind: 'editor',
                source: { type: 'inapp' },
                filePath: 'untitled:Untitled',
                untitled: {
                  name: 'Untitled',
                  suggestedExtension: '.txt',
                  hasBeenRenamed: false,
                },
              },
            },
          },
        },
      },
      tabOrder: ['tab-1'],
      activeTabId: 'tab-1',
      visitHistory: [],
    })

    render(<EditorNewTabSection onSelect={onSelect} />)

    fireEvent.click(screen.getByRole('button', { name: 'New File' }))

    await waitFor(() => {
      expect(onSelect).toHaveBeenCalledWith({
        kind: 'editor',
        source: { type: 'inapp' },
        filePath: 'untitled:Untitled-1',
        untitled: {
          name: 'Untitled-1',
          suggestedExtension: '.txt',
          hasBeenRenamed: false,
        },
      })
    })
  })
})
