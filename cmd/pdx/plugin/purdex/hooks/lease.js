// Purdex mod — host resource lease, the classifier (host-resource-lease plan Task 2.1, spec D-7, R7, R8).
//
// The classifier is pure functions: they read a Bash command string and say whether it is a heavy command (a
// full test run, a build, a lint of everything) and of which kind, and rewrite a full vitest run to cap its
// workers. Nothing in them touches the engine ($), so they run, and are tested, as plain code. The hook that
// uses them, registerLease, is at the end of the file.
//
// Fail open everywhere: a command that cannot be parsed, or that merely might be heavy, is not intercepted.
// Splitting is shell-aware only as far as it needs to be: quotes and backslashes, the separators
// `&&` `||` `;` `|` `|&` newline and `(` `)`, redirections. A single `&` (a backgrounded command) is not
// intercepted (R8).

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

const PM_VALUE_OPTS = new Set(['-C', '--dir', '--prefix', '--filter', '-F', '--workspace', '-w', '--package', '-p', '--cwd'])
const RUNNERS = new Set(['npx', 'pnpm', 'pnpx', 'yarn', 'npm', 'bunx', 'bun', 'corepack'])
const PM_SUBCOMMANDS = new Set(['exec', 'run', 'run-script', 'dlx', 'x'])
const SHELLS = new Set(['sh', 'bash', 'zsh', 'dash', 'fish', 'ksh'])
const ASSIGN = /^[A-Za-z_][A-Za-z0-9_]*=/

const base = (p) => p.slice(p.lastIndexOf('/') + 1)

// commandOf strips what comes before the command proper: environment assignments, `env`, `time`,
// `timeout N`, `nice`, `exec`, and a package runner with its own options. It returns the words that are
// the command and its arguments (redirections left out), or [] when there is none.
function commandOf(words) {
  let w = words.filter((x) => !x.redirect).map((x) => x.text)
  // a redirection's target (the word after a bare `>`) is not an argument
  const idx = words.map((x, k) => (x.redirect && BARE.test(x.text) ? k + 1 : -1)).filter((k) => k >= 0)
  if (idx.length) w = words.filter((x, k) => !x.redirect && !idx.includes(k)).map((x) => x.text)
  for (let guard = 0; guard < 8 && w.length; guard++) {
    const head = base(w[0])
    if (ASSIGN.test(w[0])) { w = w.slice(1); continue }
    if (head === 'env' || head === 'time' || head === 'exec' || head === 'command') { w = w.slice(1); continue }
    if (head === 'nice') { w = w.slice(1); if (w[0] === '-n') w = w.slice(2); continue }
    if (head === 'timeout') {
      w = w.slice(1)
      while (w.length && w[0].startsWith('-')) w = w.slice(w[0] === '-s' || w[0] === '-k' ? 2 : 1)
      if (w.length) w = w.slice(1) // the duration
      continue
    }
    if (RUNNERS.has(head)) {
      w = w.slice(1)
      while (w.length) {
        if (PM_VALUE_OPTS.has(w[0])) { w = w.slice(2); continue }
        if (w[0].startsWith('-') && !w[0].includes('=') && PM_VALUE_OPTS.has(w[0])) { w = w.slice(2); continue }
        if (w[0].startsWith('-')) { w = w.slice(1); continue }
        if (PM_SUBCOMMANDS.has(w[0])) { w = w.slice(1); continue }
        break
      }
      continue
    }
    break
  }
  return w
}

// vitest flags that take a value as the next word. Anything unknown starting with `-` is taken as a
// switch, so the word after it counts as a positional (a path): not intercepted, the safe side.
const VITEST_VALUE = new Set(['-t', '--testNamePattern', '--project', '--maxWorkers', '--max-workers', '--reporter', '--config', '-c', '--root', '-r', '--dir', '--pool', '--shard', '--outputFile', '--environment', '--mode', '-m', '--coverage.reporter', '--coverage.provider', '--poolOptions.threads.maxThreads', '--poolOptions.forks.maxForks', '--poolOptions.threads.minThreads', '--poolOptions.forks.minForks', '--minWorkers', '--min-workers', '--retry', '--bail', '--testTimeout', '--hookTimeout', '--exclude', '--browser.name', '--cache'])
const VITEST_LIMIT = /^--(maxWorkers|max-workers|poolOptions\..*(maxThreads|maxForks))(=|$)/

