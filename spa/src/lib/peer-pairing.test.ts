import { describe, it, expect } from 'vitest'
import {
  aliasDrift, matchCounterpart, matchReturnEntry, normalizePeerUrl, pairStatus, toOutcome,
  type InboundState, type PairStatus, type Side, type VerifyOutcome,
} from './peer-pairing'
import type { PeerHostRow } from './host-api'

const OK: VerifyOutcome = { ok: true, self_alias: 'air26', daemon_version: '1.0.0-alpha.378', host_id: 'wakes-air-2026:oa6drb' }
const FAIL: VerifyOutcome = { ok: false, error: 'dial tcp: host is down' }

describe('normalizePeerUrl', () => {
  it('matches the daemon: scheme+host, trailing slash trimmed, host lower-cased', () => {
    expect(normalizePeerUrl('http://100.64.0.4:7860')).toBe('http://100.64.0.4:7860')
    expect(normalizePeerUrl('http://100.64.0.4:7860/')).toBe('http://100.64.0.4:7860')
    expect(normalizePeerUrl('HTTP://Air.Local:7860/')).toBe('http://air.local:7860')
    expect(normalizePeerUrl('http://h:7860/base/')).toBe('http://h:7860/base')
  })
  it('returns the trimmed input when it does not parse, so garbage never equals garbage by accident', () => {
    expect(normalizePeerUrl('not a url')).toBe('not a url')
    expect(normalizePeerUrl('')).toBe('')
  })
})

describe('matchCounterpart (spec D-3, §5.2 step 2)', () => {
  const air = { hostId: 'hA', host_id: 'wakes-air-2026:oa6drb', url: 'http://100.64.0.4:7860' }
  const other = { hostId: 'hB', host_id: 'other:111111', url: 'http://100.64.0.9:7860' }
  const unknown = { hostId: 'hU', host_id: '', url: 'http://100.64.0.7:7860' }   // unavailable host

  it('host_id wins even when URLs differ', () => {
    const entry = { host_id: 'wakes-air-2026:oa6drb', url: 'http://air.local:7860' }
    expect(matchCounterpart(entry, [other, air])).toBe(air)
  })
  it('URL is the fallback when the entry has no host_id (normalised, trailing slash ignored)', () => {
    const entry = { host_id: '', url: 'http://100.64.0.4:7860/' }
    expect(matchCounterpart(entry, [other, air])).toBe(air)
  })
  it('URL also joins an entry to a host whose host_id is unknown (unavailable), so it can be "could not be asked"', () => {
    const entry = { host_id: 'somebody:abcdef', url: 'http://100.64.0.7:7860' }
    expect(matchCounterpart(entry, [air, unknown])).toBe(unknown)
  })
  it('same URL but a different KNOWN host_id is not a match — that is another daemon at that address', () => {
    const entry = { host_id: 'reinstalled:zzzzzz', url: 'http://100.64.0.4:7860' }
    expect(matchCounterpart(entry, [air])).toBeNull()
  })
  it('an entry with neither a host_id match nor a URL match is null', () => {
    expect(matchCounterpart({ host_id: 'x:1', url: 'http://1.2.3.4:1' }, [air, other, unknown])).toBeNull()
  })
  it('an entry and a candidate that are both host_id-less match only by URL, never by "" === ""', () => {
    const entry = { host_id: '', url: 'http://9.9.9.9:1' }
    expect(matchCounterpart(entry, [unknown])).toBeNull()
  })
})

describe('matchReturnEntry — the same rule with the roles swapped', () => {
  const row = (p: Partial<PeerHostRow>): PeerHostRow => ({
    alias: 'mini-lab', url: 'http://100.64.0.2:7860', host_id: 'mini-lab:278cbm',
    verified: true, has_token: true, has_inbound_token: true, allow_bypass: true, ...p,
  })
  const self = { host_id: 'mini-lab:278cbm', url: 'http://100.64.0.2:7860' }

  it('returns the row whose host_id is ours, whatever it is named', () => {
    const r = row({ alias: 'mlab' })
    expect(matchReturnEntry(self, [row({ alias: 'x', host_id: 'other:1' }), r])).toBe(r)
  })
  it('falls back to URL only for a row with no host_id', () => {
    const r = row({ alias: 'by-url', host_id: '', url: 'http://100.64.0.2:7860/' })
    expect(matchReturnEntry(self, [r])).toBe(r)
    expect(matchReturnEntry(self, [row({ host_id: 'stranger:1' })])).toBeNull()
  })
  it('returns null on an empty list', () => expect(matchReturnEntry(self, [])).toBeNull())
})

describe('pairStatus — every row of the §5.1 table', () => {
  const cases: Array<[string, Side, InboundState, PairStatus]> = [
    ['ok / ok', OK, OK, 'bidirectional'],
    ['ok / failed', OK, FAIL, 'one-way'],
    ['ok / no-entry', OK, 'no-entry', 'one-way'],
    ['ok / not-app-host', OK, 'not-app-host', 'outbound-only'],
    ['ok / counterpart-unavailable', OK, 'counterpart-unavailable', 'return-unknown'],
    ['failed / ok', FAIL, OK, 'one-way'],
    ['failed / failed', FAIL, FAIL, 'unpaired'],
    ['failed / no-entry', FAIL, 'no-entry', 'unpaired'],
    ['failed / not-app-host', FAIL, 'not-app-host', 'unpaired'],
    ['failed / counterpart-unavailable', FAIL, 'counterpart-unavailable', 'unpaired'],
    ['pending / ok', 'pending', OK, 'checking'],
    ['ok / pending', OK, 'pending', 'checking'],
    ['pending / not-app-host', 'pending', 'not-app-host', 'checking'],
    ['pending / pending', 'pending', 'pending', 'checking'],
  ]
  it.each(cases)('%s → %s', (_name, outbound, inbound, want) => {
    expect(pairStatus(outbound, inbound)).toBe(want)
  })
  it('outbound-only and return-unknown are never the same word (D-4 vs transient)', () => {
    expect(pairStatus(OK, 'not-app-host')).not.toBe(pairStatus(OK, 'counterpart-unavailable'))
  })
})

describe('aliasDrift (same rule as cmd/pdx aliasDriftField)', () => {
  it('returns the self alias when it differs', () => expect(aliasDrift('air', 'air26')).toBe('air26'))
  it('empty self alias is not drift', () => expect(aliasDrift('air', '')).toBe(''))
  it('case-insensitive equal is not drift', () => expect(aliasDrift('Air', 'aIR')).toBe(''))
  it('whitespace is not trimmed away — the daemon bounded it, we compare what it said', () => {
    expect(aliasDrift('air', 'air ')).toBe('air ')
  })
})

describe('toOutcome', () => {
  it('maps ok:true to the success shape and ok:false to {error}', () => {
    expect(toOutcome({ alias: 'air', host_id: 'h:1', ok: true, self_alias: 'air26', daemon_version: 'v' }))
      .toEqual({ ok: true, self_alias: 'air26', daemon_version: 'v', host_id: 'h:1' })
    expect(toOutcome({ alias: 'air', host_id: 'h:1', ok: false, error: 'no outbound token', self_alias: '', daemon_version: '' }))
      .toEqual({ ok: false, error: 'no outbound token' })
  })
  it('an ok:false with no error text still names a cause (belt and braces over the daemon guarantee)', () => {
    expect(toOutcome({ alias: 'a', host_id: '', ok: false, self_alias: '', daemon_version: '' }))
      .toEqual({ ok: false, error: 'peer reported ok=false' })
  })
})
