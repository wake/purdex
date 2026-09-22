import { describe, it, expect, beforeEach } from 'vitest'
import { useNewTabLayoutStore, makePreset, healPresetState } from './useNewTabLayoutStore'
import type { LayoutPreset } from './useNewTabLayoutStore'
import { buildSettingsSection } from '../lib/profile/sections'

// helper for tests
function initialStatePresets() {
  return {
    '3col': makePreset(false, 3),
    '2col': makePreset(false, 2),
    '1col': makePreset(true, 1),
  }
}

beforeEach(() => {
  useNewTabLayoutStore.setState(useNewTabLayoutStore.getInitialState(), true)
})

describe('useNewTabLayoutStore', () => {
  describe('initial state', () => {
    it('presets have correct column counts', () => {
      const { profiles } = useNewTabLayoutStore.getState()
      expect(profiles['3col'].columns).toHaveLength(3)
      expect(profiles['2col'].columns).toHaveLength(2)
      expect(profiles['1col'].columns).toHaveLength(1)
    })

    it('only 1col enabled by default', () => {
      const { profiles } = useNewTabLayoutStore.getState()
      expect(profiles['1col'].enabled).toBe(true)
      expect(profiles['2col'].enabled).toBe(false)
      expect(profiles['3col'].enabled).toBe(false)
    })

    it('activeEditingProfile default 1col; knownIds empty', () => {
      const s = useNewTabLayoutStore.getState()
      expect(s.activeEditingProfile).toBe('1col')
      expect(s.knownIds).toEqual([])
    })
  })

  describe('setEnabled', () => {
    it('toggles 3col and 2col', () => {
      useNewTabLayoutStore.getState().setEnabled('3col', true)
      expect(useNewTabLayoutStore.getState().profiles['3col'].enabled).toBe(true)
      useNewTabLayoutStore.getState().setEnabled('2col', true)
      expect(useNewTabLayoutStore.getState().profiles['2col'].enabled).toBe(true)
    })

    it('ignores disable on 1col', () => {
      useNewTabLayoutStore.getState().setEnabled('1col', false)
      expect(useNewTabLayoutStore.getState().profiles['1col'].enabled).toBe(true)
    })
  })

  describe('setEditing', () => {
    it('switches active editing preset', () => {
      useNewTabLayoutStore.getState().setEditing('3col')
      expect(useNewTabLayoutStore.getState().activeEditingProfile).toBe('3col')
    })
  })

  describe('placeModule', () => {
    it('inserts into empty column', () => {
      useNewTabLayoutStore.getState().placeModule('1col', 'a', 0, 0)
      expect(useNewTabLayoutStore.getState().profiles['1col'].columns[0]).toEqual(['a'])
    })

    it('appends to non-empty column (rowIdx beyond length clamps to end)', () => {
      useNewTabLayoutStore.getState().placeModule('1col', 'a', 0, 0)
      useNewTabLayoutStore.getState().placeModule('1col', 'b', 0, 99)
      expect(useNewTabLayoutStore.getState().profiles['1col'].columns[0]).toEqual(['a', 'b'])
    })

    it('moving same-column downward compensates for index shift', () => {
      // initial: col0 = [a, b, c, d]
      const s = useNewTabLayoutStore.getState()
      s.placeModule('1col', 'a', 0, 0)
      s.placeModule('1col', 'b', 0, 1)
      s.placeModule('1col', 'c', 0, 2)
      s.placeModule('1col', 'd', 0, 3)
      // move a to index 2 (after b, before c) → [b, a, c, d]
      s.placeModule('1col', 'a', 0, 2)
      expect(useNewTabLayoutStore.getState().profiles['1col'].columns[0]).toEqual(['b', 'a', 'c', 'd'])
    })

    it('moving same-column to end places at true end (no compensation needed)', () => {
      const s = useNewTabLayoutStore.getState()
      s.placeModule('1col', 'a', 0, 0)
      s.placeModule('1col', 'b', 0, 1)
      s.placeModule('1col', 'c', 0, 2)
      // move a to end (toRow = 3)
      s.placeModule('1col', 'a', 0, 3)
      expect(useNewTabLayoutStore.getState().profiles['1col'].columns[0]).toEqual(['b', 'c', 'a'])
    })

    it('cross-column move removes from source and inserts at target', () => {
      useNewTabLayoutStore.setState((state) => ({
        profiles: {
          ...state.profiles,
          '3col': { enabled: false, columns: [['a', 'b'], ['c'], []] },
        },
      }))
      useNewTabLayoutStore.getState().placeModule('3col', 'a', 2, 0)
      const cols = useNewTabLayoutStore.getState().profiles['3col'].columns
      expect(cols[0]).toEqual(['b'])
      expect(cols[2]).toEqual(['a'])
    })

    it('cross-preset placement is independent', () => {
      useNewTabLayoutStore.getState().placeModule('1col', 'x', 0, 0)
      useNewTabLayoutStore.getState().placeModule('2col', 'x', 0, 0)
      const s = useNewTabLayoutStore.getState()
      expect(s.profiles['1col'].columns[0]).toContain('x')
      expect(s.profiles['2col'].columns[0]).toContain('x')
    })

    it('negative rowIdx clamps to 0', () => {
      useNewTabLayoutStore.getState().placeModule('1col', 'a', 0, -5)
      expect(useNewTabLayoutStore.getState().profiles['1col'].columns[0]).toEqual(['a'])
    })
  })

  describe('placeModuleInShortest', () => {
    it('places in column 0 when all columns empty', () => {
      useNewTabLayoutStore.getState().placeModuleInShortest('3col', 'a')
      expect(useNewTabLayoutStore.getState().profiles['3col'].columns[0]).toEqual(['a'])
    })

    it('places in the shortest column (ties pick first)', () => {
      useNewTabLayoutStore.setState((state) => ({
        profiles: { ...state.profiles, '3col': { enabled: false, columns: [['a'], [], ['c']] } },
      }))
      useNewTabLayoutStore.getState().placeModuleInShortest('3col', 'b')
      expect(useNewTabLayoutStore.getState().profiles['3col'].columns[1]).toEqual(['b'])
    })

    it('appends to end of shortest column', () => {
      useNewTabLayoutStore.setState((state) => ({
        profiles: { ...state.profiles, '2col': { enabled: false, columns: [['x'], ['y']] } },
      }))
      useNewTabLayoutStore.getState().placeModuleInShortest('2col', 'z')
      const cols = useNewTabLayoutStore.getState().profiles['2col'].columns
      // shortest-first ties → col 0, so appends there
      expect(cols[0]).toEqual(['x', 'z'])
    })
  })

  describe('removeModule', () => {
    it('removes from all occurrences in a preset', () => {
      useNewTabLayoutStore.setState((state) => ({
        profiles: {
          ...state.profiles,
          '3col': { enabled: false, columns: [['a'], ['b'], ['c']] },
        },
      }))
      useNewTabLayoutStore.getState().removeModule('3col', 'b')
      expect(useNewTabLayoutStore.getState().profiles['3col'].columns[1]).toEqual([])
    })

    it('is a no-op when id not present', () => {
      const before = useNewTabLayoutStore.getState().profiles
      useNewTabLayoutStore.getState().removeModule('1col', 'nope')
      expect(useNewTabLayoutStore.getState().profiles).toEqual(before)
    })
  })

  describe('ensureDefaults', () => {
    it('populates shortest column of EVERY preset on first call', () => {
      useNewTabLayoutStore.getState().ensureDefaults([
        { id: 'a', order: 0 },
        { id: 'b', order: 1 },
        { id: 'c', order: 2 },
      ])
      const { profiles, knownIds } = useNewTabLayoutStore.getState()
      // 1col: all go to the single column
      expect(profiles['1col'].columns[0]).toEqual(['a', 'b', 'c'])
      // 2col: shortest-first: ['a','c'] / ['b']
      expect(profiles['2col'].columns[0]).toEqual(['a', 'c'])
      expect(profiles['2col'].columns[1]).toEqual(['b'])
      // 3col: shortest-first: ['a'] / ['b'] / ['c']
      expect(profiles['3col'].columns[0]).toEqual(['a'])
      expect(profiles['3col'].columns[1]).toEqual(['b'])
      expect(profiles['3col'].columns[2]).toEqual(['c'])
      expect(knownIds).toEqual(['a', 'b', 'c'])
    })

    it('skips providers with disabled=true', () => {
      useNewTabLayoutStore.getState().ensureDefaults([
        { id: 'a', order: 0 },
        { id: 'b', order: 1, disabled: true },
      ])
      const { profiles, knownIds } = useNewTabLayoutStore.getState()
      expect(knownIds).toEqual(['a'])
      expect(profiles['1col'].columns[0]).toEqual(['a'])
    })

    it('does not re-add ids already in knownIds (user removal persists)', () => {
      useNewTabLayoutStore.getState().ensureDefaults([{ id: 'a', order: 0 }])
      useNewTabLayoutStore.getState().removeModule('1col', 'a')
      useNewTabLayoutStore.getState().ensureDefaults([{ id: 'a', order: 0 }])
      expect(useNewTabLayoutStore.getState().profiles['1col'].columns[0]).toEqual([])
    })

    it('does not prune ids whose provider disappeared (render-time skip)', () => {
      useNewTabLayoutStore.getState().ensureDefaults([{ id: 'a', order: 0 }])
      useNewTabLayoutStore.getState().ensureDefaults([]) // a removed from registry
      expect(useNewTabLayoutStore.getState().profiles['1col'].columns[0]).toEqual(['a'])
    })

    it('respects order ascending', () => {
      useNewTabLayoutStore.getState().ensureDefaults([
        { id: 'b', order: 5 },
        { id: 'a', order: -10 },
      ])
      expect(useNewTabLayoutStore.getState().knownIds).toEqual(['a', 'b'])
    })
  })

  describe('pruneIds', () => {
    it('removes ids from every preset and from knownIds', () => {
      const s = useNewTabLayoutStore.getState()
      s.ensureDefaults([{ id: 'a', order: 0 }, { id: 'b', order: 1 }, { id: 'c', order: 2 }])
      s.setEnabled('3col', true)
      useNewTabLayoutStore.getState().pruneIds(['b', 'missing'])
      const next = useNewTabLayoutStore.getState()
      expect(next.knownIds).toEqual(['a', 'c'])
      for (const key of ['3col', '2col', '1col'] as const) {
        expect(next.profiles[key].columns.flat()).not.toContain('b')
        expect(next.profiles[key].columns.flat()).toEqual(expect.arrayContaining(['a', 'c']))
      }
      expect(next.profiles['3col'].enabled).toBe(true)
    })

    it('is a no-op (same state object) when no id is present', () => {
      useNewTabLayoutStore.getState().ensureDefaults([{ id: 'a', order: 0 }])
      const before = useNewTabLayoutStore.getState().profiles
      useNewTabLayoutStore.getState().pruneIds(['zzz'])
      expect(useNewTabLayoutStore.getState().profiles).toBe(before)
    })

    it('lets a pruned id be re-added by a later ensureDefaults', () => {
      useNewTabLayoutStore.getState().ensureDefaults([{ id: 'a', order: 0 }])
      useNewTabLayoutStore.getState().pruneIds(['a'])
      useNewTabLayoutStore.getState().ensureDefaults([{ id: 'a', order: 0 }])
      expect(useNewTabLayoutStore.getState().profiles['1col'].columns[0]).toEqual(['a'])
    })
  })

  describe('migrateId', () => {
    it('replaces a placed id in place with the targets, in every preset', () => {
      useNewTabLayoutStore.setState({
        profiles: {
          '3col': { enabled: true, columns: [['x'], ['old', 'y'], []] },
          '2col': { enabled: false, columns: [['y'], ['old']] },
          '1col': { enabled: true, columns: [['x', 'old', 'y']] },
        },
        knownIds: ['x', 'old', 'y'],
      })
      useNewTabLayoutStore.getState().migrateId('old', ['n1', 'n2'])
      const s = useNewTabLayoutStore.getState()
      expect(s.profiles['3col'].columns).toEqual([['x'], ['n1', 'n2', 'y'], []])
      expect(s.profiles['2col'].columns).toEqual([['y'], ['n1', 'n2']])
      expect(s.profiles['1col'].columns).toEqual([['x', 'n1', 'n2', 'y']])
      expect(s.knownIds).toEqual(['x', 'y', 'n1', 'n2'])
      expect(s.profiles['3col'].enabled).toBe(true)
    })

    it('marks targets known but unplaced when the old id was only known (user removed it)', () => {
      useNewTabLayoutStore.setState({
        profiles: {
          '3col': { enabled: false, columns: [[], [], []] },
          '2col': { enabled: false, columns: [[], []] },
          '1col': { enabled: true, columns: [['x']] },
        },
        knownIds: ['x', 'old'],
      })
      useNewTabLayoutStore.getState().migrateId('old', ['n1', 'n2'])
      const s = useNewTabLayoutStore.getState()
      expect(s.profiles['1col'].columns).toEqual([['x']])
      expect(s.knownIds).toEqual(['x', 'n1', 'n2'])
    })

    it('does not duplicate a target already placed or known', () => {
      useNewTabLayoutStore.setState({
        profiles: {
          '3col': { enabled: false, columns: [[], [], []] },
          '2col': { enabled: false, columns: [[], []] },
          '1col': { enabled: true, columns: [['n1', 'old']] },
        },
        knownIds: ['n1', 'old'],
      })
      useNewTabLayoutStore.getState().migrateId('old', ['n1', 'n2'])
      const s = useNewTabLayoutStore.getState()
      expect(s.profiles['1col'].columns).toEqual([['n1', 'n2']])
      expect(s.knownIds).toEqual(['n1', 'n2'])
    })

    it('is a no-op (same state) when the old id is absent', () => {
      useNewTabLayoutStore.getState().ensureDefaults([{ id: 'a', order: 0 }])
      const before = useNewTabLayoutStore.getState()
      useNewTabLayoutStore.getState().migrateId('old', ['n1'])
      expect(useNewTabLayoutStore.getState().profiles).toBe(before.profiles)
      expect(useNewTabLayoutStore.getState().knownIds).toBe(before.knownIds)
    })
  })

  describe('reset', () => {
    it('restores initial state', () => {
      useNewTabLayoutStore.getState().setEnabled('3col', true)
      useNewTabLayoutStore.getState().placeModule('1col', 'x', 0, 0)
      useNewTabLayoutStore.getState().reset()
      const s = useNewTabLayoutStore.getState()
      expect(s.profiles['3col'].enabled).toBe(false)
      expect(s.profiles['1col'].columns[0]).toEqual([])
      expect(s.knownIds).toEqual([])
    })
  })
})

