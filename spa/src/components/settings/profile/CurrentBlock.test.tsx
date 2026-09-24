import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import en from '../../../locales/en.json'
import zhTW from '../../../locales/zh-TW.json'
import { useI18nStore } from '../../../stores/useI18nStore'
import { CurrentBlock } from './CurrentBlock'
import { useProfileStore } from '../../../stores/useProfileStore'
import { useHostStore } from '../../../stores/useHostStore'
import { useLocalProfilesStore } from '../../../stores/useLocalProfilesStore'
import { useWorkspaceStore } from '../../../features/workspace/store'
import { useProfileSync } from '../../../hooks/useProfileSync'
import { detachMaster, requestSyncNow } from '../../../lib/profile/start'
import type { ProfileSyncSnapshot } from '../../../lib/profile/start'
import { readMasterWorld } from '../../../lib/profile/master-world'
import type { MasterWorldRead, UnsettledReason } from '../../../lib/profile/master-world'
import type { ExecutorStatus, SectionLock } from '../../../lib/profile/executor'

vi.mock('../../../hooks/useProfileSync', () => ({ useProfileSync: vi.fn() }))
vi.mock('../../../lib/profile/start', () => ({ requestSyncNow: vi.fn(), detachMaster: vi.fn(), retryPendingDetach: vi.fn(), requestResolve: vi.fn() }))
// The wizard is its own file with its own tests; here it is a box that says it is open and can be closed.
vi.mock('./wizard/ProfileWizard', () => ({
  ProfileWizard: ({ onClose }: { onClose: () => void }) => <div data-testid="profile-wizard"><button data-testid="profile-wizard-close" onClick={onClose} /></div>,
}))
vi.mock('../../../lib/profile/master-world', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../../lib/profile/master-world')>()),
  readMasterWorld: vi.fn(),
}))

const NO_MASTER: ProfileSyncSnapshot = { master: null, leader: false, blocked: null, status: null, problems: [], remote: false, stale: false }
const status = (sections: ExecutorStatus['sections'] = {}, profile: ExecutorStatus['profile'] = 'synced', locks: ExecutorStatus['locks'] = {}): ExecutorStatus => ({ profile, schemaLock: null, sections, locks, profileGone: false, detail: {}, indexFailures: 0, lastSuccessAt: null })
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
  useProfileStore.setState({ masterHostId: 'h1', masterProfileId: 'p1', masterEndpoint: '10.0.0.1:7860', pendingDirection: null, suspension: null, autoSync: true, pendingDetaches: [] })
  localStorage.removeItem('purdex-profile-pull-unconfirmed')
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
    // ONE control: the way into the wizard (P3d-3)
    expect(within(block).getAllByRole('button').map((b) => b.getAttribute('data-testid'))).toEqual(['profile-setup-start'])
    expect(screen.getByTestId('profile-setup-start')).toHaveTextContent(en['settings.profile.current.setup'])
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

