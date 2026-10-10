// spa/src/stores/useNexHostStore.ts — per-host cache of "is Nexen ready
// here?" (P-C spec §4.1): `/api/info.nex` plus `GET /v1/capabilities`, one
// truth shared by the Host → Nex page, the Headless NewTab section and the
// hand-off entry points. Not persisted, not synced. This file is the zustand
// shell, the selectors and the host-store watcher; the entry shape and
// transitions live in `lib/nex/nex-host-reducer.ts`, the fetching in
// `lib/nex/nex-host-effects.ts`.
import { create } from 'zustand'
import { createNexHostEffects, type NexHostEntries } from '../lib/nex/nex-host-effects'
import { hostFingerprint } from '../lib/nex/nex-host-reducer'
import { useHostStore } from './useHostStore'
import type { ImageAttachmentCaps, TranscriptPreludeCapability, WorkerRollupCapability } from '../lib/nex/types'

export { NEX_HOST_TTL_MS, type NexHostEntry, type NexHostPhase } from '../lib/nex/nex-host-reducer'

interface NexHostState {
  byHost: NexHostEntries
  ensure: (hostId: string) => Promise<void>
  invalidate: (hostId: string) => Promise<void>
  clearHost: (hostId: string) => void
}

export const useNexHostStore = create<NexHostState>()((set, get) => ({
  byHost: {},
  ...createNexHostEffects({
    get: () => get().byHost,
    set: (update) =>
      set((s) => {
        const next = update(s.byHost)
        return next === s.byHost ? s : { byHost: next }
      }),
  }),
}))

export function selectReady(hostId: string): (s: Pick<NexHostState, 'byHost'>) => boolean {
  return (s) => s.byHost[hostId]?.phase === 'ready'
}

/** Nexen v0.17: `GET /v1/executions?session_id=` is honoured. Key presence, never a version compare. */
export function selectSessionFilter(hostId: string): (s: Pick<NexHostState, 'byHost'>) => boolean {
  return (s) => {
    const entry = s.byHost[hostId]
    return entry?.phase === 'ready' && entry.capabilities?.list?.session_filter === true
  }
}

/**
 * The daemon lists `conversations.scope.v1` in `/api/info.capabilities`: `GET /api/nex/conversations?scope=` is
 * honoured and Workers/test tabs may split test from normal. Key presence, never a version compare; false until the
 * host is ready.
 */
export function selectConversationsScope(hostId: string): (s: Pick<NexHostState, 'byHost'>) => boolean {
  return (s) => {
    const entry = s.byHost[hostId]
    return entry?.phase === 'ready' && entry.daemonCapabilities?.includes('conversations.scope.v1') === true
  }
}

/**
 * The daemon lists `conversations.v1` in `/api/info.capabilities`: `GET /api/conversations/{provider}/{session_id}` (the
 * conversation snapshot and its stream) is served. Key presence, never a version compare. Independent of Nexen's phase:
 * the conversation API reads Claude Code's transcript, not Nexen; false until the host's info has been fetched.
 */
export function selectConversationsV1(hostId: string): (s: Pick<NexHostState, 'byHost'>) => boolean {
  return (s) => s.byHost[hostId]?.daemonCapabilities?.includes('conversations.v1') === true
}

/** The daemon lists `team.ask_chat.v1`: a `hook_ask` can be answered in words (decide `deny` + `hook.message`). */
export function selectAskChatV1(hostId: string): (s: Pick<NexHostState, 'byHost'>) => boolean {
  return (s) => s.byHost[hostId]?.daemonCapabilities?.includes('team.ask_chat.v1') === true
}

/** Nexen v0.17: every prelude item carries an integer `offset`. */
export function selectPreludeItemOffset(hostId: string): (s: Pick<NexHostState, 'byHost'>) => boolean {
  return (s) => {
    const entry = s.byHost[hostId]
    return entry?.phase === 'ready' && entry.capabilities?.transcript_prelude?.item_offset === true
  }
}

