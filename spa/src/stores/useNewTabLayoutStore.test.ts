import { describe, it, expect, beforeEach, vi } from 'vitest'
import { useNewTabLayoutStore, makePreset, healPresetState } from './useNewTabLayoutStore'
import type { LayoutPreset } from './useNewTabLayoutStore'
import { buildSettingsSection } from '../lib/profile/sections'
import { syncManager } from '../lib/storage'

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
      const { presets } = useNewTabLayoutStore.getState()
      expect(presets['3col'].columns).toHaveLength(3)
      expect(presets['2col'].columns).toHaveLength(2)
      expect(presets['1col'].columns).toHaveLength(1)
    })

    it('only 1col enabled by default', () => {
      const { presets } = useNewTabLayoutStore.getState()
      expect(presets['1col'].enabled).toBe(true)
      expect(presets['2col'].enabled).toBe(false)
      expect(presets['3col'].enabled).toBe(false)
    })

    it('activeEditingPreset default 1col; knownIds empty', () => {
      const s = useNewTabLayoutStore.getState()
      expect(s.activeEditingPreset).toBe('1col')
      expect(s.knownIds).toEqual([])
    })
  })

  describe('setEnabled', () => {
    it('toggles 3col and 2col', () => {
      useNewTabLayoutStore.getState().setEnabled('3col', true)
      expect(useNewTabLayoutStore.getState().presets['3col'].enabled).toBe(true)
      useNewTabLayoutStore.getState().setEnabled('2col', true)
      expect(useNewTabLayoutStore.getState().presets['2col'].enabled).toBe(true)
    })

    it('ignores disable on 1col', () => {
      useNewTabLayoutStore.getState().setEnabled('1col', false)
      expect(useNewTabLayoutStore.getState().presets['1col'].enabled).toBe(true)
    })
  })

  describe('setEditing', () => {
    it('switches active editing preset', () => {
      useNewTabLayoutStore.getState().setEditing('3col')
      expect(useNewTabLayoutStore.getState().activeEditingPreset).toBe('3col')
    })
  })

  describe('placeModule', () => {
    it('inserts into empty column', () => {
      useNewTabLayoutStore.getState().placeModule('1col', 'a', 0, 0)
      expect(useNewTabLayoutStore.getState().presets['1col'].columns[0]).toEqual(['a'])
    })

    it('appends to non-empty column (rowIdx beyond length clamps to end)', () => {
      useNewTabLayoutStore.getState().placeModule('1col', 'a', 0, 0)
      useNewTabLayoutStore.getState().placeModule('1col', 'b', 0, 99)
      expect(useNewTabLayoutStore.getState().presets['1col'].columns[0]).toEqual(['a', 'b'])
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
      expect(useNewTabLayoutStore.getState().presets['1col'].columns[0]).toEqual(['b', 'a', 'c', 'd'])
    })

    it('moving same-column to end places at true end (no compensation needed)', () => {
      const s = useNewTabLayoutStore.getState()
      s.placeModule('1col', 'a', 0, 0)
      s.placeModule('1col', 'b', 0, 1)
      s.placeModule('1col', 'c', 0, 2)
      // move a to end (toRow = 3)
      s.placeModule('1col', 'a', 0, 3)
      expect(useNewTabLayoutStore.getState().presets['1col'].columns[0]).toEqual(['b', 'c', 'a'])
    })

    it('cross-column move removes from source and inserts at target', () => {
      useNewTabLayoutStore.setState((state) => ({
        presets: {
          ...state.presets,
          '3col': { enabled: false, columns: [['a', 'b'], ['c'], []] },
        },
      }))
      useNewTabLayoutStore.getState().placeModule('3col', 'a', 2, 0)
      const cols = useNewTabLayoutStore.getState().presets['3col'].columns
      expect(cols[0]).toEqual(['b'])
      expect(cols[2]).toEqual(['a'])
    })

    it('cross-preset placement is independent', () => {
      useNewTabLayoutStore.getState().placeModule('1col', 'x', 0, 0)
      useNewTabLayoutStore.getState().placeModule('2col', 'x', 0, 0)
      const s = useNewTabLayoutStore.getState()
      expect(s.presets['1col'].columns[0]).toContain('x')
      expect(s.presets['2col'].columns[0]).toContain('x')
    })

    it('negative rowIdx clamps to 0', () => {
      useNewTabLayoutStore.getState().placeModule('1col', 'a', 0, -5)
      expect(useNewTabLayoutStore.getState().presets['1col'].columns[0]).toEqual(['a'])
    })
  })

  describe('placeModuleInShortest', () => {
    it('places in column 0 when all columns empty', () => {
      useNewTabLayoutStore.getState().placeModuleInShortest('3col', 'a')
      expect(useNewTabLayoutStore.getState().presets['3col'].columns[0]).toEqual(['a'])
    })

    it('places in the shortest column (ties pick first)', () => {
      useNewTabLayoutStore.setState((state) => ({
        presets: { ...state.presets, '3col': { enabled: false, columns: [['a'], [], ['c']] } },
      }))
      useNewTabLayoutStore.getState().placeModuleInShortest('3col', 'b')
      expect(useNewTabLayoutStore.getState().presets['3col'].columns[1]).toEqual(['b'])
    })

    it('appends to end of shortest column', () => {
      useNewTabLayoutStore.setState((state) => ({
        presets: { ...state.presets, '2col': { enabled: false, columns: [['x'], ['y']] } },
      }))
      useNewTabLayoutStore.getState().placeModuleInShortest('2col', 'z')
      const cols = useNewTabLayoutStore.getState().presets['2col'].columns
      // shortest-first ties → col 0, so appends there
      expect(cols[0]).toEqual(['x', 'z'])
    })
  })

  describe('removeModule', () => {
    it('removes from all occurrences in a preset', () => {
      useNewTabLayoutStore.setState((state) => ({
        presets: {
          ...state.presets,
          '3col': { enabled: false, columns: [['a'], ['b'], ['c']] },
        },
      }))
      useNewTabLayoutStore.getState().removeModule('3col', 'b')
      expect(useNewTabLayoutStore.getState().presets['3col'].columns[1]).toEqual([])
    })

    it('is a no-op when id not present', () => {
      const before = useNewTabLayoutStore.getState().presets
      useNewTabLayoutStore.getState().removeModule('1col', 'nope')
      expect(useNewTabLayoutStore.getState().presets).toEqual(before)
    })
  })

  describe('ensureDefaults', () => {
    it('populates shortest column of EVERY preset on first call', () => {
      useNewTabLayoutStore.getState().ensureDefaults([
        { id: 'a', order: 0 },
        { id: 'b', order: 1 },
        { id: 'c', order: 2 },
      ])
      const { presets, knownIds } = useNewTabLayoutStore.getState()
      // 1col: all go to the single column
      expect(presets['1col'].columns[0]).toEqual(['a', 'b', 'c'])
      // 2col: shortest-first: ['a','c'] / ['b']
      expect(presets['2col'].columns[0]).toEqual(['a', 'c'])
      expect(presets['2col'].columns[1]).toEqual(['b'])
      // 3col: shortest-first: ['a'] / ['b'] / ['c']
      expect(presets['3col'].columns[0]).toEqual(['a'])
      expect(presets['3col'].columns[1]).toEqual(['b'])
      expect(presets['3col'].columns[2]).toEqual(['c'])
      expect(knownIds).toEqual(['a', 'b', 'c'])
    })

    it('skips providers with disabled=true', () => {
      useNewTabLayoutStore.getState().ensureDefaults([
        { id: 'a', order: 0 },
        { id: 'b', order: 1, disabled: true },
      ])
      const { presets, knownIds } = useNewTabLayoutStore.getState()
      expect(knownIds).toEqual(['a'])
      expect(presets['1col'].columns[0]).toEqual(['a'])
    })

    it('does not re-add ids already in knownIds (user removal persists)', () => {
      useNewTabLayoutStore.getState().ensureDefaults([{ id: 'a', order: 0 }])
      useNewTabLayoutStore.getState().removeModule('1col', 'a')
      useNewTabLayoutStore.getState().ensureDefaults([{ id: 'a', order: 0 }])
      expect(useNewTabLayoutStore.getState().presets['1col'].columns[0]).toEqual([])
    })

    it('does not prune ids whose provider disappeared (render-time skip)', () => {
      useNewTabLayoutStore.getState().ensureDefaults([{ id: 'a', order: 0 }])
      useNewTabLayoutStore.getState().ensureDefaults([]) // a removed from registry
      expect(useNewTabLayoutStore.getState().presets['1col'].columns[0]).toEqual(['a'])
    })

    // PR #1406 attacker medium: the "already placed under another id" check runs inside the action, on the state it
    // writes — never on a snapshot taken before it.
    it('placedAs: a provider whose alternative id is already known or placed is not placed', () => {
      useNewTabLayoutStore.setState({
        presets: { '3col': makePreset(false, 3), '2col': { enabled: false, columns: [['sessions:d1_x'], []] }, '1col': makePreset(true, 1) },
        knownIds: ['headless:d1_x'],
      })
      const placedAs = (id: string) => id.replace(':h2', ':d1_x')
      useNewTabLayoutStore.getState().ensureDefaults([
        { id: 'sessions:h2', order: 0 },
        { id: 'headless:h2', order: 1 },
        { id: 'browser', order: 2 },
      ], placedAs)
      const s = useNewTabLayoutStore.getState()
      expect(s.knownIds).toEqual(['headless:d1_x', 'browser'])
      expect(s.presets['1col'].columns[0]).toEqual(['browser'])
    })

    // knownIds is device-local: a block placed in a preset that arrived by sync is not in it. Placed is placed.
    it('a provider already placed in a preset but not known is not placed again — and becomes known', () => {
      useNewTabLayoutStore.setState({
        presets: { '3col': { enabled: true, columns: [['browser'], ['sessions:h2'], []] }, '2col': { enabled: false, columns: [['sessions:h2'], []] }, '1col': { enabled: true, columns: [['sessions:h2']] } },
        knownIds: [],
      })
      useNewTabLayoutStore.getState().ensureDefaults([{ id: 'sessions:h2', order: 0 }])
      const s = useNewTabLayoutStore.getState()
      expect(s.presets['3col'].columns).toEqual([['browser'], ['sessions:h2'], []])
      expect(s.presets['1col'].columns).toEqual([['sessions:h2']])
      expect(s.knownIds).toEqual(['sessions:h2'])
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
        expect(next.presets[key].columns.flat()).not.toContain('b')
        expect(next.presets[key].columns.flat()).toEqual(expect.arrayContaining(['a', 'c']))
      }
      expect(next.presets['3col'].enabled).toBe(true)
    })

    it('is a no-op (same state object) when no id is present', () => {
      useNewTabLayoutStore.getState().ensureDefaults([{ id: 'a', order: 0 }])
      const before = useNewTabLayoutStore.getState().presets
      useNewTabLayoutStore.getState().pruneIds(['zzz'])
      expect(useNewTabLayoutStore.getState().presets).toBe(before)
    })

    it('lets a pruned id be re-added by a later ensureDefaults', () => {
      useNewTabLayoutStore.getState().ensureDefaults([{ id: 'a', order: 0 }])
      useNewTabLayoutStore.getState().pruneIds(['a'])
      useNewTabLayoutStore.getState().ensureDefaults([{ id: 'a', order: 0 }])
      expect(useNewTabLayoutStore.getState().presets['1col'].columns[0]).toEqual(['a'])
    })
  })

  describe('migrateId', () => {
    it('replaces a placed id in place with the targets, in every preset', () => {
      useNewTabLayoutStore.setState({
        presets: {
          '3col': { enabled: true, columns: [['x'], ['old', 'y'], []] },
          '2col': { enabled: false, columns: [['y'], ['old']] },
          '1col': { enabled: true, columns: [['x', 'old', 'y']] },
        },
        knownIds: ['x', 'old', 'y'],
      })
      useNewTabLayoutStore.getState().migrateId('old', ['n1', 'n2'])
      const s = useNewTabLayoutStore.getState()
      expect(s.presets['3col'].columns).toEqual([['x'], ['n1', 'n2', 'y'], []])
      expect(s.presets['2col'].columns).toEqual([['y'], ['n1', 'n2']])
      expect(s.presets['1col'].columns).toEqual([['x', 'n1', 'n2', 'y']])
      expect(s.knownIds).toEqual(['x', 'y', 'n1', 'n2'])
      expect(s.presets['3col'].enabled).toBe(true)
    })

    it('marks targets known but unplaced when the old id was only known (user removed it)', () => {
      useNewTabLayoutStore.setState({
        presets: {
          '3col': { enabled: false, columns: [[], [], []] },
          '2col': { enabled: false, columns: [[], []] },
          '1col': { enabled: true, columns: [['x']] },
        },
        knownIds: ['x', 'old'],
      })
      useNewTabLayoutStore.getState().migrateId('old', ['n1', 'n2'])
      const s = useNewTabLayoutStore.getState()
      expect(s.presets['1col'].columns).toEqual([['x']])
      expect(s.knownIds).toEqual(['x', 'n1', 'n2'])
    })

    it('does not duplicate a target already placed or known', () => {
      useNewTabLayoutStore.setState({
        presets: {
          '3col': { enabled: false, columns: [[], [], []] },
          '2col': { enabled: false, columns: [[], []] },
          '1col': { enabled: true, columns: [['n1', 'old']] },
        },
        knownIds: ['n1', 'old'],
      })
      useNewTabLayoutStore.getState().migrateId('old', ['n1', 'n2'])
      const s = useNewTabLayoutStore.getState()
      expect(s.presets['1col'].columns).toEqual([['n1', 'n2']])
      expect(s.knownIds).toEqual(['n1', 'n2'])
    })

    it('is a no-op (same state) when the old id is absent', () => {
      useNewTabLayoutStore.getState().ensureDefaults([{ id: 'a', order: 0 }])
      const before = useNewTabLayoutStore.getState()
      useNewTabLayoutStore.getState().migrateId('old', ['n1'])
      expect(useNewTabLayoutStore.getState().presets).toBe(before.presets)
      expect(useNewTabLayoutStore.getState().knownIds).toBe(before.knownIds)
    })
  })

  // Host ownership plan H1b T1 / §0.11: the re-resolve pass renames host-bearing column ids in every preset and in
  // knownIds. A rename whose target is already placed keeps the WIRE-form one (the sync-id rule of host settings; the build,
  // mapping column by column, would have sent twice).
  describe('renameIds', () => {
    const W = 'sessions:d1_aaaaaaaaaaaaaaaa'
    const map = (id: string) => (id === W ? 'sessions:loc1' : id === 'headless:d1_aaaaaaaaaaaaaaaa' ? 'headless:loc1' : id)

    it('renames in every preset and in knownIds, in place', () => {
      useNewTabLayoutStore.setState({
        presets: {
          '3col': { enabled: true, columns: [['browser'], [W], ['headless:d1_aaaaaaaaaaaaaaaa']] },
          '2col': { enabled: false, columns: [[W, 'browser'], []] },
          '1col': { enabled: true, columns: [['browser', W]] },
        },
        knownIds: ['browser', W, 'headless:d1_aaaaaaaaaaaaaaaa'],
      })
      useNewTabLayoutStore.getState().renameIds(map)
      const s = useNewTabLayoutStore.getState()
      expect(s.presets['3col']).toEqual({ enabled: true, columns: [['browser'], ['sessions:loc1'], ['headless:loc1']] })
      expect(s.presets['2col']).toEqual({ enabled: false, columns: [['sessions:loc1', 'browser'], []] })
      expect(s.presets['1col']).toEqual({ enabled: true, columns: [['browser', 'sessions:loc1']] })
      expect(s.knownIds).toEqual(['browser', 'sessions:loc1', 'headless:loc1'])
    })

    it('a target already placed in a preset: the WIRE-form one wins its place, whichever comes first (knownIds too)', () => {
      useNewTabLayoutStore.setState({
        presets: {
          '3col': { enabled: true, columns: [[W], ['sessions:loc1'], []] },
          '2col': { enabled: false, columns: [['sessions:loc1'], [W]] },
          '1col': { enabled: true, columns: [['browser', 'sessions:loc1', W]] },
        },
        knownIds: ['sessions:loc1', 'browser', W],
      })
      useNewTabLayoutStore.getState().renameIds(map)
      const s = useNewTabLayoutStore.getState()
      expect(s.presets['3col'].columns).toEqual([['sessions:loc1'], [], []])
      expect(s.presets['2col'].columns).toEqual([[], ['sessions:loc1']])
      expect(s.presets['1col'].columns).toEqual([['browser', 'sessions:loc1']])
      expect(s.knownIds).toEqual(['browser', 'sessions:loc1'])
    })

    it('duplicates of one form only: the first is kept', () => {
      const legacy = (id: string) => (id === 'sessions:old1' || id === 'sessions:old2' ? 'sessions:loc1' : id)
      useNewTabLayoutStore.setState({
        presets: { '3col': makePreset(false, 3), '2col': makePreset(false, 2), '1col': { enabled: true, columns: [['sessions:old2', 'browser', 'sessions:old1']] } },
        knownIds: [],
      })
      useNewTabLayoutStore.getState().renameIds(legacy)
      expect(useNewTabLayoutStore.getState().presets['1col'].columns).toEqual([['sessions:loc1', 'browser']])
    })

    it('nothing to rename → the same state object', () => {
      useNewTabLayoutStore.setState({
        presets: { '3col': makePreset(false, 3), '2col': makePreset(false, 2), '1col': { enabled: true, columns: [['browser', 'sessions:loc1']] } },
        knownIds: ['browser', 'sessions:loc1'],
      })
      const before = useNewTabLayoutStore.getState()
      useNewTabLayoutStore.getState().renameIds(map)
      expect(useNewTabLayoutStore.getState()).toBe(before)
    })

    it('an untouched preset keeps its identity', () => {
      const untouched = { enabled: false, columns: [['browser'], []] }
      useNewTabLayoutStore.setState({
        presets: { '3col': makePreset(false, 3), '2col': untouched, '1col': { enabled: true, columns: [[W]] } },
        knownIds: [W],
      })
      useNewTabLayoutStore.getState().renameIds(map)
      expect(useNewTabLayoutStore.getState().presets['2col']).toBe(untouched)
    })
  })

  describe('reset', () => {
    it('restores initial state', () => {
      useNewTabLayoutStore.getState().setEnabled('3col', true)
      useNewTabLayoutStore.getState().placeModule('1col', 'x', 0, 0)
      useNewTabLayoutStore.getState().reset()
      const s = useNewTabLayoutStore.getState()
      expect(s.presets['3col'].enabled).toBe(false)
      expect(s.presets['1col'].columns[0]).toEqual([])
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
      presets: {
        '3col': makePreset(false, 3),
        '2col': makePreset(false, 2),
        '1col': makePreset(true, 1),
      },
      knownIds: ['x'],
      activeEditingPreset: '1col' as const,
    }
    const before = JSON.parse(JSON.stringify(s))
    healPresetState(s)
    expect(s).toEqual(before)
  })

  it('restores 1col.enabled=true if corrupted', () => {
    const s = {
      presets: {
        '3col': makePreset(true, 3),
        '2col': makePreset(false, 2),
        '1col': makePreset(false, 1),
      },
      knownIds: [],
      activeEditingPreset: '1col' as const,
    }
    healPresetState(s)
    expect(s.presets['1col'].enabled).toBe(true)
  })

  it('resets missing preset key to defaults', () => {
    const s = {
      presets: {
        '3col': makePreset(false, 3),
        '2col': makePreset(false, 2),
      } as unknown as Record<string, LayoutPreset>,
      knownIds: [],
      activeEditingPreset: '1col' as const,
    }
    healPresetState(s)
    expect(s.presets['1col']).toEqual({ enabled: true, columns: [[]] })
  })

  it('resets preset with wrong columns length', () => {
    const s = {
      presets: {
        '3col': { enabled: true, columns: [[], []] },
        '2col': makePreset(false, 2),
        '1col': makePreset(true, 1),
      },
      knownIds: [],
      activeEditingPreset: '1col' as const,
    }
    healPresetState(s)
    expect(s.presets['3col'].columns).toHaveLength(3)
    expect(s.presets['3col'].enabled).toBe(false) // reset to default
  })

  it('coerces non-array columns to empty array', () => {
    const s = {
      presets: {
        '3col': {
          enabled: false,
          columns: ['not an array', [], []] as unknown as string[][],
        },
        '2col': makePreset(false, 2),
        '1col': makePreset(true, 1),
      },
      knownIds: [],
      activeEditingPreset: '1col' as const,
    }
    healPresetState(s)
    expect(s.presets['3col'].columns[0]).toEqual([])
  })

  it('strips non-string entries from columns', () => {
    const s = {
      presets: {
        '3col': {
          enabled: false,
          columns: [['a', 42, null, 'b'] as unknown as string[], [], []],
        },
        '2col': makePreset(false, 2),
        '1col': makePreset(true, 1),
      },
      knownIds: [],
      activeEditingPreset: '1col' as const,
    }
    healPresetState(s)
    expect(s.presets['3col'].columns[0]).toEqual(['a', 'b'])
  })

  it('resets knownIds to [] if not an array', () => {
    const s = {
      presets: initialStatePresets(),
      knownIds: 'bad' as unknown as string[],
      activeEditingPreset: '1col' as const,
    }
    healPresetState(s)
    expect(s.knownIds).toEqual([])
  })

  it('resets invalid activeEditingPreset to 1col', () => {
    const s = {
      presets: initialStatePresets(),
      knownIds: [],
      activeEditingPreset: 'bogus' as unknown as '1col',
    }
    healPresetState(s)
    expect(s.activeEditingPreset).toBe('1col')
  })
})

