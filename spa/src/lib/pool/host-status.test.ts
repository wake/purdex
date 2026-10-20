// The host page's 狀態 data (monitoring tabs spec M44; plan MON-7b-1): pure functions from the pool's facts and host row to what the six cards and the facts table say. Words are i18n keys, never text.
import { describe, it, expect } from 'vitest'
import { connectionOf, identityOf, judgeOf, pausesOf, tagsOf, workerCardOf } from './host-status'
import { poolHostState } from './host-state'
import type { HostAlertsEntry } from './alerts-store'
import type { PoolAlert } from './alerts-api'
import type { MonitorHost } from './use-monitor-hosts'
import { parseFacts, parseHostsAnswer } from './parse'
import type { PoolExceptionDetail, PoolFacts, PoolHost } from './types'
import darwinJson from './__fixtures__/facts_darwin.json'
import hostsJson from './__fixtures__/pool_hosts_response.json'

const darwin = parseFacts(darwinJson)!
const baseHost = parseHostsAnswer(hostsJson)![0]
const pool = (over: Partial<PoolHost> = {}): PoolHost => ({ ...baseHost, ...over })
const withFacts = (over: (f: PoolFacts) => PoolFacts): PoolFacts => over(JSON.parse(JSON.stringify(darwin)) as PoolFacts)
const disk = (used: number) => ({ mount: '/', free_gb: 10, used_pct: used, total_bytes: 1, avail_bytes: 1 })
const part = (j: ReturnType<typeof judgeOf>, id: string) => j.parts.find((p) => p.id === id)!

describe('judgeOf', () => {
  it('the zone, the level and the page rate it rests on, the slots and the sessions room', () => {
    const f = withFacts((x) => ({ ...x, mem: { ...x.mem, pressure: 2, page_out_rate: 12 }, derived: { ...x.derived, zone: 'warning', headroom: { heavy: 1, light: 3 }, sessions_room: 4 } }))
    const j = judgeOf(f, pool())
    expect(j).toMatchObject({ zone: 'warning', level: 2, pageRate: 12, sessionsRoom: 4 })
    expect([j.heavy.n, j.light.n]).toEqual([1, 3])
  })

  it('M8: a composite load of 91 held up by the disk while the zone is normal: the disk part is the daemon\'s driver, and counted', () => {
    const f = withFacts((x) => ({ ...x, disk: [disk(40), disk(91)], derived: { ...x.derived, zone: 'normal' } }))
    const j = judgeOf(f, pool({ load: { score: 91, driver: 'disk' } }))
    expect(j.zone).toBe('normal')
    expect(j.load).toEqual({ score: 91, driver: 'disk' })
    expect(part(j, 'disk')).toMatchObject({ raw: 91, score: 91, counted: true, driver: true })
    expect(j.parts.filter((p) => p.driver).map((p) => p.id)).toEqual(['disk'])
  })

  it('a disk at 80 % does not count (85 is where it starts): score 0, the raw reading kept', () => {
    const j = judgeOf(withFacts((x) => ({ ...x, disk: [disk(80)] })), pool({ load: { score: 30, driver: 'cpu' } }))
    expect(part(j, 'disk')).toMatchObject({ raw: 80, score: 0, counted: false, driver: false })
  })

  it('a disk at exactly 85 counts', () => {
    expect(part(judgeOf(withFacts((x) => ({ ...x, disk: [disk(85)] })), pool()), 'disk')).toMatchObject({ counted: true, score: 85 })
  })

  it('the parts, in the ring\'s order: CPU is 100 minus idle, memory is the used share, pressure is the level\'s score, paging is capped at 100', () => {
    const f = withFacts((x) => ({ ...x, cpu: { ...x.cpu, idle_pct: 70 }, mem: { ...x.mem, total_mb: 1000, available_mb: 250, pressure: 2, page_out_rate: 400 } }))
    const j = judgeOf(f, pool())
    expect(j.parts.map((p) => p.id)).toEqual(['cpu', 'mem', 'pressure', 'paging', 'disk'])
    expect(part(j, 'cpu')).toMatchObject({ raw: 30, score: 30 })
    expect(part(j, 'mem')).toMatchObject({ raw: 75, score: 75 })
    expect(part(j, 'pressure')).toMatchObject({ raw: 2, score: 75 })
    expect(part(j, 'paging')).toMatchObject({ raw: 400, score: 100 })
  })

  it('an unknown pressure (0) and an unknown page rate score 0 with no raw reading', () => {
    const j = judgeOf(withFacts((x) => ({ ...x, mem: { ...x.mem, pressure: 0, page_out_rate: null } })), pool())
    expect(j.level).toBeNull()
    expect(j.pageRate).toBeNull()
    expect(part(j, 'pressure')).toMatchObject({ raw: null, score: 0 })
    expect(part(j, 'paging')).toMatchObject({ raw: null, score: 0 })
  })

  it('no disk at all: that part has no reading', () => {
    expect(part(judgeOf(withFacts((x) => ({ ...x, disk: [] })), pool()), 'disk')).toMatchObject({ raw: null, score: 0, counted: false })
  })

  it('no composite load from the daemon: no main driver, none invented', () => {
    const j = judgeOf(darwin, pool({ load: undefined }))
    expect(j.load).toBeNull()
    expect(j.parts.some((p) => p.driver)).toBe(false)
  })

  it('the reasons are the host row\'s; a daemon without pool.assess.v1 gives null, not an invented reason', () => {
    expect(judgeOf(darwin, pool({ headroom_reasons: undefined })).heavy.codes).toBeNull()
    const j = judgeOf(darwin, pool({ headroom_reasons: { heavy: ['slots_full'], light: [] } }))
    expect(j.heavy.codes).toEqual(['slots_full'])
    expect(j.light.codes).toEqual([])
  })
})

