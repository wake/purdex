import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import en from '../../../locales/en.json'
import { LocalProfilesBlock } from './LocalProfilesBlock'
import { useLocalProfilesStore, type LocalProfile } from '../../../stores/useLocalProfilesStore'
import { __resetProfileSwitcherForTest, useProfileSwitcherStore } from '../../../stores/useProfileSwitcherStore'
import { useDeviceNameStore } from '../../../stores/useDeviceNameStore'
import { useUndoToast } from '../../../stores/useUndoToast'
import { copyMasterAsSlave, createBlankSlave, createSettingsCopySlave, deleteSlave, reorderSlaves, saveScreenAsSlave, switchActiveProfile } from '../../../lib/profile/switch-active'
import { useShownHostsStore } from '../../../stores/useShownHostsStore'
import { useTabStore } from '../../../stores/useTabStore'
import { useWorkspaceStore } from '../../../features/workspace/store'
import type { Tab } from '../../../types/tab'

vi.mock('../../../lib/profile/switch-active', () => ({
  switchActiveProfile: vi.fn(),
  copyMasterAsSlave: vi.fn(),
  saveScreenAsSlave: vi.fn(),
  createBlankSlave: vi.fn(),
  createSettingsCopySlave: vi.fn(),
  deleteSlave: vi.fn(),
  reorderSlaves: vi.fn(),
}))
// The real picker needs a layout engine; it has its own tests. The editor's use of it is pinned in
// ProfileAppearanceEditor.test.tsx.
vi.mock('../../../features/workspace/components/WorkspaceIconPicker', () => ({ WorkspaceIconPicker: () => null }))

const slave = (id: string, name: string, onScreen = false, extra: Partial<LocalProfile> = {}): LocalProfile => ({
  id,
  name,
  createdAt: 1,
  shownHostIds: [],
  world: onScreen ? null : { workspaces: [], tabs: {}, activeWorkspaceId: null, activeTabId: null },
  ...extra,
})

/** `slaveOrder` deliberately differs from the key order of `slaves`. */
function seed(activeProfileId = 'master') {
  useLocalProfilesStore.setState({
    slaves: { s1: slave('s1', 'Scratch', activeProfileId === 's1'), s2: slave('s2', 'Client work', activeProfileId === 's2') },
    slaveOrder: ['s2', 's1'],
    activeProfileId,
    parkedMaster: activeProfileId === 'master' ? null : { workspaces: [], tabs: {}, activeWorkspaceId: null, activeTabId: null },
  })
}

const rowIds = () => screen.getAllByTestId(/^profile-row-(master|s\d)$/).map((el) => el.getAttribute('data-testid'))
const flush = () => act(async () => { await Promise.resolve() })

beforeEach(() => {
  for (const fn of [switchActiveProfile, copyMasterAsSlave, saveScreenAsSlave, createSettingsCopySlave, createBlankSlave, deleteSlave, reorderSlaves]) vi.mocked(fn).mockReset()
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: 'master', parkedMaster: null, worldEpoch: 0, relabelCount: 0, master: { name: null } })
  useDeviceNameStore.setState({ deviceName: 'Mini', defaultDeviceName: 'Browser' })
  __resetProfileSwitcherForTest()
  useUndoToast.setState({ toast: null })
})

afterEach(() => {
  cleanup()
  __resetProfileSwitcherForTest()
})

