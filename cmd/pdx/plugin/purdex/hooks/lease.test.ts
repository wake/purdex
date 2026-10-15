// hooks/lease.test.ts — the heavy-command classifier (host-resource-lease plan Task 2.1), run by
// `claude plugin test cmd/pdx/plugin/purdex`. Pure functions, so no engine fakes: a table of commands
// and what they are.
import { test, expect } from 'claude-code/testing'
import { classify, rewriteMaxWorkers } from './lease.js'

const kind = (c: string) => classify(c)?.kind ?? null

const KINDS: [string, string | null][] = [
  // test-full
  ['cd spa && npx vitest run', 'test-full'],
  ['npx vitest run', 'test-full'],
  ['pnpm exec vitest run', 'test-full'],
  ['pnpm -C spa exec vitest run --reporter=dot', 'test-full'],
  ['yarn vitest run', 'test-full'],
  ['vitest run', 'test-full'],
  ['FOO=1 npx vitest run', 'test-full'],
  ['timeout 600 npx vitest run', 'test-full'],
  ['cd spa && npx vitest run 2>&1 | tail -30', 'test-full'],
  ['npx vitest run --maxWorkers=3', 'test-full'],
  ['go test -race ./...', 'test-full'],
  ['go test ./...', 'test-full'],
  ['cd cmd && go test ./internal/...', 'test-full'],
  ['make test', 'test-full'],
  ['bash -c "cd spa && npx vitest run"', 'test-full'],
  // narrowed vitest is not heavy
  ['npx vitest run src/lib/foo.test.ts', null],
  ['cd spa && npx vitest run src/lib/foo.test.ts src/lib/bar.test.ts', null],
  ['npx vitest run -t "x"', null],
  ['npx vitest run --testNamePattern x', null],
  ['npx vitest run --project spa', null],
  ['npx vitest run --changed', null],
  ['npx vitest run --dom src/foo.test.ts', null],
  ['npx vitest run --logHeapUsage src/foo.test.ts', null],
  ['npx vitest', null],
  ['npx vitest watch', null],
  ['npx vitest related src/a.ts', null],
  // go
  ['go test -race ./internal/module/team/', 'test-pkg'],
  ['go test -race ./internal/module/team', 'test-pkg'],
  ['go test ./cmd/pdx -run TestX', null],
  ['go test -race ./cmd/pdx -run TestX', null],
  ['go test ./internal/module/team/', null],
  ['go test -race ./a ./b', null],
  ['go test -count=1 ./cmd/pdx', null],
  ['go build ./...', null],
  // build
  ['cd spa && pnpm run build', 'build'],
  ['pnpm build', 'build'],
  ['npm run build', 'build'],
  ['pnpm run electron:build', 'build'],
  ['tsc -b', 'build'],
  ['npx tsc --build', 'build'],
  ['npx vite build', 'build'],
  ['npx electron-vite build', 'build'],
  ['tsc --noEmit -p tsconfig.app.json', null],
  ['pnpm install', null],
  // lint-full
  ['go vet ./...', 'lint-full'],
  ['npx eslint .', 'lint-full'],
  ['cd spa && pnpm run lint', 'lint-full'],
  ['npx eslint src/lib/foo.ts', null],
  ['go vet ./cmd/pdx', null],
  // heaviest segment decides
  ['pnpm run lint && npx vitest run', 'test-full'],
  ['go vet ./... && pnpm run build', 'build'],
  // not commands, wrapped, background, unparseable
  ['pdx lease run --kind build -- pnpm run build', null],
  ['cd spa && pdx lease run --kind test-full -- npx vitest run', null],
  ['echo "npx vitest run"', null],
  ['pdx lease run --kind build -- pnpm run build && npx vitest run', 'test-full'],
  ['pdx lease run --kind build -- pnpm run build && ls', null],
  ["echo 'pnpm run build'", null],
  ['grep -r "vitest run" docs', null],
  ['ls', null],
  ['', null],
  ['npx vitest run "unterminated', null],
  ['npx vitest run &', null],
  ['pnpm run build & sleep 1', null],
  ['npx vitest run > out.txt 2>&1', 'test-full'],
  // attached redirections end the word
  ['npx vitest run>out.txt', 'test-full'],
  ['npx vitest run>>out.txt', 'test-full'],
  ['npx vitest run 2>/dev/null', 'test-full'],
  ['npx vitest run &>out.txt', 'test-full'],
  ['npx vitest run <in.txt', 'test-full'],
  ['npx vitest run src/a.test.ts>out.txt', null],
  // substitutions and other shells run the command too
  ['echo $(npx vitest run)', 'test-full'],
  ['echo "$(npx vitest run)"', 'test-full'],
  ['echo `npx vitest run`', 'test-full'],
  ['x=$(cd spa && pnpm run build)', 'build'],
  ["echo '$(npx vitest run)'", null],
  ['echo "it\'s $(npx vitest run)"', 'test-full'],
  ["echo \"it's\" '$(npx vitest run)'", null],
  ['fish -c "npx vitest run"', 'test-full'],
  ['echo $(echo $(npx vitest run))', 'test-full'],
  // heredoc bodies and comments are data
  ['cat <<EOF\nnpx vitest run\nEOF', null],
  ["cat <<'EOF'\nnpx vitest run\nEOF\nnpx vitest run", 'test-full'],
  ['cat <<-EOF\n\tpnpm run build\n\tEOF', null],
  ['cat <<A <<B\nnpx vitest run\nA\npnpm run build\nB', null],
  ['cat > f <<EOF\nx\nEOF\ngo vet ./...', 'lint-full'],
  ['echo ok # npx vitest run', null],
  ["cat <<'END X'\ndata\nEND X\nnpx vitest run", 'test-full'],
  ["cat <<\"E E\"\nnpx vitest run\nE E", null],
  ['# npx vitest run\nls', null],
  // a quoted heredoc's body and a comment hold no substitution that runs (the real edit scripts are full of backticks)
  ["python3 - <<'EOF'\ns = 'run `go test ./...` here'\nEOF", null],
  ['ls # $(npx vitest run)', null],
  ["cat <<'EOF'\n$(go vet ./...)\nEOF\necho $(go vet ./...)", 'lint-full'],
  ['cat <<EOF\n$(go vet ./...)\nEOF', 'lint-full'],
  ['ls # ; npx vitest run', null],
  ['echo a#b && npx vitest run', 'test-full'],
]

