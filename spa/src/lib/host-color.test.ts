import { describe, it, expect } from 'vitest'
import {
  HOST_COLOR_PRESETS,
  DEFAULT_HOST_ICON,
  hasHostBadge,
  isValidHostColor,
  isIconWeight,
  isPhosphorIconName,
  normalizeHostColor,
  getTabHostId,
  getTabBadgePane,
  sanitizeHostConfig,
  HOST_COLOR_MODES,
  HOST_COLOR_ALPHA_DEFAULTS,
  isHostColorMode,
  clampHostAlpha,
  isHostColorLayer,
  isHostColorSet,
  resolveHostColorSet,
  resolveHostColors,
} from './host-color'
import type { PaneLayout, Tab } from '../types/tab'
import type { HostConfig } from '../stores/useHostStore'

function tmuxLeaf(hostId: string, sessionCode = 'sess'): PaneLayout {
  return {
    type: 'leaf',
    pane: {
      id: `pane-${hostId}-${sessionCode}`,
      content: {
        kind: 'tmux-session',
        hostId,
        sessionCode,
        mode: 'terminal',
        cachedName: 'x',
        tmuxInstance: 'default',
      },
    },
  }
}

function leaf(id: string, content: Extract<PaneLayout, { type: 'leaf' }>['pane']['content']): PaneLayout {
  return { type: 'leaf', pane: { id, content } }
}

function splitH(...children: PaneLayout[]): PaneLayout {
  return { type: 'split', id: 's', direction: 'h', children, sizes: children.map(() => 1 / children.length) }
}

function tab(layout: PaneLayout): Tab {
  return { id: 't1', pinned: false, locked: false, createdAt: 0, layout }
}

describe('HOST_COLOR_PRESETS', () => {
  it('has 8 valid lowercase #rrggbb presets', () => {
    expect(HOST_COLOR_PRESETS).toHaveLength(8)
    for (const c of HOST_COLOR_PRESETS) {
      expect(isValidHostColor(c)).toBe(true)
      expect(c).toBe(c.toLowerCase())
    }
  })
})

describe('isValidHostColor', () => {
  it.each([
    ['#ABCDEF', true],
    ['#abcdef', true],
    ['abcdef', false],
    ['  #abc123 ', false],
    ['#abc', false],
    ['red', false],
    ['url(x)', false],
    ['', false],
  ])('%j → %s', (input, expected) => {
    expect(isValidHostColor(input)).toBe(expected)
  })

  it('rejects non-strings', () => {
    expect(isValidHostColor(undefined)).toBe(false)
    expect(isValidHostColor(null)).toBe(false)
    expect(isValidHostColor(0xabcdef)).toBe(false)
  })
})

describe('normalizeHostColor', () => {
  it.each([
    ['#ABCDEF', '#abcdef'],
    ['abcdef', '#abcdef'],
    ['  #abc123 ', '#abc123'],
    ['#abc', null],
    ['red', null],
    ['url(x)', null],
    ['', null],
  ])('%j → %j', (input, expected) => {
    expect(normalizeHostColor(input)).toBe(expected)
  })
})

describe('getTabHostId', () => {
  it('returns the hostId of a single tmux-session leaf', () => {
    expect(getTabHostId(tab(tmuxLeaf('h1')))).toBe('h1')
  })

  it('returns the first tmux-session hostId in pre-order for a split with mixed pane kinds', () => {
    const layout = splitH(
      leaf('p-new', { kind: 'new-tab' }),
      splitH(leaf('p-dash', { kind: 'dashboard' }), tmuxLeaf('h2')),
      tmuxLeaf('h1'),
    )
    expect(getTabHostId(tab(layout))).toBe('h2')
  })

  it('returns null when there is no tmux-session pane', () => {
    const layout = splitH(leaf('p-new', { kind: 'new-tab' }), leaf('p-dash', { kind: 'dashboard' }))
    expect(getTabHostId(tab(layout))).toBeNull()
  })
})

