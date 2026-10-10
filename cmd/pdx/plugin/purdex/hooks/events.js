// Purdex mod — the event reporter (interface U1 spec §6.2–§6.5).
//
// Every interactive session whose pdx.json names the daemon's `mod_socket` reports what the
// engine does to the daemon over that Unix socket: one stream per mod load (a random id),
// events stamped with a strictly increasing seq, queued, and POSTed in batches to
// `/mod/v1/events` from a $.clock.after timer — never inside a hook — one request at a time,
// retried with backoff until the daemon acks them. A heartbeat every 10 s carries the live
// mirror (the running main turn, open asks, compacting, the agents, the last main turn's
// error outcome, the last background tasks) and every batch says where the session runs
// and that it is interactive, so a daemon that restarts or a batch that is lost converges
// within one beat. `session.end` flushes inside its hook. Headless runs (`claude -p`, a
// Nexen worker) report nothing.
//
// The hooks only observe: each calls next(e) and returns what it resolved to; nothing here
// ever changes what the engine does. Every registration carries `.catch(($, e, next) =>
// next(e))`: in a `.catch` handler `next` is replay-safe (d.ts CatchHandler / Caught) — a
// hook that throws after its next(e) settled gets that same settlement back and nothing
// beneath runs again. Enqueueing never throws.
//
// Loading rules this file is shaped by (measured on Claude Code 2.1.293, spec §3):
// - M-U1-2: `$` is followed only into a function declared at the top level of THIS file,
//   never across an import. So every helper that takes `$` lives here, every timer callback
//   is an arrow that only calls one of them (`$.clock.after(150, () => flushTick($))`), and
//   register.js cannot call into this file with its `$` — which is why the events
//   register.js owns are observed below by hooks of this file's own, with a matcher.
// - M-U1-3: an event may be registered without a matcher once per plugin. register.js owns
//   `session.start`, `turn.start`, `turn.complete`, `classic.SessionStart`, `prompt.submit`
//   and `session.compact` unmatched; here those carry a matcher (one with and one without
//   load, the first registered outermost — registerEvents runs before register.js's own, so
//   these wrap the relay's), and only `tool.call`, `tool.check`, `agent.spawn`,
//   `session.measure`, `session.end` and `classic.Stop` are unmatched. `ui.render` carries
//   a matcher too (`component: 'ToolUse'`). A Go test over the embedded files keeps it so
//   (cmd/pdx/plugin/embed_test.go).

import { CAPS, DEFAULT_TIMEOUT_MS, MAX_JOBS_PER_DRAIN, MODEL_SLACK_MS, NEXT_URL, PROMPT_BACKOFF_MS, PROMPT_IDLE_MS, PROMPT_NEXT_URL, PROMPT_RESULT_BUDGET_MS, PROMPT_RESULT_POST_MS, PROMPT_RESULT_RETRY_MS, PROMPT_RESULT_TRIES, PROMPT_RESULT_URL, PROMPT_WAIT_MS, parsePromptJob, promptNextBody, promptResultBody, REFRESH_URL, RESULT_URL, REQUEST_DEADLINE_MS, WAIT_MS, completeRequest, forkRequest, moreOf, nextBody, parseNext, refreshBody, refreshNotice, refusedBody, resultBody, shouldAsk } from './workbook.js'

const URL = 'http://pdx/mod/v1/events' // the host is not read; the socket is the address
const FLUSH_MS = 150 // a flush goes out this long after the first event queued
const BATCH_MAX = 200 // events per POST
const FINAL_MAX = 500 // the session.end flush sends at most the newest this many (the daemon's cap)
const QUEUE_MAX = 1000 // queued events; one more drops the oldest
const POST_DEADLINE_MS = 5000 // a POST not answered by then failed ($.http.fetch has no timeout)
const BACKOFF_MIN_MS = 1000
const BACKOFF_MAX_MS = 30_000
const HEARTBEAT_MS = 10_000
const STREAM_CHARS = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-' // 64: one per 6 bits
const MONITORS_MAX = 64 // monitor ids kept at once; one more drops the oldest
const ASK_TOOLS = new Set(['AskUserQuestion', 'ExitPlanMode']) // tools that wait on the person by themselves
const TIMEOUT = Symbol('timeout')
const TEAM_URL = 'http://pdx/mod/v1/team' // GET ?session_id=<sid> on the same socket (TI-5a)
const ORPHAN_MAX_MS = 600_000 // how long a fork past its deadline holds the workbook executor at most
const TEAM_MS = 15_000 // how often the lead's member count is read

// team is the last good answer of the daemon's team read for the CURRENT session id. The ui.render hook below only
// looks at it (never at the socket); a change asks for a redraw. `gen` counts the session changes: an answer that left
// before one and lands after it belongs to the old session and is dropped.
const team = { good: false, role: 'none', members: 0, gen: 0, timer: null, pending: -1 }

// wb is the workbook job executor's state (session workbook spec §5.1): one job at a time. `busy` is the loop that polls the
// daemon and runs what it hands out; a trigger that comes while it runs only sets `again`. `gen` counts the session ends
// and switches, so a loop that began before one stops asking. `wait` is the longest poll that is asked for next.
// `orphan` is a fork that outlived its deadline: $.model.fork takes no signal, so it cannot be cut and keeps spending the
// whole conversation's tokens. While it runs no further model call is started (one job at a time is a cost promise, not
// only a state); when it settles the executor asks again.
const wb = { busy: false, again: false, scheduled: false, gen: 0, wait: 0, orphan: false }

// pq is the Apps' send / interrupt loop (interface U3 plan D7): one standing long poll of `prompt/next` per session id.
// `loop` is the wb.gen it runs under (0: none), so a session end or switch ends it and the new session starts its own.
const pq = { loop: 0 }

