import { describe, it, expect, vi, beforeEach } from 'vitest'
import { answerAsk, buildAnswers, checkReply, openAsks, parseAsk, replyToAsk, terminalAnswerText, REPLY_MAX_RUNES } from './asks'
import { applyApprovals, applyApprovalOp, emptyDoc } from './model'
import type { ConversationApproval } from './types'

const decideApproval = vi.hoisted(() => vi.fn())
vi.mock('../team/approval-api', () => ({ decideApproval }))
vi.mock('../team/client-label', () => ({ clientDescriptor: async () => ({ kind: 'app', label: 'Mac' }) }))

const ask = (id: string, over: Record<string, unknown> = {}, payload: Record<string, unknown> = {}): ConversationApproval => ({
  id, kind: 'hook_ask', state: 'open',
  payload: {
    tool_use_id: `toolu_${id}`,
    questions: [
      { question: '先做哪個方案？', header: '方案', multiSelect: false, options: [{ label: '甲案', description: '先做甲' }, { label: '乙案' }] },
      { question: '要哪些顏色？', multiSelect: true, options: [{ label: '紅' }, { label: '綠' }] },
    ],
    ...payload,
  },
  ...over,
})

beforeEach(() => { decideApproval.mockReset() })

describe('parseAsk / openAsks', () => {
  it('reads an open hook_ask into a card bound to its id', () => {
    const a = parseAsk(ask('a1'))!
    expect(a.id).toBe('a1')
    expect(a.toolUseId).toBe('toolu_a1')
    expect(a.terminalOnly).toBe(false)
    expect(a.questions).toHaveLength(2)
    expect(a.questions[0]).toMatchObject({ question: '先做哪個方案？', header: '方案', multiple: false })
    expect(a.questions[0].options[0]).toEqual({ label: '甲案', description: '先做甲' })
    expect(a.questions[1].multiple).toBe(true)
  })
  it('knows a terminal_only card', () => {
    expect(parseAsk(ask('a2', {}, { terminal_only: true }))!.terminalOnly).toBe(true)
  })
  it('is not a card unless it is an open hook_ask with a readable question', () => {
    expect(parseAsk(ask('p', { kind: 'hook_permission' }))).toBeNull()
    expect(parseAsk(ask('l', { kind: 'lead' }))).toBeNull()
    expect(parseAsk(ask('c', { state: 'approved' }))).toBeNull()
    expect(parseAsk({ id: 'x', kind: 'hook_ask', payload: { questions: [] } })).toBeNull()
    expect(parseAsk({ id: 'x', kind: 'hook_ask', payload: { questions: 'nope' } })).toBeNull()
    expect(parseAsk({ id: 'x', kind: 'hook_ask' })).toBeNull()
  })
  it('skips wrong-typed questions and options instead of throwing', () => {
    const a = parseAsk({ id: 'x', kind: 'hook_ask', payload: { questions: [null, 5, { question: 'q?', options: [null, { label: 3 }, { label: 'ok', description: 9 }] }] } })!
    expect(a.questions).toHaveLength(1)
    expect(a.questions[0].options).toEqual([{ label: 'ok' }])
  })
  it('lists the cards in order and ignores other kinds', () => {
    expect(openAsks([ask('1'), ask('2', { kind: 'adopt' }), ask('3')]).map((a) => a.id)).toEqual(['1', '3'])
  })
  it('follows the conversation document: a snapshot replaces the set, a closed op removes one', () => {
    let doc = applyApprovals(emptyDoc(), [ask('1'), ask('2')])
    expect(openAsks(doc.approvals).map((a) => a.id)).toEqual(['1', '2'])
    doc = applyApprovalOp(doc, 'closed', ask('1'))
    expect(openAsks(doc.approvals).map((a) => a.id)).toEqual(['2'])
    doc = applyApprovals(doc, [ask('9')])
    expect(openAsks(doc.approvals).map((a) => a.id)).toEqual(['9'])
  })
})

