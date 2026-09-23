import { describe, expect, it } from 'vitest'
import type { HostConfig } from '../stores/useHostStore'
import type { TransferRow } from './host-transfer-api'
import { commitSet, isPickable, parseTransferRows, payloadRowsOf, planReceive, type Observation } from './host-transfer-plan'

function host(id: string, over: Partial<HostConfig> = {}): HostConfig {
  return { id, name: id, ip: `10.0.0.${id.length}`, port: 7860, order: 0, token: `tok-${id}`, ...over }
}

function hostsOf(...list: HostConfig[]): Record<string, HostConfig> {
  return Object.fromEntries(list.map((h) => [h.id, h]))
}

function row(over: Partial<TransferRow> = {}): TransferRow {
  return { name: 'air26', ip: '100.64.0.4', port: 7860, token: 'tok-air', ...over }
}

const ok = (hostId: string): Observation => ({ kind: 'ok', hostId })
const failed: Observation = { kind: 'failed' }

describe('parseTransferRows (R5)', () => {
  it('keeps a well-formed row with every field', () => {
    const raw = {
      name: 'air26',
      ip: '100.64.0.4',
      port: 7860,
      token: 't',
      daemonId: 'd1_air',
      look: { color: '#ff0000', colors: { console: { main: { color: '#00ff00', alpha: 80 } } }, icon: 'Laptop', iconWeight: 'bold' },
    }
    expect(parseTransferRows([raw])).toEqual({ rows: [raw], dropped: 0 })
  })

  it.each([
    ['not an object', 'x'],
    ['an array', [1]],
    ['null', null],
    ['ip missing', { port: 1, token: 't' }],
    ['ip empty', { ip: '', port: 1, token: 't' }],
    ['ip not a string', { ip: 5, port: 1, token: 't' }],
    ['ip with a slash', { ip: 'evil.com/x', port: 1, token: 't' }],
    ['port a string', { ip: 'a', port: '7860', token: 't' }],
    ['port fractional', { ip: 'a', port: 78.5, token: 't' }],
    ['port 0', { ip: 'a', port: 0, token: 't' }],
    ['port 65536', { ip: 'a', port: 65536, token: 't' }],
    ['token missing', { ip: 'a', port: 1 }],
    ['token empty', { ip: 'a', port: 1, token: '' }],
    ['token not a string', { ip: 'a', port: 1, token: 7 }],
  ])('drops a row: %s', (_label, raw) => {
    expect(parseTransferRows([raw])).toEqual({ rows: [], dropped: 1 })
  })

  // F1: the ip is spliced into `http://${ip}:${port}` for the probe and stored as the host's address, so anything
  // that is not a bare hostname / dotted-quad IPv4 would change which URL is requested (a fragment, a query, userinfo,
  // an extra port) — the probe then answers for a different address than the one stored.
  it.each([
    ['a fragment', 'attacker.example#ignored'],
    ['a query', 'attacker.example?x=1'],
    ['userinfo', 'user@attacker.example'],
    ['a backslash', 'attacker.example\\x'],
    ['a space', 'mlab host'],
    ['a leading space', ' mlab.host'],
    ['a tab', 'mlab\thost'],
    ['a control character', 'mlab\u0001host'],
    ['a DEL', 'mlab\u007fhost'],
    ['percent-encoding', 'attacker.example%2Fx'],
    ['a port', 'host:1'],
    ['bare IPv6 (`http://::1:7860` is not a URL; only the bracketed form is accepted)', '::1'],
    ['an unclosed IPv6 bracket', '[::1'],
    ['an unopened IPv6 bracket', '::1]'],
    ['empty brackets', '[]'],
    ['a non-hex IPv6 digit', '[g::1]'],
    ['text after the IPv6 bracket', '[::1]x'],
    ['an IPv6 zone id', '[fe80::1%25eth0]'],
    ['an IPv6 literal the URL parser rejects', '[:::1]'],
    ['an underscore', 'my_host'],
    ['a leading dot', '.mlab.host'],
    ['an empty label', 'mlab..host'],
    ['a leading hyphen', '-mlab.host'],
    ['an IPv4 octet over 255', '100.64.0.256'],
    ['a three-part numeric address', '100.64.4'],
    ['a zero-padded octet (octal to a URL parser)', '010.0.0.1'],
    ['a hex IPv4 form', '0x7f.0.0.1'],
    ['a numeric last label', 'mlab.123'],
    ['non-ASCII', 'mläb.host'],
  ])('drops a row whose ip has %s', (_label, ip) => {
    expect(parseTransferRows([{ ip, port: 1, token: 't' }])).toEqual({ rows: [], dropped: 1 })
  })

  it.each(['mlab.host', 'air-2026', 'localhost', 'A1.Example.COM', '100.64.0.4', '127.0.0.1', '255.255.255.255', '0.0.0.0'])(
    'keeps a row whose ip is %s',
    (ip) => {
      expect(parseTransferRows([{ ip, port: 1, token: 't' }]).rows.map((r) => r.ip)).toEqual([ip])
    },
  )

  // PR #1397 critic: the add-host dialog stores whatever the user typed (trimmed) and builds `http://${ip}:${port}`,
  // so a bracketed IPv6 is a working host there — the receive side must not silently drop it.
  it.each(['[::1]', '[fe80::1]', '[2001:db8::1]', '[::ffff:100.64.0.2]'])('keeps a row whose ip is the bracketed IPv6 %s', (ip) => {
    expect(parseTransferRows([{ ip, port: 1, token: 't' }])).toEqual({ rows: [{ name: ip, ip, port: 1, token: 't' }], dropped: 0 })
  })

  it('counts every bad-ip row as dropped, keeping the good ones', () => {
    const res = parseTransferRows([
      { ip: 'a#b', port: 1, token: 't' },
      { ip: 'ok.host', port: 1, token: 't' },
      { ip: 'x?y', port: 1, token: 't' },
      { ip: 'h:1', port: 1, token: 't' },
    ])
    expect(res.dropped).toBe(3)
    expect(res.rows.map((r) => r.ip)).toEqual(['ok.host'])
  })

  it('accepts the port bounds 1 and 65535', () => {
    expect(parseTransferRows([{ ip: 'a', port: 1, token: 't' }, { ip: 'b', port: 65535, token: 't' }]).rows).toHaveLength(2)
  })

  it('derives the name from ip when it is missing or not a string', () => {
    const { rows } = parseTransferRows([{ ip: 'a', port: 1, token: 't' }, { ip: 'b', port: 1, token: 't', name: 5 }])
    expect(rows.map((r) => r.name)).toEqual(['a', 'b'])
  })

  it('drops an invalid daemonId but keeps the row', () => {
    const { rows } = parseTransferRows([{ ip: 'a', port: 1, token: 't', daemonId: '' }, { ip: 'b', port: 1, token: 't', daemonId: 'x\u0000y' }])
    expect(rows).toHaveLength(2)
    expect(rows.every((r) => !('daemonId' in r))).toBe(true)
  })

  it('drops malformed look fields one by one, keeping the row and the good fields', () => {
    const { rows, dropped } = parseTransferRows([
      { ip: 'a', port: 1, token: 't', look: { color: 'red', icon: 'NotAnIcon', iconWeight: 'heavy', colors: { console: { main: { color: '#123456', alpha: 50 } } } } },
      { ip: 'b', port: 1, token: 't', look: 'nope' },
      { ip: 'c', port: 1, token: 't', look: { color: 'red' } },
    ])
    expect(dropped).toBe(0)
    expect(rows[0].look).toEqual({ colors: { console: { main: { color: '#123456', alpha: 50 } } } })
    expect('look' in rows[1]).toBe(false)
    expect('look' in rows[2]).toBe(false)
  })

  it('counts dropped rows and keeps the good ones in order', () => {
    const res = parseTransferRows([{ ip: 'a', port: 1, token: 't' }, 1, { ip: 'b', port: 99999, token: 't' }, { ip: 'c', port: 2, token: 't' }])
    expect(res.dropped).toBe(2)
    expect(res.rows.map((r) => r.ip)).toEqual(['a', 'c'])
  })

  it('ignores unknown fields', () => {
    expect(parseTransferRows([{ ip: 'a', port: 1, token: 't', evil: 1 }]).rows[0]).toEqual({ name: 'a', ip: 'a', port: 1, token: 't' })
  })
})