describe('getTabBadgePane', () => {
  it('returns the first tmux-session pane content in pre-order — non-tmux leaf first', () => {
    const layout = splitH(leaf('p-new', { kind: 'new-tab' }), tmuxLeaf('host-a'))
    const pane = getTabBadgePane(tab(layout))
    expect(pane?.kind).toBe('tmux-session')
    expect(pane?.hostId).toBe('host-a')
  })

  it('returns the first tmux-session pane content in pre-order — two tmux leaves', () => {
    const layout = splitH(tmuxLeaf('host-b'), tmuxLeaf('host-a'))
    const pane = getTabBadgePane(tab(layout))
    expect(pane?.hostId).toBe('host-b')
  })

  it('returns null when there is no tmux-session pane', () => {
    const layout = splitH(leaf('p-new', { kind: 'new-tab' }), leaf('p-dash', { kind: 'dashboard' }))
    expect(getTabBadgePane(tab(layout))).toBeNull()
  })

  it('agrees with getTabHostId on hostId', () => {
    const layout = splitH(leaf('p-new', { kind: 'new-tab' }), tmuxLeaf('host-a'))
    expect(getTabBadgePane(tab(layout))?.hostId).toBe(getTabHostId(tab(layout)))

    const empty = splitH(leaf('p-new', { kind: 'new-tab' }), leaf('p-dash', { kind: 'dashboard' }))
    expect(getTabBadgePane(tab(empty))).toBeNull()
    expect(getTabHostId(tab(empty))).toBeNull()
  })
})

describe('DEFAULT_HOST_ICON', () => {
  it('is the Desktop Phosphor icon', () => {
    expect(DEFAULT_HOST_ICON).toBe('Desktop')
  })

  it('is itself a real catalog name — the fallback can never render as text', () => {
    expect(isPhosphorIconName(DEFAULT_HOST_ICON)).toBe(true)
  })
})

describe('isPhosphorIconName', () => {
  it.each(['Desktop', 'Laptop', 'Cloud', 'Rocket'])('accepts the catalog name %j', (name) => {
    expect(isPhosphorIconName(name)).toBe(true)
  })

  it.each([
    // well-shaped but not in the catalog — the shape guard alone is not enough
    'NotARealPhosphorIcon',
    'Zzz',
    // wrong case
    'laptop',
    'LAPTOP',
    'desktop',
    // padded: rejected outright, never trimmed-then-accepted
    ' Laptop ',
    'Laptop ',
    ' Laptop',
    // blank
    '',
    '   ',
    // punctuation / injection shapes
    'Laptop; background:url(x)',
    'Laptop<script>',
    'Laptop-Extra',
    'Laptop.Extra',
    '../../etc/passwd',
  ])('rejects %j', (bad) => {
    expect(isPhosphorIconName(bad)).toBe(false)
  })

  it('rejects an overlong string without scanning the catalog', () => {
    expect(isPhosphorIconName('a'.repeat(200))).toBe(false)
    expect(isPhosphorIconName('A'.repeat(200))).toBe(false)
  })

  it.each([42, null, undefined, {}, ['Laptop'], true])('rejects the non-string %j', (bad) => {
    expect(isPhosphorIconName(bad)).toBe(false)
  })
})

describe('isIconWeight', () => {
  it.each(['bold', 'regular', 'thin', 'light', 'fill', 'duotone'])('accepts %s', (w) => {
    expect(isIconWeight(w)).toBe(true)
  })

  it.each(['evil', '', 'Regular', 'REGULAR', 42, null, undefined, {}, ['bold']])(
    'rejects %j',
    (bad) => {
      expect(isIconWeight(bad)).toBe(false)
    },
  )
})

describe('hasHostBadge', () => {
  it('is false for a tab that resolves no host', () => {
    expect(hasHostBadge(null)).toBe(false)
  })

  it('is false when the host set neither a color nor an icon', () => {
    expect(hasHostBadge({ colors: null, icon: undefined, iconWeight: undefined })).toBe(false)
  })

  it('is true when the host set a color only', () => {
    expect(
      hasHostBadge({ colors: { main: 'x', middle: 'y', light: 'z' }, icon: undefined, iconWeight: undefined }),
    ).toBe(true)
  })

  it('is true when the host set an icon only', () => {
    expect(hasHostBadge({ colors: null, icon: 'Laptop', iconWeight: undefined })).toBe(true)
  })

  it('is true when the host set both', () => {
    expect(
      hasHostBadge({ colors: { main: 'x', middle: 'y', light: 'z' }, icon: 'Laptop', iconWeight: 'duotone' }),
    ).toBe(true)
  })

  it('ignores a weight on its own — a weight is not something to show', () => {
    expect(hasHostBadge({ colors: null, icon: undefined, iconWeight: 'duotone' })).toBe(false)
  })
})

