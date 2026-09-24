import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, act } from '@testing-library/react'
import en from '../../../locales/en.json'
import { HomeRow } from './HomeRow'
import { ActivityBarNarrow } from './ActivityBarNarrow'
import { useLocalProfilesStore, type LocalProfile } from '../../../stores/useLocalProfilesStore'
import { __resetProfileSwitcherForTest, BUSY_RETRY_MS, BUSY_RETRY_TOTAL_MS, useProfileSwitcherStore } from '../../../stores/useProfileSwitcherStore'
import { useUndoToast } from '../../../stores/useUndoToast'
import { switchActiveProfile, type SwitchResult } from '../../../lib/profile/switch-active'
import { useProfileSync } from '../../../hooks/useProfileSync'
import { useProfileStore } from '../../../stores/useProfileStore'
import type { ProfileSyncSnapshot } from '../../../lib/profile/start'

vi.mock('../../../lib/profile/switch-active', () => ({ switchActiveProfile: vi.fn() }))
vi.mock('../../../hooks/useProfileSync', () => ({ useProfileSync: vi.fn() }))

const NO_MASTER: ProfileSyncSnapshot = { master: null, leader: false, blocked: null, status: null, problems: [], remote: false, stale: false }
const attached = (over: Partial<ProfileSyncSnapshot> = {}, profile: NonNullable<ProfileSyncSnapshot['status']>['profile'] = 'synced'): ProfileSyncSnapshot => ({
  master: { hostId: 'h1', profileId: 'p1' },
  leader: true,
  blocked: null,
  status: { profile, schemaLock: null, sections: {}, locks: {}, profileGone: false, detail: {}, indexFailures: 0, lastSuccessAt: null },
  problems: [],
  remote: false,
  stale: false,
  ...over,
})

const slave = (id: string, name: string, onScreen = false): LocalProfile => ({
  id,
  name,
  createdAt: 1,
  shownHostIds: [],
  world: onScreen ? null : { workspaces: [], tabs: {}, activeWorkspaceId: null, activeTabId: null },
})

/** `slaveOrder` deliberately differs from the key order of `slaves`. */
function seedSlaves(activeProfileId = 'master') {
  useLocalProfilesStore.setState({
    slaves: { s1: slave('s1', 'Scratch', activeProfileId === 's1'), s2: slave('s2', 'Client work', activeProfileId === 's2') },
    slaveOrder: ['s2', 's1'],
    activeProfileId,
  })
}

const result = (r: SwitchResult) => vi.mocked(switchActiveProfile).mockResolvedValue(r)
const menu = () => screen.queryByTestId('profile-switcher-menu')
const toast = () => useUndoToast.getState().toast?.message ?? null
/** Let the mocked promise (and whatever it schedules) settle. */
const flush = () => act(async () => { await vi.advanceTimersByTimeAsync(0) })
const advance = (ms: number) => act(async () => { await vi.advanceTimersByTimeAsync(ms) })

function renderHome(onSelectHome = vi.fn()) {
  const utils = render(<HomeRow isActive={false} onSelectHome={onSelectHome} />)
  return { ...utils, onSelectHome }
}

beforeEach(() => {
  vi.useFakeTimers()
  vi.mocked(switchActiveProfile).mockReset()
  vi.mocked(useProfileSync).mockReturnValue(NO_MASTER)
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: 'master', parkedMaster: null, worldEpoch: 0, relabelCount: 0, master: { name: null } })
  __resetProfileSwitcherForTest()
  useUndoToast.setState({ toast: null })
})

afterEach(() => {
  cleanup()
  __resetProfileSwitcherForTest()
  vi.useRealTimers()
})

