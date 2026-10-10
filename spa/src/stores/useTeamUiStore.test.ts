// spa/src/stores/useTeamUiStore.test.ts — the person's per-team arrangement on this device (order, collapse, panel mode,
// ghost workspace): persisted under purdex-team-ui, never synced; pruned only by a roster frame that omits a team or by
// deleting the host.
import { describe, it, expect, beforeEach } from 'vitest'
import { useTeamUiStore } from './useTeamUiStore'
import { useUISettingsStore } from './useUISettingsStore'
import { teamKeyOf } from '../lib/team/team-views'

const k = (host: string, team: string) => teamKeyOf(host, team)

beforeEach(() => {
  localStorage.clear()
  useUISettingsStore.setState({ tabIndicatorStyle: 'badge', hostBadgeSidebarBox: 16 })
  useTeamUiStore.setState({ memberOrder: {}, collapsed: {}, panelMode: {}, panelLast: {}, sharedPanelMode: 'titlebar', sharedPanelLast: 'full', legacyMax: false, ghostWorkspace: {}, teamDrill: {}, workbookTabs: {}, panel: { width: 312 }, teamBeadHost: true })
})

describe('useTeamUiStore', () => {
  it('persists under purdex-team-ui and round-trips', () => {
    expect(useTeamUiStore.persist.getOptions().name).toBe('purdex-team-ui')
    const key = k('h1', 't1')
    useTeamUiStore.getState().setMemberOrder(key, ['b', 'a'])
    useTeamUiStore.getState().setCollapsed(key, true)
    useTeamUiStore.getState().setPanelMode(key, 'line')
    useTeamUiStore.getState().setGhostWorkspace(key, 'w9')
    const raw = JSON.parse(localStorage.getItem('purdex-team-ui')!)
    expect(raw.state).toEqual({
      memberOrder: { [key]: ['b', 'a'] }, collapsed: { [key]: true }, panelMode: { [key]: 'line' }, ghostWorkspace: { [key]: 'w9' },
      teamBeadHost: true, teamDrill: {}, panel: { width: 312 }, workbookTabs: {},
      panelLast: { [key]: 'line' }, sharedPanelMode: 'titlebar', sharedPanelLast: 'full',
    })
    const saved = localStorage.getItem('purdex-team-ui')!
    useTeamUiStore.setState({ memberOrder: {}, collapsed: {}, panelMode: {}, ghostWorkspace: {} }) // persists the empty state too
    localStorage.setItem('purdex-team-ui', saved)
    useTeamUiStore.persist.rehydrate()
    expect(useTeamUiStore.getState().memberOrder[key]).toEqual(['b', 'a'])
    expect(useTeamUiStore.getState().panelMode[key]).toBe('line')
  })

  it('the panel mode is absent (= full) until set, and "full" removes the entry', () => {
    const key = k('h1', 't1')
    expect(useTeamUiStore.getState().panelMode[key]).toBeUndefined()
    useTeamUiStore.getState().setPanelMode(key, 'line')
    useTeamUiStore.getState().setPanelMode(key, 'full')
    expect(key in useTeamUiStore.getState().panelMode).toBe(false)
  })

  it('collapsed false removes the entry; a ghost workspace can be cleared', () => {
    const key = k('h1', 't1')
    useTeamUiStore.getState().setCollapsed(key, true)
    useTeamUiStore.getState().setCollapsed(key, false)
    expect(key in useTeamUiStore.getState().collapsed).toBe(false)
    useTeamUiStore.getState().setGhostWorkspace(key, 'w1')
    useTeamUiStore.getState().setGhostWorkspace(key, null)
    expect(key in useTeamUiStore.getState().ghostWorkspace).toBe(false)
  })

  it('memberOrder keeps its identity through setCollapsed / setPanelMode / setGhostWorkspace', () => {
    const key = k('h1', 't1')
    useTeamUiStore.getState().setMemberOrder(key, ['a'])
    const before = useTeamUiStore.getState().memberOrder
    useTeamUiStore.getState().setCollapsed(key, true)
    useTeamUiStore.getState().setPanelMode(key, 'line')
    useTeamUiStore.getState().setGhostWorkspace(key, 'w1')
    expect(useTeamUiStore.getState().memberOrder).toBe(before)
  })

  it('setting the same order again changes nothing (no new identity)', () => {
    const key = k('h1', 't1')
    useTeamUiStore.getState().setMemberOrder(key, ['a', 'b'])
    const before = useTeamUiStore.getState().memberOrder
    useTeamUiStore.getState().setMemberOrder(key, ['a', 'b'])
    expect(useTeamUiStore.getState().memberOrder).toBe(before)
  })

  describe('forgetTeams(hostId, liveKeys)', () => {
    it('drops the host\'s teams that are not live, keeps the live ones and other hosts', () => {
      const live = k('h1', 'live'), gone = k('h1', 'gone'), other = k('h2', 'x')
      for (const key of [live, gone, other]) {
        useTeamUiStore.getState().setMemberOrder(key, ['a'])
        useTeamUiStore.getState().setCollapsed(key, true)
        useTeamUiStore.getState().setPanelMode(key, 'line')
        useTeamUiStore.getState().setGhostWorkspace(key, 'w')
      }
      useTeamUiStore.getState().forgetTeams('h1', [live])
      const s = useTeamUiStore.getState()
      for (const slice of [s.memberOrder, s.collapsed, s.panelMode, s.ghostWorkspace]) {
        expect(Object.keys(slice).sort()).toEqual([live, other].sort())
      }
    })
    it('leaves every slice untouched (same identity) when nothing is stale', () => {
      const key = k('h1', 't1')
      useTeamUiStore.getState().setMemberOrder(key, ['a'])
      useTeamUiStore.getState().setCollapsed(key, true)
      const before = useTeamUiStore.getState()
      useTeamUiStore.getState().forgetTeams('h1', [key])
      const after = useTeamUiStore.getState()
      expect(after.memberOrder).toBe(before.memberOrder)
      expect(after.collapsed).toBe(before.collapsed)
    })
    it('does not confuse a host id that is a prefix of another', () => {
      const a = k('h1', 't'), b = k('h10', 't')
      useTeamUiStore.getState().setCollapsed(a, true)
      useTeamUiStore.getState().setCollapsed(b, true)
      useTeamUiStore.getState().forgetTeams('h1', [])
      expect(Object.keys(useTeamUiStore.getState().collapsed)).toEqual([b])
    })
  })

  it('forgetHostTeams drops every team of that host and only that host', () => {
    const a = k('h1', 't1'), b = k('h1', 't2'), c = k('h2', 't1')
    for (const key of [a, b, c]) useTeamUiStore.getState().setMemberOrder(key, ['x'])
    useTeamUiStore.getState().forgetHostTeams('h1')
    expect(Object.keys(useTeamUiStore.getState().memberOrder)).toEqual([c])
  })

  it('snapshotHostTeams / restoreHostTeams carry a host\'s entries through a delete + undo', () => {
    const a = k('h1', 't1'), c = k('h2', 't1')
    useTeamUiStore.getState().setMemberOrder(a, ['x'])
    useTeamUiStore.getState().setCollapsed(a, true)
    useTeamUiStore.getState().setMemberOrder(c, ['y'])
    const snap = useTeamUiStore.getState().snapshotHostTeams('h1')
    useTeamUiStore.getState().forgetHostTeams('h1')
    useTeamUiStore.getState().restoreHostTeams(snap)
    const s = useTeamUiStore.getState()
    expect(s.memberOrder[a]).toEqual(['x'])
    expect(s.collapsed[a]).toBe(true)
    expect(s.memberOrder[c]).toEqual(['y'])
  })

  it('heals malformed persisted data to empty slices', () => {
    localStorage.setItem('purdex-team-ui', JSON.stringify({ state: { memberOrder: 'oops', collapsed: [1], panelMode: { k: 'weird' }, ghostWorkspace: { k: 5 } }, version: 0 }))
    useTeamUiStore.persist.rehydrate()
    const s = useTeamUiStore.getState()
    expect(s.memberOrder).toEqual({})
    expect(s.collapsed).toEqual({})
    expect(s.panelMode).toEqual({})
    expect(s.ghostWorkspace).toEqual({})
  })
})