// vitestRun reads the words after `vitest`. kind is 'test-full' for a run over everything, null for any
// narrowed or watch invocation; capped says a worker limit is already given.
function vitestRun(args) {
  let run = false
  let capped = false
  for (let i = 0; i < args.length; i++) {
    const a = args[i]
    if (a === 'run') { run = true; continue }
    if (a.startsWith('-')) {
      const name = a.split('=')[0]
      if (VITEST_LIMIT.test(a)) capped = true
      if (name === '-t' || name === '--testNamePattern' || name === '--project' || name === '--changed' || name === '--related' || name === '--shard') return { kind: null, capped }
      if (!a.includes('=') && VITEST_VALUE.has(a)) i++
      continue
    }
    return { kind: null, capped } // a positional: a path, a filter, `related`, `watch`
  }
  return { kind: run ? 'test-full' : null, capped }
}

const GO_VALUE = new Set(['-run', '-bench', '-count', '-timeout', '-tags', '-p', '-parallel', '-cpu', '-coverprofile', '-o', '-vet', '-covermode', '-coverpkg', '-skip', '-fuzz', '-exec', '-ldflags', '-gcflags', '-mod', '-modfile', '-overlay', '-pkgdir', '-blockprofile', '-cpuprofile', '-memprofile', '-trace', '-outputdir', '-shuffle', '-fuzztime', '-test.run', '-asmflags', '-buildvcs'])

// goTest reads the words after `go test`.
function goTest(args) {
  let race = false
  let run = false
  const pkgs = []
  for (let i = 0; i < args.length; i++) {
    const a = args[i]
    if (a.startsWith('-')) {
      const name = a.replace(/^--?/, '-').split('=')[0]
      if (name === '-race') race = true
      if (name === '-run' || name === '-test.run' || name === '-skip' || name === '-fuzz') run = true
      if (!a.includes('=') && GO_VALUE.has(name)) i++
      continue
    }
    pkgs.push(a)
  }
  if (run) return null
  if (pkgs.some((p) => p === '...' || p.endsWith('/...'))) return 'test-full'
  if (race && pkgs.length === 1) return 'test-pkg'
  return null
}

// kindOf is the kind of one command (its words after commandOf), or null.
function kindOf(w, depth) {
  if (!w.length) return null
  const head = base(w[0])
  const args = w.slice(1)
  if (SHELLS.has(head)) {
    const c = args.indexOf('-c')
    return c >= 0 && args[c + 1] !== undefined && depth < 2 ? classifyKind(args[c + 1], depth + 1) : null
  }
  if (head === 'pdx') return args[0] === 'lease' ? 'wrapped' : null
  if (head === 'vitest') return vitestRun(args).kind
  if (head === 'go') {
    if (args[0] === 'test') return goTest(args.slice(1))
    if (args[0] === 'vet') return args.slice(1).some((p) => p === '...' || p.endsWith('/...')) ? 'lint-full' : null
    return null
  }
  if (head === 'make') return args.length === 1 && args[0] === 'test' ? 'test-full' : null
  if (head === 'tsc') return args.includes('-b') || args.includes('--build') ? 'build' : null
  if (head === 'vite' || head === 'electron-vite') return args[0] === 'build' ? 'build' : null
  if (head === 'eslint') return args.includes('.') ? 'lint-full' : null
  // package scripts, as `pnpm run build` / `npm run build` / `yarn build` are left after the runner is stripped
  if (head === 'build' || head === 'electron:build') return 'build'
  if (head === 'lint') return 'lint-full'
  return null
}

const ORDER = ['test-full', 'build', 'test-pkg', 'lint-full']

// substitutions lists the command lines inside `$( )` and backticks, outside single quotes: they run
// too, so a heavy command there counts.
function substitutions(cmd) {
  const out = []
  let single = false
  let double = false
  for (let i = 0; i < cmd.length; i++) {
    const c = cmd[i]
    if (single) { if (c === "'") single = false; continue }
    if (c === '"') { double = !double; continue }
    if (c === "'" && !double) { single = true; continue }
    if (c === '\\') { i++; continue }
    if (c === '`') {
      let j = i + 1
      while (j < cmd.length && cmd[j] !== '`') j += cmd[j] === '\\' ? 2 : 1
      out.push(cmd.slice(i + 1, j))
      i = j
    } else if (c === '$' && cmd[i + 1] === '(') {
      let depth = 1
      let j = i + 2
      for (; j < cmd.length && depth > 0; j++) {
        if (cmd[j] === '(') depth++
        else if (cmd[j] === ')') depth--
      }
      out.push(cmd.slice(i + 2, j - 1))
      // the text is still scanned, so a nested one is found by the recursion in classifyKind
    }
  }
  return out
}

