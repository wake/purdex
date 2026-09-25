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
//   - A RETIRED SECTION (`hosts`, host ownership H3a-2; projections.ts
//     `RETIRED_SECTIONS`) NEVER ENTERS THE EXECUTOR. `dispatch` refuses its key
//     before any state exists — so no index entry, remote event, collector report
//     or `resolve` makes one, and no request ever names it; its persisted record is
//     dropped at startup and the stash pruned of its payloads (they held tokens).
//     It stays on the SOT, untouched, for older clients (spec §5.3).
//   - No `tabs.*` pull while `workspaces` is not UP TO DATE — clean, index
//     fresh, SOT not moved, no action of its own running — nor while its
//     workspace does not exist locally (the apply would be `unrendered`).
//   - `settings` WAITS FOR `workspaces`, IN BOTH DIRECTIONS. Its workspace-scoped
//     entries are built and applied for the master's workspace set only
//     (sections.ts, applier.ts), so the section means nothing apart from the
//     `workspaces` it was written against:
//       pull — applied while `workspaces` is behind, the entry of a workspace
//         that has not arrived is not written (no such master workspace here);
//         the stores then hash differently from the SOT, the section is dirty,
//         and it is PUSHED BACK WITHOUT THAT ENTRY — the setting is deleted on
//         the device that made it. So no `settings` pull while `workspaces` is
//         not UP TO DATE (judged again after the fetch, like every pull).
//       push — sent before the `workspaces` that lists a workspace created
//         here, the entry is an orphan to every other client, which removes it
//         the same way. So no `settings` push while `workspaces` is not UP TO
//         DATE either. With that, the SOT never holds a scoped entry ahead of
//         its workspace, and events reach a client in SOT order — which is what
//         makes the pull gate sufficient. (The cost: while `workspaces` is
//         locked or failing, no setting of any kind travels.)
//     An entry whose workspace is on NO client is an orphan an older build
//     pushed: it is dropped by the apply and pushed back without it, once.
//   - `hosts` IS NO GATE (host ownership H3a-2). Until H3 a `tabs.*` pull and
//     `settings` (both directions) also waited for `hosts`: a host id they name
//     had to arrive through it first. The host list is this device's now and
//     never arrives through the sync; a host id this device does not know is
//     kept as it is (host ownership H1), not branded or dropped.
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
//     EXCEPT DURING A `push` PERIOD (#1450). Every attach starts without a base,
//     so a workspace this device holds EMPTY — `unsorted` is a fixed id on every
//     device, and a Hosts tab is never in the payload — while the SOT's has tabs
//     looked like a placeholder: the report was dropped, the section stayed
//     clean + moved, and the SOT's tabs were PULLED over a device the user had
//     just told to replace the SOT. Under `push` the empty report is the edit it
//     is: dirty + moved → the conflict `push` answers keep-local → pushed empty.
//     The limit: a workspace a `workspaces` pull ADDED here during the period
//     (another client moved `workspaces` while it was open) is the very case
//     the rule is for — its tabs are 500 ms away — and stays a placeholder
//     (`pulledInThisPeriod`) — from the moment the apply BEGINS: it writes the
//     stores before it awaits, so while it is awaited only a workspace that was
//     here before it counts (`workspacesPullInFlight`). The held path is judged
//     by the same function.
//   - A `tabs.<id>` DELETION IS NEVER APPLIED BY A PULL. Its workspace still
//     being here means the `workspaces` change that removes it has not arrived
//     (two writes, two events, either order): the pull waits (backoff, the index
//     asked once) instead of emptying a live workspace and pushing the emptiness
//     back as an orphan. The section converges when the workspace goes: the
//     collector reports it gone, both sides are absent, the reducer folds that.
//   - THE SCHEMA LOCK HAS THREE ENTRANCES AND ONE EXIT. It is set by the index
//     (`profileLock`), by a PUT answered `schema`, and by a PULL whose fetched
//     section carries a newer / unorderable shape (a remote-event brings only
//     rev and hash, so the fetch is where a newer writer is first seen). All
//     three set the same profile-level lock: no push, no delete, no pull, for
//     any section. It is lifted only by an index that no longer offends — and
//     the index lists every live section's shape, so a lock set by a pull is
//     confirmed, not cleared, by the next reindex.
//   - AN OLDER SHAPE ON THE SOT (`i-am-newer`) IS PULLED LIKE ANY OTHER. The
//     worry is `applySettings`' rule that a field the payload lacks was cleared
//     on the sending side: would it wipe this build's new fields? It cannot, by
//     construction: a pull lands on a CLEAN section only, and clean means the
//     local payload — built with THIS build's projection — hashes to the base
//     agreed under the old one, i.e. the new fields hold nothing (`undefined`
//     members are dropped from payload and hash alike). A new field that holds
//     a value makes the section dirty → push (my shape replaces the stored one,
//     §4.5) or, if the SOT moved too, a conflict the user answers; and
//     keep-sot IS the user asking for the SOT's content. Overwriting the SOT
//     unasked instead would discard the edits other (older) devices made while
//     this one was being upgraded.
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
//     `workspaces` is up to date, nor once its workspace is no
//     longer here — its workspace may be about to be replaced, and the push
//     would plant an orphan only to delete it again. (An
//     empty SOT has nothing to take: `workspaces` is pushed by the ordinary
//     rules, becomes up to date, and the tabs follow.)
//   - ONE PERIOD PER EXECUTOR, and it never re-opens. An attach — to the same
//     master too — gets a NEW executor from the start layer (and cleared bases,
//     without which a direction means little: a section that is dirty against a
//     base it still holds is simply pushed, `pull` or not). An executor born
//     without a direction has no period at all. So a direction that is stored
//     once the period is over — the callee did not clear it, or some path set
//     it without rebuilding the driver — is STALE: it answers no lock (that
//     would be a silent overwrite hours later, in a direction nobody confirmed
//     for THAT conflict), it is reported (`stale-direction`) and handed back
//     through `onInitialSettled()` once per appearance so that it gets cleared.
//     The check runs at the end of every round, action or not.
//   - It ends when, with an index SEEN by this executor, every section is up to
//     date (clean, index fresh, SOT not moved, nothing in flight / forced /
//     to restore / running), the write queue is empty, no placeholder is held
//     and nothing is locked: `onInitialSettled()`; the start
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
import { getClientId } from '../client-identity'
import { deleteSection, getSection, listProfiles, putSection } from './api'
import type { DeleteOutcome, Failure, PutOutcome } from './api'
import { applySectionToStores } from './apply-to-stores'
import type { ApplyOutcome, InvalidReason } from './apply-to-stores'
import { upcastLegacySettings, upcastLegacyTabs } from './applier'
import type { SectionReport } from './collector'
import { hashSection, structuralKey } from './hash'
import { readMasterWorld } from './master-world'
import { compareShape, profileLock, profileStatus, reconcileSectionSet } from './profile-state'
import type { ProfileStatus, SchemaLock } from './profile-state'
import type { ProfileRemoteEvent } from './profile-ws-dispatch'
import { isRetiredSection, sectionKind, shapeTable, workspaceIdOf } from './projections'
import { isSyncableWorkspaceId } from './sections'
import { dropSection, dropStash, getStash, loadSectionStore, pruneStash, saveConflict, saveSection } from './section-store'
import type { PersistedSection } from './section-store'
import { canApplyPull, canRestoreLocal, decideSection, initialSectionState, reduceSection, restoreSectionState, retainedHashes, sotMoved } from './sync-state'
import type { FlightToken, SectionConflict, SectionEvent, SectionStatus, SectionSyncState } from './sync-state'
import type { ProfileSectionKey, SectionKind, Shape, TabsPayload } from './types'

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
  /**
   * One section as the collector would build it NOW (collector.ts `buildSectionPayload`): `{ payload }`, or `null`
   * while nobody can say what the stores hold. Synchronous. Asked right before a pull stashes the payload its apply
   * hashed (#1369 critic): only a payload the stores still build is stashed. Absent = never stashed — the push then
   * waits for the collector's report, as it did before #1369.
   */
  buildNow?: (key: string) => { payload: unknown | null } | null
}

