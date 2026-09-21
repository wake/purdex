import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import en from '../../../locales/en.json'
import { CurrentBlock } from './CurrentBlock'
import { useProfileStore } from '../../../stores/useProfileStore'
import { useHostStore } from '../../../stores/useHostStore'
import { useLocalProfilesStore } from '../../../stores/useLocalProfilesStore'
import { useProfileSync } from '../../../hooks/useProfileSync'
import { detachMaster, requestSyncNow } from '../../../lib/profile/start'
import type { ProfileSyncSnapshot } from '../../../lib/profile/start'
import { readMasterWorld } from '../../../lib/profile/master-world'
import type { MasterWorldRead, UnsettledReason } from '../../../lib/profile/master-world'
import type { ExecutorStatus, SectionLock } from '../../../lib/profile/executor'

vi.mock('../../../hooks/useProfileSync', () => ({ useProfileSync: vi.fn() }))
vi.mock('../../../lib/profile/start', () => ({ requestSyncNow: vi.fn(), detachMaster: vi.fn() }))
vi.mock('../../../lib/profile/master-world', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../../lib/profile/master-world')>()),
  readMasterWorld: vi.fn(),
}))

const NO_MASTER: ProfileSyncSnapshot = { master: null, leader: false, blocked: null, status: null, problems: [], remote: false, stale: false }
const status = (sections: ExecutorStatus['sections'] = {}, profile: ExecutorStatus['profile'] = 'synced', locks: ExecutorStatus['locks'] = {}): ExecutorStatus => ({ profile, schemaLock: null, sections, locks })
const attached = (over: Partial<ProfileSyncSnapshot> = {}): ProfileSyncSnapshot => ({
  master: { hostId: 'h1', profileId: 'p1' },
  leader: true,
  blocked: null,
  status: status(),
  problems: [],
  remote: false,
  stale: false,
  ...over,
})
const lock = (kind: SectionLock['status'], rev: number): SectionLock => ({ status: kind, currentHash: 'a', sot: { rev, hash: 'b' }, conflict: null })
const SETTLED: MasterWorldRead = { settled: true, onScreen: true, world: { workspaces: [], tabs: {}, activeWorkspaceId: null, activeTabId: null } }

const show = (snapshot: ProfileSyncSnapshot, masterName: string | null = 'default') => {
  vi.mocked(useProfileSync).mockReturnValue(snapshot)
  return render(<CurrentBlock masterName={masterName} />)
}

beforeEach(() => {
  vi.mocked(requestSyncNow).mockReset()
  vi.mocked(detachMaster).mockReset()
  vi.mocked(detachMaster).mockResolvedValue({ ok: true })
  vi.mocked(readMasterWorld).mockReturnValue(SETTLED)
  useProfileStore.setState({ masterHostId: 'h1', masterProfileId: 'p1', masterEndpoint: '10.0.0.1:7860', pendingDirection: null, suspension: null, autoSync: true })
  useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'mlab', ip: '10.0.0.1', port: 7860, order: 0 } }, hostOrder: ['h1'] })
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: 'master', parkedMaster: null, worldEpoch: 0, relabelCount: 0, master: { name: null } })
})

afterEach(() => {
  cleanup()
  vi.useRealTimers()
})

describe('no master', () => {
  it('says what Profile Sync is, in two sentences — and offers nothing that does nothing', () => {
    show(NO_MASTER)
    const block = screen.getByTestId('profile-current-block')
    expect(block).toHaveAttribute('data-state', 'none')
    expect(block).toHaveTextContent(en['settings.profile.current.none_what'])
    expect(block).toHaveTextContent(en['settings.profile.current.none_how'])
    expect(within(block).queryAllByRole('button')).toHaveLength(0)
    expect(within(block).queryAllByRole('switch')).toHaveLength(0)
    expect(screen.queryByTestId('profile-sync-now')).toBeNull()
    expect(screen.queryByTestId('profile-stop-sync')).toBeNull()
    expect(screen.queryByTestId('profile-auto-sync')).toBeNull()
  })

  it('reads nothing of the master world, and starts no timer', () => {
    vi.useFakeTimers()
    show(NO_MASTER)
    expect(readMasterWorld).not.toHaveBeenCalled()
    expect(vi.getTimerCount()).toBe(0)
  })
})

