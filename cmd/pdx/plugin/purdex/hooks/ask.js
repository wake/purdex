// Purdex mod — 分流 for AskUserQuestion (lead-team spec §6.6 steps 1–7, U19; facts M24).
//
// The engine's own dialog is drawn untouched (`next(e)` issued, not awaited) and raced
// against the daemon: `pdx ask begin` opens a hook_ask row for the connected clients,
// `pdx ask wait` long-polls it in bounded rounds (each ≤ 9 min, inside $.process.run's
// ten-minute cap), and whichever answers first wins. Terminal first ⇒ the native result
// is returned unchanged and reported as answered_local; remote first ⇒ `{ result }` is
// returned, which closes the native dialog at once; Esc ⇒ dismissed. No responder, a
// daemon that is down, or any failure ⇒ the native dialog runs alone.
//
// This module runs in every interactive Claude Code session on the host, so it fails
// open everywhere: nothing of ours ever holds the dialog (`next(e)` goes out before the
// first daemon call), and nothing of ours holds the person's answer for long once they
// gave it in the terminal (SETTLE_MS below). The hook keeps a call of its own in flight
// the whole time it waits — the native `next(e)`, a `pdx ask` child or both — so its
// 10 s budget (HookBudget) stands still however long the dialog stays up.

const WAIT_TIMEOUT_MS = 590_000 // $.process.run is capped at 600 000; `pdx ask wait` returns after ≤ 9 min
const CALL_TIMEOUT_MS = 40_000 // begin / report: the daemon client's 30 s restart grace plus room

// SETTLE_MS bounds how long the person's terminal answer waits on us. Once the native
// dialog has settled, the mod still owes the daemon its report (and, when the person
// answered before `pdx ask begin` replied, begin's answer first). It waits for those at
// most this long, then returns the native result regardless; whatever is still out goes
// on by itself and is not awaited. The trade-off: a healthy daemon answers in tens of
// milliseconds, so the report lands while the dispatch is alive and the answer is delayed
// by that much only; a daemon that is down or restarting (its client waits 30 s) costs
// the person SETTLE_MS at most, and the report then lands later or never (the row is
// abandoned 30 s after the wait polls stop renewing its lease, so it never lingers).
// Every report's child is started before the answer goes back, so it is sent either way.
const SETTLE_MS = 3_000

const parse = (s) => { try { return JSON.parse(s) } catch { return null } }
const isObject = (v) => !!v && typeof v === 'object' && !Array.isArray(v)

// log writes to the debug log only: a session asked a question with no App connected
// gets no line in its transcript.
function log($, text) {
  try {
    const p = $.ui.log('pdx-ask: ' + text, { to: 'debug' })
    if (p && typeof p.catch === 'function') p.catch(() => {})
  } catch {}
}

// pdxConfig reads pdx.json beside VERSION (P5b-1's extractor writes it; the install flow
// does not put pdx on PATH): the binary to run and the installing daemon's config, which
// every call carries as `--config` so a second daemon on this machine is never the one
// asked (as register.js). Absent — as under `claude plugin test` — it is `pdx` from PATH
// and pdx's own default config. Read once per tool.call: the binary can move between sessions.
async function pdxConfig($) {
  let cfg = null
  try { cfg = parse(await $.fs.read($.plugin.root + '/pdx.json')) } catch {}
  return {
    bin: isObject(cfg) && typeof cfg.pdx === 'string' && cfg.pdx ? cfg.pdx : 'pdx',
    config: isObject(cfg) && typeof cfg.config === 'string' ? cfg.config : '',
  }
}

// ask runs `<pdx> ask <args> [--config <path>]`. The child is started at once (the call
// is out before this returns), whoever awaits it.
function ask($, cfg, args, timeoutMs) {
  return $.process.run([cfg.bin, 'ask', ...args, ...(cfg.config ? ['--config', cfg.config] : [])], { timeoutMs })
}

// stderrCode reads the API code `pdx ask` prints as the LAST whitespace-separated stderr
// token (`pdx ask: <detail> <code>`, the same shape as `pdx relay`); for the log line
// only — every non-zero exit takes the same "native dialog alone" branch.
function stderrCode(r) {
  return ((r && r.stderr) || '').trim().split(/\s+/).pop() || ''
}

// openedId is the row id a begin answered with: exit 0 and `{"id":…}` (a 409 ask_open is
// adopted by the CLI and reads the same), else '' — no_responders (13), daemon down (20),
// unsupported (21), usage (2), invalid_response (1), a body without an id, a rejected call.
function openedId(b) {
  if (b.who !== 'begin' || b.r.exitCode !== 0) return ''
  const o = parse(b.r.stdout)
  return isObject(o) && typeof o.id === 'string' ? o.id : ''
}

// What the native dialog settled to, read for the report: the answers when the person
// answered, else a dismissal (Esc, an interrupted turn, an error, a rejected next).
function nativeOutcome(n) {
  const r = n.who === 'native' ? n.r : null
  const answers = r && r.deny === undefined && r.isError !== true && r.result && r.result.answers
  return isObject(answers) && Object.keys(answers).length > 0
    ? { state: 'answered_local', hook: { answers } }
    : { state: 'dismissed' }
}