export function selectHandoffReady(hostId: string): (s: Pick<NexHostState, 'byHost'>) => boolean {
  return (s) => {
    const entry = s.byHost[hostId]
    if (entry?.phase !== 'ready' || !entry.capabilities) return false
    return entry.capabilities.delegate?.resume_session_id === true
      && entry.capabilities.sandbox_profiles.includes('handoff')
  }
}

/**
 * Whether 「需要核准」 can be offered for a handoff on this host (permission channel plan Task 6; nexen consumer-guide
 * §9.8): the host is ready AND its build has the channel (`capabilities.permissions`) AND its `max_profile` lets
 * `handoff_ask` through (`sandbox_profiles`). Only the first two together are not enough: Nexen rejects — never
 * downgrades — a `handoff_ask` delegate on a host whose `max_profile` is below it.
 */
export function selectPermissionAskReady(hostId: string): (s: Pick<NexHostState, 'byHost'>) => boolean {
  return (s) => {
    const entry = s.byHost[hostId]
    if (entry?.phase !== 'ready' || !entry.capabilities) return false
    const { permissions, sandbox_profiles: profiles } = entry.capabilities
    return typeof permissions === 'object' && permissions !== null
      && Array.isArray(profiles) && profiles.includes('handoff_ask')
  }
}

/**
 * `capabilities.permissions.timeout.max_s` of a ready host, or null (not ready, unknown host, a build without the
 * timeout — Nexen v0.19.0 PR-B or older — or a malformed number). Non-null is the ONLY licence to send
 * `permission_timeout_s`: an older daemon silently ignores it, and the worker would wait forever.
 */
export function selectPermissionTimeoutMax(hostId: string): (s: Pick<NexHostState, 'byHost'>) => number | null {
  return (s) => {
    const entry = s.byHost[hostId]
    if (entry?.phase !== 'ready' || !entry.capabilities) return null
    const max = entry.capabilities.permissions?.timeout?.max_s
    return typeof max === 'number' && Number.isFinite(max) && max > 0 ? max : null
  }
}

/**
 * `capabilities.worker_rollup` of a ready host, or null (not ready, unknown
 * host, or an older daemon without task events / rollup fields). Presence is
 * the only feature detect (nexen contract §0) — never a version compare.
 * Returns the cached object itself, so it is a stable selector result.
 */
export function selectWorkerRollup(hostId: string): (s: Pick<NexHostState, 'byHost'>) => WorkerRollupCapability | null {
  return (s) => {
    const entry = s.byHost[hostId]
    if (entry?.phase !== 'ready' || !entry.capabilities) return null
    const rollup = entry.capabilities.worker_rollup
    return typeof rollup === 'object' && rollup !== null ? rollup : null
  }
}

/**
 * `capabilities.transcript_prelude` of a ready host, or null (not ready, an
 * unknown host, or a daemon older than Nexen v0.16.0). Presence is the only
 * feature detect (worker prelude spec §4.5). Returns the cached object
 * itself, so it is a stable selector result.
 */
export function selectTranscriptPrelude(hostId: string): (s: Pick<NexHostState, 'byHost'>) => TranscriptPreludeCapability | null {
  return (s) => {
    const entry = s.byHost[hostId]
    if (entry?.phase !== 'ready' || !entry.capabilities) return null
    const cap = entry.capabilities.transcript_prelude
    return typeof cap === 'object' && cap !== null ? cap : null
  }
}

/**
 * Whether the lists may show the rollup `cost_usd` (R4 T4.1). Only
 * `worker_rollup.cost_basis === "result_evidence"` (nexen v0.13.2+, the same
 * continuation rule as the pane's `costSummary`) is trusted; v0.13.1's
 * `"session_cumulative"` under-counts, and any other or missing value is
 * unknown — hidden too.
 */