// Profile Sync P3e — the persisted and synced bytes, pinned. PR-A (the pure
// rename) had to leave both strings exactly as main produced them (fields
// `profiles` / `activeEditingProfile`, persist version 1). PR-B changes them on
// purpose: the fields are `presets` / `activeEditingPreset` and the blob is
// persist version 2 (the v1 → v2 migrate is tested below).
describe('persisted and synced bytes (P3e: pinned, changed on purpose by PR-B)', () => {
  const fixture = {
    presets: {
      '3col': { enabled: true, columns: [['a'], ['b', 'c'], []] },
      '2col': { enabled: false, columns: [['a', 'b'], ['c']] },
      '1col': { enabled: true, columns: [['c', 'a', 'b']] },
    },
    knownIds: ['a', 'b', 'c'],
    activeEditingPreset: '2col' as const,
  }

  it('localStorage JSON is exactly this', () => {
    useNewTabLayoutStore.setState(fixture)
    expect(localStorage.getItem('purdex-newtab-layout')).toBe(
      '{"state":{"presets":{"3col":{"enabled":true,"columns":[["a"],["b","c"],[]]},"2col":{"enabled":false,"columns":[["a","b"],["c"]]},"1col":{"enabled":true,"columns":[["c","a","b"]]}},"knownIds":["a","b","c"],"activeEditingPreset":"2col"},"version":2}',
    )
  })

  it('settings-section projection of purdex-newtab-layout is exactly this', () => {
    useNewTabLayoutStore.setState(fixture)
    const payload = buildSettingsSection({ 'purdex-newtab-layout': useNewTabLayoutStore.getState() }, new Set())
    expect(JSON.stringify(payload['purdex-newtab-layout'])).toBe(
      '{"presets":{"3col":{"enabled":true,"columns":[["a"],["b","c"],[]]},"2col":{"enabled":false,"columns":[["a","b"],["c"]]},"1col":{"enabled":true,"columns":[["c","a","b"]]}}}',
    )
  })
})

