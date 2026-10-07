import { describe, it, expect, afterEach } from 'vitest'
import { clearAllRelayPromptDrafts, forgetRelayPromptDrafts, readRelayPromptDraft, relayPromptDraftKey, writeRelayPromptDraft } from './relay-prompt-draft-memory'

afterEach(() => clearAllRelayPromptDrafts())

describe('forgetRelayPromptDrafts', () => {
  it('drops every draft of that host and only of it, a host id holding ":" included', () => {
    for (const kind of ['write', 'fix', 'seed']) writeRelayPromptDraft(relayPromptDraftKey('a', kind), `a ${kind}`)
    writeRelayPromptDraft(relayPromptDraftKey('a:b', 'write'), 'a:b write')
    writeRelayPromptDraft(relayPromptDraftKey('ab', 'write'), 'ab write')
    forgetRelayPromptDrafts('a')
    for (const kind of ['write', 'fix', 'seed']) expect(readRelayPromptDraft(relayPromptDraftKey('a', kind))).toBeUndefined()
    expect(readRelayPromptDraft(relayPromptDraftKey('a:b', 'write'))).toBe('a:b write')
    expect(readRelayPromptDraft(relayPromptDraftKey('ab', 'write'))).toBe('ab write')
  })
})