describe('the list', () => {
  it('no slave: one row, the master, unnamed → Home, tagged, on screen; no slave-only action', () => {
    render(<LocalProfilesBlock />)
    expect(rowIds()).toEqual(['profile-row-master'])
    expect(screen.getByTestId('profile-row-name-master')).toHaveTextContent(en['nav.home'])
    expect(screen.getByTestId('profile-row-master-badge')).toHaveTextContent(en['profile.master'])
    expect(screen.getByTestId('profile-row-on-screen-master')).toBeInTheDocument()
    expect(screen.queryByTestId('profile-row-switch-master')).toBeNull()
    expect(screen.queryByTestId('profile-row-delete-master')).toBeNull()
    expect(screen.queryByTestId('profile-row-up-master')).toBeNull()
    expect(screen.queryByTestId('profile-row-down-master')).toBeNull()
  })

  it('the master first, then the slaves in slaveOrder; a named master shows its name', () => {
    seed()
    useLocalProfilesStore.setState({ master: { name: 'Work' } })
    render(<LocalProfilesBlock />)
    expect(rowIds()).toEqual(['profile-row-master', 'profile-row-s2', 'profile-row-s1'])
    expect(screen.getByTestId('profile-row-name-master')).toHaveTextContent('Work')
    expect(screen.getByTestId('profile-row-name-s2')).toHaveTextContent('Client work')
    expect(within(screen.getByTestId('profile-row-s2')).queryByTestId('profile-row-master-badge')).toBeNull()
  })

  it('only the profile on screen says so, and only the others offer a switch', () => {
    seed('s1')
    render(<LocalProfilesBlock />)
    expect(screen.getByTestId('profile-row-on-screen-s1')).toBeInTheDocument()
    expect(screen.queryByTestId('profile-row-on-screen-master')).toBeNull()
    expect(screen.queryByTestId('profile-row-switch-s1')).toBeNull()
    expect(screen.getByTestId('profile-row-switch-master')).toBeEnabled()
    expect(screen.getByTestId('profile-row-switch-s2')).toBeEnabled()
  })

  it('the icon is the profile\'s own, tinted; the logo without one', () => {
    seed()
    useLocalProfilesStore.setState((s) => ({ slaves: { ...s.slaves, s1: { ...s.slaves.s1, icon: 'Star', color: '#ef4444' } } }))
    render(<LocalProfilesBlock />)
    const icon = within(screen.getByTestId('profile-row-s1')).getByTestId('profile-icon')
    expect(icon).toHaveAttribute('data-icon', 'Star')
    expect(icon).toHaveStyle({ color: '#ef4444' })
    expect(within(screen.getByTestId('profile-row-s2')).queryByTestId('profile-icon')).toBeNull()
  })
})

describe('switching — the switcher store\'s one path', () => {
  it('a click starts the store\'s switch; the row is busy and every other switch waits', async () => {
    seed()
    let answer: (r: { ok: true }) => void = () => {}
    vi.mocked(switchActiveProfile).mockReturnValue(new Promise((resolve) => { answer = resolve }))
    render(<LocalProfilesBlock />)
    fireEvent.click(screen.getByTestId('profile-row-switch-s1'))
    expect(switchActiveProfile).toHaveBeenCalledWith('s1')
    expect(useProfileSwitcherStore.getState().pending?.targetId).toBe('s1')
    expect(screen.getByTestId('profile-row-switch-s1')).toHaveAttribute('aria-busy', 'true')
    expect(screen.getByTestId('profile-row-switch-s1')).toBeDisabled()
    expect(screen.getByTestId('profile-row-switch-s2')).toBeDisabled()
    await act(async () => { answer({ ok: true }) })
    expect(useProfileSwitcherStore.getState().pending).toBeNull()
  })

  it('a refusal is the store\'s toast, not a second message here', async () => {
    seed()
    vi.mocked(switchActiveProfile).mockResolvedValue({ ok: false, reason: 'unsettled' })
    render(<LocalProfilesBlock />)
    fireEvent.click(screen.getByTestId('profile-row-switch-s1'))
    await flush()
    expect(useUndoToast.getState().toast?.message).toBe(en['profile.switch.try_again'])
    expect(screen.queryByTestId('profile-local-status')).toBeNull()
  })
})

describe('deleting a slave', () => {
  it('the one on screen cannot be deleted, and the row says why', () => {
    seed('s1')
    render(<LocalProfilesBlock />)
    expect(screen.getByTestId('profile-row-delete-s1')).toBeDisabled()
    expect(screen.getByTestId('profile-row-delete-blocked-s1')).toHaveTextContent(en['settings.profile.local.delete_blocked'])
    expect(screen.getByTestId('profile-row-delete-s2')).toBeEnabled()
    expect(screen.queryByTestId('profile-row-delete-blocked-s2')).toBeNull()
    fireEvent.click(screen.getByTestId('profile-row-delete-s1'))
    expect(screen.queryByTestId('profile-delete-dialog')).toBeNull()
  })

  it('asks first; Cancel deletes nothing', () => {
    seed()
    render(<LocalProfilesBlock />)
    fireEvent.click(screen.getByTestId('profile-row-delete-s1'))
    expect(screen.getByTestId('profile-delete-dialog')).toHaveTextContent('Scratch')
    expect(deleteSlave).not.toHaveBeenCalled()
    fireEvent.click(screen.getByTestId('profile-delete-cancel'))
    expect(screen.queryByTestId('profile-delete-dialog')).toBeNull()
    expect(deleteSlave).not.toHaveBeenCalled()
  })

  it('Confirm deletes that slave', () => {
    seed()
    vi.mocked(deleteSlave).mockReturnValue({ ok: true })
    render(<LocalProfilesBlock />)
    fireEvent.click(screen.getByTestId('profile-row-delete-s1'))
    fireEvent.click(screen.getByTestId('profile-delete-confirm'))
    expect(deleteSlave).toHaveBeenCalledWith('s1')
    expect(screen.queryByTestId('profile-delete-dialog')).toBeNull()
    expect(screen.queryByTestId('profile-local-status')).toBeNull()
  })

  it.each([
    ['not-found', 'settings.profile.local.error.not_found'],
    ['on-screen', 'settings.profile.local.error.on_screen'],
  ] as const)('%s has its own sentence', (reason, key) => {
    seed()
    vi.mocked(deleteSlave).mockReturnValue({ ok: false, reason })
    render(<LocalProfilesBlock />)
    fireEvent.click(screen.getByTestId('profile-row-delete-s1'))
    fireEvent.click(screen.getByTestId('profile-delete-confirm'))
    expect(screen.getByTestId('profile-local-status')).toHaveTextContent(en[key])
  })
})

