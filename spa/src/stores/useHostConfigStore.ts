// spa/src/stores/useHostConfigStore.ts — per-host cache of the daemon's
// projects / commands / resume templates / quick replies (spec §4.1). Not
// persisted, not synced: the daemon is the source of truth and every write is
// a CAS.
import { create } from 'zustand'
import {
  fetchHostConfig,
  HostConfigApiError,
  HostConfigConflictError,
  putHostConfig,
  type HostCommand,
  type HostConfigCollection,
  type HostConfigCollectionItems,
  type HostProject,
  type QuickReply,
  type RelaySwitches,
  type ResumeTemplateOverrides,
} from '../lib/host-config-api'
import {
  parseHostConfig,
  parseHostConfigField,
  type HostConfigProblem,
  type ParsedHostConfig,
} from '../lib/host-config-parse'
import { useHostStore } from './useHostStore'

export type { HostConfigProblem } from '../lib/host-config-parse'

export type HostConfigStatus = 'idle' | 'loading' | 'ready' | 'unsupported' | 'error'

export interface HostConfigEntry {
  status: HostConfigStatus
  projects: HostProject[]
  commands: HostCommand[]
  resumeTemplates: ResumeTemplateOverrides
  quickReplies: QuickReply[]
  /**
   * The daemon's GET carried a `quickReplies` field. An older daemon has the
   * rest of host config but not this collection, so `status` alone cannot tell.
   */
  quickRepliesSupported: boolean
  /** Lead-team-relay spec §8.7 (a). Defaults (both on) until the daemon's copy loads. */
  relay: RelaySwitches
  /** The daemon's GET carried a `relay` field (P5a+). */
  relaySupported: boolean
  revisions: { projects: number; commands: number; resumeTemplates: number; quickReplies: number; relay: number }
  /**
   * #1489: each collection whose stored copy on the daemon was malformed, and
   * how. Its field above holds only what could be read; the section says what
   * it hid.
   */
  problems: Partial<Record<ConfigField, HostConfigProblem>>
  error?: string
}

export const DEFAULT_RELAY_SWITCHES: RelaySwitches = Object.freeze({ self_solo: true, self_lead: true }) as RelaySwitches

export function emptyHostConfigEntry(status: HostConfigStatus = 'idle'): HostConfigEntry {
  return {
    status,
    projects: [],
    commands: [],
    resumeTemplates: {},
    quickReplies: [],
    quickRepliesSupported: false,
    relay: DEFAULT_RELAY_SWITCHES,
    relaySupported: false,
    revisions: { projects: 0, commands: 0, resumeTemplates: 0, quickReplies: 0, relay: 0 },
    problems: {},
  }
}

/** Stable fallback for selectors — a fresh object per call would loop useSyncExternalStore. */
export const EMPTY_HOST_CONFIG: HostConfigEntry = Object.freeze(emptyHostConfigEntry()) as HostConfigEntry

type ConfigField = keyof HostConfigEntry['revisions']
type Problems = HostConfigEntry['problems']

const CONFIG_FIELDS: readonly ConfigField[] = ['projects', 'commands', 'resumeTemplates', 'quickReplies', 'relay']

function problemsOf(p: ParsedHostConfig): Problems {
  const out: Problems = {}
  for (const field of CONFIG_FIELDS) {
    const problem = p[field]?.problem
    if (problem) out[field] = problem
  }
  return out
}

/** `problems` with `field`'s entry replaced by `problem`, or dropped when there is none. */
function withProblem(problems: Problems, field: ConfigField, problem: HostConfigProblem | null): Problems {
  const { [field]: _dropped, ...rest } = problems
  return problem ? { ...rest, [field]: problem } : rest
}

interface HostConfigState {
  byHost: Record<string, HostConfigEntry>
  load: (hostId: string) => Promise<void>
  ensureLoaded: (hostId: string) => Promise<void>
  saveProjects: (hostId: string, items: HostProject[]) => Promise<void>
  saveCommands: (hostId: string, items: HostCommand[]) => Promise<void>
  saveResumeTemplates: (hostId: string, items: ResumeTemplateOverrides) => Promise<void>
  saveQuickReplies: (hostId: string, items: QuickReply[]) => Promise<void>
  saveRelay: (hostId: string, items: RelaySwitches) => Promise<void>
  forget: (hostId: string) => void
}

