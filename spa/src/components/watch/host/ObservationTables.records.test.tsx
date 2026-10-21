// spa/src/components/watch/host/ObservationTables.records.test.tsx — the 行程 tab's table of observation records (monitoring tabs spec M45; plan MON-8d): the columns, the newest 10 and the expansion, loading older, the words when it cannot be read.
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, within, cleanup, fireEvent } from '@testing-library/react'
import { RecordsTable, lengthOf } from './ObservationTables'
import { clock } from '../../../lib/pool/words'
import { useI18nStore } from '../../../stores/useI18nStore'
import type { ObservationRecords } from '../../../lib/pool/host-observations'
import type { PoolObservation } from '../../../lib/pool/observations-api'

const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)
const NOW = Date.UTC(2026, 9, 20, 4, 0, 0)
const rec = (id: string, over: Partial<PoolObservation> = {}): PoolObservation => ({
  id, host: 'mlab', session: 'ab12', via: 'session', command: `go test ./... # ${id}`, signature: `sig-${id}`, startedAt: NOW - 600_000, endedAt: NOW - 540_000,
  cpuSeconds: 61.5, cpuPeak: 250, memPeakMb: 1536, procsPeak: 7, ...over,
})
const records = (list: PoolObservation[], over: Partial<ObservationRecords> = {}): ObservationRecords => ({ list, nextUntil: null, truncated: false, known: true, at: NOW, ...over })
const many = (n: number) => Array.from({ length: n }, (_, i) => rec(`r${i}`, { startedAt: NOW - i * 1000, endedAt: NOW - i * 1000 + 5000 }))
const mount = (p: Partial<React.ComponentProps<typeof RecordsTable>> = {}) =>
  render(<RecordsTable availability="ok" records={records([rec('a')])} load="ok" olderLoad="idle" stateWord="" now={NOW} expanded={false} onToggle={() => {}} onOlder={() => {}} {...p} />)

beforeEach(() => cleanup())

