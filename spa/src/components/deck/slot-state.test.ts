import { describe, it, expect } from 'vitest'
import type { ConversationItem } from '../../lib/conversations/types'
import { slotModel } from './slot-state'

const step = (over: object = {}): ConversationItem => ({
  type: 'step', id: 's', at: 5, index: 0, kind: 'execute', tool: 'Bash', status: 'done', summary: 'x', started_at: 1000, input: null, ...over,
}) as ConversationItem
const agent = { type: 'agent_text', id: 'a', at: 9, index: 1, markdown: 'ok' } as ConversationItem

describe('slotModel', () => {
  it('idle with nothing, with an idle header, and after the end', () => {
    expect(slotModel('idle', [])).toEqual({ state: 'idle' })
    for (const s of ['idle', 'ended', 'unknown']) expect(slotModel(s, [step()]).state).toBe('idle')
  })

  it('running counts from the running step, else from the newest item', () => {
    expect(slotModel('running', [step({ status: 'done', started_at: 10 }), step({ id: 'r', status: 'running', started_at: 2000 })])).toEqual({ state: 'running', runningSince: 2000 })
    expect(slotModel('running', [step({ started_at: 777 })])).toEqual({ state: 'running', runningSince: 777 })
    expect(slotModel('running', [agent])).toEqual({ state: 'running', runningSince: 9 })
    expect(slotModel('running', [])).toEqual({ state: 'running', runningSince: undefined })
  })

  it('a denied newest step reads denied; a failed one reads exit N when it has a command exit code, else failed', () => {
    expect(slotModel('idle', [step({ status: 'denied' })]).state).toBe('denied')
    expect(slotModel('idle', [step({ status: 'failed', command: { text: 'false', exit_code: 2 } })])).toEqual({ state: 'exit', exitCode: 2 })
    expect(slotModel('idle', [step({ status: 'failed' })])).toEqual({ state: 'failed' })
    expect(slotModel('idle', [step({ status: 'failed', command: { text: 'x', exit_code: 0 } })])).toEqual({ state: 'failed' })
  })

  it('only the NEWEST item decides: a failure the agent has since spoken after is history', () => {
    expect(slotModel('idle', [step({ status: 'failed' }), agent]).state).toBe('idle')
  })

  it('an error header says failed when no newer step says more', () => {
    expect(slotModel('error', [agent]).state).toBe('failed')
    expect(slotModel('error', [step({ status: 'denied' })]).state).toBe('denied')
  })

  it('waiting for the person is its own state and outranks running and any step result', () => {
    expect(slotModel('waiting', [])).toEqual({ state: 'waiting' })
    expect(slotModel('waiting', [step({ status: 'running' })]).state).toBe('waiting')
    expect(slotModel('waiting', [step({ status: 'failed', command: { text: 'x', exit_code: 2 } })]).state).toBe('waiting')
    expect(slotModel('waiting', [step({ status: 'denied' })]).state).toBe('waiting')
  })

  it('running outranks an old failure', () => {
    expect(slotModel('running', [step({ status: 'failed' })]).state).toBe('running')
  })
})