for (const [cmd, want] of KINDS) {
  test(`classify ${JSON.stringify(cmd)} → ${want}`, () => {
    expect(kind(cmd)).toBe(want)
  })
}

test('classify: a non-string command is not heavy', () => {
  expect(classify(undefined as unknown as string)).toBe(null)
  expect(classify(42 as unknown as string)).toBe(null)
})

test('classify: needsMaxWorkers says a full vitest run has no worker limit', () => {
  expect(classify('npx vitest run')?.needsMaxWorkers).toBe(true)
  expect(classify('npx vitest run --maxWorkers=3')?.needsMaxWorkers).toBe(false)
  expect(classify('npx vitest run --max-workers 2')?.needsMaxWorkers).toBe(false)
  expect(classify('npx vitest run --poolOptions.threads.maxThreads=2')?.needsMaxWorkers).toBe(false)
  expect(classify('bash -c "npx vitest run"')?.needsMaxWorkers).toBe(true)
  expect(classify('pnpm run build')?.needsMaxWorkers).toBe(false)
  expect(classify('go test -race ./...')?.needsMaxWorkers).toBe(false)
})

const REWRITES: [string, string][] = [
  ['cd spa && npx vitest run', 'cd spa && npx vitest run --maxWorkers=3'],
  ['FOO=1 npx vitest run', 'FOO=1 npx vitest run --maxWorkers=3'],
  ['npx vitest run --reporter=dot', 'npx vitest run --reporter=dot --maxWorkers=3'],
  ['npx vitest run 2>&1 | tail -30', 'npx vitest run --maxWorkers=3 2>&1 | tail -30'],
  ['npx vitest run > out.txt 2>&1', 'npx vitest run --maxWorkers=3 > out.txt 2>&1'],
  ['cd spa && npx vitest run && pnpm run build', 'cd spa && npx vitest run --maxWorkers=3 && pnpm run build'],
  ['bash -c "cd spa && npx vitest run"', 'bash -c "cd spa && npx vitest run --maxWorkers=3"'],
  ["sh -c 'npx vitest run 2>&1 | tail'", "sh -c 'npx vitest run --maxWorkers=3 2>&1 | tail'"],
  ['npx vitest run>out.txt', 'npx vitest run --maxWorkers=3>out.txt'],
  ['npx vitest run 2>/dev/null', 'npx vitest run --maxWorkers=3 2>/dev/null'],
  ['pdx lease run --kind build -- ls && npx vitest run', 'pdx lease run --kind build -- ls && npx vitest run --maxWorkers=3'],
  ['npx vitest run; npx vitest run', 'npx vitest run --maxWorkers=3; npx vitest run --maxWorkers=3'],
]

for (const [cmd, want] of REWRITES) {
  test(`rewriteMaxWorkers ${JSON.stringify(cmd)}`, () => {
    expect(rewriteMaxWorkers(cmd)).toEqual({ command: want, changed: true })
  })
}

