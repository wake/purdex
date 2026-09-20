// spa/src/lib/profile/executor.ts — the sync driver of Profile Sync (spec §4.4,
// §4.6–§4.6.3; P2b plan Task 9). `sync-state.ts` decides WHAT to do; this file
// does it and turns every result back into an event. It obeys, line by line,
// the "driver contract" in the header of sync-state.ts.
//
// It has no caller yet (Task 11 starts it). One executor = one (host, profile)
// pair for the time this window holds the leader lease; a new master or a lost
// lease gets `dispose()` and a fresh executor.
//
//   dispatch(key, event) → reduceSection → (same reference? stop) → persist →
//   pump(key) → decideSection → run the action → its result is an event again
//
// RULES THIS FILE KEEPS
//   - One action per section at a time. A pump that arrives while one runs is
//     remembered and replayed when it ends. Events are never held back — only
//     actions are serialised; a lock refuses to apply, not to know.
//   - One network WRITE per profile at a time (a FIFO). A `schema` answer sets
//     the profile's schema lock before the next write is dequeued, so "locked:
//     schema is the whole profile" (§4.4) is true, not eventual. Reads are not
//     serialised.
//   - A flight is opened (`push-started`) only when its write is about to go
//     out, and is sent only if the reducer took that very token AND this window
//     still holds the lease. Once opened it ends in exactly one terminal event —
//     unless the executor is disposed, whose state machines are garbage.
//   - `reindex` is profile-level single-flight. Every section's `indexEpoch` is
//     read when the request is SENT. A failed list request dispatches NOTHING:
//     "the request failed" must never read as "the profile is gone".
//   - A well-formed list WITHOUT this profile means it was deleted (a rebuilt
//     one has a new id). That is NOT `sot-index(null)` per section — that reads
//     as "every section was deleted" and would pull the deletions over the local
//     data. The executor stops instead (`profileGone`): no action runs again,
//     nothing is applied, nothing is dropped, and `status().profile` reports
//     `locked:reset` for P3's wizard.
//   - No `tabs.*` pull while `hosts` or `workspaces` is not UP TO DATE — clean,
//     index fresh, SOT not moved, no action of its own running — (apply-to-stores'
//     CALLER CONTRACT: a pane of a host not known yet would be branded
//     `host-removed`, and that brand is synced back), nor while its workspace
//     does not exist locally (the apply would be `unrendered`).
//   - AN EMPTY `tabs.<id>` THAT WAS NEVER AGREED ON, WHILE THE SOT HAS CONTENT,
//     IS NOT AN EDIT — IT HAS NOT ARRIVED YET. Applying `workspaces` from another
//     client makes an empty workspace appear here; 500 ms later the collector
//     reports `tabs.<id>` = the empty tabs. Fed to the reducer while the pull is
//     still out, that is "dirty + SOT moved" → `lock-conflict`: the user, having
//     done nothing, would be asked to choose between a blank and the real tabs.
//     So `onSection` does NOT dispatch a report that is (a) a `tabs.*` section,
//     (b) with `base.hash === null` (this client never agreed on it), (c) whose
//     SOT has content, (d) and whose payload is the empty placeholder; it only
//     pumps the section so the pull happens. While the index has not landed,
//     (c) is unknown — `sot.hash` is a placeholder then — so such a report is
//     HELD (the latest per key) and judged when the index lands: SOT has
//     content → dropped; SOT has nothing → delivered (a workspace created HERE,
//     which is pushed). A non-empty payload is a real edit and conflicts, as it
//     should; once there is a base, an empty report is an ordinary one.
//   - Every retry goes through one timer mechanism, all of it cancelled by
//     `dispose()`. A failed network action is not retried before its backoff has
//     passed, whatever else pumps the section meanwhile (2 s → 4 → 8 … 30 s cap;
//     reset by a success, by `onReconnected()` and by `syncNow()`).
//   - After `dispose()` every continuation checks `disposed` after every
//     `await` and drops its result: no dispatch, no persistence, no timer.
//
// THE FIRST RECONCILIATION (spec §4.9, decision 10) — while `initialDirection()`
// is not null
//   A client that has just attached never agreed with the SOT on anything, so a
//   section both sides hold is dirty + moved → `lock-conflict`. The state
//   machine is right: it cannot know which side to keep. The user said so when
//   they attached — `push` (this machine overwrites the SOT) or `pull` (the SOT
//   overwrites this machine) — and during this period the executor answers the
//   lock on their behalf, THROUGH THE SAME DOOR the user would use: `locked` is
//   dispatched as always and `resolved{keep}` right after it (`pull` → 'sot' →
//   forcePull; `push` → 'local' → rebase, push). `locked:reset` likewise, and a
//   lock restored from the section store or made by a 409.
//   - NOT `locked:invalid` (the payload cannot be applied; no direction fixes
//     that), not the schema lock, not `profileGone`.
//   - NOT, under `push`, a 409 whose sent snapshot is no longer what the stores
//     hold: keep-local restores the SENT snapshot, which would undo the edit
//     made since. That one is the user's.
//   - `push` means this machine REPLACES the SOT, so a `tabs.<id>` another
//     client left there, whose workspace is not here, must not stay behind as an
//     orphan. Once `workspaces` is up to date such a section (no local content,
//     never agreed on, live on the SOT) gets a `deleteSection(baseRev = the rev
//     the index gave)` through the write FIFO. 200 → the section is observed as
//     gone. 409 → someone is writing it right now: a problem, no retry (by this
//     executor), the ordinary rules have it afterwards. Sections of a kind this
//     client does not know never enter the executor at all: carried, not deleted.
//   - `pull` means nothing of this machine's should reach the SOT before the
//     SOT's `workspaces` has been taken: a local `tabs.<id>` is not pushed until
//     `hosts` and `workspaces` are up to date, nor once its workspace is no
//     longer here — its workspace may be about to be replaced, and the push
//     would plant an orphan only to delete it again. (An
//     empty SOT has nothing to take: `workspaces` is pushed by the ordinary
//     rules, becomes up to date, and the tabs follow.)
//   - It ends when, with an index SEEN by this executor, every section is up to
//     date (clean, index fresh, SOT not moved, nothing in flight / forced /
//     to restore / running), the write queue is empty, no placeholder is held
//     and nothing is locked: `onInitialSettled()` — once per period; the start
//     layer clears the stored direction. A `tabs.*` section that cannot be
//     rendered here (no such workspace, no local content) does not count: it is
//     carried, and would otherwise hold the period open for ever.
//   - IT NEVER ENDS BY TIMEOUT. Offline, a `locked:invalid`, a schema lock: the
//     direction stays and the period resumes at the next connect, reload
//     included. Ending it early would be SAFE — later conflicts go to the user —
//     but "pull onto a new machine" would then turn into a dialog per section
//     whenever the first attempt was cut short. The cost of the choice made
//     here: a conflict that arises while the period is still open (another
//     client writing meanwhile) is settled by the direction, not by the user.
//
// WHAT THE START LAYER (Task 11) OWES THIS FILE
//   - `onReconnected()` whenever the master host's event stream (re)connects —
//     THE FIRST CONNECT INCLUDED. Creating an executor starts nothing by itself:
//     a restored section is `indexStale` and waits for that call (or for a
//     collector report that changes it).
//   - `dispose()` on a lost lease / a changed master. `putAttachment` /
//     `deleteAttachment` are the start layer's too.
//
// KNOWN GAPS, SURFACED THROUGH `onProblem` RATHER THAN HIDDEN
//   - `conflict-not-persisted`: `saveConflict` refused — storage would not take
//     the sent snapshot (quota), or it is not held. The lock lives in memory
//     only; the sent snapshot does not survive a restart (spec §7, issue #1244).
//     (A missing SOT-side payload is NOT this: the store requires the local
//     side only.)
//   - `restore-payload-missing`: keep-local was chosen but the sent snapshot is
//     in neither stash. `local-restored` is NOT dispatched (it would claim a
//     restore that did not happen); the section stays on `restore-local` until a
//     local edit cancels the restore — the only way out the reducer offers.
//   - `pull-hash-mismatch`: after an apply the stores hold something else than
//     what was fetched (a sanitiser; a deleted `tabs.<id>` whose workspace is
//     still here). Not a fault: `pull-applied` carries both hashes, the section
//     is dirty against the base it just agreed on, and it is pushed back.
import { useWorkspaceStore } from '../../features/workspace/store'
import { getClientId } from '../client-identity'
import { deleteSection, getSection, listProfiles, putSection } from './api'
import type { DeleteOutcome, Failure, PutOutcome } from './api'
import { applySectionToStores } from './apply-to-stores'
import type { ApplyOutcome } from './apply-to-stores'
import type { SectionReport } from './collector'
import { compareShape, profileLock, profileStatus, reconcileSectionSet } from './profile-state'
import type { ProfileStatus, SchemaLock } from './profile-state'
import type { ProfileRemoteEvent } from './profile-ws-dispatch'
import { sectionKind, shapeTable, workspaceIdOf } from './projections'
import { dropSection, getStash, loadSectionStore, pruneStash, saveConflict, saveSection } from './section-store'
import { canApplyPull, canRestoreLocal, decideSection, initialSectionState, reduceSection, restoreSectionState, retainedHashes, sotMoved } from './sync-state'
import type { FlightToken, SectionEvent, SectionStatus, SectionSyncState } from './sync-state'
import type { ProfileSectionKey, SectionKind, Shape } from './types'