// Profile Sync P3e PR-B — persist v1 → v2: `profiles` → `presets`,
// `activeEditingProfile` → `activeEditingPreset`. Through the real
// `purdexStorage` and `persist.rehydrate()`, like the heal tests above.
describe('persist migrate v1 → v2 (P3e PR-B)', () => {
  const KEY = 'purdex-newtab-layout'
  type Key = '3col' | '2col' | '1col'
  type V1 = { profiles?: unknown; knownIds?: unknown; activeEditingProfile?: unknown; [k: string]: unknown }

  const layout = () => ({
    '3col': { enabled: true, columns: [['a'], ['b', 'c'], []] },
    '2col': { enabled: false, columns: [['a', 'b'], ['c']] },
    '1col': { enabled: true, columns: [['c', 'a', 'b']] },
  })
  const v1Blob = (state: V1) => JSON.stringify({ state, version: 1 })
  const picked = () => {
    const s = useNewTabLayoutStore.getState()
    return { presets: s.presets, knownIds: s.knownIds, activeEditingPreset: s.activeEditingPreset }
  }

  // main's healProfileState (14b3db64), frozen: the healing a v1 blob got
  // before this PR, on the old field names. Mutates in place, as it did.
  function frozenV1Heal(state: V1): void {
    const COLS: Record<Key, number> = { '3col': 3, '2col': 2, '1col': 1 }
    const fresh = (enabled: boolean, n: number) => ({ enabled, columns: Array.from({ length: n }, () => [] as string[]) })
    if (!state.profiles || typeof state.profiles !== 'object') {
      state.profiles = { '3col': fresh(false, 3), '2col': fresh(false, 2), '1col': fresh(true, 1) }
    } else {
      const profiles = state.profiles as Record<string, { enabled: unknown; columns: unknown[] } | undefined>
      for (const key of ['3col', '2col', '1col'] as const) {
        const expectedLen = COLS[key]
        const p = profiles[key]
        if (!p || typeof p !== 'object' || !Array.isArray(p.columns) || p.columns.length !== expectedLen) {
          profiles[key] = fresh(key === '1col', expectedLen)
          continue
        }
        for (let i = 0; i < p.columns.length; i++) {
          const col = p.columns[i]
          p.columns[i] = Array.isArray(col) ? col.filter((s): s is string => typeof s === 'string') : []
        }
        if (typeof p.enabled !== 'boolean') p.enabled = false
      }
      if (profiles['1col']!.enabled !== true) profiles['1col']!.enabled = true
    }
    if (!Array.isArray(state.knownIds)) state.knownIds = []
    else state.knownIds = state.knownIds.filter((s): s is string => typeof s === 'string')
    if (!['3col', '2col', '1col'].includes(state.activeEditingProfile as string)) state.activeEditingProfile = '1col'
  }

  /** What the store held after rehydrating a v1 blob BEFORE this PR (default shallow merge onto the initial state, then heal), under the new names. */
  function expectedFromV1(state: V1) {
    const init = useNewTabLayoutStore.getInitialState()
    const merged: V1 = {
      profiles: structuredClone(init.presets),
      knownIds: [...init.knownIds],
      activeEditingProfile: init.activeEditingPreset,
      ...structuredClone(state),
    }
    frozenV1Heal(merged)
    return { presets: merged.profiles, knownIds: merged.knownIds, activeEditingPreset: merged.activeEditingProfile }
  }

  async function rehydrateFrom(raw: string) {
    localStorage.setItem(KEY, raw)
    await useNewTabLayoutStore.persist.rehydrate()
  }

  beforeEach(() => localStorage.removeItem(KEY))

  it('a v1 blob lands under the new names, the same data', async () => {
    await rehydrateFrom(v1Blob({ profiles: layout(), knownIds: ['a', 'b', 'c'], activeEditingProfile: '3col' }))
    expect(picked()).toEqual({ presets: layout(), knownIds: ['a', 'b', 'c'], activeEditingPreset: '3col' })
    expect('profiles' in useNewTabLayoutStore.getState()).toBe(false)
    expect('activeEditingProfile' in useNewTabLayoutStore.getState()).toBe(false)
  })

  it('a v1 blob without activeEditingProfile gets activeEditingPreset 1col, presets equal', async () => {
    await rehydrateFrom(v1Blob({ profiles: layout(), knownIds: ['a'] }))
    expect(picked()).toEqual({ presets: layout(), knownIds: ['a'], activeEditingPreset: '1col' })
  })

  it('the written-back blob is v2 with the new names and no old key', async () => {
    await rehydrateFrom(v1Blob({ profiles: layout(), knownIds: ['a', 'b', 'c'], activeEditingProfile: '2col' }))
    const raw = localStorage.getItem(KEY)!
    expect(JSON.parse(raw)).toEqual({ state: { presets: layout(), knownIds: ['a', 'b', 'c'], activeEditingPreset: '2col' }, version: 2 })
    expect(raw).not.toContain('profiles')
    expect(raw).not.toContain('activeEditingProfile')
  })

  it('a v2 blob round-trips unchanged (no write-back)', async () => {
    const raw = JSON.stringify({ state: { presets: layout(), knownIds: ['a', 'b', 'c'], activeEditingPreset: '2col' }, version: 2 })
    await rehydrateFrom(raw)
    expect(localStorage.getItem(KEY)).toBe(raw)
    expect(picked()).toEqual({ presets: layout(), knownIds: ['a', 'b', 'c'], activeEditingPreset: '2col' })
  })

  it('a presets field already in a v1 blob wins over profiles', async () => {
    const other = { ...layout(), '2col': { enabled: true, columns: [['z'], []] } }
    await rehydrateFrom(v1Blob({ profiles: layout(), presets: other, knownIds: [] }))
    expect(useNewTabLayoutStore.getState().presets).toEqual(other)
  })

  describe('healing equivalence: migrate + healPresetState ≡ v1 healing, then rename', () => {
    const good = layout
    const fixtures: Array<[string, V1]> = [
      ['well-formed', { profiles: good(), knownIds: ['a', 'b', 'c'], activeEditingProfile: '2col' }],
      ['profiles is a string', { profiles: 'nope', knownIds: ['a'], activeEditingProfile: '3col' }],
      ['profiles is null', { profiles: null, knownIds: ['a'], activeEditingProfile: '3col' }],
      ['profiles is a number', { profiles: 5, knownIds: [], activeEditingProfile: '1col' }],
      ['profiles missing', { knownIds: ['a'], activeEditingProfile: '2col' }],
      ['a preset missing', { profiles: { '3col': good()['3col'], '1col': good()['1col'] }, knownIds: [], activeEditingProfile: '1col' }],
      ['a preset is null', { profiles: { ...good(), '2col': null }, knownIds: [] }],
      ['wrong column count', { profiles: { ...good(), '3col': { enabled: true, columns: [['a'], ['b']] } }, knownIds: [] }],
      ['columns not an array', { profiles: { ...good(), '3col': { enabled: true, columns: 'x' } }, knownIds: [] }],
      ['a column not an array', { profiles: { ...good(), '3col': { enabled: true, columns: [['a'], 'b', null] } }, knownIds: [] }],
      ['columns holding non-strings', { profiles: { ...good(), '3col': { enabled: true, columns: [['a', 1, null, { x: 1 }], [], ['c']] } }, knownIds: [] }],
      ['enabled not boolean', { profiles: { ...good(), '2col': { enabled: 'yes', columns: [[], []] } }, knownIds: [] }],
      ['enabled missing', { profiles: { ...good(), '2col': { columns: [[], []] } }, knownIds: [] }],
      ['1col disabled', { profiles: { ...good(), '1col': { enabled: false, columns: [['a']] } }, knownIds: [] }],
      ['knownIds not an array', { profiles: good(), knownIds: 'a,b' }],
      ['knownIds holding non-strings', { profiles: good(), knownIds: ['a', 2, null, 'b'] }],
      ['knownIds missing', { profiles: good(), activeEditingProfile: '3col' }],
      ['illegal activeEditingProfile', { profiles: good(), knownIds: [], activeEditingProfile: 'bogus' }],
      ['activeEditingProfile a number', { profiles: good(), knownIds: [], activeEditingProfile: 7 }],
      [
        'everything wrong at once',
        { profiles: { '3col': 1, '2col': { enabled: 0, columns: [[1], 'x'] }, '1col': { enabled: false, columns: [[]] } }, knownIds: [3], activeEditingProfile: null },
      ],
    ]

    it.each(fixtures)('%s — pure: migrate(·, 1) then healPresetState', (_name, state) => {
      const migrate = useNewTabLayoutStore.persist.getOptions().migrate!
      const migrated = migrate(structuredClone(state), 1) as Record<string, unknown>
      expect(Object.keys(migrated).filter((k) => k === 'profiles' || k === 'activeEditingProfile')).toEqual([])
      healPresetState(migrated)
      const old = structuredClone(state)
      frozenV1Heal(old)
      expect(migrated).toEqual({ presets: old.profiles, knownIds: old.knownIds, activeEditingPreset: old.activeEditingProfile })
    })

    it.each(fixtures)('%s — through rehydrate', async (_name, state) => {
      await rehydrateFrom(v1Blob(state))
      expect(picked()).toEqual(expectedFromV1(state))
    })
  })

  it('cross-window: another window writes a v1 blob → same data under the new names, and the v2 write-back does not loop', async () => {
    const rehydrate = vi.spyOn(useNewTabLayoutStore.persist, 'rehydrate')
    const notify = vi.spyOn(syncManager, 'notify')
    const subscriber = vi.fn()
    const unsubscribe = useNewTabLayoutStore.subscribe(subscriber)
    const flush = async () => {
      for (let i = 0; i < 5; i++) await new Promise((r) => setTimeout(r, 0))
    }
    try {
      // the other window's write: the storage area already holds the new value when the event fires (sync.ts header)
      const raw = v1Blob({ profiles: layout(), knownIds: ['a', 'b', 'c'], activeEditingProfile: '3col' })
      localStorage.setItem(KEY, raw)
      window.dispatchEvent(new StorageEvent('storage', { key: KEY, oldValue: null, newValue: raw, storageArea: localStorage }))
      await flush()

      expect(rehydrate).toHaveBeenCalledTimes(1)
      expect(picked()).toEqual({ presets: layout(), knownIds: ['a', 'b', 'c'], activeEditingPreset: '3col' })
      const writtenBack = localStorage.getItem(KEY)!
      expect(JSON.parse(writtenBack)).toEqual({ state: { presets: layout(), knownIds: ['a', 'b', 'c'], activeEditingPreset: '3col' }, version: 2 })
      expect(notify).toHaveBeenCalledTimes(1) // the one v2 write-back, announced once
      expect(subscriber.mock.calls.length).toBeGreaterThanOrEqual(1)
      expect(subscriber.mock.calls.length).toBeLessThanOrEqual(2)

      // the write-back as a window sees it (a v2 value): at most one more rehydrate,
      // which reads v2 → no migrate → no write → nothing announced
      window.dispatchEvent(new StorageEvent('storage', { key: KEY, oldValue: raw, newValue: writtenBack, storageArea: localStorage }))
      await flush()
      expect(rehydrate.mock.calls.length).toBeLessThanOrEqual(2)
      expect(notify).toHaveBeenCalledTimes(1)
      expect(localStorage.getItem(KEY)).toBe(writtenBack)
      const settled = { rehydrates: rehydrate.mock.calls.length, subs: subscriber.mock.calls.length }
      await flush()
      expect({ rehydrates: rehydrate.mock.calls.length, subs: subscriber.mock.calls.length }).toEqual(settled)
      expect(picked()).toEqual({ presets: layout(), knownIds: ['a', 'b', 'c'], activeEditingPreset: '3col' })
    } finally {
      unsubscribe()
      rehydrate.mockRestore()
      notify.mockRestore()
    }
  })
})
