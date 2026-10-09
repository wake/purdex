// spa/src/lib/team/appearance-api.ts — `PUT /api/team/appearance` (TR-1, team-interface spec §4.12): edit a live team's
// name, short label and colour on its lead's host.
//
// The body is ALL of the fields every time (`team_name`, `team_label`, `team_color` 0-7 or null = automatic, `client`):
// the daemon never tells "absent" from "leave it", so a missing one is a 400. `team_label: ''` means "derive it from the
// name". The caller passes the three values it wants stored (the roster's current ones with the edits); the answer is
// NOT applied anywhere: the panel always shows what the roster's next frame says.
//
// Errors (daemon wire, internal/module/team/appearance_handler.go): 400 `{error:'bad_request', detail}` where `detail`
// starts with the field (`team_name: ...`, `team_label: ...`, `team_color must be ...`), 409 `not_live` (the team ended),
// 404 `not_found` (gone altogether). Anything else is a form-level failure.
import { ApprovalApiError, send } from './approval-api'
import { descriptorFor } from './unattended-api'

export const APPEARANCE_PATH = '/api/team/appearance'
export const APPEARANCE_TIMEOUT_MS = 10_000

export interface AppearanceValues {
  name: string
  label: string
  /** 0-7, or null for automatic. */
  color: number | null
}

export type AppearanceField = 'name' | 'label' | 'color'

export type AppearanceResult =
  | { ok: true }
  | { ok: false; kind: 'field'; field: AppearanceField; message: string }
  | { ok: false; kind: 'form'; message: string }
  | { ok: false; kind: 'ended' }

const FIELD_OF: ReadonlyArray<[prefix: string, field: AppearanceField]> = [['team_name', 'name'], ['team_label', 'label'], ['team_color', 'color']]

/** Which field a 400's detail is about, from the field name it starts with; null when it names none. */
export function fieldOfDetail(detail: string): AppearanceField | null {
  for (const [prefix, field] of FIELD_OF) if (detail.startsWith(prefix)) return field
  return null
}

/** Send the three values to the team's lead host; the outcome as data, never a throw. */
export async function saveAppearance(hostId: string, teamId: string, v: AppearanceValues): Promise<AppearanceResult> {
  try {
    const client = await descriptorFor(hostId)
    await send<unknown>(hostId, APPEARANCE_PATH, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ team_id: teamId, team_name: v.name, team_label: v.label, team_color: v.color, client }),
      signal: AbortSignal.timeout(APPEARANCE_TIMEOUT_MS), // a request that never answers must not leave the popover stuck
    })
    return { ok: true }
  } catch (e: unknown) {
    if (!(e instanceof ApprovalApiError)) return { ok: false, kind: 'form', message: e instanceof Error ? e.message : String(e) }
    if (e.status === 409 && e.code === 'not_live') return { ok: false, kind: 'ended' }
    if (e.status === 404 && e.code === 'not_found') return { ok: false, kind: 'ended' }
    const message = e.detail !== '' ? e.detail : e.code
    if (e.status === 400) {
      const field = fieldOfDetail(e.detail)
      if (field !== null) return { ok: false, kind: 'field', field, message }
    }
    return { ok: false, kind: 'form', message }
  }
}