describe('the wizard\'s two ways in (P3d-3)', () => {
  it('no master: "Set up sync…" opens it IN PLACE of the two sentences; closing brings them back', () => {
    show(NO_MASTER)
    expect(screen.queryByTestId('profile-wizard')).toBeNull()
    fireEvent.click(screen.getByTestId('profile-setup-start'))
    expect(screen.getByTestId('profile-wizard')).toBeInTheDocument()
    expect(screen.queryByTestId('profile-setup-start')).toBeNull()
    expect(screen.getByTestId('profile-current-block')).not.toHaveTextContent(en['settings.profile.current.none_how'])
    fireEvent.click(screen.getByTestId('profile-wizard-close'))
    expect(screen.queryByTestId('profile-wizard')).toBeNull()
    expect(screen.getByTestId('profile-setup-start')).toBeInTheDocument()
  })

  it('a master attached: "another profile or host…" opens the same wizard in place of the state — and the plain Stop sync is not offered beside it', () => {
    show(attached())
    // the button carries its row's words, like "Sync now" does (P3d-4c F6) — not the no-master "Set up sync…"
    expect(screen.getByTestId('profile-setup-change')).toHaveTextContent(en['settings.profile.current.change'])
    expect(screen.getByTestId('profile-setup-change')).not.toHaveTextContent(en['settings.profile.current.setup'])
    fireEvent.click(screen.getByTestId('profile-setup-change'))
    expect(screen.getByTestId('profile-wizard')).toBeInTheDocument()
    expect(screen.queryByTestId('profile-sync-now')).toBeNull()
    expect(screen.queryByTestId('profile-stop-sync')).toBeNull()
    fireEvent.click(screen.getByTestId('profile-wizard-close'))
    expect(screen.getByTestId('profile-stop-sync')).toBeInTheDocument()
  })

  it('the wizard stays open while the master goes and comes (its first and last steps do exactly that)', () => {
    const view = show(attached())
    fireEvent.click(screen.getByTestId('profile-setup-change'))
    vi.mocked(useProfileSync).mockReturnValue(NO_MASTER)
    view.rerender(<CurrentBlock masterName={null} />)
    expect(screen.getByTestId('profile-wizard')).toBeInTheDocument()
    vi.mocked(useProfileSync).mockReturnValue(attached())
    view.rerender(<CurrentBlock masterName="default" />)
    expect(screen.getByTestId('profile-wizard')).toBeInTheDocument()
  })

  it('a host that was not told of a stop is still said while the wizard is open: the notice is not the wizard\'s to hide', () => {
    useProfileStore.setState({ pendingDetaches: [{ hostId: 'h1', profileId: 'p1', endpoint: '10.0.0.1:7860', detail: 'timeout', at: 1 }] })
    show(NO_MASTER)
    fireEvent.click(screen.getByTestId('profile-setup-start'))
    expect(screen.getByTestId('profile-detach-leftover')).toBeInTheDocument()
  })

  it('opening the page still opens nothing: the wizard is a click away', () => {
    show(NO_MASTER)
    expect(screen.queryByTestId('profile-wizard')).toBeNull()
    cleanup()
    show(attached())
    expect(screen.queryByTestId('profile-wizard')).toBeNull()
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

  it('one row per section, in reading order, each with its state; the raw key stays in data-section and the title', () => {
    show(attached({ status: status({ workspaces: 'synced', hosts: 'pending', 'tabs.w1': 'synced' }, 'pending') }))
    const rows = screen.getAllByTestId(/^profile-current-section-(?!rev-)/)
    expect(rows.map((r) => r.getAttribute('data-section'))).toEqual(['hosts', 'workspaces', 'tabs.w1'])
    expect(screen.getByTestId('profile-current-section-hosts')).toHaveAttribute('data-status', 'pending')
    expect(screen.getByTestId('profile-current-section-hosts')).toHaveTextContent(en['settings.profile.current.section.pending'])
    expect(screen.getByTestId('profile-current-section-workspaces')).toHaveTextContent(en['settings.profile.current.section.synced'])
    expect(screen.getByTestId('profile-current-section-hosts')).toHaveTextContent(en['settings.profile.current.label.hosts'])
    expect(within(screen.getByTestId('profile-current-section-tabs.w1')).getByTitle('tabs.w1')).toBeInTheDocument()
  })

  describe('a tabs section is named after its workspace — in the MASTER world', () => {
    const world = (workspaces: { id: string; name: string }[], onScreen = true): MasterWorldRead =>
      ({ settled: true, onScreen, world: { workspaces: workspaces.map((w) => ({ ...w, tabs: [] })) as never, tabs: {}, activeWorkspaceId: null, activeTabId: null } })
    const tabs = (...ids: string[]) => attached({ status: status(Object.fromEntries(ids.map((id) => [`tabs.${id}`, 'synced' as const]))) })

    it('by name, never by id', () => {
      vi.mocked(readMasterWorld).mockReturnValue(world([{ id: '4ecsi1', name: 'Client work' }]))
      show(tabs('4ecsi1'))
      const row = screen.getByTestId('profile-current-section-tabs.4ecsi1')
      expect(row).toHaveTextContent('Client work')
      expect(row).not.toHaveTextContent('4ecsi1')
    })

    it('a slave on screen: the master is PARKED — the name is the parked world\'s, not the live store\'s', () => {
      useWorkspaceStore.setState({ workspaces: [{ id: 'w1', name: 'The slave\'s w1', tabs: [] }] as never })
      vi.mocked(readMasterWorld).mockReturnValue(world([{ id: 'w1', name: 'The master\'s w1' }], false))
      show(tabs('w1'))
      expect(screen.getByTestId('profile-current-section-tabs.w1')).toHaveTextContent('The master\'s w1')
      expect(screen.getByTestId('profile-current-section-tabs.w1')).not.toHaveTextContent('The slave')
      useWorkspaceStore.setState({ workspaces: [] })
    })

    it('a workspace this device has not seen yet: said so, and the id is not shown', () => {
      vi.mocked(readMasterWorld).mockReturnValue(world([]))
      show(tabs('aapu1q'))
      const row = screen.getByTestId('profile-current-section-tabs.aapu1q')
      expect(row).toHaveTextContent(en['settings.profile.current.label.tabs_unseen'])
      expect(row).not.toHaveTextContent('aapu1q')
    })

    it('the master world cannot be read right now: no name is claimed, and "unseen" is not either', () => {
      vi.mocked(readMasterWorld).mockReturnValue({ settled: false, reason: 'world-mismatch' })
      show(tabs('w1'))
      const row = screen.getByTestId('profile-current-section-tabs.w1')
      expect(row).toHaveTextContent(en['settings.profile.current.label.tabs_unknown'])
      expect(row).not.toHaveTextContent('w1')
    })

    it('in the master world\'s workspace order', () => {
      vi.mocked(readMasterWorld).mockReturnValue(world([{ id: 'w2', name: 'B' }, { id: 'w1', name: 'A' }]))
      show(tabs('w1', 'w2'))
      expect(screen.getAllByTestId(/^profile-current-section-tabs/).map((r) => r.getAttribute('data-section'))).toEqual(['tabs.w2', 'tabs.w1'])
    })
  })

  it('no status yet: said, not an empty list', () => {
    show(attached({ status: null }))
    expect(screen.getByTestId('profile-current-no-status')).toBeInTheDocument()
    expect(screen.queryByTestId(/^profile-current-section-/)).toBeNull()
  })

  it('a locked section shows its kind and the SOT revision — and a Resolve row, in the section list\'s order, with its ways out (P3d-4b)', () => {
    show(attached({ status: status({ workspaces: 'locked:conflict', hosts: 'locked:reset', settings: 'locked:invalid' }, 'locked:reset', { workspaces: lock('locked:conflict', 9), hosts: lock('locked:reset', 3), settings: lock('locked:invalid', 41) }) }))
    const row = screen.getByTestId('profile-current-section-workspaces')
    expect(row).toHaveTextContent(en['settings.profile.current.section.locked_conflict'])
    // the HOST's rev beside the lock: the one a decision would overwrite (the agreed one is `-rev-`)
    expect(screen.getByTestId('profile-current-section-sot-rev-workspaces')).toHaveTextContent(en['settings.profile.current.sot_rev'].replace('{{rev}}', '9'))
    expect(screen.getByTestId('profile-current-section-hosts')).toHaveTextContent(en['settings.profile.current.section.locked_reset'])
    expect(screen.getByTestId('profile-current-section-settings')).toHaveTextContent(en['settings.profile.current.section.locked_invalid'])
    // the sentence that promised this panel is gone, and so is its key
    expect(screen.queryByTestId('profile-current-locked-note')).toBeNull()
    expect(Object.keys(en)).not.toContain('settings.profile.current.locked_note')
    const rows = within(screen.getByTestId('profile-resolve-block')).getAllByTestId(/^profile-resolve-row-/)
    expect(rows.map((r) => r.getAttribute('data-section'))).toEqual(['hosts', 'settings', 'workspaces'])
    // labelled like the section list
    expect(rows[0]).toHaveTextContent(en['settings.profile.current.label.hosts'])
    expect(screen.getByTestId('profile-resolve-take-sot-hosts')).toBeInTheDocument()
    expect(screen.getByTestId('profile-resolve-take-sot-workspaces')).toBeInTheDocument()
    expect(screen.queryByTestId('profile-resolve-take-sot-settings')).toBeNull()
    expect(screen.getByTestId('profile-resolve-keep-local-settings')).not.toBeDisabled()
  })

  it('a follower\'s Resolve rows carry the "reported by the window that is syncing" badge', () => {
    show(attached({ leader: false, remote: true, status: status({ hosts: 'locked:reset' }, 'locked:reset', { hosts: lock('locked:reset', 3) }) }))
    expect(screen.getByTestId('profile-resolve-source-hosts')).toHaveTextContent(en['settings.profile.current.from_leader'])
  })

  it('blocked (no driver runs): the Resolve buttons are disabled, as Sync now is', () => {
    show(attached({ blocked: 'suspended', status: status({ hosts: 'locked:reset' }, 'locked:reset', { hosts: lock('locked:reset', 3) }) }))
    expect(screen.getByTestId('profile-resolve-keep-local-hosts')).toBeDisabled()
    expect(screen.getByTestId('profile-resolve-take-sot-hosts')).toBeDisabled()
  })

  it('locked:schema is a sentence, not a row', () => {
    const schemaLock = { section: 'hosts', kind: 'hosts' as const, verdict: 'sot-is-newer' as const, mine: { fingerprint: 'a', ordinal: 1 }, sot: { fingerprint: 'b', ordinal: 2 } }
    show(attached({ status: { ...status({ hosts: 'synced' }, 'locked:schema'), schemaLock } }))
    expect(screen.getByTestId('profile-current-schema')).toBeInTheDocument()
    expect(screen.queryByTestId('profile-resolve-block')).toBeNull()
  })

  it('nothing locked: no Resolve block, no host revision', () => {
    show(attached({ status: status({ workspaces: 'synced' }) }))
    expect(screen.queryByTestId('profile-resolve-block')).toBeNull()
    expect(screen.queryByTestId('profile-current-section-sot-rev-workspaces')).toBeNull()
  })

  describe('what the executor publishes beyond the status (P3d-4a)', () => {
    const fine = (rev: number | null) => ({ rev, failures: 0, retryAt: null, invalidReason: null })

    it('every row shows the AGREED rev; a locked one keeps the host rev beside it', () => {
      show(attached({
        status: {
          ...status({ hosts: 'synced', workspaces: 'locked:conflict' }, 'locked:conflict', { workspaces: lock('locked:conflict', 9) }),
          detail: { hosts: fine(4), workspaces: fine(7) },
        },
      }))
      expect(screen.getByTestId('profile-current-section-rev-hosts')).toHaveTextContent(en['settings.profile.current.rev'].replace('{{rev}}', '4'))
      expect(screen.getByTestId('profile-current-section-rev-workspaces')).toHaveTextContent(en['settings.profile.current.rev'].replace('{{rev}}', '7'))
      expect(screen.getByTestId('profile-current-section-sot-rev-workspaces')).toHaveTextContent('9')
      expect(screen.queryByTestId('profile-current-section-sot-rev-hosts')).toBeNull()
    })

    it('never agreed (null), or a record from an older build (no detail): no rev at all — never "0"', () => {
      show(attached({ status: { ...status({ hosts: 'pending', settings: 'synced' }), detail: { hosts: fine(null) } } }))
      expect(screen.getByTestId('profile-current-section-hosts')).toBeInTheDocument()
      expect(screen.queryByTestId('profile-current-section-rev-hosts')).toBeNull()
      expect(screen.queryByTestId('profile-current-section-rev-settings')).toBeNull()
      expect(screen.getByTestId('profile-current-sections')).not.toHaveTextContent(/rev 0/)
    })

    it('"in sync as of" the last answer that left the profile synced, in absolute local time; nothing while there is none', () => {
      const at = new Date(2026, 8, 23, 14, 2, 31).getTime()
      show(attached({ status: { ...status({ hosts: 'synced' }), lastSuccessAt: at } }))
      const el = screen.getByTestId('profile-current-last-sync')
      expect(el).toHaveTextContent(en['settings.profile.current.last_sync'].replace('{{time}}', new Date(at).toLocaleString('en')))
      cleanup()
      show(attached({ status: status({ hosts: 'synced' }) }))
      expect(screen.queryByTestId('profile-current-last-sync')).toBeNull()
    })

    it('a failing section says so, and when the next try is — not in red; without an armed retry it says only "failing"', () => {
      const at = new Date(2026, 8, 23, 14, 2, 31).getTime()
      show(attached({
        status: {
          ...status({ hosts: 'pending', workspaces: 'pending', settings: 'synced' }, 'pending'),
          detail: { hosts: { rev: 1, failures: 3, retryAt: at, invalidReason: null }, workspaces: { rev: 1, failures: 1, retryAt: null, invalidReason: null }, settings: fine(1) },
        },
      }))
      const hosts = screen.getByTestId('profile-current-section-failing-hosts')
      expect(hosts).toHaveTextContent(en['settings.profile.current.section_failing_at'].replace('{{time}}', new Date(at).toLocaleTimeString('en')))
      expect(hosts.className).not.toMatch(/red/)
      expect(screen.getByTestId('profile-current-section-failing-workspaces')).toHaveTextContent(en['settings.profile.current.section_failing'])
      expect(screen.queryByTestId('profile-current-section-failing-settings')).toBeNull()
    })
  })

  it('a schema lock is said in words', () => {
    const schemaLock = { section: 'hosts', kind: 'hosts', verdict: 'sot-is-newer', mine: { fingerprint: 'a', ordinal: 1 }, sot: { fingerprint: 'b', ordinal: 2 } } as unknown as ExecutorStatus['schemaLock']
    show(attached({ status: { ...status({}, 'locked:schema'), schemaLock } }))
    expect(screen.getByTestId('profile-current-schema')).toHaveTextContent(en['settings.profile.current.schema.sot-is-newer'])
  })
})

describe('Auto-sync off — pending is waiting, not syncing (P3d-4c F2)', () => {
  const pending = () => attached({ status: status({ hosts: 'pending', settings: 'synced' }, 'pending') })

  it('the overall state and a pending row say "waiting — Auto-sync is off"; the raw status stays in data-status / data-state', () => {
    useProfileStore.setState({ autoSync: false })
    show(pending())
    const state = screen.getByTestId('profile-current-state')
    expect(state).toHaveAttribute('data-state', 'syncing')
    expect(state).toHaveAttribute('data-held', 'auto-sync-off')
    expect(state).toHaveTextContent(en['profile.sync.held'])
    expect(state).not.toHaveTextContent(en['profile.sync.syncing'])
    const hosts = screen.getByTestId('profile-current-section-hosts')
    expect(hosts).toHaveAttribute('data-status', 'pending')
    expect(hosts).toHaveAttribute('data-held', 'auto-sync-off')
    expect(hosts).toHaveTextContent(en['settings.profile.current.section.held'])
    expect(hosts).not.toHaveTextContent(en['settings.profile.current.section.pending'])
    const settings = screen.getByTestId('profile-current-section-settings')
    expect(settings).not.toHaveAttribute('data-held')
    expect(settings).toHaveTextContent(en['settings.profile.current.section.synced'])
  })

  it('Auto-sync on: "Syncing…", no data-held', () => {
    show(pending())
    expect(screen.getByTestId('profile-current-state')).toHaveTextContent(en['profile.sync.syncing'])
    expect(screen.getByTestId('profile-current-state')).not.toHaveAttribute('data-held')
    expect(screen.getByTestId('profile-current-section-hosts')).toHaveTextContent(en['settings.profile.current.section.pending'])
    expect(screen.getByTestId('profile-current-section-hosts')).not.toHaveAttribute('data-held')
  })

  it('a stale follower: the state is unknown and no row claims to be held', () => {
    useProfileStore.setState({ autoSync: false })
    show(attached({ leader: false, remote: true, stale: true, status: status({ hosts: 'pending' }, 'pending') }))
    expect(screen.getByTestId('profile-current-state')).toHaveAttribute('data-state', 'unknown')
    expect(screen.getByTestId('profile-current-state')).not.toHaveAttribute('data-held')
    expect(screen.getByTestId('profile-current-section-hosts')).not.toHaveAttribute('data-held')
    expect(screen.getByTestId('profile-current-section-hosts')).not.toHaveTextContent(en['settings.profile.current.section.held'])
  })

  it('turning Auto-sync off while the page is open changes the words at once', () => {
    show(pending())
    act(() => useProfileStore.setState({ autoSync: false }))
    expect(screen.getByTestId('profile-current-state')).toHaveTextContent(en['profile.sync.held'])
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

  describe('paused by a host (host-sync-identity §4, §11.4): which host, and what mends it', () => {
    const D = 'mlab:278cbm'
    const hostCfg = (id: string, name: string, ip: string, daemonId?: string) => ({ id, name, ip, port: 7860, order: 0, ...(daemonId ? { daemonId } : {}) })

    it('host-identity-mismatch: names the host this window sees at another daemon, and says to fix its address or remove it', () => {
      useHostStore.setState({
        hosts: { h1: hostCfg('h1', 'mlab', '10.0.0.1', D), h2: hostCfg('h2', 'air', '10.0.0.2', 'air:111111') },
        hostOrder: ['h1', 'h2'],
        runtime: { h2: { status: 'connected', daemonIdMismatch: { stored: 'air:111111', observed: 'else:222222', endpoint: '10.0.0.2:7860' } } },
      })
      show(attached({ blocked: 'host-identity-mismatch', status: null }))
      const el = screen.getByTestId('profile-current-blocked')
      expect(el).toHaveAttribute('data-reason', 'host-identity-mismatch')
      expect(el).toHaveTextContent(en['settings.profile.current.blocked.host_identity_mismatch'].replace('{{hosts}}', 'air'))
      expect(screen.getByTestId('profile-current-state')).toHaveAttribute('data-state', 'problem')
      expect(screen.getByTestId('profile-sync-now')).toBeDisabled()
    })

    it('host-identity-mismatch published by a leader whose runtime this window does not share: the sentence without a name', () => {
      useHostStore.setState({ hosts: { h1: hostCfg('h1', 'mlab', '10.0.0.1', D) }, hostOrder: ['h1'], runtime: {} })
      show(attached({ blocked: 'host-identity-mismatch', status: null, leader: false, remote: true }))
      expect(screen.getByTestId('profile-current-blocked')).toHaveTextContent(en['settings.profile.current.blocked.host_identity_mismatch_unnamed'])
    })

    it('host-identity-conflict: names the hosts that claim one daemon, and says to remove one', () => {
      useHostStore.setState({ hosts: { h1: hostCfg('h1', 'mlab', '10.0.0.1', D), h2: hostCfg('h2', 'mlab (old)', '10.0.0.5', D) }, hostOrder: ['h1', 'h2'], runtime: {} })
      show(attached({ blocked: 'host-identity-conflict', status: null }))
      const el = screen.getByTestId('profile-current-blocked')
      expect(el).toHaveAttribute('data-reason', 'host-identity-conflict')
      expect(el).toHaveTextContent(en['settings.profile.current.blocked.host_identity_conflict'].replace('{{hosts}}', 'mlab, mlab (old)'))
    })

    it('host-identity-conflict this window does not see (yet): the sentence without names', () => {
      show(attached({ blocked: 'host-identity-conflict', status: null }))
      expect(screen.getByTestId('profile-current-blocked')).toHaveTextContent(en['settings.profile.current.blocked.host_identity_conflict_unnamed'])
    })
  })

  it('the profile gone by the INDEX (the executor\'s profileGone, `blocked` null) is said exactly as the 404 is', () => {
    show(attached({ status: { ...status({ hosts: 'synced' }, 'locked:reset'), profileGone: true } }))
    const el = screen.getByTestId('profile-current-blocked')
    expect(el).toHaveAttribute('data-reason', 'profile-gone')
    expect(el).toHaveTextContent(en['settings.profile.current.blocked.profile_gone'])
    expect(screen.getByTestId('profile-current-state')).toHaveAttribute('data-state', 'problem')
    expect(screen.getByTestId('profile-sync-now')).toBeDisabled()
    // the one way out the sentence points at is there
    expect(screen.getByTestId('profile-setup-change')).toBeEnabled()
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
  it('workspaces locked and settings with something to send → said, as "locked"', () => {
    show(attached({ status: status({ workspaces: 'locked:conflict', settings: 'pending' }, 'locked:conflict', { workspaces: lock('locked:conflict', 9) }) }))
    const el = screen.getByTestId('profile-current-settings-waiting')
    expect(el).toHaveAttribute('data-reason', 'locked')
    expect(el).toHaveTextContent(en['settings.profile.current.settings_waiting_locked'])
  })

  it('F2: nothing is waited for while nothing syncs — the profile gone, or blocked: no waiting sentence, no "next try"', () => {
    const failingStatus = {
      ...status({ workspaces: 'locked:conflict', settings: 'pending' }, 'locked:reset', { workspaces: lock('locked:conflict', 9) }),
      detail: { workspaces: { rev: 1, failures: 3, retryAt: 5000, invalidReason: null }, settings: { rev: 1, failures: 1, retryAt: 6000, invalidReason: null } },
      indexFailures: 2,
    }
    show(attached({ status: { ...failingStatus, profileGone: true } }))
    expect(screen.getByTestId('profile-current-blocked')).toHaveAttribute('data-reason', 'profile-gone')
    expect(screen.queryByTestId('profile-current-settings-waiting')).toBeNull()
    expect(screen.queryByTestId('profile-current-section-failing-workspaces')).toBeNull()
    expect(screen.queryByTestId('profile-current-section-failing-settings')).toBeNull()
    expect(screen.getByTestId('profile-current-section-sot-rev-workspaces')).toBeInTheDocument() // the lock is kept, and shown
    cleanup()
    show(attached({ blocked: 'master-endpoint-changed', status: { ...failingStatus, sections: { workspaces: 'pending', settings: 'pending' }, locks: {} } }))
    expect(screen.queryByTestId('profile-current-settings-waiting')).toBeNull()
    expect(screen.queryByTestId('profile-current-section-failing-workspaces')).toBeNull()
  })

  it('F3: a STALE follower (the leader is gone) makes no live claim: no "next try", no failure-based wait — a lock still counts', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date(2026, 8, 23, 15, 0, 0))
    const past = new Date(2026, 8, 23, 14, 2, 31).getTime()
    const failingStatus = {
      ...status({ workspaces: 'pending', settings: 'pending' }, 'pending'),
      detail: { workspaces: { rev: 1, failures: 3, retryAt: past, invalidReason: null } },
      indexFailures: 1,
    }
    show(attached({ leader: false, remote: true, stale: true, status: failingStatus }))
    expect(screen.getByTestId('profile-current-stale')).toBeInTheDocument()
    expect(screen.queryByTestId('profile-current-section-failing-workspaces')).toBeNull()
    expect(screen.getByTestId('profile-current-sections')).not.toHaveTextContent(new Date(past).toLocaleTimeString())
    expect(screen.queryByTestId('profile-current-settings-waiting')).toBeNull()
    cleanup()
    // the control: the same figures from a leader that is there ARE said
    show(attached({ leader: false, remote: true, stale: false, status: failingStatus }))
    expect(screen.getByTestId('profile-current-section-failing-workspaces')).toBeInTheDocument()
    expect(screen.getByTestId('profile-current-settings-waiting')).toHaveAttribute('data-reason', 'failing')
    cleanup()
    // a lock is a fact, not a promise: said even when stale
    show(attached({ leader: false, remote: true, stale: true, status: status({ workspaces: 'locked:conflict', settings: 'pending' }, 'locked:conflict', { workspaces: lock('locked:conflict', 9) }) }))
    expect(screen.getByTestId('profile-current-settings-waiting')).toHaveAttribute('data-reason', 'locked')
  })

  it('workspaces failing, or the index read failing → said, as "failing"', () => {
    show(attached({ status: { ...status({ workspaces: 'pending', settings: 'pending' }, 'pending'), detail: { workspaces: { rev: 1, failures: 2, retryAt: null, invalidReason: null } } } }))
    const el = screen.getByTestId('profile-current-settings-waiting')
    expect(el).toHaveAttribute('data-reason', 'failing')
    expect(el).toHaveTextContent(en['settings.profile.current.settings_waiting_failing'])
    cleanup()
    show(attached({ status: { ...status({ workspaces: 'synced', settings: 'pending' }, 'pending'), indexFailures: 1 } }))
    expect(screen.getByTestId('profile-current-settings-waiting')).toHaveAttribute('data-reason', 'failing')
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

  it('Stop sync: the confirmation outlives the half of the block that goes with the master — busy until the answer, then gone', async () => {
    let answer: (r: { ok: true }) => void = () => {}
    vi.mocked(detachMaster).mockReturnValue(new Promise((resolve) => { answer = resolve }))
    const { rerender } = show(attached())
    fireEvent.click(screen.getByTestId('profile-stop-sync'))
    expect(screen.getByTestId('profile-stop-sync-dialog')).toHaveTextContent(en['settings.profile.current.stop_body'])
    fireEvent.click(screen.getByTestId('profile-stop-sync-confirm'))
    expect(detachMaster).toHaveBeenCalledTimes(1)
    // start.ts clears the master at once: the block is the no-master one from here on.
    vi.mocked(useProfileSync).mockReturnValue(NO_MASTER)
    rerender(<CurrentBlock masterName={null} />)
    expect(screen.getByTestId('profile-current-block')).toHaveAttribute('data-state', 'none')
    expect(screen.getByTestId('profile-stop-sync-dialog')).toBeInTheDocument()
    expect(screen.getByTestId('profile-stop-sync-confirm')).toBeDisabled()
    await act(async () => { answer({ ok: true }) })
    expect(screen.queryByTestId('profile-stop-sync-dialog')).toBeNull()
  })

  it('an attachment a failed detach left on the daemon is said WITHOUT a master too — that is when it matters', () => {
    useProfileStore.setState({ masterHostId: null, masterProfileId: null, masterEndpoint: null, pendingDetaches: [{ hostId: 'h1', profileId: 'p_000000000001', endpoint: '10.0.0.1:7860', detail: 'timeout', at: 1 }] })
    show(NO_MASTER)
    expect(screen.getByTestId('profile-current-block')).toHaveAttribute('data-state', 'none')
    expect(screen.getByTestId('profile-detach-leftover')).toHaveTextContent('mlab')
    expect(screen.getByTestId('profile-detach-retry')).toBeInTheDocument()
  })
})

describe('the stopped-pull notice is gone (host ownership H3b): an older build\'s key is not read', () => {
  const NOTICE = JSON.stringify({ hostId: 'h1', profileId: 'p_000000000001', at: 1 })

  it('with the old key in localStorage: nothing is said, with a master or without', () => {
    localStorage.setItem('purdex-profile-pull-unconfirmed', NOTICE)
    useProfileStore.setState({ masterHostId: null, masterProfileId: null, masterEndpoint: null })
    show(NO_MASTER)
    expect(screen.queryByTestId('profile-pull-unconfirmed')).toBeNull()
    expect(screen.getByTestId('profile-setup-start')).toBeInTheDocument()
    cleanup()
    useProfileStore.setState({ masterHostId: 'h1', masterProfileId: 'p1', masterEndpoint: '10.0.0.1:7860' })
    show(NO_MASTER)
    expect(screen.queryByTestId('profile-pull-unconfirmed')).toBeNull()
  })

  it('another window writing the old key is not heard either', () => {
    useProfileStore.setState({ masterHostId: null, masterProfileId: null, masterEndpoint: null })
    show(NO_MASTER)
    act(() => {
      localStorage.setItem('purdex-profile-pull-unconfirmed', NOTICE)
      window.dispatchEvent(new StorageEvent('storage', { key: 'purdex-profile-pull-unconfirmed' }))
    })
    expect(screen.queryByTestId('profile-pull-unconfirmed')).toBeNull()
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

describe('every time the block shows is in the UI language, not the browser\'s (P3d-4c F5)', () => {
  const at = new Date(2026, 8, 23, 14, 2, 31).getTime()
  // `toHaveTextContent` collapses the received whitespace (ICU puts U+202F in times); the expected side likewise
  const fmt = (d: number, locale: string, how: 'toLocaleString' | 'toLocaleTimeString') => new Date(d)[how](locale).replace(/\s+/g, ' ')

  afterEach(() => {
    useI18nStore.getState().setLocale('en')
  })

  it('zh-TW: "in sync as of", a failing row\'s next try and the problem log are formatted as zh-TW', () => {
    useI18nStore.getState().setLocale('zh-TW')
    show(attached({
      status: {
        ...status({ hosts: 'pending' }, 'pending'),
        lastSuccessAt: at,
        detail: { hosts: { rev: 1, failures: 2, retryAt: at, invalidReason: null } },
      },
      problems: [{ kind: 'k', detail: 'd', at }],
    }))
    expect(screen.getByTestId('profile-current-last-sync')).toHaveTextContent(zhTW['settings.profile.current.last_sync'].replace('{{time}}', fmt(at, 'zh-TW', 'toLocaleString')))
    expect(screen.getByTestId('profile-current-section-failing-hosts')).toHaveTextContent(zhTW['settings.profile.current.section_failing_at'].replace('{{time}}', fmt(at, 'zh-TW', 'toLocaleTimeString')))
    expect(screen.getByTestId('profile-current-problem')).toHaveTextContent(fmt(at, 'zh-TW', 'toLocaleTimeString'))
  })

  it('English UI: the times are asked for in "en", whatever the browser\'s locale is', () => {
    const toLocaleString = vi.spyOn(Date.prototype, 'toLocaleString')
    const toLocaleTimeString = vi.spyOn(Date.prototype, 'toLocaleTimeString')
    try {
      show(attached({
        status: { ...status({ hosts: 'pending' }, 'pending'), lastSuccessAt: at, detail: { hosts: { rev: 1, failures: 2, retryAt: at, invalidReason: null } } },
        problems: [{ kind: 'k', detail: 'd', at }],
      }))
      // what the page asked for, whatever the runner's default locale is
      expect(toLocaleString.mock.calls.length).toBeGreaterThan(0)
      for (const call of toLocaleString.mock.calls) expect(call[0]).toBe('en')
      expect(toLocaleTimeString.mock.calls.length).toBeGreaterThan(0)
      for (const call of toLocaleTimeString.mock.calls) expect(call[0]).toBe('en')
    } finally {
      toLocaleString.mockRestore()
      toLocaleTimeString.mockRestore()
    }
  })

  it('a user-imported locale (its id is no language tag): English times, never the browser\'s', () => {
    const id = useI18nStore.getState().importLocale({ name: 'Mine', translations: {} })
    useI18nStore.getState().setLocale(id)
    const toLocaleString = vi.spyOn(Date.prototype, 'toLocaleString')
    try {
      show(attached({ status: { ...status({ hosts: 'synced' }), lastSuccessAt: at } }))
      expect(toLocaleString.mock.calls.map((c) => c[0])).toEqual(['en'])
    } finally {
      toLocaleString.mockRestore()
      useI18nStore.getState().deleteCustomLocale(id)
    }
  })
})

describe('one word for a reset, in the section list and the Resolve row (P3d-4c F7)', () => {
  it('"recreated" / 「被重建過」 in both', () => {
    expect(en['settings.profile.current.section.locked_reset']).toMatch(/recreated/)
    expect(en['settings.profile.resolve.why.reset']).toMatch(/recreated/)
    expect(zhTW['settings.profile.current.section.locked_reset']).toMatch(/被重建過/)
    expect(zhTW['settings.profile.resolve.why.reset']).toMatch(/被重建過/)
  })
})