describe('Home button — no slaves: exactly today', () => {
  it('a click selects Home; no menu, no chevron, no aria-haspopup', () => {
    const { onSelectHome } = renderHome()
    const button = screen.getByTestId('home-button')
    expect(button).not.toHaveAttribute('aria-haspopup')
    expect(button).not.toHaveAttribute('aria-expanded')
    expect(screen.queryByTestId('home-switcher-chevron')).toBeNull()
    fireEvent.click(button)
    expect(onSelectHome).toHaveBeenCalledTimes(1)
    expect(menu()).toBeNull()
    expect(useProfileSwitcherStore.getState().open).toBe(false)
  })

  it('an attached master alone changes nothing either', () => {
    vi.mocked(useProfileSync).mockReturnValue(attached())
    const { onSelectHome } = renderHome()
    fireEvent.click(screen.getByTestId('home-button'))
    expect(onSelectHome).toHaveBeenCalledTimes(1)
    expect(menu()).toBeNull()
  })

  it('an open request with no slaves opens nothing, and is dropped (it must not fire when a slave appears later)', () => {
    renderHome()
    act(() => useProfileSwitcherStore.getState().setOpen(true))
    expect(menu()).toBeNull()
    expect(useProfileSwitcherStore.getState().open).toBe(false)
  })
})

describe('Home button — with slaves: the profile switcher', () => {
  beforeEach(() => seedSlaves())

  it('is a menu trigger: aria-haspopup, aria-expanded, a chevron; a click opens the menu and does not select Home', () => {
    const { onSelectHome } = renderHome()
    const button = screen.getByTestId('home-button')
    expect(button).toHaveAttribute('aria-haspopup', 'menu')
    expect(button).toHaveAttribute('aria-expanded', 'false')
    expect(screen.getByTestId('home-switcher-chevron')).toBeInTheDocument()
    fireEvent.click(button)
    expect(menu()).toBeInTheDocument()
    expect(menu()).toHaveAttribute('aria-label', en['profile.switcher.label'])
    expect(button).toHaveAttribute('aria-expanded', 'true')
    expect(onSelectHome).not.toHaveBeenCalled()
  })

  it('a second click closes it', () => {
    renderHome()
    fireEvent.click(screen.getByTestId('home-button'))
    fireEvent.click(screen.getByTestId('home-button'))
    expect(menu()).toBeNull()
  })

  it('lists the master first, then the slaves in slaveOrder; then a divider and Settings › Profile', () => {
    renderHome()
    fireEvent.click(screen.getByTestId('home-button'))
    const items = screen.getAllByRole('menuitemradio')
    expect(items.map((el) => el.dataset.testid)).toEqual(['profile-item-master', 'profile-item-s2', 'profile-item-s1'])
    expect(items[0]).toHaveTextContent(en['profile.master']) // the tag; the name is `Home` until it is given one
    expect(screen.getAllByRole('separator')).toHaveLength(1)
    // A plain item, not one of the radio group: it is not a profile.
    const plain = screen.getAllByRole('menuitem')
    expect(plain.map((el) => el.dataset.testid)).toEqual(['profile-item-settings'])
    expect(plain[0]).toHaveTextContent(en['profile.switcher.settings'])
    expect(items[2].compareDocumentPosition(plain[0]) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('Settings › Profile: goes to /settings/profile, closes the menu, switches nothing', () => {
    window.history.replaceState(null, '', '/')
    renderHome()
    fireEvent.click(screen.getByTestId('home-button'))
    fireEvent.click(screen.getByTestId('profile-item-settings'))
    expect(window.location.pathname).toBe('/settings/profile')
    expect(menu()).toBeNull()
    expect(switchActiveProfile).not.toHaveBeenCalled()
    window.history.replaceState(null, '', '/')
  })

  it('… and stays available while a switch is under way', () => {
    vi.mocked(switchActiveProfile).mockReturnValue(new Promise(() => {}))
    renderHome()
    fireEvent.click(screen.getByTestId('home-button'))
    fireEvent.click(screen.getByTestId('profile-item-s1'))
    expect(screen.getByTestId('profile-item-settings')).not.toHaveAttribute('aria-disabled')
  })

  it('a slave shows its own name verbatim, with the full name as its title', () => {
    useLocalProfilesStore.setState({ slaves: { s1: slave('s1', 'profile.master') }, slaveOrder: ['s1'] })
    renderHome()
    fireEvent.click(screen.getByTestId('home-button'))
    const item = screen.getByTestId('profile-item-s1')
    expect(item).toHaveTextContent('profile.master') // user data: never through t()
    expect(item).toHaveAttribute('title', 'profile.master')
  })

  it('the master on screen: it is the checked one, and where focus lands', () => {
    renderHome()
    fireEvent.click(screen.getByTestId('home-button'))
    expect(screen.getByTestId('profile-item-master')).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByTestId('profile-item-s1')).toHaveAttribute('aria-checked', 'false')
    expect(screen.getByTestId('profile-item-s2')).toHaveAttribute('aria-checked', 'false')
    expect(screen.getByTestId('profile-item-master')).toHaveFocus()
  })

  it('a slave on screen: that slave is the checked one', () => {
    seedSlaves('s1')
    renderHome()
    fireEvent.click(screen.getByTestId('home-button'))
    expect(screen.getByTestId('profile-item-s1')).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByTestId('profile-item-master')).toHaveAttribute('aria-checked', 'false')
    expect(screen.getByTestId('profile-item-s2')).toHaveAttribute('aria-checked', 'false')
    expect(screen.getByTestId('profile-item-s1')).toHaveFocus()
  })

  it('an open request from outside (the shortcut) opens the menu with focus inside it', () => {
    renderHome()
    act(() => useProfileSwitcherStore.getState().setOpen(true))
    expect(menu()).toBeInTheDocument()
    expect(menu()!.contains(document.activeElement)).toBe(true)
  })

  it('the last slave going away (another window deleted it) closes the menu and drops the request', () => {
    renderHome()
    fireEvent.click(screen.getByTestId('home-button'))
    act(() => useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [] }))
    expect(menu()).toBeNull()
    expect(useProfileSwitcherStore.getState().open).toBe(false)
  })
})