describe('workerCardOf', () => {
  it('the switch, the slots, the running, the owner rule in minutes and the owner reading', () => {
    const f = withFacts((x) => ({ ...x, worker: { ...x.worker, enabled: true, slots: { heavy: 2, light: 4 }, running: { heavy: 1, light: 0 }, owner_rule: 'hid<5m' }, owner: { active: true, idle_s: 30, reason: 'hid' } }))
    expect(workerCardOf(f)).toEqual({ enabled: true, slots: { heavy: 2, light: 4 }, running: { heavy: 1, light: 0 }, ownerRule: 'hid<5m', ownerMinutes: 5, owner: { active: true, idle_s: 30, reason: 'hid' } })
  })

  it('no owner reading (Linux) is null, not an idle owner; a rule that is not a minutes rule has no minutes', () => {
    const w = workerCardOf(withFacts((x) => ({ ...x, owner: null, worker: { ...x.worker, owner_rule: '' } })))
    expect(w.owner).toBeNull()
    expect(w.ownerMinutes).toBeNull()
  })
})

describe('pausesOf', () => {
  const at = 1_000_000
  const exc = (name: string, since: number, until: number | null) => ({ name, since, until })
  const detail = (name: string, over: Partial<PoolExceptionDetail> = {}): PoolExceptionDetail => ({ name, until: at + 60_000, pid: null, holds: ['heavy'], ...over })

  it('lists the pauses in force at the record\'s own time, by start time, with what each holds', () => {
    const list = pausesOf([exc('b', at - 10, at + 60_000), exc('a', at - 100, at + 60_000)], [detail('a', { holds: ['heavy', 'light'] }), detail('b')], at)
    expect(list.map((p) => p.name)).toEqual(['a', 'b'])
    expect(list[0]).toEqual({ name: 'a', since: at - 100, until: at + 60_000, pid: null, holds: ['heavy', 'light'] })
  })

  it('one that has ended, or has not begun, is not listed; one with no end is', () => {
    const list = pausesOf([exc('over', at - 100, at - 1), exc('later', at + 1, null), exc('open', at - 5, null)], [], at)
    expect(list.map((p) => p.name)).toEqual(['open'])
  })

  it('judged at the record\'s time, not the App\'s clock', () => {
    const ex = [exc('x', 1000, 2000)]
    expect(pausesOf(ex, [], 1500)).toHaveLength(1)
    expect(pausesOf(ex, [], Date.now())).toHaveLength(0)
  })

  it('what is not read (no details at all) is "not known what it holds", not "holds nothing"', () => {
    const list = pausesOf([exc('a', at - 1, at + 1000)], null, at)
    expect(list[0]).toMatchObject({ pid: null, holds: null })
  })

  it('a pause that follows a process carries its pid; one the details do not name is not known', () => {
    const list = pausesOf([exc('p', at - 1, null), exc('q', at - 2, null)], [detail('p', { pid: 4242, until: null })], at)
    expect(list.find((p) => p.name === 'p')).toMatchObject({ pid: 4242, holds: ['heavy'] })
    expect(list.find((p) => p.name === 'q')).toMatchObject({ pid: null, holds: null })
  })
})

