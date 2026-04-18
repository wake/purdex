import { describe, it, expect } from 'vitest'
import type { IBufferLine, IBufferCell } from '@xterm/xterm'
import { buildJsOffsetToCol } from './col-map'

// 建 fake IBufferLine：cells 由 [chars, width] 陣列描述
function fakeLine(cells: Array<[string, number]>): IBufferLine {
  const flat: Array<{ chars: string; width: number }> = []
  for (const [c, w] of cells) {
    flat.push({ chars: c, width: w })
    for (let i = 1; i < w; i++) flat.push({ chars: '', width: 0 })
  }
  return {
    length: flat.length,
    getCell(x: number): IBufferCell | undefined {
      const cell = flat[x]
      if (!cell) return undefined
      return {
        getWidth: () => cell.width,
        getChars: () => cell.chars,
      } as unknown as IBufferCell
    },
  } as unknown as IBufferLine
}

describe('buildJsOffsetToCol', () => {
  it('maps ASCII-only line 1:1', () => {
    const line = fakeLine([['h', 1], ['i', 1], [' ', 1], ['a', 1]])
    const map = buildJsOffsetToCol(line)
    expect(map(0)).toBe(0)
    expect(map(2)).toBe(2)
    expect(map(4)).toBe(4)
  })

  it('wide chars consume 2 cells per JS char', () => {
    // JS: 完整 a (4 chars) ; Cells: [完(2)][整(2)][ (1)][a(1)] = 6 cells
    const line = fakeLine([['完', 2], ['整', 2], [' ', 1], ['a', 1]])
    const map = buildJsOffsetToCol(line)
    expect(map(0)).toBe(0)
    expect(map(1)).toBe(2)
    expect(map(2)).toBe(4)
    expect(map(3)).toBe(5)
    expect(map(4)).toBe(6)
  })

  it('mapping for URL after CJK prefix (the bug)', () => {
    // JS: 完整路徑： (5 chars) + https://x (9 chars) = 14
    // Cells: 5 wide (10 cells) + 9 narrow (9) = 19
    const line = fakeLine([
      ['完', 2], ['整', 2], ['路', 2], ['徑', 2], ['：', 2],
      ['h', 1], ['t', 1], ['t', 1], ['p', 1], ['s', 1], [':', 1], ['/', 1], ['/', 1], ['x', 1],
    ])
    const map = buildJsOffsetToCol(line)
    // 'h' in JS is at offset 5 → terminal col should be 10 (5 wide chars × 2)
    expect(map(5)).toBe(10)
    // 'x' at JS offset 13 → terminal col 18; past-end at 14 → col 19
    expect(map(13)).toBe(18)
    expect(map(14)).toBe(19)
  })

  it('handles emoji surrogate pair (width 2, JS length 2)', () => {
    // 👍 in JS = 2 chars (surrogate pair), in terminal = 2 cells
    const line = fakeLine([['👍', 2], ['a', 1]])
    const map = buildJsOffsetToCol(line)
    expect(map(0)).toBe(0)
    // After emoji: JS offset 2, terminal col 2
    expect(map(2)).toBe(2)
  })

  it('offset past end clamps to line.length', () => {
    const line = fakeLine([['a', 1], ['b', 1]])
    const map = buildJsOffsetToCol(line)
    expect(map(99)).toBe(2)
  })
})