describe('sanitizeHostConfig', () => {
  const base: HostConfig = { id: 'h1', name: 'H', ip: '1.2.3.4', port: 7860, order: 0 }

  it.each(['url(x)', '#abc', 'red', '', {}, 42, null])('removes invalid color %j', (bad) => {
    const out = sanitizeHostConfig({ ...base, token: 'T', color: bad as never })
    expect('color' in out).toBe(false)
    expect(out).toEqual({ ...base, token: 'T' })
  })

  it('returns the same object when color is valid', () => {
    const h = { ...base, color: '#3b82f6' }
    expect(sanitizeHostConfig(h)).toBe(h)
  })

  it('returns the same object when color key is absent', () => {
    expect(sanitizeHostConfig(base)).toBe(base)
  })

  it('removes an explicit undefined color key', () => {
    const out = sanitizeHostConfig({ ...base, color: undefined })
    expect('color' in out).toBe(false)
  })

  it('keeps a valid icon and iconWeight (same object reference)', () => {
    const h: HostConfig = { ...base, color: '#3b82f6', icon: 'Laptop', iconWeight: 'duotone' }
    expect(sanitizeHostConfig(h)).toBe(h)
  })

  it.each([
    42,
    '',
    '   ',
    {},
    null,
    true,
    // hostile / corrupted values that the old "any non-empty string" check let through
    'a'.repeat(200),
    'laptop',
    ' Laptop ',
    'NotARealPhosphorIcon',
    'Laptop; background:url(x)',
  ])('removes invalid icon %j', (bad) => {
    const out = sanitizeHostConfig({ ...base, icon: bad as never, iconWeight: 'bold' })
    expect('icon' in out).toBe(false)
    expect(out.iconWeight).toBe('bold')
  })

  it.each([
    'a'.repeat(513),
    'mini:abc123\n',
    'mini:abc\u0000123',
    'mini:abc123\t',
    'mini\u202e:abc123',
    'mini:abc\u200b',
    '',
    42,
    null,
  ])('removes an invalid daemonId %j (isValidDaemonId, codex attacker)', (bad) => {
    const out = sanitizeHostConfig({ ...base, token: 'T', daemonId: bad as never })
    expect('daemonId' in out).toBe(false)
    expect(out).toEqual({ ...base, token: 'T' })
  })

  it.each(['mini-lab:278cbm', 'unknown:abc123', 'Mini-Lab:278cbm', 'host name:abc123', 'm\u00fcnchen:abc123'])('keeps a valid daemonId %j (same object reference)', (id) => {
    const h: HostConfig = { ...base, daemonId: id }
    expect(sanitizeHostConfig(h)).toBe(h)
  })

  it('removes an explicit undefined icon key', () => {
    const out = sanitizeHostConfig({ ...base, icon: undefined })
    expect('icon' in out).toBe(false)
  })

  it.each(['evil', '', 42, null, 'Regular'])('removes invalid iconWeight %j', (bad) => {
    const out = sanitizeHostConfig({ ...base, icon: 'Laptop', iconWeight: bad as never })
    expect('iconWeight' in out).toBe(false)
    expect(out.icon).toBe('Laptop')
  })

  it('drops an invalid color while keeping a valid icon', () => {
    const out = sanitizeHostConfig({ ...base, color: 'red' as never, icon: 'Laptop' })
    expect('color' in out).toBe(false)
    expect(out.icon).toBe('Laptop')
  })

  it('drops icon, iconWeight and color together when all are invalid', () => {
    const out = sanitizeHostConfig({
      ...base,
      color: 'red' as never,
      icon: 42 as never,
      iconWeight: 'evil' as never,
    })
    expect(out).toEqual(base)
  })

  const set = (color: string) => ({ main: { color, alpha: 100 } })

  it('keeps a fully valid colors map (same object)', () => {
    const h: HostConfig = { ...base, colors: { console: set('#3b82f6'), terminal: { ...set('#ef4444'), middle: { alpha: 40 } } } }
    expect(sanitizeHostConfig(h)).toBe(h)
  })

  it.each([null, 'x', 42, []])('drops colors when it is not a plain object: %j', (bad) => {
    const out = sanitizeHostConfig({ ...base, colors: bad as never })
    expect('colors' in out).toBe(false)
  })

  it('drops unknown mode keys and keeps the valid ones', () => {
    const out = sanitizeHostConfig({ ...base, colors: { console: set('#3b82f6'), shell: set('#000000') } as never })
    expect(out.colors).toEqual({ console: set('#3b82f6') })
  })

  it('drops a set whose main is invalid', () => {
    const out = sanitizeHostConfig({ ...base, colors: { console: set('#3b82f6'), terminal: { main: { alpha: 100 } } } as never })
    expect(out.colors).toEqual({ console: set('#3b82f6') })
  })

  it('drops an invalid middle/light layer but keeps the rest of the set', () => {
    const out = sanitizeHostConfig({
      ...base,
      colors: { console: { ...set('#3b82f6'), middle: { alpha: 500 }, light: { color: '#000000', alpha: 10 } } } as never,
    })
    expect(out.colors).toEqual({ console: { ...set('#3b82f6'), light: { color: '#000000', alpha: 10 } } })
  })

  it('removes colors entirely when no mode survives', () => {
    const out = sanitizeHostConfig({ ...base, colors: { bogus: set('#3b82f6') } as never })
    expect('colors' in out).toBe(false)
  })

  it('cleans colors and legacy color independently', () => {
    const out = sanitizeHostConfig({ ...base, color: 'red', colors: { console: set('#3b82f6') } } as never)
    expect('color' in out).toBe(false)
    expect(out.colors).toEqual({ console: set('#3b82f6') })
  })

  it('keeps the colors map and every set by reference when all valid, even if another key is cleaned', () => {
    const colors = { console: set('#3b82f6'), terminal: { ...set('#ef4444'), middle: { alpha: 40 } } }
    const out = sanitizeHostConfig({ ...base, icon: 'NotAnIcon', colors } as never)
    expect(out.colors).toBe(colors)
    expect(out.colors?.terminal).toBe(colors.terminal)
  })

  it('rebuilds only the set that had an invalid layer; sibling sets keep their reference', () => {
    const consoleSet = set('#3b82f6')
    const terminalSet = { ...set('#ef4444'), light: { alpha: 900 } }
    const out = sanitizeHostConfig({ ...base, colors: { console: consoleSet, terminal: terminalSet } } as never)
    expect(out.colors?.console).toBe(consoleSet)
    expect(out.colors?.terminal).not.toBe(terminalSet)
    expect(out.colors?.terminal).toEqual(set('#ef4444'))
  })
})