/**
 * What a LOCKED section looks like to whoever answers the lock — copies of the reducer's fields, nothing derived.
 * A `resolve` from the UI is bound to ALL of it (sync-status.ts): every one of these can move while the status
 * string stands still, and "keep local" then pushes content — or overwrites a SOT — that nobody confirmed.
 */
export interface SectionLock {
  status: Extract<SectionStatus, `locked:${string}`>
  /** The live local payload: what "keep local" would push. */
  currentHash: string | null
  /** The newest SOT known: what "keep local" rebases on. `locked:reset` and `locked:invalid` have no pair — this is their SOT side. */
  sot: { rev: number; hash: string | null }
  /** `locked:conflict` only. */
  conflict: SectionConflict | null
}

/** What the page shows of one section beyond its status string (P3d-4a). Every field is there for every section. */
export interface SectionDetail {
  /** `base.rev`: the rev this device and the host last AGREED on. null = nothing was ever agreed. */
  rev: number | null
  /** Consecutive failed requests for this section; 0 = none. */
  failures: number
  /** When the next attempt is armed (the executor's clock, ms); null = none is armed. */
  retryAt: number | null
  /** WHY this device refuses the host's copy — the apply's code (apply-to-stores.ts), never its text. Non-null only
   *  while the section is `locked:invalid` (P3d-4b). */
  invalidReason: InvalidReason | null
}

