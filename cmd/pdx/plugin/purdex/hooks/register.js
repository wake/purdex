// Purdex mod — self relay (spec §8.1–§8.3 steps 4–8, §8.7).
//
//   idle ──(turn.complete: used ≥ threshold, growth ≥ minGrowth, +10 since last ask)──▶ beginning
//   beginning: `pdx relay begin --self` runs from a timer ──▶ awaiting, or back to idle on a refusal
//   awaiting: a timer loops `pdx relay wait` and alone asks the daemon and moves the state;
//   every prompt.submit is held until that loop's answer (≤ 11 min), on local `/bin/sleep 5`
//   calls of its own (P5b-3 critic), and one that arrives while beginning waits for begin's
//   answer first (≤ 40 s); every prompt is held, however many (U7: no new turn while a request is open)
//   awaiting ──approved──▶ approved: write prompt submitted (a fresh nonce), report writing
//   approved ──(turn.complete of the write turn, file ok)──▶ clearing: report written, timer → /clear
//   clearing ──(classic.SessionStart source=clear)──▶ seeding: report cleared --new-session, hello, seed prompt
//   seeding ──(turn.complete of the seed turn)──▶ idle: report done, floor = tokens now
//   awaiting ──denied / timeout / cancelled / unavailable──▶ idle (ask again at +10 points)
//   a deferred step that fails (prompt refused, /clear refused) ──▶ idle: write / fix / seed report
//   failed{handoff_incomplete}, /clear reports cancelled{abandoned}
//   the user's own /clear ──▶ idle: awaiting / approved report cancelled{abandoned}, seeding
//   failed{handoff_incomplete}; a begin still out is cancelled{abandoned} when it answers (s.gen)
//   a compaction ──▶ awaiting reports cancelled{compacted}, idle; approved skips an auto one
//   every return to idle lets the request go first (letGo): its held prompts go on at once,
//   unchanged, before any report; the loop's late answer for it is ignored
//
// Everything that starts a turn, runs a command or waits on the daemon goes
// out from a $.clock.after timer, never inside a hook: $.command.run rejects
// inside a hook the turn waits on (F3), and a daemon that is down answers
// only after the client's 30 s grace, which no turn end, session start or
// /clear may wait for (P5b-1 review). Hooks only read the engine and move
// the state. Two hooks wait on purpose: the prompt hold, on local
// `/bin/sleep 5` calls (a `$` call in flight stops the hook's 10 s budget,
// HookBudget; it asks the daemon nothing, the timers ask it and move the
// state) and failing open; and /relay, on its daemon call (the person waits
// for its output; 8 s bound).
//
// Each write / fix / seed prompt is composed at use (U21, spec §8.8): the
// mod's own fixed head and tail (prompts.js) around the body that `pdx relay
// prompts` answers right before the prompt goes out, from the step's timer
// (8 s bound); a call that fails, times out or answers junk gives the
// built-in body, and the relay goes on.
//
// This file is the plugin's one hooks module (hooks/hooks.json names a single
// path); it also registers ask.js, the AskUserQuestion 分流 (P8a-2), and
// events.js, the event reporter (interface U1 spec §6.5), and imports prompts.js, the copy of the daemon's relay prompts generated from
// internal/team/relay_prompts.go (P9a): the fixed head and tail of each
// prompt, and the built-in bodies.

import { register as registerAsk } from './ask.js'
import { registerEvents } from './events.js'
import { DEFAULT_BODIES, FIXED } from './prompts.js'

const VERSION = '1' // the mod ↔ daemon protocol version `pdx relay hello --version` reports
const DEFAULT_THRESHOLD = 70
const DEFAULT_MIN_GROWTH = 20000
const REASK_POINTS = 10
const MAX_FIX_ROUNDS = 2
const WAIT_TIMEOUT_MS = 590_000 // $.process.run caps at 10 min (M24); pdx relay wait bounds itself to 9
const CALL_TIMEOUT_MS = 35_000 // one daemonclient grace (30 s) plus slack
const SELF_TIMEOUT_MS = 8_000 // /relay waits in its hook for `pdx relay self`: the person waits for the answer
const PROMPTS_TIMEOUT_MS = 8_000 // `pdx relay prompts` before each write / fix / seed prompt (spec §8.8); then the built-in body
const MAX_BODY_BYTES = 16_384 // a body's limit in UTF-8 bytes, the daemon's (internal/team RelayPromptMaxBytes)
const HOLD_SLEEP = ['/bin/sleep', '5'] // the prompt hold's own `$` call, again while it waits: local, it asks the daemon nothing
const HOLD_SLEEP_TIMEOUT_MS = 10_000 // its $.process.run bound
const HOLD_MAX_MS = 660_000 // a held prompt waits for the request's answer at most 11 min: its 10 min deadline and slack
const BEGIN_HOLD_MS = 40_000 // … and for begin's answer at most 40 s: begin's own bound (CALL_TIMEOUT_MS, 35 s) and slack
const STEP_MS = 50 // the timer a step that starts a turn or a command waits for (F3)
const MAX_RESENDS = 20 // a report that keeps failing with 20 / 21 is re-sent at most this often, then dropped
const MAX_OUTBOX = 50 // reports queued at once; one more pushes out the oldest
const REQUIRED = ['## 1.', '## 2.', '## 3.', '## 4.', '## 5.', '## 6.', '## 7.', '## 8.']
const STATUS_WAITING = '接力等待核准中'
const TOAST_WAITING = '接力等待核准：請在 Purdex App 按核准或拒絕'
const NOTE = '接力已核准，這一輪只做簡短回應；如果這是一件新工作，不要開始做，把它寫進接力檔「下一步」的第一項，由接手後的新對話處理。'
const SKIP_COMPACT = '接力已核准，略過壓縮，改為寫接力檔'
const RELAY_UNREACHABLE = 'Purdex daemon 連不上，無法變更自我接力'
const RELAY_USAGE = '用法：/relay off|on|status'
const RELAY_MEMBER = 'member 的接力由 lead 安排'
const TOAST_GAVE_UP = '接力檔不完整，已放棄接力；對話照常繼續'
const toastSeedFailed = (path) => '接力未完成：接力檔在 ' + path + '，可手動貼給新 session'