describe('host color modes — guards', () => {
  it('HOST_COLOR_MODES is exactly console/terminal/execution', () => {
    expect(HOST_COLOR_MODES).toEqual(['console', 'terminal', 'execution'])
  })

  it.each(['console', 'terminal', 'execution'])('isHostColorMode accepts %s', (m) => {
    expect(isHostColorMode(m)).toBe(true)
  })
  it.each(['shell', '', 'Console', 1, null, undefined])('isHostColorMode rejects %j', (m) => {
    expect(isHostColorMode(m)).toBe(false)
  })

  it('HOST_COLOR_ALPHA_DEFAULTS is main 100 / middle 60 / light 22', () => {
    expect(HOST_COLOR_ALPHA_DEFAULTS).toEqual({ main: 100, middle: 60, light: 22 })
  })

  it.each([
    [50, 50], [-5, 0], [150, 100], [33.4, 33], [33.5, 34], [NaN, 0], [Infinity, 100],
  ])('clampHostAlpha(%j) = %j', (input, expected) => {
    expect(clampHostAlpha(input)).toBe(expected)
  })

  it('isHostColorLayer accepts alpha-only and color+alpha layers', () => {
    expect(isHostColorLayer({ alpha: 60 })).toBe(true)
    expect(isHostColorLayer({ color: '#3b82f6', alpha: 100 })).toBe(true)
  })
  it.each([
    { alpha: 101 }, { alpha: -1 }, { alpha: 1.5 }, { alpha: '60' }, {}, { color: '#3b82f6' },
    { color: 'red', alpha: 50 }, { color: '#ABC', alpha: 50 }, null, 'x', [],
  ])('isHostColorLayer rejects %j', (bad) => {
    expect(isHostColorLayer(bad)).toBe(false)
  })
  it('isHostColorLayer with requireColor rejects an alpha-only layer', () => {
    expect(isHostColorLayer({ alpha: 100 }, { requireColor: true })).toBe(false)
    expect(isHostColorLayer({ color: '#3b82f6', alpha: 100 }, { requireColor: true })).toBe(true)
  })

  it('isHostColorSet requires main with a color; middle/light optional', () => {
    expect(isHostColorSet({ main: { color: '#3b82f6', alpha: 100 } })).toBe(true)
    expect(isHostColorSet({ main: { color: '#3b82f6', alpha: 100 }, middle: { alpha: 60 }, light: { color: '#000000', alpha: 10 } })).toBe(true)
  })
  it.each([
    { main: { alpha: 100 } },
    { middle: { alpha: 60 } },
    { main: { color: '#3b82f6', alpha: 100 }, middle: { alpha: 200 } },
    { main: { color: '#3b82f6', alpha: 100 }, light: 'x' },
    null, {}, [],
  ])('isHostColorSet rejects %j', (bad) => {
    expect(isHostColorSet(bad)).toBe(false)
  })
})

