import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.mock('../../stores/usePersonaStore', () => {
  const load = vi.fn(async () => {})
  const forget = vi.fn()
  return { usePersonaStore: { getState: () => ({ load, forget }) } }
})

import { usePersonaStore } from '../../stores/usePersonaStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { useHostStore } from '../../stores/useHostStore'
import { loadPersonasIfSupported, handlePersonasEvent, startPersonaLoader } from './persona-loader'

function setCaps(hostId: string, caps: string[]) {
  useNexHostStore.setState({
    byHost: { [hostId]: { phase: 'ready', daemonCapabilities: caps } } as never,
    ensure: async () => {},
  })
}

describe('persona loader', () => {
  beforeEach(() => {
    vi.mocked(usePersonaStore.getState().load).mockClear()
    vi.mocked(usePersonaStore.getState().forget).mockClear()
  })

  it('loads from a daemon that declares personas.v1', async () => {
    setCaps('h1', ['personas.v1'])
    await loadPersonasIfSupported('h1')
    expect(usePersonaStore.getState().load).toHaveBeenCalledWith('h1')
  })

  it('touches nothing on a daemon without the capability', async () => {
    setCaps('h1', ['watch.v1'])
    await loadPersonasIfSupported('h1')
    handlePersonasEvent('h1')
    await Promise.resolve()
    expect(usePersonaStore.getState().load).not.toHaveBeenCalled()
  })

  it('reloads on a hostconfig.personas event', async () => {
    setCaps('h1', ['personas.v1'])
    handlePersonasEvent('h1')
    await vi.waitFor(() => expect(usePersonaStore.getState().load).toHaveBeenCalledWith('h1'))
  })

  it('forgets and reloads a connected host whose endpoint moved', async () => {
    setCaps('h1', ['personas.v1'])
    const host = (port: number) => ({ id: 'h1', ip: '10.0.0.1', port, token: 't' }) as never
    useHostStore.setState({ hosts: { h1: host(7860) }, runtime: { h1: { status: 'connected' } as never } })
    const stop = startPersonaLoader()
    vi.mocked(usePersonaStore.getState().load).mockClear()
    useHostStore.setState({ hosts: { h1: host(7861) } })
    expect(usePersonaStore.getState().forget).toHaveBeenCalledWith('h1')
    await vi.waitFor(() => expect(usePersonaStore.getState().load).toHaveBeenCalledWith('h1'))
    stop()
  })

  it('forgets a host that was removed', () => {
    const stop = startPersonaLoader()
    useHostStore.setState({ hosts: { h1: { id: 'h1' } as never }, runtime: {} })
    useHostStore.setState({ hosts: {} })
    expect(usePersonaStore.getState().forget).toHaveBeenCalledWith('h1')
    stop()
  })
})