describe('choosing a profile', () => {
  beforeEach(() => seedSlaves())

  const open = () => fireEvent.click(screen.getByTestId('home-button'))

  it('calls switchActiveProfile with the id; success closes the menu, silently', async () => {
    result({ ok: true })
    renderHome()
    open()
    fireEvent.click(screen.getByTestId('profile-item-s2'))
    expect(switchActiveProfile).toHaveBeenCalledTimes(1)
    expect(switchActiveProfile).toHaveBeenCalledWith('s2')
    await flush()
    expect(menu()).toBeNull()
    expect(toast()).toBeNull()
  })

  it('the master is chosen by its id', async () => {
    seedSlaves('s1')
    result({ ok: true })
    renderHome()
    open()
    fireEvent.click(screen.getByTestId('profile-item-master'))
    expect(switchActiveProfile).toHaveBeenCalledWith('master')
    await flush()
    expect(menu()).toBeNull()
  })

  it('already-on-screen just closes', async () => {
    result({ ok: false, reason: 'already-on-screen' })
    renderHome()
    open()
    fireEvent.click(screen.getByTestId('profile-item-master'))
    await flush()
    expect(menu()).toBeNull()
    expect(toast()).toBeNull()
  })

  it('while one switch is under way no other item can start a second', async () => {
    vi.mocked(switchActiveProfile).mockReturnValue(new Promise(() => {}))
    renderHome()
    open()
    fireEvent.click(screen.getByTestId('profile-item-s2'))
    fireEvent.click(screen.getByTestId('profile-item-s1'))
    fireEvent.click(screen.getByTestId('profile-item-s2'))
    expect(switchActiveProfile).toHaveBeenCalledTimes(1)
    expect(screen.getByTestId('profile-item-s1')).toHaveAttribute('aria-disabled', 'true')
    expect(screen.getByTestId('profile-item-master')).toHaveAttribute('aria-disabled', 'true')
    expect(screen.getByTestId('profile-item-s2')).toHaveAttribute('aria-busy', 'true')
  })

  // The two bars are two components sharing one `open` flag: changing the bar's width unmounts one switcher and
  // mounts the other with the menu still open. The switch under way must survive that — it is the store's.
  it('the bar changes under a pending switch: the new menu is still busy, cannot start a second, and the first answer is still handled', async () => {
    let answer: (r: SwitchResult) => void = () => {}
    vi.mocked(switchActiveProfile).mockReturnValue(new Promise((resolve) => { answer = resolve }))
    const { unmount } = renderHome()
    open()
    fireEvent.click(screen.getByTestId('profile-item-s2'))
    unmount()
    render(
      <ActivityBarNarrow
        workspaces={[]}
        activeWorkspaceId={null}
        onSelectWorkspace={() => {}}
        onSelectHome={() => {}}
        onAddWorkspace={() => {}}
        onOpenHosts={() => {}}
        onOpenSettings={() => {}}
      />,
    )
    expect(menu()).toBeInTheDocument() // `open` outlived the wide bar
    expect(screen.getByTestId('profile-item-s2')).toHaveAttribute('aria-busy', 'true')
    expect(screen.getByTestId('profile-item-s1')).toHaveAttribute('aria-disabled', 'true')
    fireEvent.click(screen.getByTestId('profile-item-s1'))
    fireEvent.click(screen.getByTestId('profile-item-master'))
    expect(switchActiveProfile).toHaveBeenCalledTimes(1)

    await act(async () => { answer({ ok: true }) })
    await flush()
    expect(menu()).toBeNull()
    expect(toast()).toBeNull()
  })

  describe('busy', () => {
    it('retries silently every BUSY_RETRY_MS with the item marked busy and the menu open; a success ends it', async () => {
      vi.mocked(switchActiveProfile)
        .mockResolvedValueOnce({ ok: false, reason: 'busy' })
        .mockResolvedValueOnce({ ok: false, reason: 'busy' })
        .mockResolvedValue({ ok: true })
      renderHome()
      open()
      fireEvent.click(screen.getByTestId('profile-item-s2'))
      await flush()
      expect(switchActiveProfile).toHaveBeenCalledTimes(1)
      expect(screen.getByTestId('profile-item-s2')).toHaveAttribute('aria-busy', 'true')
      expect(menu()).toBeInTheDocument()
      expect(toast()).toBeNull()

      await advance(BUSY_RETRY_MS - 1)
      expect(switchActiveProfile).toHaveBeenCalledTimes(1) // not before the interval
      await advance(1)
      expect(switchActiveProfile).toHaveBeenCalledTimes(2)
      expect(switchActiveProfile).toHaveBeenLastCalledWith('s2')
      expect(menu()).toBeInTheDocument()

      await advance(BUSY_RETRY_MS)
      expect(switchActiveProfile).toHaveBeenCalledTimes(3)
      expect(menu()).toBeNull()
      expect(toast()).toBeNull()
    })

    it('gives up after BUSY_RETRY_TOTAL_MS: one toast, no further attempt, the item no longer busy', async () => {
      result({ ok: false, reason: 'busy' })
      renderHome()
      open()
      fireEvent.click(screen.getByTestId('profile-item-s2'))
      await advance(BUSY_RETRY_TOTAL_MS - 1)
      expect(toast()).toBeNull()
      await advance(1)
      const attempts = BUSY_RETRY_TOTAL_MS / BUSY_RETRY_MS + 1
      expect(switchActiveProfile).toHaveBeenCalledTimes(attempts)
      expect(toast()).toBe(en['profile.switch.busy'])

      await advance(60_000)
      expect(switchActiveProfile).toHaveBeenCalledTimes(attempts)
      expect(vi.getTimerCount()).toBe(0)
      expect(screen.getByTestId('profile-item-s2')).not.toHaveAttribute('aria-busy')
      expect(screen.getByTestId('profile-item-s2')).not.toHaveAttribute('aria-disabled')
    })

    it('the budget is time, not attempts: a switch that itself waited 3 s for the world lock gets few retries', async () => {
      vi.mocked(switchActiveProfile).mockImplementation(() => new Promise((resolve) => setTimeout(() => resolve({ ok: false, reason: 'busy' }), 3_000)))
      renderHome()
      open()
      fireEvent.click(screen.getByTestId('profile-item-s2'))
      await advance(60_000)
      expect(switchActiveProfile).toHaveBeenCalledTimes(2) // 0 s → 3 s, 3.25 s → 6.25 s: past the budget
      expect(toast()).toBe(en['profile.switch.busy'])
    })

    it('closing the menu stops the retries: no timer left, no further attempt, no toast', async () => {
      result({ ok: false, reason: 'busy' })
      renderHome()
      open()
      fireEvent.click(screen.getByTestId('profile-item-s2'))
      await flush()
      expect(vi.getTimerCount()).toBe(1)
      fireEvent.keyDown(document.activeElement!, { key: 'Escape' })
      expect(menu()).toBeNull()
      await flush() // the effect cleanup that cancels is a passive one
      expect(vi.getTimerCount()).toBe(0)
      await advance(60_000)
      expect(switchActiveProfile).toHaveBeenCalledTimes(1)
      expect(toast()).toBeNull()
    })

    it('unmounting the menu\'s bar does NOT end the switch (the store owns it): the retries go on', async () => {
      result({ ok: false, reason: 'busy' })
      const { unmount } = renderHome()
      open()
      fireEvent.click(screen.getByTestId('profile-item-s2'))
      await flush()
      unmount()
      await advance(BUSY_RETRY_MS)
      expect(switchActiveProfile).toHaveBeenCalledTimes(2)
    })

    it('a busy that arrives after the menu was closed says nothing and schedules nothing', async () => {
      let answer: (r: SwitchResult) => void = () => {}
      vi.mocked(switchActiveProfile).mockReturnValue(new Promise((resolve) => { answer = resolve }))
      renderHome()
      open()
      fireEvent.click(screen.getByTestId('profile-item-s2'))
      fireEvent.keyDown(document.activeElement!, { key: 'Escape' })
      await act(async () => { answer({ ok: false, reason: 'busy' }) })
      await flush()
      expect(vi.getTimerCount()).toBe(0)
      expect(toast()).toBeNull()
      // and the next opening starts clean
      open()
      expect(screen.getByTestId('profile-item-s2')).not.toHaveAttribute('aria-busy')
      expect(screen.getByTestId('profile-item-s2')).not.toHaveAttribute('aria-disabled')
    })
  })

  describe('refusals', () => {
    it('unsettled: the neutral "another window just switched" — not an error, no retry loop; the menu stays for the second click', async () => {
      result({ ok: false, reason: 'unsettled' })
      renderHome()
      open()
      fireEvent.click(screen.getByTestId('profile-item-s2'))
      await flush()
      expect(toast()).toBe(en['profile.switch.try_again'])
      expect(menu()).toBeInTheDocument()
      expect(screen.getByTestId('profile-item-s2')).not.toHaveAttribute('aria-disabled')
      await advance(60_000)
      expect(switchActiveProfile).toHaveBeenCalledTimes(1)
    })

    it('superseded: the same neutral message, and the menu closes — this window is about to show the other switch', async () => {
      result({ ok: false, reason: 'superseded' })
      renderHome()
      open()
      fireEvent.click(screen.getByTestId('profile-item-s2'))
      await flush()
      expect(toast()).toBe(en['profile.switch.try_again'])
      expect(menu()).toBeNull()
      await advance(60_000)
      expect(switchActiveProfile).toHaveBeenCalledTimes(1)
    })

    it('write-failed: the one real error, with its detail', async () => {
      result({ ok: false, reason: 'write-failed', detail: 'QuotaExceededError' })
      renderHome()
      open()
      fireEvent.click(screen.getByTestId('profile-item-s2'))
      await flush()
      expect(toast()).toBe(en['profile.switch.write_failed'].replace('{{detail}}', 'QuotaExceededError'))
      expect(toast()).not.toBe(en['profile.switch.try_again'])
    })

    it('not-found: says the profile is gone', async () => {
      result({ ok: false, reason: 'not-found' })
      renderHome()
      open()
      fireEvent.click(screen.getByTestId('profile-item-s2'))
      await flush()
      expect(toast()).toBe(en['profile.switch.not_found'])
    })

    it.each(['bad-world', 'bad-epoch'] as const)('%s: a failure that names its reason', async (reason) => {
      result({ ok: false, reason })
      renderHome()
      open()
      fireEvent.click(screen.getByTestId('profile-item-s2'))
      await flush()
      expect(toast()).toBe(en['profile.switch.failed'].replace('{{reason}}', reason))
    })

    it('a rejected promise is a failure too, and the menu is usable again', async () => {
      vi.mocked(switchActiveProfile).mockRejectedValue(new Error('boom'))
      renderHome()
      open()
      fireEvent.click(screen.getByTestId('profile-item-s2'))
      await flush()
      expect(toast()).toBe(en['profile.switch.failed'].replace('{{reason}}', 'boom'))
      expect(screen.getByTestId('profile-item-s2')).not.toHaveAttribute('aria-disabled')
    })
  })
})