const UNCHANGED = [
  'cat <<EOF\nnpx vitest run\nEOF',
  'echo ok # npx vitest run',
  'ls # ; npx vitest run',
  'npx vitest run --maxWorkers=3',
  'npx vitest run --max-workers=2',
  'npx vitest run src/lib/foo.test.ts',
  'npx vitest run -t "x"',
  'go test -race ./...',
  'pnpm run build',
  'echo "npx vitest run"',
  'npx vitest run "unterminated',
  'npx vitest run &',
  'pdx lease run --kind test-full -- npx vitest run',
]

for (const cmd of UNCHANGED) {
  test(`rewriteMaxWorkers leaves ${JSON.stringify(cmd)} alone`, () => {
    expect(rewriteMaxWorkers(cmd)).toEqual({ command: cmd, changed: false })
  })
}

test('rewriteMaxWorkers: the rewritten command still classifies as test-full with the limit given', () => {
  const r = rewriteMaxWorkers('cd spa && npx vitest run 2>&1 | tail -30')
  expect(classify(r.command)).toEqual({ kind: 'test-full', needsMaxWorkers: false })
})

// ---- the hook: registerLease (plan Task 2.2) ----
//
// The test's `on` hooks stand beneath the whole mod as the engine: `process.run` is `pdx` (recording argv and
// answering), `session.id` is the session, `tool.call` for Bash is the tool, counting its runs.

const ok = (stdout: string, exitCode = 0, stderr = '') => ({ value: { exitCode, stdout, stderr, isStdoutTruncated: false, isStderrTruncated: false } })
const GRANT = (extra: object = {}) => JSON.stringify({ id: 'lease-1', granted: true, ...extra })
const BASH_OK = { ref: 1, result: { stdout: 'ok', stderr: '', interrupted: false }, text: 'ok' }
const UUID4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/

type Rig = { calls: string[][]; ran: string[]; sid: { v: string; mode: 'ok' | 'throw' | 'hang' }; checks: string[]; verdict: (command: string) => string; env: Record<string, string> }
// rig stands the engine up: `answer` is what pdx says to each call (by subcommand), `bash` the tool beneath.
function rig(on: any, answer: (argv: string[]) => any, bash: (e: any) => any = () => BASH_OK, pdxJSON?: string): Rig {
  const r: Rig = { calls: [], ran: [], sid: { v: 'sess-1', mode: 'ok' }, checks: [], verdict: () => 'allow', env: { SHELL: '/bin/zsh' } }
  on('session.id', () => {
    if (r.sid.mode === 'throw') throw new Error('the engine refused session.id')
    if (r.sid.mode === 'hang') return new Promise(() => {})
    return { value: r.sid.v }
  })
  on('session.start', async (_$: any, e: any) => ({ cwd: e.cwd }))
  on('env.get', (_$: any, e: any) => ({ value: r.env[e.name] }))
  on('classic.Stop', async () => ({}))
  on('session.cwd', () => ({ value: '/work/purdex' }))
  on('tool.check', { tool: 'Bash' }, (_$: any, e: any) => { r.checks.push(e.input.command); return { decision: r.verdict(e.input.command) } })
  on('clock.sleep', async (_$: any, e: any) => { await new Promise((res) => setTimeout(res, e.ms)); return { value: undefined } })
  on('fs.read', (_$: any, e: any) => (pdxJSON !== undefined && e.path.endsWith('/pdx.json') ? { value: pdxJSON } : { deny: 'ENOENT' }))
  on('ui.log', () => ({ value: undefined }))
  // gate.js (registered before the lease hook) asks `pdx gate would-wait`; these tests are about the lease, so that question is
  // answered "no" and not counted (gate.test.ts covers it, and the order of the two hooks).
  on('process.run', (_$: any, e: any) => {
    if (e.argv[1] === 'gate') return { value: { exitCode: 0, stdout: '{"would_wait":false}', stderr: '', isStdoutTruncated: false, isStderrTruncated: false } }
    r.calls.push([...e.argv])
    return answer(e.argv)
  })
  on('tool.call', { tool: 'Bash' }, (_$: any, e: any) => { r.ran.push(e.command); return bash(e) })
  return r
}
const sub = (a: string[]) => a[2] // <pdx> lease <sub> …
const arg = (a: string[], flag: string) => a[a.indexOf(flag) + 1]
const bash = ($: any, command: string, extra: object = {}) => $.tool.call({ tool: 'Bash', command, tool_use_id: 'toolu_1', ...extra })

