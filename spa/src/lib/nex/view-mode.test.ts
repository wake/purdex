import { describe, expect, it } from 'vitest'
import type { ExecutionContent, ExecutionViewMode } from '../../types/tab'
import { viewModeOf, withViewMode } from './view-mode'

describe('viewModeOf', () => {
  it('reads an absent mode as room', () => {
    expect(viewModeOf({ kind: 'execution', executionId: 'e1' })).toBe('room')
    expect(viewModeOf({ kind: 'execution', executionId: 'e1', mode: 'chat' })).toBe('chat')
  })

  it('reads an unknown mode as room', () => {
    // an older or newer client may have written a mode this build does not know
    const content = { kind: 'execution', executionId: 'e1', mode: 'terminal' as unknown as ExecutionViewMode } satisfies ExecutionContent
    expect(viewModeOf(content)).toBe('room')
  })
})

describe('withViewMode', () => {
  it('keeps from and host when setting the mode', () => {
    const from = { sessionCode: 'zk16vd', tmuxInstance: 'inst-1', cachedName: 'purdex' }
    const content: ExecutionContent = { kind: 'execution', executionId: 'e1', host: 'h1', from }
    const next = withViewMode(content, 'chat')
    expect(next).toEqual({ kind: 'execution', executionId: 'e1', host: 'h1', from, mode: 'chat' })
    expect(content.mode).toBeUndefined() // the input is not mutated
    expect(withViewMode(next, 'room')).toEqual({ kind: 'execution', executionId: 'e1', host: 'h1', from, mode: 'room' })
  })
})
