// U4-4a: the lifecycle row's words, and the ended classification the deck, the ended screen and the hook share.
import { describe, it, expect } from 'vitest'
import { lifecycleText, workerEndedKind } from './worker-ended'
import type { ExecutionSummary } from './types'

const row = (over: Partial<ExecutionSummary>): ExecutionSummary => ({ id: 'e', state: 'running', archived: false, ...over }) as ExecutionSummary
const t = (key: string, p?: Record<string, string | number>) => (p ? `${key}(${Object.values(p).join(',')})` : key)

describe('workerEndedKind', () => {
  it('terminated or archived is exited; a live failed / rejected row failed to start; anything else runs', () => {
    expect(workerEndedKind(null)).toBeNull()
    expect(workerEndedKind(row({ state: 'terminated' }))).toBe('exited')
    expect(workerEndedKind(row({ state: 'idle', archived: true }))).toBe('exited')
    expect(workerEndedKind(row({ state: 'failed' }))).toBe('failed')
    expect(workerEndedKind(row({ state: 'rejected' }))).toBe('failed')
    for (const state of ['queued', 'running', 'idle']) expect(workerEndedKind(row({ state }))).toBeNull()
  })
})

describe('lifecycleText', () => {
  it('failed to start: the title and the reason (rejection first, else the terminal reason)', () => {
    expect(lifecycleText(row({ state: 'rejected', reject_reason: 'bad profile', terminal_reason: 'x' }), false, t)).toBe('worker.ended.failed_title · worker.ended.reason(bad profile)')
    expect(lifecycleText(row({ state: 'failed', terminal_reason: 'crashed' }), false, t)).toBe('worker.ended.failed_title · worker.ended.reason(crashed)')
    expect(lifecycleText(row({ state: 'failed' }), false, t)).toBe('worker.ended.failed_title')
  })
  it('exited: the title and its reason when it has one', () => {
    expect(lifecycleText(row({ state: 'terminated', terminal_reason: 'idle timeout' }), false, t)).toBe('worker.ended.exited_title · worker.ended.reason(idle timeout)')
    expect(lifecycleText(row({ state: 'terminated' }), false, t)).toBe('worker.ended.exited_title')
  })
  it('queued says so; a running worker with no session yet is starting; otherwise there is no row', () => {
    expect(lifecycleText(row({ state: 'queued' }), false, t)).toBe('executions.activity.queued')
    expect(lifecycleText(row({ state: 'running' }), true, t)).toBe('executions.activity.starting')
    expect(lifecycleText(row({ state: 'running' }), false, t)).toBe('')
    expect(lifecycleText(row({ state: 'idle' }), false, t)).toBe('')
  })
})
