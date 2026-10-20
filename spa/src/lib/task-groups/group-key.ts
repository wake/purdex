// spa/src/lib/task-groups/group-key.ts — the key of a group in the task-group view (task groups spec §3): a team
// (`team\0<TeamView.key>`, local host id inside), a manual group (`group\0<id>`) or the implicit 「未分組」.
// Stores on the device hold the LOCAL form; only the section builders and appliers see wire ids.

export const UNGROUPED = 'ungrouped'

const SEP = '\u0000'
const TEAM = 'team'
const GROUP = 'group'

export type ParsedGroupKey =
  | { kind: 'team'; teamKey: string }
  | { kind: 'manual'; id: string }
  | { kind: 'ungrouped' }

function isTeamKey(k: string): boolean {
  const parts = k.split(SEP)
  return parts.length === 2 && parts[0] !== '' && parts[1] !== ''
}

/** `teamKey` is a `TeamView.key` (`<local host id>\0<team id>`). */
export function teamGroupKey(teamKey: string): string {
  if (!isTeamKey(teamKey)) throw new Error('task group: not a team view key')
  return `${TEAM}${SEP}${teamKey}`
}

export function manualGroupKey(id: string): string {
  if (id === '' || id.includes(SEP)) throw new Error('task group: bad group id')
  return `${GROUP}${SEP}${id}`
}

export function parseGroupKey(key: string): ParsedGroupKey | null {
  if (key === UNGROUPED) return { kind: 'ungrouped' }
  const cut = key.indexOf(SEP)
  if (cut < 0) return null
  const head = key.slice(0, cut)
  const rest = key.slice(cut + 1)
  if (head === TEAM) return isTeamKey(rest) ? { kind: 'team', teamKey: rest } : null
  if (head === GROUP) return rest !== '' && !rest.includes(SEP) ? { kind: 'manual', id: rest } : null
  return null
}

/** The `TeamView.key` a team group key carries, or null for any other key. */
export function teamKeyOfGroupKey(key: string): string | null {
  const parsed = parseGroupKey(key)
  return parsed?.kind === 'team' ? parsed.teamKey : null
}
