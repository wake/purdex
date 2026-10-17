// spa/src/lib/relay/handoff-time.ts — when a handoff file was written, as the mark's tooltip prints it: `HH:mm` when that was
// today (the same local calendar day as `now`), `YYYY-MM-DD HH:mm` otherwise. Pure: `now` is a parameter. Only the calendar day
// decides: a write the host's clock puts into tomorrow (clock skew between the daemon's host and this Mac) carries its date.
// A time `Date` cannot hold (Invalid Date) prints as the empty string: the caller leaves the time out.
const pad2 = (n: number): string => (n < 10 ? `0${n}` : String(n))

export function formatHandoffTime(writtenAt: number, now: number): string {
  const w = new Date(writtenAt)
  if (Number.isNaN(w.getTime())) return ''
  const n = new Date(now)
  const clock = `${pad2(w.getHours())}:${pad2(w.getMinutes())}`
  const sameDay = w.getFullYear() === n.getFullYear() && w.getMonth() === n.getMonth() && w.getDate() === n.getDate()
  if (sameDay) return clock
  return `${w.getFullYear()}-${pad2(w.getMonth() + 1)}-${pad2(w.getDate())} ${clock}`
}