// ev is the reporter's whole state; one per mod load. `stream` and `seq` live as long as the
// load (a /clear or a resume goes on in the same stream), the queue holds every event not yet
// acked, and droppedTotal counts the events this stream lost — never reset (spec §6.2).
const ev = {
  on: false,
  sock: '',
  stream: '',
  seq: 0,
  sid: '',
  cwd: '', // where the session runs: from session.start, kept across a /clear or a resume
  ccVersion: '',
  modVersion: '',
  queue: [],
  droppedTotal: 0,
  inflight: false, // a POST is out
  scheduled: false, // a flush timer (the 150 ms one, or a backoff) is pending
  backoffMs: 0,
  turnId: '', // the running main turn
  closed: [], // the ids of the last few main turns that ended (see rememberClosed)
  // tool_use_id → 'permission' | 'question': what waits on the person. A question
  // (AskUserQuestion / ExitPlanMode) leaves at its tool.end; a permission ask leaves too
  // when its ToolUse row starts running (tool.approved).
  asks: new Map(),
  compacting: false, // the main conversation is compacting
  lastError: false, // the last main turn ended in error (cleared by the next main turn)
  background: null, // {tasks, crons} as the last classic.Stop listed them; null before one
  // id → whether a classic.Stop has listed it yet, for the background tasks the Monitor tool started (its tool.call
  // result's taskId). Claude Code lists such a task as type "shell" (the "monitor" type is an MCP watch), so the
  // daemon would draw nothing for it; the background event this reporter sends names those ids "monitor" instead
  // (spec §7 "Background symbol"). An id leaves when a Stop no longer lists it after having listed it (the task
  // ended, so a reused id is a different task), at the cap (the oldest), and when the session ends or switches;
  // rebuilt only by new Monitor calls, so after a mod reload a task that is still running stays "shell" until it
  // is started again (known limit).
  monitors: new Map(),
  beat: null, // the heartbeat timer
  beatGen: 0, // bumped when the heartbeat stops or pauses: a beat begun before then lands nowhere
  switching: false, // from session.end{clear|resume} to session.switch: the heartbeat pauses
}

const parse = (s) => { try { return JSON.parse(s) } catch { return null } }
const isObject = (v) => !!v && typeof v === 'object' && !Array.isArray(v)

function log($, text) {
  try {
    const p = $.ui.log('pdx-events: ' + text, { to: 'debug' })
    if (p && typeof p.catch === 'function') p.catch(() => {})
  } catch {}
}

// newStream mints the stream id: 22 characters of [A-Za-z0-9_-] from Web Crypto
// (132 bits); '' when the environment has none (the reporter then stays off).
function newStream() {
  const c = globalThis.crypto
  if (!c || typeof c.getRandomValues !== 'function') return ''
  const b = new Uint8Array(22)
  c.getRandomValues(b)
  return Array.from(b, (x) => STREAM_CHARS[x & 63]).join('')
}

// ---- the queue and the flush ----

// enqueue stamps one event and schedules a flush. Never throws: the reporter's failures stay
// the reporter's.
function enqueue($, type, data, sid) {
  try {
    if (!ev.on) return
    ev.queue.push({ seq: ++ev.seq, at: Date.now(), sid: sid || ev.sid, type, data })
    while (ev.queue.length > QUEUE_MAX) {
      ev.queue.shift()
      ev.droppedTotal += 1
    }
    schedule($, FLUSH_MS)
  } catch (err) {
    log($, 'enqueue ' + type + ' failed: ' + String(err))
  }
}

// schedule starts the flush timer unless one is pending, a POST is out (its answer schedules
// the next) or nothing is queued.
function schedule($, ms) {
  if (ev.scheduled || ev.inflight || ev.queue.length === 0) return
  ev.scheduled = true
  $.clock.after(ms, () => flushTick($))
}

function flushTick($) {
  ev.scheduled = false
  void flush($).catch((err) => log($, 'flush failed: ' + String(err)))
}

function body(events) {
  return JSON.stringify({
    v: 1,
    stream: ev.stream,
    agent: 'cc',
    cc_version: ev.ccVersion,
    mod_version: ev.modVersion,
    dropped_total: ev.droppedTotal,
    cwd: ev.cwd,
    interactive: true, // the reporter runs only for interactive sessions
    caps: CAPS, // what this mod can run besides reporting (the workbook's turn and re-write jobs)
    events,
  })
}

function post($, events) {
  return $.http.fetch(URL, { method: 'POST', headers: { 'content-type': 'application/json' }, body: body(events), socketPath: ev.sock })
}

// postWithDeadline races one POST against POST_DEADLINE_MS: { res }, { err }, or TIMEOUT. A
// POST that misses the deadline is left to itself; its late answer is never read (the events
// it carried are still queued and go again; the daemon drops what it already applied).
async function postWithDeadline($, events) {
  let timer = null
  const deadline = new Promise((resolve) => { timer = $.clock.after(POST_DEADLINE_MS, () => resolve(TIMEOUT)) })
  try {
    return await Promise.race([post($, events).then((res) => ({ res }), (err) => ({ err })), deadline])
  } finally {
    if (timer) timer.cancel()
  }
}

// hintOf reads the daemon's `workbook: true` on an events answer: a job of this session's conversation waits that nobody
// holds, so the mod asks `next`.
function hintOf(res) {
  const o = parse(res.text)
  return isObject(o) && o.workbook === true
}

// ackOf reads the daemon's {"ack": N}; undefined for anything else.
function ackOf(res) {
  const o = parse(res.text)
  return isObject(o) && Number.isFinite(o.ack) ? o.ack : undefined
}

// dropThrough removes every queued event with seq ≤ n and returns how many it removed.
function dropThrough(n) {
  const before = ev.queue.length
  ev.queue = ev.queue.filter((e) => e.seq > n)
  return before - ev.queue.length
}