describe('the master\'s sync dot', () => {
  beforeEach(() => seedSlaves())

  const dot = () => {
    fireEvent.click(screen.getByTestId('home-button'))
    return screen.queryByTestId('profile-sync-dot')
  }

  it('no attached master: no dot — and the master item is still there', () => {
    renderHome()
    expect(dot()).toBeNull()
    expect(screen.getByTestId('profile-item-master')).toBeInTheDocument()
  })

  it.each([
    ['synced', 'synced', 'profile.sync.synced'],
    ['pending', 'syncing', 'profile.sync.syncing'],
    ['locked:conflict', 'locked', 'profile.sync.locked'],
    ['locked:reset', 'locked', 'profile.sync.locked'],
    ['locked:invalid', 'locked', 'profile.sync.locked'],
    ['locked:schema', 'locked', 'profile.sync.locked'],
    ['idle', 'unknown', 'profile.sync.unknown'],
  ] as const)('profile status %s → %s', (profile, state, labelKey) => {
    vi.mocked(useProfileSync).mockReturnValue(attached({}, profile))
    renderHome()
    const el = dot()!
    expect(el).toHaveAttribute('data-state', state)
    expect(el).toHaveAttribute('aria-label', en[labelKey])
    expect(el).toHaveAttribute('title', en[labelKey])
    expect(screen.getByTestId('profile-item-master').contains(el)).toBe(true)
  })

  it.each([
    ['master-endpoint-changed', 'problem'],
    ['profile-gone', 'problem'],
    ['suspended', 'syncing'],
  ] as const)('blocked %s → %s, whatever the sections say', (blocked, state) => {
    vi.mocked(useProfileSync).mockReturnValue(attached({ blocked }))
    renderHome()
    expect(dot()).toHaveAttribute('data-state', state)
  })

  it('pending with Auto-sync off: the dot is the same, its words say it waits (P3d-4c F2 — one reading with Settings › Profile)', () => {
    vi.mocked(useProfileSync).mockReturnValue(attached({}, 'pending'))
    useProfileStore.setState({ autoSync: false })
    try {
      renderHome()
      const el = dot()!
      expect(el).toHaveAttribute('data-state', 'syncing')
      expect(el).toHaveAttribute('aria-label', en['profile.sync.held'])
      expect(el).toHaveAttribute('title', en['profile.sync.held'])
    } finally {
      useProfileStore.setState({ autoSync: true })
    }
  })

  it('a follower window shows what the leader published', () => {
    vi.mocked(useProfileSync).mockReturnValue(attached({ leader: false, remote: true }, 'pending'))
    renderHome()
    expect(dot()).toHaveAttribute('data-state', 'syncing')
  })

  it('a follower whose leader is gone (stale) does not vouch for the old figure', () => {
    vi.mocked(useProfileSync).mockReturnValue(attached({ leader: false, remote: true, stale: true }, 'synced'))
    renderHome()
    expect(dot()).toHaveAttribute('data-state', 'unknown')
  })

  it('no status yet → unknown', () => {
    vi.mocked(useProfileSync).mockReturnValue(attached({ status: null }))
    renderHome()
    expect(dot()).toHaveAttribute('data-state', 'unknown')
  })

  it('only the master carries a dot', () => {
    vi.mocked(useProfileSync).mockReturnValue(attached())
    renderHome()
    dot()
    expect(screen.getAllByTestId('profile-sync-dot')).toHaveLength(1)
  })
})

