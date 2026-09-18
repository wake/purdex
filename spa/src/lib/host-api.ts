// spa/src/lib/host-api.ts — Host-aware API layer (unified)
import { useHostStore, type HostInfo } from '../stores/useHostStore'

/* ─── Shared types ─── */

export interface Session {
  code: string
  name: string
  cwd: string
  mode: string
  cc_session_id: string
  cc_model: string
  has_relay: boolean
  current_command?: string
  pane_title?: string
  window_name?: string
  // tmux server generation ("<server pid>:<start_time>") the daemon stamped on
  // the payload that carried this session. Changes on every tmux server
  // restart; "" means unknown (probe failure/timeout, or an older daemon) and
  // never counts as a match. Optional so older daemons stay consumable.
  tmux_instance?: string
}

export interface NexSandboxConfig {
  max_profile: string
  default_profile: string
}

export interface NexTimeoutsConfig {
  lease_ttl: string
  interrupt: string
  turn: string
}

export interface NexConfig {
  enabled: boolean
  repo_roots: string[]
  service_roots: string[]
  claude_bin: string
  path_prepend: string[]
  sandbox: NexSandboxConfig
  timeouts: NexTimeoutsConfig
}

export interface NexEffective {
  data_dir: string
  claude_bin: string
  max_profile: string
  default_profile: string
  repo_roots: string[]
  service_roots: string[]
  path_prefix: string
  lease_ttl: string
  interrupt: string
  turn: string
}

export interface NexInfo {
  configured: boolean
  mounted: boolean
  ready: boolean
  init_error: string
  effective: NexEffective | null
  // Daemon-computed (spec §4.4.2): the live [nex] section differs from the
  // one the daemon booted with. Optional so older daemons stay consumable.
  restart_required?: boolean
}

/* ─── Peers API wire types (internal/peers/record.go, envelope.go) ─── */

/** `PeerRecord.agent`: the agent owning a peer row, when one is known. */
export interface PeerAgentWire {
  type: string                // cc | codex | opencode | proxy
  session_id?: string
  /** The agent's identity — the readable half of an address, e.g. `ai-chat-story-3a`. */
  peer_name?: string
  pid?: number
  proc_start?: string
  inbox?: string
  status?: string             // idle | busy
  version: string             // always present; '' when unknown
}

/**
 * One row of `GET /api/peers`: one per tmux session, plus one per live Claude
 * Code registry entry no session row consumed. `row_kind` tells the two apart —
 * only `'session'` rows carry a `session_code` that joins to a pane.
 */
export interface PeerRecordWire {
  host: string
  host_id: string
  address: string
  row_kind: string            // session | entry
  /**
   * The sessionId-derived address head (`_3k9f2m`), `''` when the row has no
   * cc agent. This — not `title_source` — is what tells an agent row apart from
   * a row with no agent.
   */
  ref: string
  title: string               // '' until the conversation names itself
  title_source: string        // user | ''
  title_rev: number
  session_code: string
  session_name: string
  tmux_instance: string
  cwd?: string
  agent: PeerAgentWire | null
  deliverable: boolean
  reason: string              // '' | no_agent | not_cc | inbox_dead | proxy | ambiguous
}

/**
 * `GET /api/peers` for one host. `partial` has three independent causes — owner
 * lookups that did not run, `unknown_registry_files`, `titles_unavailable` — so
 * "no row and partial" never means "no peer".
 */
export interface PeersEnvelope {
  host_id: string
  ok: boolean
  error?: string
  partial: boolean
  peers: PeerRecordWire[]     // never null
  daemon_version: string
  unknown_registry_files: string[]  // never null
  titles_unavailable: boolean
}

/* ─── Peer-host wire types (internal/module/peers/hosts.go hostRow, hosts_verify.go, settings.go) ─── */

/** One `[[peers.hosts]]` entry as `GET /api/peers/hosts` renders it: never a token value. */
export interface PeerHostRow {
  alias: string
  url: string
  host_id: string             // '' when the entry was added without a token and never verified
  verified: boolean
  has_token: boolean          // outbound token present (what we present to them)
  has_inbound_token: boolean  // what they must present to us
  allow_bypass: boolean
  rotation_pending: boolean                   // inbound_token_prev is set (spec §6.1)
  last_inbound_auth: '' | 'current' | 'prev'  // which token the peer LAST presented, derived at read time (§6.2)
}

/**
 * `POST /api/peers/hosts` 201 body: the first of the two responses that carry
 * a live token value (`inbound_token`, the one the peer must present to us).
 * Spec D-8: it lives in the flow that consumes it and nowhere else.
 */