describe('a stale persisted groupShadow (the trial ended 2026-10-10)', () => {
  it('is ignored: the blob still loads and the key is neither state nor re-persisted', () => {
    localStorage.setItem('purdex-team-ui', JSON.stringify({ state: { groupShadow: 'v2', teamBeadHost: false }, version: 0 }))
    useTeamUiStore.persist.rehydrate()
    expect(useTeamUiStore.getState().teamBeadHost).toBe(false)
    expect('groupShadow' in useTeamUiStore.getState()).toBe(false)
    useTeamUiStore.getState().setTeamBeadHost(true)
    expect(JSON.parse(localStorage.getItem('purdex-team-ui')!).state.groupShadow).toBeUndefined()
  })
})

describe('the bead host-icon setting (spec P7, device-local)', () => {
  it('defaults on, persists in purdex-team-ui, and is not part of any synced store', () => {
    expect(useTeamUiStore.getState().teamBeadHost).toBe(true)
    useTeamUiStore.getState().setTeamBeadHost(false)
    expect(JSON.parse(localStorage.getItem('purdex-team-ui')!).state.teamBeadHost).toBe(false)
    const saved = localStorage.getItem('purdex-team-ui')!
    useTeamUiStore.setState({ teamBeadHost: true })
    localStorage.setItem('purdex-team-ui', saved)
    useTeamUiStore.persist.rehydrate()
    expect(useTeamUiStore.getState().teamBeadHost).toBe(false)
  })
  it('a non-boolean persisted value heals to the default', () => {
    localStorage.setItem('purdex-team-ui', JSON.stringify({ state: { teamBeadHost: 'no' }, version: 0 }))
    useTeamUiStore.persist.rehydrate()
    expect(useTeamUiStore.getState().teamBeadHost).toBe(true)
  })
  it('setting the same value again does not notify', () => {
    let calls = 0
    const unsub = useTeamUiStore.subscribe(() => { calls++ })
    useTeamUiStore.getState().setTeamBeadHost(true)
    unsub()
    expect(calls).toBe(0)
  })
})

