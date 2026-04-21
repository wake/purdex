import { describe, it, expect } from 'vitest'
import { AGENT_NAMES } from './agent-metadata'

describe('agent-metadata', () => {
  it('includes opencode display name', () => {
    expect(AGENT_NAMES.opencode).toBe('OpenCode')
  })
})
