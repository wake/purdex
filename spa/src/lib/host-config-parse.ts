// spa/src/lib/host-config-parse.ts — what the SPA makes of the daemon's host
// config answers (#1489). Every write is normalised on the daemon; since #1889
// its GET (and a PUT's answer, a 409's current) also sends only the rows its
// PUT would take, and marks what it left out (`invalid`, `dropped`). An older
// daemon hands back whatever a row holds, so a row edited by hand arrives
// as-is. One parser per collection, and the rule is the same for all of them:
// a bad row is skipped, a bad collection reads as empty, and the section is
// told so — by the daemon's markers plus whatever is still skipped here.
// Nothing malformed may reach a render (`items.map`, `text.trim()`, `icon.kind`).
//
// Only what rendering needs is checked; the daemon's own rules (id pattern,
// lengths, Phosphor names) stay the daemon's. The exception is a value the
// section cannot show yet sends back whole (resume templates, relay prompt
// bodies): one the daemon would refuse is dropped, or no edit would save.
import { AGENT_ICON_VALUES } from './command-icons'
import { trimLikeGo } from './go-trim'
import { checkRelayPromptBody, RELAY_PROMPT_MAX_BYTES, type RelayPromptProblem } from './relay-prompt-check'
import type { HostCommand, HostProject, QuickReply, RelaySwitches, ResumeTemplateOverrides } from './host-config-api'

/** Why a section shows less than the host stores. */
export type HostConfigProblem =
  /** The collection, or its `items`, is not the container it should be: it reads as empty. */
  | { kind: 'shape' }
  /** `count` rows (resume templates: agents) are malformed and hidden. */
  | { kind: 'rows'; count: number }
  /** The relay switches do not read: the daemon refuses self relay, so both show off. */
  | { kind: 'relay' }

export interface ParsedCollection<T> { items: T; revision: number; problem: HostConfigProblem | null }

/** A collection by its GET field name (`resumeTemplates`, not `resume-templates`). */
export interface HostConfigFieldItems {
  projects: HostProject[]
  commands: HostCommand[]
  resumeTemplates: ResumeTemplateOverrides
  quickReplies: QuickReply[]
  relay: RelaySwitches
}
export type HostConfigField = keyof HostConfigFieldItems

export interface ParsedHostConfig {
  projects: ParsedCollection<HostProject[]>
  commands: ParsedCollection<HostCommand[]>
  resumeTemplates: ParsedCollection<ResumeTemplateOverrides>
  /** Absent on a daemon that predates the collection (R3-A). */
  quickReplies?: ParsedCollection<QuickReply[]>
  /** Absent on a daemon that predates P5a. */
  relay?: ParsedCollection<RelaySwitches>
}

type JsonObject = Record<string, unknown>

const utf8 = new TextEncoder()