describe('the order of the slaves', () => {
  it('up / down hand the new order over; the ends are disabled', () => {
    seed()
    vi.mocked(reorderSlaves).mockImplementation((order) => useLocalProfilesStore.getState().reorderSlaves(order))
    render(<LocalProfilesBlock />)
    expect(screen.getByTestId('profile-row-up-s2')).toBeDisabled()
    expect(screen.getByTestId('profile-row-down-s1')).toBeDisabled()
    fireEvent.click(screen.getByTestId('profile-row-down-s2'))
    expect(reorderSlaves).toHaveBeenCalledWith(['s1', 's2'])
    expect(rowIds()).toEqual(['profile-row-master', 'profile-row-s1', 'profile-row-s2'])
    fireEvent.click(screen.getByTestId('profile-row-up-s2'))
    expect(reorderSlaves).toHaveBeenLastCalledWith(['s2', 's1'])
  })

  it('bad-order has its own sentence', () => {
    seed()
    vi.mocked(reorderSlaves).mockReturnValue({ ok: false, reason: 'bad-order' })
    render(<LocalProfilesBlock />)
    fireEvent.click(screen.getByTestId('profile-row-down-s2'))
    expect(screen.getByTestId('profile-local-status')).toHaveTextContent(en['settings.profile.local.error.bad_order'])
  })
})

