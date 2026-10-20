// spa/src/components/watch/host/ProcList.test.tsx — the 行程 tab's table (monitoring tabs spec M45; plan MON-8a): top 12 by CPU or by memory, a bar relative to the table's largest value, the other reading dimmed, who owns the process.
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, within, cleanup } from '@testing-library/react'
import { ProcList, procRowsOf } from './ProcList'
import { useI18nStore } from '../../../stores/useI18nStore'
import type { PoolProc } from '../../../lib/pool/types'

const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)
const proc = (pid: number, over: Partial<PoolProc> = {}): PoolProc => ({ pid, name: `p${pid}`, cpu_pct: 1, mem_mb: 100, mem_kind: 'footprint', owner: null, ...over })

beforeEach(() => cleanup())

describe('procRowsOf', () => {
  const many = Array.from({ length: 20 }, (_, i) => proc(i + 1, { cpu_pct: i, mem_mb: 1000 - i * 10 }))
  it('takes the top 12, by CPU or by memory, largest first', () => {
    const cpu = procRowsOf(many, 'cpu')
    expect(cpu).toHaveLength(12)
    expect(cpu[0].pid).toBe(20)
    const mem = procRowsOf(many, 'mem')
    expect(mem).toHaveLength(12)
    expect(mem[0].pid).toBe(1)
    expect(mem.map((p) => p.mem_mb)).toEqual([...mem.map((p) => p.mem_mb)].sort((a, b) => b - a))
  })
  it('does not reorder the input', () => {
    const input = [proc(1, { cpu_pct: 1 }), proc(2, { cpu_pct: 9 })]
    procRowsOf(input, 'cpu')
    expect(input.map((p) => p.pid)).toEqual([1, 2])
  })
})

describe('ProcList', () => {
  it('shows pid, name, the main reading with a bar relative to the largest, and the other reading dimmed', () => {
    const rows = [proc(10, { cpu_pct: 80, mem_mb: 2048 }), proc(11, { cpu_pct: 20, mem_mb: 512 })]
    render(<ProcList testId="l" title="T" rows={rows} by="cpu" offline={false} />)
    const list = screen.getByTestId('l')
    expect(within(list).getByText('p10')).toBeInTheDocument()
    expect(screen.getByTestId('l-bar-10')).toHaveStyle({ width: '100%' })
    expect(screen.getByTestId('l-bar-11')).toHaveStyle({ width: '25%' })
    expect(screen.getByTestId('l-main-10')).toHaveTextContent('80%')
    expect(screen.getByTestId('l-sub-10')).toHaveTextContent('2,048 MB')
  })
  it('the memory table reads memory as the main value and CPU as the other', () => {
    render(<ProcList testId="m" title="T" rows={[proc(1, { cpu_pct: 12.34, mem_mb: 1536 })]} by="mem" offline={false} />)
    expect(screen.getByTestId('m-main-1')).toHaveTextContent('1,536 MB')
    expect(screen.getByTestId('m-sub-1')).toHaveTextContent('12.3%')
  })
  it('an RSS reading is marked', () => {
    render(<ProcList testId="m" title="T" rows={[proc(1, { mem_kind: 'rss' })]} by="mem" offline={false} />)
    expect(screen.getByTitle(t('watch.host.mem_rss_hint'))).toBeInTheDocument()
  })
  it('says who owns a process; a session that is the orphan has no owner', () => {
    const rows = [proc(1, { owner: { kind: 'job', id: 'j-9' } }), proc(2, { owner: { kind: 'session', session: 'orphan' } }), proc(3, { owner: { kind: 'session', session: 'ab12' } })]
    render(<ProcList testId="o" title="T" rows={rows} by="cpu" offline={false} />)
    expect(screen.getByTestId('o-owner-1')).toHaveTextContent(`${t('watch.host.owner.job')} j-9`)
    expect(screen.getByTestId('o-owner-2')).toHaveTextContent(t('watch.host.owner.orphan'))
    expect(screen.getByTestId('o-owner-3')).toHaveTextContent(`${t('watch.host.owner.session')} ab12`)
  })
  it('the wire\'s orphan is a resident whose session is "orphan": also no owner (codex attack)', () => {
    render(<ProcList testId="w" title="T" rows={[proc(1, { owner: { kind: 'resident', session: 'orphan' } })]} by="cpu" offline={false} />)
    expect(screen.getByTestId('w-owner-1')).toHaveTextContent(t('watch.host.owner.orphan'))
    expect(screen.getByTestId('w-owner-1')).not.toHaveTextContent(t('watch.host.owner.resident'))
  })
  it('offline: one row that says there is no data', () => {
    render(<ProcList testId="x" title="T" rows={[proc(1)]} by="cpu" offline />)
    expect(screen.getByTestId('x')).toHaveTextContent(t('watch.host.offline_no_data'))
    expect(screen.queryByText('p1')).toBeNull()
  })
  it('no rows: the no-data word', () => {
    render(<ProcList testId="x" title="T" rows={[]} by="cpu" offline={false} />)
    expect(screen.getByTestId('x')).toHaveTextContent(t('fleet.no_data'))
  })
})
