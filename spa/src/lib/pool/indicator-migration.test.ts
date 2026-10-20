// spa/src/lib/pool/indicator-migration.test.ts — the one-time copy of the old global 「主機格指示」 into each host's own indicator (MON-9a-3, spec W7, M51): the control is gone from 設定 → 終端機, so the value the reader chose must
// already be written in every host before the control disappears.
import { describe, it, expect, beforeEach } from 'vitest'
import { migrateGlobalIndicator } from './indicator-migration'
import { usePoolStore } from './store'
import { usePoolHostsStore } from '../../stores/usePoolHostsStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { STORAGE_KEYS } from '../storage'
import { indicatorOf } from './host-looks'

const MLAB = '82b11c521df16c12'
const AIR = '0123456789abcdef'
const MSERV = 'aaaaaaaaaaaaaaaa'
const MARKER = STORAGE_KEYS.POOL_INDICATOR_MIGRATED
const BARS_CPU_MEM = { shape: 'bars', data: ['cpu', 'mem'] }
const poolHost = (alias: string, host_id: string) => ({ alias, host_id }) as never
const answerPool = (ids: string[] = [MLAB, AIR, MSERV]) => usePoolStore.setState({ hosts: ids.map((id, i) => poolHost(`h${i}`, id)), sourceHostId: 'cfg-a' } as never)
const indicators = () => Object.fromEntries(Object.entries(usePoolHostsStore.getState().looks).map(([k, v]) => [k, v.indicator]))

beforeEach(() => {
  localStorage.removeItem(MARKER)
  usePoolStore.setState({ hosts: [], sourceHostId: null } as never)
  usePoolHostsStore.setState({ order: [], looks: {} })
  useUISettingsStore.setState({ fleetCellIndicator: 'ring' })
})

describe('migrateGlobalIndicator — the hosts are known at boot', () => {
  it('the global value is bars-memory and no host has set one: every host gets the double bar of CPU and memory', () => {
    useUISettingsStore.setState({ fleetCellIndicator: 'bars-memory' })
    answerPool()
    migrateGlobalIndicator()
    expect(indicators()).toEqual({ [MLAB]: BARS_CPU_MEM, [AIR]: BARS_CPU_MEM, [MSERV]: BARS_CPU_MEM })
    expect(localStorage.getItem(MARKER)).not.toBeNull()
  })

  it('bars-pressure gives CPU and memory pressure', () => {
    useUISettingsStore.setState({ fleetCellIndicator: 'bars-pressure' })
    answerPool([MLAB])
    migrateGlobalIndicator()
    expect(indicators()[MLAB]).toEqual({ shape: 'bars', data: ['cpu', 'pressure'] })
  })

  it('a host that already has its own indicator is not touched', () => {
    useUISettingsStore.setState({ fleetCellIndicator: 'bars-memory' })
    usePoolHostsStore.setState({ order: [], looks: { [MSERV]: { indicator: { shape: 'ring', data: ['disk'] } } } })
    answerPool()
    migrateGlobalIndicator()
    expect(indicators()[MSERV]).toEqual({ shape: 'ring', data: ['disk'] })
    expect(indicators()[MLAB]).toEqual(BARS_CPU_MEM)
  })

  it('the global value is the default ring: nothing is written (a ring of the overall load is what a host without an indicator shows), and the marker is written', () => {
    answerPool()
    migrateGlobalIndicator()
    expect(usePoolHostsStore.getState().looks).toEqual({})
    expect(localStorage.getItem(MARKER)).not.toBeNull()
  })

  it('a host that is in the order or the looks but not in the pool answer is migrated too', () => {
    useUISettingsStore.setState({ fleetCellIndicator: 'bars-memory' })
    usePoolHostsStore.setState({ order: [AIR], looks: { [MSERV]: { icon: 'Cube' } } })
    answerPool([MLAB])
    migrateGlobalIndicator()
    expect(Object.keys(indicators()).sort()).toEqual([MLAB, AIR, MSERV].sort())
    expect(usePoolHostsStore.getState().looks[MSERV]).toMatchObject({ icon: 'Cube', indicator: BARS_CPU_MEM })
  })
})

