import { describe, it, expect } from 'vitest'
import { isWorkspaceSettingsOf, secondarySettingsPaneIds, withoutSecondarySettingsPanes, withoutStaleSettingsPanes } from './workspace-settings-panes'
import type { PaneContent, PaneLayout } from '../types/tab'

const leaf = (id: string, content: PaneContent): PaneLayout => ({ type: 'leaf', pane: { id, content } })
const settings = (workspaceId: string): PaneContent => ({ kind: 'settings', scope: { workspaceId } })
const split = (...children: PaneLayout[]): PaneLayout => ({ type: 'split', id: 's', direction: 'h', children, sizes: children.map(() => 100 / children.length) })

describe('workspace settings panes (#1955)', () => {
  it('recognises only a workspace-scoped settings page of the listed workspaces', () => {
    expect(isWorkspaceSettingsOf(settings('w1'), ['w1'])).toBe(true)
    expect(isWorkspaceSettingsOf(settings('w2'), ['w1'])).toBe(false)
    expect(isWorkspaceSettingsOf({ kind: 'settings', scope: 'global' }, ['w1'])).toBe(false)
    expect(isWorkspaceSettingsOf({ kind: 'dashboard' }, ['w1'])).toBe(false)
  })

  it('finds secondary panes only: the primary pane is never listed, even when it is such a page', () => {
    const layout = split(leaf('p1', settings('w1')), leaf('p2', settings('w1')), leaf('p3', { kind: 'dashboard' }))
    expect(secondarySettingsPaneIds(layout, ['w1'])).toEqual(['p2'])
  })

  it('finds them in nested splits', () => {
    const layout = split(leaf('p1', { kind: 'dashboard' }), split(leaf('p2', { kind: 'dashboard' }), leaf('p3', settings('w1'))))
    expect(secondarySettingsPaneIds(layout, ['w1'])).toEqual(['p3'])
  })

  it('withoutStaleSettingsPanes drops only secondary settings panes of workspaces that do not exist (#2514)', () => {
    const layout = split(leaf('p1', { kind: 'dashboard' }), leaf('p2', settings('gone')), leaf('p3', settings('here')), leaf('p4', { kind: 'settings', scope: 'global' }))
    const next = withoutStaleSettingsPanes(layout, new Set(['here']))
    expect(secondarySettingsPaneIds(next, ['gone'])).toEqual([])
    expect(JSON.stringify(next)).toContain('p3')
    expect(JSON.stringify(next)).toContain('p4')
    expect(JSON.stringify(next)).not.toContain('p2')
    const intact = split(leaf('p1', { kind: 'dashboard' }), leaf('p2', settings('here')))
    expect(withoutStaleSettingsPanes(intact, new Set(['here']))).toBe(intact)
    const primary = split(leaf('p1', settings('gone')), leaf('p2', { kind: 'dashboard' }))
    expect(withoutStaleSettingsPanes(primary, new Set())).toBe(primary) // a primary pane is never removed here
  })

  it('removes them and collapses a split left with one child; the same object when there is nothing to remove', () => {
    const two = split(leaf('p1', { kind: 'dashboard' }), leaf('p2', settings('w1')))
    expect(withoutSecondarySettingsPanes(two, ['w1'])).toEqual(leaf('p1', { kind: 'dashboard' }))
    const none = split(leaf('p1', { kind: 'dashboard' }), leaf('p2', { kind: 'dashboard' }))
    expect(withoutSecondarySettingsPanes(none, ['w1'])).toBe(none)
    const single = leaf('p1', settings('w1'))
    expect(withoutSecondarySettingsPanes(single, ['w1'])).toBe(single) // a lone primary pane is the tab-close path's business
  })
})
