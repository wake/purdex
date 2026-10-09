// spa/src/lib/team/time-text.ts — the clock text of the unattended panel's rows: `HH:mm` today, `M/D HH:mm` on another day
// (the list can span days).
const pad2 = (n: number) => (n < 10 ? `0${n}` : String(n))
const clock = (ms: number) => { const d = new Date(ms); return `${pad2(d.getHours())}:${pad2(d.getMinutes())}` }
const sameDay = (a: Date, b: Date) => a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate()

export function sinceText(ms: number): string {
  const d = new Date(ms)
  return sameDay(d, new Date()) ? clock(ms) : `${d.getMonth() + 1}/${d.getDate()} ${clock(ms)}`
}
