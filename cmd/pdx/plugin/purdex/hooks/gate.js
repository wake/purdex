// Purdex mod — host load control, the part the PATH shims cannot see (host-load-control spec §6, plan L1a).
//
// Two things happen to a Bash call here, both only for commands of the heavy and light kinds the gate holds:
//
// 1. Direct calls to a tool binary by path — node_modules/.bin/{vitest,tsc}, vendor/bin/{phpunit,pest}, with or without
//    a leading ./ — get `pdx gate --auto --` in front of them. The shims stand in PATH and never see a call by path.
//    A rewrite that would change what the engine asks permission for is not made.
// 2. A foreground call that the gate would make wait (`pdx gate would-wait --auto`: no free slot, a queue in front, or the
//    memory pressure over the limit) is run in the background instead, with a note: who holds the slot, for how long, the
//    place in the queue, "it starts by itself and you will be told, do not run it again". A call that can start at once
//    stays in the foreground. The hook asks pdx; it keeps no copy of the gate's rules.
//
// Everything fails open: a pdx that does not answer, an answer that is not JSON, a throw while planning — the call runs as
// it was. The planning is finished before `next` is called, and nothing after `next` can run the tool twice.
//
// `$` is used only in the top-level functions below (M-U1-2); the handler hands it straight on.

// ---- tokenizing ----

// a redirection word ends in an operator (its target is the next word)
const BARE = /[<>&]$/

// scan splits a command into segments of words. A word records the text between its quotes and where it
// sits in the original string. Returns null when the command cannot be parsed (an open quote) or has a
// backgrounding `&`.
function scan(cmd) {
  const segments = []
  let words = []
  let cur = null // {text, start, end, redirect}
  let quote = ''
  let background = false
  const heredocs = [] // {delim, strip, quoted} awaiting the next newline
  const masks = [] // [from, to) of text that is data (a quoted heredoc's body, a comment): substitutions() must not read it

  const endWord = (i) => {
    if (cur) {
      cur.end = i
      words.push(cur)
      cur = null
    }
  }
  const endSegment = (i) => {
    endWord(i)
    if (words.length) segments.push(words)
    words = []
  }
  const push = (ch, i) => {
    if (!cur) cur = { text: '', start: i, end: i, quoted: false, redirect: false }
    cur.text += ch
  }

  for (let i = 0; i < cmd.length; i++) {
    const c = cmd[i]
    if (quote === "'") {
      if (c === "'") quote = ''
      else push(c, i)
      continue
    }
    if (quote === '"') {
      if (c === '"') quote = ''
      else if (c === '\\' && i + 1 < cmd.length) { push(cmd[++i], i) }
      else push(c, i)
      continue
    }
    if (c === "'" || c === '"') {
      quote = c
      if (!cur) cur = { text: '', start: i, end: i, quoted: false, redirect: false }
      cur.quoted = true
      continue
    }
    if (c === '\\' && i + 1 < cmd.length) { push(cmd[++i], i); continue }
    if (c === ' ' || c === '\t') { endWord(i); continue }
    if (c === '\n') {
      endSegment(i)
      for (const h of heredocs.splice(0)) {
        const from = i + 1
        // the body: lines up to one that is the delimiter; none of it is a command
        for (;;) {
          const nl = cmd.indexOf('\n', i + 1)
          const line = cmd.slice(i + 1, nl < 0 ? cmd.length : nl)
          i = nl < 0 ? cmd.length : nl
          if ((h.strip ? line.replace(/^\t+/, '') : line) === h.delim || nl < 0) break
        }
        if (h.quoted) masks.push([from, i])
      }
      continue
    }
    if (c === ';') { endSegment(i); continue }
    if (c === '#' && !cur) {
      const from = i
      while (i + 1 < cmd.length && cmd[i + 1] !== '\n') i++
      masks.push([from, i + 1])
      continue
    }
    if (c === '>' || c === '<') {
      // an unquoted redirection ends the word before it (`run>out`), but keeps a file descriptor
      // (`2>`) and the operator it is building (`>>`, `<<`)
      if (cur && !(cur.redirect && /[<>]$/.test(cur.text)) && !/^(\d+|&)$/.test(cur.text)) endWord(i)
      if (c === '<' && cmd[i + 1] === '<' && cmd[i - 1] !== '<' && cmd[i + 2] !== '<') {
        let j = i + 2
        const strip = cmd[j] === '-'
        if (strip) j++
        while (cmd[j] === ' ' || cmd[j] === '\t') j++
        let delim = ''
        let quoted = false
        // a shell word: quotes group (and keep their spaces), a backslash escapes, the rest ends at a separator
        for (let q = ''; j < cmd.length; j++) {
          const d = cmd[j]
          if (q) { if (d === q) q = ''; else delim += d; continue }
          if (d === "'" || d === '"') { q = d; quoted = true; continue }
          if (d === '\\' && j + 1 < cmd.length) { delim += cmd[++j]; quoted = true; continue }
          if (' \t\n;|&()<>'.includes(d)) break
          delim += d
        }
        heredocs.push({ delim, strip, quoted })
      }
      push(c, i)
      cur.redirect = true
      continue
    }
    if (c === '|') {
      endSegment(i)
      if (cmd[i + 1] === '|') i++
      else if (cmd[i + 1] === '&') i++
      continue
    }
    if (c === '&') {
      if (cmd[i + 1] === '&') { endSegment(i); i++; continue }
      const prev = cur ? cur.text[cur.text.length - 1] : ''
      if (prev === '>' || prev === '<' || cmd[i + 1] === '>') { push(c, i); continue } // 2>&1, &>file
      background = true
      endSegment(i)
      continue
    }
    if ((c === '(' || c === ')') && cmd[i - 1] !== '$') { endSegment(i); continue }
    push(c, i)
  }
  if (quote !== '') return null
  endSegment(cmd.length)
  if (background) return null
  // a copy of the command with the data spans blanked, the same length, for the substitution search
  let masked = cmd
  for (const [a, b] of masks) masked = masked.slice(0, a) + ' '.repeat(b - a) + masked.slice(b)
  segments.masked = masked
  return segments
}