export interface PeerHostAdded {
  alias: string
  url: string
  host_id: string
  inbound_token: string
  verified: boolean
}

/**
 * `POST /api/peers/hosts/{alias}/verify` (Phase D spec §4.1). Exactly one
 * `scope=all` fan-out row minus its peer rows, so this and `pdx peers --all`
 * can never disagree. `ok:false` always carries a non-empty `error`.
 * `self_alias` is the peer's own word for itself — display value, unvalidated.
 */
export interface PeerHostVerify {
  alias: string
  host_id: string
  ok: boolean
  error?: string
  self_alias: string
  daemon_version: string
}

/**
 * `GET`/`PUT /api/peers/settings`: this daemon's deliver toggle and its own
 * effective alias. `alias_source` says where the alias comes from (self alias
 * spec S-3): `'config'` = `[peers] alias` is set, `'host_id'` = derived from
 * `host_id`. It is absent on a daemon older than alpha.399 — and that absence
 * is how a PUT that was silently ignored is told apart from one that was
 * applied (S-5): the wrapper passes the body through and never invents it.
 */
export interface PeerSettings {
  deliver: boolean
  alias: string
  alias_source?: 'config' | 'host_id'
}

export interface ConfigData {
  bind: string
  port: number
  upload_dir?: string
  terminal?: { sizing_mode: string }
  detect: { cc_commands: string[]; poll_interval: number }
  nex?: NexConfig
}

/* ─── Core helpers ─── */

export function hostFetch(hostId: string, path: string, init?: RequestInit): Promise<Response> {
  const { getDaemonBase, getAuthHeaders } = useHostStore.getState()
  const base = getDaemonBase(hostId)
  const headers = new Headers(init?.headers)
  const auth = getAuthHeaders(hostId)
  for (const [k, v] of Object.entries(auth)) {
    headers.set(k, v)
  }
  return fetch(`${base}${path}`, { ...init, headers })
}

/**
 * The auth headers `hostFetch` attaches, exported for transports that cannot
 * go through `hostFetch` (the nex SSE reader builds its own fetch so it can
 * stream the body). One source of truth for "how do we authenticate to host X".
 */
export function hostAuthHeaders(hostId: string): Record<string, string> {
  return useHostStore.getState().getAuthHeaders(hostId)
}

export function hostWsUrl(hostId: string, path: string): string {
  const base = useHostStore.getState().getWsBase(hostId)
  return `${base}${path}`
}

export async function fetchWsTicket(hostId: string): Promise<string> {
  const res = await hostFetch(hostId, '/api/ws-ticket', { method: 'POST' })
  if (!res.ok) throw new Error(`ws-ticket failed: ${res.status}`)
  const data = await res.json()
  return data.ticket
}

/* ─── API functions ─── */

export function fetchHealth(hostId: string) {
  return hostFetch(hostId, '/api/health')
}

export function fetchInfo(hostId: string) {
  return hostFetch(hostId, '/api/info')
}

export function fetchUploadStats(hostId: string) {
  return hostFetch(hostId, '/api/upload/stats')
}

export function fetchUploadFiles(hostId: string) {
  return hostFetch(hostId, '/api/upload/files')
}

export function deleteUploadFile(hostId: string, session: string, filename: string) {
  return hostFetch(hostId, `/api/upload/files/${session}/${filename}`, { method: 'DELETE' })
}

export function deleteUploadSession(hostId: string, session: string) {
  return hostFetch(hostId, `/api/upload/files/${session}`, { method: 'DELETE' })
}

export function deleteAllUploads(hostId: string) {
  return hostFetch(hostId, '/api/upload/files', { method: 'DELETE' })
}