// apply takes one POST's outcome for the batch it carried: true when the daemon answered for
// it (200 with an ack, or a refusal of the batch), false for a failure to retry.
function apply(outcome, batch, $, ask) {
  const res = outcome && outcome !== TIMEOUT ? outcome.res : undefined
  if (!res) return false
  if (res.status === 200) {
    const ack = ackOf(res)
    if (ack === undefined) return false
    dropThrough(ack)
    if (ask && hintOf(res)) wbAsk($, 0) // a job waits: ask at once (a refresh needs no turn to end)
    return true
  }
  // 400: the daemon refused the batch as written (a bad event, a bad sid…): sending it again
  // would be refused again (no poison loop), so it is dropped and counted. 413 is the same.
  if (res.status === 400 || res.status === 413) {
    ev.droppedTotal += dropThrough(batch[batch.length - 1].seq)
    return true
  }
  return false // 503 registry_full, any other status: retry
}

// flush sends one batch (one request in flight at a time) and schedules the next: 150 ms
// later after an answer, after the backoff after a failure.
async function flush($) {
  if (ev.inflight || ev.queue.length === 0) return
  const batch = ev.queue.slice(0, BATCH_MAX)
  const wbGen = wb.gen
  ev.inflight = true
  let outcome
  try {
    outcome = await postWithDeadline($, batch)
  } finally {
    ev.inflight = false
  }
  // A hint that comes back after the session ended or switched is for a session that is gone: it asks nothing.
  if (apply(outcome, batch, $, wbGen === wb.gen)) {
    ev.backoffMs = 0
    schedule($, FLUSH_MS)
    return
  }
  ev.backoffMs = Math.min(Math.max(BACKOFF_MIN_MS, ev.backoffMs * 2), BACKOFF_MAX_MS)
  schedule($, ev.backoffMs)
}

// finalFlush is the session.end flush, awaited inside its hook: one POST of every queued
// event — the newest FINAL_MAX, older ones counted as dropped — whatever is in flight or
// backing off (an in-flight batch's events are still queued, so its late arrival is all
// duplicates). No deadline: the session.end bound (1.5 s) cuts it.
async function finalFlush($) {
  if (ev.queue.length === 0) return
  if (ev.queue.length > FINAL_MAX) {
    const cut = ev.queue.length - FINAL_MAX
    ev.queue = ev.queue.slice(cut)
    ev.droppedTotal += cut
  }
  const batch = ev.queue.slice()
  let outcome
  try {
    outcome = { res: await post($, batch) }
  } catch (err) {
    outcome = { err }
  }
  apply(outcome, batch, $, false) // the session is ending: no more asking
}

// ---- the heartbeat ----

function beatTick($) {
  void beat($).catch((err) => log($, 'heartbeat failed: ' + String(err)))
}

// beat reads the agents and queues one heartbeat — unless the heartbeat stopped or paused
// while it waited on $.agent.list (a session.end, a new session.start): stopping cancels the
// timer, not a beat already past its start, so that one checks again on its way out. While
// a /clear or a resume switches the session id, no beat goes out: it would carry the old id.
async function beat($) {
  if (!ev.on || ev.switching) return
  const gen = ev.beatGen
  let agents // left out when the list fails: [] would clear every dot until the next beat
  try {
    agents = (await $.agent.list()).map((a) => ({ id: a.id, status: a.status }))
  } catch {}
  if (gen !== ev.beatGen) return
  const data = { asks: [...ev.asks.keys()], compacting: ev.compacting, error: ev.lastError }
  if (agents) data.agents = agents
  if (ev.turnId) data.turn_id = ev.turnId
  if (ev.background) data.background = ev.background
  enqueue($, 'heartbeat', data)
}

function stopBeat() {
  if (ev.beat) ev.beat.cancel()
  ev.beat = null
  ev.beatGen += 1
}

// ---- the workbook's jobs (session workbook spec §5.1) ----

// wbAsk asks the daemon for a job soon: from a timer, never inside a hook, and one loop at a time — a trigger that comes
// while the loop runs only marks that it must ask once more when it ends (with the longer wait, if either asked for one).
function wbAsk($, waitMs) {
  if (!ev.on) return
  wb.wait = Math.max(wb.wait, waitMs)
  if (wb.busy) {
    wb.again = true
    return
  }
  if (wb.scheduled) return
  wb.scheduled = true
  const gen = wb.gen
  $.clock.after(0, () => wbTick($, gen))
}

// wbTick: an ask scheduled before a session end or switch is stale and goes nowhere (the next trigger asks afresh).
function wbTick($, gen) {
  wb.scheduled = false
  if (gen !== wb.gen) return
  void wbLoop($).catch((err) => log($, 'workbook loop failed: ' + String(err)))
}

// wbRequest posts to the daemon with a deadline of its own ($.http.fetch has none): { res }, { err } or TIMEOUT.
async function wbRequest($, url, bodyText, waitMs, slackMs = REQUEST_DEADLINE_MS) {
  let timer = null
  const deadline = new Promise((resolve) => { timer = $.clock.after(waitMs + slackMs, () => resolve(TIMEOUT)) })
  try {
    const req = $.http.fetch(url, { method: 'POST', headers: { 'content-type': 'application/json' }, body: bodyText, socketPath: ev.sock })
    return await Promise.race([req.then((res) => ({ res }), (err) => ({ err })), deadline])
  } finally {
    if (timer) timer.cancel()
  }
}

