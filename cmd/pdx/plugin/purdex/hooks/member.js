// Purdex mod — member relay, the pure half (plan v3 P6-6; spec §8.2 steps 3–8, M1, M28).
//
// A lead asks a member to relay with a control message: a peer message whose text carries
// `[pdx-relay:control] op=<uuid>`. register.js (which owns the shared state and every `$` call)
//   - takes it in `session.receive`, always, busy or idle: the model never sees it;
//   - tells the daemon from a timer that the mod has it (`pdx relay seen`, Q2 branch A);
//   - claims the op only when no main-conversation turn runs: at once when idle, else at that
//     turn's turn.complete (codex finding 1), then takes the write path a self relay takes after
//     an approval: write → check → fix ×2 → written → /clear → seed → done.
//   - a claim that fails does nothing: the daemon's own timeout reports it.
//
// The mod's static rules never follow `$` across an import (Q3 deviation: the receive hook and
// the claim cannot live here), so this file holds what needs no `$`: reading the control message,
// checking a claim's answer, and writing the team facts of the write prompt.

const CONTROL_PREFIX = '[pdx-relay:control] op=' // team.RelayControlPrefix
const UUID = '[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}'
const CONTROL_RE = new RegExp(CONTROL_PREFIX.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '(' + UUID + ')')

export const CONSUMED = 'pdx relay control message' // the reason logged for a consumed delivery

// controlOp is the op id of a control message, '' for any other delivery. Only a peer's text counts
// (M1): the same words typed by the user, or in a tool result, are ordinary text.
export function controlOp(e) {
  if (!e || !e.origin || e.origin.kind !== 'peer' || typeof e.text !== 'string') return ''
  const m = CONTROL_RE.exec(e.text)
  return m ? m[1] : ''
}

// claimOp is the op of a claim's answer (`pdx relay claim` stdout), undefined when it is not one.
export function claimOp(body) {
  const op = body && body.op
  return op && typeof op.id === 'string' && typeof op.handoff_path === 'string' ? op : undefined
}

// leadLine is a member's `{{team}}` (write prompt tail §8.2 step 4), starting with a newline since the
// placeholder ends the whoami line. `clean` makes third-party text safe (register.js cleanGit).
export function leadLine(lead, clean) {
  return '\n- 我的 lead：' + clean(String(lead.address)) + '（ref ' + clean(String(lead.ref)) + '，team ' + clean(String(lead.team_id)) + '）'
}

// rosterLines is a lead's `{{team}}` from `pdx team --json`'s answer (undefined when it was unreadable).
// Titles and directories are third-party text: cleaned, one line each.
export function rosterLines(view, clean) {
  if (!view || typeof view !== 'object') return '\n- 我管理的 members：（讀不到，接手後請用 pdx team 查）'
  const members = Array.isArray(view.members) ? view.members : []
  if (members.length === 0) return '\n- 我管理的 members：無'
  const one = (v) => clean(String(v ?? '')).replace(/\s+/g, ' ').trim()
  return '\n- 我管理的 members：' + members.map((m) => '\n  - ' + one(m.address) + '（ref ' + one(m.ref) + '，title ' + (one(m.title) || '無') + '，cwd ' + one(m.cwd) + '）').join('')
}
