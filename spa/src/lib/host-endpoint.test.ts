import { describe, it, expect } from 'vitest'
import { hostScheme, deriveDaemonBase, deriveWsBase, hostEndpointKey } from './host-endpoint'

describe('host-endpoint', () => {
  it('scheme 缺省視為 http（向後相容）', () => {
    expect(hostScheme({ scheme: undefined })).toBe('http')
    expect(hostScheme({ scheme: 'https' })).toBe('https')
  })

  it('deriveDaemonBase：缺 scheme → http://ip:port', () => {
    expect(deriveDaemonBase({ scheme: undefined, ip: '100.64.0.2', port: 7860 }))
      .toBe('http://100.64.0.2:7860')
  })

  it('deriveDaemonBase：https + 443 省略 port', () => {
    expect(deriveDaemonBase({ scheme: 'https', ip: 'purdex.mlab.host', port: 443 }))
      .toBe('https://purdex.mlab.host')
  })

  it('deriveDaemonBase：https + 非預設 port 保留', () => {
    expect(deriveDaemonBase({ scheme: 'https', ip: 'purdex.mlab.host', port: 8443 }))
      .toBe('https://purdex.mlab.host:8443')
  })

  it('deriveWsBase：http → ws、https → wss，並套用預設 port 省略', () => {
    expect(deriveWsBase({ scheme: undefined, ip: '100.64.0.2', port: 7860 }))
      .toBe('ws://100.64.0.2:7860')
    expect(deriveWsBase({ scheme: 'https', ip: 'purdex.mlab.host', port: 443 }))
      .toBe('wss://purdex.mlab.host')
    expect(deriveWsBase({ scheme: 'https', ip: 'purdex.mlab.host', port: 8443 }))
      .toBe('wss://purdex.mlab.host:8443')
  })

  it('hostEndpointKey：含 scheme，http 與 https 不同 key', () => {
    expect(hostEndpointKey({ scheme: undefined, ip: 'h', port: 7860 })).toBe('http:h:7860')
    expect(hostEndpointKey({ scheme: 'https', ip: 'h', port: 7860 })).toBe('https:h:7860')
  })

  it('deriveDaemonBase / deriveWsBase：對稱 port 省略規則（只省略該 scheme 的預設 port）', () => {
    expect(deriveDaemonBase({ scheme: 'http', ip: 'h', port: 80 })).toBe('http://h')
    expect(deriveDaemonBase({ scheme: 'http', ip: 'h', port: 443 })).toBe('http://h:443')
    expect(deriveDaemonBase({ scheme: 'https', ip: 'h', port: 80 })).toBe('https://h:80')
    expect(deriveWsBase({ scheme: 'http', ip: 'h', port: 80 })).toBe('ws://h')
    expect(deriveWsBase({ scheme: 'https', ip: 'h', port: 80 })).toBe('wss://h:80')
  })
})
