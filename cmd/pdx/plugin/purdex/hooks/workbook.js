// Purdex mod — the session workbook's job executor, the pure half (session workbook spec §5.1).
//
// The daemon hands this mod a summariser job (`POST /mod/v1/workbook/next`), the mod runs the model call in the session's
// own process (`$.model.complete`) and reports what happened (`POST /mod/v1/workbook/result`). The daemon keeps every
// decision — the input, the order, the prompt, the validation; this file only maps shapes. Nothing here touches `$`:
// M-U1-2 says `$` is followed only into functions declared in events.js, so the parts that call `$` live there and the
// plain data conversions live here, where a test can reach them.

export const NEXT_URL = 'http://pdx/mod/v1/workbook/next'
export const RESULT_URL = 'http://pdx/mod/v1/workbook/result'
export const REFRESH_URL = 'http://pdx/mod/v1/workbook/refresh'
export const CAPS = ['workbook.v2', 'workbook.refresh', 'prompt.v1'] // announced on every events batch: turn / re-write jobs, the refresh fork, and the Apps' send / interrupt
export const WAIT_MS = 15_000 // the long poll after a main turn ends
export const REQUEST_DEADLINE_MS = 5000 // slack over the wait for a request to be answered ($.http.fetch has no timeout)
export const REASONS = new Set(['api-error', 'empty-reply', 'aborted', 'nothing-to-fork'])
export const MODEL_SLACK_MS = 5000 // a call that outlives its own timeout_ms by this much is cut by the mod — and the
// daemon's lease lasts timeout_ms + 10 s, so the abort report still has 5 s to arrive before the lease runs out
export const DEFAULT_TIMEOUT_MS = 30_000 // the deadline of a job that names none
export const DEFAULT_FORK_TIMEOUT_MS = 90_000 // the deadline of a fork job that names none (the daemon names 90 s)
export const MAX_JOBS_PER_DRAIN = 8 // jobs one run of the loop takes before it stops and waits for the next trigger

// Bounds on a job from the daemon (fail closed: a job outside them is answered `refused`, the model is not called).
const MODEL_RE = /^[a-z0-9][a-z0-9._-]{0,63}$/
const MAX_TEXT = 200_000 // characters of the prompt, and of each system block
const MAX_BLOCKS = 8
const MAX_TOKENS = 4096 // the daemon's contract; 4096 tokens is far below the 64 KiB the result route takes as text
const MAX_TIMEOUT_MS = 120_000
const MAX_FORK_TIMEOUT_MS = 300_000 // a fork sends the whole conversation
const EFFORTS = new Set(['low', 'medium', 'high', 'xhigh', 'max'])

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
  if (!isObject(c) || typeof c.model !== 'string' || !MODEL_RE.test(c.model) || typeof c.prompt !== 'string' || c.prompt.length > MAX_TEXT) return null
  const req = { model: c.model, prompt: c.prompt }
  if (c.system !== undefined) {
    // fail closed: a system that is not a short list of text blocks is a daemon this mod does not understand
    if (!Array.isArray(c.system) || c.system.length > MAX_BLOCKS) return null
    for (const b of c.system) if (!isObject(b) || typeof b.text !== 'string' || b.text.length > MAX_TEXT) return null
    req.system = c.system.map((b) => (b.cache ? { text: b.text, cache: true } : { text: b.text }))
  }
  if (c.max_tokens !== undefined) {
    if (!Number.isInteger(c.max_tokens) || c.max_tokens < 1 || c.max_tokens > MAX_TOKENS) return null
    req.maxTokens = c.max_tokens
  }
  if (c.effort !== undefined) {
    if (!EFFORTS.has(c.effort)) return null
    req.effort = c.effort
  }
  if (c.timeout_ms !== undefined) {
    if (!Number.isInteger(c.timeout_ms) || c.timeout_ms < 1000 || c.timeout_ms > MAX_TIMEOUT_MS) return null
    req.timeoutMs = c.timeout_ms
  }
  return req
}

// forkRequest reads a refresh job's `fork` ({prompt, timeout_ms}): the request of $.model.fork (just the prompt — the fork
// sends the conversation itself) and the deadline the mod keeps. Null when the job has nothing this mod will run.
export function forkRequest(f) {
  if (!isObject(f) || typeof f.prompt !== 'string' || !f.prompt || f.prompt.length > MAX_TEXT) return null
  if (f.timeout_ms !== undefined && (!Number.isInteger(f.timeout_ms) || f.timeout_ms < 1000 || f.timeout_ms > MAX_FORK_TIMEOUT_MS)) return null
  return { req: { prompt: f.prompt }, timeoutMs: f.timeout_ms ?? DEFAULT_FORK_TIMEOUT_MS }
}

