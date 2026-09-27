// spa/src/features/workspace/components/ProfileConflict.test.tsx — the Home row's sync-conflict icon and its popover
// (sidebar conflict icons spec §3, D1 / D2; the plan's review amendments M2 / M4 / M5), rendered in the REAL HomeRow
// over the REAL sync snapshot (`setLocalSnapshot`) and the REAL master world. The popover is information only: every
// way out leads somewhere else — Settings › Profile, the workspace row's panel, or back to the master.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import en from '../../../locales/en.json'
import { switchActiveProfile } from '../../../lib/profile/switch-active'
import { __resetSyncStatusForTest, setLocalSnapshot } from '../../../lib/profile/sync-status'
import type { ExecutorStatus, SectionLock } from '../../../lib/profile/executor'
import type { InvalidReason } from '../../../lib/profile/apply-to-stores'
import { MASTER_PROFILE_ID, useLocalProfilesStore } from '../../../stores/useLocalProfilesStore'
import { __resetProfileSwitcherForTest, useProfileSwitcherStore } from '../../../stores/useProfileSwitcherStore'
import { useTabStore } from '../../../stores/useTabStore'
import { useConflictPanelStore } from '../../../stores/useConflictPanelStore'
import { useWorkspaceStore } from '../store'
import type { Workspace } from '../../../types/tab'
import { HomeRow } from './HomeRow'

vi.mock('../../../lib/profile/switch-active', () => ({ switchActiveProfile: vi.fn(() => new Promise(() => {})) }))
const navigate = vi.fn()
vi.mock('wouter', async (importOriginal) => ({
  ...(await importOriginal<typeof import('wouter')>()),
  useLocation: () => ['/', navigate],
}))

const H = (c: string) => c.repeat(64)
const CONFLICT: SectionLock = { status: 'locked:conflict', currentHash: H('a'), sot: { rev: 7, hash: H('b') }, conflict: { localHash: H('a'), sot: { rev: 7, hash: H('b') } } }
const RESET: SectionLock = { status: 'locked:reset', currentHash: H('c'), sot: { rev: 2, hash: H('d') }, conflict: null }
const INVALID: SectionLock = { status: 'locked:invalid', currentHash: H('e'), sot: { rev: 9, hash: H('f') }, conflict: null }

const W1: Workspace = { id: 'w1', name: 'Alpha', tabs: [], activeTabId: null }
const W2: Workspace = { id: 'w2', name: 'Beta', tabs: [], activeTabId: null }
const SLAVE_WS: Workspace = { id: 'sw', name: 'Scratchpad', tabs: [], activeTabId: null }

function statusOf(locks: Record<string, SectionLock>, reasons: Record<string, InvalidReason> = {}): ExecutorStatus {
  const sections = Object.fromEntries(Object.entries(locks).map(([k, l]) => [k, l.status]))
  return {
    profile: Object.keys(locks).length > 0 ? 'locked:conflict' : 'synced',
    schemaLock: null,
    sections,
    locks,
    profileGone: false,
    detail: Object.fromEntries(Object.keys(sections).map((k) => [k, { rev: 1, failures: 0, retryAt: null, invalidReason: reasons[k] ?? null }])),
    indexFailures: 0,
    lastSuccessAt: null,
  }
}

const setSync = (locks: Record<string, SectionLock>, reasons: Record<string, InvalidReason> = {}, over: { profileGone?: boolean; master?: null } = {}) =>
  act(() =>
    setLocalSnapshot({
      master: over.master === null ? null : { hostId: 'h1', profileId: 'p1' },
      leader: true,
      blocked: null,
      status: { ...statusOf(locks, reasons), profileGone: over.profileGone ?? false },
      problems: [],
    }),
  )

function putMasterOnScreen(withSlave = false): void {
  useLocalProfilesStore.setState({
    slaves: withSlave ? { s1: { id: 's1', name: 'Scratch', createdAt: 1, shownHostIds: [], world: { workspaces: [SLAVE_WS], tabs: {}, activeWorkspaceId: 'sw', activeTabId: null } } } : {},
    slaveOrder: withSlave ? ['s1'] : [],
    activeProfileId: MASTER_PROFILE_ID,
    parkedMaster: null,
    worldEpoch: 0,
    master: { name: null },
  })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useWorkspaceStore.setState({ workspaces: [W1], activeWorkspaceId: 'w1', worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
}

/** Slave `s1` on screen; the master (Alpha, Beta) parked. */
function putSlaveOnScreen(): void {
  useLocalProfilesStore.setState({
    slaves: { s1: { id: 's1', name: 'Scratch', createdAt: 1, shownHostIds: [], world: null } },
    slaveOrder: ['s1'],
    activeProfileId: 's1',
    parkedMaster: { workspaces: [W1, W2], tabs: {}, activeWorkspaceId: 'w1', activeTabId: null },
    worldEpoch: 1,
    master: { name: null },
  })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, worldId: 's1', worldEpoch: 1 })
  useWorkspaceStore.setState({ workspaces: [SLAVE_WS], activeWorkspaceId: 'sw', worldId: 's1', worldEpoch: 1 })
}

