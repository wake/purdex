// spa/src/lib/team/team-names.ts — what a team is called on each surface (team interface spec §4.8, P6, P12).
//   - the tab bar capsule: the team's LABEL (the daemon derives or the person sets it, ≤ 5 wide characters worth), else the
//     lead's title cut to 10 display width;
//   - the panel header: the team's NAME, else the lead's title uncut;
//   - the tooltip: both.
// Width is `cellWidth` (the shared table, never UTF-16 length); a cut keeps whole grapheme clusters and the "…" (width 1)
// counts inside the 10.
import { cellWidth } from '../textwidth'
import type { TeamView } from './team-views'

/** The widest a derived (lead-title) label is: the "…" included. */
export const LABEL_MAX_WIDTH = 10

const ELLIPSIS = '…'

const segmenter = new Intl.Segmenter(undefined, { granularity: 'grapheme' })

/** `s` as shown in at most `max` display width: whole when it fits, else whole clusters followed by "…", all within `max`. */
export function cutToWidth(s: string, max: number): { text: string; truncated: boolean } {
  if (cellWidth(s) <= max) return { text: s, truncated: false }
  const room = max - cellWidth(ELLIPSIS)
  let text = ''
  let used = 0
  for (const { segment } of segmenter.segment(s)) {
    const w = cellWidth(segment)
    if (used + w > room) break
    text += segment
    used += w
  }
  return { text: text + ELLIPSIS, truncated: true }
}

/** The tab bar capsule text: `text` is what is drawn, `full` what a tooltip shows. */
export function groupLabel(view: TeamView): { text: string; full: string; truncated: boolean } {
  if (view.label !== '') return { text: view.label, full: view.label, truncated: false }
  const full = view.lead.label
  const cut = cutToWidth(full, LABEL_MAX_WIDTH)
  return { text: cut.text, full, truncated: cut.truncated }
}

/** The panel header: the team's name, else the lead's label uncut (`unnamed` says which). */
export function panelName(view: TeamView): { text: string; unnamed: boolean } {
  if (view.name !== '') return { text: view.name, unnamed: false }
  return { text: view.lead.label, unnamed: true }
}

/** `"<name> (<label>)"` when both exist; whichever exists alone; else the lead's title. */
export function tooltipOf(view: TeamView): string {
  if (view.name !== '' && view.label !== '') return `${view.name} (${view.label})`
  return view.name || view.label || view.lead.label
}
