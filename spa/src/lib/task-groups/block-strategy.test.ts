// spa/src/lib/task-groups/block-strategy.test.ts — the drag preview moves a seat's tabs as one block (TG-4c).
import { describe, it, expect } from 'vitest'
import { blockSortingStrategy } from './block-strategy'

// five items, each 100 wide at 0,100,...: a1 a2 | b | c1 c2
const KEYS = ['A', 'A', 'B', 'C', 'C']
const rects = KEYS.map((_, i) => ({ left: i * 100, right: i * 100 + 100, top: 0, bottom: 10, width: 100, height: 10 }))
const run = (activeIndex: number, overIndex: number) =>
  KEYS.map((_, index) => blockSortingStrategy(KEYS)({ activeIndex, overIndex, index, rects, activeNodeRect: rects[activeIndex], overIndexRect: rects[overIndex] } as never)?.x ?? 0)

describe('blockSortingStrategy', () => {
  it('a seat dragged forward carries all its tabs to the far edge of the block it passes; the passed blocks shift back by its width', () => {
    // a1 a2 over the block c1 c2: the block lands after c2 (+300), b, c1 and c2 each make way by the block's width (-200)
    expect(run(0, 3)).toEqual([300, 300, -200, -200, -200])
    // over b: the block lands after b (+100)
    expect(run(0, 2)).toEqual([100, 100, -200, 0, 0])
  })

  it('dragging the second tab of the seat is the same preview as dragging the first', () => {
    expect(run(1, 3)).toEqual(run(0, 3))
  })

  it('a seat dragged back lands at the near edge of the block it passes; the passed blocks shift forward by its width', () => {
    // c (index 3) over b (index 2) → c1 c2 take b's place; b moves forward by the width of the block (200)
    expect(run(3, 2)).toEqual([0, 0, 200, -100, -100])
  })

  it('over a tab of the same block, or no target: nothing moves', () => {
    expect(run(0, 1)).toEqual([0, 0, 0, 0, 0])
    expect(blockSortingStrategy(KEYS)({ activeIndex: 0, overIndex: -1, index: 2, rects } as never)).toBeNull()
  })

  it('a single tab (its own block) passing a seat block lands after the whole block', () => {
    // b over the block c1 c2: it lands after c2 (+200), c1 and c2 make way by its width (-100)
    expect(run(2, 3)).toEqual([0, 0, 200, -100, -100])
  })
})
