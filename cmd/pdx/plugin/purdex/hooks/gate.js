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
// 3. With the wwd entry switched on (the "entry" key of pdx.json, off by default; spec §8.2) a test or build command is first shown
//    to `pdx entry match`, which knows the repo's task manifest: a command that is a declared task is run as `pdx test <task> …`
//    (in the background when the task goes through the queue), one that is not is advised or refused as the manifest's
//    [entry].mode says. The commands advised stay on the paths above. With the switch off none of this runs and the hook is what
//    it was.
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
// backgrounding `&`. With `continuation`, a backslash and a newline are removed as the shell removes them before it splits words
// (the entry reads commands that way; the older steps read them as they always did).
function scan(cmd, continuation) {
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
      else if (continuation && c === '\\' && cmd[i + 1] === '\n') i++
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
    if (continuation && c === '\\' && cmd[i + 1] === '\n') { i++; continue }
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
const MAX_ASKS = 6 // one call can hold several commands: all of them (distinct ones, at most this many) are asked in one process, and the first that would wait decides

const parse = (s) => { try { return JSON.parse(s) } catch { return null } }
const isObject = (v) => !!v && typeof v === 'object' && !Array.isArray(v)
const sq = (s) => "'" + String(s).replace(/'/g, "'\\''") + "'"
const withNotes = (r, notes) => (notes.length && r && typeof r === 'object' ? { ...r, context: [...(r.context ?? []), ...notes] } : r)

// A tool binary called by path, which no shim sees.
const GATED_BIN = /(?:^|\/)(?:node_modules\/\.bin\/(?:vitest|tsc)|vendor\/bin\/(?:phpunit|pest))$/
// `claude plugin test`: the mods' test run, which no shim sees (claude is the agent itself). The gate's classifier holds a whole
// run and lets one narrowed with --file through as a light one; it is prefixed and asked about like a tool binary called by path.
const isPluginTest = (c) => base(c.first) === 'claude' && c.argv[1] === 'plugin' && c.argv[2] === 'test'
const isGatedBin = (c) => GATED_BIN.test(c.first) || isPluginTest(c)
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
    entry: isObject(cfg) && typeof cfg.entry === 'string' && cfg.entry !== '', // the wwd entry switch
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
  let last = k
  let dead = -1 // the first redirection word (or the file it takes) after the command word
  for (let j = k; j < words.length; j++) {
    if (live(j)) { rest.push(words[j].text); last = j } else if (dead < 0) dead = j
  }
  // clean: no redirection among the words of the command, so its text from `at` to `end` is the command and nothing else (one
  // after the last word, `2>&1`, stays where it is when the command is replaced)
  return { at: words[k].start, end: words[last].end, clean: dead < 0 || dead > last, first: words[k].text, argv: rest }
}

// readCommand splits a Bash command into the commands in it. null for what cannot be read or that goes to the
// background by itself (`&`): those are left exactly as they are.
export function readCommand(command, continuation = false) {
  const segs = scan(command, continuation)
  if (!segs) return null
  const out = []
  for (const words of segs) {
    const h = headOf(words)
    if (h) out.push(h)
  }
  return out
}

// prefixGated puts `prefix` in front of every call of a tool binary by path, and of `claude plugin test`. changed says whether it did.
export function prefixGated(command, prefix) {
  const cmds = readCommand(command)
  if (!cmds) return { command, changed: false }
  const at = cmds.filter(isGatedBin).map((c) => c.at)
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
    if (!TOOLS.has(base(c.first)) && !isGatedBin(c)) continue
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

// askWouldWaitAll is the gate's answers for all the candidates of a call in one process: an array in the order of argvs, each
// as askWouldWait gives it. A pdx that does not know the batch form (older than the mod) or an answer that cannot be read
// falls back to asking one by one, so the answer for each command is the one it would have had.
export async function askWouldWaitAll($, cfg, argvs) {
  if (argvs.length === 0) return []
  if (argvs.length === 1) return [await askWouldWait($, cfg, argvs[0])]
  try {
    const r = await $.process.run([cfg.bin, 'gate', 'would-wait', '--auto-batch', JSON.stringify(argvs), '--json', ...(cfg.config ? ['--config', cfg.config] : [])], { timeoutMs: ASK_MS })
    if (r && r.exitCode === 0) {
      const a = parse(String(r.stdout || '').trim())
      if (Array.isArray(a) && a.length === argvs.length) return a.map((o) => (isObject(o) && typeof o.would_wait === 'boolean' ? o : null))
    }
  } catch (err) {
    log($, 'would-wait batch failed: ' + (err && err.message ? err.message : String(err)))
  }
  return Promise.all(argvs.map((argv) => askWouldWait($, cfg, argv)))
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

// ---- the wwd entry ----

// a word that needs no quoting in a shell
const word = (s) => (/^[A-Za-z0-9_@%+=:,./-]+$/.test(String(s)) ? String(s) : sq(s))

// entryCandidates are the commands of a call that the entry is asked about, in order, with where each stands in the text and the
// directory it runs in as far as `cd` words say (a relative one is pdx's to resolve from the session's directory), and a key naming
// the command and that directory. ok is false when the directory cannot be known: a cd the hook cannot read, or a command that is
// not reached by `&&` from the cd before it (after `cd x ; go test` or `cd x || go test` the test runs in x only if the cd worked,
// and the hook cannot tell). Every occurrence is a candidate; the ones with one key share an answer.
export function entryCandidates(command) {
  if (command.includes('(')) return [] // a subshell or a substitution: where a cd applies is not something to guess
  const cmds = readCommand(command, true)
  if (!cmds) return []
  const out = []
  let cwd = ''
  let ok = true
  let cded = false // a cd has changed the directory: from here on only `&&` carries it
  let prevEnd = -1
  for (const c of cmds) {
    // between the two commands: `&&`, then at most the environment words and wrappers that headOf skips before the command word
    if (cded && !/^\s*&&(\s|$)/.test(command.slice(prevEnd, c.at))) ok = false
    prevEnd = c.end
    if (base(c.first) === 'cd') {
      const d = c.argv[1]
      if (c.argv.length !== 2 || !/^[A-Za-z0-9_@%+=:,./-]+$/.test(d) || d === '-') ok = false
      else { cwd = d.startsWith('/') ? d : cwd ? cwd.replace(/\/$/, '') + '/' + d : d; cded = true }
      continue
    }
    if (!TOOLS.has(base(c.first)) && !isGatedBin(c)) continue
    out.push({ ...c, cwd, ok, key: JSON.stringify([cwd, c.argv]) })
  }
  return out
}

// askEntry is `pdx entry match`'s answer for one command, or null (no answer is "leave it").
async function askEntry($, cfg, c) {
  if (!c.ok) return null
  try {
    const r = await $.process.run([cfg.bin, 'entry', 'match', '--json', ...(c.cwd ? ['--cwd', c.cwd] : []), '--', ...c.argv], { timeoutMs: ASK_MS })
    if (!r || r.exitCode !== 0) return null
    const o = parse(String(r.stdout || '').trim())
    if (!isObject(o)) return null // an action nobody knows is ignored below
    if (o.action === 'rewrite' && (typeof o.task !== 'string' || o.task === '' || (o.args !== undefined && !(Array.isArray(o.args) && o.args.every((x) => typeof x === 'string'))))) return null
    return o
  } catch (err) {
    log($, 'entry match failed: ' + (err && err.message ? err.message : String(err)))
    return null
  }
}

// an ad-hoc job runs the argv as it is typed; the words are quoted again, so a command whose words the shell would still have to
// work on (a variable, a substitution, a glob, a brace, a tilde, a backslash) is not turned into one: its text is what the shell
// makes of it, not what the hook reads
const adhocPlain = (text) => !/[$`*?[\]{}~!\\]/.test(text)

const pdxAdhoc = (cfg, c) => sq(cfg.bin) + ' test --adhoc -- ' + c.argv.map(word).join(' ')

const pdxTest = (cfg, a) => {
  const args = Array.isArray(a.args) ? a.args : []
  return sq(cfg.bin) + ' test ' + word(a.task) + (args.length ? (args.some((x) => x.startsWith('-')) ? ' -- ' : ' ') + args.map(word).join(' ') : '')
}

// planEntry is what the entry makes of a call: { deny } to refuse it, or { command, background, notes } (notes only when nothing
// is replaced), or null for nothing to do. The replacement is not made when it would change the permission verdict.
async function planEntry($, e, cfg, wasBackground) {
  const cands = entryCandidates(e.command)
  if (cands.length === 0) return null
  // one question for each command in each directory, at most MAX_ASKS of them; its answer holds for every place it stands
  const firsts = []
  for (const c of cands) if (!firsts.some((f) => f.key === c.key)) firsts.push(c)
  const byKey = new Map(await Promise.all(firsts.slice(0, MAX_ASKS).map(async (c) => [c.key, await askEntry($, cfg, c)])))
  const answers = cands.map((c) => (c.ok ? byKey.get(c.key) ?? null : null)) // where the directory is unknown, no answer is borrowed
  const notes = []
  const rewrites = []
  let deny = null
  answers.forEach((a, i) => {
    if (!a) return
    if (a.broken === true) notes.push('Purdex mod：這個 repo 的 .wwd/tasks.toml 讀不了（' + (a.reason ?? '') + '），測試／建置指令先照常走。')
    const hint = typeof a.hint === 'string' && a.hint ? a.hint : ''
    if (a.action === 'deny' && !deny) {
      deny = 'Purdex mod 拒絕這個指令：這個 repo 的 .wwd/tasks.toml 規定測試與建置要用 pdx test 的 task（[entry].mode = deny）。' +
        (hint ? '最接近的是 pdx test ' + hint + ' …；' : '') + 'pdx test --list 列出全部。'
    } else if (a.action === 'adhoc' && cands[i].clean && adhocPlain(e.command.slice(cands[i].at, cands[i].end))) {
      rewrites.push({ c: cands[i], a, adhoc: true })
    } else if (a.action === 'advise' || a.action === 'adhoc') {
      // an ad-hoc answer for a command that cannot be turned into a job (a redirection, something the shell expands) is advice
      notes.push('Purdex mod：這個指令沒有對應的 .wwd/tasks.toml task，照常執行。' + (hint ? '建議改用 pdx test ' + hint + ' …；' : '') + 'pdx test --list 列出全部。')
    } else if (a.action === 'rewrite' && cands[i].clean) {
      rewrites.push({ c: cands[i], a })
    }
  })
  notes.splice(0, notes.length, ...new Set(notes)) // a command that stands twice says its piece once
  if (deny) return { deny }
  if (rewrites.length === 0) return notes.length ? { command: e.command, background: wasBackground, notes } : null
  let command = e.command
  for (const { c, a, adhoc } of rewrites.sort((x, y) => y.c.at - x.c.at)) command = command.slice(0, c.at) + (adhoc ? pdxAdhoc(cfg, c) : pdxTest(cfg, a)) + command.slice(c.end)
  const [before, after] = await Promise.all([verdict($, e.command, wasBackground), verdict($, command, wasBackground)])
  if (before === null || before !== after) {
    log($, 'the rewrite to pdx test would change the permission verdict (' + before + ' → ' + after + '): the call goes the old way')
    return notes.length ? { command: e.command, background: wasBackground, notes } : null
  }
  let background = wasBackground
  const queued = rewrites.some((r) => r.a.queued === true)
  if (queued && !wasBackground) {
    const [b2, a2] = await Promise.all([verdict($, e.command, false), verdict($, command, true)])
    if (b2 !== null && b2 === a2) background = true
  }
  const onlyAdhoc = rewrites.every((r) => r.adhoc)
  notes.unshift((onlyAdhoc
    ? 'Purdex mod 依這個 repo 的 .wwd/tasks.toml（[entry].mode = rewrite）把沒有對應 task 的測試／建置指令改成 pdx test --adhoc 執行（占用 heavy 名額，不是 task），實際執行的是：'
    : 'Purdex mod 依這個 repo 的 .wwd/tasks.toml 把測試／建置指令改成 pdx test，實際執行的是：') + command +
    (background && !wasBackground ? '。這個 task 要排佇列，所以整段改成背景執行：它輪到時會自己開始，整段跑完時你會收到通知與輸出；在那之前不要假設它做完了，不要重跑。' : ''))
  return { command, background, notes }
}

// planGate is everything decided before the call runs: the command to run, whether to run it in the background, and the
// notes for the model. A plan that is null (or throws) means the call runs as it is.
async function planGate($, e) {
  if (typeof e.command !== 'string' || e.command === '') return null
  const cfg = await gateConfig($)
  const wasBackground = e.run_in_background === true
  let command = e.command
  const notes = []
  let entryBackground = false
  let asked = e.command // the command the older steps work on: the call as it came, or as the entry made it
  if (cfg.entry) {
    let ep = null
    try { ep = await planEntry($, e, cfg, wasBackground) } catch (err) { log($, 'entry failed, the call goes the old way: ' + (err && err.message ? err.message : String(err))) }
    if (ep && ep.deny) return { deny: ep.deny }
    if (ep) { command = ep.command; asked = ep.command; entryBackground = ep.background && !wasBackground; notes.push(...ep.notes) }
  }
  const prefixed = prefixGated(command, sq(cfg.bin) + ' gate' + (cfg.config ? ' --config ' + sq(cfg.config) : '') + ' --auto -- ')
  if (prefixed.changed) {
    // A prefix can change what the engine asks permission for: then the call is left as it is.
    const [before, after] = await Promise.all([verdict($, asked, wasBackground), verdict($, prefixed.command, wasBackground)])
    if (before !== null && before === after) {
      command = prefixed.command
      notes.push('Purdex mod 把直接呼叫 node_modules/.bin 或 vendor/bin 的指令交給 pdx gate（主機負載控制），實際執行的是：' + command)
    } else {
      log($, 'the prefix would change the permission verdict (' + before + ' → ' + after + '): the call runs as it is')
    }
  }
  let background = wasBackground || entryBackground
  if (!background) {
    const cands = gatedCandidates(asked)
    const answers = await askWouldWaitAll($, cfg, cands)
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
  // The steps above were each checked against the form before them. When the entry changed the call, the form that will run (after
  // the prefix and the background the older steps added) is checked once against the call as it came, and anything but an equal
  // verdict puts the whole call back as it was.
  if (asked !== e.command || entryBackground) {
    const [before, after] = await Promise.all([verdict($, e.command, wasBackground), verdict($, command, background)])
    if (before === null || before !== after) {
      log($, 'the final form of the call would change the permission verdict (' + before + ' → ' + after + '): the call runs as it came')
      return null
    }
  }
  if (command === e.command && background === wasBackground && notes.length === 0) return null
  return { command, background, notes }
}

export function registerGate(on) {
  on('tool.call', { tool: 'Bash' }, async ($, e, next) => {
    let plan = null
    try { plan = await withDeadline($, planGate($, e)) } catch (err) { log($, 'planning failed, the call runs as it is: ' + (err && err.message ? err.message : String(err))) }
    if (!plan) return next(e)
    if (plan.deny) return { deny: plan.deny }
    const r = await next({ ...e, command: plan.command, ...(plan.background ? { run_in_background: true } : {}) })
    if (!r || typeof r !== 'object' || 'deny' in r) return r
    return withNotes(r, plan.notes)
  }).catch(($, e, next) => next(e))
}