// wbLoop asks, runs the job it is handed, reports it, and goes on while the daemon says another one is ready. Any failure
// ends the loop: a job that could not be reported is dropped (its lease runs out on the daemon, which fails the entry).
async function wbLoop($) {
  if (wb.busy || !ev.on || wb.orphan) return
  wb.busy = true
  const gen = wb.gen
  try {
    // A drain takes at most MAX_JOBS_PER_DRAIN jobs and never the same job id twice: a daemon that keeps answering
    // "more" (or hands the same lease again) cannot make this mod spend the person's model quota without end. The next
    // trigger (a turn end, the heartbeat's hint) starts another drain.
    const seen = new Set()
    for (let n = 0; n < MAX_JOBS_PER_DRAIN; n++) {
      if (wb.orphan) break // a fork is still running past its deadline: wait for it to settle (wbOrphaned asks again)
      const waitMs = wb.wait
      wb.wait = 0
      wb.again = false
      const out = await wbRequest($, NEXT_URL, nextBody(ev.stream, ev.sid, waitMs), waitMs)
      if (gen !== wb.gen || !ev.on || !out || out === TIMEOUT || !out.res) return
      const job = parseNext(out.res)
      if (!job || seen.has(job.id)) break // 204 (nothing yet), an error, or a job already taken in this drain
      seen.add(job.id)
      const more = await wbRun($, job, gen)
      if (more !== true) break
    }
  } finally {
    wb.busy = false
    if (wb.again && ev.on) { // a trigger came while the loop ran (or the session moved on under it): ask once more
      wb.again = false
      wbAsk($, 0)
    }
  }
}

// wbRun runs one job and reports it; true when the daemon said another job is ready. A turn or re-write job is one
// $.model.complete; a refresh job is one $.model.fork (the conversation itself, a cache read while the main thread's cache is
// warm); a kind it does not know is answered `refused`. A call the engine refuses rejects: that is reported as `refused` too.
async function wbRun($, job, gen) {
  // gen only decides whether to go on asking afterwards (see the end)
  const jobId = job.id
  let bodyText
  const fork = job.kind === 'refresh' ? forkRequest(job.fork) : null
  const req = job.kind === 'turn' || job.kind === 'rewrite' ? completeRequest(job.complete) : fork ? fork.req : null
  if (!req) {
    bodyText = refusedBody(ev.stream, jobId)
  } else {
    const t0 = await $.clock.now()
    // The mod owns a deadline of its own: a call that never settles (or outlives its timeout_ms) must not hold the
    // executor for good. It is aborted and reported `aborted`; the daemon reads that as a timeout.
    const ctl = typeof AbortController === 'function' ? new AbortController() : null
    let cut = null
    const deadline = new Promise((resolve) => {
      cut = $.clock.after((fork ? fork.timeoutMs : req.timeoutMs ?? DEFAULT_TIMEOUT_MS) + MODEL_SLACK_MS, () => {
        if (ctl) ctl.abort()
        resolve({ isAnswered: false, reason: 'aborted', usage: {} })
      })
    })
    let r = null
    try {
      // $.model.fork takes no signal: the deadline above reports a fork that outlives it, the call itself runs on
      const call = fork ? $.model.fork(req) : ctl ? $.model.complete(req, { signal: ctl.signal }) : $.model.complete(req)
      let forkDone = false
      if (fork) call.then(() => { forkDone = true }, () => { forkDone = true })
      r = await Promise.race([call, deadline])
      if (fork && !forkDone) wbOrphaned($, call)
    } catch (err) {
      log($, 'workbook call refused: ' + String(err))
    } finally {
      if (cut) cut.cancel()
    }
    bodyText = resultBody(ev.stream, jobId, r, (await $.clock.now()) - t0)
  }
  // The result is reported even if the session moved on meanwhile: the lease belongs to this stream, not to a session id.
  const out = await wbRequest($, RESULT_URL, bodyText, 0)
  if (!out || out === TIMEOUT || !out.res) return false
  return gen === wb.gen && moreOf(out.res)
}

// wbOrphaned: the race was won by the deadline while the fork was still running → hold the executor until it settles. Its
// late answer is dropped (the daemon has already been told `aborted`).
// A fork that never settles must not wedge the executor for good: the hold is given up after ORPHAN_MAX_MS (a call to the
// API ends long before that by itself) and the executor asks again. A mod reload forgets the hold (known limit).
function wbOrphaned($, call) {
  let done = false
  let cap = null
  const settle = () => {
    if (done) return
    done = true
    if (cap) cap.cancel()
    if (!wb.orphan) return
    wb.orphan = false
    wbAsk($, 0)
  }
  wb.orphan = true
  call.then(settle, settle)
  cap = $.clock.after(ORPHAN_MAX_MS, settle)
}

// ---- the Apps' send and interrupt (interface U3 plan D7) ----

// pqStart starts the standing poll for the current session id unless one runs under this generation. Called after the
// reporter is on and after a session switch; never inside a hook (it starts from a timer).
function pqStart($) {
  if (!ev.on || pq.loop === wb.gen) return
  const gen = wb.gen
  pq.loop = gen
  $.clock.after(0, () => {
    void pqLoop($, gen).catch((err) => log($, 'prompt loop failed: ' + String(err))).finally(() => { if (pq.loop === gen) pq.loop = 0 })
  })
}

// pqLoop polls `prompt/next` for the session this process runs now and runs what it is handed, one job at a time. A poll
// that fails waits PROMPT_BACKOFF_MS; one that returns empty at once (the daemon does not know this stream yet) waits
// PROMPT_IDLE_MS, so a daemon that answers fast never makes a busy loop.
async function pqLoop($, gen) {
  while (gen === wb.gen && ev.on) {
    const t0 = await $.clock.now()
    const out = await wbRequest($, PROMPT_NEXT_URL, promptNextBody(ev.stream, ev.sid, PROMPT_WAIT_MS), PROMPT_WAIT_MS)
    if (gen !== wb.gen || !ev.on) return
    const failed = !out || out === TIMEOUT || !out.res || (out.res.status !== 200 && out.res.status !== 204)
    const job = failed ? null : parsePromptJob(out.res)
    if (job) {
      await pqRun($, job)
      continue
    }
    if (failed) await $.clock.sleep(PROMPT_BACKOFF_MS)
    else if ((await $.clock.now()) - t0 < PROMPT_IDLE_MS) await $.clock.sleep(PROMPT_IDLE_MS)
  }
}