export function renameSession(hostId: string, code: string, name: string) {
  return hostFetch(hostId, `/api/sessions/${code}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }),
  })
}

/* ─── Session API ─── */

/**
 * An HTTP error that keeps its status code. The rebuild engine retries a
 * duplicate session name ONLY on 409 — the status the daemon returns
 * specifically for that case (`internal/module/session/handler.go:101-104`) —
 * and must never retry a 400 (validation) or 500 (create failure).
 */
export class HostApiError extends Error {
  status: number
  /**
   * The daemon's `{error}` text when the body carried one, else `statusText`
   * when non-empty, else `HTTP <status>` — under HTTP/2 `statusText` is
   * always `''`, so that case must not fall through to an empty string.
   */
  detail: string
  constructor(status: number, statusText: string, detail?: string) {
    super(`${status} ${statusText}`)
    this.name = 'HostApiError'
    this.status = status
    this.detail = detail ?? (statusText || `HTTP ${status}`)
  }
}

export async function listSessions(hostId: string): Promise<Session[]> {
  const res = await hostFetch(hostId, '/api/sessions')
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json()
}

export async function createSession(
  hostId: string, name: string, cwd: string, mode: string,
): Promise<Session> {
  const res = await hostFetch(hostId, '/api/sessions', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, cwd, mode }),
  })
  // Typed so a 409 (duplicate name) stays distinguishable from 400/500; the
  // message is unchanged, so existing callers that only render it are unaffected.
  if (!res.ok) throw new HostApiError(res.status, res.statusText)
  return res.json()
}

export async function deleteSession(hostId: string, code: string): Promise<void> {
  const res = await hostFetch(hostId, `/api/sessions/${code}`, { method: 'DELETE' })
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
}

/* ─── Peers API ─── */

/**
 * The host's whole peer inventory. Expensive: the daemon resolves the owning
 * agent of *every* tmux session under a 2 s budget (`internal/module/peers/
 * module.go`), and with a couple of dozen sessions it spends that budget every
 * call. Never put this behind a per-tab interaction — see the fetch policy in
 * `usePeerInfo`.
 */
export async function fetchPeers(hostId: string, signal?: AbortSignal): Promise<PeersEnvelope> {
  const res = await hostFetch(hostId, '/api/peers', { signal })
  if (!res.ok) throw new Error(`fetchPeers failed: ${res.status}`)
  return res.json()
}

/* ─── Peer-host API (Phase D) ─── */

/**
 * Reads the daemon's `{error}` body into a HostApiError. Every peer-host route
 * answers errors as `{"error": "<msg>"}` (`writeJSONError`); anything else
 * (a proxy page, an empty body) falls back to the status text.
 */
async function peerHostError(res: Response): Promise<HostApiError> {
  let detail: string | undefined
  try {
    const body = await res.json()
    if (body && typeof body.error === 'string' && body.error) detail = body.error
  } catch { /* not JSON */ }
  return new HostApiError(res.status, res.statusText, detail)
}

async function peerHostJson<T>(res: Response): Promise<T> {
  if (!res.ok) throw await peerHostError(res)
  return res.json() as Promise<T>
}

export async function listPeerHosts(hostId: string): Promise<PeerHostRow[]> {
  const body = await peerHostJson<{ hosts: PeerHostRow[] | null }>(await hostFetch(hostId, '/api/peers/hosts'))
  // A daemon older than alpha.391 (pre-D3) omits `rotation_pending` and
  // `last_inbound_auth`. Absent means the same fact as false / '': no rotation
  // pending, no evidence of which token the peer last presented.
  return (body.hosts ?? []).map((r) => ({
    ...r,
    rotation_pending: r.rotation_pending === true,
    last_inbound_auth: r.last_inbound_auth === 'current' || r.last_inbound_auth === 'prev' ? r.last_inbound_auth : '',
  }))
}

/**
 * `POST /api/peers/hosts` (D0): `{alias?, url, token?}` — only the keys given
 * are sent, so the daemon's own defaults apply (alias from the peer's
 * published self alias; no outbound token = unverified entry). 201 carries
 * `inbound_token` (spec D-8). 400 invalid input / self-pair, 409 alias taken
 * or changed concurrently, 502 verify failed (the dial runs before the alias
 * check, `hosts.go:64–82`).
 */
export function addPeerHost(
  hostId: string,
  body: { alias?: string; url: string; token?: string },
): Promise<PeerHostAdded> {
  return hostFetch(hostId, '/api/peers/hosts', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  }).then(peerHostJson<PeerHostAdded>)
}

/**
 * `DELETE /api/peers/hosts/{alias}` → 204. A 404 (`unknown alias`) is thrown
 * as a HostApiError, not swallowed: whether "already gone" counts as done is
 * the caller's decision (the unpair flow treats it so; nothing else should).
 */
export async function deletePeerHost(hostId: string, alias: string): Promise<void> {
  const res = await hostFetch(hostId, `/api/peers/hosts/${encodeURIComponent(alias)}`, { method: 'DELETE' })
  if (!res.ok) throw await peerHostError(res)
}

/**
 * `POST /api/peers/hosts/{alias}/rotate` (D3): mints a new inbound token,
 * keeps the old one as `inbound_token_prev` until commit/cancel. The 200 body
 * is the second response carrying a live token value (spec D-8). 409
 * `rotation already pending`, 404 unknown alias. No body.
 */
export function rotatePeerHost(hostId: string, alias: string): Promise<{ alias: string; inbound_token: string }> {
  return hostFetch(hostId, `/api/peers/hosts/${encodeURIComponent(alias)}/rotate`, { method: 'POST' })
    .then(peerHostJson<{ alias: string; inbound_token: string }>)
}

/**
 * `POST …/{alias}/rotate/commit`: drops the previous token. Sent with NO body
 * on purpose — an empty body is `force=false` on the daemon
 * (`hosts_rotate.go:60`), and the page never sends `force` (spec D-7). 409
 * `rotation unconfirmed` unless the peer's last dial used the new token;
 * a 200 no-op when nothing is pending.
 */
export function commitRotation(hostId: string, alias: string): Promise<PeerHostRow> {
  return hostFetch(hostId, `/api/peers/hosts/${encodeURIComponent(alias)}/rotate/commit`, { method: 'POST' })
    .then(peerHostJson<PeerHostRow>)
}

/**
 * `POST …/{alias}/rotate/cancel`: restores the previous token. NO body, same
 * reason as `commitRotation` (spec D-7). 409 `no rotation pending` /
 * `rotation unconfirmed` unless the peer's last dial used the old token.
 */
export function cancelRotation(hostId: string, alias: string): Promise<PeerHostRow> {
  return hostFetch(hostId, `/api/peers/hosts/${encodeURIComponent(alias)}/rotate/cancel`, { method: 'POST' })
    .then(peerHostJson<PeerHostRow>)
}

/** Live dial, ≤ 3 s on the daemon side; never cached (spec D-6, §5.2 step 4). */
export function verifyPeerHost(hostId: string, alias: string): Promise<PeerHostVerify> {
  return hostFetch(hostId, `/api/peers/hosts/${encodeURIComponent(alias)}/verify`, { method: 'POST' })
    .then(peerHostJson<PeerHostVerify>)
}

/**
 * `PUT /api/peers/hosts/{alias}`: any subset of `{alias, token, allow_bypass}`.
 * D2 passes only `alias` (adopt the peer's self alias = plain rename, spec D-5).
 * D4 passes `token` for the push step (the daemon verifies with it before
 * storing); a token value must never be held longer than the call that
 * consumes it (spec D-8).
 */
export function updatePeerHost(
  hostId: string, alias: string,
  patch: { alias?: string; token?: string; allow_bypass?: boolean },
): Promise<PeerHostRow> {
  return hostFetch(hostId, `/api/peers/hosts/${encodeURIComponent(alias)}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  }).then(peerHostJson<PeerHostRow>)
}