test('a heavy foreground Bash acquires, runs the rewritten command once, releases by client id', async ($, on) => {
  const r = rig(on, (a) => (sub(a) === 'acquire' ? ok(GRANT()) : ok('{"released":true}')))
  const out = await bash($, 'cd spa && npx vitest run')
  expect(out.result).toEqual(BASH_OK.result)
  expect(r.ran).toEqual(['cd spa && npx vitest run --maxWorkers=3'])
  expect(r.calls.map(sub)).toEqual(['acquire', 'release'])
  const [acq, rel] = r.calls
  expect(acq.slice(0, 3)).toEqual(['pdx', 'lease', 'acquire'])
  expect(arg(acq, '--kind')).toBe('test-full')
  expect(arg(acq, '--session')).toBe('sess-1')
  expect(arg(acq, '--tool-use')).toBe('toolu_1')
  expect(arg(acq, '--client-id')).toMatch(UUID4)
  expect(acq).not.toContain('--json') // acquire prints its one JSON line without it
  // The queue shows the command as the model gave it (not the capped one) and where it runs; no mode: the daemon decides.
  expect(arg(acq, '--command')).toBe('cd spa && npx vitest run')
  expect(arg(acq, '--cwd')).toBe('/work/purdex')
  for (const flag of ['--queue', '--ask', '--wait']) expect(acq).not.toContain(flag)
  // The release is the end of the command: it asks for the final notice.
  expect(rel).toEqual(['pdx', 'lease', 'release', '--client-id', arg(acq, '--client-id'), '--notice', '--json'])
})

test('the context names the rewrite, with the command that really ran', async ($, on) => {
  rig(on, (a) => (sub(a) === 'acquire' ? ok(GRANT()) : ok('{}')))
  const out = await bash($, 'npx vitest run')
  expect(out.context).toEqual(['Purdex mod 把 --maxWorkers=3 加進這個指令（主機資源規則 R7），實際執行的是：npx vitest run --maxWorkers=3'])
})

test('the context says plainly that the command waited', async ($, on) => {
  const answer = GRANT({ waited_ms: 37000, host_measured: 72 })
  rig(on, (a) => (sub(a) === 'acquire' ? ok(answer) : ok('{}')))
  expect((await bash($, 'pnpm run build')).context).toEqual(['這個指令先等了 37 秒主機資源（負載 72/100），不是卡住，不要重試'])
})

test('a wait under a second is not announced; a fail-open answer says nothing', async ($, on) => {
  let answer = GRANT({ waited_ms: 400 })
  rig(on, (a) => (sub(a) === 'acquire' ? ok(answer) : ok('{}')))
  expect((await bash($, 'pnpm run build')).context).toBeUndefined()
  answer = JSON.stringify({ granted: true, fail_open: 'daemon_unreachable' })
  expect((await bash($, 'pnpm run build')).context).toBeUndefined()
})

test('background Bash is not intercepted when no interactive session.start was seen', async ($, on) => {
  const r = rig(on, () => ok(GRANT()))
  await bash($, 'npx vitest run', { run_in_background: true })
  expect(r.calls).toEqual([])
  expect(r.ran).toEqual(['npx vitest run'])
})

test('non-heavy Bash passes through untouched (no process.run)', async ($, on) => {
  const r = rig(on, () => ok(GRANT()))
  await bash($, 'ls -la')
  await bash($, 'npx vitest run src/lib/foo.test.ts')
  expect(r.calls).toEqual([])
  expect(r.ran).toEqual(['ls -la', 'npx vitest run src/lib/foo.test.ts'])
})

test('already wrapped in pdx lease run → untouched', async ($, on) => {
  const r = rig(on, () => ok(GRANT()))
  await bash($, 'pdx lease run --kind build -- pnpm run build')
  expect(r.calls).toEqual([])
})

test('acquire fails (non-zero, junk, rejected) → the command runs, release still goes by the same client id', async ($, on) => {
  let acquire: any = ok('', 20, 'pdx lease: daemon unreachable')
  const r = rig(on, (a) => (sub(a) === 'acquire' ? acquire : ok('{}')))
  for (const [i, a] of [ok('', 20, 'pdx lease: daemon unreachable'), ok('not json at all'), { deny: 'spawn failed' }].entries()) {
    acquire = a
    const out = await bash($, 'pnpm run build')
    expect(out.result).toEqual(BASH_OK.result)
    expect(r.ran.length).toBe(i + 1)
    expect(r.ran[i]).toBe('pnpm run build')
    const mine = r.calls.slice(i * 2)
    expect(mine.map(sub)).toEqual(['acquire', 'release'])
    expect(mine[1][4]).toBe(arg(mine[0], '--client-id'))
  }
})

test('release runs even when the tool throws', async ($, on) => {
  const r = rig(on, (a) => (sub(a) === 'acquire' ? ok(GRANT()) : ok('{}')), () => { throw new Error('the tool failed') })
  await bash($, 'pnpm run build').catch(() => {})
  expect(r.calls.map(sub)).toEqual(['acquire', 'release'])
  expect(r.ran).toEqual(['pnpm run build']) // once: a throw after next never re-runs the tool
})

