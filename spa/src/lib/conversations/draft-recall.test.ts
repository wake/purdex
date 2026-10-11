// K-2b 2: prependToDraft — text goes in front of the draft (the memories), whether or not an input is mounted; listeners are told.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { prependToDraft, subscribeRecall } from './draft-recall'
import { clearAllDrafts, readDraft, writeDraft } from './draft-memory'
import { clearAllAttachments } from './attachment-memory'
import { clearAllCleared, clearDraft, hasCleared } from './draft-clear'

const K = 'k'
beforeEach(() => { clearAllDrafts(); clearAllAttachments(); clearAllCleared() })

describe('prependToDraft', () => {
  it('an empty draft becomes the text; otherwise text + newline + draft', () => {
    prependToDraft(K, 'a')
    expect(readDraft(K)).toBe('a')
    prependToDraft(K, 'b')
    expect(readDraft(K)).toBe('b\na')
  })

  it('works with no input mounted (the memory holds it for the return) and tells every listener when there is one', () => {
    prependToDraft(K, 'held')
    expect(readDraft(K)).toBe('held')
    const cb = vi.fn()
    const off = subscribeRecall(K, cb)
    prependToDraft(K, 'x')
    expect(cb).toHaveBeenCalledTimes(1)
    off()
    prependToDraft(K, 'y')
    expect(cb).toHaveBeenCalledTimes(1)
    expect(readDraft(K)).toBe('y\nx\nheld')
  })

  it('typing something new ends the Esc undo window', () => {
    writeDraft(K, 'old')
    clearDraft(K)
    expect(hasCleared(K)).toBe(true)
    prependToDraft(K, 'z')
    expect(hasCleared(K)).toBe(false)
  })
})
