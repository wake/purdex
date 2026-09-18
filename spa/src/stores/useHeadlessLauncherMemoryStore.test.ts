import { describe, it, expect, beforeEach } from 'vitest'
import { useHeadlessLauncherMemoryStore } from './useHeadlessLauncherMemoryStore'

describe('useHeadlessLauncherMemoryStore', () => {
  beforeEach(() => {
    useHeadlessLauncherMemoryStore.setState({ byHost: {} })
  })

  it('persists under the purdex-headless-launcher key', () => {
    expect(useHeadlessLauncherMemoryStore.persist.getOptions().name).toBe('purdex-headless-launcher')
  })

  it('remember stores root and profile per host', () => {
    useHeadlessLauncherMemoryStore.getState().remember('h1', { root: '/srv/dev', profile: 'strict' })
    useHeadlessLauncherMemoryStore.getState().remember('h2', { root: '/srv/svc', profile: 'handoff' })
    expect(useHeadlessLauncherMemoryStore.getState().byHost).toEqual({
      h1: { root: '/srv/dev', profile: 'strict' },
      h2: { root: '/srv/svc', profile: 'handoff' },
    })
  })

  it('remember overwrites a host without touching the others', () => {
    useHeadlessLauncherMemoryStore.getState().remember('h1', { root: '/a', profile: 'p' })
    useHeadlessLauncherMemoryStore.getState().remember('h2', { root: '/b', profile: 'q' })
    useHeadlessLauncherMemoryStore.getState().remember('h1', { root: '/c', profile: 'r' })
    expect(useHeadlessLauncherMemoryStore.getState().byHost).toEqual({
      h1: { root: '/c', profile: 'r' },
      h2: { root: '/b', profile: 'q' },
    })
  })

  it('forgetHost drops that host only', () => {
    useHeadlessLauncherMemoryStore.getState().remember('h1', { root: '/a', profile: 'p' })
    useHeadlessLauncherMemoryStore.getState().remember('h2', { root: '/b', profile: 'q' })
    useHeadlessLauncherMemoryStore.getState().forgetHost('h1')
    expect(useHeadlessLauncherMemoryStore.getState().byHost).toEqual({ h2: { root: '/b', profile: 'q' } })
  })

  it('forgetHost on an unknown host is a no-op', () => {
    useHeadlessLauncherMemoryStore.getState().remember('h1', { root: '/a', profile: 'p' })
    const before = useHeadlessLauncherMemoryStore.getState().byHost
    useHeadlessLauncherMemoryStore.getState().forgetHost('nope')
    expect(useHeadlessLauncherMemoryStore.getState().byHost).toBe(before)
  })
})