export function selectRollupCostShown(hostId: string): (s: Pick<NexHostState, 'byHost'>) => boolean {
  const rollup = selectWorkerRollup(hostId)
  return (s) => rollup(s)?.cost_basis === 'result_evidence'
}

/** A number the wire may hand us that we can actually use as a byte cap: finite and > 0. */
function isUsableCap(n: unknown): n is number {
  return typeof n === 'number' && Number.isFinite(n) && n > 0
}

/**
 * Cache of `selectImageAttachments`'s combined result, keyed on the `send`
 * capabilities object identity. A ready host's `capabilities` object (and
 * therefore its `send` sub-object) is only ever replaced wholesale by a
 * fresh fetch (see `nex-host-effects`), never mutated in place, so keying
 * on it gives a result that is referentially stable for as long as the
 * underlying capabilities are — required so
 * `useNexHostStore(selectImageAttachments(h, p))` does not re-render forever
 * under Zustand 5 + React 19's `useSyncExternalStore` (a fresh object every
 * call fails the `Object.is` snapshot check and loops). Old entries are
 * reclaimed by the GC once their `send` object is no longer referenced
 * (WeakMap), so no explicit invalidation is needed on `clearHost`/refetch.
 */
const imageAttachmentsCache = new WeakMap<object, ImageAttachmentCaps & { maxRequestBytes: number }>()

/**
 * `capabilities.send.attachments.image` of a ready host, gated on `provider`
 * (nexen contract §0/§1.9, fail-closed per §1.9 rules 1–2): null unless the
 * capability object exists AND its `providers` includes `provider` AND its
 * three byte/count caps are all usable numbers AND the sibling
 * `send.max_request_bytes` (shared with delegate) is itself a usable number
 * — missing, non-finite or ≤ 0 fails closed the same as the other caps, so
 * callers never have to separately null-check `maxRequestBytes`; images
 * simply fall back to the non-attachment path when the daemon can't state
 * a request budget.
 *
 * The success-path object is cached (see `imageAttachmentsCache`) so equal
 * calls against the same underlying capabilities return the same reference.
 */
export function selectImageAttachments(
  hostId: string,
  provider: string,
): (s: Pick<NexHostState, 'byHost'>) => (ImageAttachmentCaps & { maxRequestBytes: number }) | null {
  return (s) => {
    const entry = s.byHost[hostId]
    if (entry?.phase !== 'ready' || !entry.capabilities) return null
    const send = entry.capabilities.send
    const image = send?.attachments?.image
    if (typeof image !== 'object' || image === null) return null
    if (!Array.isArray(image.providers) || !image.providers.includes(provider)) return null
    if (!Array.isArray(image.media_types)) return null
    if (typeof image.fetch !== 'object' || image.fetch === null
      || typeof image.fetch.method !== 'string' || typeof image.fetch.path !== 'string') return null
    if (!isUsableCap(image.max_bytes) || !isUsableCap(image.max_count) || !isUsableCap(image.max_total_bytes)) return null
    if (!isUsableCap(send.max_request_bytes)) return null

    const cached = imageAttachmentsCache.get(send)
    if (cached) return cached

    const result = {
      media_types: image.media_types,
      max_bytes: image.max_bytes,
      max_count: image.max_count,
      max_total_bytes: image.max_total_bytes,
      providers: image.providers,
      fetch: image.fetch,
      maxRequestBytes: send.max_request_bytes,
    }
    imageAttachmentsCache.set(send, result)
    return result
  }
}

/**
 * Interns `selectAttachmentFetch`'s result by `method + '\n' + path`. A ready
 * host's `capabilities` object is replaced wholesale on every refetch (the
 * same 60s TTL `ensure`/`invalidate` cycle documented on
 * `imageAttachmentsCache` above), so the raw `send.attachments.image.fetch`
 * object gets a fresh identity each time even when its content is unchanged.
 * `AttachmentThumbs` keys a fetch/revoke effect on this selector's result, so
 * without interning, every capability refresh revoked and refetched every
 * visible thumbnail. A plain `Map` (not `WeakMap`) is correct here: the key
 * is a string, not an object, and the keyspace is bounded — realistically one
 * route for the life of the app, a handful across daemon versions.
 */