export function fetchPeerSettings(hostId: string): Promise<PeerSettings> {
  return hostFetch(hostId, '/api/peers/settings').then(peerHostJson<PeerSettings>)
}

/**
 * `PUT /api/peers/settings`: any subset of `{alias, deliver}`; the JSON body
 * is exactly the keys given, because absent means unchanged on the daemon
 * (S-2) — `{alias: ''}` clears the self alias back to the `host_id` default,
 * a missing `alias` key leaves it alone. 400 pattern/reserved, 409 collision
 * with a configured peer host's alias (S-6). Read `alias_source` on the
 * answer before calling the write a success (S-5).
 */
export function updatePeerSettings(
  hostId: string, patch: { alias?: string; deliver?: boolean },
): Promise<PeerSettings> {
  return hostFetch(hostId, '/api/peers/settings', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  }).then(peerHostJson<PeerSettings>)
}

/** Typed `/api/info` (the untyped `fetchInfo` above stays for its existing callers). */
export function fetchHostInfo(hostId: string): Promise<HostInfo> {
  return fetchInfo(hostId).then(peerHostJson<HostInfo>)
}

/**
 * A cwd reading and the tmux generation it was sampled in (spec §4.6.2).
 *
 * The pair travels together because a bare string cannot be attributed: a
 * caller that stamps the answer with the generation it *asked* with writes the
 * new session's directory into the old one's record whenever tmux restarted
 * and reused the code mid-request. `tmuxInstance` is `''` when unknown — an
 * old daemon, or a generation that moved during the read — and `''` never
 * equals a real generation, so unknown can only ever mean "do not write".
 */
export interface SessionCwd {
  cwd: string
  tmuxInstance: string
}

