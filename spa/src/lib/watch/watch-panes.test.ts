import { describe, it, expect, beforeEach } from 'vitest'
import type { PaneContent, PaneLayout, Tab } from '../../types/tab'
import { getPaneLabel, getPaneIcon, type TFunction } from '../pane-labels'
import { tabToUrl } from '../route-utils'
import { contentMatches } from '../pane-utils'
import { isLightTab } from '../pane-weight'
import { layoutFromWire, layoutToWire, identityOfSync } from '../profile/host-identity'
import { ICON_MAP } from '../../components/tab-icon-map'
import { clearModuleRegistry, getModule, getPaneRenderer } from '../module-registry'
import { registerBuiltinModules } from '../register-modules'
import { useFleetStore } from '../fleet/store'
import { useAlertsStore } from '../pool/alerts-store'
import { getTabBadgePane, getTabHostId } from '../host-color'
import { usePoolStore } from '../pool/store'
import { resetRememberedPoolHosts } from '../pool/use-monitor-hosts'
import { hostIdOfTabId, watchHostTabId } from './ids'

const t: TFunction = (key) => key
const sessions = { getByCode: () => undefined }
const workspaces = { getById: () => undefined }
const host = (id: string): PaneContent => ({ kind: 'watch-host', fleetHostId: id })
const leaf = (content: PaneContent): PaneLayout => ({ type: 'leaf', pane: { id: 'p', content } })

describe('watch pane kinds', () => {
  beforeEach(() => { useFleetStore.getState().reset(); useAlertsStore.getState().reset() })

  it('the events tab carries the number of active push-level alerts (「事件 n」, M36)', () => {
    const al = (key: string, over: Record<string, unknown> = {}) => ({ key, kind: 'disk_low', level: 'push', since: 1, last: 2, clearedAt: null, detail: key, ...over })
    useAlertsStore.setState({ byHost: {
      mlab: { kind: 'ok', known: true, at: 1, alerts: [al('silence:mserv', { kind: 'silence' }), al('disk_low:mlab'), al('o', { level: 'digest' })] as never },
      air26: { kind: 'ok', known: true, at: 1, alerts: [al('silence:mserv', { kind: 'silence' })] as never },
    } })
    expect(getPaneLabel({ kind: 'watch-events' }, sessions, workspaces, (k, p) => (p ? `${k}:${p.n}` : k))).toBe('watch.pane.events_n:2')
    useAlertsStore.getState().reset()
  })
  it('labels: guardian, events, and the host alias from the fleet snapshot (id as fallback)', () => {
    expect(getPaneLabel({ kind: 'watch-guardian' }, sessions, workspaces, t)).toBe('watch.pane.guardian')
    expect(getPaneLabel({ kind: 'watch-events' }, sessions, workspaces, t)).toBe('watch.pane.events')
    expect(getPaneLabel({ kind: 'watch-jobs' }, sessions, workspaces, t)).toBe('watch.pane.jobs')
    expect(getPaneLabel(host('h1'), sessions, workspaces, t)).toBe('h1')
    useFleetStore.setState({
      snapshot: { hostId: 'p', term: 1, order: ['h1'], hosts: { h1: { id: 'h1', alias: 'mlab' } as never } },
    })
    expect(getPaneLabel(host('h1'), sessions, workspaces, t)).toBe('mlab')
  })

  it('icons exist in ICON_MAP', () => {
    expect(getPaneIcon({ kind: 'watch-guardian' })).toBe('ShieldCheck')
    expect(getPaneIcon({ kind: 'watch-events' })).toBe('ListBullets')
    expect(getPaneIcon({ kind: 'watch-jobs' })).toBe('ListChecks')
    expect(getPaneIcon(host('h'))).toBe('Desktop')
    for (const n of ['ShieldCheck', 'ListBullets', 'ListChecks', 'Desktop']) expect(ICON_MAP[n]).toBeTruthy()
  })

  it('route is /', () => {
    expect(tabToUrl('t', { kind: 'watch-guardian' })).toBe('/')
    expect(tabToUrl('t', { kind: 'watch-events' })).toBe('/')
    expect(tabToUrl('t', { kind: 'watch-jobs' })).toBe('/')
    expect(tabToUrl('t', host('h'))).toBe('/')
  })

  it('contentMatches: host by fleetHostId, the others by kind', () => {
    expect(contentMatches(host('a'), host('a'))).toBe(true)
    expect(contentMatches(host('a'), host('b'))).toBe(false)
    expect(contentMatches({ kind: 'watch-guardian' }, { kind: 'watch-guardian' })).toBe(true)
    expect(contentMatches({ kind: 'watch-events' }, { kind: 'watch-events' })).toBe(true)
    expect(contentMatches({ kind: 'watch-jobs' }, { kind: 'watch-jobs' })).toBe(true)
    expect(contentMatches({ kind: 'watch-jobs' }, { kind: 'watch-events' })).toBe(false)
  })

  it('none is a light tab', () => {
    for (const c of [{ kind: 'watch-guardian' }, { kind: 'watch-events' }, { kind: 'watch-jobs' }, host('h')] as PaneContent[]) {
      expect(isLightTab(leaf(c))).toBe(false)
    }
  })

  it('mapContent leaves fleetHostId unchanged (it is global, not an App host id)', () => {
    const layout = leaf(host('d1_abc'))
    const identity = identityOfSync({})
    expect(layoutToWire(layout, identity)).toBe(layout)
    expect(layoutFromWire(layout, () => 'MAPPED')).toBe(layout)
  })

  it('host-bearing readers ignore a watch-host pane', () => {
    const tab = { id: 't', layout: leaf(host('h')) } as unknown as Tab
    expect(getTabBadgePane(tab)).toBeNull()
    expect(getTabHostId(tab)).toBeNull()
  })

  it('registers the four kinds in one non-disableable module', () => {
    clearModuleRegistry()
    registerBuiltinModules()
    const m = getModule('watch-area')
    expect(m).toBeTruthy()
    expect(m!.disableable).toBeFalsy()
    expect(m!.panes?.map((p) => p.kind).sort()).toEqual(['watch-events', 'watch-guardian', 'watch-host', 'watch-jobs'])
    expect(getPaneRenderer('watch-jobs')).toBeTruthy()
    expect(getPaneRenderer('watch-host')).toBeTruthy()
  })
})