const fresh = () => ({
  interactive: false,
  envThreshold: false,
  pdx: 'pdx',
  config: '', // the installing daemon's config file (pdx.json "config"); '' lets pdx use its default
  threshold: DEFAULT_THRESHOLD,
  minGrowth: DEFAULT_MIN_GROWTH,
  role: 'none',
  helloOK: false, // this session's hello answered (exit 0, JSON): only then is the threshold the daemon's
  helloSeq: 0, // the newest hello sent; an older one's answer is dropped
  helloBusy: false, // the newest hello has not answered yet
  gen: 0, // bumped at every return to idle and every session change: a begin answers only for its own
  state: 'idle', // idle | beginning | awaiting | approved | clearing | seeding
  begun: undefined, // while beginning: a deferred resolving to the request begin opened and adopted, or undefined
  pending: undefined, // { op, requestId, path, oldSession, oldRef, before, nonce, nonceState, who, wait, answer }
  lastAskPct: undefined,
  floor: undefined,
  fixRounds: 0,
  writeTurnId: undefined,
  seedTurnId: undefined,
  outbox: [], // reports not yet landed, in order: { op, argv, tries }
  held: new Set(), // ops whose report did not land: their later reports wait for the next turn.complete
  pumping: false,
  again: false,
})

const s = fresh()

// holding counts the prompts held right now (diagnostics only: no cap — U7
// says no new turn starts while a request is open, and each held prompt
// costs one local `/bin/sleep 5` at a time, never a daemon call; P5b-3
// critic). It lives beside `s`: a session reset while prompts are held must
// not zero it, since each hold gives its own place back (try/finally).
let holding = 0

// resetState starts the session over; the counters keep counting up, so a
// begin or a hello sent before the reset never answers for one sent after it.
function resetState() {
  letGo()
  Object.assign(s, fresh(), { gen: s.gen + 1, helloSeq: s.helloSeq })
}

// letGo releases the prompts held on the request the mod is leaving, at
// once and unchanged — its answer settles 'cancelled' unless it already has
// one — before anything is reported: that report may never land (a daemon
// that is down) and the row would then stay open with no wait to answer.
// A prompt held while begin is out goes on too. The wait loop's late answer
// for a request let go is ignored (settle: s.pending === p). (P5b-3 review)
function letGo() {
  if (s.pending) s.pending.answer.resolve('cancelled')
  if (s.begun) s.begun.resolve(undefined)
}

// deferred is a promise and the function that settles it; a second call is a no-op.
function deferred() {
  let resolve
  const promise = new Promise((r) => { resolve = r })
  return { promise, resolve }
}

function parseJSON(text) {
  try { return JSON.parse(text) } catch { return undefined }
}

function log($, text) {
  try { $.ui.log('pdx-relay: ' + text) } catch {}
}

// later runs fn from a timer, outside every hook; a failure is logged.
function later($, ms, fn) {
  $.clock.after(ms, () => { void fn().catch((err) => log($, 'deferred call failed: ' + String(err))) })
}

async function run($, argv, timeoutMs) {
  try {
    return await $.process.run([s.pdx, ...argv], { timeoutMs })
  } catch (err) {
    return { exitCode: 20, stdout: '', stderr: String(err) }
  }
}

// pdx runs `pdx <args>` against the daemon that installed the mod: with a
// config in pdx.json every call carries `--config <path>`, so a second
// daemon on this machine (another data dir) is never the one asked.
function pdx($, args, timeoutMs) {
  return run($, [...args, ...(s.config ? ['--config', s.config] : [])], timeoutMs)
}

// stderrCode is the 409 code: the last whitespace-separated stderr token (P5a-2c).
function stderrCode(r) {
  return (r.stderr || '').trim().split(/\s+/).pop() || ''
}

// hello tells the daemon the mod is here and takes its role, threshold and
// minimum growth. Only the newest hello's answer counts (a /clear sends one
// under the new session id while the old one may still be out), and only an
// answer that is exit 0 and JSON sets helloOK: until then nothing is asked
// (the defaults are not the daemon's), and the next turn.complete sends
// hello again.
async function hello($, seq) {
  try {
    const sid = await $.session.id()
    const r = await pdx($, ['relay', 'hello', '--session', sid, '--version', VERSION, '--agent', 'cc'], CALL_TIMEOUT_MS)
    if (seq !== s.helloSeq) return // a newer hello answers for the session now
    const h = r.exitCode === 0 ? parseJSON(r.stdout) : undefined
    if (!h || typeof h !== 'object') return // any other exit: not answered; said again at the next turn end
    s.helloOK = true
    if (h.role) s.role = h.role
    if (!s.envThreshold && h.threshold > 0) s.threshold = h.threshold
    if (h.min_growth > 0) s.minGrowth = h.min_growth
  } finally {
    if (seq === s.helloSeq) s.helloBusy = false
  }
}

