// Purdex mod — self relay (spec §8.1–§8.3 steps 4–8, §8.7).
//
//   idle ──(turn.complete: used ≥ threshold, growth ≥ minGrowth, +10 since last ask)──▶ beginning
//   beginning: `pdx relay begin --self` runs from a timer ──▶ awaiting, or back to idle on a refusal
//   awaiting: a timer loops `pdx relay wait`; every prompt.submit waits on that loop (P5b-3)
//   awaiting ──approved──▶ approved: write prompt submitted (a fresh nonce), report writing
//   approved ──(turn.complete of the write turn, file ok)──▶ clearing: report written, timer → /clear
//   clearing ──(classic.SessionStart source=clear)──▶ seeding: report cleared --new-session, hello, seed prompt
//   seeding ──(turn.complete of the seed turn)──▶ idle: report done, floor = tokens now
//   awaiting ──denied / timeout / cancelled / unavailable──▶ idle (ask again at +10 points)
//   a deferred step that fails (prompt refused, /clear refused) ──▶ idle: write / fix / seed report
//   failed{handoff_incomplete}, /clear reports cancelled{abandoned}
//   the user's own /clear ──▶ idle: awaiting / approved report cancelled{abandoned}, seeding
//   failed{handoff_incomplete}; a begin still out is cancelled{abandoned} when it answers (s.gen)
//
// Everything that starts a turn, runs a command or waits on the daemon goes
// out from a $.clock.after timer, never inside a hook: $.command.run rejects
// inside a hook the turn waits on (F3), and a daemon that is down answers
// only after the client's 30 s grace, which no turn end, session start or
// /clear may wait for (P5b-1 review). Hooks only read the engine and move
// the state.

const VERSION = '1' // the mod ↔ daemon protocol version `pdx relay hello --version` reports
const DEFAULT_THRESHOLD = 70
const DEFAULT_MIN_GROWTH = 20000
const REASK_POINTS = 10
const MAX_FIX_ROUNDS = 2
const WAIT_TIMEOUT_MS = 590_000 // $.process.run caps at 10 min (M24); pdx relay wait bounds itself to 9
const CALL_TIMEOUT_MS = 35_000 // one daemonclient grace (30 s) plus slack
const STEP_MS = 50 // the timer a step that starts a turn or a command waits for (F3)
const MAX_RESENDS = 20 // a report that keeps failing with 20 / 21 is re-sent at most this often, then dropped
const MAX_OUTBOX = 50 // reports queued at once; one more pushes out the oldest
const REQUIRED = ['## 1.', '## 2.', '## 3.', '## 4.', '## 5.', '## 6.', '## 7.', '## 8.']
const STATUS_WAITING = '接力等待核准中'
const TOAST_WAITING = '接力等待核准：請在 Purdex App 按核准或拒絕'
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
  pending: undefined, // { op, requestId, path, oldSession, oldRef, before, nonce, nonceState, who, wait }
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

// resetState starts the session over; the counters keep counting up, so a
// begin or a hello sent before the reset never answers for one sent after it.
function resetState() {
  Object.assign(s, fresh(), { gen: s.gen + 1, helloSeq: s.helloSeq })
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

const tag = (p) => '[pdx-relay op=' + p.op.id + ' n=' + p.nonce + ']'

function writePrompt(p) {
  return [
    tag(p) + ' 這個 session 的 context 已達接力門檻，使用者已核准接力（之後會 /clear）。',
    '請先停下手邊工作，用你完整的工具撰寫接力檔：' + p.path,
    '',
    '要求：',
    '- 自己跑 `git status`、`git diff --stat`、`git log --oneline -10` 取得檔案狀態，不要憑記憶寫。',
    '- 接力檔必須自成一體：讀它的是一個完全沒有這段對話記憶的新對話。',
    '- 寫完後只回一行「HANDOFF-WRITTEN」，不要繼續原本的工作。',
    '',
    '格式（每一段都要有，沒有內容就寫「無」）：',
    '# HANDOFF',
    '## 1. 目標與完成定義（使用者要的是什麼、怎樣算完成、範圍外）',
    '## 2. 進度（已完成且驗證 / 進行中停在哪 / 下一步第一個動作具體到指令）',
    '## 3. 檔案異動（git status 與 diff --stat 的結果，加上每個檔案的用途）',
    '## 4. 決策紀錄（選了什麼、為什麼、否決了什麼）',
    '## 5. 死路（試過失敗、不要再試的）',
    '## 6. 環境與指令（測試 / 執行方式）',
    '## 7. 未決問題與需要使用者決定的事',
    '## 8. 協作關係（下面的 pdx 身分；我的 lead 與我管理的 members，沒有就寫無）',
    '',
    '機器提供的事實（請照抄進對應段落）：',
    '- 舊 session id：' + p.oldSession,
    '- 舊 ref：' + p.oldRef,
    '- 接力時 context：' + p.before,
    '- pdx 身分：' + p.who,
  ].join('\n')
}

function fixPrompt(p, missing) {
  return tag(p) + ' 接力檔 ' + p.path + ' 不完整，缺少段落：' + (missing.join('、') || '(內容過短)') + '。請補齊後只回「HANDOFF-WRITTEN」。'
}

function seedPrompt(p) {
  return [
    '↪ 接手自 ' + p.oldRef,
    '[pdx-relay seed op=' + p.op.id + ' n=' + p.nonce + '] 你是接手的新對話：前一段對話 context 已滿並已清空。',
    '請先讀接力檔 ' + p.path + '，然後：',
    '1. 用三行複述：目標、下一步第一個動作、目前有哪些檔案異動。',
    '2. 跑 `git status` 確認與接力檔一致，不一致就指出來。',
    '3. 接著從「下一步」繼續原本的工作。',
    '回覆的第一行請寫「↪ 接手自 ' + p.oldRef + '」。',
  ].join('\n')
}

function usageLine(u) {
  return (u.tokens ?? '?') + ' tokens / ' + u.window + ' (' + (u.percent ?? '?') + '%)'
}

async function whoami($) {
  const r = await pdx($, ['msg', 'whoami'], 10_000)
  return (r.stdout || '').trim().replace(/\n/g, ' | ') || '(unknown)'
}

function toIdle() {
  Object.assign(s, { gen: s.gen + 1, state: 'idle', pending: undefined, fixRounds: 0, writeTurnId: undefined, seedTurnId: undefined })
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
  later($, 0, () => begin($, sid, gen, u))
}

// begin opens the self-relay request (from a timer, state beginning). Its
// answer is taken only by the generation and the session it was sent from:
// a /clear, a session start or a return to idle meanwhile bumped s.gen, and
// another begin may be in flight for the new session. An op opened for a
// generation that is gone is reported cancelled{abandoned} at once, so the
// daemon closes its approval row and no dialog is left without a mod.
async function begin($, sid, gen, u) {
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
  }
  s.state = 'awaiting'
  s.fixRounds = 0
  $.ui.status(STATUS_WAITING)
  const p = s.pending
  later($, STEP_MS, async () => {
    if (s.pending === p) await waitLoop($)
  })
}

