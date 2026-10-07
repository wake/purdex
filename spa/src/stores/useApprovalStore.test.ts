// spa/src/stores/useApprovalStore.test.ts — the open approval requests this app shows (lead-team spec §6.3),
// per host, oldest first, with the decisions clicked while a host was not connected (spec §9.4).
import { beforeEach, describe, expect, it } from 'vitest'
import { approvalKey, selectCurrent, selectNearestDeadline, selectOpenCount, selectOpenCountFor, useApprovalStore } from './useApprovalStore'
import type { Approval } from '../lib/team/types'

const approval = (over: Partial<Approval> = {}): Approval => ({
  id: 'req-1', kind: 'lead', host_id: 'd1',
  origin: { session_id: 'S1', ref: '_40iueq', name: 'purdex-7c', pid: 1, proc_start: 'p', cwd: '/w', tmux: '' },
  payload: { reason: 'r', max_members: 3, roots: ['/w'] },
  state: 'open', created_at: 1_000, deadline_at: 541_000, lease_until: 31_000,
  ...over,
})
const s = () => useApprovalStore.getState()
const ids = () => Object.values(s().entries).map((e) => `${e.hostId}:${e.approval.id}`).sort()

beforeEach(() => s().reset())

describe('useApprovalStore', () => {
  it('keys entries by hostId NUL id, so the same request id on two hosts never collides', () => {
    expect(approvalKey('h1', 'req-1')).toBe('h1\u0000req-1')
    s().applyOpened('h1', approval())
    s().applyOpened('h2', approval())
    expect(ids()).toEqual(['h1:req-1', 'h2:req-1'])
    expect(selectOpenCount(s())).toBe(2)
    expect(selectOpenCountFor('h1')(s())).toBe(1)
  })

  describe('applySnapshot', () => {
    it('replaces that host\'s set only and returns the ids that vanished', () => {
      s().applyOpened('h1', approval({ id: 'a' }))
      s().applyOpened('h1', approval({ id: 'b' }))
      s().applyOpened('h2', approval({ id: 'c' }))
      const vanished = s().applySnapshot('h1', [approval({ id: 'b' }), approval({ id: 'd' })])
      expect(vanished).toEqual(['a'])
      expect(ids()).toEqual(['h1:b', 'h1:d', 'h2:c'])
    })

    it('an empty snapshot clears the host (a request that closed while disconnected disappears)', () => {
      s().applyOpened('h1', approval({ id: 'a' }))
      expect(s().applySnapshot('h1', [])).toEqual(['a'])
      expect(ids()).toEqual([])
    })

    it('does not duplicate a request already held, and keeps only open ones', () => {
      s().applyOpened('h1', approval({ id: 'a' }))
      s().applySnapshot('h1', [approval({ id: 'a' }), approval({ id: 'z', state: 'denied' })])
      expect(ids()).toEqual(['h1:a'])
    })
  })

  describe('applyOpened', () => {
    it('adds once: a second opened for the same request is ignored and reports false', () => {
      expect(s().applyOpened('h1', approval())).toBe(true)
      expect(s().applyOpened('h1', approval({ created_at: 999 }))).toBe(false)
      expect(s().entries[approvalKey('h1', 'req-1')].approval.created_at).toBe(1_000)
    })

    it('ignores a non-open approval', () => {
      expect(s().applyOpened('h1', approval({ state: 'timeout' }))).toBe(false)
      expect(ids()).toEqual([])
    })
  })

  describe('applyClosed', () => {
    it('removes the entry and tells whether the decision was ours, elsewhere, or for an unknown request', () => {
      s().applyOpened('h1', approval({ id: 'a' }))
      s().applyOpened('h1', approval({ id: 'b' }))
      s().markDecidedHere('h1', 'a')
      expect(s().applyClosed('h1', approval({ id: 'a', state: 'approved' }))).toBe('ours')
      expect(s().applyClosed('h1', approval({ id: 'b', state: 'denied' }))).toBe('elsewhere')
      expect(s().applyClosed('h1', approval({ id: 'b', state: 'denied' }))).toBe('absent')
      expect(ids()).toEqual([])
      expect(s().decidedHere).toEqual({})
    })

    it('drops a queued decision for the closed request too', () => {
      s().applyOpened('h1', approval({ id: 'a' }))
      s().queueDecision('h1', approval({ id: 'a' }), 'deny')
      s().applyClosed('h1', approval({ id: 'a', state: 'timeout' }))
      expect(s().takeQueued('h1')).toEqual([])
    })

    it('unmarkDecidedHere undoes the mark (a send that never reached the daemon)', () => {
      s().applyOpened('h1', approval({ id: 'a' }))
      s().markDecidedHere('h1', 'a')
      s().unmarkDecidedHere('h1', 'a')
      expect(s().applyClosed('h1', approval({ id: 'a', state: 'approved' }))).toBe('elsewhere')
    })
  })

  // A `closed` that reaches this app before its `opened` (a socket that connected between the two): the late
  // `opened` must not revive a request the daemon already closed. The snapshot is authoritative and clears the marks.
  describe('a closed arriving before its opened (tombstones)', () => {
    it('applyClosed on an unknown id is `absent` and still tombstones it; the late applyOpened is ignored', () => {
      expect(s().applyClosed('h1', approval({ id: 'a', state: 'denied' }))).toBe('absent')
      expect(s().applyOpened('h1', approval({ id: 'a' }))).toBe(false)
      expect(ids()).toEqual([])
      expect(selectOpenCountFor('h1')(s())).toBe(0)
    })

    it('the snapshot clears the host\'s tombstones: a request the daemon lists as open is shown again', () => {
      s().applyClosed('h1', approval({ id: 'a', state: 'denied' }))
      s().applySnapshot('h1', [approval({ id: 'a' })])
      expect(ids()).toEqual(['h1:a'])
      expect(s().closedIds).toEqual({})
    })

    it('a close of a held request tombstones it too, so a duplicate opened after the close is ignored', () => {
      s().applyOpened('h1', approval({ id: 'a' }))
      s().applyClosed('h1', approval({ id: 'a', state: 'approved' }))
      expect(s().applyOpened('h1', approval({ id: 'a' }))).toBe(false)
      expect(ids()).toEqual([])
    })

    it('tombstones are per host', () => {
      s().applyClosed('h1', approval({ id: 'a', state: 'denied' }))
      expect(s().applyOpened('h2', approval({ id: 'a' }))).toBe(true)
      expect(ids()).toEqual(['h2:a'])
    })

    it('keeps at most 256 ids per host, dropping the oldest', () => {
      for (let i = 0; i < 257; i++) s().applyClosed('h1', approval({ id: `c${i}`, state: 'timeout' }))
      expect(s().closedIds.h1).toHaveLength(256)
      expect(s().applyOpened('h1', approval({ id: 'c0' }))).toBe(true) // evicted: oldest first
      expect(s().applyOpened('h1', approval({ id: 'c1' }))).toBe(false)
      expect(s().applyOpened('h1', approval({ id: 'c256' }))).toBe(false)
    })

    it('reset clears them', () => {
      s().applyClosed('h1', approval({ id: 'a', state: 'denied' }))
      s().reset()
      expect(s().closedIds).toEqual({})
      expect(s().applyOpened('h1', approval({ id: 'a' }))).toBe(true)
    })
  })

  describe('queued decisions', () => {
    it('queueDecision keeps one decision per request (the last click wins); takeQueued removes and returns that host\'s', () => {
      const a = approval({ id: 'a' })
      s().applyOpened('h1', a)
      s().applyOpened('h2', approval({ id: 'c' }))
      s().queueDecision('h1', a, 'approve', { max_members: 2, roots: ['/w'] })
      s().queueDecision('h1', a, 'deny')
      s().queueDecision('h2', approval({ id: 'c' }), 'approve')
      expect(s().takeQueued('h1')).toEqual([{ hostId: 'h1', approval: a, decision: 'deny', grant: undefined }])
      expect(s().takeQueued('h1')).toEqual([])
      expect(s().takeQueued('h2')).toHaveLength(1)
    })
  })

  // U22 (b): minimized is per window (this store is per renderer) and temporary — it ends when nothing is open, so
  // the next request opens the dialog (plan P9 open question 3). Only a click restores it: `applyOpened` never does.
  describe('minimized (U22)', () => {
    const minimize = () => {
      s().setMinimized(true)
      expect(s().minimized).toBe(true)
    }

    it('starts false; setMinimized(true) with no entry stays false', () => {
      expect(s().minimized).toBe(false)
      s().setMinimized(true)
      expect(s().minimized).toBe(false)
    })

    it('setMinimized(false) restores; a new request (applyOpened) never does', () => {
      s().applyOpened('h1', approval({ id: 'a' }))
      minimize()
      expect(s().applyOpened('h2', approval({ id: 'b', kind: 'self_relay', payload: { op_id: 'op-1', used_percentage: 72, window: 1_000_000 } }))).toBe(true)
      expect(s().minimized).toBe(true)
      s().setMinimized(false)
      expect(s().minimized).toBe(false)
    })

    it('minimized resets when applyClosed, applySnapshot or reset leave no entry', () => {
      s().applyOpened('h1', approval({ id: 'a' }))
      s().applyOpened('h1', approval({ id: 'b' }))
      minimize()
      s().applyClosed('h1', approval({ id: 'a', state: 'denied' }))
      expect(s().minimized).toBe(true) // one still open
      s().applyClosed('h1', approval({ id: 'b', state: 'timeout' }))
      expect(s().minimized).toBe(false)

      s().applyOpened('h1', approval({ id: 'c' }))
      minimize()
      s().applySnapshot('h1', [])
      expect(s().minimized).toBe(false)

      s().applyOpened('h1', approval({ id: 'd' }))
      minimize()
      s().reset()
      expect(s().minimized).toBe(false)
    })

    it('a snapshot that still holds entries keeps minimized (the reconnect case)', () => {
      s().applyOpened('h1', approval({ id: 'a' }))
      s().applyOpened('h2', approval({ id: 'b' }))
      minimize()
      s().applySnapshot('h1', [approval({ id: 'a' })])
      expect(s().minimized).toBe(true)
      // The host's own set emptied, another host's request still open: still minimized.
      s().applySnapshot('h1', [])
      expect(s().minimized).toBe(true)
      expect(ids()).toEqual(['h2:b'])
    })

    it('a close of an unknown request while something else is open keeps minimized', () => {
      s().applyOpened('h1', approval({ id: 'a' }))
      minimize()
      expect(s().applyClosed('h1', approval({ id: 'zz', state: 'denied' }))).toBe('absent')
      expect(s().minimized).toBe(true)
    })
  })

  describe('selectNearestDeadline', () => {
    it('is the smallest deadline_at across hosts; null when empty', () => {
      expect(selectNearestDeadline(s())).toBeNull()
      s().applyOpened('h1', approval({ id: 'a', created_at: 1_000, deadline_at: 900_000 }))
      s().applyOpened('h2', approval({ id: 'b', created_at: 2_000, deadline_at: 300_000 }))
      s().applyOpened('h3', approval({ id: 'c', created_at: 3_000, deadline_at: 600_000 }))
      expect(selectNearestDeadline(s())).toBe(300_000)
      s().applyClosed('h2', approval({ id: 'b', state: 'approved' }))
      expect(selectNearestDeadline(s())).toBe(600_000)
    })
  })

  describe('selectCurrent', () => {
    it('is the oldest created_at across hosts, ties broken by id; null when empty', () => {
      expect(selectCurrent(s())).toBeNull()
      s().applyOpened('h2', approval({ id: 'late', created_at: 3_000 }))
      s().applyOpened('h1', approval({ id: 'b', created_at: 2_000 }))
      s().applyOpened('h3', approval({ id: 'a', created_at: 2_000 }))
      expect(selectCurrent(s())).toMatchObject({ hostId: 'h3', approval: { id: 'a' } })
      s().applyClosed('h3', approval({ id: 'a', state: 'denied' }))
      expect(selectCurrent(s())).toMatchObject({ hostId: 'h1', approval: { id: 'b' } })
    })

    it('returns the stored entry object itself, so a zustand selector sees a stable reference', () => {
      s().applyOpened('h1', approval())
      expect(selectCurrent(s())).toBe(s().entries[approvalKey('h1', 'req-1')])
    })
  })
})