export interface ExecutorDeps {
  hostId: string
  profileId: string
  /** Asked again right before every network write goes out. */
  isLeader: () => boolean
  isReachable: () => boolean
  autoSync: () => boolean
  /** Backoff clock; default `Date.now`. */
  now?: () => number
  onProblem?: (p: { kind: string; section?: string; detail: string }) => void
  /** For P3's UI. Called only when the status actually changed. */
  onStatus?: (s: ExecutorStatus) => void
  /** The direction chosen at attach, until the first reconciliation has settled (see the header). Absent = null. Read live. */
  initialDirection?: () => 'push' | 'pull' | null
  /** The first reconciliation has settled. Once per period; the callee clears the direction. */
  onInitialSettled?: () => void
}

export interface ExecutorStatus {
  profile: ProfileStatus
  schemaLock: SchemaLock | null
  sections: Record<string, SectionStatus>
}

export interface Executor {
  /** Wire to the collector. */
  onSection(r: SectionReport): void
  /** Wire to `subscribeProfileEvents`. */
  onRemoteEvent(e: ProfileRemoteEvent): void
  /** The master host's event stream (re)connected — the first connect included. */
  onReconnected(): void
  /** Manual sync: this round decides as if `autoSync` were on. */
  syncNow(): void
  resolve(section: string, keep: 'local' | 'sot'): void
  status(): ExecutorStatus
  dispose(): void
}

export const BACKOFF_BASE_MS = 2_000
export const BACKOFF_CAP_MS = 30_000
/** `busy` (the operation lock is held by someone else) is not a failure: a short, flat wait. */
export const BUSY_RETRY_MS = 500
const DEFAULT_CONTENDED_MS = 1_000
/** The sections every `tabs.*` pull waits for (apply-to-stores' CALLER CONTRACT). */
const GATES: readonly string[] = ['hosts', 'workspaces']

/** How an action ended: decide again now · wait for the next event · decide again after `ms`. */
type Finish = { how: 'again' } | { how: 'wait' } | { how: 'retry'; ms: number }
const AGAIN: Finish = { how: 'again' }
const WAIT: Finish = { how: 'wait' }

type Timer = ReturnType<typeof setTimeout>
type Shapes = Record<SectionKind, Shape>

function backoffMs(failures: number): number {
  return Math.min(BACKOFF_BASE_MS * 2 ** failures, BACKOFF_CAP_MS)
}