export interface ExecutorStatus {
  profile: ProfileStatus
  schemaLock: SchemaLock | null
  sections: Record<string, SectionStatus>
  /**
   * Every locked section, and only those. Part of the status, and therefore of `onStatus`'s "changed": a follower
   * window's UI sends back the lock it rendered (P3 plan Task 2), so it has to hear when one moves.
   */
  locks: Record<string, SectionLock>
  /** The index answered without this profile (then `profile` is `locked:reset`). start.ts's 404 status says so too. */
  profileGone: boolean
  /** One per key of `sections`. Every change of a counter in it emits (P3 plan, P3d-4 R3). */
  detail: Record<string, SectionDetail>
  /** Consecutive failed index reads; 0 = none. */
  indexFailures: number
  /** The last time an answer FROM THE HOST (an index read, a pull, a push outcome) left the profile `synced`. A
   *  local edit does not move it, nor does a failure. null = not since this executor started. */
  lastSuccessAt: number | null
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
/** A deleted `tabs.<id>` whose workspace stays: said once after this many retries. */
const STUCK_DELETION_ATTEMPTS = 4
/** The sections every `tabs.*` pull waits for (see the header). Not `hosts`: it is retired (host ownership H3a-2). */
const GATES: readonly string[] = ['workspaces']
/** The sections `settings` waits for, pull and push (see the header): `workspaces`, for its scoped entries. Its
 *  release pumps it (`pumpGatedBy`). Not `hosts` any more (host ownership H3a-2). */
const SETTINGS_GATES: readonly string[] = ['workspaces']

/** What must be up to date before `key` is pulled. */
function pullGatesOf(key: string): readonly string[] {
  const kind = sectionKind(key)
  return kind === 'tabs' ? GATES : kind === 'settings' ? SETTINGS_GATES : []
}

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

/**
 * The MASTER world's workspace ids — from the parked master while a local profile is on screen — or `null` while
 * nobody can say where that world is (master-world.ts). `null` is never "no workspaces": every caller below turns
 * it into "do nothing yet". Read as an empty list it would make every `tabs.*` of the SOT look unrendered, and
 * under `push` the orphan sweep would DELETE them.
 */
function localWorkspaceIds(): string[] | null {
  const read = readMasterWorld()
  return read.settled ? read.world.workspaces.map((w) => w.id) : null
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
  /** The code of the last `invalid` verdict per section; read only while that section IS `locked:invalid`. */
  const invalidReasons = new Map<string, InvalidReason>()
  const retryTimers = new Map<string, Timer>()
  /** `restore-local` whose payload is in neither stash, by the hash that is missing. */
  const parkedRestore = new Map<string, string>()

  /** Empty `tabs.*` placeholders reported before the index landed: the latest per key (see the header). */
  const heldPlaceholders = new Map<string, SectionReport>()
  /** Workspaces a `workspaces` pull ADDED here during this executor's life (#1450): their empty `tabs.<id>` stays a
   *  placeholder under `push` too (see the header). Per executor, never persisted — every attach is a new period. */
  const pulledInThisPeriod = new Set<string>()
  /** A `workspaces` pull's apply is being awaited: the ids here BEFORE it began ('all' = the world was unsettled). */
  let workspacesPullInFlight: Set<string> | 'all' | null = null

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
  /** This executor's ONE period is over (or never was: born without a direction). Never goes back. */
  let periodOver = false
  /** A direction seen after the period: reported and handed back once per appearance. */
  let staleNotified = false
  /** Orphan deletes queued or out, and the ones already tried (never again by this executor). */
  const orphanPending = new Set<string>()
  const orphanTried = new Set<string>()
  /** Not sent, or sent and failed without an answer: not again before the next index (a sweep runs every round). */
  const orphanDeferred = new Set<string>()

  let shapesPromise: Promise<Shapes> | null = null
  let previousWorkspaceIds = localWorkspaceIds() ?? []
  let lastStatus = ''
  let lastSuccessAt: number | null = null
  /** When the leader last handled an answer from the host — whatever the profile was then (see `successAt`). */
  let lastAnswerAt: number | null = null

  /* ─── the one door every request goes through ─── */

  /**
   * `null` = the host is not reachable NOW: the request is not made. `decideSection`
   * asks `isReachable()` when an action is chosen, but between that and the
   * request lie `await shapes()`, the write queue, a backoff timer — and the start
   * layer makes this executor unreachable WITHOUT disposing it (the connection
   * dropped; the attachment is not confirmed). So the question is asked again
   * here, where the request is made, for all five of them: list, get, put,
   * delete, orphan delete. The caller neither dispatches nor retries on `null`
   * (no busy wait): `onReconnected()` pumps everything again. A flight that was
   * already opened is closed (`push-failed`).
   */
  function request<T>(call: () => Promise<T>): Promise<T> | null {
    return deps.isReachable() ? call() : null
  }

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

  function lockOf(s: SectionSyncState): SectionLock | null {
    if (s.status !== 'locked:conflict' && s.status !== 'locked:reset' && s.status !== 'locked:invalid') return null
    return {
      status: s.status,
      currentHash: s.currentHash,
      sot: { rev: s.sot.rev, hash: s.sot.hash },
      conflict: s.conflict === null ? null : { localHash: s.conflict.localHash, sot: { rev: s.conflict.sot.rev, hash: s.conflict.sot.hash } },
    }
  }

  function profileNow(): ProfileStatus {
    return profileGone ? 'locked:reset' : profileStatus({ hasMaster: true, sections: Object.fromEntries(sections), lock: schemaLock })
  }

  function detailOf(key: string, s: SectionSyncState): SectionDetail {
    // `{0, null}` is "nothing yet" (sync-state): not a rev anybody agreed on.
    const rev = s.base.rev === 0 && s.base.hash === null ? null : s.base.rev
    const invalidReason = s.status === 'locked:invalid' ? (invalidReasons.get(key) ?? null) : null
    return { rev, failures: failures.get(key) ?? 0, retryAt: notBefore.get(key) ?? null, invalidReason }
  }

  function status(): ExecutorStatus {
    return {
      profile: profileNow(),
      schemaLock,
      sections: Object.fromEntries([...sections].map(([key, s]) => [key, s.status])),
      locks: Object.fromEntries(
        [...sections].flatMap(([key, s]) => {
          const lock = lockOf(s)
          return lock === null ? [] : [[key, lock]]
        }),
      ),
      profileGone,
      detail: Object.fromEntries([...sections].map(([key, s]) => [key, detailOf(key, s)])),
      indexFailures: reindexFailures,
      lastSuccessAt: successAt(),
    }
  }

  /**
   * R4: the time of the last host answer after which the profile is `synced`. The profile does not always become
   * `synced` ON an answer: a pull that removes a workspace leaves that workspace's `tabs.*` dirty until the
   * collector reports it gone (both sides absent — agreement, no request), so the first reconciliation of a pull
   * can end on a LOCAL report (P3d-4c F1). What is agreed then is what the host said in its last answer, so that
   * answer's time is the one latched, the moment the profile is seen `synced`. A local edit or a failure is no
   * answer: it moves nothing (an edit undone falls back on the same answer).
   */
  function successAt(): number | null {
    if (!disposed && lastAnswerAt !== null && profileNow() === 'synced') lastSuccessAt = lastAnswerAt
    return lastSuccessAt
  }

  /** An answer from the host has just been handled (see `successAt`). */
  function answered(): void {
    if (disposed) return
    lastAnswerAt = now()
    emitStatus()
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
    invalidReasons.delete(key)
    reportedOnce.delete(`stuck-deletion:${key}`)
  }

  /* ─── the loop ─── */

  /** `true` iff the state changed. A section nobody knows starts from `initialSectionState(null)`. */
  function dispatch(key: string, event: SectionEvent, pumpAfter = true): boolean {
    // A retired section never gets a state (see the header): the one door every path to `sections.set` goes through.
    if (disposed || isRetiredSection(key)) return false
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
      // `workspaces` gates every `tabs.*` pull and `settings`
      pumpGatedBy(key)
      checkSettled()
    }
    return true
  }