const button = () => screen.queryByTestId('home-conflict-button')
const popover = () => screen.queryByTestId('profile-conflict-panel')
const row = (key: string) => within(popover()!).getByTestId(`profile-conflict-row-${key}`)

function renderHome() {
  const onSelectHome = vi.fn()
  render(<HomeRow isActive={false} onSelectHome={onSelectHome} />)
  return { onSelectHome }
}

beforeEach(() => {
  __resetSyncStatusForTest()
  __resetProfileSwitcherForTest()
  useConflictPanelStore.setState({ openWsId: null })
  navigate.mockReset()
  vi.mocked(switchActiveProfile).mockClear()
  putMasterOnScreen()
})

afterEach(() => {
  cleanup()
  __resetSyncStatusForTest()
  __resetProfileSwitcherForTest()
})

describe('the icon', () => {
  it('no master / no locks / the profile gone → no icon (the row is its one plain button)', () => {
    renderHome()
    expect(button()).toBeNull()
    setSync({})
    expect(button()).toBeNull()
    setSync({ workspaces: CONFLICT }, {}, { profileGone: true })
    expect(button()).toBeNull()
    expect(screen.getAllByRole('button')).toHaveLength(1)
  })

  it('locks → an icon with their count, OUTSIDE the switcher trigger', () => {
    setSync({ workspaces: CONFLICT, 'tabs.w1': RESET })
    renderHome()
    const b = button()!
    const label = en['profile.conflict.button'].replace('{{count}}', '2')
    expect(b).toHaveAttribute('aria-label', label)
    expect(b).toHaveAttribute('title', label)
    expect(screen.getByTestId('home-button').contains(b)).toBe(false)
    expect(screen.getByTestId('home-header').contains(b)).toBe(true)
  })

  it('a click opens the popover — and neither selects Home nor opens the switcher', () => {
    putMasterOnScreen(true)
    setSync({ workspaces: CONFLICT })
    const { onSelectHome } = renderHome()
    fireEvent.click(button()!)
    expect(popover()).toBeInTheDocument()
    expect(popover()).toHaveAttribute('aria-label', en['profile.conflict.title'])
    expect(onSelectHome).not.toHaveBeenCalled()
    expect(useProfileSwitcherStore.getState().open).toBe(false)
    expect(screen.queryByTestId('profile-switcher-menu')).toBeNull()
    fireEvent.click(button()!)
    expect(popover()).toBeNull()
  })

  it('the last lock lifted → icon and popover go; a new lock later does not reopen it by itself', () => {
    setSync({ workspaces: CONFLICT })
    renderHome()
    fireEvent.click(button()!)
    setSync({})
    expect(button()).toBeNull()
    expect(popover()).toBeNull()
    setSync({ settings: CONFLICT })
    expect(button()).toBeInTheDocument()
    expect(popover()).toBeNull()
  })
})

