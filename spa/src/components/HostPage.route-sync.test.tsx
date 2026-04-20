import { act, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { Router } from 'wouter'
import { memoryLocation } from 'wouter/memory-location'
import { useRouteSync } from '../hooks/useRouteSync'
import { getPrimaryPane } from '../lib/pane-tree'
import { useHistoryStore } from '../stores/useHistoryStore'
import { useHostStore } from '../stores/useHostStore'
import { useTabStore } from '../stores/useTabStore'
import type { Pane } from '../types/tab'
import { HostPage, resetLastHostSelection } from './HostPage'

vi.mock('../lib/host-api', async (importOriginal) => {
  const actual = await importOriginal() as Record<string, unknown>
  return { ...actual, listSessions: vi.fn().mockResolvedValue([]) }
})

vi.mock('./hosts/HostSidebar', () => ({
  HostSidebar: (props: {
    selectedHostId: string
    selectedSubPage: string
    onSelect: (hostId: string, subPage: 'overview' | 'sessions' | 'hooks' | 'agents' | 'uploads' | 'logs') => void
  }) => (
    <div data-testid="host-sidebar" data-host={props.selectedHostId} data-subpage={props.selectedSubPage}>
      <button data-testid="select-test-host-logs" onClick={() => props.onSelect('test-host', 'logs')}>
        test-host logs
      </button>
    </div>
  ),
}))
vi.mock('./hosts/OverviewSection', () => ({
  OverviewSection: (props: { hostId: string }) => <div data-testid="overview-section" data-host={props.hostId} />,
}))
vi.mock('./hosts/SessionsSection', () => ({
  SessionsSection: (props: { hostId: string }) => <div data-testid="sessions-section" data-host={props.hostId} />,
}))
vi.mock('./hosts/HooksSection', () => ({
  HooksSection: (props: { hostId: string }) => <div data-testid="hooks-section" data-host={props.hostId} />,
}))
vi.mock('./hosts/AgentsSection', () => ({
  AgentsSection: (props: { hostId: string }) => <div data-testid="agents-section" data-host={props.hostId} />,
}))
vi.mock('./hosts/UploadSection', () => ({
  UploadSection: (props: { hostId: string }) => <div data-testid="upload-section" data-host={props.hostId} />,
}))
vi.mock('./hosts/LogsSection', () => ({
  LogsSection: (props: { hostId: string }) => <div data-testid="logs-section" data-host={props.hostId} />,
}))
vi.mock('./hosts/AddHostDialog', () => ({
  AddHostDialog: (props: { onClose: () => void }) => <div data-testid="add-host-dialog" onClick={props.onClose} />,
}))

const TEST_HOST_ID = 'test-host'
const SECOND_HOST_ID = 'host-b'

const hostPane: Pane = {
  id: 'pane-hosts',
  content: { kind: 'hosts' },
}

function resetTabStore() {
  useTabStore.setState({
    tabs: {},
    tabOrder: [],
    activeTabId: null,
    visitHistory: [],
  })
}

function seedHosts() {
  useHostStore.setState({
    hosts: {
      [TEST_HOST_ID]: { id: TEST_HOST_ID, name: 'Test Host', ip: '1.2.3.4', port: 7860, order: 0 },
      [SECOND_HOST_ID]: { id: SECOND_HOST_ID, name: 'Second Host', ip: '5.6.7.8', port: 7860, order: 1 },
    },
    hostOrder: [TEST_HOST_ID, SECOND_HOST_ID],
    activeHostId: TEST_HOST_ID,
    runtime: {},
  })
}

function RouteSyncHostPage({ pane }: { pane: Pane }) {
  useRouteSync()
  return <HostPage pane={pane} isActive />
}

function renderWithRoute(initialPath: string) {
  const mem = memoryLocation({ path: initialPath, record: true })
  const view = render(
    <Router hook={mem.hook}>
      <RouteSyncHostPage pane={hostPane} />
    </Router>,
  )
  return { ...view, mem }
}

function currentPath(mem: ReturnType<typeof memoryLocation>) {
  return mem.history[mem.history.length - 1]
}

describe('HostPage route sync', () => {
  beforeEach(() => {
    resetTabStore()
    useHistoryStore.setState({ browseHistory: [], closedTabs: [] })
    resetLastHostSelection()
    seedHosts()
  })

  it('canonicalizes bare /hosts to the fallback selection with replace semantics', async () => {
    const { mem } = renderWithRoute('/hosts')

    await waitFor(() => {
      expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-host', TEST_HOST_ID)
      expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-subpage', 'overview')
      expect(currentPath(mem)).toBe('/hosts/test-host/overview')
      expect(mem.history).toEqual(['/hosts/test-host/overview'])
    })
  })

  it('keeps /hosts/:hostId/:subPage on first mount while opening the singleton hosts tab', async () => {
    const { mem } = renderWithRoute('/hosts/test-host/logs')

    expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-host', TEST_HOST_ID)
    expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-subpage', 'logs')
    expect(screen.getByTestId('logs-section')).toHaveAttribute('data-host', TEST_HOST_ID)

    await waitFor(() => {
      const state = useTabStore.getState()
      const tab = state.activeTabId ? state.tabs[state.activeTabId] : null
      const primary = tab ? getPrimaryPane(tab.layout) : null

      expect(primary?.content.kind).toBe('hosts')
      expect(currentPath(mem)).toBe('/hosts/test-host/logs')
    })
  })

  it('canonicalizes invalid deep links with replace semantics', async () => {
    const { mem } = renderWithRoute('/hosts/missing-host/logs')

    await waitFor(() => {
      expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-host', TEST_HOST_ID)
      expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-subpage', 'logs')
      expect(currentPath(mem)).toBe('/hosts/test-host/logs')
      expect(mem.history).toEqual(['/hosts/test-host/logs'])
    })
  })

  it('updates the canonical URL after host deletion fallback', async () => {
    const { rerender, mem } = renderWithRoute('/hosts/test-host/logs')

    act(() => {
      useHostStore.setState({
        hosts: {
          [SECOND_HOST_ID]: { id: SECOND_HOST_ID, name: 'Second Host', ip: '5.6.7.8', port: 7860, order: 0 },
        },
        hostOrder: [SECOND_HOST_ID],
        activeHostId: SECOND_HOST_ID,
        runtime: {},
      })
    })

    rerender(
      <Router hook={mem.hook}>
        <RouteSyncHostPage pane={hostPane} />
      </Router>,
    )

    await waitFor(() => {
      expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-host', SECOND_HOST_ID)
      expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-subpage', 'logs')
      expect(currentPath(mem)).toBe('/hosts/host-b/logs')
      expect(mem.history).toEqual(['/hosts/host-b/logs'])
    })
  })
})
