import { describe, it, expect, beforeEach } from 'vitest'
import { usePersonaSourceStore, resolveSourceHostId } from './usePersonaSourceStore'

describe('resolveSourceHostId', () => {
  const hosts = { h1: {}, h2: {}, h3: {} }

  it('uses the chosen host when this device still has it', () => {
    expect(resolveSourceHostId('h2', ['h1', 'h2', 'h3'], hosts)).toBe('h2')
  })

  it('falls back to the first host of the App when nothing is chosen', () => {
    expect(resolveSourceHostId(null, ['h1', 'h2', 'h3'], hosts)).toBe('h1')
  })

  it('falls back when the chosen host was removed from this device', () => {
    expect(resolveSourceHostId('gone', ['h3', 'h1'], hosts)).toBe('h3')
  })

  it('skips an order entry the host table no longer has', () => {
    expect(resolveSourceHostId(null, ['ghost', 'h2'], hosts)).toBe('h2')
  })

  it('is null when the App has no host', () => {
    expect(resolveSourceHostId(null, [], {})).toBeNull()
  })
})

describe('usePersonaSourceStore', () => {
  beforeEach(() => {
    localStorage.clear()
    usePersonaSourceStore.setState({ hostId: null })
  })

  it('holds the choice, and null means the default', () => {
    expect(usePersonaSourceStore.getState().hostId).toBeNull()
    usePersonaSourceStore.getState().setSource('h2')
    expect(usePersonaSourceStore.getState().hostId).toBe('h2')
    usePersonaSourceStore.getState().setSource(null)
    expect(usePersonaSourceStore.getState().hostId).toBeNull()
  })

  it('writes the choice to this device, and reads it back after a reload', async () => {
    usePersonaSourceStore.getState().setSource('h2')
    const stored = JSON.parse(localStorage.getItem('purdex-persona-source') ?? '{}') as { state?: { hostId?: string } }
    expect(stored.state?.hostId).toBe('h2')
    // what a reload does: the memory is empty, the stored value is still there
    localStorage.setItem('purdex-persona-source', JSON.stringify({ state: { hostId: 'h3' }, version: 1 }))
    await usePersonaSourceStore.persist.rehydrate()
    expect(usePersonaSourceStore.getState().hostId).toBe('h3')
  })

  it('ignores a corrupted stored value instead of breaking', async () => {
    localStorage.setItem('purdex-persona-source', JSON.stringify({ state: { hostId: 42 }, version: 1 }))
    await usePersonaSourceStore.persist.rehydrate()
    expect(usePersonaSourceStore.getState().hostId).toBeNull()
  })
})