// pqRun runs one job and reports it. A submit goes only to the session it was made for (a /clear since then makes the id
// differ: dropped session_changed) and only while no turn runs: $.prompt.submit does not refuse mid-turn, it blocks until
// the session is idle, so a turn in progress is reported `busy` at once and the App sends again when it is idle. The text goes
// in as the person's own words (asUser), which is also how the transcript shows it. An interrupt aborts the running main turn.
async function pqRun($, job) {
  const t0 = await $.clock.now() // when the job arrived: the result's retries are bounded from here
  let status = 'accepted'
  let reason = ''
  try {
    // A /clear or resume in progress (session.end seen, the switch not yet) still reports the old id: nothing runs then
    if (ev.switching || job.sessionId !== String(await $.session.id())) {
      status = 'dropped'
      reason = 'session_changed'
    } else if (job.kind === 'submit') {
      if (ev.turnId) {
        status = 'busy'
      } else {
        const r = await $.prompt.submit({ text: job.text, asUser: true })
        if (isObject(r) && typeof r.drop === 'string') {
          status = 'dropped'
          reason = r.drop || 'dropped'
        }
      }
    } else if (!ev.turnId) {
      status = 'dropped'
      reason = 'not_running'
    } else {
      const turnId = ev.turnId
      await $.turn.abort({ turnId })
      turnAborted($, turnId)
    }
  } catch (err) {
    status = 'dropped'
    reason = job.kind === 'interrupt' ? 'not_running' : 'refused'
    log($, 'prompt job ' + job.kind + ' failed: ' + String(err))
  }
  // The job has run (or been refused): its report must get through. A lost answer or a busy daemon is tried again, a few
  // times; a 409 means the daemon has settled it already (expired, or the first try did arrive) and there is nothing to
  // add. The whole sequence is bounded from the moment the job arrived: no attempt starts after PROMPT_RESULT_BUDGET_MS
  // (the daemon's lease is 10 s) and each one waits at most PROMPT_RESULT_POST_MS, so the poll loop is held up for a
  // bounded time even when every POST hangs.
  for (let attempt = 0; attempt < PROMPT_RESULT_TRIES; attempt++) {
    if (attempt > 0) await $.clock.sleep(PROMPT_RESULT_RETRY_MS * attempt)
    if ((await $.clock.now()) - t0 >= PROMPT_RESULT_BUDGET_MS) break
    const out = await wbRequest($, PROMPT_RESULT_URL, promptResultBody(ev.stream, job.id, status, reason), 0, PROMPT_RESULT_POST_MS)
    const code = out && out !== TIMEOUT && out.res ? out.res.status : 0
    if (code === 200 || code === 409) return
  }
  log($, 'prompt result not accepted for ' + job.kind)
}

// ---- /workbook refresh (session workbook spec §5.6) ----

const WORKBOOK_USAGE = '用法：/workbook refresh — 依整段對話重整這個 session 的工作簿（目前狀況與待辦）。'

// workbookCommand answers `/workbook refresh`: it asks the daemon to queue the refresh and, once queued, asks `next` at
// once (from a timer: wbAsk never runs the loop inside the hook). The person waits for the line, so the one daemon call is
// awaited here, bounded by wbRequest's deadline.
async function workbookCommand($, e) {
  if (String(e.args ?? '').trim() !== 'refresh') return { text: WORKBOOK_USAGE }
  if (!ev.on) return { text: '工作簿重整：這個 session 沒有連上 daemon。' }
  if (wb.orphan) return { text: '工作簿重整：上一次重整的模型呼叫還在背景執行，請等它結束再試。' }
  const out = await wbRequest($, REFRESH_URL, refreshBody(ev.stream, ev.sid), 0)
  const notice = refreshNotice(out && out !== TIMEOUT ? out.res : null)
  if (notice.queued) wbAsk($, 0)
  return { text: notice.text }
}

// ---- the lead's footer (TI-5b, spec §4.10) ----

// teamLabel is the footer mode for a lead, '' for any other role and before the first good read.
function teamLabel() {
  if (!team.good || team.role !== 'lead') return ''
  return 'lead mode · ' + team.members + (team.members === 1 ? ' member' : ' members')
}

// teamAnswer reads the daemon's {"role","members","team_label"}; null for anything else.
function teamAnswer(res) {
  if (!res || res.status !== 200) return null
  const o = parse(res.text)
  if (!isObject(o) || !['lead', 'member', 'none'].includes(o.role)) return null
  if (!Number.isInteger(o.members) || o.members < 0) return null
  return { role: o.role, members: o.members }
}

// readTeam asks the daemon about the current session and keeps the answer. A failed or late read changes nothing (the last
// good value stands); an answer for a session that is gone is dropped. Only a change of the label asks for a redraw.
async function readTeam($) {
  if (!ev.on) return
  const gen = team.gen
  // One read per session at a time: $.http.fetch cannot be cancelled, so a daemon that takes a request and never answers
  // must not collect one more every tick. A switch is a new generation and may read again.
  if (team.pending === gen) return
  team.pending = gen
  const url = TEAM_URL + '?session_id=' + encodeURIComponent(ev.sid)
  let timer = null
  const deadline = new Promise((resolve) => { timer = $.clock.after(POST_DEADLINE_MS, () => resolve(TIMEOUT)) })
  const request = $.http.fetch(url, { method: 'GET', socketPath: ev.sock }).then((res) => ({ res }), (err) => ({ err }))
  // the slot is freed when the request itself ends, not at the deadline
  void request.finally(() => { if (team.pending === gen) team.pending = -1 })
  let out
  try {
    out = await Promise.race([request, deadline])
  } finally {
    if (timer) timer.cancel()
  }
  if (gen !== team.gen || !out || out === TIMEOUT) return
  const a = teamAnswer(out.res)
  if (!a) return
  const before = teamLabel()
  team.good = true
  team.role = a.role
  team.members = a.members
  if (teamLabel() !== before) $.ui.invalidate('ui.render')
}

