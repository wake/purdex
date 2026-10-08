// Purdex mod — host resource lease, the classifier (host-resource-lease plan Task 2.1, spec D-7, R7, R8).
//
// Pure functions: they read a Bash command string and say whether it is a heavy command (a full test run, a
// build, a lint of everything) and of which kind, and rewrite a full vitest run to cap its workers. Nothing here
// touches the engine ($), so it runs, and is tested, as plain code. The hook that uses them is registerLease
// (P2-2); until then this file is not imported.
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
  const heredocs = [] // {delim, strip} awaiting the next newline

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
        // the body: lines up to one that is the delimiter; none of it is a command
        for (;;) {
          const nl = cmd.indexOf('\n', i + 1)
          const line = cmd.slice(i + 1, nl < 0 ? cmd.length : nl)
          i = nl < 0 ? cmd.length : nl
          if ((h.strip ? line.replace(/^\t+/, '') : line) === h.delim || nl < 0) break
        }
      }
      continue
    }
    if (c === ';') { endSegment(i); continue }
    if (c === '#' && !cur) { while (i + 1 < cmd.length && cmd[i + 1] !== '\n') i++; continue }
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
        for (; j < cmd.length && !' \t\n;|&()<>'.includes(cmd[j]); j++) if (!'\'"\\'.includes(cmd[j])) delim += cmd[j]
        heredocs.push({ delim, strip })
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
  for (let i = 0; i < cmd.length; i++) {
    const c = cmd[i]
    if (single) { if (c === "'") single = false; continue }
    if (c === "'") { single = true; continue }
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
// them. A segment already going through `pdx lease` is skipped, not the whole command: what follows it
// is not covered by its lease. null for none or an unparseable command.
function classifyKind(cmd, depth) {
  const segs = scan(cmd)
  if (segs === null) return null
  let best = null
  const consider = (k) => {
    if (k && k !== 'wrapped' && (best === null || ORDER.indexOf(k) < ORDER.indexOf(best))) best = k
  }
  for (const s of segs) consider(kindOf(commandOf(s), depth))
  if (depth < 3) for (const inner of substitutions(cmd)) consider(classifyKind(inner, depth + 1))
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
