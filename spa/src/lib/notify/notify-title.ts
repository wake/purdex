// spa/src/lib/notify/notify-title.ts — the title of an agent notification on the Mac (notify-title spec §1, §3.4): a port of `NotifyTitle` in internal/push/agent_content.go. The phone push and the Mac run the SAME
// cases (internal/push/testdata/notify-title-cases.json; notify-title.test.ts here, TestNotifyTitleFixture there), so a rule changed on one side and not the other turns a test red.
// With a persona name: "{name} — {group}", or without a group "{name} — {host}：{tmux}"; without a name today's "{host}：{tmux}". A workbook thing follows as "・{thing}". Over the limit, the thing is cut first, then
// the group (or the host and tmux part); the name is never cut. `hostLabel` is THIS device's own name for the host.
import { cutRunes, normaliseNotificationText, plainNotificationText, NOTIFICATION_TITLE_RUNES } from '../notification-normalise'

/** What the daemon worked out for a session (`NormalizedEvent.notify_label`); every part may be absent. */
export interface NotifyLabel { name?: string; group?: string; tmux?: string }

export type NotifyTitleLocale = 'zh-TW' | 'en'

const MAX_NAMED_RUNES = 40 // a session's title inside a longer title (maxNamedRunes in internal/push/content.go)
const NO_LIMIT = Number.POSITIVE_INFINITY // Normalise's "no cut" limit
const runeCount = (s: string): number => Array.from(s).length

function workbookTitle(title: string, thing: string): string {
  const t = normaliseNotificationText(thing, MAX_NAMED_RUNES)
  if (t === '') return cutRunes(title, NOTIFICATION_TITLE_RUNES)
  const base = cutRunes(title, NOTIFICATION_TITLE_RUNES)
  const room = NOTIFICATION_TITLE_RUNES - runeCount(base) - 1 // the "・"
  if (room <= 0) return base
  return `${base}・${cutRunes(t, room)}`
}

export function notifyTitle(label: NotifyLabel, hostLabel: string, thing: string, locale: NotifyTitleLocale): string {
  // The host's name and the tmux name are written outside (a look name, a session name): cleaned of control, format and direction characters, not Markdown-processed (`finalTitle` skips the cut that used to clean the whole).
  const tmux = plainNotificationText(label.tmux ?? '', NO_LIMIT)
  const host = plainNotificationText(hostLabel, NO_LIMIT)
  const row = host === '' ? tmux : `${host}${locale === 'en' ? ': ' : '：'}${tmux}`
  const name = normaliseNotificationText(label.name ?? '', NO_LIMIT) // only normalised: a persona's name is never cut
  if (name === '') return workbookTitle(row, thing)
  let mid = normaliseNotificationText(label.group ?? '', MAX_NAMED_RUNES)
  if (mid === '') mid = row
  const prefix = `${name} — `
  const room = NOTIFICATION_TITLE_RUNES - runeCount(prefix)
  if (runeCount(mid) > room) return room <= 0 ? name : prefix + cutRunes(mid, room)
  return workbookTitle(prefix + mid, thing)
}