describe('resolveHostColorSet / resolveHostColors', () => {
  const console_ = { main: { color: '#3b82f6', alpha: 100 } }

  it('returns null for undefined host / no colors / no legacy color', () => {
    expect(resolveHostColorSet(undefined, 'console')).toBeNull()
    expect(resolveHostColorSet({}, 'terminal')).toBeNull()
    expect(resolveHostColors({}, 'console')).toBeNull()
  })

  it('fills middle/light from main with default alphas', () => {
    expect(resolveHostColorSet({ colors: { console: console_ } }, 'console')).toEqual({
      main: { color: '#3b82f6', alpha: 100 },
      middle: { color: '#3b82f6', alpha: 60 },
      light: { color: '#3b82f6', alpha: 22 },
    })
  })

  it('keeps explicit middle/light colors and alphas', () => {
    const set = { ...console_, middle: { alpha: 40 }, light: { color: '#000000', alpha: 10 } }
    expect(resolveHostColorSet({ colors: { console: set } }, 'console')).toEqual({
      main: { color: '#3b82f6', alpha: 100 },
      middle: { color: '#3b82f6', alpha: 40 },
      light: { color: '#000000', alpha: 10 },
    })
  })

  it('terminal / execution fall back to console when unset', () => {
    const host = { colors: { console: console_ } }
    expect(resolveHostColorSet(host, 'terminal')?.main.color).toBe('#3b82f6')
    expect(resolveHostColorSet(host, 'execution')?.main.color).toBe('#3b82f6')
  })

  it('a mode with its own set does not inherit console layers', () => {
    const host = { colors: { console: { ...console_, middle: { alpha: 10 } }, terminal: { main: { color: '#ef4444', alpha: 90 } } } }
    expect(resolveHostColorSet(host, 'terminal')).toEqual({
      main: { color: '#ef4444', alpha: 90 },
      middle: { color: '#ef4444', alpha: 60 },
      light: { color: '#ef4444', alpha: 22 },
    })
  })

  it('legacy color acts as console main with default alphas, for every mode', () => {
    expect(resolveHostColorSet({ color: '#22c55e' }, 'terminal')).toEqual({
      main: { color: '#22c55e', alpha: 100 },
      middle: { color: '#22c55e', alpha: 60 },
      light: { color: '#22c55e', alpha: 22 },
    })
  })

  it('colors wins over legacy color', () => {
    expect(resolveHostColorSet({ color: '#22c55e', colors: { console: console_ } }, 'console')?.main.color).toBe('#3b82f6')
  })

  it('an invalid legacy color yields null instead of throwing', () => {
    expect(resolveHostColorSet({ color: 'red' }, 'console')).toBeNull()
  })

  it('malformed colors that bypassed sanitize resolve to null (or legacy) without throwing', () => {
    expect(resolveHostColorSet({ colors: 'x' as never }, 'console')).toBeNull()
    expect(resolveHostColorSet({ colors: 42 as never }, 'terminal')).toBeNull()
    expect(resolveHostColorSet({ colors: { console: { main: { alpha: 100 } } } as never }, 'console')).toBeNull()
    expect(resolveHostColorSet({ colors: { console: { main: { color: 'red', alpha: 100 } } } as never }, 'console')).toBeNull()
    expect(resolveHostColorSet({ colors: { console: { ...console_, middle: { alpha: 900 } } } as never }, 'console')).toBeNull()
    // A null set for the mode is "absent": legacy color still applies.
    expect(resolveHostColorSet({ colors: { console: null } as never, color: '#22c55e' }, 'console')?.main.color).toBe('#22c55e')
    expect(resolveHostColors({ colors: [] as never }, 'console')).toBeNull()
  })

  it('resolveHostColors emits rgba strings', () => {
    expect(resolveHostColors({ colors: { console: console_ } }, 'console')).toEqual({
      main: 'rgba(59, 130, 246, 1)',
      middle: 'rgba(59, 130, 246, 0.6)',
      light: 'rgba(59, 130, 246, 0.22)',
    })
  })
})