// classifyKind is the heaviest kind among a command's segments and the command lines substituted into
// them (a heavy command inside a substitution is leased but not given the R7 cap: rewriting inside
// `$( )` is not attempted). A segment already going through `pdx lease` is skipped, not the whole command: what follows it
// is not covered by its lease. null for none or an unparseable command.
function classifyKind(cmd, depth) {
  const segs = scan(cmd)
  if (segs === null) return null
  let best = null
  const consider = (k) => {
    if (k && k !== 'wrapped' && (best === null || ORDER.indexOf(k) < ORDER.indexOf(best))) best = k
  }
  for (const s of segs) consider(kindOf(commandOf(s), depth))
  if (depth < 3) for (const inner of substitutions(segs.masked)) consider(classifyKind(inner, depth + 1))
  return best
}

// classify says whether a Bash command is heavy: {kind, needsMaxWorkers} where kind is test-full, build, test-pkg
// or lint-full (spec D-7) and needsMaxWorkers says a full vitest run in it has no worker limit (R7); null when it
// is not heavy, is already wrapped in `pdx lease`, runs in the background, or cannot be parsed.
export function classify(command) {
  if (typeof command !== 'string') return null
  const kind = classifyKind(command, 0)
  if (kind === null) return null
  return { kind, needsMaxWorkers: rewriteMaxWorkers(command).changed }
}

// hasLeaseSegment says whether a command has a segment that is itself a `pdx lease` call.
export function hasLeaseSegment(command) {
  const segs = typeof command === 'string' ? scan(command) : null
  return segs !== null && segs.some((s) => kindOf(commandOf(s), 0) === 'wrapped')
}

// needsCap lists the full-vitest segments (indexes into the scanned segments) that carry no worker limit.
function needsCap(segs) {
  const out = []
  segs.forEach((s, i) => {
    const w = commandOf(s)
    if (w.length && base(w[0]) === 'vitest') {
      const r = vitestRun(w.slice(1))
      if (r.kind === 'test-full' && !r.capped) out.push(i)
    }
  })
  return out
}

// rewriteMaxWorkers (R7) appends ` --maxWorkers=3` to each full vitest run that has no worker limit,
// right after its last argument, so it lands before a redirection (`2>&1`) as well as before a pipe.
// A run inside `sh -c '...'` is rewritten in place when the string is plainly quoted. Returns
// {command, changed}.
export function rewriteMaxWorkers(command) {
  return rewriteAt(command, 0)
}

function rewriteAt(command, depth) {
  if (typeof command !== 'string') return { command, changed: false }
  const segs = scan(command)
  if (segs === null) return { command, changed: false }
  const todo = new Set(needsCap(segs))
  const edits = [] // {at, end, text}: replace command.slice(at, end) with text
  segs.forEach((s, i) => {
    if (todo.has(i)) {
      // the last word that is not a redirection or a redirection's target
      let last = null
      for (let k = 0; k < s.length; k++) {
        if (s[k].redirect) { if (BARE.test(s[k].text)) k++; continue }
        last = s[k]
      }
      if (last !== null) edits.push({ at: last.end, end: last.end, text: ' --maxWorkers=3' })
      return
    }
    // sh -c "<command>": the string is another command line
    const w = commandOf(s)
    if (depth >= 2 || !w.length || !SHELLS.has(base(w[0]))) return
    const c = w.indexOf('-c')
    if (c < 0 || w[c + 1] === undefined) return
    const arg = s.find((x) => x.text === w[c + 1] && x.quoted)
    if (!arg) return
    const src = command.slice(arg.start, arg.end)
    const q = src[0]
    if ((q !== '"' && q !== "'") || src[src.length - 1] !== q || src.slice(1, -1) !== arg.text) return
    const inner = rewriteAt(arg.text, depth + 1)
    if (inner.changed) edits.push({ at: arg.start + 1, end: arg.end - 1, text: inner.command })
  })
  if (edits.length === 0) return { command, changed: false }
  let out = command
  for (const e of edits.sort((a, b) => b.at - a.at)) out = out.slice(0, e.at) + e.text + out.slice(e.end)
  return { command: out, changed: true }
}

