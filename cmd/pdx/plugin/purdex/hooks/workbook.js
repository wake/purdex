// Purdex mod — the session workbook's job executor, the pure half (session workbook spec §5.1).
//
// The daemon hands this mod a summariser job (`POST /mod/v1/workbook/next`), the mod runs the model call in the session's
// own process (`$.model.complete`) and reports what happened (`POST /mod/v1/workbook/result`). The daemon keeps every
// decision — the input, the order, the prompt, the validation; this file only maps shapes. Nothing here touches `$`:
// M-U1-2 says `$` is followed only into functions declared in events.js, so the parts that call `$` live there and the
// plain data conversions live here, where a test can reach them.

export const NEXT_URL = 'http://pdx/mod/v1/workbook/next'
export const RESULT_URL = 'http://pdx/mod/v1/workbook/result'
export const CAPS = ['workbook.v2'] // announced on every events batch; no `workbook.refresh` — this mod runs no refresh job
export const WAIT_MS = 15_000 // the long poll after a main turn ends
export const REQUEST_DEADLINE_MS = 5000 // slack over the wait for a request to be answered ($.http.fetch has no timeout)
export const REASONS = new Set(['api-error', 'empty-reply', 'aborted'])

const isObject = (v) => !!v && typeof v === 'object' && !Array.isArray(v)
const num = (v) => (Number.isFinite(v) && v >= 0 ? Math.trunc(v) : 0)

// shouldAsk: a main-thread turn that ended in an answer or an error is a turn the daemon may have a job for. A subagent's
// turn.complete (it carries an agentId), an interrupted turn and the like are not.
export function shouldAsk(e) {
  return !!e && !e.agentId && (e.reason === 'answer' || e.reason === 'error')
}

// nextBody is the request of `next`.
export function nextBody(stream, sessionId, waitMs) {
  return JSON.stringify({ stream, session_id: sessionId, wait_ms: waitMs })
}

// parseNext reads the answer of `next`: the job, or null (204, an error, anything that is not a well-formed job).
export function parseNext(res) {
  if (!res || res.status !== 200) return null
  let o
  try { o = JSON.parse(res.text) } catch { return null }
  const job = isObject(o) ? o.job : null
  if (!isObject(job) || typeof job.id !== 'string' || !job.id || typeof job.kind !== 'string') return null
  return job
}

// completeRequest maps a job's `complete` to the API's keys (max_tokens → maxTokens, timeout_ms → timeoutMs; the system
// blocks keep their cache marks). Null when the job has nothing to run.
export function completeRequest(c) {
  if (!isObject(c) || typeof c.model !== 'string' || typeof c.prompt !== 'string') return null
  const req = { model: c.model, prompt: c.prompt }
  if (Array.isArray(c.system)) {
    req.system = c.system.filter((b) => isObject(b) && typeof b.text === 'string').map((b) => (b.cache ? { text: b.text, cache: true } : { text: b.text }))
  }
  if (Number.isInteger(c.max_tokens)) req.maxTokens = c.max_tokens
  if (typeof c.effort === 'string') req.effort = c.effort
  if (Number.isInteger(c.timeout_ms)) req.timeoutMs = c.timeout_ms
  return req
}

// usageOf maps the API's usage to the daemon's.
export function usageOf(u) {
  const o = isObject(u) ? u : {}
  return { input: num(o.input_tokens), output: num(o.output_tokens), cache_read: num(o.cache_read_input_tokens) }
}

// resultBody is the request of `result` for a finished call: r is `$.model.complete`'s result, or null when the call was
// refused (it rejected). A reason the daemon does not know is reported as `api-error` rather than guessed at.
export function resultBody(stream, jobId, r, latencyMs) {
  const body = { stream, job_id: jobId, latency_ms: num(latencyMs) }
  if (!isObject(r)) return JSON.stringify({ ...body, answered: false, reason: 'refused', usage: usageOf(null) })
  if (r.isAnswered === true) return JSON.stringify({ ...body, answered: true, text: String(r.text ?? ''), usage: usageOf(r.usage) })
  const out = { ...body, answered: false, reason: REASONS.has(r.reason) ? r.reason : 'api-error', usage: usageOf(r.usage) }
  if (Number.isInteger(r.status)) out.status = r.status
  if (typeof r.error === 'string') out.error = r.error
  return JSON.stringify(out)
}

// refusedBody answers a job this mod cannot run (a refresh, a kind it does not know).
export function refusedBody(stream, jobId) {
  return resultBody(stream, jobId, null, 0)
}

// moreOf reads the answer of `result`: whether another job of the conversation is ready (a 200 {"more": true}).
export function moreOf(res) {
  if (!res || res.status !== 200) return false
  try { return JSON.parse(res.text)?.more === true } catch { return false }
}
