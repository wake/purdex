// spa/src/features/workspace/components/WorkspaceConflict.test.tsx — a workspace row's sync-conflict icon and panel
// (sidebar conflict icons spec §5 + the plan's review amendments), over the REAL sync snapshot (`setLocalSnapshot`)
// and the REAL master world. The counts helper and `requestResolve` are replaced as in ResolveBlock.test.tsx; what
// happens inside the row is ResolveBlock.test.tsx's — here only that it is the same row, and where it sits.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { DndContext } from '@dnd-kit/core'
import { SortableContext } from '@dnd-kit/sortable'
import en from '../../../locales/en.json'
import { requestResolve } from '../../../lib/profile/start'
import { __resetSyncStatusForTest, setLocalSnapshot } from '../../../lib/profile/sync-status'
import type { ExecutorStatus, SectionLock } from '../../../lib/profile/executor'
import { useHostStore } from '../../../stores/useHostStore'
import { useLayoutStore } from '../../../stores/useLayoutStore'
import { MASTER_PROFILE_ID, useLocalProfilesStore } from '../../../stores/useLocalProfilesStore'
import { useProfileStore } from '../../../stores/useProfileStore'
import { useTabStore } from '../../../stores/useTabStore'
import { useConflictPanelStore } from '../../../stores/useConflictPanelStore'
import { useWorkspaceStore } from '../store'
import type { Workspace } from '../../../types/tab'
import { readHostSide, readLocalSide } from '../../../components/settings/profile/resolve-counts'
import { WorkspaceRow } from './WorkspaceRow'

vi.mock('../../../lib/profile/start', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../../lib/profile/start')>()),
  requestResolve: vi.fn(),
}))
vi.mock('../../../components/settings/profile/resolve-counts', () => ({ readLocalSide: vi.fn(), readHostSide: vi.fn() }))

/** What the header's drag listeners receive: a pointer-down that reaches them could start a workspace drag. */
const headerPointerDown = vi.fn()
vi.mock('@dnd-kit/sortable', async (importOriginal) => {
  const real = await importOriginal<typeof import('@dnd-kit/sortable')>()
  return {
    ...real,
    useSortable: (args: Parameters<typeof real.useSortable>[0]) => {
      const r = real.useSortable(args)
      return { ...r, listeners: { ...r.listeners, onPointerDown: headerPointerDown } }
    },
  }
})

const MASTER = { hostId: 'h1', profileId: 'p_0123456789ab' }
const ENDPOINT = '10.0.0.1:7860'
const TAG = 'h1|p_0123456789ab|1'
const H = (c: string) => c.repeat(64)
const CONFLICT: SectionLock = { status: 'locked:conflict', currentHash: H('a'), sot: { rev: 7, hash: H('b') }, conflict: { localHash: H('a'), sot: { rev: 7, hash: H('b') } } }

const W1: Workspace = { id: 'w1', name: 'Alpha', tabs: [], activeTabId: null }
const W2: Workspace = { id: 'w2', name: 'Beta', tabs: [], activeTabId: null }

function statusOf(locks: Record<string, SectionLock>): ExecutorStatus {
  const sections = Object.fromEntries(Object.entries(locks).map(([k, l]) => [k, l.status]))
  return {
    profile: Object.keys(locks).length > 0 ? 'locked:conflict' : 'synced',
    schemaLock: null,
    sections,
    locks,
    profileGone: false,
    detail: Object.fromEntries(Object.keys(sections).map((k) => [k, { rev: 1, failures: 0, retryAt: null, invalidReason: null }])),
    indexFailures: 0,
    lastSuccessAt: null,
  }
}

const setSync = (locks: Record<string, SectionLock>, over: { blocked?: 'suspended' | null } = {}) =>
  act(() => setLocalSnapshot({ master: MASTER, leader: true, blocked: over.blocked ?? null, status: statusOf(locks), problems: [] }))

function putMasterOnScreen(): void {
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0 })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useWorkspaceStore.setState({ workspaces: [W1, W2], activeWorkspaceId: 'w1', worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
}

/** A slave on screen that shares the master's workspace ids (a demoted master keeps them). */
function putSlaveOnScreen(): void {
  useLocalProfilesStore.setState({
    slaves: { s1: { id: 's1', name: 'Slave', createdAt: 1, shownHostIds: [], world: null } },
    slaveOrder: ['s1'],
    activeProfileId: 's1',
    parkedMaster: { workspaces: [W1, W2], tabs: {}, activeWorkspaceId: 'w1', activeTabId: null },
    worldEpoch: 1,
  })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, worldId: 's1', worldEpoch: 1 })
  useWorkspaceStore.setState({ workspaces: [W1, W2], activeWorkspaceId: 'w1', worldId: 's1', worldEpoch: 1 })
}