// ---- the hook (host-resource-lease plan Task 2.2; lease v2 amendment A, Tasks 23, 25, 26) ----
//
// registerLease puts a heavy Bash call through the host's lease: it asks `pdx lease acquire`, runs the
// command, and releases by client id when the call ends, whatever happened. Everything fails open: a daemon
// that is down, an answer that is not JSON, a throw before the command ran — the command runs, unchanged
// except for the worker cap.
//
// Foreground: the request names the command and the session's directory and no mode, so the daemon decides
// (mode auto: queue for a caller under an interactive session, wait for any other). A queue answer is not
// waited for: the call is denied with where the command stands, and the session is told when its turn comes.
// Background: in an interactive session a heavy command is wrapped in `pdx lease run --ask`, so it waits
// inside its own background process and is asked at its turn; the mod adds nothing else to any command (any
// prefix changes the permission prompts: lease v2 plan, Q1, M-P2 (a)).
//
// `$` is used only in the top-level functions below (M-U1-2); the handler hands it straight on.

const ACQUIRE_TIMEOUT_MS = 600_000 // $.process.run's cap
const RELEASE_TIMEOUT_MS = 5_000
const PRE_MS = 3_000 // pdx.json and the session id: a read that has not answered by then is a failed one
const WAIT_NOTE_MS = 1_000 // a wait shorter than this is not worth telling the model about
const COMMAND_MAX = 200 // bytes of the command the queue shows
const HEX = '0123456789abcdef'

const parse = (s) => { try { return JSON.parse(s) } catch { return null } }
const isObject = (v) => !!v && typeof v === 'object' && !Array.isArray(v)

function log($, text) {
  try {
    const p = $.ui.log('pdx-lease: ' + text, { to: 'debug' })
    if (p && typeof p.catch === 'function') p.catch(() => {})
  } catch {}
}

// bounded runs one engine read and gives its answer, or undefined when it rejects or has not answered
// within PRE_MS: the hook must reach next(e) whatever those reads do, so the command is never held by them.
async function bounded($, read) {
  const cap = Promise.resolve().then(() => $.clock.sleep(PRE_MS)).then(() => undefined, () => undefined)
  return Promise.race([Promise.resolve().then(read).catch(() => undefined), cap])
}

// leaseConfig reads pdx.json beside VERSION as ask.js does: the binary to run and the installing
// daemon's config. Absent — as under `claude plugin test` — it is `pdx` from PATH and its default config.
async function leaseConfig($) {
  const cfg = parse(await bounded($, () => $.fs.read($.plugin.root + '/pdx.json')))
  return {
    bin: isObject(cfg) && typeof cfg.pdx === 'string' && cfg.pdx ? cfg.pdx : 'pdx',
    config: isObject(cfg) && typeof cfg.config === 'string' ? cfg.config : '',
  }
}

// newClientId is a lower-case UUID v4 from Web Crypto; '' when the environment has none (the hook then
// releases by the id acquire returned).
function newClientId() {
  const c = globalThis.crypto
  if (!c || typeof c.getRandomValues !== 'function') return ''
  const b = new Uint8Array(16)
  c.getRandomValues(b)
  b[6] = (b[6] & 0x0f) | 0x40
  b[8] = (b[8] & 0x3f) | 0x80
  const h = Array.from(b, (x) => HEX[x >> 4] + HEX[x & 15]).join('')
  return h.slice(0, 8) + '-' + h.slice(8, 12) + '-' + h.slice(12, 16) + '-' + h.slice(16, 20) + '-' + h.slice(20)
}

// cutBytes cuts a string to at most n UTF-8 bytes without splitting a character.
function cutBytes(s, n) {
  let bytes = 0
  let out = ''
  for (const ch of String(s)) {
    const cp = ch.codePointAt(0)
    const w = cp < 0x80 ? 1 : cp < 0x800 ? 2 : cp < 0x10000 ? 3 : 4
    if (bytes + w > n) break
    bytes += w
    out += ch
  }
  return out
}