function teamTick($) {
  void readTeam($).catch((err) => log($, 'team read failed: ' + String(err)))
}

// forgetTeam drops what was read for the session that is gone; the label goes with it.
function forgetTeam($) {
  team.gen += 1
  const had = teamLabel() !== ''
  team.good = false
  team.role = 'none'
  team.members = 0
  if (had) $.ui.invalidate('ui.render')
}

function stopTeam() {
  if (team.timer) team.timer.cancel()
  team.timer = null
  team.gen += 1
}

// startTeam reads at once and then every TEAM_MS, with the id the session has by then.
function startTeam($) {
  stopTeam()
  teamTick($)
  team.timer = $.clock.every(TEAM_MS, () => teamTick($))
}

// ---- the session ----

// startReporter turns the reporter on for an interactive session whose pdx.json names the
// daemon's socket (U1-1a's extractor writes `mod_socket`; an older install has none, and
// the reporter stays off). The stream and its seq outlive a second session.start in the
// same load.
async function startReporter($, e) {
  stopBeat()
  stopTeam()
  wb.gen += 1
  ev.on = false
  ev.switching = false
  const cfg = parse(await $.fs.read($.plugin.root + '/pdx.json').catch(() => ''))
  const sock = isObject(cfg) && typeof cfg.mod_socket === 'string' ? cfg.mod_socket : ''
  if (!sock) return
  if (!ev.stream) ev.stream = newStream()
  if (!ev.stream) {
    log($, 'reporter off: this environment has no crypto.getRandomValues for the stream id')
    return
  }
  ev.sock = sock
  ev.modVersion = String(await $.fs.read($.plugin.root + '/VERSION').catch(() => '')).trim()
  const v = await $.session.version().catch(() => undefined)
  ev.ccVersion = isObject(v) && typeof v.version === 'string' ? v.version : ''
  ev.sid = String(await $.session.id())
  ev.cwd = String(e.cwd ?? '')
  ev.turnId = ''
  ev.asks.clear()
  ev.compacting = false
  ev.lastError = false
  ev.background = null
  ev.monitors.clear()
  ev.on = true
  pqStart($)
  await $.command.register({ name: 'workbook', description: 'Purdex 工作簿：refresh 依整段對話重整目前狀況與待辦', argumentHint: 'refresh' })
    .catch((err) => log($, '/workbook not registered: ' + String(err)))
  enqueue($, 'session.start', { cwd: e.cwd, surface: e.surface })
  ev.beat = $.clock.every(HEARTBEAT_MS, () => beatTick($))
  forgetTeam($) // a second session.start in this load: nothing read for an earlier session stays
  startTeam($)
}

// sessionSwitch follows a /clear or a resume: the process goes on under a new session id,
// in the same stream, its seq and heartbeat going on; the old conversation's mirror is gone.
// The cwd and the background tasks stay (same process: the tasks run on; the next
// classic.Stop refreshes them). It ends the pause session.end began, whatever happens here.
async function sessionSwitch($, source) {
  if (!ev.on) return
  try {
    const prev = ev.sid
    ev.sid = String(await $.session.id())
    wb.gen += 1 // a poll made under the old session id stops asking
    ev.turnId = ''
    ev.asks.clear()
    ev.compacting = false
    ev.lastError = false
    ev.monitors.clear()
    enqueue($, 'session.switch', { prev_sid: prev, source })
    forgetTeam($) // the lead of the old conversation says nothing about the new one
    teamTick($)
    pqStart($) // the poll under the old session id ended with the generation: the new session polls for itself
  } finally {
    ev.switching = false
  }
}

// sessionEnd queues session.end under the ending session's own id and flushes inside the
// hook. `session.end` also fires on /clear and /resume (reason clear / resume), after which
// the same process and stream go on: the heartbeat stops only on the other reasons, before
// the final flush, so nothing of it (not even a beat in flight) lands after session.end. On
// clear / resume it pauses until session.switch (M-U1-4: about 660 ms later), so no beat
// goes out under the old id meanwhile, the one in flight included.
async function sessionEnd($, e) {
  if (!ev.on) return
  ev.monitors.clear()
  if (e.reason === 'clear' || e.reason === 'resume') {
    ev.switching = true
    ev.beatGen += 1
  } else {
    stopBeat()
    stopTeam()
    wb.gen += 1
  }
  enqueue($, 'session.end', { reason: e.reason }, e.sessionId)
  await finalFlush($)
}

// ---- what each hook reports ----

function withAgent(data, agentId) {
  if (agentId) data.agent_id = agentId
  return data
}

// rememberClosed keeps the ids of the last few main turns that ended: positive evidence that a turn.complete naming one of
// them, arriving after the next turn began, is late and must not end the running one.
function rememberClosed(turnId) {
  if (!turnId || ev.closed.includes(turnId)) return
  ev.closed.push(turnId)
  if (ev.closed.length > 8) ev.closed.shift()
}

function turnStarted($, e) {
  if (!ev.on) return
  ev.turnId = e.turnId // turn.start has no agentId: it is always the main conversation's
  ev.lastError = false
  enqueue($, 'turn.start', { turn_id: e.turnId })
}

