// M36: the events tab's label carries the number of active push-level alerts (「事件 n」), read from the alerts store.
import { describe, it, expect, afterEach } from 'vitest'
import { getPaneLabel } from './pane-labels'
import type { TFunction } from './pane-labels'
import type { PaneContent } from '../types/tab'
import { useAlertsStore } from './pool/alerts-store'
import type { PoolAlert } from './pool/alerts-api'

const mockT: TFunction = (key, params) => (params ? `${key}:${JSON.stringify(params)}` : key)
const al = (key: string, over: Partial<PoolAlert> = {}): PoolAlert => ({ key, kind: 'disk_low', level: 'push', since: 1, last: 2, clearedAt: null, detail: key, ...over })
const ok = (alerts: PoolAlert[]) => ({ kind: 'ok' as const, known: true, alerts, at: 1 })
const label = (c: PaneContent) => getPaneLabel(c, { getByCode: () => undefined }, { getById: () => undefined }, mockT)

afterEach(() => useAlertsStore.getState().reset())

describe('the events tab label', () => {
  const content: PaneContent = { kind: 'watch-events' }
  it('is 事件 alone while no push-level alert is active', () => {
    expect(label(content)).toBe('watch.pane.events')
    useAlertsStore.setState({ byHost: { mlab: ok([al('a', { clearedAt: 9 }), al('b', { level: 'digest' })]) } })
    expect(label(content)).toBe('watch.pane.events') // cleared and digest-level alerts do not count
  })
  it('is 事件 n with the active push alerts of all hosts, one silence key counted once', () => {
    const silence = al('silence:mserv', { kind: 'silence', silence: 'host_down' })
    useAlertsStore.setState({ byHost: { mlab: ok([silence, al('disk_low:mlab')]), air26: ok([silence]) } })
    expect(label(content)).toBe('watch.pane.events_n:{"n":2}')
  })
})