/** What JSON calls an object: not null, not an array. */
function isObject(v: unknown): v is JsonObject {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

/** One line per skipped row or bad collection, so a developer can find it in the row. */
function warn(hostId: string, field: HostConfigField, why: string): void {
  console.warn(`[host-config] host ${hostId}, ${field}: ${why}`)
}

/**
 * The CAS base for the next PUT. A bad one reads as 0: that PUT then 409s and
 * reloads the daemon's copy, which is the right outcome anyway.
 */
function revisionOf(collection: JsonObject, hostId: string, field: HostConfigField): number {
  const r = collection.revision
  if (typeof r === 'number' && Number.isInteger(r) && r >= 0) return r
  warn(hostId, field, 'revision is not an integer >= 0; reading 0')
  return 0
}

/** Why `row` cannot be shown, or null when it can. */
type RowCheck = (row: unknown) => string | null

function stringFields(...fields: string[]): RowCheck {
  return (row) => {
    if (!isObject(row)) return 'not an object'
    const missing = fields.find((f) => typeof row[f] !== 'string')
    return missing ? `${missing} is not a string` : null
  }
}

const projectProblem = stringFields('id', 'name', 'slug', 'path')
const quickReplyProblem = stringFields('id', 'text')
const resumePairProblem = stringFields('exact', 'fallback')

function commandProblem(row: unknown): string | null {
  const why = stringFields('id', 'name', 'command')(row)
  if (why) return why
  const icon = (row as JsonObject).icon
  if (!isObject(icon)) return 'icon is not an object'
  // An agent icon is drawn from a fixed set; anything else has no component.
  if (icon.kind === 'agent') return (AGENT_ICON_VALUES as readonly unknown[]).includes(icon.value) ? null : 'unknown agent icon'
  if (icon.kind === 'phosphor') return typeof icon.value === 'string' ? null : 'icon value is not a string'
  return 'icon kind is neither agent nor phosphor'
}

function rowsProblem(count: number): HostConfigProblem | null {
  return count > 0 ? { kind: 'rows', count } : null
}

/**
 * The section's problem, from the daemon's markers (#1889) and the `skipped`
 * rows this parser dropped itself. `invalid: true` (the daemon could not read
 * the stored value; `items` is the empty value) is `invalidKind`; a positive
 * integer `dropped.count` adds to `skipped`. A malformed marker reads as none.
 */
function problemOf(
  collection: JsonObject,
  hostId: string,
  field: HostConfigField,
  skipped: number,
  invalidKind: 'shape' | 'relay' = 'shape',
): HostConfigProblem | null {
  if (collection.invalid === true) {
    warn(hostId, field, 'the daemon could not read the stored value and sent the empty value')
    return { kind: invalidKind }
  }
  const dropped = isObject(collection.dropped) ? collection.dropped : {}
  const count = Number.isInteger(dropped.count) && (dropped.count as number) > 0 ? (dropped.count as number) : 0
  if (count > 0) {
    const reasons = Array.isArray(dropped.reasons) ? dropped.reasons.filter((r) => typeof r === 'string') : []
    warn(hostId, field, `the daemon left out ${count} row(s)${reasons.length > 0 ? `: ${reasons.join('; ')}` : ''}`)
  }
  return rowsProblem(count + skipped)
}

function parseList<T>(raw: unknown, hostId: string, field: HostConfigField, check: RowCheck): ParsedCollection<T[]> {
  if (!isObject(raw)) {
    warn(hostId, field, 'collection is not an object; reading as empty')
    return { items: [], revision: 0, problem: { kind: 'shape' } }
  }
  const revision = revisionOf(raw, hostId, field)
  if (!Array.isArray(raw.items)) {
    warn(hostId, field, 'items is not an array; reading as empty')
    return { items: [], revision, problem: { kind: 'shape' } }
  }
  const items = raw.items.filter((row, i) => {
    const why = check(row)
    if (why) warn(hostId, field, `item ${i}${isObject(row) && typeof row.id === 'string' ? ` (id ${row.id})` : ''} skipped: ${why}`)
    return why === null
  }) as T[]
  return { items, revision, problem: problemOf(raw, hostId, field, raw.items.length - items.length) }
}

export function parseProjects(raw: unknown, hostId: string): ParsedCollection<HostProject[]> {
  return parseList(raw, hostId, 'projects', projectProblem)
}

export function parseCommands(raw: unknown, hostId: string): ParsedCollection<HostCommand[]> {
  return parseList(raw, hostId, 'commands', commandProblem)
}

export function parseQuickReplies(raw: unknown, hostId: string): ParsedCollection<QuickReply[]> {
  return parseList(raw, hostId, 'quickReplies', quickReplyProblem)
}

/**
 * `normalizeResumeTemplates` (`internal/module/hostconfig/validate.go`) refuses
 * the WHOLE map over one agent key off `agentTypePattern`, one template over
 * `commandMaxBytes` (UTF-8) or holding a NUL (`validTemplate`), or more than
 * `maxResumeAgents` agents. The section shows only the known agents yet PUTs
 * the whole map back, so an entry it cannot show would make every edit a 400.
 */
const RESUME_AGENT_PATTERN = /^[a-z0-9][a-z0-9_-]{0,31}$/
const RESUME_TEMPLATE_MAX_BYTES = 4096
const RESUME_MAX_AGENTS = 32

function resumeTemplateProblem(name: string, v: string): string | null {
  if (utf8.encode(v).length > RESUME_TEMPLATE_MAX_BYTES) return `${name} is over ${RESUME_TEMPLATE_MAX_BYTES} bytes`
  if (v.includes('\0')) return `${name} holds a NUL`
  return null
}

/** Why the daemon's PUT would refuse this entry, or null. */
function resumeEntryProblem(agent: string, pair: unknown): string | null {
  const why = resumePairProblem(pair)
  if (why) return why
  if (!RESUME_AGENT_PATTERN.test(agent)) return 'not a valid agent type'
  const { exact, fallback } = pair as { exact: string; fallback: string }
  return resumeTemplateProblem('exact', exact) ?? resumeTemplateProblem('fallback', fallback)
}

export function parseResumeTemplates(raw: unknown, hostId: string): ParsedCollection<ResumeTemplateOverrides> {
  const field = 'resumeTemplates'
  if (!isObject(raw)) {
    warn(hostId, field, 'collection is not an object; reading as empty')
    return { items: {}, revision: 0, problem: { kind: 'shape' } }
  }
  const revision = revisionOf(raw, hostId, field)
  if (!isObject(raw.items)) {
    warn(hostId, field, 'items is not an object; reading as empty')
    return { items: {}, revision, problem: { kind: 'shape' } }
  }
  const entries = Object.entries(raw.items)
  const valid = entries.filter(([agent, pair]) => {
    const why = resumeEntryProblem(agent, pair)
    if (why) warn(hostId, field, `agent ${agent} skipped: ${why}`)
    return why === null
  })
  // Over the cap, the first ones in object order stay.
  const kept = valid.slice(0, RESUME_MAX_AGENTS)
  for (const [agent] of valid.slice(RESUME_MAX_AGENTS)) warn(hostId, field, `agent ${agent} skipped: over ${RESUME_MAX_AGENTS} agents`)
  // `fromEntries` defines own keys, never the prototype.
  const items = Object.fromEntries(kept) as ResumeTemplateOverrides
  return { items, revision, problem: problemOf(raw, hostId, field, entries.length - kept.length) }
}

const RELAY_SWITCHES = ['self_solo', 'self_lead'] as const
const RELAY_PROMPTS = ['prompt_write', 'prompt_fix', 'prompt_seed'] as const
const RELAY_KEYS: readonly string[] = [...RELAY_SWITCHES, ...RELAY_PROMPTS]

/** The warning for each of the shared check's answers (`relay-prompt-check`, the editor's rules too). */
const RELAY_PROMPT_PROBLEM: Record<RelayPromptProblem, string> = {
  not_utf8: 'holds an unpaired surrogate (stored as U+FFFD, not as written)',
  too_long: `is over ${RELAY_PROMPT_MAX_BYTES} bytes`,
  control_chars: 'holds a control character',
  has_tag: 'holds [pdx-relay',
}

/**
 * Why the daemon's PUT would refuse this stored body (`relayPromptsOf`: blank is
 * the default, else `ValidateRelayPromptBody`), or null. A toggle sends the row
 * back whole, so a body it refuses would lock the switches. An unpaired
 * surrogate the daemon would take, but as U+FFFD: dropped too, so a toggle
 * never rewrites a body into one nobody wrote.
 */
function relayPromptProblem(v: unknown): string | null {
  if (typeof v !== 'string') return 'is not a string'
  if (trimLikeGo(v) === '') return null
  const problem = checkRelayPromptBody(v)
  return problem === null ? null : RELAY_PROMPT_PROBLEM[problem]
}

/**
 * The daemon's READ path, exactly (`relay.go` RelaySwitches → relayFields +
 * relaySwitchesOf): an object of the five known keys; an unknown key, or a
 * present switch that is not a boolean (null included), makes the whole value
 * unreadable; a missing switch is on. The prompt bodies are not the switches'
 * business there either: one the daemon would take is kept as stored, any other
 * is dropped (`dropped`) — the next write then stores the default.
 */
function readRelay(items: unknown, dropped: string[]): RelaySwitches | string {
  if (!isObject(items)) return 'items is not an object'
  const unknown = Object.keys(items).find((k) => !RELAY_KEYS.includes(k))
  if (unknown !== undefined) return `unknown key ${unknown}`
  const out: RelaySwitches = { self_solo: true, self_lead: true }
  for (const key of RELAY_SWITCHES) {
    if (!Object.hasOwn(items, key)) continue
    const v = items[key]
    if (typeof v !== 'boolean') return `${key} is not a boolean`
    out[key] = v
  }
  for (const key of RELAY_PROMPTS) {
    if (!Object.hasOwn(items, key)) continue
    const v = items[key]
    const why = relayPromptProblem(v)
    if (why === null) out[key] = v as string
    else dropped.push(`${key} ${why}; dropped`)
  }
  return out
}

/**
 * An unreadable value fails CLOSED, as on the daemon, which then refuses self
 * relay (503): both switches show off — never the defaults (on).
 */
export function parseRelay(raw: unknown, hostId: string): ParsedCollection<RelaySwitches> {
  const off = (revision: number, why: string): ParsedCollection<RelaySwitches> => {
    warn(hostId, 'relay', `${why}; the daemon refuses self relay, showing both switches off`)
    return { items: { self_solo: false, self_lead: false }, revision, problem: { kind: 'relay' } }
  }
  if (!isObject(raw)) return off(0, 'collection is not an object')
  const revision = revisionOf(raw, hostId, 'relay')
  const dropped: string[] = []
  const read = readRelay(raw.items, dropped)
  if (typeof read === 'string') return off(revision, read)
  for (const why of dropped) warn(hostId, 'relay', why)
  // The daemon marks an unreadable value `invalid` and already sends both switches off.
  return { items: read, revision, problem: problemOf(raw, hostId, 'relay', dropped.length, 'relay') }
}

/** One parser per field: what a load, a PUT answer and a 409's `current` all go through. */
export const parseHostConfigField: {
  [F in HostConfigField]: (raw: unknown, hostId: string) => ParsedCollection<HostConfigFieldItems[F]>
} = {
  projects: parseProjects,
  commands: parseCommands,
  resumeTemplates: parseResumeTemplates,
  quickReplies: parseQuickReplies,
  relay: parseRelay,
}

/**
 * The whole GET body. A required collection that is missing reads as empty
 * with a problem; a missing optional one is an older daemon, not a problem. A
 * body that is not even an object is no host's config: a load error.
 */
export function parseHostConfig(body: unknown, hostId: string): ParsedHostConfig {
  if (!isObject(body)) throw new Error('host config answer is not a JSON object')
  return {
    projects: parseProjects(body.projects, hostId),
    commands: parseCommands(body.commands, hostId),
    resumeTemplates: parseResumeTemplates(body.resumeTemplates, hostId),
    quickReplies: body.quickReplies === undefined ? undefined : parseQuickReplies(body.quickReplies, hostId),
    relay: body.relay === undefined ? undefined : parseRelay(body.relay, hostId),
  }
}