// One rule, no branches: the Home button shows the name, icon and colour of the profile on screen. Unnamed →
// `Home`; no icon → the Purdex logo; no colour → none. So with nothing set it is, item for item, today's button.
describe('the Home button shows the profile on screen', () => {
  function renderNarrow() {
    return render(
      <ActivityBarNarrow
        workspaces={[]}
        activeWorkspaceId={null}
        onSelectWorkspace={() => {}}
        onSelectHome={() => {}}
        onAddWorkspace={() => {}}
        onOpenHosts={() => {}}
        onOpenSettings={() => {}}
      />,
    )
  }
  const LONG = 'A long local profile name that will not fit in the bar' // names are capped at 64 code points
  const LOOK = { icon: 'Rocket', iconWeight: 'fill', color: '#3b82f6' } as const
  const triggerLabel = (name: string) => en['profile.switcher.trigger'].replace('{{name}}', name)
  const attrs = (el: Element) => Array.from(el.attributes).map((a) => a.name).sort()
  const setLook = (id: string, patch: Parameters<ReturnType<typeof useLocalProfilesStore.getState>['setProfileAppearance']>[1]) => {
    const r = useLocalProfilesStore.getState().setProfileAppearance(id, patch)
    if (!r.ok) throw new Error(r.reason)
  }

  describe('nothing set, no slave — today\'s button, attribute for attribute', () => {
    it('wide: the logo and `Home`; no title, no aria-*, no chevron, no menu, nothing else inside', () => {
      renderHome()
      const button = screen.getByTestId('home-button')
      expect(attrs(button)).toEqual(['class', 'data-testid', 'type'])
      expect(button.children).toHaveLength(2)
      const [img, label] = Array.from(button.children)
      expect(img.tagName).toBe('IMG')
      expect(img).toHaveAttribute('src', '/icons/logo-transparent.png')
      expect(img).toHaveAttribute('alt', '')
      expect(img).toHaveAttribute('width', '16')
      expect(label).toHaveTextContent(en['nav.home'])
      expect(screen.getByTestId('home-header').children).toHaveLength(1)
      expect(screen.queryByTestId('profile-icon')).toBeNull()
    })

    it('narrow: the logo, title `Home`; no aria-*, no menu, nothing else inside', () => {
      renderNarrow()
      const button = screen.getByTestId('home-button')
      expect(attrs(button)).toEqual(['class', 'data-testid', 'title'])
      expect(button).toHaveAttribute('title', en['nav.home'])
      expect(button.children).toHaveLength(1)
      const img = button.children[0]
      expect(img.tagName).toBe('IMG')
      expect(img).toHaveAttribute('src', '/icons/logo-transparent.png')
      expect(img).toHaveAttribute('alt', 'Purdex')
      expect(img).toHaveAttribute('width', '20')
      expect(button.parentElement!.children).toHaveLength(1)
    })
  })

  describe('the master, named and styled — slave or no slave', () => {
    it('wide: its name (CSS-truncated, whole in the title), its icon in its colour, and no logo', () => {
      setLook('master', { name: LONG, ...LOOK })
      renderHome()
      const button = screen.getByTestId('home-button')
      expect(screen.getByTestId('home-label')).toHaveTextContent(LONG)
      expect(screen.getByTestId('home-label')).toHaveClass('truncate')
      expect(button).toHaveAttribute('title', LONG)
      expect(button).toHaveAttribute('aria-label', LONG)
      expect(button).not.toHaveAttribute('aria-haspopup') // a name does not make a menu
      const icon = screen.getByTestId('profile-icon')
      expect(icon).toHaveAttribute('data-icon', 'Rocket')
      expect(icon).toHaveAttribute('data-weight', 'fill')
      expect(icon.style.color).toBe('rgb(59, 130, 246)')
      expect(button.querySelector('img')).toBeNull()
    })

    it('narrow: the icon in its colour; the name is the title and the accessible name', () => {
      setLook('master', { name: 'Work', ...LOOK })
      renderNarrow()
      const button = screen.getByTestId('home-button')
      expect(button).toHaveAttribute('title', 'Work')
      expect(button).toHaveAttribute('aria-label', 'Work')
      expect(screen.getByTestId('profile-icon')).toHaveAttribute('data-icon', 'Rocket')
      expect(screen.getByTestId('profile-icon').style.color).toBe('rgb(59, 130, 246)')
      expect(button.querySelector('img')).toBeNull()
    })

    it('a name alone keeps the logo; an icon alone keeps `Home`', () => {
      setLook('master', { name: 'Work' })
      const { unmount } = renderHome()
      expect(screen.getByTestId('home-button').querySelector('img')).not.toBeNull()
      expect(screen.getByTestId('home-label')).toHaveTextContent('Work')
      unmount()
      setLook('master', { name: null, icon: 'Rocket' })
      renderHome()
      expect(screen.getByTestId('home-label')).toHaveTextContent(en['nav.home'])
      expect(screen.getByTestId('home-button')).toHaveAttribute('aria-label', en['nav.home'])
      expect(screen.getByTestId('profile-icon')).toHaveAttribute('data-icon', 'Rocket')
    })
  })

  describe('with a slave', () => {
    it('the master on screen, unnamed: still `Home` — and the accessible name says it is the profile menu', () => {
      seedSlaves('master')
      renderHome()
      expect(screen.getByTestId('home-label')).toHaveTextContent(en['nav.home'])
      expect(screen.getByTestId('home-button')).toHaveAttribute('aria-label', triggerLabel(en['nav.home']))
      expect(screen.getByTestId('home-button')).toHaveAttribute('aria-haspopup', 'menu')
    })

    it.each(['wide', 'narrow'] as const)('%s — a slave on screen: ITS name, icon and colour, never the master\'s', (bar) => {
      setLook('master', { name: 'Work', icon: 'Briefcase', color: '#ef4444' })
      seedSlaves('s1')
      setLook('s1', LOOK)
      if (bar === 'wide') renderHome()
      else renderNarrow()
      const button = screen.getByTestId('home-button')
      expect(button).toHaveAttribute('title', 'Scratch')
      expect(button).toHaveAttribute('aria-label', triggerLabel('Scratch'))
      expect(screen.getByTestId('profile-icon')).toHaveAttribute('data-icon', 'Rocket')
      expect(screen.getByTestId('profile-icon').style.color).toBe('rgb(59, 130, 246)')
      if (bar === 'wide') {
        expect(screen.getByTestId('home-label')).toHaveTextContent('Scratch')
        expect(screen.getByTestId('home-label')).not.toHaveTextContent('Work')
      }
    })

    it.each(['wide', 'narrow'] as const)('%s — a plain slave on screen shows the logo, not the master\'s icon', (bar) => {
      setLook('master', { name: 'Work', icon: 'Briefcase', color: '#ef4444' })
      seedSlaves('s1')
      if (bar === 'wide') renderHome()
      else renderNarrow()
      expect(screen.queryByTestId('profile-icon')).toBeNull()
      expect(screen.getByTestId('home-button').querySelector('img')).not.toBeNull()
      expect(screen.getByTestId('home-button')).toHaveAttribute('title', 'Scratch')
    })

    it('follows a switch', () => {
      seedSlaves('master')
      renderHome()
      act(() => seedSlaves('s2'))
      expect(screen.getByTestId('home-label')).toHaveTextContent('Client work')
    })
  })

  describe('the menu', () => {
    const openMenu = () => fireEvent.click(screen.getByTestId('home-button'))

    it('every item carries its profile\'s icon and name; the master is tagged as the master', () => {
      setLook('master', { name: 'Work', icon: 'Briefcase', color: '#ef4444' })
      seedSlaves('master')
      setLook('s2', LOOK)
      renderHome()
      openMenu()
      const master = screen.getByTestId('profile-item-master')
      expect(master).toHaveTextContent('Work')
      expect(master).toHaveTextContent(en['profile.master'])
      expect(master.querySelector('[data-testid="profile-icon"]')).toHaveAttribute('data-icon', 'Briefcase')
      const s2 = screen.getByTestId('profile-item-s2')
      expect(s2).toHaveTextContent('Client work')
      expect(s2).not.toHaveTextContent(en['profile.master'])
      expect(s2.querySelector('[data-testid="profile-icon"]')).toHaveAttribute('data-icon', 'Rocket')
      expect((s2.querySelector('[data-testid="profile-icon"]') as HTMLElement).style.color).toBe('rgb(59, 130, 246)')
      // a plain slave: the logo
      const s1 = screen.getByTestId('profile-item-s1')
      expect(s1.querySelector('[data-testid="profile-icon"]')).toBeNull()
      expect(s1.querySelector('img')).toHaveAttribute('src', '/icons/logo-transparent.png')
    })

    it('an unnamed master is `Home` + the tag', () => {
      seedSlaves('master')
      renderHome()
      openMenu()
      const master = screen.getByTestId('profile-item-master')
      expect(master).toHaveTextContent(en['nav.home'])
      expect(master).toHaveTextContent(en['profile.master'])
    })
  })
})
