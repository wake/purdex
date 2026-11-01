// spa/src/components/recap/recap-text.ts — small display helpers the recap pieces share.

/** A daemon stamp (Unix milliseconds) as local 24-hour HH:MM. */
export function clockOf(at: number): string {
  const d = new Date(at)
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
}
