// spa/src/lib/team/label.test.ts — the label rules against the daemon's: derive reads testdata/teamlabel/derive.json,
// the same file `internal/team` reads.
import { describe, it, expect } from 'vitest'
import deriveRaw from '../../../../testdata/teamlabel/derive.json?raw'
import { deriveTeamLabel, labelProblem, goTrim, TEAM_LABEL_MAX_WIDTH } from './label'

const derive: { input: string; label: string }[] = JSON.parse(deriveRaw)

describe('deriveTeamLabel (the daemon fixture)', () => {
  it.each(derive)('$input -> $label', ({ input, label }) => {
    expect(deriveTeamLabel(input)).toBe(label)
  })
})

describe('labelProblem', () => {
  it('takes what the daemon takes', () => {
    for (const ok of ['', '租約', 'A 線', '資源租約派', '0123456789', 'lease-p1']) expect(labelProblem(ok)).toBeNull()
  })
  it('names the problem', () => {
    expect(labelProblem('01234567890')).toBe('width') // 11
    expect(labelProblem('資源租約派工')).toBe('width') // 12
    expect(labelProblem('😀😀😀😀😀😀')).toBe('width')
    expect(labelProblem('️')).toBe('invisible')
    expect(labelProblem('́́')).toBe('invisible')
    expect(labelProblem('a\u0007b')).toBe('chars')
    expect(labelProblem('a\nb')).toBe('chars')
    expect(labelProblem('á'.repeat(40))).toBe('chars') // narrow, but over 64 bytes
  })
  it('the limit is the daemon\'s 10', () => {
    expect(TEAM_LABEL_MAX_WIDTH).toBe(10)
  })
})

describe('goTrim', () => {
  it('trims what Go trims (U+3000, U+0085) and keeps what Go keeps (U+FEFF)', () => {
    expect(goTrim('　A\u0085')).toBe('A')
    expect(goTrim('﻿A')).toBe('﻿A')
  })
})