// report tells the daemon how the terminal settled the row. Never rejects.
function report($, cfg, id, outcome) {
  const args = ['report', id, outcome.state]
  if (outcome.hook) args.push('--hook', JSON.stringify(outcome.hook))
  return ask($, cfg, args, CALL_TIMEOUT_MS).then((r) => {
    if (r.exitCode !== 0) log($, 'report ' + outcome.state + ' exit ' + r.exitCode + ' ' + stderrCode(r))
  }, (err) => log($, 'report ' + outcome.state + ' failed: ' + String(err)))
}

// settle waits for `work` (begin's answer, the report) at most SETTLE_MS, and less when
// the dispatch is abandoned (next.signal: the person interrupted). Never rejects; a sleep
// the host refuses ends the wait at once.
async function settle($, next, work) {
  const cap = Promise.resolve().then(() => $.clock.sleep(SETTLE_MS, { signal: next.signal })).catch(() => {})
  await Promise.race([work.catch(() => {}), cap])
}

// native returns the native outcome as the hook's answer: the result object as it came
// (so core uses its own messages verbatim), or — when next(e) rejected — the same
// rejection, which the .catch below replays (nothing runs twice).
function native(n) {
  if (n.who === 'native') return n.r
  throw n.err
}

export function register(on) {
  on('tool.call', { tool: 'AskUserQuestion' }, async ($, e, next) => {
    // A subagent's question is not relayed in v1 (coordinator decision): its dialog shows
    // in the same terminal, but the row would name the wrong conversation.
    if (e.agentId) return next(e)
    // Headless (claude -p) has no dialog to race; a worker's questions are Nexen's (spec §6.6).
    if ((await $.session.surfaces()).length === 0) return next(e)

    const dialog = next(e).then((r) => ({ who: 'native', r }), (err) => ({ who: 'native-error', err }))

    const cfg = await pdxConfig($)
    const sid = await $.session.id()
    const begin = ask($, cfg, ['begin', '--session', String(sid || ''), '--tool-use', String(e.tool_use_id || ''), '--kind', 'hook_ask',
      '--payload', JSON.stringify({ questions: e.questions })], CALL_TIMEOUT_MS)
      .then((r) => ({ who: 'begin', r }), (err) => ({ who: 'begin-error', err }))

    const first = await Promise.race([dialog, begin])
    if (first.who === 'native' || first.who === 'native-error') {
      // The person answered (or dismissed) before the daemon even replied: the native
      // outcome stands. A row begin does open is told, within SETTLE_MS or after.
      await settle($, next, begin.then((b) => { const id = openedId(b); return id ? report($, cfg, id, nativeOutcome(first)) : undefined }))
      return native(first)
    }
    const id = openedId(first)
    if (!id) {
      // no_responders (13), daemon down (20), unsupported (21), anything else: the dialog runs alone.
      log($, first.who === 'begin' ? 'begin exit ' + first.r.exitCode + ' ' + stderrCode(first.r) + ': native dialog only' : 'begin failed: ' + String(first.err) + ': native dialog only')
      return native(await dialog)
    }

    // `stopped` once the race is decided; an abandoned dispatch (next.signal) starts no
    // further round either, so a hook the engine has given up on never keeps the row alive.
    let stopped = false
    const remote = (async () => {
      while (!stopped && !(next.signal && next.signal.aborted)) {
        const r = await ask($, cfg, ['wait', id], WAIT_TIMEOUT_MS)
        if (stopped) break
        if (r.exitCode !== 0) return { who: 'remote-error', why: 'wait exit ' + r.exitCode + ' ' + stderrCode(r) }
        const out = parse(r.stdout)
        if (isObject(out) && out.state === 'still_open') continue // another bounded round; the dialog stays up
        if (isObject(out) && out.state === 'answered_remote') {
          const answers = isObject(out.hook) && out.hook.answers
          if (isObject(answers) && Object.keys(answers).length > 0) return { who: 'remote', answers }
          return { who: 'remote-error', why: 'answered_remote without answers' }
        }
        if (isObject(out) && out.state === 'closed') return { who: 'remote-closed', why: 'closed ' + out.reason }
        // A body the mod cannot read ends the race; it never loops on one (no spin).
        return { who: 'remote-error', why: 'wait answered ' + String(r.stdout).slice(0, 80) }
      }
      return { who: 'remote-stopped', why: 'the dispatch was abandoned' }
    })().catch((err) => ({ who: 'remote-error', why: 'wait failed: ' + String(err) }))

    const w = await Promise.race([dialog, remote])
    stopped = true // the loop starts no further round
    if (w.who === 'native' || w.who === 'native-error') {
      // Step 3 / 6 (and step 5: if a remote decide won the CAS meanwhile, the daemon
      // records terminal_override — the terminal's answer still stands).
      await settle($, next, report($, cfg, id, nativeOutcome(w)))
      return native(w)
    }
    if (w.who === 'remote') {
      // Step 4: returning with next(e) pending closes the native dialog (M24 P-B).
      return { result: { questions: e.questions, answers: w.answers } }
    }
    // Closed another way (abandoned, dismissed by the backstop, …) or the loop failed:
    // the dialog runs on alone. A row the daemon still holds open is told how the
    // terminal settled it, as on step 3.
    log($, w.why + ': native dialog only')
    const n = await dialog
    if (w.who !== 'remote-closed') await settle($, next, report($, cfg, id, nativeOutcome(n)))
    return native(n)
  }).catch(($, e, next) => next(e)) // any failure of ours: the engine's own dialog, as without the mod (replayed, never run twice)
}
