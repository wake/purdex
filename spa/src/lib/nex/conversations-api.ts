// spa/src/lib/nex/conversations-api.ts — client for the daemon's
// GET /api/nex/conversations?state=ended|gone (internal/module/nex/conversations_http.go).
// Purdex-orchestrated (not a Nexen route), so it goes through handoff-api's getJson.
import { HandoffApiError, getJson } from './handoff-api'

export type ConversationState = 'ended' | 'gone'
/** `?scope=` (daemon capability `conversations.scope.v1`); omitted = all. */
export type ConversationScope = 'test' | 'normal'

export interface ConversationRow {
  session_id: string
  title: string
  title_source: 'custom' | 'ai' | 'nexen' | 'prompt' | 'registry' | 'session_id'
  first_prompt?: string
  cwd?: string
  cwd_exists: boolean
  /** Unix ms. */
  last_activity_at: number
  last_in: 'terminal' | 'worker'
  transcript_path?: string
  latest_execution_id?: string
  effective_profile?: string
}

export interface ConversationsPage {
  state: ConversationState
  /** Unix ms of the scan the snapshot used. */
  scanned_at: number
  /** The projects root could not be listed (R-4-1). */
  root_error?: string
  home: string
  /** Rows in this state before the 2,000-row cap. */
  total: number
  truncated: boolean
  /** Conversations whose owner could not be verified; listed in neither state (R-4-9). */
  unknown_owner: number
  conversations: ConversationRow[]
}

/** `GET /api/nex/conversations?state=…`. Errors arrive as `HandoffApiError`; 404 is `http_404` (Nexen disabled). */
export async function listConversations(hostId: string, state: ConversationState, scope?: ConversationScope): Promise<ConversationsPage> {
  const query = `state=${encodeURIComponent(state)}${scope ? `&scope=${encodeURIComponent(scope)}` : ''}`
  const body = await getJson<unknown>(hostId, `/api/nex/conversations?${query}`)
  if (typeof body !== 'object' || body === null || !Array.isArray((body as { conversations?: unknown }).conversations)) {
    throw new HandoffApiError(200, 'bad_response', {}, 'conversations: malformed response')
  }
  return body as ConversationsPage
}