describe('watch-host panes that carry poolHostId (MON-6a-1)', () => {
  const pooled = (id: string): PaneContent => ({ kind: 'watch-host', poolHostId: id })
  const answer = (hosts: Array<{ host_id: string; alias: string }>) => usePoolStore.setState({ hosts: hosts as never, sourceHostId: 'cfg-a' } as never)
  beforeEach(() => {
    useFleetStore.getState().reset()
    usePoolStore.setState({ hosts: [], sourceHostId: null } as never)
    resetRememberedPoolHosts()
  })

  it('tab ids go both ways; a tab id that is not a host tab has no host id', () => {
    expect(hostIdOfTabId(watchHostTabId('abc123'))).toBe('abc123')
    expect(hostIdOfTabId('watch-jobs')).toBeNull()
    expect(hostIdOfTabId('t1')).toBeNull()
  })

  it('label: the alias from the pool store, else the one the list remembered, else the short id; follows a rename', () => {
    const id = 'abcdef123456'
    expect(getPaneLabel(pooled(id), sessions, workspaces, t)).toBe('abcdef')
    answer([{ host_id: id, alias: 'mserv' }])
    expect(getPaneLabel(pooled(id), sessions, workspaces, t)).toBe('mserv')
    answer([{ host_id: id, alias: 'mserv-2' }])
    expect(getPaneLabel(pooled(id), sessions, workspaces, t)).toBe('mserv-2')
    // the whole pool cannot be read: the remembered list still names it
    usePoolStore.setState({ hosts: [], sourceHostId: null } as never)
    expect(getPaneLabel(pooled(id), sessions, workspaces, t)).toBe('mserv-2')
  })

  it('label of an old content is still the fleet alias', () => {
    useFleetStore.setState({ snapshot: { hostId: 'p', term: 1, order: ['h1'], hosts: { h1: { id: 'h1', alias: 'mlab' } as never } } })
    answer([{ host_id: 'h1', alias: 'from-pool' }])
    expect(getPaneLabel(host('h1'), sessions, workspaces, t)).toBe('mlab')
  })

  it('contentMatches: the same poolHostId matches, a different one or an old content does not', () => {
    expect(contentMatches(pooled('a'), pooled('a'))).toBe(true)
    expect(contentMatches(pooled('a'), pooled('b'))).toBe(false)
    expect(contentMatches(pooled('a'), host('a'))).toBe(false)
  })
})