describe('a master attached', () => {
  it('names the SOT profile and its host, and reads the whole as the switcher\'s dot does', () => {
    show(attached())
    expect(screen.getByTestId('profile-current-block')).toHaveAttribute('data-state', 'attached')
    expect(screen.getByTestId('profile-current-master')).toHaveTextContent('default')
    expect(screen.getByTestId('profile-current-host')).toHaveTextContent('mlab')
    expect(screen.getByTestId('profile-current-state')).toHaveAttribute('data-state', 'synced')
    expect(screen.getByTestId('profile-current-state')).toHaveTextContent(en['profile.sync.synced'])
  })

  it('the name not known (yet): the profile id stands in', () => {
    show(attached(), null)
    expect(screen.getByTestId('profile-current-master')).toHaveTextContent('p1')
  })

  it.each([
    [attached({ status: status({}, 'pending') }), 'syncing'],
    [attached({ status: status({}, 'locked:conflict') }), 'locked'],
    [attached({ blocked: 'profile-gone' }), 'problem'],
    [attached({ status: null }), 'unknown'],
  ])('the overall state follows syncDotOf (%#)', (snapshot, dot) => {
    show(snapshot)
    expect(screen.getByTestId('profile-current-state')).toHaveAttribute('data-state', dot)
  })

  it('one row per section, sorted, each with its state', () => {
    show(attached({ status: status({ workspaces: 'synced', hosts: 'pending', 'tabs.w1': 'synced' }, 'pending') }))
    const rows = screen.getAllByTestId(/^profile-current-section-(?!rev-)/)
    expect(rows.map((r) => r.getAttribute('data-section'))).toEqual(['hosts', 'tabs.w1', 'workspaces'])
    expect(screen.getByTestId('profile-current-section-hosts')).toHaveAttribute('data-status', 'pending')
    expect(screen.getByTestId('profile-current-section-hosts')).toHaveTextContent(en['settings.profile.current.section.pending'])
    expect(screen.getByTestId('profile-current-section-workspaces')).toHaveTextContent(en['settings.profile.current.section.synced'])
  })

  it('no status yet: said, not an empty list', () => {
    show(attached({ status: null }))
    expect(screen.getByTestId('profile-current-no-status')).toBeInTheDocument()
    expect(screen.queryByTestId(/^profile-current-section-/)).toBeNull()
  })

  it('a locked section shows its kind and the SOT revision — and NO button: the panel is the next version\'s', () => {
    show(attached({ status: status({ workspaces: 'locked:conflict', hosts: 'locked:reset', settings: 'locked:invalid' }, 'locked:reset', { workspaces: lock('locked:conflict', 9), hosts: lock('locked:reset', 3), settings: lock('locked:invalid', 41) }) }))
    const row = screen.getByTestId('profile-current-section-workspaces')
    expect(row).toHaveTextContent(en['settings.profile.current.section.locked_conflict'])
    expect(screen.getByTestId('profile-current-section-rev-workspaces')).toHaveTextContent('9')
    expect(screen.getByTestId('profile-current-section-hosts')).toHaveTextContent(en['settings.profile.current.section.locked_reset'])
    expect(screen.getByTestId('profile-current-section-settings')).toHaveTextContent(en['settings.profile.current.section.locked_invalid'])
    expect(screen.getByTestId('profile-current-locked-note')).toHaveTextContent(en['settings.profile.current.locked_note'])
    expect(within(screen.getByTestId('profile-current-sections')).queryAllByRole('button')).toHaveLength(0)
  })

  it('nothing locked: no note about the panel, no revision', () => {
    show(attached({ status: status({ workspaces: 'synced' }) }))
    expect(screen.queryByTestId('profile-current-locked-note')).toBeNull()
    expect(screen.queryByTestId('profile-current-section-rev-workspaces')).toBeNull()
  })

  it('a schema lock is said in words', () => {
    const schemaLock = { section: 'hosts', kind: 'hosts', verdict: 'sot-is-newer', mine: { fingerprint: 'a', ordinal: 1 }, sot: { fingerprint: 'b', ordinal: 2 } } as unknown as ExecutorStatus['schemaLock']
    show(attached({ status: { ...status({}, 'locked:schema'), schemaLock } }))
    expect(screen.getByTestId('profile-current-schema')).toHaveTextContent(en['settings.profile.current.schema.sot-is-newer'])
  })
})

