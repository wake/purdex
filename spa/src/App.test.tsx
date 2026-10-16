// spa/src/App.test.tsx — the app shell's chrome. Purdex ships only as the Mac App (Electron) and the iOS App, so the
// title bar is part of every window: it is rendered whether or not `window.electronAPI` exists (browser convergence,
// batch 3). Everything App composes is stubbed except the stores and the ErrorBoundary; the TitleBar stub only marks
// that App rendered it (`TitleBar.test.tsx` covers the bar itself).
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import App from './App'
import { useConfigStore } from './stores/useConfigStore'
import { useWorkspaceStore } from './stores/useWorkspaceStore'
import { useI18nStore } from './stores/useI18nStore'
import { WATCH_WORKSPACE_ID } from './features/workspace/store'

const activityBarProps = vi.hoisted(() => vi.fn())

vi.mock('./features/workspace/lib/icon-path-cache', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./features/workspace/lib/icon-path-cache')>()),
  prefetchWeight: vi.fn(() => Promise.resolve()),
}))
vi.mock('./components/TitleBar', () => ({
  TitleBar: ({ title }: { title: string }) => <div data-testid="app-title-bar">{title}</div>,
}))
vi.mock('./components/ActivityBar', () => ({ ActivityBar: (props: unknown) => { activityBarProps(props); return null } }))
vi.mock('./components/TabBar', () => ({ TabBar: () => null }))
vi.mock('./components/TabContent', () => ({ TabContent: () => null }))
vi.mock('./components/StatusBar', () => ({ StatusBar: () => null }))
vi.mock('./components/TabContextMenu', () => ({ TabContextMenu: () => null }))
vi.mock('./components/RenamePopover', () => ({ RenamePopover: () => null }))
vi.mock('./components/HandoffDialogHost', () => ({ HandoffDialogHost: () => null }))
vi.mock('./components/ApprovalDialogHost', () => ({ ApprovalDialogHost: () => null }))
vi.mock('./components/ThemeInjector', () => ({ ThemeInjector: () => null }))
vi.mock('./components/GlobalUndoToast', () => ({ GlobalUndoToast: () => null }))
vi.mock('./features/workspace', () => ({
  getVisibleTabIds: () => [],
  nextWorkspaceName: () => 'Workspace',
  WorkspaceContextMenu: () => null,
  WorkspaceEmptyState: () => null,
}))
vi.mock('./lib/browser-shortcuts', () => ({}))
const stopLifecycle = vi.hoisted(() => vi.fn())
const startLifecycle = vi.hoisted(() => vi.fn())
vi.mock('./lib/team/team-tab-lifecycle', () => ({ startTeamTabLifecycle: startLifecycle }))
vi.mock('./hooks/useMultiHostEventWs', () => ({ useMultiHostEventWs: vi.fn() }))
vi.mock('./hooks/useRouteSync', () => ({ useRouteSync: vi.fn() }))
vi.mock('./hooks/useShortcuts', () => ({ useShortcuts: vi.fn() }))
vi.mock('./hooks/useNotificationDispatcher', () => ({ useNotificationDispatcher: vi.fn() }))
vi.mock('./hooks/useWorkerAgentProjection', () => ({ useWorkerAgentProjection: vi.fn() }))
vi.mock('./hooks/useDeeplinkResolver', () => ({ useDeeplinkResolver: vi.fn() }))
vi.mock('./hooks/useElectronIpc', () => ({ useElectronIpc: vi.fn() }))
vi.mock('./hooks/useNewTabBootstrap', () => ({ useNewTabBootstrap: vi.fn() }))
vi.mock('./hooks/useWorkspaceWindowActions', () => ({
  useWorkspaceWindowActions: () => ({ handleWsTearOff: vi.fn(), handleWsMergeTo: vi.fn() }),
}))
vi.mock('./features/workspace/hooks', () => ({
  useTabWorkspaceActions: () => ({
    contextMenu: null,
    setContextMenu: vi.fn(),
    renameTarget: null,
    openRenameForTab: vi.fn(),
    openSingletonAndSelect: vi.fn(),
    handleSelectWorkspace: vi.fn(),
  }),
}))

const setElectronApi = (present: boolean) => {
  if (present) (window as unknown as Record<string, unknown>).electronAPI = {}
  else delete (window as unknown as Record<string, unknown>).electronAPI
}

beforeEach(() => {
  // The default host would otherwise make App fetch its config over the network.
  useConfigStore.setState({ fetch: vi.fn(() => Promise.resolve()) })
})

afterEach(() => {
  setElectronApi(false)
})

describe('App — team tab lifecycle', () => {
  it('starts the subscriber once on mount and stops it on unmount', () => {
    startLifecycle.mockReset()
    stopLifecycle.mockReset()
    startLifecycle.mockReturnValue(stopLifecycle)
    const { rerender, unmount } = render(<App />)
    rerender(<App />)
    expect(startLifecycle).toHaveBeenCalledTimes(1)
    expect(stopLifecycle).not.toHaveBeenCalled()
    unmount()
    expect(stopLifecycle).toHaveBeenCalledTimes(1)
  })
})

describe('App — title bar', () => {
  it('renders the title bar with no window.electronAPI (a plain browser run)', () => {
    setElectronApi(false)
    render(<App />)
    expect(screen.getByTestId('app-title-bar')).toBeInTheDocument()
  })

  it('renders the title bar under Electron', () => {
    setElectronApi(true)
    render(<App />)
    expect(screen.getByTestId('app-title-bar')).toBeInTheDocument()
  })
})

describe('App — the watcher area is not a workspace the UI lists', () => {
  beforeEach(() => {
    activityBarProps.mockClear()
    useWorkspaceStore.getState().reset()
  })

  it('the bars get the workspaces without the area, and the title says 守門員專區 while it is shown', () => {
    const a = useWorkspaceStore.getState().addWorkspace('Alpha')
    useWorkspaceStore.getState().ensureWatchWorkspace()
    render(<App />)
    const last = activityBarProps.mock.calls.at(-1)![0] as { workspaces: { id: string }[] }
    expect(last.workspaces.map((w) => w.id)).toEqual([a.id])
    expect(screen.getByTestId('app-title-bar')).toHaveTextContent('Alpha')

    useWorkspaceStore.getState().setActiveWorkspace(WATCH_WORKSPACE_ID)
    activityBarProps.mockClear()
    render(<App />)
    expect(screen.getAllByTestId('app-title-bar').at(-1)).toHaveTextContent(useI18nStore.getState().t('watch.area.name'))
    const again = activityBarProps.mock.calls.at(-1)![0] as { workspaces: { id: string }[] }
    expect(again.workspaces.map((w) => w.id)).toEqual([a.id])
  })
})
