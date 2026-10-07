// spa/src/lib/nex/peer-turn.test.ts — a `peer_message` turn (peer mailbox spec
// §7), driven by the recorded Nexen fixtures (nexen #161): it opens a turn like
// a send the daemon accepted, but it is not this pane's send and never reads as
// the user's own line.
import { describe, it, expect } from 'vitest'
import scoped from './__fixtures__/peer-mailbox/event-peer-message.scoped.json'
import sitewideSse from './__fixtures__/peer-mailbox/event-peer-message.sitewide.sse?raw'
import { applyDurableEvent, defaultExecutionState, frameToEvent, hasOpenTurn, type ExecutionState } from './event-reducer'
import { isPeerMessage } from './message-types'
import { SseParser } from './sse-parser'
import { buildSentHistory } from './sent-history'
import { buildSearchUnits, findMatches, searchUnitId } from './transcript-search'
import { indexOperations } from './operations'
import { groupTurns, isOpeningLine } from './turns'
import type { NexCapabilities, NexEvent } from './types'

/** The scoped event exactly as `/events` serves it (a history page item). */
const peerEvent = (): NexEvent => structuredClone(scoped) as NexEvent

/** The same event as the live per-execution stream frames it: the bare payload under `event: peer_message`. */
const liveFrame = () => ({ id: String(scoped.seq), event: scoped.kind, data: JSON.stringify(scoped.payload) })

/**
 * The site-wide frame, parsed from the recorded SSE bytes. The capture stops
 * after the `data:` line; the blank line that dispatches it is the next byte
 * on the wire, so it is fed separately.
 */
function sitewideFrame() {
  const parser = new SseParser()
  const frames = [...parser.push(sitewideSse), ...parser.push('\n')]
  expect(frames).toHaveLength(1)
  return frames[0]
}

const accepted = (seq: number, text: string, turnId: string): NexEvent =>
  ({ seq, execution_id: scoped.execution_id, kind: 'execution.message_accepted', payload: { text, turn_id: turnId, delivery: 'delivered' }, created_at: 2 })

const EXPECTED_LINE = {
  type: 'purdex_peer',
  from_name: 'mlab/purdex-54',
  text: 'Please reply with the single word PONG.',
  msg_id: 'fixture-0001',
  at: scoped.created_at,
}

describe('peer_message opens a turn (spec §7)', () => {
  it('a history page item opens a turn with its turn_id and yields one peer line', () => {
    const s = applyDurableEvent(defaultExecutionState(), peerEvent())
    expect(s.turnStarts).toEqual([0])
    expect(s.turnEnds).toHaveLength(1)
    expect(s.turnEnds[0].turnId).toBe(scoped.payload.turn_id)
    expect(s.turnMeta[0]).toMatchObject({ startAt: scoped.created_at, endAt: null, outcome: null })
    expect(s.turnLive).toBe(true)
    expect(hasOpenTurn(s)).toBe(true)
    expect(s.summaryStale).toBe(true)
    expect(s.lastSeq).toBe(scoped.seq)
    expect(s.messages).toEqual([EXPECTED_LINE])
  })

  it('never appends the raw payload (no reply_to, principal_id or template_version reaches messages)', () => {
    const s = applyDurableEvent(defaultExecutionState(), peerEvent())
    for (const m of s.messages) {
      expect(m).not.toHaveProperty('principal_id')
      expect(m).not.toHaveProperty('turn_id')
      expect(m).not.toHaveProperty('template_version')
    }
  })

  it('the live frame (bare payload) and the wrapped frame reduce to the same line', () => {
    const bare = frameToEvent(liveFrame())
    expect(bare).not.toBeNull()
    // The subscription hook stamps arrival time on a live frame (created_at 0).
    const live = applyDurableEvent(defaultExecutionState(), { ...bare!, created_at: 1234 })
    expect(live.messages).toEqual([{ ...EXPECTED_LINE, at: 1234 }])
    expect(live.turnEnds[0].turnId).toBe(scoped.payload.turn_id)

    const wrapped = frameToEvent({ id: String(scoped.seq), event: scoped.kind, data: JSON.stringify(scoped) })
    expect(applyDurableEvent(defaultExecutionState(), wrapped!).messages).toEqual([EXPECTED_LINE])
  })

  it('sets turnLive from idle', () => {
    const idle: ExecutionState = { ...defaultExecutionState(), turnLive: false }
    expect(applyDurableEvent(idle, peerEvent()).turnLive).toBe(true)
  })

  it("leaves the pane's own pending send alone (it is not the user's send)", () => {
    const mine: ExecutionState = {
      ...defaultExecutionState(),
      pendingSend: true,
      sendLocked: true,
      pendingLocal: { text: 'my own words', delivery: null },
    }
    const s = applyDurableEvent(mine, peerEvent())
    expect(s.pendingLocal).toEqual({ text: 'my own words', delivery: null })
    expect(s.sendLocked).toBe(true)
    expect(s.pendingSend).toBe(true)
    // The pane's own message_accepted still settles it, as a second turn.
    const after = applyDurableEvent(s, accepted(scoped.seq + 1, 'my own words', 'trn_mine'))
    expect(after.pendingLocal).toBeNull()
    expect(after.sendLocked).toBe(false)
    expect(after.turnStarts).toEqual([0, 1])
  })

  it('a payload without text (the site-wide shape) opens the turn and pushes nothing', () => {
    const ev = frameToEvent(sitewideFrame())
    expect(ev).not.toBeNull()
    expect(ev!.payload).not.toHaveProperty('text')
    const s = applyDurableEvent(defaultExecutionState(), ev!)
    expect(s.messages).toEqual([])
    expect(s.turnStarts).toEqual([0])
    expect(s.turnEnds[0].turnId).toBe(scoped.payload.turn_id)
  })

  it('is applied once (seq guard): a history/live overlap does not duplicate the line or the turn', () => {
    let s = applyDurableEvent(defaultExecutionState(), peerEvent())
    s = applyDurableEvent(s, { ...frameToEvent(liveFrame())!, created_at: 99 })
    expect(s.messages).toHaveLength(1)
    expect(s.turnStarts).toEqual([0])
  })

  it('the peer turn is its own turn with no opening user line', () => {
    let s = applyDurableEvent(defaultExecutionState(), accepted(1, 'hi', 'trn_a'))
    s = applyDurableEvent(s, peerEvent())
    const turns = groupTurns(s.messages, s.turnStarts)
    expect(turns).toHaveLength(2)
    expect(turns[0].openerIndex).toBe(0)
    expect(turns[1]).toMatchObject({ start: 1, end: 2, openerIndex: null })
  })
})

