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
function apply(outcome, batch) {
  const res = outcome && outcome !== TIMEOUT ? outcome.res : undefined
  if (!res) return false
  if (res.status === 200) {
    const ack = ackOf(res)
    if (ack === undefined) return false
    dropThrough(ack)
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
  ev.inflight = true
  let outcome
  try {
    outcome = await postWithDeadline($, batch)
  } finally {
    ev.inflight = false
  }
  if (apply(outcome, batch)) {
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
  apply(outcome, batch)
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

// ---- the session ----

// startReporter turns the reporter on for an interactive session whose pdx.json names the
// daemon's socket (U1-1a's extractor writes `mod_socket`; an older install has none, and
// the reporter stays off). The stream and its seq outlive a second session.start in the
// same load.
async function startReporter($, e) {
  stopBeat()
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
  enqueue($, 'session.start', { cwd: e.cwd, surface: e.surface })
  ev.beat = $.clock.every(HEARTBEAT_MS, () => beatTick($))
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
    ev.turnId = ''
    ev.asks.clear()
    ev.compacting = false
    ev.lastError = false
    ev.monitors.clear()
    enqueue($, 'session.switch', { prev_sid: prev, source })
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
  }
  enqueue($, 'session.end', { reason: e.reason }, e.sessionId)
  await finalFlush($)
}

// ---- what each hook reports ----

function withAgent(data, agentId) {
  if (agentId) data.agent_id = agentId
  return data
}

function turnStarted($, e) {
  if (!ev.on) return
  ev.turnId = e.turnId // turn.start has no agentId: it is always the main conversation's
  ev.lastError = false
  enqueue($, 'turn.start', { turn_id: e.turnId })
}

function turnCompleted($, e) {
  if (!ev.on) return
  if (!e.agentId) {
    ev.turnId = ''
    ev.asks.clear()
    ev.lastError = e.reason === 'error'
  }
  enqueue($, 'turn.complete', { ...withAgent({ turn_id: e.turnId, reason: e.reason }, e.agentId), duration_ms: e.durationMs, aborted: !!e.isAborted })
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
}
