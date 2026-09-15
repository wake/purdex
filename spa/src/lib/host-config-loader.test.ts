import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { startHostConfigLoader } from './host-config-loader'
import { useHostStore } from '../stores/useHostStore'
import { emptyHostConfigEntry, useHostConfigStore } from '../stores/useHostConfigStore'

const host = (id: string, ip = '100.64.0.2') => ({ id, name: id, ip, port: 7860, token: null, order: 0 })
let stop: () => void = () => {}
const load = vi.fn(async (_hostId: string) => {})

beforeEach(() => {
  load.mockClear()
  useHostConfigStore.setState({ byHost: {}, load })
  useHostStore.setState({ hosts: { h1: host('h1'), h2: host('h2') }, hostOrder: ['h1', 'h2'], runtime: { h2: { status: 'connected' } } })
})
afterEach(() => stop())

describe('startHostConfigLoader', () => {
  it('loads hosts already connected at start', () => {
    stop = startHostConfigLoader()
    expect(load).toHaveBeenCalledWith('h2')
    expect(load).not.toHaveBeenCalledWith('h1')
  })

  it('loads a host when it transitions to connected, once', () => {
    stop = startHostConfigLoader()
    load.mockClear()
    useHostStore.getState().setRuntime('h1', { status: 'connected' })
    useHostStore.getState().setRuntime('h1', { latency: 3 })
    expect(load).toHaveBeenCalledTimes(1)
    expect(load).toHaveBeenCalledWith('h1')
  })

  it('forgets a removed host', () => {
    useHostConfigStore.setState({ byHost: { h1: emptyHostConfigEntry('ready') } })
    stop = startHostConfigLoader()
    useHostStore.setState({ hosts: { h2: host('h2') }, hostOrder: ['h2'] })
    expect(useHostConfigStore.getState().byHost.h1).toBeUndefined()
  })

  it('an endpoint change forgets the host and reloads it if connected', () => {
    useHostConfigStore.setState({ byHost: { h2: emptyHostConfigEntry('ready') } })
    stop = startHostConfigLoader()
    load.mockClear()
    useHostStore.setState({ hosts: { h1: host('h1'), h2: host('h2', '100.64.0.4') } })
    expect(useHostConfigStore.getState().byHost.h2).toBeUndefined()
    expect(load).toHaveBeenCalledWith('h2')
  })
})
