import { describe, expect, it } from 'vitest'
import type { PaneContent, PaneLayout } from '../../types/tab'
import type { LayoutPreset, PresetKey } from '../resolve-preset'
import {
  HOST_BEARING_COLUMN_PREFIXES,
  hostSettingsFromWire,
  hostSettingsToWire,
  identityOfSync,
  layoutFromWire,
  layoutToWire,
  makeWireResolver,
  presetColumnIdFromWire,
  presetColumnIdToWire,
  presetColumnsFromWire,
  presetColumnsToWire,
  syncIdOfSync,
} from './host-identity'

const MLAB = 'mini-lab:278cbm'
const W = syncIdOfSync(MLAB)
const L = 'loc001'
const identity = identityOfSync({ [L]: { id: L, daemonId: MLAB }, loc002: { id: 'loc002' } })
const resolve = makeWireResolver({ identity })

function leaf(id: string, content: PaneContent): PaneLayout {
  return { type: 'leaf', pane: { id, content } }
}

const tmux = (hostId: string): PaneContent => ({
  kind: 'tmux-session',
  hostId,
  sessionCode: 'abc',
  mode: 'terminal',
  cachedName: 'main',
  tmuxInstance: '1:1',
})

/** Every pane kind, host-bearing ones on `hostId`. */
function everyKind(hostId: string): PaneContent[] {
  return [
    { kind: 'new-tab' },
    tmux(hostId),
    { kind: 'dashboard' },
    { kind: 'hosts' },
    { kind: 'history' },
    { kind: 'settings', scope: 'global' },
    { kind: 'settings', scope: { workspaceId: 'ws1' } },
    { kind: 'browser', url: 'https://x' },
    { kind: 'memory-monitor' },
    { kind: 'editor', source: { type: 'daemon', hostId }, filePath: '/a' },
    { kind: 'editor', source: { type: 'local' }, filePath: '/a' },
    { kind: 'editor', source: { type: 'inapp' }, filePath: '/a' },
    { kind: 'editor-buffers' },
    { kind: 'image-preview', source: { type: 'daemon', hostId }, filePath: '/a.png' },
    { kind: 'pdf-preview', source: { type: 'daemon', hostId }, filePath: '/a.pdf' },
    { kind: 'execution', executionId: 'e1', host: hostId },
    { kind: 'execution', executionId: 'e2' },
    { kind: 'execution', executionId: 'e3', host: '' },
  ]
}

function nested(hostId: string): PaneLayout {
  const kinds = everyKind(hostId)
  return {
    type: 'split',
    id: 's0',
    direction: 'h',
    sizes: [50, 50],
    children: [
      leaf('p0', kinds[1]),
      {
        type: 'split',
        id: 's1',
        direction: 'v',
        sizes: [30, 70],
        children: kinds.slice(2).map((c, i) => leaf(`p${i + 2}`, c)),
      },
    ],
  }
}

function contentOf(layout: PaneLayout, index: number): PaneContent {
  const leaves: PaneContent[] = []
  const walk = (l: PaneLayout) => (l.type === 'leaf' ? leaves.push(l.pane.content) : l.children.forEach(walk))
  walk(layout)
  return leaves[index]
}

