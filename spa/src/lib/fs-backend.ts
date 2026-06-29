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
   * Clear everything under `root` and write `entries` (dirs first so empty dirs
   * survive) as a single atomic transaction. Each `relPath` is re-validated
   * before any mutation. Does NOT emit `onMutation` — restore must not
   * self-trigger an auto-backup.
   */
  replaceTree(root: string, entries: ReplaceEntry[]): Promise<void>
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

const backends = new Map<string, FsBackend>()

export function registerFsBackend(sourceType: string, backend: FsBackend): void {
  backends.set(sourceType, backend)
}

export function getFsBackend(source: FileSource): FsBackend | undefined {
  return backends.get(source.type)
}

export function clearFsBackendRegistry(): void {
  backends.clear()
}
