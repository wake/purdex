import { describe, it, expect } from 'vitest'
import { connectionKey } from './host-connection-key'

describe('connectionKey', () => {
  it('is equal for the same endpoint and token, and treats null / absent token as no token', () => {
    expect(connectionKey({ ip: '1.2.3.4', port: 7860, token: 't' })).toBe(connectionKey({ ip: '1.2.3.4', port: 7860, token: 't' }))
    expect(connectionKey({ ip: '1.2.3.4', port: 7860, token: null })).toBe(connectionKey({ ip: '1.2.3.4', port: 7860 }))
    expect(connectionKey({ ip: '1.2.3.4', port: 7860, token: null })).toBe(connectionKey({ ip: '1.2.3.4', port: 7860, token: '' }))
  })

  it('differs on ip, port or token', () => {
    const base = { ip: '1.2.3.4', port: 7860, token: 't' }
    expect(connectionKey({ ...base, ip: '1.2.3.5' })).not.toBe(connectionKey(base))
    expect(connectionKey({ ...base, port: 7861 })).not.toBe(connectionKey(base))
    expect(connectionKey({ ...base, token: 'u' })).not.toBe(connectionKey(base))
  })

  it('does not collide when the ip or token carries the separator a joined key would use', () => {
    // joined as `${ip}:${port}:${token}` both read `h:1:2:x`
    expect(connectionKey({ ip: 'h', port: 1, token: '2:x' })).not.toBe(connectionKey({ ip: 'h:1', port: 2, token: 'x' }))
  })
})