function turnCompleted($, e) {
  if (!ev.on) return
  // A completion that names an earlier turn (the engine's own, arriving after the mod closed that turn itself and a new
  // one began) does not end the one now running.
  const late = !e.agentId && !!e.turnId && e.turnId !== ev.turnId && ev.closed.includes(e.turnId)
  if (!e.agentId && !late) {
    rememberClosed(e.turnId)
    ev.turnId = ''
    ev.asks.clear()
    ev.lastError = e.reason === 'error'
  }
  if (shouldAsk(e)) wbAsk($, WAIT_MS) // the daemon's job for this turn appears after its Stop hook and the catch-up
  enqueue($, 'turn.complete', { ...withAgent({ turn_id: e.turnId, reason: e.reason }, e.agentId), duration_ms: e.durationMs, aborted: !!e.isAborted })
}

// turnAborted closes the main turn this mod just cancelled with $.turn.abort. Claude Code ends such a turn without a
// turn.complete for the main conversation and without a Stop hook (measured, CC 2.1.294: after the abort the light and the
// heartbeat's turn_id stayed 'running' for good), so the mod says so itself: the state a real turn.complete clears, and a
// turn.complete{reason:'aborted', aborted:true} for the daemon (the light goes idle; the conversation ends its running
// turn as interrupted, since the abort leaves no marker in the transcript). Only when the turn it cancelled is still the one
// running: a turn.complete that did arrive, or a new turn, has already moved on.
function turnAborted($, turnId) {
  if (!ev.on || !turnId || ev.turnId !== turnId) return
  rememberClosed(turnId)
  ev.turnId = ''
  ev.asks.clear()
  enqueue($, 'turn.complete', { turn_id: turnId, reason: 'aborted', duration_ms: 0, aborted: true })
}

function toolStarted($, e) {
  if (ASK_TOOLS.has(e.tool) && e.tool_use_id) ev.asks.set(e.tool_use_id, 'question')
  enqueue($, 'tool.start', withAgent({ tool: e.tool, tool_use_id: e.tool_use_id }, e.agentId))
}

function toolEnded($, e, ms, error) {
  if (e.tool_use_id) ev.asks.delete(e.tool_use_id)
  enqueue($, 'tool.end', withAgent({ tool_use_id: e.tool_use_id, ms, error }, e.agentId))
}

// toolUseDrawn reports the approval of a permission ask: the ToolUse row's isRunning is false
// while the permission dialog is open and turns true about 16 ms after the person approves
// (M-U1-6, Claude Code 2.1.294). It runs on every redraw of every tool row, so it only looks
// the id up; the ask leaves the mirror at once, so a later redraw reports nothing. A question
// is never approved here: it waits until its tool.end.
function toolUseDrawn($, e) {
  if (!ev.on) return
  const p = e.props
  if (!p || p.isRunning !== true || ev.asks.get(p.tool_use_id) !== 'permission') return
  ev.asks.delete(p.tool_use_id)
  enqueue($, 'tool.approved', { tool_use_id: p.tool_use_id })
}

// monitorStarted remembers the task id of a Monitor call (measured on Claude Code 2.1.295: the hook's result is
// {ref, result: {taskId, timeoutMs, persistent}, text}, and taskId is the id classic.Stop lists in background_tasks).
function monitorStarted(e, r) {
  if (e.tool !== 'Monitor' || !isObject(r) || isErrorResult(r)) return
  // the nested result first, then a top-level taskId, then the strictly anchored text of the success message
  let id = isObject(r.result) ? r.result.taskId : undefined
  if (typeof id !== 'string' || !id) id = r.taskId
  if (typeof id !== 'string' || !id) {
    const m = typeof r.text === 'string' ? /^Monitor started \(task ([A-Za-z0-9_-]+)[,)]/.exec(r.text) : null
    id = m ? m[1] : undefined
  }
  if (typeof id !== 'string' || !id) return
  ev.monitors.delete(id) // a reused id is the newest again (Map keeps insertion order)
  ev.monitors.set(id, false)
  while (ev.monitors.size > MONITORS_MAX) ev.monitors.delete(ev.monitors.keys().next().value)
}

function isErrorResult(r) {
  return !!r && (r.isError === true || r.deny !== undefined)
}

function toolChecked($, e, r) {
  if (!ev.on) return
  const decision = isObject(r) ? r.decision : undefined
  if (decision === 'ask' && e.tool_use_id) ev.asks.set(e.tool_use_id, ASK_TOOLS.has(e.tool) ? 'question' : 'permission')
  const data = { tool: e.tool }
  if (e.tool_use_id) data.tool_use_id = e.tool_use_id
  enqueue($, 'tool.check', { ...withAgent(data, e.agentId), decision })
}

function agentSpawned($, e, r) {
  if (!ev.on || !isObject(r) || typeof r.agentId !== 'string') return // refused, or answered without starting one
  const data = { agent_id: r.agentId, tool_use_id: e.tool_use_id, background: !!e.background, subagent_type: e.subagentType }
  if (isObject(e.workflow) && e.workflow.runId) data.workflow_run_id = e.workflow.runId
  enqueue($, 'agent.spawn', data)
}

function measured($, e) {
  if (!ev.on) return
  const c = isObject(e.context) ? e.context : {}
  const context = { window: c.window }
  if (c.tokens !== undefined) context.tokens = c.tokens
  if (c.percent !== undefined) context.percent = c.percent
  const data = {
    context,
    rate_limits: (Array.isArray(e.rateLimits) ? e.rateLimits : []).map((l) => {
      const o = { kind: l.kind, percent_used: l.percentUsed }
      if (l.resetsAt !== undefined) o.resets_at = l.resetsAt
      return o
    }),
  }
  if (isObject(e.cost) && Number.isFinite(e.cost.usd)) data.cost_usd = e.cost.usd
  data.changed = Array.isArray(e.changed) ? [...e.changed] : []
  enqueue($, 'usage', data)
}

