import { describe, it, expect, beforeEach } from 'vitest'
import { useHostStore } from './useHostStore'

describe('useHostStore endpoint derivation', () => {
  beforeEach(() => { useHostStore.getState().reset() })

  it('缺 scheme 的 host → http/ws（向後相容）', () => {
    const id = useHostStore.getState().addHost({ name: 'a', ip: '10.0.0.1', port: 7860 })
    expect(useHostStore.getState().getDaemonBase(id)).toBe('http://10.0.0.1:7860')
    expect(useHostStore.getState().getWsBase(id)).toBe('ws://10.0.0.1:7860')
  })

  it('scheme=https 的 host → https/wss，443 省略 port', () => {
    const id = useHostStore.getState().addHost({
      name: 'web', ip: 'purdex.mlab.host', port: 443, scheme: 'https',
    })
    expect(useHostStore.getState().getDaemonBase(id)).toBe('https://purdex.mlab.host')
    expect(useHostStore.getState().getWsBase(id)).toBe('wss://purdex.mlab.host')
  })

  it('updateHost 可改 scheme', () => {
    const id = useHostStore.getState().addHost({ name: 'a', ip: 'h', port: 443 })
    useHostStore.getState().updateHost(id, { scheme: 'https' })
    expect(useHostStore.getState().getDaemonBase(id)).toBe('https://h')
  })
})