export async function fetchSessionCwd(
  hostId: string,
  sessionCode: string,
  signal?: AbortSignal,
): Promise<SessionCwd> {
  const res = await hostFetch(hostId, `/api/sessions/${sessionCode}/cwd`, { signal })
  if (!res.ok) throw new Error(`fetchSessionCwd failed: ${res.status}`)
  const body = await res.json()
  return { cwd: String(body.cwd ?? ''), tmuxInstance: String(body.tmux_instance ?? '') }
}

/**
 * The daemon's answer to "which agent owns this pane?" (spec §5.3): the root
 * agent frame of the session, resolved through the frames and the tmux session
 * id, never through the session name.
 *
 * `found: false` means no root frame carried a session id; every `omitempty`
 * field is then absent from the body and normalised to '' / 0 here, so a caller
 * never has to distinguish "missing" from "empty". `tmuxInstance` follows the
 * same rule as {@link SessionCwd}: '' is the daemon's "I could not tell" and
 * never equals a real generation.
 */
export interface SessionProvenance {
  found: boolean
  agentType: string
  sessionId: string
  cwd: string
  tmuxPaneId: string
  tmuxInstance: string
  lastSeenAt: number
}

export async function fetchSessionProvenance(
  hostId: string,
  sessionCode: string,
  signal?: AbortSignal,
): Promise<SessionProvenance> {
  const res = await hostFetch(hostId, `/api/sessions/${sessionCode}/provenance`, { signal })
  if (!res.ok) throw new Error(`fetchSessionProvenance failed: ${res.status}`)
  const body = await res.json()
  return {
    found: Boolean(body.found),
    agentType: String(body.agent_type ?? ''),
    sessionId: String(body.session_id ?? ''),
    cwd: String(body.cwd ?? ''),
    tmuxPaneId: String(body.tmux_pane_id ?? ''),
    tmuxInstance: String(body.tmux_instance ?? ''),
    lastSeenAt: Number(body.last_seen_at ?? 0),
  }
}

/**
 * The verdict `POST /api/shell/resolve-command` returns (spec §4.4).
 *
 * `unverifiable` is not a daemon verdict — it is what a 404 means: a daemon
 * older than this endpoint. The user's template is already saved and the check
 * is advice, so an old daemon must read as "could not check", never as an
 * error the user has to debug (spec §8).
 */
export type ShellResolveVerdict =
  | { status: 'resolved'; detail: string }
  | { status: 'unresolved'; reason: string }
  | { status: 'unverifiable' }

/**
 * `command` must be the COMMAND WORD, not a whole template: the daemon passes
 * it to the shell as a single positional parameter, so `cld-yolo --resume {id}`
 * would be looked up verbatim and answer `not_found`. Splitting is the caller's
 * job (spec §4.4).
 */
export async function resolveShellCommand(
  hostId: string,
  command: string,
  signal?: AbortSignal,
): Promise<ShellResolveVerdict> {
  const res = await hostFetch(hostId, '/api/shell/resolve-command', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ command }),
    signal,
  })
  if (res.status === 404) return { status: 'unverifiable' }
  if (!res.ok) throw new Error(`resolveShellCommand failed: ${res.status}`)
  const body = await res.json()
  if (body.resolved) return { status: 'resolved', detail: String(body.detail ?? '') }
  return { status: 'unresolved', reason: String(body.reason ?? '') }
}

export async function fetchSessionHome(
  hostId: string,
  sessionCode: string,
  signal?: AbortSignal,
): Promise<string> {
  const res = await hostFetch(hostId, `/api/sessions/${sessionCode}/home`, { signal })
  if (!res.ok) throw new Error(`fetchSessionHome failed: ${res.status}`)
  const body = await res.json()
  return String(body.home ?? '')
}

/* ─── Config API ─── */

export async function getConfig(hostId: string): Promise<ConfigData> {
  const res = await hostFetch(hostId, '/api/config')
  if (!res.ok) throw new Error(`get config failed: ${res.status}`)
  return res.json()
}

export async function updateConfig(
  hostId: string,
  updates: Partial<ConfigData>,
): Promise<ConfigData> {
  const res = await hostFetch(hostId, '/api/config', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(updates),
  })
  if (!res.ok) throw new Error(`update config failed: ${res.status}`)
  return res.json()
}

/* ─── Agent Upload API ─── */

export async function agentUpload(
  hostId: string,
  file: File,
  session: string,
): Promise<{ filename: string; injected: boolean }> {
  const form = new FormData()
  form.append('file', file)
  form.append('session', session)
  const res = await hostFetch(hostId, '/api/agent/upload', {
    method: 'POST',
    body: form,
  })
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json()
}

