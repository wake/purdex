import { beforeEach, describe, expect, it } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import { deleteHostCascade } from '../host-lifecycle'
import { STORAGE_KEYS } from '../storage/keys'
import { presenceProjection, revPatch } from './host-projection'

// PA-2 D8 contract: every way the host list can change either bumps `hostsRev` (when the part the presence reporter
// uses — id, endpoint, token — changed) or leaves it alone (when it did not). A path that forgets the bump leaves the
// main-process reporter on a stale list.

const A = { id: 'ha', name: 'a', ip: '10.0.0.1', port: 7860, token: 'ta', order: 0, daemonId: 'mlab:aaaaaa' }
const B = { id: 'hb', name: 'b', ip: '10.0.0.2', port: 7860, token: 'tb', order: 1 }

beforeEach(() => {
  useHostStore.getState().reset()
  useHostStore.setState({ hosts: { ha: { ...A }, hb: { ...B } }, hostOrder: ['ha', 'hb'], activeHostId: 'ha', hostsRev: 5 })
})

const rev = () => useHostStore.getState().hostsRev

describe('host-projection', () => {
  it('projection ignores name, order and daemonId; sees endpoint and token', () => {
    const base = useHostStore.getState().hosts
    expect(presenceProjection({ ...base, ha: { ...A, name: 'x', order: 9, daemonId: 'other:1' } })).toBe(presenceProjection(base))
    expect(presenceProjection({ ...base, ha: { ...A, port: 1 } })).not.toBe(presenceProjection(base))
    expect(presenceProjection({ ...base, ha: { ...A, token: 'z' } })).not.toBe(presenceProjection(base))
  })

  it('revPatch is empty for an equal projection and always moves forward otherwise', () => {
    const base = useHostStore.getState().hosts
    expect(revPatch(5, base, { ...base })).toEqual({})
    expect(revPatch(Number.MAX_SAFE_INTEGER - 10, base, { ...base, ha: { ...A, port: 2 } }).hostsRev).toBe(Number.MAX_SAFE_INTEGER - 9)
  })
})

describe('which host mutation paths bump hostsRev', () => {
  it('addHost bumps', () => {
    useHostStore.getState().addHost({ name: 'c', ip: '10.0.0.3', port: 1 })
    expect(rev()).toBeGreaterThan(5)
  })

  it('removeHost bumps', () => {
    useHostStore.getState().removeHost('hb')
    expect(rev()).toBeGreaterThan(5)
  })

  it('updateHost: endpoint bumps, token bumps, name-only does not', () => {
    useHostStore.getState().updateHost('ha', { name: 'renamed' })
    expect(rev()).toBe(5)
    useHostStore.getState().updateHost('ha', { port: 7861 })
    const afterEndpoint = rev()
    expect(afterEndpoint).toBeGreaterThan(5)
    useHostStore.getState().updateHost('ha', { token: 'new' })
    expect(rev()).toBeGreaterThan(afterEndpoint)
  })

  it('applyHostTransfer: create bumps, overwrite of the endpoint bumps, a name-only overwrite does not', () => {
    const apply = useHostStore.getState().applyHostTransfer
    expect(apply({ create: [{ name: 'n', ip: '10.0.0.9', port: 2, token: 'tn', daemonId: 'mlab:bbbbbb' }], overwrite: [] }).kind).toBe('applied')
    const afterCreate = rev()
    expect(afterCreate).toBeGreaterThan(5)
    const expectA = { endpoint: '10.0.0.1:7860', token: 'ta', daemonId: 'mlab:aaaaaa' }
    expect(apply({ create: [], overwrite: [{ hostId: 'ha', expect: expectA, name: 'only-name', ip: '10.0.0.1', port: 7860, token: 'ta' }] }).kind).toBe('applied')
    expect(rev()).toBe(afterCreate)
    expect(apply({ create: [], overwrite: [{ hostId: 'ha', expect: expectA, name: 'only-name', ip: '10.0.0.7', port: 7860, token: 'ta' }] }).kind).toBe('applied')
    expect(rev()).toBeGreaterThan(afterCreate)
  })

  it('applyHostTransfer that no longer fits (stale) changes nothing', () => {
    const r = useHostStore.getState().applyHostTransfer({ create: [], overwrite: [{ hostId: 'ha', expect: { endpoint: 'x:1', token: null, daemonId: 'mlab:aaaaaa' }, name: 'n', ip: 'x', port: 1, token: null }] })
    expect(r.kind).toBe('stale')
    expect(rev()).toBe(5)
  })

  it('registerLocalHost: a new endpoint bumps; an existing one with a token does not; an existing one without a token bumps', () => {
    const { registerLocalHost } = useHostStore.getState()
    registerLocalHost({ url: 'http://10.0.0.50:7860', token: 'tl', hostname: 'local' })
    const afterNew = rev()
    expect(afterNew).toBeGreaterThan(5)
    registerLocalHost({ url: 'http://10.0.0.1:7860', token: 'ignored', hostname: 'ha' })
    expect(rev()).toBe(afterNew)
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, hb: { ...s.hosts.hb, token: null } } }))
    registerLocalHost({ url: 'http://10.0.0.2:7860', token: 'filled', hostname: 'hb' })
    expect(rev()).toBeGreaterThan(afterNew)
  })

  it('reorderHosts does not bump', () => {
    useHostStore.getState().reorderHosts(['hb', 'ha'])
    expect(rev()).toBe(5)
  })

  it('delete-undo restore bumps (the delete bumps too, the restore goes higher)', () => {
    const restore = deleteHostCascade('hb')
    const afterDelete = rev()
    expect(afterDelete).toBeGreaterThan(5)
    restore()
    expect(rev()).toBeGreaterThan(afterDelete)
    expect(useHostStore.getState().hosts.hb).toBeDefined()
  })

  it('rehydrate takes the stored rev as it is (the hand-over hook compares content, not only the rev)', async () => {
    localStorage.setItem(STORAGE_KEYS.HOSTS, JSON.stringify({
      state: { hosts: { h9: { id: 'h9', name: 'z', ip: '5.5.5.5', port: 5, token: 't', order: 0 } }, hostOrder: ['h9'], activeHostId: 'h9', devHostId: null, hostsRev: 42 },
      version: 1,
    }))
    await useHostStore.persist.rehydrate()
    expect(rev()).toBe(42)
  })
})
