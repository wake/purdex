// =============================================================================
// Sync Architecture — Core Types
// =============================================================================

// ---------------------------------------------------------------------------
// Payloads
// ---------------------------------------------------------------------------

/** Full-serialisation payload: the contributor sends its entire state. */
export interface FullPayload {
  version: number
  data: Record<string, unknown>
}

/** One entry in a chunked manifest: maps a logical id to its content hash. */
export interface ChunkedManifestEntry {
  id: string
  hash: string
}

/**
 * Content-addressed payload: large blobs are split into chunks identified by
 * their hash so unchanged chunks can be skipped during transfer.
 */
export interface ChunkedPayload {
  version: number
  manifest: ChunkedManifestEntry[]
  chunks: Record<string, Uint8Array>
}

// ---------------------------------------------------------------------------
// SyncBundle
// ---------------------------------------------------------------------------

/**
 * Top-level envelope exchanged between devices.  Collections maps each
 * contributor id to its serialised payload (full or chunked).
 */
export interface SyncBundle {
  version: number
  timestamp: number
  device: string
  collections: Record<string, FullPayload | ChunkedPayload>
}

// ---------------------------------------------------------------------------
// Merge
// ---------------------------------------------------------------------------

/** Per-field resolution choices when a field-merge strategy is used. */
export interface ResolvedFields {
  [field: string]: 'local' | 'remote'
}

/**
 * Describes how incoming remote data should be merged with local state.
 * - `full-replace`: overwrite local state entirely with the remote payload.
 * - `field-merge`: apply per-field decisions captured in `resolved`.
 */
export type MergeStrategy =
  | { type: 'full-replace' }
  | { type: 'field-merge'; resolved: ResolvedFields }

/**
 * A single conflict that could not be resolved automatically.
 * Captures the three-way state required to present a conflict-resolution UI.
 */
export interface ConflictItem {
  /** Contributor id that owns this field. */
  contributor: string
  /** Name of the conflicting field. */
  field: string
  /** Value at the time of the last successful sync (common ancestor). */
  lastSynced: unknown
  /** Current local value. */
  local: unknown
  /** Remote value and the device it originated from. */
  remote: {
    value: unknown
    device: string
  }
}

// ---------------------------------------------------------------------------
// SyncContributor
// ---------------------------------------------------------------------------

/**
 * A store or module that participates in synchronisation.
 *
 * Contributors are registered with the SyncEngine and are responsible for
 * serialising/deserialising their own state.  The engine orchestrates when
 * these methods are called.
 */
export interface SyncContributor {
  /** Unique identifier — used as the collection key in SyncBundle. */
  id: string
  /** Serialisation strategy this contributor uses. */
  strategy: 'full' | 'content-addressed'
  /** Produce a payload representing the current local state. */
  serialize(): FullPayload | ChunkedPayload
  /**
   * Apply an incoming payload to local state.
   * @param payload  Raw payload received from the bundle (typed as `unknown`
   *                 so the contributor can validate before applying).
   * @param merge    How conflicts should be resolved.
   */
  deserialize(payload: unknown, merge: MergeStrategy): void
  /** Return the current schema version of this contributor's data. */
  getVersion(): number
  /**
   * Optional: migrate a payload from an older schema version.
   * If omitted the engine will pass the payload to `deserialize` as-is.
   */
  migrate?(payload: unknown, fromVersion: number): unknown
}

// ---------------------------------------------------------------------------
// SyncProvider
// ---------------------------------------------------------------------------

/** A snapshot entry returned by listHistory. */
export interface SyncSnapshot {
  id: string
  timestamp: number
  device: string
  /** Whether this snapshot was created locally or pulled from a remote. */
  source: 'local' | 'remote'
  /** What triggered the sync operation that produced this snapshot. */
  trigger: 'auto' | 'manual'
  /** Opaque reference to the stored bundle (path, key, URL, …). */
  bundleRef: string
}

/**
 * Abstraction over the transport/storage layer that actually moves
 * SyncBundles between devices.
 *
 * Implementations may use the file system, a local daemon, a remote API, etc.
 */
export interface SyncProvider {
  /** Unique identifier for this provider instance. */
  id: string
  /** Upload a full bundle. */
  push(bundle: SyncBundle): Promise<void>
  /** Download the most-recent bundle, or `null` if none exists yet. */
  pull(): Promise<SyncBundle | null>
  /** Upload raw content-addressed chunks (keyed by hash). */
  pushChunks(chunks: Record<string, Uint8Array>): Promise<void>
  /** Download only the chunks whose hashes are listed. */
  pullChunks(hashes: string[]): Promise<Record<string, Uint8Array>>
  /** Return up to `limit` historical snapshot entries, newest first. */
  listHistory(limit: number): Promise<SyncSnapshot[]>
}

// ---------------------------------------------------------------------------
// SyncState
// ---------------------------------------------------------------------------

/**
 * Persisted runtime state for the sync subsystem, held in the SyncState store.
 */
export interface SyncState {
  /** The bundle from the last successful sync, or `null` before first sync. */
  lastSyncedBundle: SyncBundle | null
  /** Unix-ms timestamp of the last successful sync, or `null`. */
  lastSyncedAt: number | null
  /** Id of the currently configured provider, or `null` if none. */
  activeProviderId: string | null
  /** Ids of contributor modules that have opted in to sync. */
  enabledModules: string[]
}
