// spa/src/lib/relay/handoff-time.ts — when a handoff file was written, as the mark's tooltip prints it: `HH:mm` when that was
// today (the same local calendar day as `now`), `YYYY-MM-DD HH:mm` otherwise. Pure: `now` is a parameter. A time in the future
// (clock skew between the daemon's host and this Mac) is printed as a time of day, as a write of "now".
const pad2 = (n: number): string => (n < 10 ? `0${n}` : String(n))

export function formatHandoffTime(writtenAt: number, now: number): string {
  const w = new Date(writtenAt)
  const n = new Date(now)
  const clock = `${pad2(w.getHours())}:${pad2(w.getMinutes())}`
  const sameDay = w.getFullYear() === n.getFullYear() && w.getMonth() === n.getMonth() && w.getDate() === n.getDate()
  if (sameDay || writtenAt > now) return clock
  return `${w.getFullYear()}-${pad2(w.getMonth() + 1)}-${pad2(w.getDate())} ${clock}`
}
