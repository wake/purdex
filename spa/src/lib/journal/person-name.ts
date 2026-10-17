// spa/src/lib/journal/person-name.ts — find the person's name in an actor or a text so it can be highlighted (spec §2 item 23).
// The name is the host's `person_name`; it is never hard-coded here. Pure.

export interface NamePart { text: string; hit: boolean }

const escapeRegExp = (s: string): string => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')

/** `text` cut into runs, the occurrences of `name` marked (literal match, every occurrence). An empty / missing name marks nothing. */
export function splitByName(text: string, name: string | undefined): NamePart[] {
  if (text === '') return []
  const n = (name ?? '').trim()
  if (n === '') return [{ text, hit: false }]
  const parts: NamePart[] = []
  let from = 0
  // A name that starts / ends with a Latin letter or a digit is a whole word ("Al" is not in "Alert"), case-insensitive; a CJK
  // edge takes no boundary (those scripts have no word gaps and no case).
  const wordy = /[\p{Script=Latin}\p{N}]/u
  const chars = [...n]
  const head = wordy.test(chars[0]) ? '(?<![\\p{L}\\p{N}])' : ''
  const tail = wordy.test(chars[chars.length - 1]) ? '(?![\\p{L}\\p{N}])' : ''
  for (const m of text.matchAll(new RegExp(head + escapeRegExp(n) + tail, 'giu'))) {
    if (m.index > from) parts.push({ text: text.slice(from, m.index), hit: false })
    parts.push({ text: m[0], hit: true })
    from = m.index + m[0].length
  }
  if (from < text.length) parts.push({ text: text.slice(from), hit: false })
  return parts
}