// helloLater sends hello from a timer, never inside the hook: a session
// start, a /clear or a turn end must not wait for a daemon that is down.
function helloLater($) {
  const seq = ++s.helloSeq
  s.helloBusy = true
  $.clock.after(0, () => { void hello($, seq).catch(() => {}) })
}

// transientReport: a report that did not reach the daemon — 20 unreachable
// (a $.process.run that rejected reads as 20; a cleared answered 503
// not_ready through the CLI's grace ends here too) or 21 unsupported — is
// re-sent at the next turn.complete (§8.3), at most MAX_RESENDS times.
// Anything else is final: 13 (`bad_transition`) means the daemon is already
// PAST this state (a later report landed first, or the op was closed), and
// 1 (a runtime error), 2 (usage) or any other code would fail the same way
// again; those are dropped (all but 13 with a log line).
// A report worth sending again: 20 / 21 (daemon unreachable / no answer)
// and 1 — the CLI's exit for every other daemon or runtime error, a
// transient 500 storage_error included (cmd/pdx/relay.go relayReportErr), so
// 1 is not proof the report can never land (critic on PR #1763). Re-sends are
// bounded (MAX_RESENDS, MAX_OUTBOX); 13 (bad_transition: the daemon is past
// it) and any other code (2: usage) are dropped at once.
function transientReport(r) {
  return r.exitCode === 20 || r.exitCode === 21 || r.exitCode === 1
}

// report queues `pdx relay report <op> <state> …`; pump sends it from a
// timer. An op's reports land in order: one sent while an earlier one is
// still queued would be refused (written → done is bad_transition) and
// dropped for good, so a report that does not land holds its op's later
// reports until the next turn.complete re-sends it; a dropped one releases
// them. The relay goes on meanwhile (§8.3: the session matters more). The
// queue holds MAX_OUTBOX reports; one more pushes out the oldest.
function report($, opId, state, extra = []) {
  s.outbox.push({ op: opId, argv: ['relay', 'report', opId, state, ...extra], tries: 0 })
  while (s.outbox.length > MAX_OUTBOX) log($, 'report queue full (' + MAX_OUTBOX + '), dropped: ' + s.outbox.shift().argv.join(' '))
  pump($)
}

// nextReport is the first queued report not yet tried in this pass whose op
// is not held and that is its op's first: an op's reports go one at a time,
// in order, even when a turn.complete releases the holds mid-pass.
function nextReport(tried) {
  return s.outbox.find((it, k) => !tried.has(it) && !s.held.has(it.op) && s.outbox.findIndex((o) => o.op === it.op) === k)
}

function pump($) {
  s.again = true
  later($, 0, async () => {
    if (s.pumping) return // the running pass goes round again (s.again)
    s.pumping = true
    try {
      while (s.again) {
        s.again = false
        const tried = new Set()
        for (let item = nextReport(tried); item; item = nextReport(tried)) {
          tried.add(item)
          const r = await pdx($, item.argv, CALL_TIMEOUT_MS)
          const i = s.outbox.indexOf(item)
          if (i < 0) continue // pushed out of a full queue while it was out
          if (transientReport(r) && ++item.tries <= MAX_RESENDS) { s.held.add(item.op); continue }
          if (transientReport(r)) log($, 'report dropped after ' + MAX_RESENDS + ' re-sends (exit ' + r.exitCode + '): ' + item.argv.join(' '))
          else if (r.exitCode !== 0 && r.exitCode !== 13) log($, 'report dropped (exit ' + r.exitCode + '): ' + item.argv.join(' '))
          s.outbox.splice(i, 1)
        }
      }
    } finally {
      s.pumping = false
    }
  })
}

// newNonce mints the tag of one prompt of the mod's: 20 hex characters from
// The nonce comes from Web Crypto's getRandomValues (a CSPRNG) and nothing
// else (critic on PR #1763): an environment without it never relays —
// maybeBegin checks hasCSPRNG() and logs once. `claude plugin test` runs the
// module in the engine's own environment, so every begin test passing is the
// proof the engine has it. The nonce is also one-shot and state-bound.
function hasCSPRNG() {
  const c = globalThis.crypto
  return !!(c && typeof c.getRandomValues === 'function')
}

function newNonce() {
  const b = new Uint8Array(12)
  globalThis.crypto.getRandomValues(b)
  return Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('')
}

// arm mints the nonce of the next write / fix / seed prompt. turn.start
// takes the turn whose text carries it, only while the mod is in `state`
// (approved for write and fix, seeding for the seed), and only once.
function arm(p, state) {
  p.nonce = newNonce()
  p.nonceState = state
  if (state === 'approved') s.writeTurnId = undefined
  else s.seedTurnId = undefined
}