/* ─── Monitor API ─── */

export interface MonitorMetricBounds {
  min: number
  max: number
}

export interface MonitorConfigBounds {
  refresh_interval_ms: MonitorMetricBounds
  top_process_limit: MonitorMetricBounds
}

export interface MonitorConfig {
  refresh_interval_ms: number
  top_process_limit: number
  bounds: MonitorConfigBounds
}

export type MonitorConfigUpdate = Partial<Pick<MonitorConfig, 'refresh_interval_ms' | 'top_process_limit'>>

export interface MonitorHostCPU {
  percent: number | null
  unavailable_reason: string | null
}

export interface MonitorHostMemory {
  total_bytes: number | null
  used_bytes: number | null
  used_percent: number | null
  unavailable_reason: string | null
}

export interface MonitorHostDisk {
  total_bytes: number | null
  used_bytes: number | null
  used_percent: number | null
  unavailable_reason: string | null
}

export interface MonitorHostMetrics {
  cpu: MonitorHostCPU
  memory: MonitorHostMemory
  disk: MonitorHostDisk
}

export interface MonitorTopProcess {
  pid: number
  ppid: number
  command: string
  cpu_percent: number
  memory_bytes: number
}

export interface MonitorTmuxSession {
  id: string
  name: string
}

export interface MonitorSessionDaemonMetrics {
  cpu_percent: number | null
  memory_bytes: number | null
  process_count: number | null
  top_processes: MonitorTopProcess[]
  unavailable_reason: string | null
}

export interface MonitorSessionMetrics {
  session_code: string
  tmux_session: MonitorTmuxSession
  daemon: MonitorSessionDaemonMetrics
}

export interface MonitorSnapshot {
  sampled_at: number
  host: MonitorHostMetrics
  sessions: MonitorSessionMetrics[]
  config: MonitorConfig
}

export async function fetchMonitorSnapshot(hostId: string): Promise<MonitorSnapshot> {
  const res = await hostFetch(hostId, '/api/monitor/snapshot')
  if (!res.ok) throw new Error(`fetchMonitorSnapshot failed: ${res.status}`)
  return res.json()
}

export async function fetchMonitorConfig(hostId: string): Promise<MonitorConfig> {
  const res = await hostFetch(hostId, '/api/monitor/config')
  if (!res.ok) throw new Error(`fetchMonitorConfig failed: ${res.status}`)
  return res.json()
}

export async function updateMonitorConfig(
  hostId: string,
  updates: MonitorConfigUpdate,
): Promise<MonitorConfig> {
  const res = await hostFetch(hostId, '/api/monitor/config', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(updates),
  })
  if (!res.ok) throw new Error(`updateMonitorConfig failed: ${res.status}`)
  return res.json()
}

/* ─── Pairing API (Phase 5a) ─── */

/** POST /api/pair/verify — Quick mode: verify pairing secret, get setupSecret. */
export async function fetchPairVerify(
  base: string,
  secret: string,
): Promise<{ setupSecret: string }> {
  const res = await fetch(`${base}/api/pair/verify`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ secret }),
  })
  if (!res.ok) {
    const text = await res.text().catch(() => '')
    throw new PairingError(res.status, text)
  }
  return res.json()
}

/** POST /api/pair/setup — Quick mode: set token on daemon. */
export async function fetchPairSetup(
  base: string,
  setupSecret: string,
  token: string,
): Promise<{ ok: boolean }> {
  const res = await fetch(`${base}/api/pair/setup`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ setupSecret, token }),
  })
  if (!res.ok) {
    const text = await res.text().catch(() => '')
    throw new PairingError(res.status, text)
  }
  return res.json()
}

/** POST /api/token/auth — General mode: confirm runtime token. */
export async function fetchTokenAuth(
  base: string,
  token: string,
): Promise<{ ok: boolean }> {
  const res = await fetch(`${base}/api/token/auth`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${token}` },
  })
  if (res.status === 409) {
    // already_confirmed — treat as success per spec
    return { ok: true }
  }
  if (!res.ok) {
    const text = await res.text().catch(() => '')
    throw new PairingError(res.status, text)
  }
  return res.json()
}

export class PairingError extends Error {
  status: number
  body: string
  constructor(status: number, body: string) {
    super(`Pairing failed: HTTP ${status}`)
    this.status = status
    this.body = body
  }
}