function stopped($, e) {
  if (!ev.on) return
  // a copy: the engine's objects are never touched. A task the Monitor tool started is named "monitor".
  const listed = Array.isArray(e.background_tasks) ? e.background_tasks : []
  const tasks = listed.map((t) => ({ id: t.id, type: ev.monitors.has(t.id) ? 'monitor' : t.type, status: t.status }))
  // a monitor this Stop lists is now known to the engine; one it listed before and no longer lists has ended
  const present = new Set(listed.map((t) => t.id))
  for (const [id, seen] of [...ev.monitors]) {
    if (present.has(id)) ev.monitors.set(id, true)
    else if (seen) ev.monitors.delete(id)
  }
  ev.background = { tasks, crons: Array.isArray(e.session_crons) ? e.session_crons.length : 0 }
  enqueue($, 'background', ev.background)
}

function compactStarted($, e) {
  if (!ev.on) return
  if (!e.agentId && e.trigger !== 'precompute') ev.compacting = true
  enqueue($, 'compact.start', withAgent({ trigger: e.trigger }, e.agentId))
}

function compactEnded($, e, ok) {
  if (!ev.on) return
  if (!e.agentId && e.trigger !== 'precompute') ev.compacting = false
  enqueue($, 'compact.end', { ...withAgent({ trigger: e.trigger }, e.agentId), ok })
}

// ---- the hooks ----

async function onSessionStart($, e, next) {
  const r = await next(e)
  await startReporter($, e)
  return r
}

async function onTurnStart($, e, next) {
  turnStarted($, e)
  return next(e)
}

async function onTurnComplete($, e, next) {
  const r = await next(e)
  turnCompleted($, e)
  return r
}

// onSessionSwitch reports the switch once the hooks beneath (the relay's /clear work) are
// done with the event — and when one of them failed too: the session id changed whatever
// they did, and the heartbeat stays paused until the switch is reported.
async function onSessionSwitch($, e, next) {
  try {
    return await next(e) // the engine has moved to the new session id
  } finally {
    await sessionSwitch($, e.source)
  }
}

// onCompact wraps the relay's compact hook (registered before it, so outside): ok is false
// exactly when the compaction did not happen (a hook beneath answered {skip}).
async function onCompact($, e, next) {
  if (!ev.on) return next(e)
  compactStarted($, e)
  let ok = false
  try {
    const r = await next(e)
    ok = !(isObject(r) && r.skip !== undefined)
    return r
  } finally {
    compactEnded($, e, ok)
  }
}

// onToolCall reports tool.start before next(e) and tool.end after it, timed on the engine's
// clock. When an outer hook answers with next still pending (ask.js's remote answer) what
// runs beneath is abandoned; once next(e) here rejects, tool.end {error: true} and the
// rejection goes on (the .catch replays it; the outer hook's answer stands). Should it never
// settle, the main turn.complete still clears the open ask from the mirror.
async function onToolCall($, e, next) {
  if (!ev.on) return next(e)
  const t0 = await $.clock.now()
  toolStarted($, e)
  let r
  try {
    r = await next(e)
  } catch (err) {
    toolEnded($, e, (await $.clock.now()) - t0, true)
    throw err
  }
  toolEnded($, e, (await $.clock.now()) - t0, isErrorResult(r))
  monitorStarted(e, r)
  return r
}

// onToolUseRender only observes: the drawing is whatever beneath answers, unchanged.
async function onToolUseRender($, e, next) {
  toolUseDrawn($, e)
  return next(e)
}

// onSessionModeRender adds the lead's label to the footer's modes while the last good read says this session leads a
// team; for anything else the footer is the engine's, unchanged. It reads the cache only, never the socket.
async function onSessionModeRender($, e, next) {
  const label = ev.on ? teamLabel() : ''
  if (!label) return next(e)
  return next({ ...e, props: { ...e.props, modes: [...e.props.modes, label] } })
}

async function onToolCheck($, e, next) {
  const r = await next(e)
  toolChecked($, e, r)
  return r
}

async function onAgentSpawn($, e, next) {
  const r = await next(e)
  agentSpawned($, e, r)
  return r
}

async function onMeasure($, e, next) {
  const r = await next(e)
  measured($, e)
  return r
}

async function onStop($, e, next) {
  const r = await next(e)
  stopped($, e)
  return r
}

async function onSessionEnd($, e, next) {
  const r = await next(e)
  await sessionEnd($, e)
  return r
}

// registerEvents is called once by register.js, before register.js's own hooks, so the
// matched hooks below are outermost on the events register.js owns. The matchers admit
// every event the reporter wants: any turn (every turn has an id), any compaction trigger,
// an interactive session start, a SessionStart that switched the session id, and a tool
// row's drawing.
export function registerEvents(on) {
  on('session.start', { isInteractive: true }, onSessionStart).catch(($, e, next) => next(e))
  on('turn.start', { turnId: /^/ }, onTurnStart).catch(($, e, next) => next(e))
  on('turn.complete', { turnId: /^/ }, onTurnComplete).catch(($, e, next) => next(e))
  on('classic.SessionStart', { source: ['clear', 'resume'] }, onSessionSwitch).catch(($, e, next) => next(e))
  on('session.compact', { trigger: /^/ }, onCompact).catch(($, e, next) => next(e))
  on('tool.call', onToolCall).catch(($, e, next) => next(e))
  on('tool.check', onToolCheck).catch(($, e, next) => next(e))
  on('agent.spawn', onAgentSpawn).catch(($, e, next) => next(e))
  on('session.measure', onMeasure).catch(($, e, next) => next(e))
  on('session.end', onSessionEnd).catch(($, e, next) => next(e))
  on('classic.Stop', onStop).catch(($, e, next) => next(e))
  on('ui.render', { component: 'ToolUse' }, onToolUseRender).catch(($, e, next) => next(e))
  on('ui.render', { component: 'SessionMode' }, onSessionModeRender).catch(($, e, next) => next(e))
  on('command.run', { command: 'workbook' }, workbookCommand)
}