// fill replaces each {{name}} of `vars` in one pass: a value is never
// expanded again, and a {{name}} that `vars` does not hold stays as typed
// (U21 (d)).
function fill(text, vars) {
  return text.replace(/\{\{([a-z_]+)\}\}/g, (m, k) => (Object.hasOwn(vars, k) ? String(vars[k]) : m))
}

// compose builds the write, fix or seed prompt of request p (U21 (c)): the
// fixed head, the body, and the fixed tail on the next line —
//   fill(head, all) + fill(body, public) + (tail === '' ? '' : '\n' + fill(tail, all))
// The head and tail are always the mod's own (FIXED, never the daemon's):
// the machine tag with the nonce, the reply rule, the eight headings the
// check reads and the facts. The body gets only the five public variables;
// the mod's own op, nonce and missing are for the fixed parts. A body's
// trailing newlines are dropped, so one newline stands before the tail.
function compose(kind, body, p, extra = {}) {
  const pub = { path: p.path, old_ref: p.oldRef, old_session: p.oldSession, context: p.before, whoami: p.who }
  const all = { ...pub, op: p.op.id, nonce: p.nonce, ...extra }
  const { head, tail } = FIXED[kind]
  return fill(head, all) + fill(body.replace(/\n+$/, ''), pub) + (tail === '' ? '' : '\n' + fill(tail, all))
}

// utf8Bytes is text's length in UTF-8, as the daemon counts a body.
function utf8Bytes(text) {
  let n = 0
  for (const ch of text) {
    const c = ch.codePointAt(0)
    n += c < 0x80 ? 1 : c < 0x800 ? 2 : c < 0x10000 ? 3 : 4
  }
  return n
}

// refusal is why the daemon would have refused text as a body on write
// (internal/team ValidateRelayPromptBody), '' when it would not: not UTF-8
// (in a JS string, a lone surrogate), a control character other than \n and
// \t (Go's unicode.IsControl: U+0000–U+001F, U+007F–U+009F; so \r, NUL, DEL
// and C1), or the machine tag anywhere — the tag is the mod's alone. A
// daemon answers only what it validated; this holds against a damaged store
// or a schema drift (P9a-2 review).
function refusal(text) {
  for (const ch of text) {
    const c = ch.codePointAt(0)
    if (c >= 0xd800 && c <= 0xdfff) return 'a lone surrogate (not UTF-8)'
    if (c !== 0x0a && c !== 0x09 && (c <= 0x1f || (c >= 0x7f && c <= 0x9f))) return 'control character U+' + c.toString(16).toUpperCase().padStart(4, '0')
  }
  return text.includes('[pdx-relay') ? 'the machine tag [pdx-relay' : ''
}

// bodyFor asks the daemon for the body of the `kind` prompt about to go out
// (U21 (b), spec §8.8): read afresh for every prompt, so an edit applies from
// the next one with no `pdx setup`. Only an answer at exit 0 whose `kind` is
// a string with text, of at most MAX_BODY_BYTES, that the daemon's own rule
// (refusal) passes is used. Anything else gives the built-in body and one log
// line — 20 (unreachable, or a run that rejected: PROMPTS_TIMEOUT_MS ran
// out), 21 (a daemon from before the route), 1, junk, a missing field, a
// wrong type: a relay never fails because of its prompts. Called from the
// step's timer, never inside a hook.
async function bodyFor($, kind) {
  const r = await pdx($, ['relay', 'prompts'], PROMPTS_TIMEOUT_MS)
  const answer = r.exitCode === 0 ? parseJSON(r.stdout) : undefined
  const body = answer && typeof answer === 'object' ? answer[kind] : undefined
  let why
  if (r.exitCode !== 0) why = 'exit ' + r.exitCode
  else if (typeof body !== 'string') why = 'no string ' + kind + ' in the answer'
  else if (body.trim() === '') why = kind + ' is empty'
  else if (utf8Bytes(body) > MAX_BODY_BYTES) why = kind + ' is over ' + MAX_BODY_BYTES + ' bytes'
  else if (refusal(body)) why = kind + ' holds ' + refusal(body)
  else return body
  log($, 'relay prompts: the built-in ' + kind + ' body (' + why + ')')
  return DEFAULT_BODIES[kind]
}

function usageLine(u) {
  return (u.tokens ?? '?') + ' tokens / ' + u.window + ' (' + (u.percent ?? '?') + '%)'
}

async function whoami($) {
  const r = await pdx($, ['msg', 'whoami'], 10_000)
  return (r.stdout || '').trim().replace(/\n/g, ' | ') || '(unknown)'
}

// toIdle is every return to idle: it lets the request go first (letGo), so
// a caller that also reports does so after the held prompts went on.
function toIdle() {
  letGo()
  Object.assign(s, { gen: s.gen + 1, state: 'idle', pending: undefined, begun: undefined, fixRounds: 0, writeTurnId: undefined, seedTurnId: undefined })
}