describe('layoutToWire / layoutFromWire', () => {
  it('translates tmux-session.hostId', () => {
    const wire = layoutToWire(leaf('p', tmux(L)), identity)
    expect(wire).toEqual(leaf('p', tmux(W)))
  })

  it.each(['editor', 'image-preview', 'pdf-preview'] as const)('translates %s source.hostId when type is daemon', (kind) => {
    const content = { kind, source: { type: 'daemon', hostId: L }, filePath: '/f' } as PaneContent
    const wire = layoutToWire(leaf('p', content), identity)
    expect(wire.type === 'leaf' && wire.pane.content).toEqual({ ...content, source: { type: 'daemon', hostId: W } })
  })

  it('leaves a non-daemon source alone, even if it carries a stray hostId', () => {
    const content = { kind: 'editor', source: { type: 'local', hostId: L }, filePath: '/f' } as unknown as PaneContent
    const wire = layoutToWire(leaf('p', content), identity)
    expect(wire.type === 'leaf' && wire.pane.content).toEqual(content)
  })

  it('does not translate a hostId-shaped field on another kind', () => {
    const content = { kind: 'browser', url: 'x', hostId: L, host: L } as unknown as PaneContent
    const wire = layoutToWire(leaf('p', content), identity)
    expect(wire.type === 'leaf' && wire.pane.content).toEqual(content)
  })

  it('translates execution.host; an empty host and an absent host pass through', () => {
    const wire = layoutToWire(nested(L), identity)
    const kinds = everyKind(W)
    const exec = (id: string) => kinds.findIndex((k) => k.kind === 'execution' && k.executionId === id) - 1
    expect(contentOf(wire, exec('e1'))).toEqual({ kind: 'execution', executionId: 'e1', host: W })
    expect(contentOf(wire, exec('e2'))).toEqual({ kind: 'execution', executionId: 'e2' })
    expect(contentOf(wire, exec('e2'))).not.toHaveProperty('host')
    expect(contentOf(wire, exec('e3'))).toEqual({ kind: 'execution', executionId: 'e3', host: '' })
  })

  it('translates the whole nested tree and keeps split structure and sizes', () => {
    expect(layoutToWire(nested(L), identity)).toEqual(nested(W))
  })

  it('round-trips every pane kind in a nested split', () => {
    expect(layoutFromWire(layoutToWire(nested(L), identity), resolve)).toEqual(nested(L))
  })

  it('fromWire maps a sync id back to the local id', () => {
    expect(layoutFromWire(nested(W), resolve)).toEqual(nested(L))
  })

  it('works on a stripped layout (no sizes)', () => {
    const stripped = { type: 'split', id: 's', direction: 'h', children: [leaf('p', tmux(L))] } as const
    expect(layoutToWire(stripped, identity)).toEqual({ ...stripped, children: [leaf('p', tmux(W))] })
  })

  it('an id the identity does not know passes through', () => {
    expect(layoutToWire(leaf('p', tmux('ghost1')), identity)).toEqual(leaf('p', tmux('ghost1')))
    expect(layoutFromWire(leaf('p', tmux('d1_unknown')), resolve)).toEqual(leaf('p', tmux('d1_unknown')))
  })

  it('does not mutate its input', () => {
    const input = nested(L)
    const snapshot = structuredClone(input)
    layoutToWire(input, identity)
    layoutFromWire(input, resolve)
    expect(input).toEqual(snapshot)
  })

  it('is total on garbage', () => {
    const garbage = [
      null,
      42,
      { type: 'leaf' },
      { type: 'leaf', pane: null },
      { type: 'leaf', pane: { id: 'p', content: null } },
      { type: 'split', id: 's', children: 'x' },
      { type: 'leaf', pane: { id: 'p', content: { kind: 'tmux-session', hostId: 7 } } },
      { type: 'leaf', pane: { id: 'p', content: { kind: 'editor', source: null } } },
      { type: 'leaf', pane: { id: 'p', content: { kind: 'execution', host: 3 } } },
    ]
    for (const g of garbage) {
      expect(layoutToWire(g as unknown as PaneLayout, identity)).toEqual(g)
      expect(layoutFromWire(g as unknown as PaneLayout, resolve)).toEqual(g)
    }
  })
})