describe('makePreset', () => {
  it('creates a preset with N empty columns', () => {
    const p = makePreset(true, 3)
    expect(p).toEqual({ enabled: true, columns: [[], [], []] })
    expect(p.columns).toHaveLength(3)
  })
})

describe('healPresetState', () => {
  it('is a no-op on well-formed state', () => {
    const s = {
      profiles: {
        '3col': makePreset(false, 3),
        '2col': makePreset(false, 2),
        '1col': makePreset(true, 1),
      },
      knownIds: ['x'],
      activeEditingProfile: '1col' as const,
    }
    const before = JSON.parse(JSON.stringify(s))
    healPresetState(s)
    expect(s).toEqual(before)
  })

  it('restores 1col.enabled=true if corrupted', () => {
    const s = {
      profiles: {
        '3col': makePreset(true, 3),
        '2col': makePreset(false, 2),
        '1col': makePreset(false, 1),
      },
      knownIds: [],
      activeEditingProfile: '1col' as const,
    }
    healPresetState(s)
    expect(s.profiles['1col'].enabled).toBe(true)
  })

  it('resets missing preset key to defaults', () => {
    const s = {
      profiles: {
        '3col': makePreset(false, 3),
        '2col': makePreset(false, 2),
      } as unknown as Record<string, LayoutPreset>,
      knownIds: [],
      activeEditingProfile: '1col' as const,
    }
    healPresetState(s)
    expect(s.profiles['1col']).toEqual({ enabled: true, columns: [[]] })
  })

  it('resets preset with wrong columns length', () => {
    const s = {
      profiles: {
        '3col': { enabled: true, columns: [[], []] },
        '2col': makePreset(false, 2),
        '1col': makePreset(true, 1),
      },
      knownIds: [],
      activeEditingProfile: '1col' as const,
    }
    healPresetState(s)
    expect(s.profiles['3col'].columns).toHaveLength(3)
    expect(s.profiles['3col'].enabled).toBe(false) // reset to default
  })

  it('coerces non-array columns to empty array', () => {
    const s = {
      profiles: {
        '3col': {
          enabled: false,
          columns: ['not an array', [], []] as unknown as string[][],
        },
        '2col': makePreset(false, 2),
        '1col': makePreset(true, 1),
      },
      knownIds: [],
      activeEditingProfile: '1col' as const,
    }
    healPresetState(s)
    expect(s.profiles['3col'].columns[0]).toEqual([])
  })

  it('strips non-string entries from columns', () => {
    const s = {
      profiles: {
        '3col': {
          enabled: false,
          columns: [['a', 42, null, 'b'] as unknown as string[], [], []],
        },
        '2col': makePreset(false, 2),
        '1col': makePreset(true, 1),
      },
      knownIds: [],
      activeEditingProfile: '1col' as const,
    }
    healPresetState(s)
    expect(s.profiles['3col'].columns[0]).toEqual(['a', 'b'])
  })

  it('resets knownIds to [] if not an array', () => {
    const s = {
      profiles: initialStatePresets(),
      knownIds: 'bad' as unknown as string[],
      activeEditingProfile: '1col' as const,
    }
    healPresetState(s)
    expect(s.knownIds).toEqual([])
  })

  it('resets invalid activeEditingProfile to 1col', () => {
    const s = {
      profiles: initialStatePresets(),
      knownIds: [],
      activeEditingProfile: 'bogus' as unknown as '1col',
    }
    healPresetState(s)
    expect(s.activeEditingProfile).toBe('1col')
  })
})