// The throw path does not ask for the final notice: the command did not end the way a notice assumes (#2).
// (M: pass --notice in finally.)
test('the throw path releases without --notice', async ($, on) => {
  const r = rig(on, (a) => (sub(a) === 'acquire' ? ok(GRANT()) : ok('{}')), () => { throw new Error('the tool failed') })
  await bash($, 'pnpm run build').catch(() => {})
  const rel = r.calls.find((a) => sub(a) === 'release')!
  expect(rel).not.toContain('--notice')
  expect(rel).not.toContain('--json')
  expect(r.calls.filter((a) => sub(a) === 'release').length).toBe(1)
})

test('a release that fails is swallowed', async ($, on) => {
  rig(on, (a) => (sub(a) === 'acquire' ? ok(GRANT()) : { deny: 'release failed' }))
  expect((await bash($, 'pnpm run build')).result).toEqual(BASH_OK.result)
})

test('subagent Bash is intercepted the same way', async ($, on) => {
  const r = rig(on, (a) => (sub(a) === 'acquire' ? ok(GRANT()) : ok('{}')))
  await bash($, 'pnpm run build', { agentId: 'agent-7' })
  expect(r.calls.map(sub)).toEqual(['acquire', 'release'])
})

test('after /clear the lease carries the new session id', async ($, on) => {
  const r = rig(on, (a) => (sub(a) === 'acquire' ? ok(GRANT()) : ok('{}')))
  await bash($, 'pnpm run build')
  r.sid.v = 'sess-2'
  await bash($, 'pnpm run build')
  const acquires = r.calls.filter((a) => sub(a) === 'acquire')
  expect(acquires.map((a) => arg(a, '--session'))).toEqual(['sess-1', 'sess-2'])
})

test('pdx.json names the binary and the config, for acquire and release alike', async ($, on) => {
  const r = rig(on, (a) => (sub(a) === 'acquire' ? ok(GRANT()) : ok('{}')), () => BASH_OK, '{"pdx":"/opt/pdx/bin/pdx","config":"/tmp/pdx b/config.toml"}')
  await bash($, 'pnpm run build')
  for (const a of r.calls) {
    expect(a[0]).toBe('/opt/pdx/bin/pdx')
    expect(arg(a, '--config')).toBe('/tmp/pdx b/config.toml')
  }
})

for (const mode of ['throw', 'hang'] as const) {
  test(`session.id ${mode === 'throw' ? 'rejects' : 'never answers'} → no lease is asked for, the command runs once with the worker cap`, async ($, on) => {
    const r = rig(on, () => ok(GRANT()))
    r.sid.mode = mode
    const out = await bash($, 'npx vitest run')
    expect(out.result).toEqual(BASH_OK.result)
    expect(r.calls).toEqual([])
    expect(r.ran).toEqual(['npx vitest run --maxWorkers=3'])
    expect(out.context?.[0]).toContain('--maxWorkers=3')
  })
}

// ---- queued, the final notice, the background wrap (lease v2 amendment A, T23, T25, T26) ----

const QUEUED = (extra: object = {}) => JSON.stringify({ id: 'lease-1', state: 'queued', position: 2, ...extra })
const startInteractive = ($: any) => $.session.start({ cwd: '/work', surface: 'terminal', isInteractive: true })

// A queued answer is not waited for: the command does not run, the model reads the text as the deny, and nothing
// is released (a release by client id would cancel the queued row). (M: call next on queued.)
test('queued: the command is not run, the model gets the queued text as a deny, and nothing is released', async ($, on) => {
  const r = rig(on, (a) => (sub(a) === 'acquire' ? ok(QUEUED(), 15) : ok('{}')))
  const out = await bash($, 'pnpm run build')
  expect(out.deny ?? out.error ?? JSON.stringify(out)).toContain('主機資源忙，這個指令還沒執行：排在第 2 位。輪到你時我會通知你，先做別的事；不要改成背景執行或加 & 繞過排隊。')
  expect(r.ran).toEqual([])
  expect(r.calls.map(sub)).toEqual(['acquire'])
})

test('own_running: the deny says to wait for its own previous command', async ($, on) => {
  rig(on, (a) => (sub(a) === 'acquire' ? ok(QUEUED({ queued_reason: 'own_running' }), 15) : ok('{}')))
  const text = JSON.stringify(await bash($, 'pnpm run build'))
  expect(text).toContain('等你自己的上一個指令跑完')
  expect(text).not.toContain('第 2 位')
})

test('exit 15 without a queued line runs the command and releases', async ($, on) => {
  const r = rig(on, (a) => (sub(a) === 'acquire' ? ok(GRANT(), 15) : ok('{}')))
  await bash($, 'pnpm run build')
  expect(r.ran).toEqual(['pnpm run build'])
  expect(r.calls.map(sub)).toEqual(['acquire', 'release'])
})