describe('a follower window', () => {
  it('the figures are the leader\'s, and say so', () => {
    show(attached({ leader: false, remote: true, status: status({ hosts: 'synced' }) }))
    expect(screen.getAllByTestId('profile-current-source').length).toBeGreaterThanOrEqual(2)
    for (const el of screen.getAllByTestId('profile-current-source')) expect(el).toHaveTextContent(en['settings.profile.current.from_leader'])
    expect(screen.getByTestId('profile-current-state')).toHaveAttribute('data-source', 'leader')
    expect(screen.getByTestId('profile-current-section-hosts')).toHaveAttribute('data-source', 'leader')
    expect(screen.queryByTestId('profile-current-stale')).toBeNull()
  })

  it('the leader\'s own window does not', () => {
    show(attached({ status: status({ hosts: 'synced' }) }))
    expect(screen.queryByTestId('profile-current-source')).toBeNull()
    expect(screen.getByTestId('profile-current-state')).toHaveAttribute('data-source', 'this-window')
  })

  it('stale is said in plain words, not as an error', () => {
    show(attached({ leader: false, remote: true, stale: true }))
    const stale = screen.getByTestId('profile-current-stale')
    expect(stale).toHaveTextContent(en['settings.profile.current.stale'])
    expect(stale.className).not.toMatch(/red/)
  })

  it('Sync now works from a follower too', () => {
    show(attached({ leader: false, remote: true }))
    fireEvent.click(screen.getByTestId('profile-sync-now'))
    expect(requestSyncNow).toHaveBeenCalledTimes(1)
  })
})

describe('blocked — each reason its own sentence', () => {
  it('master-endpoint-changed: the address it was attached at, and the one the host has now', () => {
    useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'mlab', ip: '10.0.0.9', port: 7861, order: 0 } } })
    show(attached({ blocked: 'master-endpoint-changed' }))
    const el = screen.getByTestId('profile-current-blocked')
    expect(el).toHaveAttribute('data-reason', 'master-endpoint-changed')
    expect(el).toHaveTextContent('10.0.0.1:7860')
    expect(el).toHaveTextContent('10.0.0.9:7861')
  })

  it.each([
    ['profile-gone', 'settings.profile.current.blocked.profile_gone'],
    ['suspended', 'settings.profile.current.blocked.suspended'],
  ] as const)('%s', (reason, key) => {
    show(attached({ blocked: reason }))
    expect(screen.getByTestId('profile-current-blocked')).toHaveAttribute('data-reason', reason)
    expect(screen.getByTestId('profile-current-blocked')).toHaveTextContent(en[key])
  })

  it('while blocked nothing syncs: Sync now would do nothing, so it cannot be pressed', () => {
    show(attached({ blocked: 'profile-gone' }))
    expect(screen.getByTestId('profile-sync-now')).toBeDisabled()
    expect(screen.getByTestId('profile-stop-sync')).toBeEnabled()
  })

  it('not blocked: no such line', () => {
    show(attached())
    expect(screen.queryByTestId('profile-current-blocked')).toBeNull()
  })
})

describe('the master world, unsettled — a STATE, never an error', () => {
  const unsettled = (reason: UnsettledReason) => vi.mocked(readMasterWorld).mockReturnValue({ settled: false, reason })

  it.each(['epoch-mismatch', 'world-mismatch', 'behind-fence'] as const)('%s → catching up with another window', (reason) => {
    unsettled(reason)
    show(attached())
    const el = screen.getByTestId('profile-current-world')
    expect(el).toHaveAttribute('data-reason', reason)
    expect(el).toHaveTextContent(en['settings.profile.current.world.catching_up'])
    expect(el.className).not.toMatch(/red|error/)
  })

  it('junk-epoch → paused, with the one way out', () => {
    unsettled('junk-epoch')
    show(attached())
    const el = screen.getByTestId('profile-current-world')
    expect(el).toHaveTextContent(en['settings.profile.current.world.junk_epoch'])
    expect(el.className).not.toMatch(/red|error/)
  })

  it('no-parked-master → this device holds no master world', () => {
    unsettled('no-parked-master')
    show(attached())
    const el = screen.getByTestId('profile-current-world')
    expect(el).toHaveTextContent(en['settings.profile.current.world.no_parked_master'])
    expect(el.className).not.toMatch(/red|error/)
  })

  it('settled → no such line; and a change of the stores is read again', () => {
    show(attached())
    expect(screen.queryByTestId('profile-current-world')).toBeNull()
    unsettled('world-mismatch')
    act(() => useLocalProfilesStore.setState({ worldEpoch: 1 }))
    expect(screen.getByTestId('profile-current-world')).toHaveAttribute('data-reason', 'world-mismatch')
  })
})

