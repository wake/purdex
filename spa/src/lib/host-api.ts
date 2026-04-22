// spa/src/lib/host-api.ts — Host-aware API layer (unified)
import { useHostStore } from '../stores/useHostStore'
import type { StreamMessage } from './stream-ws'

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
}

export interface ConfigData {
  bind: string
  port: number
  upload_dir?: string
  terminal?: { sizing_mode: string }
  stream: { presets: Array<{ name: string; command: string }> }
  detect: { cc_commands: string[]; poll_interval: number }
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
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json()
}

export async function deleteSession(hostId: string, code: string): Promise<void> {
  const res = await hostFetch(hostId, `/api/sessions/${code}`, { method: 'DELETE' })
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
}

export async function switchMode(hostId: string, code: string, mode: string): Promise<Session> {
  const res = await hostFetch(hostId, `/api/sessions/${code}/mode`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ mode }),
  })
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json()
}

/* ─── Handoff API ─── */

export async function handoff(
  hostId: string,
  code: string,
  mode: string,
  preset?: string,
): Promise<{ handoff_id: string }> {
  const body: Record<string, string> = { mode }
  if (preset) body.preset = preset
  const res = await hostFetch(hostId, `/api/sessions/${code}/handoff`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!res.ok) {
    const text = await res.text().catch(() => '')
    throw new Error(`handoff failed: ${res.status} ${text}`.trim())
  }
  return res.json()
}

/* ─── History API ─── */

export async function fetchHistory(hostId: string, sessionCode: string): Promise<StreamMessage[]> {
  const res = await hostFetch(hostId, `/api/sessions/${sessionCode}/history`)
  if (!res.ok) return []
  return res.json()
}

export async function fetchSessionCwd(
  hostId: string,
  sessionCode: string,
  signal?: AbortSignal,
): Promise<string> {
  const res = await hostFetch(hostId, `/api/sessions/${sessionCode}/cwd`, { signal })
  if (!res.ok) throw new Error(`fetchSessionCwd failed: ${res.status}`)
  const body = await res.json()
  return String(body.cwd ?? '')
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

/* ─── Agent Monitor API ─── */

export interface AgentMonitorChainSummary {
  chain_id: string
  started_at: number
  completed_at: number
  terminal_status: string
  terminal_reason: string
  tmux_session: string
  pane_id: string
  root_agent_type: string
  root_event_name: string
  root_reason: string
  latest_step_kind: string
  latest_decision: string
  latest_step_reason: string
  step_count: number
  // Lights envelope (PR-1b-0, #569); expected value e.g. "1.0.0-lights-1b".
  // Optional to stay compatible with older daemons that omit the field.
  schema_version?: string
}

export interface AgentMonitorStep {
  step_id: string
  chain_id: string
  parent_step_id?: string
  seq: number
  kind: string
  tmux_session: string
  pane_id: string
  agent_type: string
  frame_id: string
  parent_frame_id: string
  event_name: string
  decision: string
  reason: string
  payload_json: string
  before_json: string
  after_json: string
  created_at: number
  // Lights envelope (PR-1b-0, #569) — all optional because backend uses
  // `omitempty` and older daemons will not send them. JSON-valued fields
  // are typed as `unknown` intentionally; consumers must narrow before use
  // (no `any` to avoid type erosion across the codebase).
  source_kind?: string
  action?: string
  reason_code?: string
  outcome?: string
  scenario_key?: string
  observed_generation?: number
  decision_ports?: unknown
  phase?: string
  status?: string
  watcher_token?: string
  trace_id?: string
  reason_text?: string
  attrs?: unknown
  input_refs?: unknown
  output_refs?: unknown
  state_before_ref?: unknown
  state_after_ref?: unknown
  evidence_refs?: unknown
  started_at?: number
  ended_at?: number
  otel_kind?: string
}

export interface AgentMonitorStepNode {
  step: AgentMonitorStep
  children: AgentMonitorStepNode[]
}

export interface AgentMonitorProjectionSummary {
  tmux_session: string
  pane_id: string
  primary_frame_id: string
  top_frame_id: string
  top_agent_type: string
  latest_chain_id: string
}

export async function fetchAgentMonitorChains(
  hostId: string,
  query = new URLSearchParams(),
): Promise<{ chains: AgentMonitorChainSummary[]; next_cursor: string }> {
  const qs = query.toString()
  const res = await hostFetch(hostId, `/api/agent/monitor/chains${qs ? `?${qs}` : ''}`)
  if (!res.ok) throw new Error(`fetchAgentMonitorChains failed: ${res.status}`)
  return res.json()
}

export async function fetchAgentMonitorChain(
  hostId: string,
  chainId: string,
): Promise<{ chain: AgentMonitorChainSummary; step_tree: AgentMonitorStepNode[] }> {
  const res = await hostFetch(hostId, `/api/agent/monitor/chains/${chainId}`)
  if (!res.ok) throw new Error(`fetchAgentMonitorChain failed: ${res.status}`)
  return res.json()
}

export async function fetchAgentMonitorProjection(
  hostId: string,
  query: URLSearchParams,
): Promise<{ projection: AgentMonitorProjectionSummary | null }> {
  const qs = query.toString()
  const res = await hostFetch(hostId, `/api/agent/monitor/projection${qs ? `?${qs}` : ''}`)
  if (!res.ok) throw new Error(`fetchAgentMonitorProjection failed: ${res.status}`)
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