describe('the peer line is never the user\'s own', () => {
  it('isPeerMessage recognises it; isOpeningLine does not', () => {
    const [line] = applyDurableEvent(defaultExecutionState(), peerEvent()).messages
    expect(isPeerMessage(line)).toBe(true)
    expect(isOpeningLine(line)).toBe(false)
  })

  it('sent history (ArrowUp) never offers the peer\'s text', () => {
    let s = applyDurableEvent(defaultExecutionState(), accepted(1, 'my question', 'trn_a'))
    s = applyDurableEvent(s, peerEvent())
    expect(buildSentHistory(s.messages, null).map((e) => e.text)).toEqual(['my question'])
  })
})

describe('search finds the peer text (spec §7)', () => {
  for (const view of ['room', 'chat'] as const) {
    it(`${view}: one match, at the block's own anchor, nothing to unfold`, () => {
      let s = applyDurableEvent(defaultExecutionState(), accepted(1, 'my question', 'trn_a'))
      s = applyDurableEvent(s, peerEvent())
      const units = buildSearchUnits({ messages: s.messages, index: indexOperations(s.messages), view, keyPrefix: 'k', turnStarts: s.turnStarts })
      const { matches } = findMatches(units, 'single word')
      expect(matches).toHaveLength(1)
      expect(matches[0]).toMatchObject({ unitId: searchUnitId('1:0', 'text'), reveal: [] })
      // The sender's name is the header, not the searchable body.
      expect(findMatches(units, 'purdex-54').matches).toEqual([])
    })
  }
})

describe('capabilities.peer_message (spec §7)', () => {
  // nexen v0.20.0 api/peer.go peerCapabilities; the whole key is absent when the mailbox is off.
  it('is typed and optional', () => {
    const on = JSON.parse(JSON.stringify({
      phase: 'P1a',
      peer_message: { enabled: true, route: { method: 'POST', path: '/v1/executions/{id}/peer-messages' }, max_pending: 32, wake_template_version: scoped.payload.template_version },
    })) as Partial<NexCapabilities>
    const maxPending: number | undefined = on.peer_message?.max_pending
    const path: string | undefined = on.peer_message?.route.path
    expect(maxPending).toBe(32)
    expect(path).toBe('/v1/executions/{id}/peer-messages')
    expect(on.peer_message?.wake_template_version).toBe(scoped.payload.template_version)
    const off = { phase: 'P1a' } as Partial<NexCapabilities>
    expect(off.peer_message).toBeUndefined()
  })
})