const attachmentFetchInterned = new Map<string, { method: string; path: string }>()

/**
 * `capabilities.send.attachments.image.fetch` of a ready host — the route a
 * replayed image attachment is fetched back from (nexen contract §1.9) — or
 * null (not ready, unknown host, an older daemon, a malformed route). Not
 * gated on `providers`: that list says who may *send* images now, while a
 * stored attachment stays fetchable whatever the host's runners are today.
 * Returns an interned object (see `attachmentFetchInterned`), so the result
 * is stable both within one capabilities snapshot and across a content-equal
 * refetch.
 */
export function selectAttachmentFetch(hostId: string): (s: Pick<NexHostState, 'byHost'>) => { method: string; path: string } | null {
  return (s) => {
    const entry = s.byHost[hostId]
    if (entry?.phase !== 'ready' || !entry.capabilities) return null
    const route = entry.capabilities.send?.attachments?.image?.fetch
    if (typeof route !== 'object' || route === null) return null
    if (typeof route.method !== 'string' || typeof route.path !== 'string') return null
    const key = `${route.method}\n${route.path}`
    let interned = attachmentFetchInterned.get(key)
    if (!interned) {
      interned = { method: route.method, path: route.path }
      attachmentFetchInterned.set(key, interned)
    }
    return interned
  }
}

/**
 * Whether the top-level `capabilities.session_title` object exists (nexen
 * contract §0/§1.10) — the only feature detect for the execution summary's
 * `session_title` field and the `execution.title_changed` event; never a
 * version compare. True does not mean every execution has a title (§2 #62).
 */
export function selectSessionTitleSupported(hostId: string): (s: Pick<NexHostState, 'byHost'>) => boolean {
  return (s) => {
    const entry = s.byHost[hostId]
    if (entry?.phase !== 'ready' || !entry.capabilities) return false
    const title = entry.capabilities.session_title
    return typeof title === 'object' && title !== null
  }
}

/**
 * Keep the cache honest against the host store. Same shape as
 * `startPeerCacheInvalidation`: one module-level subscription for the app's
 * lifetime, started from main.tsx; returns its unsubscribe. Two triggers:
 *
 * - a host's daemon comes (back) online → refetch its readiness. Only hosts
 *   someone has already asked about are refetched — a reconnect is not a
 *   reason to poll every daemon's Nexen state;
 * - a host's identity (`ip`, `port` or `token`) changes → drop its entry.
 *   The capabilities belonged to the old daemon (or the old credentials),
 *   so they are cleared rather than refetched; whoever needs the host next
 *   asks again with `ensure`. An in-flight request for the old identity is
 *   discarded at commit time by the fingerprint check.
 */
export function startNexHostInvalidation(): () => void {
  return useHostStore.subscribe((next, prev) => {
    if (next.hosts !== prev.hosts) {
      for (const hostId of Object.keys(useNexHostStore.getState().byHost)) {
        const before = prev.hosts[hostId]
        const after = next.hosts[hostId]
        if (before && after && hostFingerprint(before) !== hostFingerprint(after)) {
          useNexHostStore.getState().clearHost(hostId)
        }
      }
    }
    if (next.runtime === prev.runtime) return
    for (const hostId of Object.keys(next.runtime)) {
      const connected = next.runtime[hostId]?.status === 'connected'
      const wasConnected = prev.runtime[hostId]?.status === 'connected'
      if (connected && !wasConnected && useNexHostStore.getState().byHost[hostId]) {
        void useNexHostStore.getState().invalidate(hostId)
      }
    }
  })
}