// sq quotes a word for a POSIX shell.
const sq = (s) => "'" + String(s).replace(/'/g, "'\\''") + "'"

// EXIT_QUEUED is `pdx lease acquire`'s exit code for "queued": the request waits at its place and its line
// says so. It is an answer, not a failure.
const EXIT_QUEUED = 15

// readAcquire is the one JSON line `pdx lease acquire` prints, or null for anything else (any other non-zero
// exit, junk). The answer is read even when the lease was fail-open: that says so itself. Exit 15 counts only
// with a line that says queued: a command that exits 15 for itself is not this.
function readAcquire(r) {
  if (!r || (r.exitCode !== 0 && r.exitCode !== EXIT_QUEUED)) return null
  const o = parse(String(r.stdout || '').trim())
  if (!isObject(o)) return null
  if (r.exitCode === EXIT_QUEUED && o.state !== 'queued') return null
  return o
}

const isQueued = (a) => !!a && a.state === 'queued'

// queuedDeny is what the model is told when the host is busy and its command was not run (lease v2 amendment A,
// T-queued and T-queued-own): the command did not run, where it stands, that it will be told, and not to get
// around the queue. `pdx lease run` says the same.
function queuedDeny(a) {
  const head = a.queued_reason === 'own_running'
    ? '主機資源忙，這個指令還沒執行：等你自己的上一個指令跑完。'
    : '主機資源忙，這個指令還沒執行：排在第 ' + (Number(a.position) || 0) + ' 位。'
  return head + '輪到你時我會通知你，先做別的事；不要改成背景執行或加 & 繞過排隊。'
}

// leaseNotes is what the model is told, in Chinese, one line each and only what applies.
function leaseNotes(rewritten, command, answer) {
  const notes = []
  if (rewritten) notes.push('Purdex mod 把 --maxWorkers=3 加進這個指令（主機資源規則 R7），實際執行的是：' + command)
  if (answer && !answer.fail_open) {
    const ms = Number(answer.waited_ms) || 0
    if (ms >= WAIT_NOTE_MS) {
      const load = Number(answer.host_measured) > 0 ? '（負載 ' + answer.host_measured + '/100）' : ''
      notes.push('這個指令先等了 ' + Math.round(ms / 1000) + ' 秒主機資源' + load + '，不是卡住，不要重試')
    }
  }
  return notes
}

// releaseLease gives the lease back by client id (or by id when there was none), 5 s, errors swallowed: a
// grant whose answer was lost is released too, and a client id the daemon never saw answers `none`. With
// withNotice the release is the end of the command and asks for the final notice (amendment A §3.3); the
// notice, if the daemon gave one, is returned. ok says the release went through (a lease there was nothing to
// release for counts: there is no target); a release that did not is worth one more try.
async function releaseLease($, cfg, clientId, answer, withNotice) {
  const target = clientId ? ['--client-id', clientId] : answer && typeof answer.id === 'string' && answer.id ? [answer.id] : null
  if (!target) return { ok: true, notice: '' }
  try {
    const r = await $.process.run([cfg.bin, 'lease', 'release', ...target, ...(withNotice ? ['--notice', '--json'] : []),
      ...(cfg.config ? ['--config', cfg.config] : [])], { timeoutMs: RELEASE_TIMEOUT_MS })
    if (!r || r.exitCode !== 0) return { ok: false, notice: '' }
    if (!withNotice) return { ok: true, notice: '' }
    const o = parse(String(r.stdout || '').trim())
    return { ok: true, notice: isObject(o) && typeof o.notice === 'string' ? o.notice : '' }
  } catch (err) {
    log($, 'release failed: ' + (err && err.message ? err.message : String(err)))
    return { ok: false, notice: '' }
  }
}

// withNotes adds lines to a tool result's context.
const withNotes = (r, notes) => (notes.length ? { ...r, context: [...(r.context ?? []), ...notes] } : r)

// bgLeases are the leases of commands Claude Code moved to the background: background task id → what releases it.
const bgLeases = new Map()
const BG_LEASES_MAX = 20
// A final notice that arrived with a release nobody could read it at (the task ended between turns) waits here and goes
// into the next Bash result.
const pendingNotices = []

// backgroundTaskOf is the id of the background task a Bash result says its command became, or ''.
function backgroundTaskOf(r) {
  const id = r && r.result && r.result.backgroundTaskId
  return typeof id === 'string' ? id : ''
}

// leaseBackgroundChanged is called with the background tasks each time Claude Code lists them (classic.Stop, through
// events.js): a handed-over lease whose task is no longer listed as running is released, with the final notice, which
// goes to the model in the next Bash result. `$` is used here at the top level only (M-U1-2).
export function leaseBackgroundChanged($, background) {
  if (bgLeases.size === 0) return
  const running = new Set(((background && background.tasks) || []).filter((t) => t.status === 'running').map((t) => t.id))
  for (const [id, h] of [...bgLeases]) {
    if (running.has(id)) continue
    bgLeases.delete(id)
    endBackgroundLease($, h).catch(() => {})
  }
}

// endBackgroundLease is the release at the end of a background command: the same two tries with the notice as the
// foreground, then one without.
async function endBackgroundLease($, h) {
  let rel = await releaseLease($, h.cfg, h.clientId, h.answer, true)
  if (!rel.ok) rel = await releaseLease($, h.cfg, h.clientId, h.answer, true)
  if (!rel.ok) await releaseLease($, h.cfg, h.clientId, h.answer, false)
  if (rel.notice) pendingNotices.push(rel.notice)
}

// leaseCall is one foreground Bash call. A call that is not heavy or is already wrapped in `pdx lease` goes
// straight on.
async function leaseCall($, e, next) {
  const c = classify(e.command)
  if (!c) return next(e)
  // A command with a `pdx lease` of its own anywhere in it is left to that one: inside this lease the inner run
  // would find its own session's position running, be queued (exit 15), and what follows it would be skipped.
  if (hasLeaseSegment(e.command)) return next(e)
  let command = e.command
  let rewritten = false
  if (c.needsMaxWorkers) {
    const w = rewriteMaxWorkers(command)
    if (w.changed) { command = w.command; rewritten = true }
  }
  const cfg = await leaseConfig($)
  const sid = await bounded($, () => $.session.id()) // this call's: a /clear or a resume is picked up by the next one
  if (typeof sid !== 'string' || !sid) {
    // Without a session the lease cannot be asked for: the command runs, with the cap if it was given one.
    log($, 'no session id, the command runs without a lease')
    return withNotes(await next(rewritten ? { ...e, command } : e), leaseNotes(rewritten, command, null))
  }
  const cwd = await bounded($, () => $.session.cwd())
  const clientId = newClientId()
  let answer = null
  let done = false // the lease is no longer ours to release: queued, or released after the command
  try {
    try {
      answer = readAcquire(await $.process.run([cfg.bin, 'lease', 'acquire', '--kind', c.kind, '--session', sid,
        '--tool-use', String(e.tool_use_id || ''), '--command', cutBytes(e.command, COMMAND_MAX),
        ...(typeof cwd === 'string' && cwd ? ['--cwd', cwd] : []),
        ...(clientId ? ['--client-id', clientId] : []),
        ...(cfg.config ? ['--config', cfg.config] : [])], { timeoutMs: ACQUIRE_TIMEOUT_MS }))
    } catch (err) {
      log($, 'acquire failed, the command runs: ' + (err && err.message ? err.message : String(err)))
    }
    if (isQueued(answer)) {
      // The host is busy and this request waits at its place: the command is not run, nothing is released (a
      // release by client id would cancel the queued row). The model reads where it stands as the tool's own
      // result, and the same command again takes the place back.
      done = true
      return { deny: queuedDeny(answer) }
    }
    const r = await next(rewritten ? { ...e, command } : e)
    // Claude Code moved the command to the background (it ran past its timeout, or the person pressed Ctrl+B) and
    // `next` resolved at once while the process runs on: the lease is not released now. It is handed to the
    // watcher, which releases it when the task ends (leaseBackgroundChanged). If that never happens (a reload of
    // the mod loses the watcher) the daemon's sweeper ends it once nothing of the session runs any more (#2841).
    const taskId = backgroundTaskOf(r)
    if (taskId) {
      done = true
      bgLeases.set(taskId, { cfg, clientId, answer })
      // Past the cap the oldest is let go now, not forgotten: a handle that is dropped can never be released.
      while (bgLeases.size > BG_LEASES_MAX) {
        const oldest = bgLeases.keys().next().value
        const h = bgLeases.get(oldest)
        bgLeases.delete(oldest)
        releaseLease($, h.cfg, h.clientId, h.answer, false).catch(() => {})
      }
      return withNotes(r, [...leaseNotes(rewritten, command, answer),
        '這個指令跑得比 Claude Code 的前景時限久，被移到背景；它還在跑，主機資源會留給它，等它結束才放掉。'])
    }
    // The command has ended: this release is the end the daemon counts, and asks for the final notice. One that
    // did not go through (the 5 s ran out, the answer was lost) is tried once more with the notice: the daemon
    // keeps the notice on the row for a repeated release. Still no answer: finally lets go without one.
    let rel = await releaseLease($, cfg, clientId, answer, true)
    if (!rel.ok) rel = await releaseLease($, cfg, clientId, answer, true)
    done = rel.ok
    return withNotes(r, [...leaseNotes(rewritten, command, answer), ...(rel.notice ? [rel.notice] : [])])
  } finally {
    // The way out of a throw, or of a release that never answered: without a notice (the command did not end the
    // way a notice assumes, or the notice cannot be had).
    if (!done) await releaseLease($, cfg, clientId, answer, false)
  }
}

// An interactive session is one a person is at: its mod can be told the daemon's notices. Set by session.start.
let interactive = false

// shellPath is the user's shell as a literal path (M-P2 (c): a `"${SHELL:-/bin/sh}"` or `"$SHELL"` in the wrapped
// command makes the engine ask for permission, a literal path does not); /bin/sh when the environment has none or
// something that is not a plain path.
async function shellPath($) {
  const sh = await bounded($, () => $.env.get('SHELL'))
  return typeof sh === 'string' && /^\/[A-Za-z0-9_./+-]+$/.test(sh) ? sh : '/bin/sh'
}

// backgroundCall wraps a heavy background command in `pdx lease run --ask` (T25), in an interactive session
// only (nothing can ask any other), and only when the wrap does not change the permission verdict: a prompt that
// the original command would not have raised is the cost the mod must not add (Q1). A command already going
// through `pdx lease` (classify says null) is left as it is.
async function backgroundCall($, e, next) {
  if (!interactive) return next(e)
  const c = classify(e.command)
  if (!c) return next(e)
  // A command with a `pdx lease` of its own anywhere in it is not put inside another lease: the inner one would
  // find its own session's position running and be queued, and what follows it would be skipped.
  if (hasLeaseSegment(e.command)) return next(e)
  const cfg = await leaseConfig($)
  const inner = c.needsMaxWorkers ? rewriteMaxWorkers(e.command).command : e.command
  const shell = await shellPath($)
  const wrapped = [sq(cfg.bin), 'lease', 'run', '--ask', '--kind', c.kind, ...(cfg.config ? ['--config', sq(cfg.config)] : []),
    '--command', sq(cutBytes(e.command, COMMAND_MAX)), '--', shell, '-c', sq(inner)].join(' ')
  const verdict = async (command) => {
    const v = await bounded($, () => $.tool.check({ tool: 'Bash', input: { command, run_in_background: true } }))
    return v && typeof v.decision === 'string' ? v.decision : null
  }
  const [before, after] = [await verdict(e.command), await verdict(wrapped)]
  if (before === null || before !== after) {
    log($, 'the wrap would change the permission verdict (' + before + ' → ' + after + '): the command runs as it is')
    return withNotes(await next(e), ['這個背景指令沒有納入資源排隊'])
  }
  return withNotes(await next({ ...e, command: wrapped }), [
    'Purdex mod 把這個背景指令包進 pdx lease run --ask（主機資源排隊）：主機忙時它在背景等，輪到時你會收到問題，用 pdx lease confirm <id> 或 pdx lease cancel <id> 回答；不要自己加 & 或重跑。'])
}

export function registerLease(on) {
  on('session.start', { isInteractive: true }, async ($, e, next) => {
    interactive = true
    return next(e)
  })
  on('tool.call', { tool: 'Bash' }, async ($, e, next) => {
    const r = await (e.run_in_background === true ? backgroundCall($, e, next) : leaseCall($, e, next))
    // A notice a background release left behind goes in with the next Bash result (never into a deny).
    return pendingNotices.length && r && typeof r === 'object' && !('deny' in r) ? withNotes(r, pendingNotices.splice(0)) : r
  }).catch(($, e, next) => next(e))
}
