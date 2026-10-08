// spa/src/lib/team/label.ts — the team label's rules in the App (team-label spec D-L1, D-L3, D-L10): the width/validity
// check the approval dialog shows before 核准, and the label the daemon will derive from a name, which the dialog shows
// as the placeholder while the field is empty. The daemon is the authority (`team.NormaliseTeamLabel`,
// `team.DeriveTeamLabel`); this mirrors it and reads the same fixtures (testdata/textwidth/cases.json,
// testdata/teamlabel/derive.json), so a drift fails a test on one side.
import { cellWidth } from '../textwidth'

/** A label weighs at most this (about five Chinese characters, each 2). */
export const TEAM_LABEL_MAX_WIDTH = 10
const TEAM_LABEL_MAX_BYTES = 64

// Names and labels are printed into terminals: only printable characters (Go's unicode.IsPrint, approximated by the
// categories the daemon accepts: letters, marks, numbers, punctuation, symbols and the ASCII space).
export const TEAM_NAME_CHARS = /^[\p{L}\p{M}\p{N}\p{P}\p{S} ]*$/u

// Go's strings.TrimSpace strips unicode.IsSpace: \t \n \v \f \r, space, U+0085, U+00A0, U+1680, U+2000–U+200A, U+2028,
// U+2029, U+202F, U+205F and U+3000. JS trim() differs (it keeps U+0085 and strips U+FEFF), so trim by that set.
const GO_SPACE = String.raw`[\t-\r \u0085\u00A0\u1680\u2000-\u200A\u2028\u2029\u202F\u205F\u3000]`
const GO_SPACE_EDGES = new RegExp(`^${GO_SPACE}+|${GO_SPACE}+$`, 'gu')
const GO_SPACE_ONE = new RegExp(`^${GO_SPACE}$`, 'u')
export const goTrim = (s: string) => s.replace(GO_SPACE_EDGES, '')
const isSpace = (ch: string) => GO_SPACE_ONE.test(ch)

/** Why a (trimmed) label is not acceptable, or null when the daemon would take it. '' is "no label" and fine. */
export function labelProblem(label: string): 'chars' | 'invisible' | 'width' | null {
  if (label === '') return null
  if (new TextEncoder().encode(label).length > TEAM_LABEL_MAX_BYTES || !TEAM_NAME_CHARS.test(label)) return 'chars'
  const w = cellWidth(label)
  if (w === 0) return 'invisible'
  if (w > TEAM_LABEL_MAX_WIDTH) return 'width'
  return null
}

const ALWAYS = new Set([':', '：', '／', '|', '｜', '－', '—'])

/**
 * The label the daemon derives from a team name when no label is given (D-L3): the part before the name's first
 * separator (the whole name when there is none), if that part is not empty and acceptable; otherwise ''. Never a cut
 * from the middle. `: ： ／ | ｜ － —` separate wherever they stand; the ASCII `-` and `/` only with white space on both
 * sides (inside `resource-lease` or `I/O` they are not separators).
 */
export function deriveTeamLabel(name: string): string {
  const trimmed = goTrim(name)
  if (trimmed === '') return ''
  const chars = [...trimmed]
  let end = chars.length
  for (let i = 0; i < chars.length; i++) {
    const c = chars[i]
    const sep = ALWAYS.has(c) || ((c === '-' || c === '/') && i > 0 && i + 1 < chars.length && isSpace(chars[i - 1]) && isSpace(chars[i + 1]))
    if (sep) { end = i; break }
  }
  const candidate = goTrim(chars.slice(0, end).join(''))
  return candidate !== '' && labelProblem(candidate) === null ? candidate : ''
}
