import { describe, expect, it } from 'vitest'
import { parseDaemonConfig, pickBindAddress, renderInitialConfig, generateToken, DEFAULT_DATA_DIR } from './config'

const home = '/Users/x'

describe('parseDaemonConfig', () => {
  it('applies Go defaults for a missing key', () => {
    expect(parseDaemonConfig('', home)).toEqual({ bind: '127.0.0.1', port: 7860, token: null, dataDir: `${home}/.config/pdx` })
  })
  it('reads the keys the daemon rewrites (full TOML from EnsureHostID)', () => {
    const toml = `host_id = "mini:abc123"\nbind = "100.64.0.2"\nport = 7861\ntoken = "purdex_ff"\ndata_dir = "/Users/x/.config/pdx"\n\n[dev]\nupdate = false\n`
    expect(parseDaemonConfig(toml, home)).toEqual({ bind: '100.64.0.2', port: 7861, token: 'purdex_ff', dataDir: '/Users/x/.config/pdx' })
  })
  it('surfaces a custom data_dir verbatim', () => {
    expect(parseDaemonConfig('data_dir = "/Volumes/X/pdx"\n', home).dataDir).toBe('/Volumes/X/pdx')
  })
  it('throws on invalid TOML', () => {
    expect(() => parseDaemonConfig('bind = ', home)).toThrow()
  })
})

describe('pickBindAddress (spec D4 + §3.1 configure)', () => {
  const ts = { name: 'utun4', address: '100.64.0.9', family: 'IPv4', internal: false }
  it('one utun in 100.64/10 on darwin → that address', () => {
    expect(pickBindAddress([{ name: 'en0', address: '192.168.1.5', family: 'IPv4', internal: false }, ts], 'darwin')).toEqual({ bind: '100.64.0.9' })
  })
  it('none → loopback with a note', () => {
    const r = pickBindAddress([{ name: 'en0', address: '192.168.1.5', family: 'IPv4', internal: false }], 'darwin')
    expect(r.bind).toBe('127.0.0.1')
    expect(r.note).toMatch(/no tailscale/i)
  })
  it('two candidates → loopback with a note listing them', () => {
    const r = pickBindAddress([ts, { ...ts, name: 'utun5', address: '100.100.1.1' }], 'darwin')
    expect(r.bind).toBe('127.0.0.1')
    expect(r.note).toContain('100.64.0.9')
    expect(r.note).toContain('100.100.1.1')
  })
  it('rejects CGNAT addresses on non-utun interfaces on darwin, accepts them on linux', () => {
    const isp = { name: 'en0', address: '100.70.0.1', family: 'IPv4', internal: false }
    expect(pickBindAddress([isp], 'darwin').bind).toBe('127.0.0.1')
    expect(pickBindAddress([{ ...isp, name: 'tailscale0' }], 'linux').bind).toBe('100.70.0.1')
  })
  it('CIDR edges: 100.63.255.255 and 100.128.0.0 are out, 100.127.255.255 is in', () => {
    const mk = (a: string) => ({ name: 'utun1', address: a, family: 'IPv4', internal: false })
    expect(pickBindAddress([mk('100.63.255.255')], 'darwin').bind).toBe('127.0.0.1')
    expect(pickBindAddress([mk('100.128.0.0')], 'darwin').bind).toBe('127.0.0.1')
    expect(pickBindAddress([mk('100.127.255.255')], 'darwin').bind).toBe('100.127.255.255')
  })
})

describe('renderInitialConfig / generateToken', () => {
  it('renders the exact initial file', () => {
    expect(renderInitialConfig('100.64.0.9', 'purdex_abc')).toBe(
      'bind = "100.64.0.9"\nport = 7860\ntoken = "purdex_abc"\n\n[dev]\nupdate = false\n',
    )
  })
  it('token is purdex_ + 40 hex', () => {
    const t = generateToken((n) => Buffer.alloc(n, 0xab))
    expect(t).toBe('purdex_' + 'ab'.repeat(20))
  })
  it('DEFAULT_DATA_DIR', () => {
    expect(DEFAULT_DATA_DIR(home)).toBe('/Users/x/.config/pdx')
  })
})