describe('the panel area (WA-2a)', () => {
  const saved = () => JSON.parse(localStorage.getItem('purdex-team-ui')!).state
  it('defaults to the minimum width (356 under the default light style: a lead + 3 members fit one header row)', () => {
    expect(useTeamUiStore.getInitialState().panel).toEqual({ width: 356, followsMin: true })
  })
  it('a drag that lands on the same width (or is clamped to it) still makes the width the person\'s', () => {
    useTeamUiStore.setState({ panel: { width: 356, followsMin: true } })
    useTeamUiStore.getState().setPanelWidth(100) // clamped back to 356
    expect(useTeamUiStore.getState().panel).toEqual({ width: 356, followsMin: false })
  })
  it('setPanelWidth clamps to the current minimum - 720 and rounds', () => {
    const { setPanelWidth } = useTeamUiStore.getState()
    setPanelWidth(100)
    expect(useTeamUiStore.getState().panel.width).toBe(356)
    setPanelWidth(5000)
    expect(useTeamUiStore.getState().panel.width).toBe(720)
    setPanelWidth(500.6)
    expect(useTeamUiStore.getState().panel.width).toBe(501)
    setPanelWidth(Number.NaN)
    expect(useTeamUiStore.getState().panel.width).toBe(501)
  })
  it('the width is saved and survives a reload', () => {
    useTeamUiStore.getState().setPanelWidth(500)
    expect(saved().panel).toEqual({ width: 500, followsMin: false })
    useTeamUiStore.setState({ panel: { width: 312 } })
    localStorage.setItem('purdex-team-ui', JSON.stringify({ state: { panel: { width: 500 } }, version: 0 }))
    useTeamUiStore.persist.rehydrate()
    expect(useTeamUiStore.getState().panel).toEqual({ width: 500, followsMin: false })
  })
  it('heal clamps a wild width and rejects bad types', () => {
    const load = (panel: unknown) => {
      localStorage.setItem('purdex-team-ui', JSON.stringify({ state: { panel }, version: 0 }))
      useTeamUiStore.persist.rehydrate()
      return useTeamUiStore.getState().panel
    }
    expect(load({ width: 9999 })).toEqual({ width: 720, followsMin: false })
    expect(load({ width: 3 })).toEqual({ width: 356, followsMin: true })
    expect(load({ width: 'wide' })).toEqual({ width: 356, followsMin: true })
    expect(load('oops')).toEqual({ width: 356, followsMin: true })
    expect(load(null)).toEqual({ width: 356, followsMin: true })
  })
  it('a width saved under the current minimum is lifted to it on load (heal uses the CURRENT minimum)', () => {
    const load = (panel: unknown) => {
      localStorage.setItem('purdex-team-ui', JSON.stringify({ state: { panel }, version: 0 }))
      useTeamUiStore.persist.rehydrate()
      return useTeamUiStore.getState().panel
    }
    for (const old of [280, 300, 312, 355]) expect(load({ width: old })).toEqual({ width: 356, followsMin: true })
    expect(load({ width: 356 })).toEqual({ width: 356, followsMin: true })
    expect(load({ width: 413 })).toEqual({ width: 413, followsMin: false })
    useUISettingsStore.getState().setTabIndicatorStyle('iconDot')
    expect(load({ width: 380 })).toEqual({ width: 412, followsMin: true })
  })
  describe('the minimum follows the light style and the host box', () => {
    const width = () => useTeamUiStore.getState().panel.width
    beforeEach(() => {
      useUISettingsStore.getState().setTabIndicatorStyle('badge')
      useUISettingsStore.getState().setHostBadgeSidebarBox(16)
      useTeamUiStore.setState({ panel: { width: 356 } })
    })
    it('a width at the minimum follows it badge -> iconDot -> badge', () => {
      useUISettingsStore.getState().setTabIndicatorStyle('iconDot')
      expect(width()).toBe(412)
      useUISettingsStore.getState().setTabIndicatorStyle('badge')
      expect(width()).toBe(356)
    })
    it('a width below the new minimum is pulled up to it', () => {
      useTeamUiStore.setState({ panel: { width: 380 } })
      useUISettingsStore.getState().setTabIndicatorStyle('iconDot')
      expect(width()).toBe(412)
    })
    it('a width the person widened past the minimum is kept, in both directions', () => {
      useTeamUiStore.getState().setPanelWidth(500)
      useUISettingsStore.getState().setTabIndicatorStyle('iconDot')
      expect(width()).toBe(500)
      useUISettingsStore.getState().setTabIndicatorStyle('badge')
      expect(width()).toBe(500)
    })
    it('a width the person dragged to exactly the OLD minimum of another style is kept (badge 356 -> dragged 412 -> iconDot -> badge)', () => {
      useTeamUiStore.getState().setPanelWidth(412)
      expect(useTeamUiStore.getState().panel.followsMin).toBe(false)
      useUISettingsStore.getState().setTabIndicatorStyle('iconDot')
      expect(width()).toBe(412)
      useUISettingsStore.getState().setTabIndicatorStyle('badge')
      expect(width()).toBe(412)
    })
    it('a never-dragged default keeps following (badge 356 -> iconDot 412 -> badge 356), and the flag survives the follow', () => {
      useUISettingsStore.getState().setTabIndicatorStyle('iconDot')
      expect(width()).toBe(412)
      expect(useTeamUiStore.getState().panel.followsMin).toBe(true)
      useUISettingsStore.getState().setTabIndicatorStyle('badge')
      expect(width()).toBe(356)
    })
    it('a saved store without the flag infers it once from width === the current minimum; the flag is persisted', () => {
      const load = (panel: unknown) => {
        localStorage.setItem('purdex-team-ui', JSON.stringify({ state: { panel }, version: 0 }))
        useTeamUiStore.persist.rehydrate()
        return useTeamUiStore.getState().panel
      }
      expect(load({ width: 356 }).followsMin).toBe(true)
      expect(load({ width: 500 }).followsMin).toBe(false)
      expect(load({ width: 500, followsMin: true }).followsMin).toBe(true)
      expect(load({ width: 412, followsMin: false })).toEqual({ width: 412, followsMin: false })
      useTeamUiStore.getState().setPanelWidth(600)
      expect(JSON.parse(localStorage.getItem('purdex-team-ui')!).state.panel).toEqual({ width: 600, followsMin: false })
    })
    it('a bigger host box raises the minimum too', () => {
      useUISettingsStore.getState().setHostBadgeSidebarBox(24)
      expect(width()).toBe(356 + 4 * 8)
    })
    it('setPanelWidth clamps to the current minimum', () => {
      useUISettingsStore.getState().setTabIndicatorStyle('iconDot')
      useTeamUiStore.getState().setPanelWidth(100)
      expect(width()).toBe(412)
    })
  })
  it('teamDrill and workbookTabs round-trip and heal', () => {
    const key = k('h1', 't1')
    useTeamUiStore.getState().setTeamDrill(key, { hostId: 'h1', sessionId: 's1' })
    useTeamUiStore.getState().setWorkbookTab('tab-1', true)
    expect(saved().teamDrill).toEqual({ [key]: { hostId: 'h1', sessionId: 's1' } })
    expect(saved().workbookTabs).toEqual({ 'tab-1': true })
    useTeamUiStore.getState().setTeamDrill(key, null)
    useTeamUiStore.getState().setWorkbookTab('tab-1', false)
    expect(useTeamUiStore.getState().teamDrill).toEqual({})
    expect(useTeamUiStore.getState().workbookTabs).toEqual({})
    localStorage.setItem('purdex-team-ui', JSON.stringify({ state: {
      teamDrill: { [k('h', 't')]: { hostId: 'h', sessionId: 's' }, bad1: { hostId: 1, sessionId: 's' }, bad2: 'x', bad3: { hostId: '', sessionId: 's' } },
      workbookTabs: { a: true, b: false, c: 'yes' },
    }, version: 0 }))
    useTeamUiStore.persist.rehydrate()
    expect(useTeamUiStore.getState().teamDrill).toEqual({ [k('h', 't')]: { hostId: 'h', sessionId: 's' } })
    expect(useTeamUiStore.getState().workbookTabs).toEqual({ a: true })
  })
  it('heal drops teamDrill entries with a malformed key, a cross-host value or a non-string session', () => {
    const ok = k('h1', 't1')
    localStorage.setItem('purdex-team-ui', JSON.stringify({ state: { teamDrill: {
      [ok]: { hostId: 'h1', sessionId: 's' },
      'no-separator': { hostId: 'no-separator', sessionId: 's' },
      [k('', 't')]: { hostId: '', sessionId: 's' },
      [k('h1', '')]: { hostId: 'h1', sessionId: 's' },
      [k('h2', 't2')]: { hostId: 'h1', sessionId: 's' },
      [k('h1', 't3')]: { hostId: 'h1', sessionId: 5 },
      [k('h1', 't4')]: { hostId: 'h1', sessionId: '' },
    } }, version: 0 }))
    useTeamUiStore.persist.rehydrate()
    expect(useTeamUiStore.getState().teamDrill).toEqual({ [ok]: { hostId: 'h1', sessionId: 's' } })
  })
  it('forgetTeams / forgetHostTeams drop the teamDrill of a team that is gone', () => {
    const live = k('h1', 'live'), gone = k('h1', 'gone'), other = k('h2', 'x')
    for (const key of [live, gone, other]) useTeamUiStore.getState().setTeamDrill(key, { hostId: 'h1', sessionId: 's' })
    useTeamUiStore.getState().forgetTeams('h1', [live])
    expect(Object.keys(useTeamUiStore.getState().teamDrill).sort()).toEqual([live, other].sort())
    useTeamUiStore.getState().forgetHostTeams('h1')
    expect(Object.keys(useTeamUiStore.getState().teamDrill)).toEqual([other])
  })
  it('snapshot / restore carry teamDrill', () => {
    const a = k('h1', 't1')
    useTeamUiStore.getState().setTeamDrill(a, { hostId: 'h1', sessionId: 's' })
    const snap = useTeamUiStore.getState().snapshotHostTeams('h1')
    useTeamUiStore.getState().forgetHostTeams('h1')
    expect(useTeamUiStore.getState().teamDrill).toEqual({})
    useTeamUiStore.getState().restoreHostTeams(snap)
    expect(useTeamUiStore.getState().teamDrill[a]).toEqual({ hostId: 'h1', sessionId: 's' })
  })
})