describe('the three ways to a new local workbench (per-workbench plan §0.7)', () => {
  type Kind = 'duplicate' | 'settings' | 'blank'
  const KINDS: readonly Kind[] = ['duplicate', 'settings', 'blank']
  const open = (which: Kind) => fireEvent.click(screen.getByTestId(`profile-new-${which}`))
  const nameInput = () => screen.getByTestId('profile-new-name') as HTMLInputElement
  const CREATE: Record<Kind, typeof saveScreenAsSlave> = { duplicate: saveScreenAsSlave, settings: createSettingsCopySlave, blank: createBlankSlave }

  it('three separate buttons, and no fourth (the master\'s copy is the wizard\'s)', () => {
    render(<LocalProfilesBlock />)
    for (const kind of KINDS) expect(screen.getByTestId(`profile-new-${kind}`)).toBeInTheDocument()
    expect(screen.queryByTestId('profile-new-copy')).toBeNull()
    expect(screen.queryByTestId('profile-new-save')).toBeNull()
    expect(within(screen.getByTestId('profile-local-block')).getAllByTestId(/^profile-new-/)).toHaveLength(3)
  })

  it('the block says what a local profile holds, and what it shares with the master', () => {
    render(<LocalProfilesBlock />)
    expect(screen.getByTestId('profile-local-block')).toHaveTextContent(en['settings.profile.local.desc'])
  })

  it('each form says what it is naming — its own hint', () => {
    render(<LocalProfilesBlock />)
    for (const kind of KINDS) {
      open(kind)
      expect(screen.getByTestId('profile-new-form')).toHaveAttribute('data-kind', kind)
      expect(screen.getByTestId('profile-new-form')).toHaveTextContent(en[`settings.profile.local.new_${kind}_hint`])
    }
  })

  it('the name offered is the device name', () => {
    render(<LocalProfilesBlock />)
    open('settings')
    expect(nameInput().value).toBe('Mini')
  })

  it('… and never one a profile already has', () => {
    useLocalProfilesStore.setState({ slaves: { s1: slave('s1', 'Mini'), s2: slave('s2', 'Mini 2') }, slaveOrder: ['s1', 's2'] })
    render(<LocalProfilesBlock />)
    open('blank')
    expect(nameInput().value).toBe('Mini 3')
  })

  it('… the master\'s name counts as taken too', () => {
    useLocalProfilesStore.setState({ master: { name: 'Mini' } })
    render(<LocalProfilesBlock />)
    open('duplicate')
    expect(nameInput().value).toBe('Mini 2')
  })

  it.each(KINDS)('%s: its own create under the name typed, no other; then the form goes', (kind) => {
    vi.mocked(CREATE[kind]).mockReturnValue({ ok: true, id: 'n1' })
    render(<LocalProfilesBlock />)
    open(kind)
    fireEvent.change(nameInput(), { target: { value: 'Experiment' } })
    fireEvent.click(screen.getByTestId('profile-new-create'))
    expect(CREATE[kind]).toHaveBeenCalledWith('Experiment')
    for (const other of KINDS.filter((k) => k !== kind)) expect(CREATE[other]).not.toHaveBeenCalled()
    expect(screen.queryByTestId('profile-new-name')).toBeNull()
  })

  it('Enter submits', () => {
    vi.mocked(createSettingsCopySlave).mockReturnValue({ ok: true, id: 'n1' })
    render(<LocalProfilesBlock />)
    open('settings')
    fireEvent.keyDown(nameInput(), { key: 'Enter' })
    expect(createSettingsCopySlave).toHaveBeenCalledWith('Mini')
  })

  describe('with the real functions behind the buttons', () => {
    const LIST = ['d1_shown']
    const TMUX = { kind: 'tmux-session', hostId: 'h1', sessionCode: 'code01', mode: 'terminal', cachedName: 'dev', tmuxInstance: '111:1000' } as const
    beforeEach(async () => {
      const actual = await vi.importActual<typeof import('../../../lib/profile/switch-active')>('../../../lib/profile/switch-active')
      vi.mocked(saveScreenAsSlave).mockImplementation(actual.saveScreenAsSlave)
      vi.mocked(createSettingsCopySlave).mockImplementation(actual.createSettingsCopySlave)
      vi.mocked(createBlankSlave).mockImplementation(actual.createBlankSlave)
      const tab: Tab = { id: 't1', pinned: false, locked: false, createdAt: 1, layout: { type: 'leaf', pane: { id: 'p1', content: { ...TMUX } } } }
      useTabStore.setState({ tabs: { t1: tab }, tabOrder: ['t1'], activeTabId: 't1', worldId: 'master', worldEpoch: 0 })
      useWorkspaceStore.setState({ workspaces: [{ id: 'w1', name: 'On screen', tabs: ['t1'], activeTabId: 't1', moduleConfig: {} }], activeWorkspaceId: 'w1', worldId: 'master', worldEpoch: 0 })
      useShownHostsStore.setState({ ids: LIST, relabelStamp: 0 })
      useLocalProfilesStore.setState({ master: { name: 'Work', icon: 'Briefcase', color: '#ef4444' } })
    })
    afterEach(() => {
      useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
      useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
      useShownHostsStore.setState({ ids: [], relabelStamp: 0 })
    })
    const created = () => {
      const { slaves, slaveOrder } = useLocalProfilesStore.getState()
      expect(slaveOrder).toHaveLength(1)
      const slave = slaves[slaveOrder[0]]
      if (slave.world === null) throw new Error('not parked')
      return { slave, world: slave.world }
    }
    const createVia = (kind: Kind) => {
      render(<LocalProfilesBlock />)
      open(kind)
      fireEvent.click(screen.getByTestId('profile-new-create'))
      return created()
    }
    const screenDidNotMove = () => {
      expect(useWorkspaceStore.getState().workspaces.map((w) => w.id)).toEqual(['w1'])
      expect(Object.keys(useTabStore.getState().tabs)).toEqual(['t1'])
      expect(useLocalProfilesStore.getState().activeProfileId).toBe('master')
    }

    it('Duplicate all: workspaces and tabs under fresh ids, the tmux binding verbatim, and the shown list', () => {
      const { slave, world } = createVia('duplicate')
      expect(world.workspaces.map((ws) => ws.name)).toEqual(['On screen'])
      const [ws] = world.workspaces
      const tabIds = Object.keys(world.tabs)
      expect(tabIds).toHaveLength(1)
      expect(ws.tabs).toEqual(tabIds)
      expect(ws.id).not.toBe('w1')
      expect(tabIds[0]).not.toBe('t1')
      expect(world.tabs[tabIds[0]].layout).toMatchObject({ type: 'leaf', pane: { content: TMUX } })
      expect(slave.shownHostIds).toEqual(LIST)
      screenDidNotMove()
    })

    it('Duplicate settings only: one empty workspace, no tab — and the shown list', () => {
      const { slave, world } = createVia('settings')
      expect(world.tabs).toEqual({})
      expect(world.workspaces).toHaveLength(1)
      expect(world.workspaces[0]).toMatchObject({ name: 'Workspace 1', tabs: [], activeTabId: null })
      expect(world.workspaces[0].id).not.toBe('w1')
      expect(world.activeTabId).toBeNull()
      expect(slave.shownHostIds).toEqual(LIST)
      screenDidNotMove()
    })

    it('New blank workbench: one empty workspace, and every host hidden', () => {
      const { slave, world } = createVia('blank')
      expect(world.tabs).toEqual({})
      expect(world.workspaces).toHaveLength(1)
      expect(world.workspaces[0]).toMatchObject({ name: 'Workspace 1', tabs: [], activeTabId: null })
      expect(slave.shownHostIds).toEqual([])
      screenDidNotMove()
    })

    it.each(KINDS)('%s copies no name, icon or colour — the name is the one typed', (kind) => {
      const { slave } = createVia(kind)
      expect(slave.name).toBe('Mini')
      expect(slave).not.toHaveProperty('icon')
      expect(slave).not.toHaveProperty('color')
    })

    it.each(KINDS)('%s with a blank name: bad-name, nothing is created, the form stays', (which) => {
      render(<LocalProfilesBlock />)
      open(which)
      fireEvent.change(nameInput(), { target: { value: '   ' } })
      fireEvent.click(screen.getByTestId('profile-new-create'))
      expect(screen.getByTestId('profile-new-error')).toHaveTextContent(en['settings.profile.local.error.bad_name'])
      expect(useLocalProfilesStore.getState().slaveOrder).toEqual([])
      expect(nameInput()).toBeInTheDocument()
    })
  })

  it('nothing on the block copies the master: every button, every form, every create', () => {
    for (const kind of KINDS) vi.mocked(CREATE[kind]).mockReturnValue({ ok: true, id: `n-${kind}` })
    render(<LocalProfilesBlock />)
    for (const which of KINDS) {
      open(which)
      fireEvent.click(screen.getByTestId('profile-new-create'))
    }
    for (const button of within(screen.getByTestId('profile-local-block')).getAllByRole('button')) fireEvent.click(button)
    expect(copyMasterAsSlave).not.toHaveBeenCalled()
  })

  it('Cancel (and Escape) create nothing', () => {
    render(<LocalProfilesBlock />)
    open('duplicate')
    fireEvent.click(screen.getByTestId('profile-new-cancel'))
    expect(screen.queryByTestId('profile-new-name')).toBeNull()
    open('settings')
    fireEvent.keyDown(nameInput(), { key: 'Escape' })
    expect(screen.queryByTestId('profile-new-name')).toBeNull()
    for (const kind of KINDS) expect(CREATE[kind]).not.toHaveBeenCalled()
  })

  it.each([
    ['unsettled', 'settings.profile.local.error.unsettled', 'info'],
    ['bad-name', 'settings.profile.local.error.bad_name', 'error'],
    ['bad-world', 'settings.profile.local.error.bad_world', 'error'],
  ] as const)('%s has its own sentence and the form stays', (reason, key, tone) => {
    vi.mocked(createBlankSlave).mockReturnValue({ ok: false, reason })
    render(<LocalProfilesBlock />)
    open('blank')
    fireEvent.click(screen.getByTestId('profile-new-create'))
    const status = screen.getByTestId('profile-new-error')
    expect(status).toHaveTextContent(en[key])
    expect(status).toHaveAttribute('data-tone', tone)
    expect(screen.getByTestId('profile-new-name')).toBeInTheDocument()
  })

  it('write-failed says what the storage said', () => {
    vi.mocked(createSettingsCopySlave).mockReturnValue({ ok: false, reason: 'write-failed', detail: 'QuotaExceededError' })
    render(<LocalProfilesBlock />)
    open('settings')
    fireEvent.click(screen.getByTestId('profile-new-create'))
    expect(screen.getByTestId('profile-new-error')).toHaveTextContent('QuotaExceededError')
  })
})

describe('editing', () => {
  it('Edit opens the editor of that row only, and closes it again', () => {
    seed()
    render(<LocalProfilesBlock />)
    expect(screen.queryByTestId('profile-edit-master')).toBeNull()
    fireEvent.click(screen.getByTestId('profile-row-edit-master'))
    expect(screen.getByTestId('profile-edit-master')).toBeInTheDocument()
    expect(screen.queryByTestId('profile-edit-s1')).toBeNull()
    fireEvent.click(screen.getByTestId('profile-row-edit-master'))
    expect(screen.queryByTestId('profile-edit-master')).toBeNull()
  })
})
