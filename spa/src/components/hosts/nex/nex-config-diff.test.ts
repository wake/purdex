import { describe, it, expect } from 'vitest'
import { restartRequired, emptyNexConfig, parseGoDuration } from './nex-config-diff'
import type { NexInfo } from '../../../lib/host-api'

const eff = { data_dir: '/d', claude_bin: '', cswap_bin: '', max_profile: 'handoff', default_profile: '', repo_roots: ['/a', '/b'], service_roots: [], path_prefix: '', lease_ttl: '2m0s', interrupt: '10s', turn: '5m0s' }
const info = (over: Partial<NexInfo> = {}): NexInfo => ({ configured: true, mounted: true, ready: true, init_error: '', effective: eff, ...over })
const saved = (over = {}) => ({ ...emptyNexConfig(), enabled: true, repo_roots: ['/b', '/a'], sandbox: { max_profile: 'handoff', default_profile: '' }, ...over })

describe('restartRequired', () => {
  it('false when saved matches effective (root order ignored, empty timeouts ignored)', () => {
    expect(restartRequired(saved(), info())).toBe(false)
  })
  it('true when enabled differs from mounted', () => {
    expect(restartRequired(saved({ enabled: false }), info())).toBe(true)
    expect(restartRequired(saved(), info({ mounted: false, ready: false, effective: null }))).toBe(true)
  })
  it('true when roots, bins, profiles or a non-empty timeout differ', () => {
    expect(restartRequired(saved({ repo_roots: ['/a'] }), info())).toBe(true)
    expect(restartRequired(saved({ claude_bin: '/x/claude' }), info())).toBe(true)
    expect(restartRequired(saved({ sandbox: { max_profile: 'standard', default_profile: '' } }), info())).toBe(true)
    expect(restartRequired(saved({ timeouts: { lease_ttl: '90s', interrupt: '', turn: '' } }), info())).toBe(true)
    expect(restartRequired(saved({ timeouts: { lease_ttl: '2m', interrupt: '', turn: '' } }), info())).toBe(false) // 2m == 2m0s
  })
  it('false when nothing is saved and nex is not mounted (fresh host)', () => {
    expect(restartRequired(undefined, info({ configured: false, mounted: false, ready: false, effective: null }))).toBe(false)
  })
})

describe('parseGoDuration', () => {
  it('parses Go composite duration forms into a comparable number of seconds', () => {
    expect(parseGoDuration('90s')).toBe(90)
    expect(parseGoDuration('2m0s')).toBe(120)
    expect(parseGoDuration('2m')).toBe(120)
    expect(parseGoDuration('1h2m3s')).toBe(3723)
    expect(parseGoDuration('1m30s')).toBe(90)
    expect(parseGoDuration('500ms')).toBeCloseTo(0.5)
  })
  it('returns null for unparsable input', () => {
    expect(parseGoDuration('soon')).toBeNull()
    expect(parseGoDuration('')).toBeNull()
    expect(parseGoDuration('5')).toBeNull()
  })
})