describe('RecordsTable', () => {
  it('a row a record: start, length, session, signature, command (cut, the whole in the title), CPU seconds, peak cores, memory, processes', () => {
    mount()
    const row = screen.getByTestId('obs-row-a')
    expect(row).toHaveTextContent(clock(NOW - 600_000))
    expect(within(row).getByTestId('obs-length')).toHaveTextContent('1 m 00 s')
    expect(row).toHaveTextContent('ab12')
    expect(row).toHaveTextContent('sig-a')
    expect(within(row).getByTestId('obs-command').getAttribute('title')).toBe('go test ./... # a')
    expect(within(row).getByTestId('obs-command').className).toContain('truncate')
    expect(within(row).getByTestId('obs-cpu')).toHaveTextContent('61.5')
    expect(within(row).getByTestId('obs-cores')).toHaveTextContent('2.5') // cpuPeak / 100
    expect(within(row).getByTestId('obs-mem')).toHaveTextContent('1,536 MB')
    expect(within(row).getByTestId('obs-procs')).toHaveTextContent('7')
  })

  it('one still running is marked in the warning colour, its length counted to now; a codex one is marked; the orphan has no owner', () => {
    mount({ records: records([rec('live', { endedAt: null, startedAt: NOW - 90_000, via: 'codex', session: 'orphan' })]) })
    const row = screen.getByTestId('obs-row-live')
    expect(within(row).getByTestId('obs-length').className).toContain('text-status-warning-text')
    expect(within(row).getByTestId('obs-length')).toHaveTextContent(`1 m 30 s ${t('watch.host.obs.running')}`)
    expect(row).toHaveTextContent(t('watch.host.obs.via_codex'))
    expect(row).toHaveTextContent(t('watch.host.owner.orphan'))
    cleanup()
    mount()
    expect(within(screen.getByTestId('obs-row-a')).getByTestId('obs-length').className).not.toContain('text-status-warning-text')
  })

  it('shows the newest 10; "show all N" draws every one and "newest 10 only" goes back', () => {
    const onToggle = vi.fn()
    const { rerender } = mount({ records: records(many(14)), onToggle })
    expect(screen.getAllByTestId(/^obs-row-/)).toHaveLength(10)
    expect(screen.getByTestId('obs-toggle')).toHaveTextContent(t('watch.host.obs.show_all', { n: 14 }))
    fireEvent.click(screen.getByTestId('obs-toggle'))
    expect(onToggle).toHaveBeenCalledTimes(1)
    rerender(<RecordsTable availability="ok" records={records(many(14))} load="ok" olderLoad="idle" stateWord="" now={NOW} expanded onToggle={onToggle} onOlder={() => {}} />)
    expect(screen.getAllByTestId(/^obs-row-/)).toHaveLength(14)
    expect(screen.getByTestId('obs-toggle')).toHaveTextContent(t('watch.host.obs.show_newest', { n: 10 }))
  })

  it('10 or fewer: no toggle', () => {
    mount({ records: records(many(10)) })
    expect(screen.queryByTestId('obs-toggle')).toBeNull()
  })

  it('a cursor offers "load older"; loading says so and cannot be pressed twice; a failure says so', () => {
    const onOlder = vi.fn()
    const { rerender } = mount({ records: records(many(3), { nextUntil: 'c' }), onOlder })
    fireEvent.click(screen.getByTestId('obs-older'))
    expect(onOlder).toHaveBeenCalledTimes(1)
    rerender(<RecordsTable availability="ok" records={records(many(3), { nextUntil: 'c' })} load="ok" olderLoad="loading" stateWord="" now={NOW} expanded={false} onToggle={() => {}} onOlder={onOlder} />)
    expect(screen.getByTestId('obs-older')).toBeDisabled()
    expect(screen.getByTestId('obs-older')).toHaveTextContent(t('watch.host.obs.loading_older'))
    rerender(<RecordsTable availability="ok" records={records(many(3), { nextUntil: 'c' })} load="ok" olderLoad="error" stateWord="" now={NOW} expanded={false} onToggle={() => {}} onOlder={onOlder} />)
    expect(screen.getByTestId('obs-older-error')).toHaveTextContent(t('watch.host.obs.older_error'))
  })

  it('no cursor: no "load older"', () => {
    mount()
    expect(screen.queryByTestId('obs-older')).toBeNull()
  })

  it('no records: nothing in 24 hours', () => {
    mount({ records: records([]) })
    expect(screen.getByTestId('obs-records')).toHaveTextContent(t('watch.host.obs.records_none'))
  })

  it('says why it cannot be read', () => {
    const cases: Array<[Partial<React.ComponentProps<typeof RecordsTable>>, string]> = [
      [{ load: 'forbidden', records: null }, t('watch.host.obs.forbidden')],
      [{ records: records([], { known: false }) }, t('watch.host.obs.too_old')],
      [{ availability: 'no_route', records: null, load: 'idle' }, t('watch.host.obs.no_route')],
      [{ availability: 'host_down', records: null, load: 'idle', stateWord: 'Host is offline' }, t('watch.host.obs.host_down', { state: 'Host is offline' })],
      [{ load: 'error', records: null }, t('watch.host.obs.error')],
    ]
    for (const [over, text] of cases) {
      cleanup()
      mount(over)
      expect(screen.getByTestId('obs-note')).toHaveTextContent(text)
      expect(screen.queryByTestId('obs-row-a')).toBeNull()
    }
  })

  it('a failed refresh keeps the records and says so', () => {
    mount({ load: 'error' })
    expect(screen.getByTestId('obs-row-a')).toBeInTheDocument()
    expect(screen.getByTestId('obs-note')).toHaveTextContent(t('watch.host.obs.error'))
  })
})

describe('lengthOf', () => {
  it('to the second under a minute, minute and second under an hour, hour and minute above', () => {
    expect(lengthOf(7_000)).toBe('7 s')
    expect(lengthOf(65_000)).toBe('1 m 05 s')
    expect(lengthOf(3_780_000)).toBe('1 h 03 m')
    expect(lengthOf(-5)).toBe('0 s')
  })
})
