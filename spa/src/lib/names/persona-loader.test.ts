import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.mock('../../stores/usePersonaStore', () => {
  const load = vi.fn(async () => {})
  const forget = vi.fn()
  return { usePersonaStore: { getState: () => ({ load, forget }) } }
})

vi.mock('./persona-backfill', () => ({ backfillHosts: vi.fn(async () => {}) }))

import { usePersonaStore } from '../../stores/usePersonaStore'
import { backfillHosts } from './persona-backfill'
import { useNexHostStore, startNexHostInvalidation } from '../../stores/useNexHostStore'
import { useHostStore } from '../../stores/useHostStore'
import { loadPersonasIfSupported, handlePersonasEvent, startPersonaLoader } from './persona-loader'

const ensure = vi.fn(async () => {})
const entry = (caps: string[], fetchedAt = 1) => ({ phase: 'ready', daemonCapabilities: caps, fetchedAt }) as never
const setEntry = (hostId: string, caps: string[], fetchedAt = 1) =>
  useNexHostStore.setState({ byHost: { [hostId]: entry(caps, fetchedAt) }, ensure })
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

  it('compares the hosts after a read, so a host that arrived late is brought up to the source', async () => {
    setEntry('h1', ['personas.v1'])
    await loadPersonasIfSupported('h1')
    expect(backfillHosts).toHaveBeenCalled()
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

  it('on an endpoint move asks for capabilities only after the Nex entry was cleared, then loads', async () => {
    setEntry('h1', ['personas.v1'])
    useHostStore.setState({ hosts: { h1: host(7860) }, runtime: { h1: { status: 'connected' } as never } })
    const stopPersona = startPersonaLoader() // started first, as in main.tsx
    const stopNex = startNexHostInvalidation()
    load().mockClear()
    ensure.mockClear()
    let entryAtEnsure: unknown = 'unset'
    ensure.mockImplementation(async () => { entryAtEnsure = useNexHostStore.getState().byHost.h1 })
    useHostStore.setState({ hosts: { h1: host(7861) } })
    expect(forget()).toHaveBeenCalledWith('h1')
    await Promise.resolve()
    expect(ensure).toHaveBeenCalledWith('h1')
    expect(entryAtEnsure).toBeUndefined() // the old entry was already cleared
    setEntry('h1', ['personas.v1']) // the refetched entry for the new endpoint
    expect(load()).toHaveBeenCalledWith('h1')
    stopNex()
    stopPersona()
  })

  it('never asks a reconnected daemon while its capability entry is stale', () => {
    setEntry('h1', ['personas.v1'])
    const stop = startPersonaLoader()
    load().mockClear()
    setEntry('h1', ['personas.v1'], 0) // markStale: old capabilities kept, fetchedAt 0
    handlePersonasEvent('h1')
    expect(load()).not.toHaveBeenCalled()
    setEntry('h1', [], 2) // refetch: the daemon was downgraded
    expect(load()).not.toHaveBeenCalled()
    expect(forget()).toHaveBeenCalledWith('h1')
    stop()
  })

  it('forgets a host that was removed', () => {
    const stop = startPersonaLoader()
    useHostStore.setState({ hosts: { h1: { id: 'h1' } as never }, runtime: {} })
    useHostStore.setState({ hosts: {} })
    expect(forget()).toHaveBeenCalledWith('h1')
    stop()
  })
})