// maybeBegin runs in turn.complete: it reads the engine, moves to beginning
// and leaves `pdx relay begin` to a timer.
async function maybeBegin($) {
  if (!s.helloOK || s.role === 'member') return
  if (!hasCSPRNG()) {
    if (!s.noCSPRNGLogged) {
      s.noCSPRNGLogged = true
      log($, 'relay disabled: this environment has no crypto.getRandomValues for the turn nonce')
    }
    return
  }
  const u = (await $.session.usage()).context
  if (u.percent === undefined || u.percent < s.threshold) return
  if (s.floor !== undefined && (u.tokens ?? 0) < s.floor + s.minGrowth) return
  if (s.lastAskPct !== undefined && u.percent < s.lastAskPct + REASK_POINTS) return
  const sid = await $.session.id()
  s.state = 'beginning'
  s.lastAskPct = u.percent
  const gen = s.gen
  const begun = deferred() // a prompt that arrives while begin is out waits on it (P5b-3)
  s.begun = begun
  later($, 0, () => begin($, sid, gen, u, begun.resolve).finally(() => begun.resolve(undefined)))
}

// begin opens the self-relay request (from a timer, state beginning). Its
// answer is taken only by the generation and the session it was sent from:
// a /clear, a session start or a return to idle meanwhile bumped s.gen, and
// another begin may be in flight for the new session. An op opened for a
// generation that is gone is reported cancelled{abandoned} at once, so the
// daemon closes its approval row and no dialog is left without a mod.
// `adopted(p)` hands the request it opened to prompts held while beginning.
async function begin($, sid, gen, u, adopted) {
  const argv = ['relay', 'begin', '--self', '--session', sid, '--used', String(u.percent), '--window', String(u.window)]
  const r = await pdx($, argv, CALL_TIMEOUT_MS)
  const now = await $.session.id().catch(() => undefined)
  const body = r.exitCode === 0 ? parseJSON(r.stdout) : undefined
  const opened = !!(body && body.op && body.op.id && body.request_id)
  const mine = s.gen === gen && s.state === 'beginning'
  if (!mine || now !== sid) {
    if (opened) report($, body.op.id, 'cancelled', ['--error', 'abandoned'])
    if (mine) toIdle() // same generation, another session id: nothing will ever answer for this begin
    return
  }
  if (!opened) {
    if (r.exitCode === 13 && stderrCode(r) === 'member_relay_is_leads') s.role = 'member'
    toIdle()
    return // 13 self_relay_off | self_relay_paused | relay_open, 20, 21, 1: nothing (§8.1, §8.7 (d)); ask again at +10
  }
  s.pending = {
    op: body.op,
    requestId: body.request_id,
    path: body.op.handoff_path,
    oldSession: sid,
    oldRef: body.op.ref,
    before: usageLine(u),
    nonce: undefined, // minted per prompt (arm)
    nonceState: undefined,
    who: '',
    // The request's answer, made here so a prompt held before the loop's
    // timer fires has it to wait on. Only the wait loop settles it (or the
    // timer, for a request dropped before its loop started): the hold
    // never starts the loop, which Esc on that prompt would end (§8.7).
    answer: deferred(),
  }
  s.state = 'awaiting'
  s.fixRounds = 0
  $.ui.status(STATUS_WAITING)
  const p = s.pending
  adopted(p)
  later($, STEP_MS, async () => {
    if (s.pending === p) await waitLoop($)
    else p.answer.resolve('cancelled') // dropped before its loop started (the user's /clear, a compaction)
  })
}

// waitAnswer reads one `pdx relay wait`. P5a-2c's shape: exit 0 + Approval
// JSON, state 'approved' or 'open' (the call's --wait bound ran out: call
// again). Anything else at exit 0 — empty or unparsable stdout, an unknown
// state — is NOT an approval: never start the write turn on it (§8.7 (d));
// 10 / 11 / 12 close the request; 20, 21, 1 (a rejected call reads as 20)
// are 'unavailable', treated as not approved.
function waitAnswer(r) {
  if (r.exitCode === 0) {
    const a = parseJSON(r.stdout)
    if (a && a.state === 'open') return 'open'
    if (a && a.state === 'approved') return 'approved'
    return 'unavailable'
  }
  if (r.exitCode === 10) return 'denied'
  if (r.exitCode === 11) return 'timeout'
  if (r.exitCode === 12) return 'cancelled'
  return 'unavailable'
}

const TICK = Symbol('tick')

// sleepUntil waits, inside the prompt.submit hook, for `target` — a promise
// the timers settle (begin's answer, the request's answer) — at most `ms`.
// It races the target with a local `/bin/sleep 5` of the hook's own, again
// while the target is out: a `$` call in flight stops the hook's 10 s budget
// (HookBudget), where an await of the timers' promise alone would let it run
// out and the engine release the prompt early; and a sleep asks the daemon
// nothing (P5b-3 critic: a `pdx relay wait` of the hold's own was one more
// long poll, and lease renewal, per held prompt). Resolves { value } once the
// target settles; undefined when `ms` ran out, a sleep could not run (the
// call rejected, or the sleep failed and would come back at once, again and
// again: the hold fails open, it never spins) or the prompt was abandoned.
async function sleepUntil($, target, ms, signal) {
  const settled = target.then((value) => ({ value }))
  const abandoned = new Promise((resolve) => {
    if (!signal) return // nothing abandons it
    if (signal.aborted) return resolve(undefined)
    signal.addEventListener('abort', () => resolve(undefined), { once: true })
  })
  const deadline = (await $.clock.now()) + ms
  for (;;) {
    const tick = $.process.run(HOLD_SLEEP, { timeoutMs: HOLD_SLEEP_TIMEOUT_MS }).then((r) => (r.exitCode === 0 ? TICK : undefined), () => undefined)
    const r = await Promise.race([settled, abandoned, tick])
    if (r !== TICK) return r
    if ((await $.clock.now()) >= deadline) return undefined
  }
}

