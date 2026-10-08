import { describe, it, expect } from 'vitest'
import { sectionLabelOf, lockWhyOf } from './conflict-text'
import type { SectionView } from './sync-view'

const t = (key: string, p?: Record<string, string | number>) => (p ? `${key}|${JSON.stringify(p)}` : key)
const view = (v: object) => v as unknown as SectionView

describe('sectionLabelOf', () => {
  it('names an unknown kind by its key', () => expect(sectionLabelOf(t, view({ kind: 'other', key: 'x' }))).toBe('x'))
  it('names a plain kind', () => expect(sectionLabelOf(t, view({ kind: 'hosts' }))).toBe('settings.profile.current.label.hosts'))
  it('names tabs by workspace, unseen or unknown', () => {
    expect(sectionLabelOf(t, view({ kind: 'tabs', workspace: 'W' }))).toBe('settings.profile.current.label.tabs|{"workspace":"W"}')
    expect(sectionLabelOf(t, view({ kind: 'tabs', workspace: null }))).toBe('settings.profile.current.label.tabs_unseen')
    expect(sectionLabelOf(t, view({ kind: 'tabs' }))).toBe('settings.profile.current.label.tabs_unknown')
  })
})

describe('lockWhyOf', () => {
  it('conflict and reset carry no reason', () => {
    expect(lockWhyOf(t, 'locked:conflict', null)).toEqual({ text: 'settings.profile.resolve.why.conflict' })
    expect(lockWhyOf(t, 'locked:reset', null)).toEqual({ text: 'settings.profile.resolve.why.reset' })
  })
  it('invalid carries its reason, dashes as underscores, missing as unknown', () => {
    expect(lockWhyOf(t, 'locked:invalid', 'a-b')).toEqual({ reason: 'a-b', text: 'settings.profile.resolve.why.invalid.a_b' })
    expect(lockWhyOf(t, 'locked:invalid', undefined).reason).toBe('unknown')
  })
})
