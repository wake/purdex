/**
 * Compute maximum nesting depth of `value`, treating plain objects and arrays
 * as one level deeper per descent. Throws when depth exceeds `max` so callers
 * can reject pathologically deep structures without consuming all stack.
 */
export function objectDepth(value: unknown, max = 32): number {
  if (value == null || typeof value !== 'object') return 0

  let deepest = 0
  const stack: { val: object; d: number }[] = [{ val: value as object, d: 1 }]

  while (stack.length > 0) {
    const { val, d } = stack.pop()!
    if (d > max) throw new Error(`object depth exceeds ${max}`)
    if (d > deepest) deepest = d

    for (const child of Object.values(val)) {
      if (child != null && typeof child === 'object') {
        stack.push({ val: child as object, d: d + 1 })
      }
    }
  }

  return deepest
}
