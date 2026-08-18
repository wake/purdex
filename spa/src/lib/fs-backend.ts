import type { FileSource, FileStat, FileEntry } from '../types/fs'

export interface FsBackend {
  id: string
  label: string
  available(): boolean

  read(path: string): Promise<Uint8Array>
  write(path: string, content: Uint8Array): Promise<void>
  stat(path: string): Promise<FileStat>
  list(path: string): Promise<FileEntry[]>
  mkdir(path: string, recursive?: boolean): Promise<void>
  delete(path: string, recursive?: boolean): Promise<void>
  rename(from: string, to: string): Promise<void>
}

/**
 * Optional capability for the atomic unique-name reservation flow (#854). It is
 * an In-App-only concern: only `InAppBackend` has an atomic `add` primitive (IDB
 * `store.add` throws on a duplicate key) and only the In-App storage UI mints
 * `Untitled[-N]` / `New Folder[ N]` names this way. Keeping it OFF the base
 * `FsBackend` interface (codex H1) stops the Local/Daemon backends from having
 * to carry `not-supported` stubs that exist only to satisfy a contract no caller
 * exercises. Consumers narrow to this capability via `supports*` guards below.
 */
export interface SupportsUniqueCreate {
  /**
   * Atomically reserve a unique file under `dir`. Loops candidate names
   * `<baseName>`, `<baseName>-1`, … forming each path as `dir/<name>.<ext>`
   * (`ext` is bare — no leading dot) and reserves the first free key in a way
   * that is safe against concurrent callers (the single serialization point
   * that fixes the double-new-file shared-key race, #854). Returns the reserved
   * path.
   *
   * `ext` is a plain string (Phase 1c widened it from `'md' | 'txt'`) so OS-file
   * upload can reserve arbitrary extensions (`png` / `pdf` / `docx` / …). An
   * empty `ext` (an ext-less name like `README`) forms the path WITHOUT a
   * trailing dot. The optional `content` seeds the reserved file's bytes on the
   * same atomic `add` — so upload's unique-name reservation AND its byte write
   * happen as one operation (no overwrite race). It defaults to empty, so the 1b
   * 3-arg callers (which mint empty `Untitled[-N]` files) are unaffected.
   */
  createUnique(
    dir: string,
    baseName: string,
    ext: string,
    content?: Uint8Array,
  ): Promise<string>
  /**
   * Atomically reserve a unique empty DIRECTORY under `dir`. Loops candidate
   * names `<baseName>`, `<baseName> 1`, … (space-separated suffix, no extension)
   * and reserves the first free key via the same single serialization point as
   * `createUnique` — so a rapid double "New Folder" click cannot clobber an
   * existing folder (#854-class race). Returns the reserved path.
   */
  mkdirUnique(dir: string, baseName?: string): Promise<string>
}

/**
 * Optional capability: notify on every tree-mutating commit (Phase 2b). Only the
 * In-App backend implements it — it is what the persistent backup auto-trigger
 * subscribes to so editing a `/buffer` file (even with the Storage pane closed)
 * schedules a debounced backup. Additive and off the base `FsBackend` so the
 * Local/Daemon backends carry no stub; consumers narrow via the guard below.
 */
export interface SupportsMutationEvents {
  /**
   * Subscribe `cb` to fire after each committed tree mutation (write / delete /
   * mkdir / rename and the unique-create paths). Returns an unsubscribe fn.
   */
  onMutation(cb: () => void): () => void
}

/**
 * One entry of a `replaceTree` payload (Phase 2c restore). `relPath` is ALWAYS
 * root-relative (e.g. `a/b.md`) — NEVER leading-slash, `STORAGE_ROOT`-prefixed,
 * or absolute; the backend forms the stored key as `join(root, relPath)`. A dir
 * entry carries no `bytes`; a file entry carries its restored content.
 */
export interface ReplaceEntry {
  relPath: string
  isDir: boolean
  bytes?: Uint8Array
}

/**
 * Optional capability: atomically replace an entire subtree (Phase 2c restore).
 * Only the In-App backend implements it — restore clears `STORAGE_ROOT` and
 * rewrites the snapshot's manifest in ONE IndexedDB transaction so a failure
 * never leaves the tree half-applied (R2-Pa). Additive and off the base
 * `FsBackend`; consumers narrow via the guard below.
 */