// waitLoop is the one long-poll loop per request, its promise kept on the
// request (a loop left over from a request the user's own /clear dropped
// never answers for the next one). It runs in a timer's own dispatch, so a
// held prompt that is abandoned (Esc) never kills it; it settles the
// request's answer, which the held prompts await.
function waitLoop($) {
  const p = s.pending
  if (p.wait) return p.wait
  p.wait = (async () => {
    $.ui.toast(TOAST_WAITING) // once per request (one loop per request)
    for (;;) {
      const outcome = waitAnswer(await pdx($, ['relay', 'wait', p.requestId], WAIT_TIMEOUT_MS))
      if (outcome !== 'open') return outcome
    }
  })().catch(() => 'unavailable').then((outcome) => {
    p.answer.resolve(outcome) // the held prompts go on after settle below has moved the state
    settle($, p, outcome)
    return outcome
  })
  return p.wait
}

function settle($, p, outcome) {
  // A request the mod let go answers for nothing (its held prompts went on
  // at letGo, and whatever let it go cleared the status line or another
  // request owns it now).
  if (s.pending !== p) return
  $.ui.status(undefined)
  if (s.state !== 'awaiting') return
  if (outcome !== 'approved') return toIdle()
  s.state = 'approved'
  later($, STEP_MS, async () => {
    // both bounded (10 s, 8 s) and asked together: the step waits no longer than whoami did
    const [who, body] = await Promise.all([whoami($), bodyFor($, 'write')])
    p.who = who
    if (s.pending !== p || s.state !== 'approved') return // the await may span the user's /clear
    arm(p, 'approved')
    try {
      await submit($, compose('write', body, p))
    } catch (err) {
      giveUp($, p, 'approved', 'failed', 'handoff_incomplete', 'write prompt: ' + String(err))
      return
    }
    report($, p.op.id, 'writing')
  })
}

// submit sends a prompt of the mod's; a prompt that did not enter — the
// call rejected, or a hook beneath dropped it — throws, since no turn of it
// will ever start.
async function submit($, text) {
  const r = await $.prompt.submit({ text })
  if (r && r.drop !== undefined) throw new Error('prompt dropped: ' + r.drop)
}

// giveUp ends a relay whose step, deferred to a timer, failed: when the
// same request is still in the state the step was taken in, it reports and
// goes back to idle (asked again at +10 points), rather than leaving the
// mod waiting for a turn or a /clear that will never come. False when the
// relay had moved on meanwhile (nothing is reported then).
function giveUp($, p, inState, state, error, why) {
  log($, why)
  if (s.pending !== p || s.state !== inState) return false
  toIdle()
  report($, p.op.id, state, ['--error', error])
  return true
}

async function checkHandoff($, p) {
  const text = await $.fs.read(p.path).catch(() => '')
  const missing = REQUIRED.filter((h) => !text.includes(h))
  return { ok: text.length > 200 && missing.length === 0, missing }
}

async function onWriteTurnDone($) {
  const p = s.pending
  s.writeTurnId = undefined // checked once
  const c = await checkHandoff($, p)
  if (s.pending !== p) return
  if (c.ok) {
    s.state = 'clearing'
    report($, p.op.id, 'written')
    later($, STEP_MS, async () => {
      if (s.pending !== p || s.state !== 'clearing') return
      try {
        await $.command.run({ command: 'clear' })
      } catch (err) {
        giveUp($, p, 'clearing', 'cancelled', 'abandoned', '/clear: ' + String(err)) // written → cancelled
      }
    })
    return
  }
  if (s.fixRounds < MAX_FIX_ROUNDS) {
    s.fixRounds += 1
    later($, STEP_MS, async () => {
      if (s.pending !== p) return
      const body = await bodyFor($, 'fix') // every round asks again (open question 9)
      if (s.pending !== p || s.state !== 'approved') return
      arm(p, 'approved')
      try {
        await submit($, compose('fix', body, p, { missing: c.missing.join('、') || '(內容過短)' }))
      } catch (err) {
        giveUp($, p, 'approved', 'failed', 'handoff_incomplete', 'fix prompt: ' + String(err))
      }
    })
    return
  }
  toIdle()
  report($, p.op.id, 'failed', ['--error', 'handoff_incomplete'])
  $.ui.toast(TOAST_GAVE_UP)
}

async function onSeedTurnDone($) {
  const p = s.pending
  const u = (await $.session.usage()).context
  if (s.pending !== p) return
  report($, p.op.id, 'done')
  toIdle()
  s.floor = u.tokens // the loop guard: the next ask needs minGrowth more (§8.1)
  s.lastAskPct = undefined
}