describe('payloadRowsOf', () => {
  it('sends only hosts with a token; daemonId and look fields only when set', () => {
    const rows = payloadRowsOf([
      host('a', { daemonId: 'd1_a', icon: 'Laptop', color: '#ff0000' }),
      host('b', { token: null }),
      host('c', { token: '' }),
      host('dd'),
    ])
    expect(rows).toEqual([
      { name: 'a', ip: '10.0.0.1', port: 7860, token: 'tok-a', daemonId: 'd1_a', look: { icon: 'Laptop', color: '#ff0000' } },
      { name: 'dd', ip: '10.0.0.2', port: 7860, token: 'tok-dd' },
    ])
  })
})

describe('share → receive round trip', () => {
  it('a host whose ip is a bracketed IPv6 survives from payloadRowsOf through parseTransferRows (PR #1397 critic)', () => {
    const sent = payloadRowsOf([host('v6', { ip: '[::1]' }), host('v4')])
    const received = parseTransferRows(JSON.parse(JSON.stringify(sent)))
    expect(received.dropped).toBe(0)
    expect(received.rows).toEqual(sent)
    expect(received.rows.map((r) => r.ip)).toEqual(['[::1]', '10.0.0.2'])
  })
})

describe('planReceive (§6.4.3)', () => {
  it('new: verified, no local row has that daemonId', () => {
    const [p] = planReceive([row()], [ok('d1_air')], hostsOf(host('m', { daemonId: 'd1_m' })))
    expect(p.status).toBe('new')
    expect(p.observed).toBe('d1_air')
  })

  it('existing: exactly one local row has that daemonId; the target carries endpoint, token and daemonId', () => {
    const local = host('m', { daemonId: 'd1_air', ip: '1.1.1.1', port: 1, token: 'old' })
    const [p] = planReceive([row()], [ok('d1_air')], hostsOf(local))
    expect(p.status).toBe('existing')
    expect(p.target).toEqual({ hostId: 'm', endpoint: '1.1.1.1:1', token: 'old', daemonId: 'd1_air' })
  })

  it('existing at the same endpoint as the local row (a pure token refresh) is still existing', () => {
    const local = host('m', { daemonId: 'd1_air', ip: '100.64.0.4', port: 7860 })
    expect(planReceive([row()], [ok('d1_air')], hostsOf(local))[0].status).toBe('existing')
  })

  it('mismatch: the payload daemonId differs from the observed one', () => {
    expect(planReceive([row({ daemonId: 'd1_other' })], [ok('d1_air')], {})[0].status).toBe('mismatch')
  })

  it('unverified: unreachable / auth failed', () => {
    expect(planReceive([row()], [failed], {})[0].status).toBe('unverified')
  })

  it('unverified: the daemon reports no (or an invalid) id', () => {
    expect(planReceive([row(), row({ ip: 'x' })], [ok(''), ok('a\u0000b')], {}).map((p) => p.status)).toEqual(['unverified', 'unverified'])
  })

  it('duplicate: a second row with the same observed daemonId — the first wins', () => {
    const plan = planReceive([row(), row({ ip: 'other' })], [ok('d1_air'), ok('d1_air')], {})
    expect(plan.map((p) => p.status)).toEqual(['new', 'duplicate'])
  })

  it('a mismatch row does not claim its observed id: a later row that verifies as that id is new, not duplicate', () => {
    const plan = planReceive([row({ daemonId: 'd1_fake' }), row({ ip: 'other', daemonId: 'd1_real' })], [ok('d1_real'), ok('d1_real')], {})
    expect(plan.map((p) => p.status)).toEqual(['mismatch', 'new'])
  })

  it('a mismatch row does not claim its observed id: the later row is existing when a local row has that id', () => {
    const local = hostsOf(host('m', { daemonId: 'd1_real' }))
    const plan = planReceive([row({ daemonId: 'd1_fake' }), row({ ip: 'other' })], [ok('d1_real'), ok('d1_real')], local)
    expect(plan.map((p) => p.status)).toEqual(['mismatch', 'existing'])
  })

  it('local-conflict: two local rows claim the daemonId', () => {
    const local = hostsOf(host('a', { daemonId: 'd1_air' }), host('bb', { daemonId: 'd1_air' }))
    expect(planReceive([row()], [ok('d1_air')], local)[0].status).toBe('local-conflict')
  })

  it('local-conflict: the endpoint equals a local row with a different daemonId', () => {
    const local = hostsOf(host('a', { ip: '100.64.0.4', port: 7860, daemonId: 'd1_someone' }))
    expect(planReceive([row()], [ok('d1_air')], local)[0].status).toBe('local-conflict')
  })

  it('local-conflict: the endpoint equals a local row that has no daemonId yet', () => {
    const local = hostsOf(host('a', { ip: '100.64.0.4', port: 7860 }))
    expect(planReceive([row()], [ok('d1_air')], local)[0].status).toBe('local-conflict')
  })

  it('a payload without daemonId but verified is new / existing by the observed id', () => {
    const local = hostsOf(host('m', { daemonId: 'd1_m' }))
    const plan = planReceive([row({ ip: 'x' }), row({ ip: 'y' })], [ok('d1_new'), ok('d1_m')], local)
    expect(plan.map((p) => p.status)).toEqual(['new', 'existing'])
  })

  it('only new and existing are pickable; existing only in overwrite mode', () => {
    expect(isPickable('new', 'add-only')).toBe(true)
    expect(isPickable('existing', 'add-only')).toBe(false)
    expect(isPickable('existing', 'overwrite')).toBe(true)
    for (const s of ['mismatch', 'unverified', 'duplicate', 'local-conflict'] as const) {
      expect(isPickable(s, 'overwrite')).toBe(false)
    }
  })
})

