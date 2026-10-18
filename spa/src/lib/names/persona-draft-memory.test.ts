import { describe, it, expect, beforeEach } from 'vitest'
import { rememberDraft, recallDraft, forgetDraft, clearDraftMemory } from './persona-draft-memory'
import { makePersona } from './__fixtures__/personas'

describe('persona draft memory', () => {
  beforeEach(() => clearDraftMemory())

  it('remembers one draft and the base it was opened on', () => {
    const base = { hostId: 'h1', revision: 3, stamp: { at: 5, host: 'h1' } }
    rememberDraft({ key: 'hiro', draft: makePersona('hiro', { name: 'Hiro Two' }), base })
    expect(recallDraft()).toEqual({ key: 'hiro', draft: makePersona('hiro', { name: 'Hiro Two' }), base })
  })

  it('hands out a copy, so editing the recalled draft does not change the memory', () => {
    rememberDraft({ key: 'hiro', draft: makePersona('hiro'), base: null })
    const first = recallDraft()!
    first.draft.name = 'Changed'
    expect(recallDraft()!.draft.name).toBe('Hiro')
  })

  it('forgets it', () => {
    rememberDraft({ key: 'hiro', draft: makePersona('hiro'), base: null })
    forgetDraft()
    expect(recallDraft()).toBeNull()
  })
})
