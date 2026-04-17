/** Map a column count to its Tailwind grid-cols-N class. */
export function colsClass(n: number): string {
  if (n === 3) return 'grid-cols-3'
  if (n === 2) return 'grid-cols-2'
  return 'grid-cols-1'
}
