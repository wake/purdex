import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.mock('../../stores/usePersonaStore', () => {
  const load = vi.fn(async () => {})
  const forget = vi.fn()
  return { usePersonaStore: { getState: () => ({ load, forget }) } }
})

import { usePersonaStore } from '../../stores/usePersonaStore'
import { useNexHostStore, startNexHostInvalidation } from '../../stores/useNexHostStore'
import { useHostStore } from '../../stores/useHostStore'
import { loadPersonasIfSupported, handlePersonasEvent, startPersonaLoader } from './persona-loader'

const ensure = vi.fn(async () => {})
const entry = (caps: string[]) => ({ phase: 'ready', daemonCapabilities: caps }) as never
const setEntry = (hostId: string, caps: string[]) =>
  useNexHostStore.setState({ byHost: { [hostId]: entry(caps) }, ensure })
const host = (port: number) => ({ id: 'h1', ip: '10.0.0.1', port, token: 't' }) as never
const load = () => usePersonaStore.getState().load as ReturnType<typeof vi.fn>
const forget = () => usePersonaStore.getState().forget as ReturnType<typeof vi.fn>

describe('persona loader', () => {
  beforeEach(() => {
    load().mockClear()
    forget().mockClear()
    ensure.mockClear()
    useNexHostStore.setState({ byHost: {}, ensure })
    useHostStore.setState({ hosts: {}, runtime: {} })
  })

  it('loads from a daemon that declares personas.v1', async () => {
    setEntry('h1', ['personas.v1'])
    await loadPersonasIfSupported('h1')
    expect(load()).toHaveBeenCalledWith('h1')
  })

  it('touches nothing on a daemon without the capability', async () => {
    setEntry('h1', ['watch.v1'])
    await loadPersonasIfSupported('h1')
    handlePersonasEvent('h1')
    await Promise.resolve()
    expect(load()).not.toHaveBeenCalled()
  })

  it('reloads on a hostconfig.personas event', async () => {
    setEntry('h1', ['personas.v1'])
    handlePersonasEvent('h1')
    await vi.waitFor(() => expect(load()).toHaveBeenCalledWith('h1'))
  })

  it('loads when the capability entry lands, whichever subscriber ran first', () => {
    const stop = startPersonaLoader()
    setEntry('h1', ['personas.v1'])
    expect(load()).toHaveBeenCalledWith('h1')
    stop()
  })

  it('forgets when a refreshed entry no longer declares personas.v1 (downgrade)', () => {
    setEntry('h1', ['personas.v1'])
    const stop = startPersonaLoader()
    setEntry('h1', [])
    expect(forget()).toHaveBeenCalledWith('h1')
    expect(load()).not.toHaveBeenCalled()
    stop()
  })

  it('on an endpoint move forgets, then loads from the new daemon once its entry lands', () => {
    setEntry('h1', ['personas.v1'])
    useHostStore.setState({ hosts: { h1: host(7860) }, runtime: { h1: { status: 'connected' } as never } })
    const stopPersona = startPersonaLoader()
    const stopNex = startNexHostInvalidation() // the real invalidation, subscribed after the loader
    load().mockClear()
    ensure.mockClear()
    useHostStore.setState({ hosts: { h1: host(7861) } })
    expect(forget()).toHaveBeenCalledWith('h1')
    expect(ensure).toHaveBeenCalledWith('h1')
    setEntry('h1', ['personas.v1']) // the refetched entry for the new endpoint
    expect(load()).toHaveBeenCalledWith('h1')
    stopNex()
    stopPersona()
  })

  it('forgets a host that was removed', () => {
    const stop = startPersonaLoader()
    useHostStore.setState({ hosts: { h1: { id: 'h1' } as never }, runtime: {} })
    useHostStore.setState({ hosts: {} })
    expect(forget()).toHaveBeenCalledWith('h1')
    stop()
  })
})
