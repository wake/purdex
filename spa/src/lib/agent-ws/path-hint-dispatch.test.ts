import { describe, it, expect, beforeEach } from 'vitest'
import { usePathCacheStore } from '../../stores/path-cache/usePathCacheStore'
import { scopeKey } from '../../stores/path-cache/path-utils'
import { handlePathHintEvent } from './path-hint-dispatch'

const v1 = (overrides: Record<string, unknown> = {}) =>
  JSON.stringify({
    schemaVersion: 1,
    agentId: 'cc',
    sessionCode: 'sess',
    cwd: '/repo',
    dir: '/repo/src',
    kind: 'read',
    timestamp: '2026-04-27T00:00:00Z',
    ...overrides,
  })

beforeEach(() => {
  usePathCacheStore.setState({ entriesByScope: {} } as never, false)
})

describe('handlePathHintEvent', () => {
  it('v1 payload adds dir to (host, cwd) scope', () => {
    handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sess', value: v1() })
    const list = usePathCacheStore.getState().entriesByScope[scopeKey('h1', '/repo')]
    expect(list?.map((e) => e.dir)).toEqual(['/repo/src'])
    expect(list?.[0]?.sessionCode).toBe('sess')
  })

  it('schemaVersion !== 1 → drop', () => {
    handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sess', value: v1({ schemaVersion: 2 }) })
    expect(Object.keys(usePathCacheStore.getState().entriesByScope)).toEqual([])
  })

  it('non-absolute cwd → drop', () => {
    handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sess', value: v1({ cwd: 'rel/cwd' }) })
    expect(Object.keys(usePathCacheStore.getState().entriesByScope)).toEqual([])
  })

  it('non-absolute dir → drop', () => {
    handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sess', value: v1({ dir: 'rel/dir' }) })
    expect(Object.keys(usePathCacheStore.getState().entriesByScope)).toEqual([])
  })

  it('invalid kind → drop', () => {
    handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sess', value: v1({ kind: 'delete' }) })
    expect(Object.keys(usePathCacheStore.getState().entriesByScope)).toEqual([])
  })

  it('missing sessionCode → drop', () => {
    handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sess', value: v1({ sessionCode: '' }) })
    expect(Object.keys(usePathCacheStore.getState().entriesByScope)).toEqual([])
  })

  it('envelope.session !== payload.sessionCode → drop (R2-D3)', () => {
    handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'envSess', value: v1({ sessionCode: 'payloadSess' }) })
    expect(Object.keys(usePathCacheStore.getState().entriesByScope)).toEqual([])
  })

  it('payload exceeding MAX_PAYLOAD_BYTES → drop (R2-A2)', () => {
    const huge = v1({ dir: '/repo/' + 'x'.repeat(70 * 1024) })
    handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sess', value: huge })
    expect(Object.keys(usePathCacheStore.getState().entriesByScope)).toEqual([])
  })

  it('malformed JSON → drop without throwing', () => {
    expect(() =>
      handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sess', value: 'not-json' }),
    ).not.toThrow()
    expect(Object.keys(usePathCacheStore.getState().entriesByScope)).toEqual([])
  })

  it('non-object JSON (array / null / primitive) → drop', () => {
    handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sess', value: '[1,2,3]' })
    handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sess', value: 'null' })
    handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sess', value: '"bare"' })
    expect(Object.keys(usePathCacheStore.getState().entriesByScope)).toEqual([])
  })

  it('two events same (host, cwd) different sessions accumulate together', () => {
    handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sessA', value: v1({ sessionCode: 'sessA', dir: '/repo/a' }) })
    handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sessB', value: v1({ sessionCode: 'sessB', dir: '/repo/b' }) })
    const list = usePathCacheStore.getState().entriesByScope[scopeKey('h1', '/repo')]
    expect(list?.map((e) => e.dir)).toEqual(['/repo/b', '/repo/a'])
    expect(list?.map((e) => e.sessionCode)).toEqual(['sessB', 'sessA'])
  })

  it('events from different cwds isolate scopes', () => {
    handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sess', value: v1({ cwd: '/repo-a', dir: '/repo-a/x' }) })
    handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sess', value: v1({ cwd: '/repo-b', dir: '/repo-b/y' }) })
    expect(usePathCacheStore.getState().entriesByScope[scopeKey('h1', '/repo-a')]?.[0]?.dir).toBe('/repo-a/x')
    expect(usePathCacheStore.getState().entriesByScope[scopeKey('h1', '/repo-b')]?.[0]?.dir).toBe('/repo-b/y')
  })
})