test('the final notice from the release answer is added to the tool result', async ($, on) => {
  const notice = '有其他 session 在排隊，下一個指令會是這次的最後一個；之後還要跑請重新排隊。'
  const r = rig(on, (a) => (sub(a) === 'acquire' ? ok(GRANT()) : ok(JSON.stringify({ state: 'ended', notice }))))
  const out = await bash($, 'pnpm run build')
  expect(out.context).toEqual([notice])
  // the release comes after the command ran (M: release back in finally only)
  expect(r.ran).toEqual(['pnpm run build'])
  expect(r.calls.map(sub)).toEqual(['acquire', 'release'])
  expect(r.calls[1]).toContain('--notice')
})

test('a release answer without a notice adds nothing', async ($, on) => {
  rig(on, (a) => (sub(a) === 'acquire' ? ok(GRANT()) : ok(JSON.stringify({ state: 'ended' }))))
  expect((await bash($, 'pnpm run build')).context).toBeUndefined()
})

// ---- the background wrap ----

const BG = 'pnpm run build'
const wrappedOf = (r: Rig) => r.ran[0]

test('a heavy background Bash is wrapped in pdx lease run --ask with its kind and command', async ($, on) => {
  const r = rig(on, () => ok(GRANT()))
  await startInteractive($)
  const out = await bash($, BG, { run_in_background: true })
  expect(wrappedOf(r)).toBe(`'pdx' lease run --ask --kind build --command 'pnpm run build' -- /bin/zsh -c 'pnpm run build'`)
  expect(wrappedOf(r)).not.toContain('$') // a variable in the wrapped command makes the engine ask (M-P2 (c))
  expect(r.calls).toEqual([]) // the wrapped command asks for itself; the mod does not
  // the wrap is announced
  expect(out.context?.[0]).toContain('pdx lease run --ask')
  expect(out.context?.[0]).toContain('pdx lease confirm')
})

test('the wrap quotes the command for the shell, and the worker cap goes inside it', async ($, on) => {
  const r = rig(on, () => ok(GRANT()))
  await startInteractive($)
  await bash($, `pnpm run build -- --name="it's"; npx vitest run`, { run_in_background: true })
  const w = wrappedOf(r)
  // every single quote inside a quoted word is closed, escaped and opened again
  expect(w).toContain(`--command 'pnpm run build -- --name="it'\\''s"; npx vitest run'`)
  expect(w).toContain(`-c 'pnpm run build -- --name="it'\\''s"; npx vitest run --maxWorkers=3'`)
})

test('the shell is a literal path: SHELL from the environment, else /bin/sh', async ($, on) => {
  const r = rig(on, () => ok(GRANT()))
  await startInteractive($)
  r.env.SHELL = '/opt/homebrew/bin/fish'
  await bash($, BG, { run_in_background: true })
  r.env.SHELL = '$(evil) x'
  await bash($, BG, { run_in_background: true })
  delete r.env.SHELL
  await bash($, BG, { run_in_background: true })
  expect(r.ran.map((c) => c.split(' -- ')[1].split(' -c ')[0])).toEqual(['/opt/homebrew/bin/fish', '/bin/sh', '/bin/sh'])
})

test('a background pdx lease run is left as it is, not wrapped twice', async ($, on) => {
  const r = rig(on, () => ok(GRANT()))
  await startInteractive($)
  await bash($, 'pdx lease run --ask --kind build -- pnpm run build', { run_in_background: true })
  expect(r.ran).toEqual(['pdx lease run --ask --kind build -- pnpm run build'])
})

test('a non-interactive heavy background Bash is not wrapped', async ($, on) => {
  const r = rig(on, () => ok(GRANT()))
  await $.session.start({ cwd: '/work', surface: null, isInteractive: false })
  await bash($, BG, { run_in_background: true })
  expect(r.ran).toEqual([BG])
})

// The wrap must not add a prompt (M-P2 (a)): it goes in only when the engine decides the same about the original
// and the wrapped command. (M: skip the check.)
test('a wrap that would change the permission verdict is not made, and the model is told', async ($, on) => {
  const r = rig(on, () => ok(GRANT()))
  r.verdict = (c) => (c === BG ? 'allow' : 'ask')
  await startInteractive($)
  const out = await bash($, BG, { run_in_background: true })
  expect(r.ran).toEqual([BG])
  expect(out.context).toEqual(['這個背景指令沒有納入資源排隊'])
  expect(r.checks.length).toBe(2)
})

test('a verdict that cannot be read is not a reason to wrap', async ($, on) => {
  const r = rig(on, () => ok(GRANT()))
  r.verdict = () => { throw new Error('no verdict') }
  await startInteractive($)
  await bash($, BG, { run_in_background: true })
  expect(r.ran).toEqual([BG])
})