describe('migrateGlobalIndicator — the marker', () => {
  it('once done, the second run writes nothing, whatever the global value is now', () => {
    useUISettingsStore.setState({ fleetCellIndicator: 'bars-memory' })
    answerPool()
    migrateGlobalIndicator()
    usePoolHostsStore.setState({ order: [], looks: {} })
    migrateGlobalIndicator()
    expect(usePoolHostsStore.getState().looks).toEqual({})
  })

  it('a marker that does not read counts as not done: only what is missing is filled in, so running again is harmless', () => {
    localStorage.setItem(MARKER, '{broken')
    useUISettingsStore.setState({ fleetCellIndicator: 'bars-memory' })
    usePoolHostsStore.setState({ order: [], looks: { [MLAB]: { indicator: { shape: 'bar', data: ['mem'] } } } })
    answerPool([MLAB, AIR])
    migrateGlobalIndicator()
    expect(indicators()).toEqual({ [MLAB]: { shape: 'bar', data: ['mem'] }, [AIR]: BARS_CPU_MEM })
  })
})

describe('migrateGlobalIndicator — the hosts are not known yet', () => {
  it('nothing is written and no marker is set while there is not one host to write to', () => {
    useUISettingsStore.setState({ fleetCellIndicator: 'bars-memory' })
    migrateGlobalIndicator()
    expect(usePoolHostsStore.getState().looks).toEqual({})
    expect(localStorage.getItem(MARKER)).toBeNull()
  })

  it('the pool answers later in the same run: the migration is done then and the marker is written; a later change does not run it again', () => {
    useUISettingsStore.setState({ fleetCellIndicator: 'bars-memory' })
    migrateGlobalIndicator()
    answerPool([MLAB, AIR])
    expect(indicators()).toEqual({ [MLAB]: BARS_CPU_MEM, [AIR]: BARS_CPU_MEM })
    expect(localStorage.getItem(MARKER)).not.toBeNull()
    usePoolHostsStore.setState({ looks: {} })
    answerPool([MLAB, AIR, MSERV])
    expect(usePoolHostsStore.getState().looks).toEqual({}) // the watch is gone
  })

  it('a look that arrives first (from a sync) is the first known host: its own indicator is kept, and the hosts the pool lists afterwards still show the global value through the default', () => {
    useUISettingsStore.setState({ fleetCellIndicator: 'bars-memory' })
    migrateGlobalIndicator()
    usePoolHostsStore.setState({ order: [], looks: { [AIR]: { indicator: { shape: 'ring', data: ['cpu'] } } } })
    expect(indicators()).toEqual({ [AIR]: { shape: 'ring', data: ['cpu'] } })
    expect(localStorage.getItem(MARKER)).not.toBeNull()
  })

  it('the global value is read when the migration runs, not when it was asked for', () => {
    useUISettingsStore.setState({ fleetCellIndicator: 'bars-memory' })
    migrateGlobalIndicator()
    useUISettingsStore.setState({ fleetCellIndicator: 'bars-pressure' })
    answerPool([MLAB])
    expect(indicators()[MLAB]).toEqual({ shape: 'bars', data: ['cpu', 'pressure'] })
  })

  it('the global value is back to ring by the time the hosts arrive: nothing is written, the marker is', () => {
    useUISettingsStore.setState({ fleetCellIndicator: 'bars-memory' })
    migrateGlobalIndicator()
    useUISettingsStore.setState({ fleetCellIndicator: 'ring' })
    answerPool([MLAB])
    expect(usePoolHostsStore.getState().looks).toEqual({})
    expect(localStorage.getItem(MARKER)).not.toBeNull()
  })
})

describe('after the control is gone', () => {
  it('a reader who had chosen the double bar still sees it on the sidebar: the look the sidebar resolves has the indicator', () => {
    useUISettingsStore.setState({ fleetCellIndicator: 'bars-memory' })
    answerPool([MLAB])
    migrateGlobalIndicator()
    useUISettingsStore.setState({ fleetCellIndicator: 'ring' }) // the control that changed it is gone; whatever the old value does now, the host has its own
    expect(indicatorOf(usePoolHostsStore.getState().looks[MLAB], useUISettingsStore.getState().fleetCellIndicator)).toEqual(BARS_CPU_MEM)
  })
})
