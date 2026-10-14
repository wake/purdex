import { describe, it, expect, beforeEach } from 'vitest'
import { defOfDraft, draftColor, draftIcon, draftOf, forgetProbeDraft, newProbeDraft, useProbeDraftStore } from './probe-draft-memory'

beforeEach(() => useProbeDraftStore.getState().reset())

describe('draft reducers follow the host look rules', () => {
  it('icon: set with a weight, change the weight, clear drops both', () => {
    let d = draftIcon(newProbeDraft(), 'Desktop', 'bold')
    expect(d).toMatchObject({ icon: 'Desktop', icon_weight: 'bold' })
    d = draftIcon(d, 'Desktop', 'thin')
    expect(d.icon_weight).toBe('thin')
    d = draftIcon(d, null)
    expect(d.icon).toBeUndefined()
    expect(d.icon_weight).toBeUndefined()
  })
  it('icon: a name that is not in the catalog changes nothing', () => {
    const d = newProbeDraft()
    expect(draftIcon(d, 'NotAnIcon')).toBe(d)
  })
  it('colour: a main layer, then a middle one, then the mode is cleared', () => {
    let d = draftColor(newProbeDraft(), 'console', 'main', { color: '#AABBCC', alpha: 90 })
    expect(d.colors?.console?.main).toEqual({ color: '#aabbcc', alpha: 90 })
    d = draftColor(d, 'console', 'middle', { alpha: 40 })
    expect(d.colors?.console?.middle).toEqual({ alpha: 40 })
    d = draftColor(d, 'console', 'main', null)
    expect(d.colors).toBeUndefined()
  })
  it('colour: a middle layer without a main one is refused', () => {
    const d = newProbeDraft()
    expect(draftColor(d, 'console', 'middle', { color: '#112233', alpha: 40 })).toBe(d)
  })
})

describe('draft <-> row', () => {
  it('a new draft keeps id empty; the row is trimmed and carries only what is set', () => {
    expect(defOfDraft({ ...newProbeDraft(), name: ' n ', ssh_alias: 'a' })).toEqual({ id: '', name: 'n', ssh_alias: 'a' })
  })
  it('round-trips a stored row', () => {
    const row = { id: 'x', name: 'n', ssh_alias: 'a', icon: 'Desktop', icon_weight: 'bold' as const, colors: { console: { main: { color: '#112233', alpha: 100 } } } }
    expect(defOfDraft(draftOf(row))).toEqual(row)
  })
})

describe('memory', () => {
  it('open / patch / close', () => {
    const s = useProbeDraftStore.getState()
    s.open('h', newProbeDraft())
    s.patch('h', (d) => ({ ...d, name: 'x' }))
    expect(useProbeDraftStore.getState().byHost.h.name).toBe('x')
    s.patch('ghost', (d) => ({ ...d, name: 'y' }))
    expect(useProbeDraftStore.getState().byHost.ghost).toBeUndefined()
    forgetProbeDraft('h')
    expect(useProbeDraftStore.getState().byHost.h).toBeUndefined()
  })
})