describe('hostSettingsToWire / hostSettingsFromWire', () => {
  const record = { [L]: { files: { a: 1 } }, loc002: { x: {} }, ghost1: { y: {} } }

  it('re-keys by wire id; unknown keys pass through', () => {
    expect(hostSettingsToWire(record, identity)).toEqual({ [W]: { files: { a: 1 } }, loc002: { x: {} }, ghost1: { y: {} } })
  })

  it('round-trips', () => {
    expect(hostSettingsFromWire(hostSettingsToWire(record, identity), resolve)).toEqual(record)
  })

  it('does not mutate its input', () => {
    const snapshot = structuredClone(record)
    hostSettingsToWire(record, identity)
    expect(record).toEqual(snapshot)
  })

  it('when a canonical and a legacy key resolve to one host, the canonical entry wins (either order)', () => {
    const rows = { [W]: { daemonId: MLAB, aliases: ['frgn01'] } }
    const r = makeWireResolver({ identity, rows })
    expect(hostSettingsFromWire({ frgn01: { old: {} }, [W]: { new: {} } }, r)).toEqual({ [L]: { new: {} } })
    expect(hostSettingsFromWire({ [W]: { new: {} }, frgn01: { old: {} } }, r)).toEqual({ [L]: { new: {} } })
  })

  it('is total on garbage', () => {
    expect(hostSettingsFromWire(null as unknown as Record<string, unknown>, resolve)).toBeNull()
    expect(hostSettingsToWire([1] as unknown as Record<string, unknown>, identity)).toEqual([1])
  })
})

describe('preset columns', () => {
  it('the host-bearing prefixes come from one constant', () => {
    expect(HOST_BEARING_COLUMN_PREFIXES).toEqual(['sessions', 'headless'])
  })

  it.each(HOST_BEARING_COLUMN_PREFIXES)('translates %s:<hostId>', (prefix) => {
    expect(presetColumnIdToWire(`${prefix}:${L}`, identity)).toBe(`${prefix}:${W}`)
    expect(presetColumnIdFromWire(`${prefix}:${W}`, resolve)).toBe(`${prefix}:${L}`)
  })

  it('leaves non-host columns alone', () => {
    for (const id of ['browser', 'editor', 'editor-buffers', 'sessions', 'headless', `other:${L}`, `sessions:`, `${L}`]) {
      expect(presetColumnIdToWire(id, identity), id).toBe(id)
    }
  })

  it('an unknown host id in a column passes through', () => {
    expect(presetColumnIdToWire('sessions:ghost1', identity)).toBe('sessions:ghost1')
    expect(presetColumnIdFromWire('headless:d1_nobody', resolve)).toBe('headless:d1_nobody')
  })

  const presets: Record<PresetKey, LayoutPreset> = {
    '3col': { enabled: true, columns: [[`sessions:${L}`, 'browser'], [`headless:${L}`, 'editor'], ['editor-buffers', 'sessions:loc002']] },
    '2col': { enabled: false, columns: [[`sessions:${L}`], ['sessions']] },
    '1col': { enabled: true, columns: [[]] },
  }

  it('translates the whole presets shape, keeping `enabled`', () => {
    const wire = presetColumnsToWire(presets, identity)
    expect(wire['3col']).toEqual({
      enabled: true,
      columns: [[`sessions:${W}`, 'browser'], [`headless:${W}`, 'editor'], ['editor-buffers', 'sessions:loc002']],
    })
    expect(wire['2col']).toEqual({ enabled: false, columns: [[`sessions:${W}`], ['sessions']] })
    expect(wire['1col']).toEqual(presets['1col'])
  })

  it('round-trips, including non-host columns', () => {
    expect(presetColumnsFromWire(presetColumnsToWire(presets, identity), resolve)).toEqual(presets)
  })

  it('does not mutate its input', () => {
    const snapshot = structuredClone(presets)
    presetColumnsToWire(presets, identity)
    expect(presets).toEqual(snapshot)
  })

  it('is total on garbage presets', () => {
    const garbage = { '3col': null, '2col': { columns: 'x' }, '1col': { columns: [['a', 3], 'b'] }, extra: 1 }
    expect(presetColumnsToWire(garbage as unknown as Record<PresetKey, LayoutPreset>, identity)).toEqual(garbage)
    expect(presetColumnsFromWire(null as unknown as Record<PresetKey, LayoutPreset>, resolve)).toBeNull()
  })
})
