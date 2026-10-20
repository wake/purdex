// spa/src/lib/task-groups/block-strategy.ts — the preview of a drag in the task-group bar (task groups spec §6.2, TG-4c). A team seat moves with ALL its tabs, so while one of them is dragged the whole run of its tabs travels and the
// tabs it passes make way: a horizontal sorting strategy over blocks. `keys[i]` names the block of the item at index i (the tabs of one seat share a key; any other tab is a block of its own); a block is a run of equal keys.
import type { SortingStrategy } from '@dnd-kit/sortable'

const T = (x: number) => ({ x, y: 0, scaleX: 1, scaleY: 1 })

export function blockSortingStrategy(keys: readonly string[]): SortingStrategy {
  const span = (i: number): [number, number] => {
    let s = i
    let e = i
    while (s > 0 && keys[s - 1] === keys[i]) s -= 1
    while (e < keys.length - 1 && keys[e + 1] === keys[i]) e += 1
    return [s, e]
  }
  return ({ activeIndex, overIndex, index, rects }) => {
    if (activeIndex < 0 || overIndex < 0 || activeIndex >= keys.length || overIndex >= keys.length) return null
    const [as, ae] = span(activeIndex)
    const [os, oe] = span(overIndex)
    if (as === os) return null
    const first = rects[as], last = rects[ae], overFirst = rects[os], overLast = rects[oe]
    if (!first || !last || !overFirst || !overLast) return null
    const width = last.left + last.width - first.left
    if (os > as) {
      if (index >= as && index <= ae) return T(overLast.left + overLast.width - (last.left + last.width)) // the block lands where the over block ends
      if (index > ae && index <= oe) return T(-width)
    } else {
      if (index >= as && index <= ae) return T(overFirst.left - first.left)
      if (index >= os && index < as) return T(width)
    }
    return null
  }
}