describe('settings waiting for workspaces', () => {
  it('workspaces locked and settings with something to send → said', () => {
    show(attached({ status: status({ workspaces: 'locked:conflict', settings: 'pending' }, 'locked:conflict', { workspaces: lock('locked:conflict', 9) }) }))
    expect(screen.getByTestId('profile-current-settings-waiting')).toHaveTextContent(en['settings.profile.current.settings_waiting'])
  })

  it.each([
    [{ workspaces: 'locked:conflict', settings: 'synced' }],
    [{ workspaces: 'synced', settings: 'pending' }],
    [{ workspaces: 'pending', settings: 'pending' }],
  ] as const)('%j → not said', (sections) => {
    show(attached({ status: status({ ...sections }, 'pending') }))
    expect(screen.queryByTestId('profile-current-settings-waiting')).toBeNull()
  })
})

describe('the three controls', () => {
  it('Auto-sync shows the stored preference and writes it', () => {
    show(attached())
    const toggle = screen.getByTestId('profile-auto-sync')
    expect(toggle).toHaveAttribute('aria-checked', 'true')
    fireEvent.click(toggle)
    expect(useProfileStore.getState().autoSync).toBe(false)
    expect(screen.getByTestId('profile-auto-sync')).toHaveAttribute('aria-checked', 'false')
  })

  it('Sync now asks, and says it has', () => {
    show(attached())
    expect(screen.queryByTestId('profile-sync-asked')).toBeNull()
    fireEvent.click(screen.getByTestId('profile-sync-now'))
    expect(requestSyncNow).toHaveBeenCalledTimes(1)
    expect(screen.getByTestId('profile-sync-asked')).toHaveTextContent(en['settings.profile.current.sync_asked'])
  })

  it('Stop sync asks first, saying what stays; Cancel stops nothing', () => {
    show(attached())
    fireEvent.click(screen.getByTestId('profile-stop-sync'))
    expect(detachMaster).not.toHaveBeenCalled()
    expect(screen.getByTestId('profile-stop-sync-dialog')).toHaveTextContent(en['settings.profile.current.stop_body'])
    fireEvent.click(screen.getByTestId('profile-stop-sync-cancel'))
    expect(screen.queryByTestId('profile-stop-sync-dialog')).toBeNull()
    expect(detachMaster).not.toHaveBeenCalled()
  })

  it('Confirm detaches', async () => {
    show(attached())
    fireEvent.click(screen.getByTestId('profile-stop-sync'))
    await act(async () => { fireEvent.click(screen.getByTestId('profile-stop-sync-confirm')) })
    expect(detachMaster).toHaveBeenCalledTimes(1)
    expect(screen.queryByTestId('profile-stop-sync-dialog')).toBeNull()
  })
})

describe('the problem log', () => {
  it('lists the latest, newest first, as a log — and nothing when there is none', () => {
    const problems = Array.from({ length: 7 }, (_, i) => ({ kind: `k${i}`, detail: `d${i}`, at: 1000 + i, ...(i === 6 ? { section: 'hosts' } : {}) }))
    const { unmount } = show(attached({ problems }))
    const items = screen.getAllByTestId('profile-current-problem')
    expect(items).toHaveLength(5)
    expect(items[0]).toHaveTextContent('k6')
    expect(items[0]).toHaveTextContent('hosts')
    expect(items[0]).toHaveTextContent('d6')
    expect(items[4]).toHaveTextContent('k2')
    unmount()
    show(attached())
    expect(screen.queryByTestId('profile-current-problems')).toBeNull()
  })
})