function renderRows(onSelectWorkspace = vi.fn()) {
  const row = (ws: Workspace) => (
    <WorkspaceRow
      key={ws.id}
      workspace={ws}
      isActive={false}
      tabsById={{}}
      activeTabId={null}
      onSelectWorkspace={onSelectWorkspace}
      onSelectTab={() => {}}
      onCloseTab={() => {}}
      onMiddleClickTab={() => {}}
      onContextMenuTab={() => {}}
      onAddTabToWorkspace={() => {}}
    />
  )
  const utils = render(
    <DndContext>
      <SortableContext items={['w1', 'w2']}>
        {row(W1)}
        {row(W2)}
      </SortableContext>
    </DndContext>,
  )
  return { ...utils, onSelectWorkspace }
}

const icon = (id: string) => screen.queryByTestId(`ws-conflict-button-${id}`)
const panel = (id: string) => screen.queryByTestId(`ws-conflict-panel-${id}`)

beforeEach(() => {
  __resetSyncStatusForTest()
  useLayoutStore.setState({ ...useLayoutStore.getInitialState(), tabPosition: 'left', activityBarWidth: 'wide' })
  useProfileStore.setState({ masterHostId: MASTER.hostId, masterProfileId: MASTER.profileId, masterEndpoint: ENDPOINT, attachGeneration: 1, pendingDirection: null, suspension: null, autoSync: true })
  useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'mlab', ip: '10.0.0.1', port: 7860, order: 0 } }, hostOrder: ['h1'] })
  useConflictPanelStore.setState({ openWsId: null })
  putMasterOnScreen()
  headerPointerDown.mockReset()
  vi.mocked(requestResolve).mockReset()
  vi.mocked(requestResolve).mockReturnValue(true)
  vi.mocked(readLocalSide).mockReset()
  vi.mocked(readHostSide).mockReset()
  vi.mocked(readLocalSide).mockImplementation(() => new Promise(() => {}))
  vi.mocked(readHostSide).mockImplementation(() => new Promise(() => {}))
})

afterEach(() => {
  cleanup()
  __resetSyncStatusForTest()
})

describe('the icon', () => {
  it('a lock on this workspace\'s tabs → an icon on its row only, always visible, labelled with its name', () => {
    setSync({ 'tabs.w1': CONFLICT })
    renderRows()
    const button = icon('w1')!
    expect(button).toBeInTheDocument()
    expect(button).toHaveAttribute('aria-label', en['profile.conflict.workspace_button'].replace('{{name}}', 'Alpha'))
    expect(button.className).not.toMatch(/opacity-0/)
    expect(screen.getByTestId('ws-header-w1').contains(button)).toBe(true)
    expect(icon('w2')).toBeNull()
  })

  it('no master, no lock, a lock on another section → no icon', () => {
    renderRows()
    expect(icon('w1')).toBeNull()
    setSync({ workspaces: CONFLICT })
    expect(icon('w1')).toBeNull()
  })

  it('a slave on screen → no icon on any row, even with ids the master shares', () => {
    setSync({ 'tabs.w1': CONFLICT })
    putSlaveOnScreen()
    renderRows()
    expect(icon('w1')).toBeNull()
  })

  it('the lock lifted → the icon goes', () => {
    setSync({ 'tabs.w1': CONFLICT })
    renderRows()
    expect(icon('w1')).toBeInTheDocument()
    setSync({})
    expect(icon('w1')).toBeNull()
  })

  it('a click does not select the workspace, and the button does not block the header\'s pointer-down (drag)', () => {
    setSync({ 'tabs.w1': CONFLICT })
    const { onSelectWorkspace } = renderRows()
    const evt = new Event('pointerdown', { bubbles: true, cancelable: true })
    const stop = vi.spyOn(evt, 'stopPropagation')
    icon('w1')!.dispatchEvent(evt)
    expect(stop).not.toHaveBeenCalled()
    fireEvent.click(icon('w1')!)
    expect(onSelectWorkspace).not.toHaveBeenCalled()
  })
})

