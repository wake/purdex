import { describe, it, expect } from 'vitest'
import type { FleetHost, FleetWaiter } from '../fleet/types'
import type { FleetSnapshot } from '../fleet/store'
import { useI18nStore } from '../../stores/useI18nStore'
import { queuedBySession, queuedTooltip } from './queued-sessions'

const w = (over: Partial<FleetWaiter> = {}): FleetWaiter =>
  ({ class: 'heavy', place: 1, command: 'go test ./...', project: 'p', session: 'AAAA-1', since: 0, ...over })
const host = (id: string, waiters: FleetWaiter[], kind: FleetHost['kind'] = 'daemon'): FleetHost =>
  ({ id, kind, alias: id, name: id, icon: '', os: '', status: 'ok', error: null, last_ok_at: 0, fast: null,
    slow: { at: 0, top_cpu: [], top_mem: [], queue: { heavy_used: 0, heavy_total: 1, light_used: 0, light_total: 1, holders: [], waiters, longest_wait_s: 0,
      admission_held: false, admission_reason: '', heavy_paused: false, light_paused: false } } })
const snap = (...hosts: FleetHost[]): FleetSnapshot =>
  ({ hostId: 'h', term: 1, hosts: Object.fromEntries(hosts.map((h) => [h.id, h])), order: hosts.map((h) => h.id) })
const t = (k: string, p?: Record<string, string | number>) => `${k}|${JSON.stringify(p ?? {})}`

describe('queuedBySession', () => {
  it('no fleet → empty', () => {
    expect(queuedBySession(null).size).toBe(0)
  })
  it('groups the waiters of every host by lowercase session id and skips empty sessions', () => {
    const m = queuedBySession(snap(host('a', [w(), w({ session: '' })]), host('b', [w({ session: 'aaaa-1', place: 2 })])))
    expect([...m.keys()]).toEqual(['aaaa-1'])
    expect(m.get('aaaa-1')!.map((x) => x.place)).toEqual([1, 2])
  })
})

describe('queuedTooltip', () => {
  it('one waiter: place, whole minutes, command and class', () => {
    expect(queuedTooltip([w({ place: 2, since: 1000 })], 1000 + 125_000, t))
      .toBe('tab.queued.tooltip|{"place":2,"m":2,"command":"go test ./...","kind":"tab.queued.heavy|{}"}')
  })
  it('a light command says light', () => {
    expect(queuedTooltip([w({ class: 'light' })], 0, t)).toContain('tab.queued.light')
  })
  it('three waiters: the longest wait plus 「另有 2 個」', () => {
    const out = queuedTooltip([w({ command: 'new', since: 9000 }), w({ command: 'old', since: 1000 }), w({ command: 'mid', since: 5000, place: 3 })], 70_000, t)
    expect(out).toContain('"command":"old"')
    expect(out).toContain('"m":1')
    expect(out.endsWith('tab.queued.more|{"n":2}')).toBe(true)
  })
  it('an unknown start counts as the shortest wait', () => {
    expect(queuedTooltip([w({ command: 'x', since: null }), w({ command: 'y', since: 5 })], 10, t)).toContain('"command":"y"')
  })
})

describe('queuedTooltip with the real translator', () => {
  it('fills every placeholder (en)', () => {
    useI18nStore.getState().setLocale('en')
    const out = queuedTooltip([w({ place: 2, since: 0 }), w({ place: 3, since: 5000 })], 125_000, useI18nStore.getState().t)
    expect(out).toBe('Queued: #2 · waiting 2 min · go test ./... (heavy command) · 1 more')
  })
  it('fills every placeholder (zh-TW)', () => {
    useI18nStore.getState().setLocale('zh-TW')
    const out = queuedTooltip([w({ class: 'light', place: 1, since: 0 })], 61_000, useI18nStore.getState().t)
    useI18nStore.getState().setLocale('en')
    expect(out).toBe('排隊中：第 1 位・已等 1 分鐘・go test ./...（輕指令）')
  })
})