test('a non-heavy background Bash only passes through', async ($, on) => {
  const r = rig(on, () => ok(GRANT()))
  await startInteractive($)
  await bash($, 'sleep 30', { run_in_background: true })
  expect(r.ran).toEqual(['sleep 30'])
  expect(r.checks).toEqual([])
})

// ---- review: a release that did not answer, a mixed wrapped command ----

// The release after the command may not answer (the 5 s ran out): it is tried once more with the notice, which the
// daemon keeps on the row for a repeated release; and if that fails too the lease is still let go.
// (M: mark the lease done before the release.)
test('a release that does not answer is tried again and the notice is not lost', async ($, on) => {
  const notice = '有其他 session 在排隊，下一個指令會是這次的最後一個；之後還要跑請重新排隊。'
  let n = 0
  const r = rig(on, (a) => {
    if (sub(a) === 'acquire') return ok(GRANT())
    return ++n === 1 ? ok('', 20, 'pdx lease: daemon unreachable') : ok(JSON.stringify({ state: 'ended', notice }))
  })
  const out = await bash($, 'pnpm run build')
  expect(out.context).toEqual([notice])
  const rels = r.calls.filter((a) => sub(a) === 'release')
  expect(rels.length).toBe(2)
  for (const rel of rels) expect(rel).toContain('--notice')
})

test('a release that throws twice still ends with a release without a notice', async ($, on) => {
  const r = rig(on, (a) => (sub(a) === 'acquire' ? ok(GRANT()) : { deny: 'release failed' }))
  const out = await bash($, 'pnpm run build')
  expect(out.result).toEqual(BASH_OK.result)
  const rels = r.calls.filter((a) => sub(a) === 'release')
  expect(rels.length).toBe(3)
  expect(rels.map((a) => a.includes('--notice'))).toEqual([true, true, false])
  expect(r.ran).toEqual(['pnpm run build']) // the command ran once
})

test('a background command with a pdx lease segment anywhere is not wrapped', async ($, on) => {
  const r = rig(on, () => ok(GRANT()))
  await startInteractive($)
  for (const c of ['pdx lease run --ask --kind build -- pnpm run build && pnpm run build', 'pnpm run build; pdx lease run --kind build -- ls', 'pdx lease ls\npnpm run build']) await bash($, c, { run_in_background: true })
  expect(r.ran).toEqual(['pdx lease run --ask --kind build -- pnpm run build && pnpm run build', 'pnpm run build; pdx lease run --kind build -- ls', 'pdx lease ls\npnpm run build'])
  expect(r.checks).toEqual([])
})

// Inside this lease the inner run would be queued behind its own session's running position and what follows it
// skipped, so a foreground command with a pdx lease segment is left alone too. (M: drop the check.)
test('a foreground command with a pdx lease segment is not leased by the mod', async ($, on) => {
  const r = rig(on, () => ok(GRANT()))
  const cmds = ['pdx lease run --kind build -- pnpm run build && npx vitest run', 'cd spa && pdx lease run --kind build -- ls; pnpm run build']
  for (const c of cmds) await bash($, c)
  expect(r.calls).toEqual([])
  expect(r.ran).toEqual(cmds)
})

// ---- a command Claude Code moved to the background (#2841, M-P1) ----

const MOVED = { ref: 1, result: { stdout: '', stderr: '', interrupted: false, backgroundTaskId: 'bt1', timedOutAfterMs: 120000 }, text: 'moved to the background' }
const movedAs = (id: string) => () => ({ ...MOVED, result: { ...MOVED.result, backgroundTaskId: id } })
// Claude Code lists the background tasks at every Stop; lease.js reads them in its own classic.Stop hook.
const stop = ($: any, b: { tasks: any[] }) => $.classic.Stop({ stop_hook_active: false, background_tasks: b.tasks.map((t) => ({ description: 'x', ...t })), session_crons: [] } as any)
const pause = (ms: number) => new Promise((res) => setTimeout(res, ms))
// The task is listed running at one Stop and gone at the next: it ended.
const taskEnds = async ($: any, id: string) => {
  await stop($, { tasks: [{ id, type: 'shell', status: 'running' }] })
  await stop($, { tasks: [] })
}
const releasesOf = (r: Rig) => r.calls.filter((a) => sub(a) === 'release')
const settle = async ($: any, r: Rig, n: number) => {
  for (let i = 0; i < 40 && releasesOf(r).length < n; i++) await pause(50)
}