function isPayload(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

/** `{order: [], tabs: {}}` — what a workspace with no tab builds. */
function isEmptyTabs(payload: unknown): boolean {
  if (!isPayload(payload)) return false
  const { order, tabs } = payload
  return Array.isArray(order) && order.length === 0 && isPayload(tabs) && Object.keys(tabs).length === 0
}

function message(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

/** What the section store keeps of a state — the change detector for `persist`. */
function persistedSignature(s: SectionSyncState): string {
  return JSON.stringify([s.base.rev, s.base.hash, s.currentHash, s.conflict === null ? null : [s.conflict.localHash, s.conflict.sot.rev, s.conflict.sot.hash]])
}

function localWorkspaceIds(): string[] {
  return useWorkspaceStore.getState().workspaces.map((w) => w.id)
}

export function createExecutor(deps: ExecutorDeps): Executor {
  const { hostId, profileId } = deps
  const now = deps.now ?? Date.now
  const abort = new AbortController()
  const requestOptions = { signal: abort.signal }

  let disposed = false
  /** The list answered, well-formed, without this profile. Terminal for this executor. */
  let profileGone = false
  /** `unauthorized` / `unknown-host`: no request until `onReconnected()` / `syncNow()`. */
  let blocked = false
  let schemaLock: SchemaLock | null = null
  /** `syncNow()`: decide with autoSync on until the executor is idle again. */
  let manual = false

  const sections = new Map<string, SectionSyncState>()
  /** Payloads by hash: collector reports and the SOT side of a 409. Pruned to what the sections retain. */
  const stash = new Map<string, unknown>()
  const running = new Set<string>()
  const repump = new Set<string>()
  const failures = new Map<string, number>()
  const notBefore = new Map<string, number>()
  const retryTimers = new Map<string, Timer>()
  /** `restore-local` whose payload is in neither stash, by the hash that is missing. */
  const parkedRestore = new Map<string, string>()

  /** Empty `tabs.*` placeholders reported before the index landed: the latest per key (see the header). */
  const heldPlaceholders = new Map<string, SectionReport>()

  /** Last signature a persist was ATTEMPTED with, so a refused write is not retried on every event. */
  const attempted = new Map<string, string>()
  const storedConflict = new Set<string>()
  const reportedOnce = new Set<string>()

  let reindexing = false
  let reindexForced = false
  let reindexFailures = 0
  let reindexNotBefore = 0
  let reindexTimer: Timer | null = null

  /** The profile's write FIFO. `fail` = what a throwing `send` owes (closing the flight it opened). */
  const queue: Array<{ key: string; send: () => Promise<Finish>; fail: () => void; done: (f: Finish) => void }> = []
  let draining = false

  /** The first reconciliation (see the header). */
  let indexSeen = false
  let settledReported = false
  /** Orphan deletes queued or out, and the ones already tried (never again by this executor). */
  const orphanPending = new Set<string>()
  const orphanTried = new Set<string>()

  let shapesPromise: Promise<Shapes> | null = null
  let previousWorkspaceIds = localWorkspaceIds()
  let lastStatus = ''

  /* ─── reporting ─── */

  function problem(kind: string, detail: string, section?: string): void {
    try {
      deps.onProblem?.(section === undefined ? { kind, detail } : { kind, section, detail })
    } catch {
      // a listener's bug is not the driver's
    }
  }

  /** Reported once per `id` until `forget(id)`. */
  function problemOnce(id: string, kind: string, detail: string, section?: string): void {
    if (reportedOnce.has(id)) return
    reportedOnce.add(id)
    problem(kind, detail, section)
  }

  function status(): ExecutorStatus {
    const states = Object.fromEntries(sections)
    return {
      profile: profileGone ? 'locked:reset' : profileStatus({ hasMaster: true, sections: states, lock: schemaLock }),
      schemaLock,
      sections: Object.fromEntries([...sections].map(([key, s]) => [key, s.status])),
    }
  }

  function emitStatus(): void {
    if (deps.onStatus === undefined) return
    const next = status()
    const signature = JSON.stringify(next)
    if (signature === lastStatus) return
    lastStatus = signature
    try {
      deps.onStatus(next)
    } catch {
      // as above
    }
  }

  /** The projection shapes are constants of this build: computed once. */
  function shapes(): Promise<Shapes> {
    shapesPromise ??= shapeTable().then((t) => {
      const shape = ([fingerprint, ordinal]: [string, number]): Shape => ({ fingerprint, ordinal })
      return { hosts: shape(t.hosts), settings: shape(t.settings), workspaces: shape(t.workspaces), tabs: shape(t.tabs) }
    })
    return shapesPromise
  }

  function setSchemaLock(lock: SchemaLock | null): void {
    const was = schemaLock
    schemaLock = lock
    if (lock !== null && (was === null || was.section !== lock.section || was.verdict !== lock.verdict)) {
      problem('schema-lock', `${lock.verdict}: mine ${lock.mine.ordinal}, SOT ${lock.sot.ordinal}`, lock.section)
    }
    emitStatus()
  }

  /* ─── persistence ─── */

  function keepSet(): Set<string> {
    const keep = new Set<string>()
    for (const s of sections.values()) {
      for (const hash of retainedHashes(s)) keep.add(hash)
      if (s.currentHash !== null) keep.add(s.currentHash)
    }
    return keep
  }

  function pruneMemoryStash(): void {
    const keep = keepSet()
    for (const hash of [...stash.keys()]) if (!keep.has(hash)) stash.delete(hash)
  }

  function persist(key: string, s: SectionSyncState): void {
    const signature = persistedSignature(s)
    if (attempted.get(key) === signature) return
    attempted.set(key, signature)

    if (s.conflict !== null) {
      // The LOCAL side — the snapshot that was sent — is the one the store
      // requires (it exists nowhere else); it is in the stash because
      // `retainedHashes` kept it. The SOT side goes along when it is held (a
      // 409 brought it); a lock decided from an index, or one that learnt of a
      // newer SOT rev, has only its hash, and the store does not miss it.
      const payloads: Record<string, unknown> = {}
      for (const hash of [s.conflict.localHash, s.conflict.sot.hash]) {
        if (hash !== null && stash.has(hash)) payloads[hash] = stash.get(hash)
      }
      const result = saveConflict(profileId, key, { base: s.base, currentHash: s.currentHash, conflict: s.conflict }, payloads)
      if (result === 'ok') storedConflict.add(key)
      else problemOnce(`conflict:${key}`, 'conflict-not-persisted', 'the sent snapshot is not held, or storage refused it: the conflict lives in memory only and will not survive a restart', key)
      return
    }
    reportedOnce.delete(`conflict:${key}`)

    const absent = s.base.hash === null && s.currentHash === null
    const result = absent ? dropSection(profileId, key) : saveSection(profileId, key, { base: s.base, currentHash: s.currentHash })
    if (result === 'ok') reportedOnce.delete(`persist:${key}`)
    else problemOnce(`persist:${key}`, 'persist-failed', 'the section store refused the write', key)
    // A lifted lock leaves its payloads behind; what is still retained (a pending restore) stays.
    if (storedConflict.delete(key)) pruneStash(profileId, keepSet())
  }

  /** A section that exists on neither side and owes nothing is forgotten, so it stops asking for an index. */
  function forgetIfGone(key: string, s: SectionSyncState): void {
    const gone =
      s.base.hash === null && s.currentHash === null && s.sot.hash === null && s.conflict === null && s.inFlight === null &&
      s.restoreLocal === null && !s.forcePull && s.invalid === null && s.status !== 'locked:reset' && !running.has(key)
    if (!gone) return
    sections.delete(key)
    attempted.delete(key)
    clearBackoff(key)
    parkedRestore.delete(key)
  }

  /* ─── the loop ─── */

  /** `true` iff the state changed. A section nobody knows starts from `initialSectionState(null)`. */
  function dispatch(key: string, event: SectionEvent, pumpAfter = true): boolean {
    if (disposed) return false
    const prev = sections.get(key) ?? initialSectionState(null)
    const next = reduceSection(prev, event)
    if (next === prev) return false
    sections.set(key, next)
    persist(key, next)
    pruneMemoryStash()
    forgetIfGone(key, next)
    emitStatus()
    const keep = answerFor(next)
    if (keep !== null) {
      dispatch(key, { type: 'resolved', keep }, pumpAfter)
      return true
    }
    if (pumpAfter) {
      pump(key)
      // `hosts` / `workspaces` gate every `tabs.*` pull
      if (GATES.includes(key)) pumpTabs()
      checkSettled()
    }
    return true
  }

  /* ─── the first reconciliation ─── */

  function direction(): 'push' | 'pull' | null {
    if (deps.initialDirection === undefined) return null
    const d = deps.initialDirection()
    return d === 'push' || d === 'pull' ? d : null
  }

  /** What the direction answers to this lock, or null: not locked, not its to answer, or no direction. */
  function answerFor(s: SectionSyncState): 'local' | 'sot' | null {
    if (s.status !== 'locked:conflict' && s.status !== 'locked:reset') return null
    const d = direction()
    if (d === null) return null
    if (d === 'pull') return 'sot'
    // keep-local restores the SENT snapshot: not over an edit made since
    if (s.conflict !== null && s.conflict.localHash !== s.currentHash) return null
    return 'local'
  }

  /** Locks that were not made under this direction: restored from the store, or older than the attach. */
  function answerStandingLocks(): void {
    for (const [key, s] of [...sections]) {
      const keep = answerFor(s)
      if (keep !== null) dispatch(key, { type: 'resolved', keep }, false)
    }
  }

  function upToDate(key: string): boolean {
    const s = sections.get(key)
    return (
      s !== undefined && s.status === 'synced' && !s.indexStale && !sotMoved(s) && s.inFlight === null &&
      s.restoreLocal === null && !s.forcePull && !running.has(key)
    )
  }

  function gatesUpToDate(): boolean {
    return GATES.every(upToDate)
  }

  /** A `tabs.*` the SOT holds, this client never had, and whose workspace is not here. */
  function isUnrendered(key: string, s: SectionSyncState): boolean {
    if (sectionKind(key) !== 'tabs' || s.currentHash !== null || s.base.hash !== null || s.sot.hash === null) return false
    const id = workspaceIdOf(key)
    return id === null || !localWorkspaceIds().includes(id)
  }

  /** `push` only: queue the delete of every unrendered `tabs.*`, once `workspaces` says what this machine has. */
  function sweepOrphans(): void {
    if (disposed || profileGone || schemaLock !== null || blocked || !indexSeen || direction() !== 'push') return
    if (!deps.isReachable() || !gatesUpToDate()) return
    for (const [key, s] of [...sections]) {
      if (s.indexStale || s.inFlight !== null || running.has(key) || !isUnrendered(key, s)) continue
      if (orphanPending.has(key) || orphanTried.has(key)) continue
      orphanPending.add(key)
      const rev = s.sot.rev
      queue.push({
        key,
        send: () => sendOrphanDelete(key, rev),
        fail: () => undefined,
        done: () => {
          orphanPending.delete(key)
          checkSettled()
        },
      })
    }
    void drain()
  }

  async function sendOrphanDelete(key: string, rev: number): Promise<Finish> {
    const s = sections.get(key)
    // Decided on the CURRENT state: still `push`, still the rev that was judged, still not here.
    if (schemaLock !== null || profileGone || blocked || direction() !== 'push') return WAIT
    if (s === undefined || s.sot.rev !== rev || !isUnrendered(key, s)) return WAIT
    if (!deps.isLeader()) {
      problem('not-leader', 'the lease is not ours: the write was not sent', key)
      return WAIT
    }
    orphanTried.add(key)
    const outcome = await deleteSection(hostId, profileId, key, { baseRev: rev, clientId: getClientId() }, requestOptions)
    if (disposed) return WAIT
    if (outcome.kind === 'applied') {
      // Our own delete: the WS echo is `own` and ignored, so observe it here.
      dispatch(key, { type: 'remote-event', rev: outcome.rev, hash: null, own: false }, false)
      return WAIT
    }
    if (outcome.kind === 'conflict') {
      problem('orphan-delete-conflict', `someone wrote it meanwhile (now rev ${outcome.rev}); left to the ordinary rules`, key)
      return WAIT
    }
    if (!blocks(outcome)) {
      if (outcome.reason !== 'aborted') orphanTried.delete(key) // nothing was decided: the next index may try again
      problem('orphan-delete-failed', `${outcome.reason} (${outcome.status}): ${outcome.message}`, key)
    }
    return WAIT
  }

  /** The end of the first reconciliation (see the header). */
  function checkSettled(): void {
    if (disposed || deps.onInitialSettled === undefined) return
    if (direction() === null) {
      settledReported = false
      return
    }
    if (settledReported || !indexSeen || profileGone || schemaLock !== null || blocked) return
    if (reindexing || draining || queue.length > 0 || orphanPending.size > 0 || heldPlaceholders.size > 0) return
    for (const [key, s] of sections) {
      if (isUnrendered(key, s) && !s.indexStale) continue
      if (!upToDate(key)) return
    }
    settledReported = true
    try {
      deps.onInitialSettled()
    } catch {
      // a listener's bug is not the driver's
    }
  }

  function pumpAll(): void {
    for (const key of [...sections.keys()]) pump(key)
  }

  function pumpTabs(): void {
    for (const key of [...sections.keys()]) if (sectionKind(key) === 'tabs') pump(key)
  }

  function backingOff(key: string): boolean {
    const until = notBefore.get(key)
    return until !== undefined && now() < until
  }

  function clearBackoff(key: string): void {
    failures.delete(key)
    notBefore.delete(key)
    const timer = retryTimers.get(key)
    if (timer !== undefined) clearTimeout(timer)
    retryTimers.delete(key)
  }

  /** The next backoff step of `key`. */
  function failed(key: string): Finish {
    const n = failures.get(key) ?? 0
    failures.set(key, n + 1)
    return { how: 'retry', ms: backoffMs(n) }
  }

  function armRetry(key: string, ms: number): void {
    const pending = retryTimers.get(key)
    if (pending !== undefined) clearTimeout(pending)
    notBefore.set(key, now() + ms)
    retryTimers.set(
      key,
      setTimeout(() => {
        retryTimers.delete(key)
        notBefore.delete(key)
        pump(key)
      }, ms),
    )
  }

  function settleManual(): void {
    if (manual && running.size === 0 && queue.length === 0 && !draining && !reindexing) manual = false
  }

  function run(key: string, body: () => Promise<Finish>): void {
    running.add(key)
    void body()
      .catch((err: unknown): Finish => {
        if (!disposed) problem('executor-error', message(err), key)
        return failed(key)
      })
      .then((finish) => {
        if (disposed) return
        running.delete(key)
        const pumpedMeanwhile = repump.delete(key)
        if (finish.how === 'retry') armRetry(key, finish.ms)
        // The state machine may have a next step; a pump respects the backoff just armed.
        if (finish.how === 'again' || pumpedMeanwhile) pump(key)
        // only now is a gate "not running": the `tabs.*` pulls it held back may go
        if (GATES.includes(key)) pumpTabs()
        const s = sections.get(key)
        if (s !== undefined) forgetIfGone(key, s)
        settleManual()
        if (GATES.includes(key)) sweepOrphans()
        checkSettled()
      })
  }

  /** `tabs.*` only: `hosts` and `workspaces` are UP TO DATE, and the workspace is here.
   *  `status === 'synced'` alone is not that: it means clean, and a clean section
   *  that is behind the SOT (its pull decided or still out) reads `synced` too. */
  function mayPull(key: string): boolean {
    if (sectionKind(key) !== 'tabs') return true
    for (const gate of GATES) {
      const s = sections.get(gate)
      if (s === undefined || s.status !== 'synced' || s.indexStale || sotMoved(s) || running.has(gate)) return false
    }
    const id = workspaceIdOf(key)
    return id !== null && localWorkspaceIds().includes(id)
  }

  function pump(key: string): void {
    if (disposed || profileGone) return
    if (running.has(key)) {
      repump.add(key)
      return
    }
    const s = sections.get(key)
    if (s === undefined) return
    const action = decideSection(s, { reachable: deps.isReachable(), autoSync: manual || deps.autoSync() })
    if (action.do !== 'restore-local') parkedRestore.delete(key)
    switch (action.do) {
      case 'nothing':
        return
      case 'lock-conflict':
        dispatch(key, { type: 'locked', reason: 'conflict' })
        return
      case 'lock-reset':
        dispatch(key, { type: 'locked', reason: 'reset' })
        return
      case 'restore-local': {
        const hash = action.hash
        if (backingOff(key) || (hash !== null && parkedRestore.get(key) === hash)) return
        run(key, () => restoreLocal(key, hash))
        return
      }
      case 'reindex':
        requestReindex()
        return
      case 'pull':
        // §4.4: `locked:schema` is the whole profile — nothing is pulled either.
        if (schemaLock !== null || blocked || backingOff(key) || !mayPull(key)) return
        run(key, () => pull(key))
        return
      case 'push':
      case 'delete': {
        if (schemaLock !== null || blocked || backingOff(key)) return
        // The first reconciliation, `pull`: nothing of a workspace's goes out before the SOT's `workspaces` is in.
        // Nor of a workspace the pull has just removed (the collector's `null` for it is 500 ms away).
        if (sectionKind(key) === 'tabs' && action.do === 'push' && direction() === 'pull') {
          const id = workspaceIdOf(key)
          if (!gatesUpToDate() || id === null || !localWorkspaceIds().includes(id)) return
        }
        const token = action.token
        run(key, () => new Promise<Finish>((done) => enqueueWrite(key, token, done)))
        return
      }
    }
  }

  /* ─── reindex ─── */

  function requestReindex(force = false): void {
    if (disposed || profileGone || blocked) return
    if (force) reindexForced = true
    if (reindexing || now() < reindexNotBefore) return
    reindexing = true
    reindexForced = false
    void reindex()
      .catch((err: unknown) => {
        if (!disposed) problem('executor-error', message(err))
      })
      .then(() => {
        if (disposed) return
        reindexing = false
        if (profileGone) return
        pumpAll()
        if (reindexForced) requestReindex()
        settleManual()
        sweepOrphans()
        checkSettled()
      })
  }

  async function reindex(): Promise<void> {
    const mine = await shapes()
    if (disposed) return
    // Read when the request is SENT (sync-state driver contract).
    const epochs = new Map([...sections].map(([key, s]) => [key, s.indexEpoch]))
    const result = await listProfiles(hostId, requestOptions)
    if (disposed) return

    if (result.kind === 'failed') {
      // No event at all: the sections stay stale and ask again.
      if (blocks(result)) return
      const ms = backoffMs(reindexFailures)
      reindexFailures += 1
      reindexNotBefore = now() + ms
      reindexTimer = setTimeout(() => {
        reindexTimer = null
        reindexNotBefore = 0
        if (reindexForced) requestReindex()
        pumpAll()
      }, ms)
      problem('reindex-failed', `${result.reason} (${result.status}); retry in ${ms} ms`)
      return
    }
    reindexFailures = 0

    const entry = result.value.find((p) => p.id === profileId)
    if (entry === undefined) {
      profileGone = true
      clearTimers()
      problem('profile-gone', `profile ${profileId} is not on the host any more; nothing was applied and nothing was dropped`)
      emitStatus()
      return
    }

    setSchemaLock(profileLock(entry.sections, mine))

    const listed = new Map(entry.sections.map((m) => [m.section, m]))
    for (const [key, epoch] of epochs) {
      if (!sections.has(key)) continue
      const m = listed.get(key)
      dispatch(key, { type: 'sot-index', epoch, entry: m === undefined ? null : { rev: m.rev, hash: m.hash } }, false)
    }
    // A section another client added. Unknown kinds are carried, never rewritten (§4.6.3).
    for (const m of entry.sections) {
      if (epochs.has(m.section) || sections.has(m.section) || sectionKind(m.section) === null) continue
      dispatch(m.section, { type: 'sot-index', epoch: initialSectionState(null).indexEpoch, entry: { rev: m.rev, hash: m.hash } }, false)
    }

    indexSeen = true
    settleHeldPlaceholders()
    reportSectionSet(entry.sections.map((m) => m.section))
  }

  /* ─── collector reports ─── */

  function deliver(r: SectionReport): void {
    if (r.hash !== null) stash.set(r.hash, r.payload)
    if (dispatch(r.key, { type: 'local-changed', hash: r.hash })) return
    // Nothing changed — but a payload may just have arrived that a push was waiting for.
    pruneMemoryStash()
    pump(r.key)
  }

  /** `'edit'` — an ordinary report · `'not-arrived'` — drop it, the SOT's content
   *  is on its way · `'unknown'` — the index has not landed, hold it. */
  function judgePlaceholder(r: SectionReport): 'edit' | 'not-arrived' | 'unknown' {
    if (sectionKind(r.key) !== 'tabs' || !isEmptyTabs(r.payload)) return 'edit'
    const s = sections.get(r.key) // none = `initialSectionState`: no base, index never seen
    if (s === undefined) return 'unknown'
    if (s.base.hash !== null) return 'edit'
    if (s.sot.hash !== null) return 'not-arrived'
    return s.indexStale ? 'unknown' : 'edit'
  }

  function online(): boolean {
    return deps.isReachable() && (manual || deps.autoSync())
  }

  function receive(r: SectionReport): void {
    const verdict = judgePlaceholder(r)
    heldPlaceholders.delete(r.key) // whatever was held, this report is newer
    if (verdict === 'edit') return deliver(r)
    if (verdict === 'not-arrived') return pump(r.key)
    heldPlaceholders.set(r.key, r)
    // A section with no state asks nobody for an index: ask on its behalf.
    if (online()) requestReindex()
  }

  /** The index has landed: judge what was held. Still unknown (its answer was discarded) → keep holding. */
  function settleHeldPlaceholders(): void {
    for (const [key, r] of [...heldPlaceholders]) {
      if (sections.get(key)?.indexStale === true) continue
      heldPlaceholders.delete(key)
      const s = sections.get(key)
      // no state = the index does not list it = the SOT has nothing
      if (s === undefined || judgePlaceholder(r) !== 'not-arrived') deliver(r)
    }
  }

  /** `create` / `remove` need no action here (see the report of Task 9): the
   *  collector reports a new workspace's `tabs.<id>` and a removed one's `null`,
   *  and the section's own state machine pushes / deletes. What is left is to
   *  make the two "carried" sets visible. */
  function reportSectionSet(sotKeys: string[]): void {
    try {
      const localKeys = [...sections].filter(([, s]) => s.currentHash !== null).map(([key]) => key)
      const set = reconcileSectionSet({ workspaceIds: localWorkspaceIds(), previousWorkspaceIds, localKeys, sotKeys })
      if (set.keepUnrendered.length > 0) problemOnce(`unrendered:${set.keepUnrendered.join(',')}`, 'sections-unrendered', set.keepUnrendered.join(', '))
      if (set.unknown.length > 0) problemOnce(`unknown:${set.unknown.join(',')}`, 'sections-unknown-kind', set.unknown.join(', '))
    } catch (err) {
      problemOnce('reconcile', 'reconcile-failed', message(err))
    }
  }

  /** `unauthorized` / `unknown-host`: retrying cannot help until the connection is re-made. */
  function blocks(f: Failure): boolean {
    if (f.reason !== 'unauthorized' && f.reason !== 'unknown-host') return false
    blocked = true
    problem(f.reason, `${f.message}; no request until the host reconnects or a manual sync`)
    return true
  }

  /* ─── push / delete ─── */

  function enqueueWrite(key: string, token: FlightToken, done: (f: Finish) => void): void {
    queue.push({ key, send: () => send(key, token), fail: () => closeFlight(key, token), done })
    void drain()
  }

  async function drain(): Promise<void> {
    if (draining) return
    draining = true
    try {
      while (queue.length > 0 && !disposed) {
        const job = queue.shift()!
        let finish: Finish
        try {
          finish = await job.send()
        } catch (err) {
          if (disposed) return
          problem('executor-error', message(err), job.key)
          job.fail()
          finish = failed(job.key)
        }
        if (disposed) return
        job.done(finish)
      }
    } finally {
      draining = false
    }
    checkSettled()
  }

  function closeFlight(key: string, token: FlightToken): void {
    if (sections.get(key)?.inFlight === token) dispatch(key, { type: 'push-failed' }, false)
  }

  async function send(key: string, token: FlightToken): Promise<Finish> {
    const mine = await shapes()
    if (disposed) return WAIT
    // Nothing is open yet, so nothing is owed.
    if (schemaLock !== null || profileGone || blocked) return WAIT

    dispatch(key, { type: 'push-started', token }, false)
    // The reducer takes a token only from the state it was decided in. Same OBJECT, or nothing goes out.
    if (sections.get(key)?.inFlight !== token) return AGAIN

    if (!deps.isLeader()) {
      dispatch(key, { type: 'push-failed' }, false)
      problem('not-leader', 'the lease is not ours: the write was not sent', key)
      return failed(key)
    }

    let outcome: PutOutcome | DeleteOutcome
    if (token.kind === 'put') {
      const kind = sectionKind(key)
      const payload = token.hash === null ? undefined : stash.get(token.hash)
      if (kind === null || token.hash === null || !isPayload(payload)) {
        dispatch(key, { type: 'push-failed' }, false)
        problem('push-payload-missing', `no payload is held for ${String(token.hash)}`, key)
        return failed(key)
      }
      const body = { clientId: getClientId(), baseRev: token.baseRev, hash: token.hash, fingerprint: mine[kind].fingerprint, ordinal: mine[kind].ordinal, payload }
      outcome = await putSection(hostId, profileId, key, body, requestOptions)
    } else {
      outcome = await deleteSection(hostId, profileId, key, { baseRev: token.baseRev, clientId: getClientId() }, requestOptions)
    }
    if (disposed) return WAIT

    // Exactly one terminal event per outcome.
    switch (outcome.kind) {
      case 'applied':
        clearBackoff(key)
        dispatch(key, { type: 'push-applied', rev: outcome.rev }, false)
        return AGAIN
      case 'converged':
        clearBackoff(key)
        dispatch(key, { type: 'push-converged', rev: outcome.rev }, false)
        return AGAIN
      case 'conflict':
        clearBackoff(key)
        // before the event: if it locks, `retainedHashes` keeps this payload
        if (outcome.hash !== null && outcome.payload !== null) stash.set(outcome.hash, outcome.payload)
        dispatch(key, { type: 'push-conflict', rev: outcome.rev, hash: outcome.hash }, false)
        return AGAIN
      case 'schema': {
        // Set before this function returns, i.e. before the next write is dequeued.
        const kind = sectionKind(key) ?? 'tabs'
        const sot: Shape = { fingerprint: outcome.fingerprint, ordinal: outcome.ordinal }
        const verdict = compareShape(mine[kind], sot)
        setSchemaLock({
          section: key,
          kind,
          verdict: verdict === 'sot-is-newer' || verdict === 'shape-changed-without-ordinal' ? verdict : mine[kind].ordinal < sot.ordinal ? 'sot-is-newer' : 'shape-changed-without-ordinal',
          mine: mine[kind],
          sot,
        })
        dispatch(key, { type: 'push-failed' }, false)
        return WAIT
      }
      case 'failed': {
        dispatch(key, { type: 'push-failed' }, false)
        if (blocks(outcome)) return WAIT
        if (outcome.reason === 'contended') return { how: 'retry', ms: outcome.retryAfterMs ?? DEFAULT_CONTENDED_MS }
        problem('push-failed', `${outcome.reason} (${outcome.status}): ${outcome.message}`, key)
        // 404 on a write = the profile is unknown: only the list can say so.
        if (outcome.reason === 'not-found') requestReindex(true)
        return failed(key)
      }
    }
  }

  /* ─── pull ─── */

  async function pull(key: string): Promise<Finish> {
    const result = await getSection(hostId, profileId, key, requestOptions)
    if (disposed) return WAIT
    if (result.kind === 'failed') {
      if (blocks(result)) return WAIT
      problem('pull-failed', `${result.reason} (${result.status}): ${result.message}`, key)
      return failed(key)
    }

    // Decided on the CURRENT state, not on the one that asked.
    const s = sections.get(key)
    if (s === undefined || profileGone || schemaLock !== null || !canApplyPull(s)) return AGAIN
    if (!mayPull(key)) return WAIT

    const fetched = result.value
    if (fetched === null && s.sot.hash !== null) {
      // 404 is "tombstone" AND "unknown profile" alike. The index said LIVE, so
      // this is not applied as a deletion on the word of a 404: ask the list.
      problem('pull-absent-but-listed', 'the section answered 404 while the index lists it; asking the index again', key)
      requestReindex(true)
      return failed(key)
    }
    const payload = fetched === null ? null : fetched.payload
    const rev = fetched === null ? s.sot.rev : fetched.rev

    let outcome: ApplyOutcome
    try {
      outcome = await applySectionToStores(key as ProfileSectionKey, payload, { masterHostId: hostId })
    } catch (err) {
      if (disposed) return WAIT
      problem('apply-threw', message(err), key)
      return failed(key)
    }
    if (disposed) return WAIT

    if (!outcome.ok) {
      if (outcome.reason === 'busy') return { how: 'retry', ms: BUSY_RETRY_MS }
      problem('pull-invalid', outcome.detail, key)
      // Refused (same reference back) = the verdict was stale: decide again.
      dispatch(key, { type: 'locked', reason: 'invalid', rev }, false)
      return afterPull(key)
    }
    if (payload !== null && outcome.hash === null) {
      // `unrendered`: nothing was written, so nothing was applied. `pull-applied`
      // would record "the SOT holds nothing" and the next local edit would be a
      // create over a live section.
      problemOnce(`unrendered-pull:${key}`, 'pull-unrendered', 'the workspace of this section is not here (yet); the pull waits for it', key)
      return WAIT
    }
    reportedOnce.delete(`unrendered-pull:${key}`)
    // TWO hashes (sync-state, `pull-applied`): the SOT's goes into the base, the
    // one rebuilt from the stores into `currentHash`.
    const sotHash = fetched === null ? null : fetched.hash
    const mismatch = outcome.hash !== sotHash
    if (mismatch) {
      problem('pull-hash-mismatch', `the stores did not keep what arrived (fetched ${String(sotHash)}, they hold ${String(outcome.hash)}): the section is dirty and will be pushed back`, key)
    }
    if (key === 'workspaces') previousWorkspaceIds = localWorkspaceIds()
    if (!dispatch(key, { type: 'pull-applied', rev, hash: sotHash, localHash: outcome.hash }, false)) {
      problem('pull-applied-refused', 'the section changed while the payload was being applied', key)
    }
    // The push that follows needs the payload of what the stores hold, and only
    // the collector has it: its report of this very apply pumps the section.
    if (mismatch && outcome.hash !== null && !stash.has(outcome.hash)) {
      clearBackoff(key)
      return WAIT
    }
    return afterPull(key)
  }

  /** What was served may be OLDER than the SOT this section knows of (the event
   *  ran ahead of the read): the section then still wants a pull — of the same
   *  bytes, if asked at once. That is a retry, not a next step: deciding again
   *  immediately would spin on the daemon. */
  function afterPull(key: string): Finish {
    const s = sections.get(key)
    if (s !== undefined && decideSection(s, { reachable: true, autoSync: true }).do === 'pull') return failed(key)
    clearBackoff(key)
    return AGAIN
  }

  /* ─── restore-local ─── */

  async function restoreLocal(key: string, hash: string | null): Promise<Finish> {
    let payload: unknown = null
    if (hash !== null) {
      payload = stash.get(hash) ?? getStash(profileId, hash)
      if (!isPayload(payload)) {
        // NOT `local-restored`: that would claim a restore that did not happen.
        parkedRestore.set(key, hash)
        problem('restore-payload-missing', `the snapshot ${hash} chosen with keep-local is in neither stash; edit the section to move on`, key)
        return WAIT
      }
      stash.set(hash, payload) // the push that follows sends this very payload
    }
    const s = sections.get(key)
    if (s === undefined || !canRestoreLocal(s, hash)) return AGAIN

    let outcome: ApplyOutcome
    try {
      outcome = await applySectionToStores(key as ProfileSectionKey, payload, { masterHostId: hostId })
    } catch (err) {
      if (disposed) return WAIT
      problem('apply-threw', message(err), key)
      return failed(key)
    }
    if (disposed) return WAIT
    if (!outcome.ok) {
      if (outcome.reason === 'busy') return { how: 'retry', ms: BUSY_RETRY_MS }
      problem('restore-invalid', outcome.detail, key)
      return failed(key)
    }
    clearBackoff(key)
    if (key === 'workspaces') previousWorkspaceIds = localWorkspaceIds()
    dispatch(key, { type: 'local-restored', hash }, false)
    return AGAIN
  }

  /* ─── lifecycle ─── */

  function clearTimers(): void {
    for (const timer of retryTimers.values()) clearTimeout(timer)
    retryTimers.clear()
    notBefore.clear()
    if (reindexTimer !== null) clearTimeout(reindexTimer)
    reindexTimer = null
    reindexNotBefore = 0
  }

  /** A fresh start for every retry: the connection is new, or the user asked. */
  function resetRetries(): void {
    blocked = false
    clearTimers()
    failures.clear()
    reindexFailures = 0
  }

  // Startup: what the section store holds, conflicts included (sync-state driver contract).
  for (const [key, persisted] of Object.entries(loadSectionStore(profileId).sections)) {
    const s = restoreSectionState(persisted)
    sections.set(key, s)
    attempted.set(key, persistedSignature(s))
    if (persisted.conflict !== undefined) storedConflict.add(key)
  }
  lastStatus = JSON.stringify(status())

  return {
    onSection(r) {
      if (disposed) return
      receive(r)
    },
    onRemoteEvent(e) {
      if (disposed || e.hostId !== hostId || e.profileId !== profileId) return
      if (sectionKind(e.section) === null) return // a kind this client does not know: carried, never rewritten
      dispatch(e.section, { type: 'remote-event', rev: e.rev, hash: e.hash, own: e.writerClientId === getClientId() })
    },
    onReconnected() {
      if (disposed) return
      resetRetries()
      // EVERY section first, the index request after: its epochs are read post-`reconnected`.
      for (const key of [...sections.keys()]) dispatch(key, { type: 'reconnected' }, false)
      answerStandingLocks()
      pumpAll()
      if (heldPlaceholders.size > 0 && online()) requestReindex()
    },
    syncNow() {
      if (disposed) return
      resetRetries()
      manual = true
      pumpAll()
      if (heldPlaceholders.size > 0 && online()) requestReindex()
      settleManual()
    },
    resolve(section, keep) {
      if (disposed || !sections.has(section)) return
      dispatch(section, { type: 'resolved', keep })
    },
    status,
    dispose() {
      if (disposed) return
      disposed = true
      abort.abort()
      clearTimers()
      queue.length = 0
    },
  }
}