// waitLoop is the one long-poll loop per request, its promise kept on the
// request (a loop left over from a request the user's own /clear dropped
// never answers for the next one). It runs in a timer's own dispatch, so a
// held prompt that is abandoned (Esc) never kills it; hooks only await the
// promise it returns.
function waitLoop($) {
  const p = s.pending
  if (p.wait) return p.wait
  p.wait = (async () => {
    $.ui.toast(TOAST_WAITING) // once per request (one loop per request)
    for (;;) {
      const r = await pdx($, ['relay', 'wait', p.requestId], WAIT_TIMEOUT_MS)
      if (r.exitCode === 0) {
        // P5a-2c's shape: exit 0 + Approval JSON, state 'approved' or 'open'
        // (the CLI's own 9 min bound ran out: ask again). Anything else at
        // exit 0 — empty or unparsable stdout, an unknown state — is NOT an
        // approval: never start the write turn on it (§8.7 (d)).
        const a = parseJSON(r.stdout)
        if (a && a.state === 'open') continue
        if (a && a.state === 'approved') return 'approved'
        return 'unavailable'
      }
      if (r.exitCode === 10) return 'denied'
      if (r.exitCode === 11) return 'timeout'
      if (r.exitCode === 12) return 'cancelled'
      return 'unavailable' // 20, 21, 1: treat as not approved (§8.7 (d))
    }
  })().catch(() => 'unavailable').then((outcome) => { settle($, p, outcome); return outcome })
  return p.wait
}

function settle($, p, outcome) {
  if (s.pending && s.pending !== p) return // another request owns the status line now
  $.ui.status(undefined)
  if (s.state !== 'awaiting' || s.pending !== p) return
  if (outcome !== 'approved') return toIdle()
  s.state = 'approved'
  later($, STEP_MS, async () => {
    p.who = await whoami($)
    if (s.pending !== p) return
    arm(p, 'approved')
    try {
      await submit($, writePrompt(p))
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
  report($, p.op.id, state, ['--error', error])
  toIdle()
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
      arm(p, 'approved')
      try {
        await submit($, fixPrompt(p, c.missing))
      } catch (err) {
        giveUp($, p, 'approved', 'failed', 'handoff_incomplete', 'fix prompt: ' + String(err))
      }
    })
    return
  }
  report($, p.op.id, 'failed', ['--error', 'handoff_incomplete'])
  $.ui.toast(TOAST_GAVE_UP)
  toIdle()
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
  on('session.start', async ($, e, next) => {
    resetState()
    s.interactive = !!e.isInteractive
    if (!s.interactive) return next(e) // a Nexen worker's `claude -p`: the mod does nothing (spec §5)
    const t = Number(await $.env.get('PDX_RELAY_THRESHOLD').catch(() => undefined))
    if (t > 0 && t <= 100) { s.threshold = t; s.envThreshold = true }
    const cfg = parseJSON(await $.fs.read($.plugin.root + '/pdx.json').catch(() => ''))
    if (cfg && cfg.pdx) s.pdx = cfg.pdx // written beside VERSION by the extractor; absent in `claude plugin test`
    s.config = cfg && typeof cfg.config === 'string' ? cfg.config : ''
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
        arm(p, 'seeding')
        try {
          await submit($, seedPrompt(p))
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
    if (p && (s.state === 'awaiting' || s.state === 'approved')) report($, p.op.id, 'cancelled', ['--error', 'abandoned'])
    else if (p && s.state === 'seeding') report($, p.op.id, 'failed', ['--error', 'handoff_incomplete'])
    if (s.state === 'awaiting') $.ui.status(undefined)
    toIdle()
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
}