export interface SupportsReplaceTree {
  /**
   * A monotonic revision that every tree mutation bumps. Restore captures it
   * BEFORE the pre-restore snapshot and passes it back as `replaceTree`'s
   * `expectedRevision`, so a concurrent local write between capture and apply is
   * detected INSIDE the replace transaction and the wipe is aborted (no silent
   * overwrite of un-snapshotted changes — the only fully-atomic guard).
   */
  getRevision(): Promise<number>
  /**
   * Clear everything under `root` and write `entries` (dirs first so empty dirs
   * survive) as a single atomic transaction. Each `relPath` is re-validated
   * before any mutation. When `expectedRevision` is given, the transaction first
   * re-reads the stored revision and **aborts without mutating** if it no longer
   * matches (a concurrent write landed) — the check and the wipe share ONE txn,
   * so there is no lossy window. Does NOT emit `onMutation` — restore must not
   * self-trigger an auto-backup.
   */
  replaceTree(root: string, entries: ReplaceEntry[], expectedRevision?: number): Promise<void>
}

/** Thrown by `replaceTree` when `expectedRevision` no longer matches (concurrent write). */
export class TreeRevisionMismatchError extends Error {
  constructor(expected: number, actual: number) {
    super(`replaceTree: tree changed during restore (expected revision ${expected}, found ${actual})`)
    this.name = 'TreeRevisionMismatchError'
  }
}

/** Narrow a resolved backend to the atomic-subtree-replace capability (2c). */
export function supportsReplaceTree(
  backend: FsBackend | undefined,
): backend is FsBackend & SupportsReplaceTree {
  return typeof (backend as Partial<SupportsReplaceTree> | undefined)?.replaceTree === 'function'
}

/** Narrow a resolved backend to the mutation-events capability (Phase 2b). */
export function supportsMutationEvents(
  backend: FsBackend | undefined,
): backend is FsBackend & SupportsMutationEvents {
  return typeof (backend as Partial<SupportsMutationEvents> | undefined)?.onMutation === 'function'
}

/** Narrow a resolved backend to the unique-file-create capability (codex H1). */
export function supportsCreateUnique(
  backend: FsBackend | undefined,
): backend is FsBackend & Pick<SupportsUniqueCreate, 'createUnique'> {
  return typeof (backend as Partial<SupportsUniqueCreate> | undefined)?.createUnique === 'function'
}

/** Narrow a resolved backend to the unique-folder-create capability (codex H1). */
export function supportsMkdirUnique(
  backend: FsBackend | undefined,
): backend is FsBackend & Pick<SupportsUniqueCreate, 'mkdirUnique'> {
  return typeof (backend as Partial<SupportsUniqueCreate> | undefined)?.mkdirUnique === 'function'
}

/**
 * Resolves a backend from the WHOLE source, not just its `type` — the flat
 * registry below is keyed by type alone, which is why a daemon file used to be
 * read through whichever host happened to be active instead of its own
 * `source.hostId` (wrong machine's bytes; worst case a save over the wrong
 * file).
 *
 * Three outcomes, and the difference between the last two is load-bearing:
 * - an `FsBackend` — use it;
 * - `undefined` (**decline**) — the resolver has no opinion, so the flat
 *   registry answers as before;
 * - `null` (**refuse**) — there is no backend for this source and there must
 *   NOT be one. Without this outcome a refusal would decline into the flat
 *   registry's active-host proxy, i.e. straight back to the wrong machine.
 */
export type FsBackendResolver = (source: FileSource) => FsBackend | undefined | null

const backends = new Map<string, FsBackend>()
const resolvers = new Map<string, FsBackendResolver>()

export function registerFsBackend(sourceType: string, backend: FsBackend): void {
  backends.set(sourceType, backend)
}

/** Register a source-aware resolver for `sourceType` (consulted before the flat registry). */
export function registerFsBackendResolver(sourceType: string, resolver: FsBackendResolver): void {
  resolvers.set(sourceType, resolver)
}

export function getFsBackend(source: FileSource): FsBackend | undefined {
  const resolver = resolvers.get(source.type)
  if (resolver) {
    const resolved = resolver(source)
    if (resolved) return resolved
    // `null` is a refusal, not a decline — do not fall through to the flat
    // registry (which for `daemon` is the ACTIVE-host proxy).
    if (resolved === null) return undefined
  }
  return backends.get(source.type)
}

export function clearFsBackendRegistry(): void {
  backends.clear()
  // Resolvers must go too: the test bootstrap harness resets the registry
  // between suites and a leaked resolver would keep answering for a backend
  // set that no longer exists.
  resolvers.clear()
}
