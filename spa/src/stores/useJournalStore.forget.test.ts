// spa/src/stores/useJournalStore.forget.test.ts — forgetting a host takes its view memos with it (J-2a, codex R2).
import { describe, it, expect, beforeEach } from 'vitest'
import { useJournalStore } from './useJournalStore'
import { patchViewMemo, readViewMemo, clearViewMemos } from '../lib/journal/view-memory'

beforeEach(() => { useJournalStore.getState().reset(); clearViewMemos() })

describe('forgetHost', () => {
  it("drops that host's view memos and leaves other hosts alone", () => {
    patchViewMemo('h1', 'team:t1', { detailId: 'e1' })
    patchViewMemo('h2', 'team:t1', { detailId: 'e2' })
    useJournalStore.getState().forgetHost('h1')
    expect(readViewMemo('h1', 'team:t1').detailId).toBeNull()
    expect(readViewMemo('h2', 'team:t1').detailId).toBe('e2')
  })
})
