// spa/src/lib/host-config-parse.ts — what the SPA makes of the daemon's host
// config answers (#1489). Every write is normalised on the daemon, but its GET
// hands back whatever a row holds, so a row edited by hand arrives as-is. One
// parser per collection, and the rule is the same for all of them: a bad row is
// skipped, a bad collection reads as empty, and the section is told so. Nothing
// malformed may reach a render (`items.map`, `text.trim()`, `icon.kind`).
//
// Only what rendering needs is checked; the daemon's own rules (id pattern,
// lengths, Phosphor names) stay the daemon's.
import { AGENT_ICON_VALUES } from './command-icons'
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
  return { items, revision, problem: rowsProblem(raw.items.length - items.length) }
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
  const kept = entries.filter(([agent, pair]) => {
    const why = resumePairProblem(pair)
    if (why) warn(hostId, field, `agent ${agent} skipped: ${why}`)
    return why === null
  })
  // `fromEntries` defines own keys: a stored `__proto__` agent stays a key.
  const items = Object.fromEntries(kept) as ResumeTemplateOverrides
  return { items, revision, problem: rowsProblem(entries.length - kept.length) }
}

const RELAY_SWITCHES = ['self_solo', 'self_lead'] as const
const RELAY_PROMPTS = ['prompt_write', 'prompt_fix', 'prompt_seed'] as const
const RELAY_KEYS: readonly string[] = [...RELAY_SWITCHES, ...RELAY_PROMPTS]

/**
 * The daemon's READ path, exactly (`relay.go` RelaySwitches → relayFields +
 * relaySwitchesOf): an object of the five known keys; an unknown key, or a
 * present switch that is not a boolean (null included), makes the whole value
 * unreadable; a missing switch is on. The prompt bodies are not the switches'
 * business there either: a string is kept as stored, anything else is dropped
 * (`warnings`) — the next write then stores the default, which the daemon takes.
 */
function readRelay(items: unknown, warnings: string[]): RelaySwitches | string {
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
    if (typeof v === 'string') out[key] = v
    else warnings.push(`${key} is not a string; dropped`)
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
  const warnings: string[] = []
  const read = readRelay(raw.items, warnings)
  if (typeof read === 'string') return off(revision, read)
  for (const why of warnings) warn(hostId, 'relay', why)
  return { items: read, revision, problem: null }
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