  /* ─── the first reconciliation ─── */

  /** The direction while THIS executor's period is open; null afterwards, whatever the store says (see the header). */
  function direction(): 'push' | 'pull' | null {
    return periodOver ? null : storedDirection()
  }

  function storedDirection(): 'push' | 'pull' | null {
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
    if (id === null) return true
    const ids = localWorkspaceIds()
    return ids !== null && !ids.includes(id) // unsettled: not known to be missing — no sweep, and the period stays open
  }

  /** `push` only: queue the delete of every unrendered `tabs.*`, once `workspaces` says what this machine has. */
  function sweepOrphans(): void {
    if (disposed || profileGone || schemaLock !== null || blocked || !indexSeen || direction() !== 'push') return
    if (!deps.isReachable() || !gatesUpToDate()) return
    let queued = false
    for (const [key, s] of [...sections]) {
      if (s.indexStale || s.inFlight !== null || running.has(key) || !isUnrendered(key, s)) continue
      if (orphanPending.has(key) || orphanTried.has(key) || orphanDeferred.has(key)) continue
      orphanPending.add(key)
      queued = true
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
    if (queued) void drain()
  }

  async function sendOrphanDelete(key: string, rev: number): Promise<Finish> {
    const s = sections.get(key)
    orphanDeferred.add(key) // until proven otherwise
    // Decided on the CURRENT state: still `push`, still the rev that was judged, still not here.
    if (schemaLock !== null || profileGone || blocked || direction() !== 'push') return WAIT
    if (s === undefined || s.sot.rev !== rev || !isUnrendered(key, s)) return WAIT
    if (!deps.isLeader()) {
      problem('not-leader', 'the lease is not ours: the write was not sent', key)
      return WAIT
    }
    const sent = request(() => deleteSection(hostId, profileId, key, { baseRev: rev, clientId: getClientId() }, requestOptions))
    if (sent === null) return WAIT // stays deferred: the next index tries again
    const outcome = await sent
    if (disposed) return WAIT
    if (outcome.kind === 'applied') {
      orphanDeferred.delete(key)
      // Our own delete: the WS echo is `own` and ignored, so observe it here.
      dispatch(key, { type: 'remote-event', rev: outcome.rev, hash: null, own: false }, false)
      answered()
      return WAIT
    }
    if (outcome.kind === 'conflict') {
      orphanTried.add(key)
      problem('orphan-delete-conflict', `someone wrote it meanwhile (now rev ${outcome.rev}); left to the ordinary rules`, key)
      answered()
      return WAIT
    }
    // nothing was decided: the next index may try again
    if (!blocks(outcome)) problem('orphan-delete-failed', `${outcome.reason} (${outcome.status}): ${outcome.message}`, key)
    return WAIT
  }

  function handBack(): void {
    try {
      deps.onInitialSettled?.()
    } catch {
      // a listener's bug is not the driver's
    }
  }

  /** The end of the first reconciliation (see the header). Runs at the end of EVERY round — one with no action too. */
  function checkSettled(): void {
    if (disposed) return
    if (periodOver) {
      // A direction that is (still, or again) stored belongs to no period of this
      // executor. It answers nothing here; say so and hand it back to be cleared.
      if (storedDirection() === null) staleNotified = false
      else if (!staleNotified) {
        staleNotified = true
        problem('stale-direction', 'a direction is stored but the first reconciliation of this driver is over; conflicts go to the user')
        handBack()
      }
      return
    }
    if (!indexSeen || profileGone || schemaLock !== null || blocked) return
    sweepOrphans() // `push`: what it queues keeps the period open (`orphanPending`)
    if (reindexing || draining || queue.length > 0 || orphanPending.size > 0 || orphanDeferred.size > 0 || heldPlaceholders.size > 0) return
    for (const [key, s] of sections) {
      if (isUnrendered(key, s) && !s.indexStale) continue
      if (!upToDate(key)) return
    }
    periodOver = true
    handBack()
    checkSettled() // not cleared by the callee → stale, from this moment
  }

  function pumpAll(): void {
    for (const key of [...sections.keys()]) pump(key)
    checkSettled()
  }

  function pumpTabs(): void {
    for (const key of [...sections.keys()]) if (sectionKind(key) === 'tabs') pump(key)
  }

  /** A gate moved: decide again for everything that waits for it. */
  function pumpGatedBy(key: string): void {
    if (GATES.includes(key)) pumpTabs()
    if (SETTINGS_GATES.includes(key)) pump('settings')
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
    emitStatus()
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
        emitStatus()
        pump(key)
      }, ms),
    )
    emitStatus()
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
        // only now is a gate "not running": what it held back may go
        pumpGatedBy(key)
        const s = sections.get(key)
        if (s !== undefined) forgetIfGone(key, s)
        settleManual()
        if (GATES.includes(key)) sweepOrphans()
        checkSettled()
      })
  }

  /** The section's gates are UP TO DATE (`tabs.*` and `settings`: `workspaces`), and a
   *  `tabs.*` has its workspace here. `status === 'synced'` alone is not that: it means clean, and a clean section
   *  that is behind the SOT (its pull decided or still out) reads `synced` too. */
  function mayPull(key: string): boolean {
    for (const gate of pullGatesOf(key)) {
      const s = sections.get(gate)
      if (s === undefined || s.status !== 'synced' || s.indexStale || sotMoved(s) || running.has(gate)) return false
    }
    if (sectionKind(key) !== 'tabs') return true
    const id = workspaceIdOf(key)
    return id !== null && (localWorkspaceIds()?.includes(id) ?? false)
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
          if (!gatesUpToDate() || id === null || !(localWorkspaceIds()?.includes(id) ?? false)) return
        }
        // A scoped entry must not reach the SOT ahead of the `workspaces` that lists its workspace (see the header).
        if (key === 'settings' && action.do === 'push' && !SETTINGS_GATES.every(upToDate)) return
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
    const listing = request(() => listProfiles(hostId, requestOptions))
    if (listing === null) return // no event, no retry timer: the sections stay stale and the next connect asks
    const result = await listing
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
        emitStatus()
        if (reindexForced) requestReindex()
        pumpAll()
      }, ms)
      problem('reindex-failed', `${result.reason} (${result.status}); retry in ${ms} ms`)
      emitStatus()
      return
    }
    reindexFailures = 0
    emitStatus()

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
    orphanDeferred.clear()
    settleHeldPlaceholders()
    reportSectionSet(entry.sections.map((m) => m.section))
    answered()
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
    if (s.sot.hash !== null) return pushReplaces(r.key) ? 'edit' : 'not-arrived'
    return s.indexStale ? 'unknown' : 'edit'
  }

  /** `push`: this device's (empty) workspace replaces the SOT's tabs — unless a pull brought the workspace in (#1450). */
  function pushReplaces(key: string): boolean {
    const id = workspaceIdOf(key)
    if (direction() !== 'push' || id === null || pulledInThisPeriod.has(id)) return false
    // a `workspaces` apply still awaited: only a workspace that was here before it began is this device's
    return workspacesPullInFlight === null || (workspacesPullInFlight !== 'all' && workspacesPullInFlight.has(id))
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
    const workspaceIds = localWorkspaceIds()
    if (workspaceIds === null) return // unsettled: nothing to compare the SOT's set with; the next index does
    try {
      const localKeys = [...sections].filter(([, s]) => s.currentHash !== null).map(([key]) => key)
      // A workspace whose id cannot form `tabs.<id>` is device-local (the builder leaves it out, the applier
      // keeps it): it has no section to reconcile, and `reconcileSectionSet` throws on such an id — which
      // used to cost the whole report.
      const set = reconcileSectionSet({
        workspaceIds: workspaceIds.filter(isSyncableWorkspaceId),
        previousWorkspaceIds: previousWorkspaceIds.filter(isSyncableWorkspaceId),
        localKeys,
        sotKeys,
      })
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

  /** The flight was opened and the host is not reachable: it is closed, nothing was sent, nothing is retried. */
  function unsent(key: string): Finish {
    dispatch(key, { type: 'push-failed' }, false)
    return WAIT
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
      const sent = request(() => putSection(hostId, profileId, key, body, requestOptions))
      if (sent === null) return unsent(key)
      outcome = await sent
    } else {
      const sent = request(() => deleteSection(hostId, profileId, key, { baseRev: token.baseRev, clientId: getClientId() }, requestOptions))
      if (sent === null) return unsent(key)
      outcome = await sent
    }
    if (disposed) return WAIT

    // Exactly one terminal event per outcome.
    switch (outcome.kind) {
      case 'applied':
        clearBackoff(key)
        dispatch(key, { type: 'push-applied', rev: outcome.rev }, false)
        answered()
        return AGAIN
      case 'converged':
        clearBackoff(key)
        dispatch(key, { type: 'push-converged', rev: outcome.rev }, false)
        answered()
        return AGAIN
      case 'conflict':
        clearBackoff(key)
        // before the event: if it locks, `retainedHashes` keeps this payload
        if (outcome.hash !== null && outcome.payload !== null) stash.set(outcome.hash, outcome.payload)
        dispatch(key, { type: 'push-conflict', rev: outcome.rev, hash: outcome.hash }, false)
        answered()
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
    const mine = await shapes() // before the request: nothing may `await` between judging the state and applying
    if (disposed) return WAIT
    const fetching = request(() => getSection(hostId, profileId, key, requestOptions))
    if (fetching === null) return WAIT
    const result = await fetching
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
    // A DELETED `tabs.<id>` WHOSE WORKSPACE IS STILL HERE is a `workspaces` change
    // that has not arrived yet (removing a workspace is two writes, and their
    // events come in either order). Applying it would empty a workspace the user
    // is looking at, and the emptiness would then be pushed back as a create —
    // an orphan on the SOT — only for `workspaces` to arrive and undo it all.
    // So: not applied, nothing dispatched. The index is asked once (it shows
    // `workspaces` moved even if that event was lost); when the workspace goes,
    // the collector reports this section gone, both sides are absent, and that
    // is agreement. `mayPull` only lets a `tabs.*` pull run while its workspace
    // is here, so a `tabs.*` deletion is in fact never applied by a pull.
    if (fetched === null && sectionKind(key) === 'tabs') {
      const attempts = failures.get(key) ?? 0
      if (attempts === 0) requestReindex(true)
      if (attempts >= STUCK_DELETION_ATTEMPTS) {
        problemOnce(`stuck-deletion:${key}`, 'tabs-deleted-workspace-kept', 'the SOT deleted this section but `workspaces` still lists its workspace; it is left as it is here', key)
      }
      return failed(key)
    }

    // THE SHAPE OF WHAT WAS FETCHED (§4.4 / §4.5). A client learns of a new rev
    // from a remote-event, which carries no fingerprint and no ordinal; the index
    // — where `profileLock` looks — is not asked again for it. The fetched
    // section is therefore the first place a newer shape shows up, and it is
    // judged BEFORE the apply: a newer (or unorderable) shape locks the whole
    // profile, exactly as the index would have. Not `locked:invalid` — that is
    // one section's business and leaves every other section writing, which is
    // the very thing an old client must stop doing.
    const kind = sectionKind(key)
    if (fetched !== null && kind !== null) {
      const sot: Shape = { fingerprint: fetched.fingerprint, ordinal: fetched.ordinal }
      const verdict = compareShape(mine[kind], sot)
      if (verdict === 'sot-is-newer' || verdict === 'shape-changed-without-ordinal') {
        setSchemaLock({ section: key, kind, verdict, mine: mine[kind], sot })
        return WAIT
      }
      // 'i-am-newer' is applied like 'ok' — see "AN OLDER SHAPE ON THE SOT" in the header.
    }
    const payload = fetched === null ? null : fetched.payload
    const rev = fetched === null ? s.sot.rev : fetched.rev

    // #1450: the apply writes the stores BEFORE it awaits (the hash of what it wrote), so a workspace it adds is on
    // screen — and its empty `tabs.<id>` can be reported — while this is still awaited. The guard is up before the
    // apply starts: until it is down, only a workspace that was here before counts as this device's. A world
    // unsettled before the apply guards every id — the safe side (a placeholder).
    if (key === 'workspaces') {
      const ids = localWorkspaceIds()
      workspacesPullInFlight = ids === null ? 'all' : new Set(ids)
    }
    let outcome: ApplyOutcome
    try {
      outcome = await applySectionToStores(key as ProfileSectionKey, payload, { masterHostId: hostId })
    } catch (err) {
      if (disposed) return WAIT
      problem('apply-threw', message(err), key)
      return failed(key)
    } finally {
      // Whatever the outcome (ok, refused, thrown, disposed): what appeared came from the SOT; the guard goes down.
      if (key === 'workspaces' && workspacesPullInFlight !== null) {
        const before = workspacesPullInFlight
        for (const id of localWorkspaceIds() ?? []) if (before === 'all' || !before.has(id)) pulledInThisPeriod.add(id)
        workspacesPullInFlight = null
      }
    }
    if (disposed) return WAIT

    if (!outcome.ok) {
      if (outcome.reason === 'busy') return { how: 'retry', ms: BUSY_RETRY_MS }
      problem('pull-invalid', outcome.detail, key)
      // Before the event: the status it emits carries the reason. A refused verdict leaves it unread (see `detailOf`).
      invalidReasons.set(key, outcome.code)
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
    // `rewrite` = the only difference is a designed write-back after a pull (one push, then every build agrees):
    // an ordinal-2 `tabs.*` without its interface-only tabs (tabs-local-only §3.5). Pushed like any mismatch, but
    // not a problem.
    if (mismatch && outcome.rewrite === undefined) {
      problem('pull-hash-mismatch', `the stores did not keep what arrived (fetched ${String(sotHash)}, they hold ${String(outcome.hash)}): the section is dirty and will be pushed back`, key)
    }
    if (key === 'workspaces') previousWorkspaceIds = localWorkspaceIds() ?? previousWorkspaceIds
    const taken = dispatch(key, { type: 'pull-applied', rev, hash: sotHash, localHash: outcome.hash }, false)
    if (!taken) problem('pull-applied-refused', 'the section changed while the payload was being applied', key)
    answered()
    // The push that follows needs the payload of what the stores hold. The apply hands it back (#1369): stashed
    // here, the push does not wait for the collector — which reports a hash only once, so a hash it reported
    // before (and this stash has since pruned) would never come again. `pruneMemoryStash` keeps it: it is the
    // section's `currentHash` now. Without a payload the collector's report of this very apply pumps the section.
    //
    // Only if the stores STILL build it, asked here and not in the apply (#1369 critic). The apply's payload is a
    // snapshot, and the stores move after the apply's last look at them: its operation lock is released in
    // `withOperationLock`'s `finally`, and that release synchronously runs the lock observer
    // (useMultiHostEventWs.ts) → `reconcileAfterLockRelease` → on some paths `runRevivePass`, which rewrites tab
    // layouts — all before the outcome gets here. A user edit landing in the apply's hash await is the same case.
    // Pushed, the stale snapshot would overwrite the SOT until the collector's report of the change pushes again.
    // So the check sits at the one place the payload is used, and nothing is awaited between it and the stash.
    // Anything but "the same canonical form" (moved, unsettled, a builder that throws, no `buildNow`) = no stash:
    // the collector reports what the stores hold now, and that is what goes out.
    if (outcome.hash !== null && outcome.payload !== undefined && storesStillBuild(key, outcome.payload)) stash.set(outcome.hash, outcome.payload)
    const awaitsCollector = mismatch && outcome.hash !== null && !stash.has(outcome.hash)
    if (awaitsCollector) {
      clearBackoff(key)
      return WAIT
    }
    return afterPull(key)
  }

  /** Do the stores, read NOW, build exactly `payload` for `key`? Synchronous; false whenever nobody can say. */
  function storesStillBuild(key: string, payload: unknown): boolean {
    if (deps.buildNow === undefined) return false
    try {
      const built = deps.buildNow(key)
      return built !== null && built.payload !== null && structuralKey(built.payload) === structuralKey(payload)
    } catch {
      return false
    }
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
      stash.set(hash, payload) // the push that follows sends this very payload — or its upcast, below
    }
    // P3e: a persisted conflict can hold, as its LOCAL side, a settings payload an
    // ordinal-3 build sent (newtab `profiles`). Pushed as-is it would reach the SOT
    // under THIS build's ordinal / fingerprint — a row whose payload does not match
    // its shape, until (and unless) a second PUT from the collector fixed it. So the
    // snapshot is brought to this build's shape here, once: it is what gets applied,
    // what `currentHash` becomes (`local-restored.localHash`) and what the one push sends.
    // tabs-local-only §3.5, the same for `tabs.*`: a snapshot an ordinal-2 build took holds this device's own
    // interface-only tabs; pushed as-is under ordinal 3 they would travel again. `upcastLegacyTabs` drops them.
    let restoredHash = hash
    const kind = sectionKind(key)
    if ((kind === 'settings' || kind === 'tabs') && payload !== null) {
      const upcast = kind === 'settings' ? upcastLegacySettings(payload) : upcastLegacyTabs(payload as TabsPayload)
      if (upcast !== payload) {
        try {
          restoredHash = await hashSection(upcast)
          payload = upcast
        } catch {
          restoredHash = hash // unhashable: the apply below refuses it anyway
        }
        if (disposed) return WAIT
      }
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
    if (key === 'workspaces') previousWorkspaceIds = localWorkspaceIds() ?? previousWorkspaceIds
    // Only now: before the event nothing retains `restoredHash`, and a prune while the apply was awaited would have dropped it.
    if (restoredHash !== hash && restoredHash !== null) stash.set(restoredHash, payload)
    dispatch(key, restoredHash === hash ? { type: 'local-restored', hash } : { type: 'local-restored', hash, localHash: restoredHash }, false)
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
    emitStatus()
  }

  // Startup: what the section store holds, conflicts included (sync-state driver contract).
  // A retired section's record is dropped instead (see the header), and first its OWN payloads — a `hosts` conflict's
  // hold every host's token. By name, never a prune: a prune here could remove a SENT payload another leader has just
  // written for a conflict whose record is not stored yet (section-store.ts header, spec §4.6.2). The record goes only
  // once every payload has; a refused removal keeps it, so the next start comes back here and tries again (#1425).
  // RESIDUAL (#1256 — localStorage has no transactions): a payload another leader has written under the SAME content
  // hash, for a conflict whose record is not stored yet, is not in this window's keep-set and IS removed. This build
  // never writes a `hosts` payload, so that hash can only come from a pre-H3 tab of the same browser, still open,
  // storing a `hosts` conflict at that moment; its conflict is then dropped at its next load, like any damaged one.
  const retired: Array<[string, PersistedSection]> = []
  for (const [key, persisted] of Object.entries(loadSectionStore(profileId).sections)) {
    if (isRetiredSection(key)) {
      retired.push([key, persisted])
      continue
    }
    const s = restoreSectionState(persisted)
    sections.set(key, s)
    attempted.set(key, persistedSignature(s))
    if (persisted.conflict !== undefined) storedConflict.add(key)
  }
  for (const [key, persisted] of retired) {
    const keep = keepSet() // a hash a live section holds too is that section's
    const own = [persisted.base.hash, persisted.currentHash, persisted.conflict?.localHash, persisted.conflict?.sot.hash]
    const hashes = [...new Set(own.filter((h): h is string => typeof h === 'string' && !keep.has(h)))]
    if (dropStash(profileId, hashes) !== 'ok') {
      problem('persist-failed', 'the section store refused to remove a retired section\'s payloads; its record is kept and the next start tries again', key)
      continue
    }
    if (dropSection(profileId, key) !== 'ok') problem('persist-failed', 'the section store refused to drop a retired section', key)
  }
    lastStatus = JSON.stringify(status())
  // Born without a direction = an ordinary run (a reload after the period): there is no period to open later.
  periodOver = storedDirection() === null

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
