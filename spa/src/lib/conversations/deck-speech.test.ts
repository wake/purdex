// spa/src/lib/conversations/deck-speech.test.ts — which agent_text items of a turn are SPEECH STARTS (name pool v2 spec §9.4, U15): the item before it in the turn is not agent_text. A thinking row counts as a break
// (the agent spoke, thought, spoke again: two starts); a system row does not (it is the stream's own line, not the agent's, so text on either side of it is one run). A tool step breaks too.
import { describe, it, expect } from 'vitest'
import { speechStartIds } from './deck-speech'
import type { ConversationItem } from './types'

const it_ = (type: string, id: string) => ({ type, id, at: 1, markdown: 'x', text: 'x' }) as unknown as ConversationItem
const starts = (...kinds: string[]) => [...speechStartIds(kinds.map((k, i) => it_(k, `${k}${i}`)))]

describe('speechStartIds', () => {
  it('the first agent text of a turn is a start', () => {
    expect(starts('agent_text')).toEqual(['agent_text0'])
    expect(starts('user', 'agent_text')).toEqual(['agent_text1'])
  })

  it('agent text right after agent text is the same run: only the first is a start', () => {
    expect(starts('agent_text', 'agent_text', 'agent_text')).toEqual(['agent_text0'])
  })

  it('a tool step or a thinking row between two texts makes the second a start', () => {
    expect(starts('agent_text', 'step', 'agent_text')).toEqual(['agent_text0', 'agent_text2'])
    expect(starts('agent_text', 'thinking', 'agent_text')).toEqual(['agent_text0', 'agent_text2'])
  })

  it('a system row does not break a run, and is not itself a start', () => {
    expect(starts('agent_text', 'system', 'agent_text')).toEqual(['agent_text0'])
    expect(starts('system', 'agent_text')).toEqual(['agent_text1'])
  })

  it('a user message breaks it', () => {
    expect(starts('agent_text', 'user', 'agent_text')).toEqual(['agent_text0', 'agent_text2'])
  })

  it('an item this build does not know draws nothing and is skipped like a system row', () => {
    expect(starts('agent_text', 'something_new', 'agent_text')).toEqual(['agent_text0'])
  })

  it('a turn with no agent text has no start', () => {
    expect(starts('user', 'step', 'thinking')).toEqual([])
  })

  it('a turn that left its first items out cannot say what came before the first one in view: a text there is not claimed as a start', () => {
    const items = ['agent_text', 'step', 'agent_text'].map((k, i) => it_(k, `${k}${i}`))
    expect([...speechStartIds(items, { afterOmitted: true })]).toEqual(['agent_text2'])
    expect([...speechStartIds(items)]).toEqual(['agent_text0', 'agent_text2'])
  })

  it('left-out items before something that is not text change nothing: the first text after a tool is still a start', () => {
    const items = ['step', 'agent_text'].map((k, i) => it_(k, `${k}${i}`))
    expect([...speechStartIds(items, { afterOmitted: true })]).toEqual(['agent_text1'])
  })
})
