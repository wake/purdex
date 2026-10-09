// spa/src/lib/team/team-names.test.ts — what a team is called on each surface (spec §4.8, P6, P12): the short label on the
// tab bar (cut by display width, never by UTF-16 length, whole grapheme clusters, the "…" inside the 10), the longer name
// in the panel, both in the tooltip.
import { describe, it, expect } from 'vitest'
import { groupLabel, panelName, tooltipOf, cutToWidth, LABEL_MAX_WIDTH } from './team-names'
import { cellWidth } from '../textwidth'
import casesRaw from '../../../../testdata/textwidth/cases.json?raw'
import type { TeamView, Seat } from './team-views'

const seat = (label: string): Seat => ({
  role: 'lead', session: { session_id: 'L', ref: '_L', address: 'a/b', live: true }, state: 'active', origin: null,
  joinedAt: 0, label, hostId: 'h1', hostAlias: '', remote: false, tabId: null, workspaceId: null, paneIndex: null,
})
const view = (over: { name?: string; label?: string; lead?: string } = {}): TeamView => ({
  key: 'h\u0000t', hostId: 'h', teamId: 't', createdAt: 0, colorIndex: 0,
  name: over.name ?? '', label: over.label ?? '', lead: seat(over.lead ?? 'lead title'), members: [],
})

describe('groupLabel', () => {
  it('shows the label whole when the team has one (even a long one)', () => {
    expect(groupLabel(view({ label: 'A線派工會議紀錄很長', lead: 'x' }))).toEqual({ text: 'A線派工會議紀錄很長', full: 'A線派工會議紀錄很長', truncated: false })
  })
  it('falls back to the lead title cut to 10 wide: ASCII 12 → 9 + "…"', () => {
    const r = groupLabel(view({ lead: 'abcdefghijkl' }))
    expect(r).toEqual({ text: 'abcdefghi…', full: 'abcdefghijkl', truncated: true })
    expect(cellWidth(r.text)).toBe(10)
  })
  it('CJK: 6 wide characters (width 12) → 4 + "…" (width 9)', () => {
    const r = groupLabel(view({ lead: '一二三四五六' }))
    expect(r.text).toBe('一二三四…')
    expect(cellWidth(r.text)).toBe(9)
    expect(r.truncated).toBe(true)
  })
  it('exactly 10 wide is shown whole', () => {
    expect(groupLabel(view({ lead: '0123456789' }))).toEqual({ text: '0123456789', full: '0123456789', truncated: false })
    expect(groupLabel(view({ lead: '一二三四五' })).text).toBe('一二三四五')
  })
  it('keeps the "…" inside the 10: the cut text is never wider than 10', () => {
    for (const lead of ['abcdefghijklmnop', '一二三四五六七八', 'ab一二三四五六', '😀😀😀😀😀😀']) {
      expect(cellWidth(groupLabel(view({ lead })).text)).toBeLessThanOrEqual(LABEL_MAX_WIDTH)
    }
  })
  it('never splits an emoji / ZWJ cluster', () => {
    const family = '👨‍👩‍👧‍👦' // one cluster, width 2*4 = 8 (ZWJ weigh 0)
    const r = groupLabel(view({ lead: 'ab' + family + family }))
    for (const part of [...new Intl.Segmenter(undefined, { granularity: 'grapheme' }).segment(r.text)].map((s) => s.segment)) {
      expect([...part].length === 1 || part === family || part === '…').toBe(true)
    }
    expect(r.text.endsWith('…')).toBe(true)
  })
  it('a combining mark stays with its base', () => {
    const lead = 'é'.repeat(12) // 12 clusters of width 1
    const r = groupLabel(view({ lead }))
    expect(r.text).toBe('é'.repeat(9) + '…')
  })
  it('an empty lead title yields an empty label', () => {
    expect(groupLabel(view({ lead: '' }))).toEqual({ text: '', full: '', truncated: false })
  })
})

describe('cutToWidth against the shared fixture', () => {
  const cases: { name: string; s: string; width: number }[] = JSON.parse(casesRaw)
  it('never returns more than the limit, for every fixture string, and never splits a cluster', () => {
    const seg = new Intl.Segmenter(undefined, { granularity: 'grapheme' })
    for (const { name, s } of cases) {
      const r = cutToWidth(s, LABEL_MAX_WIDTH)
      expect(cellWidth(r.text), name).toBeLessThanOrEqual(LABEL_MAX_WIDTH)
      const clusters = [...seg.segment(s)].map((x) => x.segment)
      const kept = [...seg.segment(r.text.replace(/…$/, ''))].map((x) => x.segment)
      expect(clusters.slice(0, kept.length), name).toEqual(kept)
      expect(r.truncated, name).toBe(cellWidth(s) > LABEL_MAX_WIDTH)
    }
  })
})

describe('panelName', () => {
  it('is the name when there is one', () => {
    expect(panelName(view({ name: 'Release train', lead: 'x' }))).toEqual({ text: 'Release train', unnamed: false })
  })
  it('falls back to the lead label, uncut', () => {
    expect(panelName(view({ lead: 'a very long lead title that is not cut' }))).toEqual({ text: 'a very long lead title that is not cut', unnamed: true })
  })
})

describe('tooltipOf', () => {
  it('is "<name> (<label>)" when both exist', () => {
    expect(tooltipOf(view({ name: 'Release train', label: '發版' }))).toBe('Release train (發版)')
  })
  it('is whichever exists alone, else the lead title', () => {
    expect(tooltipOf(view({ name: 'Release train' }))).toBe('Release train')
    expect(tooltipOf(view({ label: '發版' }))).toBe('發版')
    expect(tooltipOf(view({ lead: 'the lead' }))).toBe('the lead')
  })
})