describe('connectionOf', () => {
  const NOW = 10_000_000
  const peerRow = (over: Partial<PoolHost> = {}): PoolHost => pool({ alias: 'mserv', host_id: 'h-mserv', liveness: 'wwd_down', liveness_at: NOW - 3000, liveness_since: NOW - 600_000, facts: null, ...over })
  const monitor = (ph: PoolHost, local = false): MonitorHost => ({ hostId: ph.host_id, alias: ph.alias, daemonHostId: null, state: poolHostState(ph, null, NOW), look: {} as never, poolFailure: null, count: null, local })
  const hosts = [{ hostId: 'h-mlab', alias: 'mlab' }, { hostId: 'h-mserv', alias: 'mserv' }, { hostId: 'h-air26', alias: 'air26' }]
  const silence = (about: string, since: number, clearedAt: number | null = null): PoolAlert => ({ key: `silence:${about}`, kind: 'silence', level: 'push', since, last: since, clearedAt, detail: `${about} 停了`, silence: 'host_down' })
  const ok = (alerts: PoolAlert[]): HostAlertsEntry => ({ kind: 'ok', known: true, alerts, at: NOW })

  it('the local host is told apart: no liveness of its own', () => {
    expect(connectionOf(monitor(pool({ liveness: undefined }), true), {}, hosts)).toEqual({ kind: 'local' })
    expect(connectionOf(monitor(pool({ liveness: undefined })), {}, hosts)).toEqual({ kind: 'local' }) // a row with no liveness is the same
  })

  it('the host the answering daemon reports as its own is local even if its row carries a liveness', () => {
    expect(connectionOf(monitor(peerRow(), true), {}, hosts)).toEqual({ kind: 'local' })
  })

  it('a peer: the daemon\'s word, since when, when it was last checked', () => {
    const c = connectionOf(monitor(peerRow()), {}, hosts)
    expect(c).toMatchObject({ kind: 'peer', state: 'wwd_down', since: NOW - 600_000, checkedAt: NOW - 3000 })
  })

  it('the outages are the silences about this host; the same stop in two hosts\' lists is one', () => {
    const s = silence('mserv', NOW - 900_000, NOW - 600_000)
    const c = connectionOf(monitor(peerRow()), { mlab: ok([s]), air26: ok([s]) }, hosts)
    expect(c.kind === 'peer' && c.outages.rows).toHaveLength(1)
  })

  it('a silence about another host is not this host\'s outage', () => {
    const c = connectionOf(monitor(peerRow()), { mlab: ok([silence('air26', NOW - 1000)]) }, hosts)
    expect(c.kind === 'peer' && c.outages.rows).toHaveLength(0)
  })

  it('an ordinary alert is not an outage', () => {
    const other: PoolAlert = { key: 'orphans:mserv', kind: 'orphans', level: 'digest', since: NOW - 10, last: NOW, clearedAt: null, detail: '' }
    const c = connectionOf(monitor(peerRow()), { mserv: ok([other]) }, hosts)
    expect(c.kind === 'peer' && c.outages.rows).toHaveLength(0)
  })

  it('six stops: the newest five, and "n more"', () => {
    const stops = Array.from({ length: 6 }, (_, i) => silence('mserv', NOW - (i + 1) * 100_000, NOW - (i + 1) * 100_000 + 10_000))
    const c = connectionOf(monitor(peerRow()), { mlab: ok(stops) }, hosts)
    expect(c.kind === 'peer' && c.outages.rows).toHaveLength(5)
    expect(c.kind === 'peer' && c.outages.more).toBe(1)
    expect(c.kind === 'peer' && c.outages.rows[0].at).toBe(NOW - 100_000) // newest first
  })

  it('a list that could not be read adds nothing', () => {
    const c = connectionOf(monitor(peerRow()), { mlab: { kind: 'error', code: 'no_answer', at: NOW } }, hosts)
    expect(c.kind === 'peer' && c.outages.rows).toHaveLength(0)
  })
})

describe('identityOf', () => {
  it('the wwd version and proto, the facts version, the platform and the OS, the host id', () => {
    const p = pool({ version: '1.2.3', proto: 2, platform: 'darwin', arch: 'arm64', host_id: 'h-1' })
    expect(identityOf(p, withFacts((x) => ({ ...x, v: 2, os_version: '15.1' })))).toEqual({ version: '1.2.3', proto: 2, factsV: 2, platform: 'darwin', arch: 'arm64', osVersion: '15.1', hostId: 'h-1' })
  })

  it('no facts (the host is not ok): the facts version and the OS are null, the rest is the row\'s', () => {
    expect(identityOf(pool({ host_id: 'h-1' }), null)).toMatchObject({ factsV: null, osVersion: null, hostId: 'h-1' })
  })
})

describe('tagsOf', () => {
  it('the labels in groups: task:, repo:, anything else', () => {
    const t = tagsOf(pool({ labels: ['task:lint', 'repo:purdex', 'os:darwin'] }), darwin)
    expect(t.labels).toEqual([{ text: 'task:lint', group: 'task' }, { text: 'repo:purdex', group: 'repo' }, { text: 'os:darwin', group: 'other' }])
  })

  it('the tools: a string as it is, a list joined, an empty list and null as "no value"', () => {
    const f = withFacts((x) => ({ ...x, tools: { go: '1.26', node: ['22', '24'], php: [], docker: null } }))
    expect(tagsOf(pool(), f).tools).toEqual([{ name: 'go', value: '1.26' }, { name: 'node', value: '22、24' }, { name: 'php', value: null }, { name: 'docker', value: null }])
  })

  it('the fingerprint is the row\'s, one entry per key', () => {
    expect(tagsOf(pool({ fingerprint: { cpu: 'Apple M4', ram: '16 GB' } }), darwin).fingerprint).toEqual([{ name: 'cpu', value: 'Apple M4' }, { name: 'ram', value: '16 GB' }])
    expect(tagsOf(pool(), darwin).fingerprint).toEqual([])
  })

  it('no facts: the tools are empty, the labels and the fingerprint still the row\'s', () => {
    const t = tagsOf(pool({ labels: ['os:linux'], fingerprint: { cpu: 'x' } }), null)
    expect(t.tools).toEqual([])
    expect(t.labels).toHaveLength(1)
    expect(t.fingerprint).toEqual([{ name: 'cpu', value: 'x' }])
  })
})