describe('the four states (WA-2a′)', () => {
  const key = k('h1', 't1')
  const st = () => useTeamUiStore.getState()
  const reload = (state: unknown) => {
    localStorage.setItem('purdex-team-ui', JSON.stringify({ state, version: 0 }))
    useTeamUiStore.persist.rehydrate()
  }

  it('a team starts in full; leaving the title bar goes back to the pane state it left', () => {
    expect(st().panelMode[key] ?? 'full').toBe('full')
    st().toggleTitleBar(key)
    expect(st().panelMode[key]).toBe('titlebar')
    st().toggleTitleBar(key)
    expect(key in st().panelMode).toBe(false) // full is the absence
    st().setPanelMode(key, 'max')
    st().setPanelMode(key, 'titlebar')
    expect(st().panelLast[key]).toBe('max')
    st().toggleTitleBar(key)
    expect(st().panelMode[key]).toBe('max')
    st().setPanelMode(key, 'line')
    st().toggleTitleBar(key)
    st().toggleTitleBar(key)
    expect(st().panelMode[key]).toBe('line')
  })

  it('each team remembers its own state and the shared (non-team) value starts in the title bar', () => {
    const other = k('h1', 't2')
    st().setPanelMode(key, 'titlebar')
    st().setPanelMode(other, 'max')
    expect(st().panelMode[key]).toBe('titlebar')
    expect(st().panelMode[other]).toBe('max')
    expect(st().sharedPanelMode).toBe('titlebar')
    st().setSharedPanelMode('line')
    st().setSharedPanelMode('titlebar')
    expect(st().sharedPanelLast).toBe('line')
    expect(st().panelMode[key]).toBe('titlebar')
  })

  it('the states survive a reload', () => {
    st().setPanelMode(key, 'max')
    st().setPanelMode(key, 'titlebar')
    st().setSharedPanelMode('full')
    const saved = localStorage.getItem('purdex-team-ui')!
    useTeamUiStore.setState({ panelMode: {}, panelLast: {}, sharedPanelMode: 'titlebar', sharedPanelLast: 'full' })
    localStorage.setItem('purdex-team-ui', saved)
    useTeamUiStore.persist.rehydrate()
    expect(st().panelMode[key]).toBe('titlebar')
    expect(st().panelLast[key]).toBe('max')
    expect(st().sharedPanelMode).toBe('full')
  })

  it('an old store maps line -> line, full -> full, and expanded:true waits for the team showing', () => {
    reload({ panelMode: { [key]: 'line' }, panel: { width: 500, expanded: true } })
    expect(st().panelMode[key]).toBe('line')
    expect(st().panel).toEqual({ width: 500, followsMin: false })
    expect(st().legacyMax).toBe(true)
    st().takeLegacyMax(k('h1', 't2'))
    expect(st().panelMode[k('h1', 't2')]).toBe('max')
    expect(st().panelMode[key]).toBe('line')
    expect(st().legacyMax).toBe(false)
    st().takeLegacyMax(k('h1', 't3')) // taken once
    expect(k('h1', 't3') in st().panelMode).toBe(false)
  })

  it('expanded:true with no team showing is dropped; not expanded asks for nothing', () => {
    reload({ panel: { width: 500, expanded: true } })
    st().takeLegacyMax(null)
    expect(st().legacyMax).toBe(false)
    expect(st().panelMode).toEqual({})
    reload({ panel: { width: 500, expanded: false } })
    expect(st().legacyMax).toBe(false)
  })

  it('heal drops bad values and never persists the legacy flag', () => {
    reload({ panelMode: { a: 'bogus', b: 3, c: 'max', d: 'full', e: 'titlebar' }, panelLast: { a: 'titlebar', b: 'x', c: 'max' }, sharedPanelMode: 'nope', sharedPanelLast: 'titlebar' })
    expect(st().panelMode).toEqual({ c: 'max', e: 'titlebar' })
    expect(st().panelLast).toEqual({ c: 'max' })
    expect(st().sharedPanelMode).toBe('titlebar')
    expect(st().sharedPanelLast).toBe('full')
    st().setPanelMode(key, 'line')
    expect('legacyMax' in JSON.parse(localStorage.getItem('purdex-team-ui')!).state).toBe(false)
  })

  it('the state and the way back are pruned and restored with the team', () => {
    st().setPanelMode(key, 'max')
    st().setPanelMode(key, 'titlebar')
    const snap = st().snapshotHostTeams('h1')
    st().forgetHostTeams('h1')
    expect(st().panelMode).toEqual({})
    expect(st().panelLast).toEqual({})
    st().restoreHostTeams(snap)
    expect(st().panelMode[key]).toBe('titlebar')
    expect(st().panelLast[key]).toBe('max')
  })
})
