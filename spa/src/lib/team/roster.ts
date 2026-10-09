// spa/src/lib/team/roster.ts — the daemon's team roster on the wire (internal/team/wire_roster.go; adopt spec D-U24-5;
// plan PL-1f′ / PL-2b′): the host-wide list of live teams, sent as a `team.roster` host event
//   {op:"snapshot", teams}  to each new subscriber;  {op:"changed", teams}  after every change.
// This is the trust boundary: the frame is checked whole and a malformed one is dropped whole (the store keeps what it
// had), because half a roster would read as "those teams ended".
import { isCapPair } from './types'

export const ROSTER_EVENT_TYPE = 'team.roster'

/** The live context window of a session (statusline sample); `used_percentage` is null before the first sample. */
export interface RosterContext {
  used_percentage: number | null
  window: number
  model_id?: string
  effort?: string
  at: number
}

/** One session of a team as the roster shows it. `live` says where the values come from (registry vs team.db). */
export interface RosterSession {
  session_id: string
  ref: string
  address: string
  title?: string
  name?: string
  /** The tmux session NAME; absent when the session is not in tmux. */
  tmux_session?: string
  live: boolean
  model?: string
  effort?: string
  context?: RosterContext
  /** Cross-host teams: the wire (daemon) id of the host this session lives on, and that host's alias (the address of a
   *  remote member is `<host_alias>/<ref>`). Absent or "" = the lead's own host. A daemon that predates cross-host teams
   *  sends neither. */
  host_id?: string
  host_alias?: string
}

/** An active member: its session plus how it joined. `state` is `active` today; a cross-host team adds `joining` /
 *  `releasing` / `killing` rows. */
export interface RosterMember extends RosterSession {
  state: string
  origin: string
  joined_at: number
}

/** One live team; `members` are the active ones in join order. */
export interface TeamRoster {
  id: string
  host_id: string
  created_at: number
  /** The team's current name; '' = unnamed (also what a daemon that predates names yields — see `parseRosterEvent`). */
  team_name: string
  /** The team's short label (explicit or derived by the daemon); '' = none, also what a daemon that predates labels yields. */
  team_label: string
  lead: RosterSession
  members: RosterMember[]
  /** The team's member cap and how many of it are used (active members + spawns still starting). Absent on a daemon
   *  that predates `team.max_members.v1`. */
  max_members?: number
  in_use?: number
}

export interface RosterEventValue {
  op: 'snapshot' | 'changed'
  teams: TeamRoster[]
}

type Rec = Record<string, unknown>
const isRecord = (v: unknown): v is Rec => typeof v === 'object' && v !== null && !Array.isArray(v)
const isNum = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v)
const isStr = (v: unknown): v is string => typeof v === 'string'
const optStr = (v: unknown): boolean => v === undefined || isStr(v)

function isContext(v: unknown): v is RosterContext {
  if (!isRecord(v)) return false
  return (v.used_percentage === null || isNum(v.used_percentage)) && isNum(v.window) && isNum(v.at)
    && optStr(v.model_id) && optStr(v.effort)
}

function isRosterSession(v: unknown): v is RosterSession {
  if (!isRecord(v)) return false
  return isStr(v.session_id) && isStr(v.ref) && isStr(v.address) && typeof v.live === 'boolean'
    && optStr(v.title) && optStr(v.name) && optStr(v.tmux_session) && optStr(v.model) && optStr(v.effort)
    && (v.context === undefined || isContext(v.context))
}

function isRosterMember(v: unknown): v is RosterMember {
  if (!isRosterSession(v)) return false
  const m = v as unknown as Rec
  return isStr(m.state) && isStr(m.origin) && isNum(m.joined_at)
}

function isTeamRoster(v: unknown): v is TeamRoster {
  if (!isRecord(v)) return false
  // A team key is `<hostId>\0<teamId>` (team-views `teamKeyOf`): an id carrying the separator could make two teams' keys
  // collide and one host's frame prune another's arrangement, so such a frame is not the wire shape.
  return isStr(v.id) && !v.id.includes('\u0000') && isStr(v.host_id) && isNum(v.created_at) && isRosterSession(v.lead)
    && Array.isArray(v.members) && v.members.every(isRosterMember)
}

// C0 controls, DEL, C1 controls, and the bidi / zero-width formatting characters: none of them belongs in a host name that
// is printed into a toast (a bidi override could make one alias read as another).
// eslint-disable-next-line no-control-regex -- matching control characters is the point
const UNPRINTABLE = /[\u0000-\u001f\u007f-\u009f\u200b-\u200f\u202a-\u202e\u2066-\u2069\ufeff]/
const HOST_ALIAS_MAX = 64
const HOST_ID_MAX = 128

/** `host_id` / `host_alias` cross the trust boundary into UI text and host matching: kept only when a string within bounds
 *  and free of control / formatting characters (the alias also non-blank). Anything else is dropped (the field is absent),
 *  never a reason to drop the frame. */
function cleanHost<T extends RosterSession>(s: T): T {
  const { host_id, host_alias, ...rest } = s
  const idOk = isStr(host_id) && host_id.length <= HOST_ID_MAX && !UNPRINTABLE.test(host_id)
  const aliasOk = isStr(host_alias) && host_alias.length <= HOST_ALIAS_MAX && !UNPRINTABLE.test(host_alias) && host_alias.trim() !== ''
  return {
    ...rest,
    ...(idOk ? { host_id } : {}),
    ...(aliasOk ? { host_alias } : {}),
  } as T
}

/** The event's `value` (a JSON string, or already parsed) → the checked event, or the reason it is not one. */
export function parseRosterEvent(value: unknown): RosterEventValue | string {
  let o: unknown = value
  if (typeof value === 'string') {
    try {
      o = JSON.parse(value)
    } catch {
      return 'value is not JSON'
    }
  }
  if (!isRecord(o)) return 'value is not an object'
  const { op, teams } = o
  if (op !== 'snapshot' && op !== 'changed') return `unknown op ${JSON.stringify(op)}`
  if (!Array.isArray(teams)) return `${op}: teams is not an array`
  if (!teams.every(isTeamRoster)) return `${op}: a team is not the wire shape`
  // The guard stays tolerant of a missing `team_name` (a daemon that predates names), so give the field its
  // declared type here instead of handing the parsed objects back as-is.
  return {
    op,
    teams: (teams as TeamRoster[]).map((t) => {
      const { max_members, in_use, ...rest } = t
      return {
        ...rest,
        lead: cleanHost(t.lead),
        members: t.members.map(cleanHost),
        team_name: typeof t.team_name === 'string' ? t.team_name : '',
        team_label: typeof t.team_label === 'string' ? t.team_label : '',
        // Both or neither: a cap without its usage (or the reverse) is not a number to build a stepper on.
        ...(isCapPair(max_members, in_use) ? { max_members, in_use } : {}),
      }
    }),
  }
}
