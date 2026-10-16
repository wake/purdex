import { describe, it, expect } from 'vitest'
import { ROADMAP_STATUS_IDS, ROADMAP_STATUS_STYLE } from './roadmap-status'
import { ROADMAP_STATUSES } from './types'

describe('the roadmap status styles', () => {
  it('maps each status to its theme token: 未開始 grey, 進行中 accent, 審查中 secondary, 完成 success, 暫停 warning', () => {
    expect(ROADMAP_STATUS_STYLE['未開始'].text).toBe('text-text-muted')
    expect(ROADMAP_STATUS_STYLE['進行中'].text).toBe('text-accent')
    expect(ROADMAP_STATUS_STYLE['審查中'].text).toBe('text-text-secondary')
    expect(ROADMAP_STATUS_STYLE['完成'].text).toBe('text-status-success-text')
    expect(ROADMAP_STATUS_STYLE['暫停'].text).toBe('text-status-warning-text')
  })
  it('covers every status the wire can carry, each with its own label id (the colour is never the only cue)', () => {
    expect(Object.keys(ROADMAP_STATUS_STYLE).sort()).toEqual([...ROADMAP_STATUSES].sort())
    expect(new Set(ROADMAP_STATUS_IDS).size).toBe(ROADMAP_STATUSES.length)
  })
  it('writes no colour code, only theme tokens', () => {
    for (const s of Object.values(ROADMAP_STATUS_STYLE)) {
      expect(s.text).toMatch(/^text-[a-z-]+$/)
      expect(s.dot).toMatch(/^bg-[a-z-]+$/)
    }
  })
})