// refreshBody is the request of the daemon's refresh route (the /workbook refresh command).
export function refreshBody(stream, sessionId) {
  return JSON.stringify({ stream, session_id: sessionId })
}

// refreshNotice turns the daemon's answer to a refresh request into the one line the person sees, and says whether the job
// was queued (then the mod asks `next` at once).
export function refreshNotice(res) {
  if (!res) return { queued: false, text: '工作簿重整：沒有連上 daemon，沒有排入。' }
  let code = ''
  try { code = String(JSON.parse(res.text)?.error ?? '') } catch {}
  if (res.status === 202) return { queued: true, text: '工作簿重整：已排入，稍後會更新「目前狀況」與待辦。' }
  if (res.status === 409 && code === 'refresh_pending') return { queued: false, text: '工作簿重整：上一次重整還在進行，請稍後。' }
  if (res.status === 409) return { queued: false, text: '工作簿重整：目前沒有可執行重整的 session（daemon 或這個 mod 還不支援）。' }
  return { queued: false, text: '工作簿重整：失敗（daemon 回應 ' + res.status + '）。' }
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

// refusedBody answers a job this mod cannot run (a kind it does not know, a job outside the bounds).
export function refusedBody(stream, jobId) {
  return resultBody(stream, jobId, null, 0)
}

// moreOf reads the answer of `result`: whether another job of the conversation is ready (a 200 {"more": true}).
export function moreOf(res) {
  if (!res || res.status !== 200) return false
  try { return JSON.parse(res.text)?.more === true } catch { return false }
}

// ---- the prompt queue (interface U3 plan D7): the Apps' send and interrupt, run through this session's own process ----

export const PROMPT_NEXT_URL = 'http://pdx/mod/v1/prompt/next'
export const PROMPT_RESULT_URL = 'http://pdx/mod/v1/prompt/result'
export const PROMPT_WAIT_MS = 15_000 // the standing long poll
export const PROMPT_IDLE_MS = 1000 // a poll that comes back sooner than this with nothing waits this long before the next
export const PROMPT_BACKOFF_MS = 2000 // after a failed poll
export const PROMPT_RESULT_TRIES = 3 // a result that gets no 200 / 409 is sent again, 0.7 s and 1.4 s later (inside the daemon's 10 s)
export const PROMPT_RESULT_RETRY_MS = 700
export const PROMPT_RESULT_POST_MS = 2000 // one try of a result waits at most this long
export const PROMPT_RESULT_BUDGET_MS = 6500 // no try starts later than this after the job arrived (the daemon's lease is 10 s)
const MAX_PROMPT_TEXT = 8000 // characters; the daemon sends at most 4000 bytes
const JOB_ID_RE = /^pj-[0-9a-f]{32}$/
const SID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/

// promptNextBody is the request of `prompt/next` for the session this process is running now.
export function promptNextBody(stream, sessionId, waitMs) {
  return JSON.stringify({ stream, session_id: sessionId, wait_ms: waitMs })
}

// parsePromptJob reads the answer of `prompt/next`: the job, or null (204, an error, anything that is not a well-formed
// job). Fail closed: a job outside the bounds is not run.
export function parsePromptJob(res) {
  if (!res || res.status !== 200) return null
  let o
  try { o = JSON.parse(res.text) } catch { return null }
  const j = isObject(o) ? o.job : null
  if (!isObject(j) || typeof j.id !== 'string' || !JOB_ID_RE.test(j.id) || typeof j.session_id !== 'string' || !SID_RE.test(j.session_id)) return null
  if (j.kind === 'interrupt') return { id: j.id, kind: 'interrupt', sessionId: j.session_id, text: '' }
  if (j.kind === 'submit' && typeof j.text === 'string' && j.text.trim() !== '' && j.text.length <= MAX_PROMPT_TEXT) {
    return { id: j.id, kind: 'submit', sessionId: j.session_id, text: j.text }
  }
  return null
}

const MAX_REASON_BYTES = 120 // the daemon refuses a reason over 128 BYTES: cut by UTF-8 length, on a character boundary

// cutUtf8 keeps the head of s whose UTF-8 encoding fits in max bytes, never splitting a character.
export function cutUtf8(s, max) {
  let bytes = 0
  let out = ''
  for (const ch of s) { // iterates code points
    const cp = ch.codePointAt(0)
    const n = cp < 0x80 ? 1 : cp < 0x800 ? 2 : cp < 0x10000 ? 3 : 4
    if (bytes + n > max) break
    bytes += n
    out += ch
  }
  return out
}

// promptResultBody is the request of `prompt/result`: status accepted | dropped | busy, and for dropped the reason.
export function promptResultBody(stream, jobId, status, reason) {
  const b = { stream, job_id: jobId, status }
  if (reason) b.reason = cutUtf8(String(reason), MAX_REASON_BYTES)
  return JSON.stringify(b)
}