describe('the popover\'s rows', () => {
  it('one per lock, in the section list\'s order, labelled like Settings, with the reason like its Resolve rows', () => {
    setSync({ 'tabs.w1': CONFLICT, workspaces: RESET, settings: INVALID }, { settings: 'rejected-settings' })
    renderHome()
    fireEvent.click(button()!)
    const rows = within(popover()!).getAllByTestId(/^profile-conflict-row-/)
    expect(rows.map((r) => r.getAttribute('data-section'))).toEqual(['settings', 'workspaces', 'tabs.w1'])
    expect(row('settings')).toHaveTextContent(en['settings.profile.current.label.settings'])
    expect(row('tabs.w1')).toHaveTextContent(en['settings.profile.current.label.tabs'].replace('{{workspace}}', 'Alpha'))
    expect(within(row('workspaces')).getByTestId('profile-conflict-why-workspaces')).toHaveTextContent(en['settings.profile.resolve.why.reset'])
    expect(within(row('tabs.w1')).getByTestId('profile-conflict-why-tabs.w1')).toHaveTextContent(en['settings.profile.resolve.why.conflict'])
    expect(within(row('settings')).getByTestId('profile-conflict-why-settings')).toHaveTextContent(en['settings.profile.resolve.why.invalid.rejected_settings'])
    // no "only Keep this device's" suffix: the popover offers no choice
    expect(within(row('settings')).getByTestId('profile-conflict-why-settings')).not.toHaveTextContent(en['settings.profile.resolve.invalid_only'])
  })

  it('an invalid lock with no reason → the general sentence', () => {
    setSync({ settings: INVALID })
    renderHome()
    fireEvent.click(button()!)
    expect(within(row('settings')).getByTestId('profile-conflict-why-settings')).toHaveTextContent(en['settings.profile.resolve.why.invalid.unknown'])
  })

  it('settings / workspaces → "Resolve in Settings": to Settings › Profile, and the popover closes', () => {
    setSync({ workspaces: CONFLICT })
    renderHome()
    fireEvent.click(button()!)
    const go = within(row('workspaces')).getByTestId('profile-conflict-settings-workspaces')
    expect(go).toHaveTextContent(en['profile.conflict.open_settings'])
    fireEvent.click(go)
    expect(navigate).toHaveBeenCalledWith('/settings/profile')
    expect(popover()).toBeNull()
  })

  it('tabs of a workspace on screen (the master) → "Go to workspace": opens that row\'s panel, and the popover closes', () => {
    setSync({ 'tabs.w1': CONFLICT })
    renderHome()
    fireEvent.click(button()!)
    expect(within(row('tabs.w1')).queryByTestId('profile-conflict-settings-tabs.w1')).toBeNull()
    const show = within(row('tabs.w1')).getByTestId('profile-conflict-show-tabs.w1')
    expect(show).toHaveTextContent(en['profile.conflict.show_workspace'])
    fireEvent.click(show)
    expect(useConflictPanelStore.getState().openWsId).toBe('w1')
    expect(popover()).toBeNull()
  })

  it('tabs of a workspace this device has not taken (master on screen) → Settings', () => {
    setSync({ 'tabs.w2': CONFLICT })
    renderHome()
    fireEvent.click(button()!)
    expect(within(row('tabs.w2')).queryByTestId('profile-conflict-show-tabs.w2')).toBeNull()
    expect(within(row('tabs.w2')).getByTestId('profile-conflict-settings-tabs.w2')).toBeInTheDocument()
  })

  it('tabs while the master world is unsettled → Settings, never "Go to workspace"', () => {
    setSync({ 'tabs.w1': CONFLICT })
    renderHome()
    act(() => useTabStore.setState({ worldEpoch: 3 }))
    fireEvent.click(button()!)
    expect(within(row('tabs.w1')).queryByTestId('profile-conflict-show-tabs.w1')).toBeNull()
    expect(within(row('tabs.w1')).getByTestId('profile-conflict-settings-tabs.w1')).toBeInTheDocument()
  })

  it('a slave on screen: the icon stays (the locks are the master\'s); a tabs row is named from the MASTER world and offers the way back', () => {
    putSlaveOnScreen()
    setSync({ 'tabs.w2': CONFLICT, workspaces: RESET })
    renderHome()
    fireEvent.click(button()!)
    const tabs = row('tabs.w2')
    expect(tabs).toHaveTextContent(en['settings.profile.current.label.tabs'].replace('{{workspace}}', 'Beta'))
    expect(tabs).toHaveTextContent(en['profile.conflict.on_master'])
    expect(within(tabs).queryByTestId('profile-conflict-show-tabs.w2')).toBeNull()
    const back = within(tabs).getByTestId('profile-conflict-switch-tabs.w2')
    expect(back).toHaveTextContent(en['profile.conflict.switch_to_master'])
    fireEvent.click(back)
    expect(switchActiveProfile).toHaveBeenCalledWith(MASTER_PROFILE_ID)
    // …and a switch under way is not asked for twice
    expect(within(row('tabs.w2')).getByTestId('profile-conflict-switch-tabs.w2')).toBeDisabled()
    // settings-level rows still go to Settings
    expect(within(row('workspaces')).getByTestId('profile-conflict-settings-workspaces')).toBeInTheDocument()
  })
})