/**
 * What a request was asked of, and what makes its answer still usable.
 *
 * A host id is not an endpoint: the same id can point at another daemon a
 * moment later (the address, the port or the token changed), or at no daemon at
 * all (the host was removed). A response that lands after either happened
 * describes a machine this cache no longer speaks to, and writing it here is
 * silent corruption — the next save would PUT the previous daemon's items,
 * under the previous daemon's revision, to the new one.
 *
 * So every request carries the endpoint it was sent to plus a generation, and
 * nothing it returns reaches the store unless both still hold.
 */
interface RequestToken { gen: number; endpoint: string }

interface Inflight { promise: Promise<void>; endpoint: string; abort: AbortController }

const inflight = new Map<string, Inflight>()
const generations = new Map<string, number>()

/** The address a request for `hostId` would go to, or `null` when there is none. */
function endpointOf(hostId: string): string | null {
  const h = useHostStore.getState().hosts[hostId]
  return h ? `${h.ip}:${h.port}:${h.token ?? ''}` : null
}

function beginRequest(hostId: string, endpoint: string): RequestToken {
  return { gen: generations.get(hostId) ?? 0, endpoint }
}

/** True only while the host still exists at the same endpoint, un-invalidated. */
function stillCurrent(hostId: string, token: RequestToken): boolean {
  if ((generations.get(hostId) ?? 0) !== token.gen) return false
  return endpointOf(hostId) === token.endpoint
}

/** Abandon every answer in flight for `hostId`; aborts the fetch when it can. */
function invalidate(hostId: string): void {
  generations.set(hostId, (generations.get(hostId) ?? 0) + 1)
  const running = inflight.get(hostId)
  if (!running) return
  inflight.delete(hostId)
  running.abort.abort()
}

/**
 * How many daemon copies a save has put in each field, per host. A load and a
 * save share the token above, so it cannot tell that a refresh was read before
 * a save landed: that GET carries the copy the save replaced (its items, the
 * older revision, the problem the save cleared). A load notes these counts when
 * it is sent and leaves alone every field whose count moved since.
 */
type WriteCounts = Partial<Record<ConfigField, number>>
const writes = new Map<string, WriteCounts>()

function countWrite(hostId: string, field: ConfigField): void {
  const counts = writes.get(hostId) ?? {}
  writes.set(hostId, { ...counts, [field]: (counts[field] ?? 0) + 1 })
}

function writesOf(hostId: string): WriteCounts {
  return writes.get(hostId) ?? {}
}

/** Where a field's `*Supported` flag lives, for the fields that have one. */
const SUPPORTED_FLAG: Partial<Record<ConfigField, 'quickRepliesSupported' | 'relaySupported'>> = {
  quickReplies: 'quickRepliesSupported',
  relay: 'relaySupported',
}

/** `loaded` with each of `fields` — items, revision, problem, `*Supported` flag — as `now` holds it. */
function holding(loaded: HostConfigEntry, now: HostConfigEntry | undefined, fields: readonly ConfigField[]): HostConfigEntry {
  if (!now || fields.length === 0) return loaded
  const out: HostConfigEntry = { ...loaded, revisions: { ...loaded.revisions } }
  for (const field of fields) {
    Object.assign(out, { [field]: now[field] })
    out.revisions[field] = now.revisions[field]
    out.problems = withProblem(out.problems, field, now.problems[field] ?? null)
    const flag = SUPPORTED_FLAG[field]
    if (flag) out[flag] = now[flag]
  }
  return out
}