describe('the panel', () => {
  it('a click opens it with the Settings page\'s own Resolve row for this workspace\'s tabs; a second click closes it', () => {
    setSync({ 'tabs.w1': CONFLICT })
    renderRows()
    expect(panel('w1')).toBeNull()
    fireEvent.click(icon('w1')!)
    const p = panel('w1')!
    expect(p).toHaveAttribute('aria-label', en['profile.conflict.workspace_title'].replace('{{name}}', 'Alpha'))
    const row = within(p).getByTestId('profile-resolve-row-tabs.w1')
    expect(row).toHaveAttribute('data-lock', 'locked:conflict')
    expect(row).toHaveTextContent(en['settings.profile.current.label.tabs'].replace('{{workspace}}', 'Alpha'))
    expect(useConflictPanelStore.getState().openWsId).toBe('w1')
    fireEvent.click(icon('w1')!)
    expect(panel('w1')).toBeNull()
  })

  it('Keep this device\'s → confirm hands requestResolve the lock that was on screen, under the master\'s tag', () => {
    setSync({ 'tabs.w1': CONFLICT })
    renderRows()
    fireEvent.click(icon('w1')!)
    fireEvent.click(screen.getByTestId('profile-resolve-keep-local-tabs.w1'))
    fireEvent.click(screen.getByTestId('profile-resolve-confirm'))
    expect(requestResolve).toHaveBeenCalledWith('tabs.w1', 'local', CONFLICT, TAG)
    expect(screen.getByTestId('profile-resolve-sent-tabs.w1')).toHaveAttribute('data-state', 'sent')
  })

  it('while no driver runs (blocked) the buttons are disabled', () => {
    setSync({ 'tabs.w1': CONFLICT }, { blocked: 'suspended' })
    renderRows()
    fireEvent.click(icon('w1')!)
    expect(screen.getByTestId('profile-resolve-keep-local-tabs.w1')).toBeDisabled()
  })

  it('the lock lifted (resolved) → icon and panel go, and the store entry is closed', () => {
    setSync({ 'tabs.w1': CONFLICT })
    renderRows()
    fireEvent.click(icon('w1')!)
    setSync({})
    expect(panel('w1')).toBeNull()
    expect(icon('w1')).toBeNull()
    expect(useConflictPanelStore.getState().openWsId).toBeNull()
  })

  it('opened from elsewhere (the Home popover: openFor) → it opens, and the row is scrolled into view', () => {
    setSync({ 'tabs.w1': CONFLICT })
    renderRows()
    const scroll = vi.fn()
    icon('w1')!.scrollIntoView = scroll
    act(() => useConflictPanelStore.getState().openFor('w1'))
    expect(panel('w1')).toBeInTheDocument()
    expect(scroll).toHaveBeenCalledWith({ block: 'nearest' })
  })

  it('a slave put on screen → the panel goes and its entry is closed', () => {
    setSync({ 'tabs.w1': CONFLICT })
    renderRows()
    fireEvent.click(icon('w1')!)
    act(() => putSlaveOnScreen())
    expect(panel('w1')).toBeNull()
    expect(useConflictPanelStore.getState().openWsId).toBeNull()
  })

  it('merely unsettled (a window catching up) → icon and panel hidden, but the entry is kept: it comes back', () => {
    setSync({ 'tabs.w1': CONFLICT })
    renderRows()
    fireEvent.click(icon('w1')!)
    act(() => useTabStore.setState({ worldEpoch: 5 }))
    expect(panel('w1')).toBeNull()
    expect(icon('w1')).toBeNull()
    expect(useConflictPanelStore.getState().openWsId).toBe('w1')
    act(() => useTabStore.setState({ worldEpoch: 0 }))
    expect(panel('w1')).toBeInTheDocument()
  })

  it('the row unmounting closes its own entry — and never another row\'s', () => {
    setSync({ 'tabs.w1': CONFLICT })
    const { unmount } = renderRows()
    fireEvent.click(icon('w1')!)
    unmount()
    expect(useConflictPanelStore.getState().openWsId).toBeNull()
    act(() => useConflictPanelStore.getState().openFor('w9'))
    renderRows().unmount()
    expect(useConflictPanelStore.getState().openWsId).toBe('w9')
  })

  it('lives OUTSIDE the header: a pointer-down on its title bar (a panel drag) never reaches the header\'s drag listeners', () => {
    setSync({ 'tabs.w1': CONFLICT })
    renderRows()
    fireEvent.click(icon('w1')!)
    fireEvent.pointerDown(within(panel('w1')!).getByTestId('floating-panel-handle'), { button: 0, pointerId: 1 })
    expect(headerPointerDown).not.toHaveBeenCalled()
    // …while one on the header itself does (the spy is wired)
    fireEvent.pointerDown(screen.getByTestId('ws-header-w1'))
    expect(headerPointerDown).toHaveBeenCalled()
  })

  it('Escape in the confirmation closes the confirmation only; a second Escape closes the panel', () => {
    setSync({ 'tabs.w1': CONFLICT })
    renderRows()
    fireEvent.click(icon('w1')!)
    fireEvent.click(screen.getByTestId('profile-resolve-keep-local-tabs.w1'))
    fireEvent.keyDown(screen.getByTestId('profile-resolve-confirm'), { key: 'Escape' })
    expect(screen.queryByTestId('profile-resolve-dialog')).toBeNull()
    expect(panel('w1')).toBeInTheDocument()
    fireEvent.keyDown(document.body, { key: 'Escape' })
    expect(panel('w1')).toBeNull()
  })
})
