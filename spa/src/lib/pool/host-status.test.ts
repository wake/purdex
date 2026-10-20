// The host page's 狀態 data (monitoring tabs spec M44; plan MON-7b-1): pure functions from the pool's facts and host row to what the six cards and the facts table say. Words are i18n keys, never text.
import { describe, it, expect } from 'vitest'
import { judgeOf } from './host-status'
import { parseFacts, parseHostsAnswer } from './parse'
import type { PoolFacts, PoolHost } from './types'
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