// Profile Sync P3e PR-A — zero-byte proof. Renaming the new-tab "profile"
// concept to "preset" must not change a byte of what this store writes to
// localStorage, nor of what the settings section sends for it. Both expected
// strings were produced by main's code before the rename (fields still named
// `profiles` / `activeEditingProfile`, persist version 1). PR-B changes them on
// purpose and must update these strings in the same commit.
describe('persisted and synced bytes (P3e PR-A zero-change proof)', () => {
  const fixture = {
    profiles: {
      '3col': { enabled: true, columns: [['a'], ['b', 'c'], []] },
      '2col': { enabled: false, columns: [['a', 'b'], ['c']] },
      '1col': { enabled: true, columns: [['c', 'a', 'b']] },
    },
    knownIds: ['a', 'b', 'c'],
    activeEditingProfile: '2col' as const,
  }

  it('localStorage JSON is byte-identical', () => {
    useNewTabLayoutStore.setState(fixture)
    expect(localStorage.getItem('purdex-newtab-layout')).toBe(
      '{"state":{"profiles":{"3col":{"enabled":true,"columns":[["a"],["b","c"],[]]},"2col":{"enabled":false,"columns":[["a","b"],["c"]]},"1col":{"enabled":true,"columns":[["c","a","b"]]}},"knownIds":["a","b","c"],"activeEditingProfile":"2col"},"version":1}',
    )
  })

  it('settings-section projection of purdex-newtab-layout is byte-identical', () => {
    useNewTabLayoutStore.setState(fixture)
    const payload = buildSettingsSection({ 'purdex-newtab-layout': useNewTabLayoutStore.getState() }, new Set())
    expect(JSON.stringify(payload['purdex-newtab-layout'])).toBe(
      '{"profiles":{"3col":{"enabled":true,"columns":[["a"],["b","c"],[]]},"2col":{"enabled":false,"columns":[["a","b"],["c"]]},"1col":{"enabled":true,"columns":[["c","a","b"]]}}}',
    )
  })
})