// next(e) resolves when Claude Code moves the command to the background, with the process still running: the lease
// is not released then. (M: release when next resolves.)
test('a command moved to the background keeps its lease until the task ends', async ($, on) => {
  const r = rig(on, (a) => (sub(a) === 'acquire' ? ok(GRANT()) : ok('{}')), () => MOVED)
  const out = await bash($, 'pnpm run build')
  expect(releasesOf(r)).toEqual([])
  expect(out.context?.[out.context.length - 1]).toContain('被移到背景')
  // a Stop that still lists the task as running changes nothing
  await stop($, { tasks: [{ id: 'bt1', type: 'shell', status: 'running' }], crons: 0 })
  await pause(200)
  expect(releasesOf(r)).toEqual([])
  // the Stop that no longer lists it as running releases it, with the final notice request
  await stop($, { tasks: [{ id: 'bt1', type: 'shell', status: 'completed' }], crons: 0 })
  await settle($, r, 1)
  expect(releasesOf(r).length).toBe(1)
  expect(releasesOf(r)[0]).toEqual(['pdx', 'lease', 'release', '--client-id', arg(r.calls[0], '--client-id'), '--notice', '--json'])
  // and it is released once, however many Stops follow
  await stop($, { tasks: [], crons: 0 })
  await pause(200)
  expect(releasesOf(r).length).toBe(1)
})

// A Stop whose list was taken before the task was registered must not release a lease whose process still runs: a
// task is taken to have ended once it was seen running, or a few seconds after the hand-over. (M: release on absence.)
test('a Stop that does not list the new task yet does not release; one that saw it running and then misses it does', async ($, on) => {
  const r = rig(on, (a) => (sub(a) === 'acquire' ? ok(GRANT()) : ok('{}')), movedAs('bt5'))
  await bash($, 'pnpm run build')
  await stop($, { tasks: [] }) // taken too early
  await pause(200)
  expect(releasesOf(r)).toEqual([])
  await stop($, { tasks: [{ id: 'bt5', type: 'shell', status: 'running' }] })
  await stop($, { tasks: [] })
  await settle($, r, 1)
  expect(releasesOf(r).length).toBe(1)
})

test('a task that is gone from the list releases the lease too', async ($, on) => {
  const r = rig(on, (a) => (sub(a) === 'acquire' ? ok(GRANT()) : ok('{}')), movedAs('bt2'))
  await bash($, 'pnpm run build')
  await stop($, { tasks: [{ id: 'bt2', type: 'shell', status: 'running' }, { id: 'other', type: 'shell', status: 'running' }] })
  await stop($, { tasks: [{ id: 'other', type: 'shell', status: 'running' }] })
  await settle($, r, 1)
  expect(releasesOf(r).length).toBe(1)
})

// The final notice of that release has no tool result to ride in: it goes into the next Bash result.
test('the final notice of a background release goes into the next Bash result', async ($, on) => {
  const notice = '有其他 session 在排隊，下一個指令會是這次的最後一個；之後還要跑請重新排隊。'
  const r = rig(on, (a) => (sub(a) === 'acquire' ? ok(GRANT()) : ok(JSON.stringify({ state: 'ended', notice }))), movedAs('bt3'))
  await bash($, 'pnpm run build')
  await taskEnds($, 'bt3')
  await settle($, r, 1)
  await pause(100)
  const next = await bash($, 'ls')
  expect(next.context).toEqual([notice])
  expect((await bash($, 'ls')).context).toBeUndefined() // once
})

// The notice is about one session's command: another session's Bash, a subagent's and a deny do not get it.
// (M: hand it to whoever calls next.)
test('a background notice goes only to the session it is about, and not to a subagent', async ($, on) => {
  const notice = '有其他 session 在排隊，下一個指令會是這次的最後一個；之後還要跑請重新排隊。'
  const r = rig(on, (a) => (sub(a) === 'acquire' ? ok(GRANT()) : ok(JSON.stringify({ state: 'ended', notice }))), movedAs('bt6'))
  await bash($, 'pnpm run build') // session sess-1
  await taskEnds($, 'bt6')
  await settle($, r, 1)
  await pause(100)
  expect((await bash($, 'ls', { agentId: 'agent-7' })).context).toBeUndefined()
  r.sid.v = 'sess-2'
  expect((await bash($, 'ls')).context).toBeUndefined()
  r.sid.v = 'sess-1'
  expect((await bash($, 'ls')).context).toEqual([notice])
})

test('a background release that does not answer is tried again, then let go without a notice', async ($, on) => {
  const r = rig(on, (a) => (sub(a) === 'acquire' ? ok(GRANT()) : { deny: 'release failed' }), movedAs('bt4'))
  await bash($, 'pnpm run build')
  await taskEnds($, 'bt4')
  await settle($, r, 3)
  expect(releasesOf(r).map((a) => a.includes('--notice'))).toEqual([true, true, false])
})