function errorText(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

export const useHostConfigStore = create<HostConfigState>()((set, get) => {
  const patch = (hostId: string, update: Partial<HostConfigEntry>) =>
    set((s) => ({ byHost: { ...s.byHost, [hostId]: { ...(s.byHost[hostId] ?? emptyHostConfigEntry()), ...update } } }))

  async function save<C extends HostConfigCollection>(
    hostId: string,
    collection: C,
    field: ConfigField,
    items: HostConfigCollectionItems[C],
  ): Promise<void> {
    const entry = get().byHost[hostId]
    if (!entry || entry.status !== 'ready') throw new Error(`host config for ${hostId} is not loaded`)
    const endpoint = endpointOf(hostId)
    if (endpoint === null) throw new Error(`host ${hostId} is not configured`)
    const token = beginRequest(hostId, endpoint)
    // The daemon's copy of this one collection, read the way a load reads it.
    // A stored answer is normalised, so it clears the field's problem; a 409's
    // `current` is whatever the row holds and may set it again.
    const take = (raw: unknown) => {
      const parsed = parseHostConfigField[field](raw, hostId)
      const now = get().byHost[hostId] ?? entry
      countWrite(hostId, field)
      patch(hostId, {
        [field]: parsed.items,
        revisions: { ...now.revisions, [field]: parsed.revision },
        problems: withProblem(now.problems, field, parsed.problem),
      })
    }
    try {
      const stored = await putHostConfig(hostId, collection, items, entry.revisions[field])
      // The PUT went to the daemon that was there when it was sent. If that is
      // no longer this host's daemon, its answer says nothing about the one
      // whose copy the cache now holds.
      if (!stillCurrent(hostId, token)) return
      take(stored)
    } catch (err) {
      if (err instanceof HostConfigConflictError && stillCurrent(hostId, token)) take(err.current)
      throw err
    }
  }

  return {
    byHost: {},

    load: (hostId) => {
      const endpoint = endpointOf(hostId)
      // A host with no address is not a host this cache can hold anything for.
      if (endpoint === null) return Promise.resolve()
      const running = inflight.get(hostId)
      // Dedupe only within one endpoint: a request sent to the old daemon can
      // never answer for the new one, so it is abandoned rather than awaited.
      if (running) {
        if (running.endpoint === endpoint) return running.promise
        invalidate(hostId)
      }
      const abort = new AbortController()
      const token = beginRequest(hostId, endpoint)
      const sentWrites = writesOf(hostId)
      const run = (async () => {
        // A refresh of a ready host keeps showing its data while it loads.
        if (get().byHost[hostId]?.status !== 'ready') patch(hostId, { status: 'loading', error: undefined })
        try {
          const body = await fetchHostConfig(hostId, abort.signal)
          if (!stillCurrent(hostId, token)) return
          // Throws only for a body that is not an object at all: a load error.
          const p = parseHostConfig(body, hostId)
          // A field a save wrote since this GET was sent already holds a newer copy than the GET read.
          const counts = writesOf(hostId)
          const saved = CONFIG_FIELDS.filter((f) => (counts[f] ?? 0) !== (sentWrites[f] ?? 0))
          patch(hostId, holding({
            status: 'ready',
            error: undefined,
            projects: p.projects.items,
            commands: p.commands.items,
            resumeTemplates: p.resumeTemplates.items,
            quickReplies: p.quickReplies?.items ?? [],
            quickRepliesSupported: p.quickReplies !== undefined,
            relay: p.relay?.items ?? DEFAULT_RELAY_SWITCHES,
            relaySupported: p.relay !== undefined,
            revisions: {
              projects: p.projects.revision,
              commands: p.commands.revision,
              resumeTemplates: p.resumeTemplates.revision,
              quickReplies: p.quickReplies?.revision ?? 0,
              relay: p.relay?.revision ?? 0,
            },
            problems: problemsOf(p),
          }, get().byHost[hostId], saved))
        } catch (err) {
          // A failure is as endpoint-specific as a success: the old daemon
          // being unreachable is not this host's status any more.
          if (!stillCurrent(hostId, token)) return
          if (err instanceof HostConfigApiError && err.status === 404) {
            patch(hostId, { ...emptyHostConfigEntry('unsupported'), error: undefined })
          } else {
            patch(hostId, { status: 'error', error: errorText(err) })
          }
        } finally {
          // Only this request's own entry: a newer one may already hold the slot.
          if (inflight.get(hostId)?.abort === abort) inflight.delete(hostId)
        }
      })()
      inflight.set(hostId, { promise: run, endpoint, abort })
      return run
    },

    ensureLoaded: async (hostId) => {
      const status = get().byHost[hostId]?.status
      if (status === 'ready' || status === 'unsupported') return
      try { await get().load(hostId) } catch { /* load never throws; belt and braces */ }
    },

    saveProjects: (hostId, items) => save(hostId, 'projects', 'projects', items),
    saveCommands: (hostId, items) => save(hostId, 'commands', 'commands', items),
    saveResumeTemplates: (hostId, items) => save(hostId, 'resume-templates', 'resumeTemplates', items),
    saveQuickReplies: (hostId, items) => save(hostId, 'quick-replies', 'quickReplies', items),
    saveRelay: (hostId, items) => save(hostId, 'relay', 'relay', items),

    forget: (hostId) => {
      // Dropping the entry is only half of it: an answer already in flight
      // would put the forgotten daemon's copy straight back.
      invalidate(hostId)
      // Its write counts go too: every answer that could compare against them was just abandoned.
      writes.delete(hostId)
      set((s) => {
        if (!(hostId in s.byHost)) return s
        const rest = { ...s.byHost }
        delete rest[hostId]
        return { byHost: rest }
      })
    },
  }
})