// ---- reading one segment ----

const ASSIGN = /^[A-Za-z_][A-Za-z0-9_]*=/

const base = (p) => p.slice(p.lastIndexOf('/') + 1)

// ---- the hook ----

const PRE_MS = 3_000 // pdx.json, the permission check: a read that has not answered by then is a failed one
const ASK_MS = 3_000 // `pdx gate would-wait` (about 70 ms): longer than this and the call is not held for it
const PLAN_MS = 4_000 // all of the planning together: a hook that waits on a dead dependency must not hold every Bash call
const MAX_ASKS = 6 // one call can hold several commands: all of them (distinct ones, at most this many) are asked at once, and the first that would wait decides

const parse = (s) => { try { return JSON.parse(s) } catch { return null } }
const isObject = (v) => !!v && typeof v === 'object' && !Array.isArray(v)
const sq = (s) => "'" + String(s).replace(/'/g, "'\\''") + "'"
const withNotes = (r, notes) => (notes.length && r && typeof r === 'object' ? { ...r, context: [...(r.context ?? []), ...notes] } : r)

// A tool binary called by path, which no shim sees.
const GATED_BIN = /(?:^|\/)(?:node_modules\/\.bin\/(?:vitest|tsc)|vendor\/bin\/(?:phpunit|pest))$/
// The first words of the commands the gate may hold; anything else is not asked about.
const TOOLS = new Set(['go', 'pnpm', 'pnpx', 'npx', 'yarn', 'npm', 'bunx', 'bun', 'vitest', 'tsc', 'make', 'composer', 'phpunit', 'pest'])

function log($, text) {
  try {
    const p = $.ui.log('pdx-gate: ' + text, { to: 'debug' })
    if (p && typeof p.catch === 'function') p.catch(() => {})
  } catch {}
}

// bounded runs one engine read and gives its answer, or undefined when it rejects or has not answered within PRE_MS.
async function bounded($, read) {
  const cap = Promise.resolve().then(() => $.clock.sleep(PRE_MS)).then(() => undefined, () => undefined)
  return Promise.race([Promise.resolve().then(read).catch(() => undefined), cap])
}

// gateConfig is the pdx binary to run and the config file of the daemon that installed the mod, from pdx.json beside VERSION
// as ask.js does; `pdx` from PATH and its default config when there is none. A second daemon on the host has its own
// config, and with it its own gate directory: the hook must ask, and send the call to, that one.
async function gateConfig($) {
  const cfg = parse(await bounded($, () => $.fs.read($.plugin.root + '/pdx.json')))
  return {
    bin: isObject(cfg) && typeof cfg.pdx === 'string' && cfg.pdx ? cfg.pdx : 'pdx',
    config: isObject(cfg) && typeof cfg.config === 'string' ? cfg.config : '',
  }
}

// headOf reads one segment: where the command proper starts (past environment assignments and the plain wrappers env,
// time, exec, command, nice, timeout), and its words from there without redirections. null when there is no command.
function headOf(words) {
  const skip = new Set()
  words.forEach((x, k) => { if (x.redirect && BARE.test(x.text)) skip.add(k + 1) }) // a bare `>` takes the next word
  const live = (k) => !words[k].redirect && !skip.has(k)
  let k = 0
  for (let guard = 0; guard < 8; guard++) {
    while (k < words.length && !live(k)) k++
    if (k >= words.length) return null
    const t = words[k].text
    const head = base(t)
    if (ASSIGN.test(t)) { k++; continue }
    if (head === 'env' || head === 'time' || head === 'exec' || head === 'command') { k++; continue }
    if (head === 'nice') { k++; if (k < words.length && words[k].text === '-n') k += 2; continue }
    if (head === 'timeout') {
      k++
      while (k < words.length && words[k].text.startsWith('-')) k += words[k].text === '-s' || words[k].text === '-k' ? 2 : 1
      k++ // the duration
      continue
    }
    break
  }
  if (k >= words.length) return null
  const rest = []
  for (let j = k; j < words.length; j++) if (live(j)) rest.push(words[j].text)
  return { at: words[k].start, first: words[k].text, argv: rest }
}

// readCommand splits a Bash command into the commands in it. null for what cannot be read or that goes to the
// background by itself (`&`): those are left exactly as they are.
export function readCommand(command) {
  const segs = scan(command)
  if (!segs) return null
  const out = []
  for (const words of segs) {
    const h = headOf(words)
    if (h) out.push(h)
  }
  return out
}

// prefixGated puts `prefix` in front of every call of a tool binary by path. changed says whether it did.
export function prefixGated(command, prefix) {
  const cmds = readCommand(command)
  if (!cmds) return { command, changed: false }
  const at = cmds.filter((c) => GATED_BIN.test(c.first)).map((c) => c.at)
  if (at.length === 0) return { command, changed: false }
  let out = command
  for (const i of at.sort((a, b) => b - a)) out = out.slice(0, i) + prefix + out.slice(i)
  return { command: out, changed: true }
}

// gatedCandidates are the distinct commands of a call that the gate may hold, in order, at most MAX_ASKS.
export function gatedCandidates(command) {
  const cmds = readCommand(command)
  if (!cmds) return []
  const seen = new Set()
  const out = []
  for (const c of cmds) {
    if (!TOOLS.has(base(c.first)) && !GATED_BIN.test(c.first)) continue
    const key = JSON.stringify(c.argv)
    if (seen.has(key)) continue
    seen.add(key)
    out.push(c.argv)
  }
  return out.slice(0, MAX_ASKS)
}

// askWouldWait is the gate's answer for one command: { would_wait, note, … }, or null (no answer is "no").
async function askWouldWait($, cfg, argv) {
  try {
    const r = await $.process.run([cfg.bin, 'gate', 'would-wait', '--auto', '--json', ...(cfg.config ? ['--config', cfg.config] : []), '--', ...argv], { timeoutMs: ASK_MS })
    if (!r || r.exitCode !== 0) return null
    const o = parse(String(r.stdout || '').trim())
    return isObject(o) && typeof o.would_wait === 'boolean' ? o : null
  } catch (err) {
    log($, 'would-wait failed: ' + (err && err.message ? err.message : String(err)))
    return null
  }
}

// verdict is the engine's permission decision for a call, or null when it could not be had.
async function verdict($, command, background) {
  const v = await bounded($, () => $.tool.check({ tool: 'Bash', input: { command, run_in_background: background } }))
  return v && typeof v.decision === 'string' ? v.decision : null
}

// withDeadline gives the answer of a plan, or null when it has not come within PLAN_MS (the call then runs as it is).
async function withDeadline($, work) {
  const cap = Promise.resolve().then(() => $.clock.sleep(PLAN_MS)).then(() => null, () => null)
  return Promise.race([work, cap])
}

// planGate is everything decided before the call runs: the command to run, whether to run it in the background, and the
// notes for the model. A plan that is null (or throws) means the call runs as it is.
async function planGate($, e) {
  if (typeof e.command !== 'string' || e.command === '') return null
  const cfg = await gateConfig($)
  const wasBackground = e.run_in_background === true
  let command = e.command
  const notes = []
  const prefixed = prefixGated(command, sq(cfg.bin) + ' gate' + (cfg.config ? ' --config ' + sq(cfg.config) : '') + ' --auto -- ')
  if (prefixed.changed) {
    // A prefix can change what the engine asks permission for: then the call is left as it is.
    const [before, after] = await Promise.all([verdict($, e.command, wasBackground), verdict($, prefixed.command, wasBackground)])
    if (before !== null && before === after) {
      command = prefixed.command
      notes.push('Purdex mod 把直接呼叫 node_modules/.bin 或 vendor/bin 的指令交給 pdx gate（主機負載控制），實際執行的是：' + command)
    } else {
      log($, 'the prefix would change the permission verdict (' + before + ' → ' + after + '): the call runs as it is')
    }
  }
  let background = wasBackground
  if (!wasBackground) {
    const cands = gatedCandidates(e.command)
    const answers = await Promise.all(cands.map((argv) => askWouldWait($, cfg, argv)))
    for (const w of answers) {
      if (!w || w.would_wait !== true) continue
      // A call moved to the background must not raise a prompt the foreground call would not have.
      const [before, after] = await Promise.all([verdict($, e.command, false), verdict($, command, true)])
      if (before === null || before !== after) {
        log($, 'the background would change the permission verdict (' + before + ' → ' + after + '): the call stays in the foreground')
        break
      }
      background = true
      notes.push('Purdex mod 把這個 Bash 呼叫（整段，包括它前後接的指令）改成背景執行：主機負載控制現在要讓其中的重指令排隊。' + (typeof w.note === 'string' ? w.note + ' ' : '') +
        '它輪到時會自己開始，整段跑完時你會收到通知與輸出；在那之前不要假設它做完了，不要重跑，也不要自己加 & 繞過排隊。')
      break
    }
  }
  if (command === e.command && background === wasBackground) return null
  return { command, background, notes }
}

export function registerGate(on) {
  on('tool.call', { tool: 'Bash' }, async ($, e, next) => {
    let plan = null
    try { plan = await withDeadline($, planGate($, e)) } catch (err) { log($, 'planning failed, the call runs as it is: ' + (err && err.message ? err.message : String(err))) }
    if (!plan) return next(e)
    const r = await next({ ...e, command: plan.command, ...(plan.background ? { run_in_background: true } : {}) })
    if (!r || typeof r !== 'object' || 'deny' in r) return r
    return withNotes(r, plan.notes)
  }).catch(($, e, next) => next(e))
}
