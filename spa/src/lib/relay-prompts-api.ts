// spa/src/lib/relay-prompts-api.ts — the relay prompts of one host (lead-team-relay spec §8.8, U21; plan v3 P9a-3):
// `GET /api/relay/prompts`, and the client mirror of the daemon's body check
// (internal/team/relay_prompts.go `ValidateRelayPromptBody`). The bodies are saved through host config's `relay`
// row (`saveRelay`), not here. The daemon stays the authority: when the two checks disagree, its 400 detail is
// what the editor shows.
import { trimLikeGo } from './go-trim'
import { pinnedHostFetch } from './host-api'

export type RelayPromptKind = 'write' | 'fix' | 'seed'
export const RELAY_PROMPT_KINDS: readonly RelayPromptKind[] = ['write', 'fix', 'seed']

export type RelayPromptBodies = Record<RelayPromptKind, string>
/** What no body can change (U21 (c)), with the mod's own placeholders ({{op}}, {{nonce}}, {{missing}}) unfilled. */
export interface RelayPromptFixed { head: string; tail: string }

/** The GET's answer: each effective body (stored, else the default), the defaults, the fixed parts, the variables. */
export interface RelayPrompts extends RelayPromptBodies {
  defaults: RelayPromptBodies
  fixed: Record<RelayPromptKind, RelayPromptFixed>
  variables: string[]
}

/** `team.RelayPromptMaxBytes`: the longest body, in UTF-8 bytes. */
export const RELAY_PROMPT_MAX_BYTES = 16 << 10
const RELAY_TAG = '[pdx-relay'

export class RelayPromptsApiError extends Error {
  readonly status: number
  constructor(status: number, message: string) {
    super(message)
    this.name = 'RelayPromptsApiError'
    this.status = status
  }
}

const isRecord = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)
const isBodies = (v: unknown): boolean => isRecord(v) && RELAY_PROMPT_KINDS.every((k) => typeof v[k] === 'string')
const isFixed = (v: unknown): boolean => isRecord(v) && typeof v.head === 'string' && typeof v.tail === 'string'

function isRelayPrompts(v: unknown): v is RelayPrompts {
  if (!isRecord(v) || !isBodies(v) || !isBodies(v.defaults)) return false
  const fixed = v.fixed
  if (!isRecord(fixed) || !RELAY_PROMPT_KINDS.every((k) => isFixed(fixed[k]))) return false
  return Array.isArray(v.variables) && v.variables.every((x) => typeof x === 'string')
}

/**
 * The host's prompts. A plain-text 404 (Go's mux: a daemon from before P9a-1 has no route) is `'unsupported'`; any
 * other failure throws, a JSON error with the daemon's `detail` as its message. The body is checked whole.
 */
export async function fetchRelayPrompts(hostId: string, signal?: AbortSignal): Promise<RelayPrompts | 'unsupported'> {
  const res = await pinnedHostFetch(hostId, '/api/relay/prompts', { signal })
  if (!res.ok) {
    const text = (await res.text().catch(() => '')).trim()
    let parsed: unknown
    try {
      parsed = JSON.parse(text)
    } catch {
      if (res.status === 404) return 'unsupported'
    }
    const detail = isRecord(parsed) && typeof parsed.detail === 'string' && parsed.detail !== '' ? parsed.detail : text
    throw new RelayPromptsApiError(res.status, detail || `${res.status} ${res.statusText}`.trim())
  }
  const body: unknown = await res.json()
  if (!isRelayPrompts(body)) throw new RelayPromptsApiError(res.status, 'the daemon answered relay prompts of an unknown shape')
  return body
}

/** A box's text as the daemon will see it: CRLF as LF (a pasted Windows text). */
export function normalizeRelayPromptBody(text: string): string {
  return text.replace(/\r\n/g, '\n')
}

export function relayPromptBytes(text: string): number {
  return new TextEncoder().encode(text).length
}

export type RelayPromptProblem = 'too_long' | 'control_chars' | 'has_tag'

/**
 * `ValidateRelayPromptBody` on a normalized body, in its order: over 16 384 UTF-8 bytes; a control character other
 * than newline and tab (Go's `unicode.IsControl`: C0, DEL and C1, so a lone `\r` too); the machine tag anywhere.
 */
export function checkRelayPromptBody(text: string): RelayPromptProblem | null {
  if (relayPromptBytes(text) > RELAY_PROMPT_MAX_BYTES) return 'too_long'
  for (const ch of text) {
    const c = ch.codePointAt(0) ?? 0
    if (c !== 0x09 && c !== 0x0a && (c < 0x20 || (c >= 0x7f && c <= 0x9f))) return 'control_chars'
  }
  if (text.includes(RELAY_TAG)) return 'has_tag'
  return null
}

/**
 * The value a save PUTs: `""` for the default text (open question 12: a later change of the default then reaches
 * this host) and for whitespace only (the daemon's `strings.TrimSpace` stores that as `""` too, U21 (a)); else the
 * normalized text.
 */
export function relayPromptValueToStore(text: string, defaultBody: string): string {
  const body = normalizeRelayPromptBody(text)
  return trimLikeGo(body) === '' || body === defaultBody ? '' : body
}
