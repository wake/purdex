// spa/src/lib/host-live.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { isHostDaemonLive, isHostLive } from './host-live'
import { useHostStore } from '../stores/useHostStore'

const H = 'h1'

function seed(runtime: Partial<{ status: string; tmuxState: string }> | undefined, known = true) {
  useHostStore.setState({
    hosts: known ? { [H]: { id: H, name: 'Mini', ip: '127.0.0.1', port: 7860, order: 0 } } : {},
    runtime: runtime ? { [H]: runtime as never } : {},
  })
}

beforeEach(() => seed({ status: 'connected', tmuxState: 'ok' }))

describe('isHostLive', () => {
  it('requires the host, a connected daemon and a usable tmux', () => {
    expect(isHostLive(H)).toBe(true)
    seed({ status: 'connected', tmuxState: 'unavailable' })
    expect(isHostLive(H)).toBe(false)
    seed({ status: 'disconnected', tmuxState: 'ok' })
    expect(isHostLive(H)).toBe(false)
    seed({ status: 'connected', tmuxState: 'ok' }, false)
    expect(isHostLive(H)).toBe(false)
  })
})

describe('isHostDaemonLive', () => {
  it('requires the host and a connected daemon, but not tmux', () => {
    expect(isHostDaemonLive(H)).toBe(true)
    seed({ status: 'connected', tmuxState: 'unavailable' })
    expect(isHostDaemonLive(H)).toBe(true)
    seed({ status: 'connected' })
    expect(isHostDaemonLive(H)).toBe(true)
  })

  it('is false when the daemon is not connected, the runtime is missing, or the host is unknown', () => {
    seed({ status: 'disconnected', tmuxState: 'ok' })
    expect(isHostDaemonLive(H)).toBe(false)
    seed(undefined)
    expect(isHostDaemonLive(H)).toBe(false)
    seed({ status: 'connected', tmuxState: 'ok' }, false)
    expect(isHostDaemonLive(H)).toBe(false)
  })
})
