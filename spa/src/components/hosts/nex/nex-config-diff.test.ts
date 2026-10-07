import { describe, it, expect } from 'vitest'
import { restartRequired, emptyNexConfig, normalizeNexConfig, SANDBOX_PROFILES } from './nex-config-diff'
import type { NexInfo } from '../../../lib/host-api'

const info = (over: Partial<NexInfo> = {}): NexInfo => ({ configured: true, mounted: true, ready: true, init_error: '', effective: null, ...over })

describe('SANDBOX_PROFILES', () => {
  it('lists Nexen v0.19 profiles in rank order — handoff_ask (rank 3) between trusted and handoff', () => {
    expect([...SANDBOX_PROFILES]).toEqual(['', 'readonly', 'standard', 'trusted', 'handoff_ask', 'handoff'])
  })
})

describe('restartRequired', () => {
  it('follows the daemon-computed info.restart_required', () => {
    expect(restartRequired(emptyNexConfig(), info({ restart_required: true }))).toBe(true)
    expect(restartRequired(emptyNexConfig(), info({ restart_required: false }))).toBe(false)
  })
  it('true on a soft-failed engine when the daemon says so (effective is null)', () => {
    expect(restartRequired(emptyNexConfig(), info({ ready: false, init_error: 'boom', restart_required: true }))).toBe(true)
  })
  it('false when the field is absent (older daemon) or info is not loaded', () => {
    expect(restartRequired(emptyNexConfig(), info())).toBe(false)
    expect(restartRequired(undefined, null)).toBe(false)
  })
})

describe('[nex.peer] defaults (peer mailbox spec §6, U4)', () => {
  const PEER_DEFAULT = { enabled: true, max_pending: 0, wake_template: '', reply_line: '' }

  it('emptyNexConfig has the mailbox on with Nexen defaults', () => {
    expect(emptyNexConfig().peer).toEqual(PEER_DEFAULT)
  })

  it('normalizeNexConfig fills a missing peer (older daemon) with the default', () => {
    expect(normalizeNexConfig({ enabled: true }).peer).toEqual(PEER_DEFAULT)
    expect(normalizeNexConfig(null).peer).toEqual(PEER_DEFAULT)
  })

  it('normalizeNexConfig keeps every peer key the daemon sent, untouched', () => {
    const peer = { enabled: false, max_pending: 4, wake_template: ' w {{.Text}} ', reply_line: ' r {{.ReplyTo}} ' }
    expect(normalizeNexConfig({ peer }).peer).toEqual(peer)
    expect(normalizeNexConfig({ peer: { enabled: false } as never }).peer).toEqual({ ...PEER_DEFAULT, enabled: false })
  })
})