describe('buildAnswers', () => {
  const qs = parseAsk(ask('a1'))!.questions
  it('maps question text to the answer; a multi-select joins with a comma', () => {
    expect(buildAnswers(qs, [{ chosen: ['甲案'], other: '' }, { chosen: ['紅', '綠'], other: '' }])).toEqual({ '先做哪個方案？': '甲案', '要哪些顏色？': '紅, 綠' })
  })
  it('the 其他 text answers a single-select and joins a multi-select', () => {
    expect(buildAnswers(qs, [{ chosen: [], other: ' 兩個都不要 ' }, { chosen: ['紅'], other: '紫' }])).toEqual({ '先做哪個方案？': '兩個都不要', '要哪些顏色？': '紅, 紫' })
  })
  it('the free text wins over a contradicting pick of a single-select', () => {
    expect(buildAnswers(qs, [{ chosen: ['甲案'], other: '丙' }, { chosen: ['紅'], other: '' }])!['先做哪個方案？']).toBe('丙')
  })
  it('is null while any question has no answer, and ignores a pick that is not an option', () => {
    expect(buildAnswers(qs, [{ chosen: ['甲案'], other: '' }, { chosen: [], other: '  ' }])).toBeNull()
    expect(buildAnswers(qs, [{ chosen: ['丁案'], other: '' }, { chosen: ['紅'], other: '' }])).toBeNull()
    expect(buildAnswers(qs, [])).toBeNull()
  })
})

describe('checkReply (mirrors the daemon: ask-chat spec §2.1)', () => {
  it('trims and keeps new lines and tabs', () => {
    expect(checkReply('  好\n\t而且  ')).toEqual({ ok: true, text: '好\n\t而且' })
  })
  it('refuses empty and over-long', () => {
    expect(checkReply('  \n ')).toEqual({ ok: false, reason: 'empty' })
    expect(checkReply('字'.repeat(REPLY_MAX_RUNES))).toMatchObject({ ok: true })
    expect(checkReply('字'.repeat(REPLY_MAX_RUNES + 1))).toEqual({ ok: false, reason: 'too_long' })
  })
  it('counts runes, not UTF-16 units', () => {
    expect(checkReply('😀'.repeat(REPLY_MAX_RUNES))).toMatchObject({ ok: true })
  })
  it.each([['carriage return', 'a\rb'], ['NUL', 'a\u0000b'], ['bidi override', 'a‮b'], ['bidi isolate', 'a⁦b'], ['line separator', 'a b'],
    ['zero-width space', 'a​b'], ['BOM', 'a﻿b'], ['DEL', 'a\u007Fb']])('refuses %s', (_n, text) => {
    expect(checkReply(text)).toEqual({ ok: false, reason: 'bad_characters' })
  })
  it('allows ZWJ and ZWNJ (emoji, Persian)', () => {
    expect(checkReply('👨‍👩')).toMatchObject({ ok: true })
    expect(checkReply('می‌خواهم')).toMatchObject({ ok: true })
  })
})

describe('deciding', () => {
  it('answers with approve + hook.answers and the app client', async () => {
    decideApproval.mockResolvedValue({})
    expect(await answerAsk('h', 'a1', { q: 'x' })).toEqual({ ok: true })
    expect(decideApproval).toHaveBeenCalledWith('h', 'a1', { decision: 'approve', hook: { answers: { q: 'x' } }, client: { kind: 'app', label: 'Mac' } })
  })
  it('replies with deny + hook.message and never both', async () => {
    decideApproval.mockResolvedValue({})
    await replyToAsk('h', 'a1', '先別動')
    const body = decideApproval.mock.calls[0][2]
    expect(body).toMatchObject({ decision: 'deny', hook: { message: '先別動' } })
    expect(body.hook.answers).toBeUndefined()
  })
  it.each([
    ['already_decided', 'changed'], ['not_found', 'changed'], ['terminal_only', 'terminal_only'], ['network', 'network'], ['storage_error', 'failed'],
  ])('maps the error %s to %s', async (code, reason) => {
    decideApproval.mockImplementation(async () => { throw Object.assign(new Error(code), { code }) })
    expect(await answerAsk('h', 'a1', { q: 'x' })).toEqual({ ok: false, reason })
  })
  it('never retries on its own', async () => {
    decideApproval.mockImplementation(async () => { throw Object.assign(new Error('n'), { code: 'network' }) })
    await answerAsk('h', 'a1', { q: 'x' })
    expect(decideApproval).toHaveBeenCalledTimes(1)
  })
})

describe('terminalAnswerText', () => {
  const step = { type: 'step', id: 'toolu_a1', question: { answers: [['甲案'], ['紅', '綠']] } }
  it('reads what the terminal answered from the question step bound to the ask', () => {
    expect(terminalAnswerText([step], 'toolu_a1')).toBe('甲案 / 紅, 綠')
  })
  it('is null without a tool_use_id, a step, or answers', () => {
    expect(terminalAnswerText([step], '')).toBeNull()
    expect(terminalAnswerText([step], 'toolu_other')).toBeNull()
    expect(terminalAnswerText([{ type: 'step', id: 'toolu_a1', question: {} }], 'toolu_a1')).toBeNull()
    expect(terminalAnswerText([{ type: 'user', id: 'toolu_a1' }], 'toolu_a1')).toBeNull()
  })
})