export function register(on) {
  registerAsk(on) // tool.call{AskUserQuestion} only: no event this module hooks below
  // The event reporter (interface U1 spec §6.5): unmatched on events this module does not
  // hook, matched on the ones it does; registered first, so its hooks wrap the relay's.
  registerEvents(on)

  on('session.start', async ($, e, next) => {
    resetState()
    s.interactive = !!e.isInteractive
    if (!s.interactive) return next(e) // a Nexen worker's `claude -p`: the mod does nothing (spec §5)
    const t = Number(await $.env.get('PDX_RELAY_THRESHOLD').catch(() => undefined))
    if (t > 0 && t <= 100) { s.threshold = t; s.envThreshold = true }
    const cfg = parseJSON(await $.fs.read($.plugin.root + '/pdx.json').catch(() => ''))
    if (cfg && cfg.pdx) s.pdx = cfg.pdx // written beside VERSION by the extractor; absent in `claude plugin test`
    s.config = cfg && typeof cfg.config === 'string' ? cfg.config : ''
    await $.command.register({ name: 'relay', description: 'Purdex 自我接力：off 暫停、on 恢復、status 查看', argumentHint: 'off|on|status' })
      .catch((err) => log($, '/relay not registered: ' + String(err)))
    helloLater($)
    return next(e)
  })

  // Own-turn recognition (MP3): a plugin's own prompt.submit hook never sees
  // its own $.prompt.submit, so the write / fix / seed turn is told at
  // turn.start by the nonce minted for that prompt (arm), and acted on at the
  // turn.complete carrying that turnId. The nonce is accepted only in the
  // state it was minted for and only once: the turnId is sealed, and a later
  // turn carrying the same text (an echo, a paste, another plugin) is not
  // the relay's.
  on('turn.start', async ($, e, next) => {
    const p = s.pending
    if (s.interactive && p && p.nonce && s.state === p.nonceState && typeof e.text === 'string' && e.text.includes(p.nonce)) {
      if (s.state === 'approved') s.writeTurnId = e.turnId
      else s.seedTurnId = e.turnId
      p.nonce = undefined
    }
    return next(e)
  })

  on('turn.complete', async ($, e, next) => {
    const r = await next(e)
    if (!s.interactive || e.agentId) return r
    try {
      if (!s.helloOK && !s.helloBusy) helloLater($) // the last hello failed (daemon down): say it again
      if (s.outbox.length) { s.held.clear(); pump($) } // re-send what did not land (§8.3)
      if (s.pending && s.writeTurnId !== undefined && e.turnId === s.writeTurnId) await onWriteTurnDone($)
      else if (s.pending && s.seedTurnId !== undefined && e.turnId === s.seedTurnId) await onSeedTurnDone($)
      else if (s.state === 'idle') await maybeBegin($)
    } catch (err) {
      log($, 'turn.complete failed: ' + String(err))
    }
    return r
  })

  // /clear gives the conversation a new session id (M1). The mod's own
  // /clear (state clearing) reports cleared under it and seeds the new
  // conversation; any /clear says hello again under it (P5b-1: the daemon
  // keys mod presence by session id). Startup / resume are session.start's.
  on('classic.SessionStart', async ($, e, next) => {
    const r = await next(e)
    if (!s.interactive || e.source !== 'clear') return r
    const p = s.pending
    s.gen += 1 // a new session id: no begin sent under the old one answers for it
    s.helloOK = false // nor does the old session's hello: the new one is sent below
    if (s.state === 'clearing' && p) {
      s.state = 'seeding'
      report($, p.op.id, 'cleared', ['--new-session', await $.session.id()])
      helloLater($)
      later($, STEP_MS, async () => {
        if (s.pending !== p) return
        // The new conversation is idle while the body is asked for (≤ 8 s;
        // M29 with the daemon up): a prompt typed meanwhile runs first.
        const body = await bodyFor($, 'seed')
        if (s.pending !== p || s.state !== 'seeding') return // the user's own /clear meanwhile
        arm(p, 'seeding')
        try {
          await submit($, compose('seed', body, p))
        } catch (err) {
          if (!giveUp($, p, 'seeding', 'failed', 'handoff_incomplete', 'seed prompt: ' + String(err))) return
          // the /clear did happen: this is a new conversation, asked afresh
          s.floor = undefined
          s.lastAskPct = undefined
          $.ui.toast(toastSeedFailed(p.path))
        }
      })
      return r
    }
    // The user's own /clear: start over (the floor and the +10 re-ask were the
    // old conversation's). A relay in flight is ended at the daemon first, so
    // no dialog or op waits on a mod that moved on: awaiting / approved →
    // cancelled{abandoned} (the daemon closes the approval row), seeding →
    // failed{handoff_incomplete}; beginning needs nothing here — the
    // generation bump above has begin() cancel the op when it answers.
    // toIdle first: a prompt held on the request goes on before the report.
    const was = s.state
    toIdle()
    if (p && (was === 'awaiting' || was === 'approved')) report($, p.op.id, 'cancelled', ['--error', 'abandoned'])
    else if (p && was === 'seeding') report($, p.op.id, 'failed', ['--error', 'handoff_incomplete'])
    if (was === 'awaiting') $.ui.status(undefined)
    s.floor = undefined
    s.lastAskPct = undefined
    helloLater($)
    return r
  })

  on('tool.check', { tool: 'Write' }, async ($, e, next) => {
    if (s.pending && e.input && e.input.file_path === s.pending.path) return { decision: 'allow', reason: 'Purdex 接力檔' }
    return next(e)
  }).catch(($, e, next) => (next.called ? next(e) : { decision: 'deny', reason: 'pdx-relay guard failed' }))

  on('tool.check', { tool: 'Edit' }, async ($, e, next) => {
    if (s.pending && e.input && e.input.file_path === s.pending.path) return { decision: 'allow', reason: 'Purdex 接力檔' }
    return next(e)
  }).catch(($, e, next) => (next.called ? next(e) : { decision: 'deny', reason: 'pdx-relay guard failed' }))

  // ---- P5b-3: the hold, the compact rule, /relay ----

  // The hold (U7, §8.7 (b)): while a request is open no prompt of the main
  // conversation starts a turn, whatever its origin (the mod's own never
  // reaches this hook, MP3). The hook waits for the answer the timer's loop
  // settles, on local sleeps of its own (sleepUntil): a `$` call in flight,
  // so its 10 s budget stands still however long the request stays open, and
  // the daemon is asked nothing more per held prompt (P5b-3 critic). A
  // prompt that arrives while begin is still out waits for begin's answer
  // (≤ BEGIN_HOLD_MS) and goes on unchanged when begin opened nothing; the
  // request's answer is waited for ≤ HOLD_MAX_MS. Every prompt is held, however
  // many: a cap that let one through would start a turn while the request is
  // open (U7, spec §8.7 (b)); each costs a local sleep, not the daemon. Approved ⇒
  // NOTE after the existing context; anything else ⇒ unchanged. The status
  // line, the toast and the loop are the timer's, so Esc here ends this
  // prompt only. The hold never loses a prompt: whatever fails, its .catch
  // answers next(e) (fail-open; P5b-3 review).
  on('prompt.submit', async ($, e, next) => {
    if (!s.interactive) return next(e)
    const open = s.state === 'awaiting' ? s.pending : undefined
    const begun = s.state === 'beginning' ? s.begun : undefined
    if (!open && !begun) return next(e)
    let approved = false
    holding += 1
    try {
      const p = open || (await sleepUntil($, begun.promise, BEGIN_HOLD_MS, next.signal))?.value
      const outcome = p && (await sleepUntil($, p.answer.promise, HOLD_MAX_MS, next.signal))
      // `s.pending === p`: an approval that raced a compaction or a /clear relays nothing
      approved = !!outcome && outcome.value === 'approved' && s.pending === p
    } finally {
      holding -= 1
    }
    return next(approved ? { ...e, context: [...(e.context ?? []), NOTE] } : e)
  }).catch(($, e, next) => {
    log($, 'prompt hold failed (' + (next.error ? next.error.kind : '?') + '); the prompt goes on unchanged')
    return next(e)
  })

  // Compaction (§8.7 (c), deviation 9): an approved relay not yet written
  // skips an AUTO compaction (the handoff is written from the full context);
  // a manual /compact runs, the person asked for it. An open request, any
  // trigger, is reported cancelled{compacted}: the daemon closes its approval
  // row (P5a-2b closeRequestOfReportedOp), every dialog closes and `pdx relay
  // wait` exits 12, which releases the held prompts. No .catch: a hook that
  // throws here lets the compaction run.
  on('session.compact', async ($, e, next) => {
    if (!s.interactive || e.agentId || e.trigger === 'precompute') return next(e)
    if (s.state === 'approved') {
      if (e.trigger === 'auto') return { skip: SKIP_COMPACT }
      return next(e)
    }
    if (s.state === 'awaiting' && s.pending) {
      const op = s.pending.op.id
      toIdle() // a held prompt goes on at once, unchanged, before the report
      $.ui.status(undefined)
      report($, op, 'cancelled', ['--error', 'compacted'])
    } else if (s.state === 'beginning') {
      toIdle() // the begin still out answers for a gone generation: its op is cancelled{abandoned}
    }
    s.lastAskPct = undefined // after a compaction the next ask needs ≥ threshold again
    return next(e)
  })

  // /relay off|on|status (§8.7 (a)): the person waits for the answer, so this
  // one daemon call is awaited in the hook, bounded at SELF_TIMEOUT_MS; a
  // timeout (read as 20), 20 or 21 says the daemon is unreachable.
  on('command.run', { command: 'relay' }, async ($, e) => {
    const action = (e.args || 'status').trim()
    if (!['off', 'on', 'status'].includes(action)) return { text: RELAY_USAGE }
    const r = await pdx($, ['relay', 'self', action, '--session', await $.session.id()], SELF_TIMEOUT_MS)
    if (r.exitCode === 20 || r.exitCode === 21) return { text: RELAY_UNREACHABLE }
    // on|off in a member: the daemon's 409 member_relay_is_leads, exit 13 (P5b-3 review)
    if (r.exitCode === 13 && stderrCode(r) === 'member_relay_is_leads') return { text: RELAY_MEMBER }
    if (r.exitCode !== 0) return { text: 'pdx relay self ' + action + ' 失敗：' + (r.stderr || '').trim() }
    const b = parseJSON(r.stdout) || {}
    if (b.member) return { text: RELAY_MEMBER } // status in a member answers 200
    if (action === 'on') s.lastAskPct = undefined // asked again at once (still only after hello answered)
    const host = b.host_switch === false ? '主機開關 關' : '主機開關 開'
    const label = { on: '開啟', off: '關閉', paused: '本 session 暫停' }[b.self_relay] || String(b.self_relay)
    return { text: '自我接力：' + label + '（' + host + '；門檻 ' + s.threshold + '%）' }
  })
}
