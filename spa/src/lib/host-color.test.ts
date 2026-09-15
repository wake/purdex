import { describe, it, expect } from 'vitest'
import {
  HOST_COLOR_PRESETS,
  isValidHostColor,
  normalizeHostColor,
  getTabHostId,
  resolveTabHostColor,
  sanitizeHostConfigColor,
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

function host(id: string, color?: unknown): HostConfig {
  const base = { id, name: id, ip: '100.64.0.2', port: 7860, order: 0 }
  return (color === undefined ? base : { ...base, color }) as HostConfig
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

describe('resolveTabHostColor', () => {
  it('returns the stored color of the tab host', () => {
    const hosts = { h1: host('h1', '#3b82f6') }
    expect(resolveTabHostColor(tab(tmuxLeaf('h1')), hosts)).toBe('#3b82f6')
  })

  it('returns null when the tab has no tmux-session pane', () => {
    const hosts = { h1: host('h1', '#3b82f6') }
    expect(resolveTabHostColor(tab(leaf('p', { kind: 'dashboard' })), hosts)).toBeNull()
  })

  it('returns null when the host is missing from the store', () => {
    expect(resolveTabHostColor(tab(tmuxLeaf('h1')), {})).toBeNull()
  })

  it('returns null when the host has no color', () => {
    const hosts = { h1: host('h1') }
    expect(resolveTabHostColor(tab(tmuxLeaf('h1')), hosts)).toBeNull()
  })

  it.each(['red', 'url(x)', '#abc', '', 42])('returns null for invalid stored color %j', (bad) => {
    const hosts = { h1: host('h1', bad) }
    expect(resolveTabHostColor(tab(tmuxLeaf('h1')), hosts)).toBeNull()
  })
})

describe('sanitizeHostConfigColor', () => {
  const base: HostConfig = { id: 'h1', name: 'H', ip: '1.2.3.4', port: 7860, order: 0 }

  it.each(['url(x)', '#abc', 'red', '', {}, 42, null])('removes invalid color %j', (bad) => {
    const out = sanitizeHostConfigColor({ ...base, token: 'T', color: bad as never })
    expect('color' in out).toBe(false)
    expect(out).toEqual({ ...base, token: 'T' })
  })

  it('returns the same object when color is valid', () => {
    const h = { ...base, color: '#3b82f6' }
    expect(sanitizeHostConfigColor(h)).toBe(h)
  })

  it('returns the same object when color key is absent', () => {
    expect(sanitizeHostConfigColor(base)).toBe(base)
  })

  it('removes an explicit undefined color key', () => {
    const out = sanitizeHostConfigColor({ ...base, color: undefined })
    expect('color' in out).toBe(false)
  })
})
