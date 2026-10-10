import { describe, it, expect } from 'vitest'
import { isWorkspaceSettingsOf, secondarySettingsPaneIds, withoutSecondarySettingsPanes } from './workspace-settings-panes'
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

  it('removes them and collapses a split left with one child; the same object when there is nothing to remove', () => {
    const two = split(leaf('p1', { kind: 'dashboard' }), leaf('p2', settings('w1')))
    expect(withoutSecondarySettingsPanes(two, ['w1'])).toEqual(leaf('p1', { kind: 'dashboard' }))
    const none = split(leaf('p1', { kind: 'dashboard' }), leaf('p2', { kind: 'dashboard' }))
    expect(withoutSecondarySettingsPanes(none, ['w1'])).toBe(none)
    const single = leaf('p1', settings('w1'))
    expect(withoutSecondarySettingsPanes(single, ['w1'])).toBe(single) // a lone primary pane is the tab-close path's business
  })
})
