// spa/src/lib/nex/worker-agent-status.test.ts — spec §8.1–8.2.
import { describe, it, expect } from 'vitest'
import {
  execAgentCode,
  isExecAgentCode,
  executionIdOfAgentCode,
  providerAgentType,
  projectWorkerStatus,
  type WorkerStatusInput,
  type WorkerProjection,
} from './worker-agent-status'

describe('execAgentCode / isExecAgentCode / executionIdOfAgentCode', () => {
  it('round-trips an execution id through the exec- namespace', () => {
    const code = execAgentCode('exc_abc123')
    expect(code).toBe('exec-exc_abc123')
    expect(code).not.toContain(':')
    expect(isExecAgentCode(code)).toBe(true)
    expect(executionIdOfAgentCode(code)).toBe('exc_abc123')
  })

  it('a tmux session code is not an exec code', () => {
    expect(isExecAgentCode('abc123')).toBe(false)
    expect(executionIdOfAgentCode('abc123')).toBeNull()
    expect(isExecAgentCode('main-1')).toBe(false)
  })

  it('the old colon spelling is not an exec code', () => {
    expect(isExecAgentCode('exec:e1')).toBe(false)
  })
})

describe('providerAgentType', () => {
  it('maps claude → cc, codex → codex, and passes anything else through unchanged', () => {
    expect(providerAgentType('claude')).toBe('cc')
    expect(providerAgentType('codex')).toBe('codex')
    expect(providerAgentType('gemini')).toBe('gemini')
  })
})

describe('projectWorkerStatus', () => {
  const baseInput = (patch: Partial<WorkerStatusInput> = {}): WorkerStatusInput => ({
    state: 'idle',
    turnLive: false,
    lastOutcome: null,
    hasTurn: false,
    archived: false,
    runningSubagents: [],
    ...patch,
  })

  // Table-driven: one row per spec §8.2 line, plus the guard/edge cases the
  // brief calls out explicitly.
  const rows: { name: string; patch: Partial<WorkerStatusInput>; expected: WorkerProjection['status'] }[] = [
    { name: 'rule 1: archived beats a live turn → clear', patch: { archived: true, state: 'running', turnLive: true }, expected: 'clear' },
    { name: 'rule 1: state terminated → clear', patch: { state: 'terminated' }, expected: 'clear' },
    { name: 'rule 2: state rejected → error', patch: { state: 'rejected' }, expected: 'error' },
    { name: 'rule 3: turnLive → running', patch: { turnLive: true }, expected: 'running' },
    { name: 'rule 3: state queued → running', patch: { state: 'queued' }, expected: 'running' },
    { name: 'rule 3: state running → running', patch: { state: 'running' }, expected: 'running' },
    { name: 'rule 4: lastOutcome failed → error', patch: { lastOutcome: 'failed', hasTurn: true }, expected: 'error' },
    { name: 'rule 4: state failed → error', patch: { state: 'failed' }, expected: 'error' },
    { name: 'rule 5: hasTurn + ok outcome → idle', patch: { hasTurn: true, lastOutcome: 'ok' }, expected: 'idle' },
    { name: 'rule 5: hasTurn + interrupted outcome → idle', patch: { hasTurn: true, lastOutcome: 'interrupted' }, expected: 'idle' },
    { name: 'rule 6: no turn yet, not running → idle', patch: { hasTurn: false }, expected: 'idle' },
    { name: 'guard: error then a new turn goes live → running (rule 3 beats rule 4)', patch: { lastOutcome: 'failed', turnLive: true }, expected: 'running' },
    { name: 'guard: error persists without a new turn → still error', patch: { lastOutcome: 'failed', turnLive: false, state: 'idle' }, expected: 'error' },
    { name: 'interrupted turn, no live turn → idle', patch: { hasTurn: true, lastOutcome: 'interrupted', turnLive: false }, expected: 'idle' },
    { name: 'rejected before any turn → error', patch: { state: 'rejected', hasTurn: false }, expected: 'error' },
    { name: 'archived → clear', patch: { archived: true }, expected: 'clear' },
    // Permission channel PC2: a live worker awaiting approval is `waiting` (the tab light), ahead of rule 3.
    { name: 'awaiting approval on a running worker → waiting', patch: { state: 'running', turnLive: true, awaitingApproval: true }, expected: 'waiting' },
    { name: 'awaiting approval after the turn\'s result (background subagent) → waiting', patch: { state: 'running', turnLive: false, awaitingApproval: true }, expected: 'waiting' },
    { name: 'awaiting approval beats a stale failed outcome → waiting', patch: { state: 'running', lastOutcome: 'failed', awaitingApproval: true }, expected: 'waiting' },
    { name: 'awaiting approval on an archived row → clear (rule 1 first)', patch: { archived: true, state: 'running', awaitingApproval: true }, expected: 'clear' },
    { name: 'awaiting approval on a terminated row → clear (rule 1 first)', patch: { state: 'terminated', awaitingApproval: true }, expected: 'clear' },
    { name: 'awaitingApproval false → running, as before', patch: { state: 'running', turnLive: true, awaitingApproval: false }, expected: 'running' },
  ]

  for (const { name, patch, expected } of rows) {
    it(name, () => {
      expect(projectWorkerStatus(baseInput(patch)).status).toBe(expected)
    })
  }

  it('maps runningSubagents to SubagentRef, defaulting type and started_at', () => {
    const result = projectWorkerStatus(
      baseInput({
        state: 'running',
        runningSubagents: [
          { task_id: 't1', subagent_type: 'reviewer', started_at: 1000 },
          { task_id: 't2', started_at: null },
        ],
      }),
    )
    expect(result.subagents).toEqual([
      { id: 't1', type: 'reviewer', started_at: 1000, source_pid: 0, source_start_time: '', is_proxy: false, delegating: false },
      { id: 't2', type: 'subagent', started_at: 0, source_pid: 0, source_start_time: '', is_proxy: false, delegating: false },
    ])
  })

  it('no running subagents → empty array', () => {
    expect(projectWorkerStatus(baseInput()).subagents).toEqual([])
  })
})