describe('commitSet', () => {
  const look = { icon: 'Laptop' }
  const local = hostsOf(host('m', { daemonId: 'd1_m', ip: '1.1.1.1', port: 1, token: 'old' }))
  const preview = planReceive(
    [row({ look }), row({ ip: '2.2.2.2', name: 'mm', token: 'new' }), row({ ip: 'bad' })],
    [ok('d1_air'), ok('d1_m'), failed],
    local,
  )

  it('add-only: only new rows, carrying the observed daemonId and the look', () => {
    expect(commitSet(preview, new Set([0, 1, 2]), 'add-only')).toEqual({
      create: [{ name: 'air26', ip: '100.64.0.4', port: 7860, token: 'tok-air', daemonId: 'd1_air', look }],
      overwrite: [],
    })
  })

  it('overwrite: existing rows become overwrites against the planned target', () => {
    expect(commitSet(preview, new Set([0, 1, 2]), 'overwrite').overwrite).toEqual([
      {
        hostId: 'm',
        expect: { endpoint: '1.1.1.1:1', token: 'old', daemonId: 'd1_m' },
        name: 'mm',
        ip: '2.2.2.2',
        port: 7860,
        token: 'new',
      },
    ])
  })

  it('an unpicked row is left out', () => {
    expect(commitSet(preview, new Set([1]), 'overwrite').create).toEqual([])
  })
})
