// spa/src/lib/host-utils.test.ts
import { describe, it, expect } from 'vitest'
import { connectionErrorMessage } from './host-utils'
import type { HostRuntime } from '../stores/useHostStore'

const t = (key: string) => key

describe('connectionErrorMessage', () => {
  it('returns null for undefined runtime', () => {
    expect(connectionErrorMessage(undefined, t)).toBeNull()
  })

  it('returns unreachable message when daemonState is unreachable', () => {
    const runtime: HostRuntime = { status: 'disconnected', daemonState: 'unreachable' }
    expect(connectionErrorMessage(runtime, t)).toBe('hosts.error_unreachable')
  })

  it('returns refused message when daemonState is refused', () => {
    const runtime: HostRuntime = { status: 'disconnected', daemonState: 'refused' }
    expect(connectionErrorMessage(runtime, t)).toBe('hosts.error_refused')
  })

  it('returns tmux down when connected but tmux unavailable', () => {
    const runtime: HostRuntime = { status: 'connected', daemonState: 'connected', tmuxState: 'unavailable' }
    expect(connectionErrorMessage(runtime, t)).toBe('hosts.error_tmux_down')
  })

  it('returns null when fully connected', () => {
    const runtime: HostRuntime = { status: 'connected', daemonState: 'connected', tmuxState: 'ok' }
    expect(connectionErrorMessage(runtime, t)).toBeNull()
  })

  it('returns null for reconnecting without specific daemon state', () => {
    const runtime: HostRuntime = { status: 'reconnecting' }
    expect(connectionErrorMessage(runtime, t)).toBeNull()
  })

  it('returns auth error message for auth-error status', () => {
    const runtime: HostRuntime = { status: 'auth-error', daemonState: 'auth-error' }
    expect(connectionErrorMessage(runtime, t)).toBe('hosts.error_auth')
  })
})
