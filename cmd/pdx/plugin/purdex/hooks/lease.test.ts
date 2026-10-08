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

type Rig = { calls: string[][]; ran: string[]; sid: { v: string; mode: 'ok' | 'throw' | 'hang' } }
// rig stands the engine up: `answer` is what pdx says to each call (by subcommand), `bash` the tool beneath.
function rig(on: any, answer: (argv: string[]) => any, bash: (e: any) => any = () => BASH_OK, pdxJSON?: string): Rig {
  const r: Rig = { calls: [], ran: [], sid: { v: 'sess-1', mode: 'ok' } }
  on('session.id', () => {
    if (r.sid.mode === 'throw') throw new Error('the engine refused session.id')
    if (r.sid.mode === 'hang') return new Promise(() => {})
    return { value: r.sid.v }
  })
  on('clock.sleep', async (_$: any, e: any) => { await new Promise((res) => setTimeout(res, e.ms)); return { value: undefined } })
  on('fs.read', (_$: any, e: any) => (pdxJSON !== undefined && e.path.endsWith('/pdx.json') ? { value: pdxJSON } : { deny: 'ENOENT' }))
  on('ui.log', () => ({ value: undefined }))
  on('process.run', (_$: any, e: any) => { r.calls.push([...e.argv]); return answer(e.argv) })
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
  expect(rel).toEqual(['pdx', 'lease', 'release', '--client-id', arg(acq, '--client-id')])
})

test('the context names the rewrite, with the command that really ran', async ($, on) => {
  rig(on, (a) => (sub(a) === 'acquire' ? ok(GRANT()) : ok('{}')))
  const out = await bash($, 'npx vitest run')
  expect(out.context).toEqual(['Purdex mod 把 --maxWorkers=3 加進這個指令（主機資源規則 R7），實際執行的是：npx vitest run --maxWorkers=3'])
})

test('the context says plainly that the command waited, and overran', async ($, on) => {
  let answer = GRANT({ waited_ms: 37000, host_measured: 72 })
  rig(on, (a) => (sub(a) === 'acquire' ? ok(answer) : ok('{}')))
  expect((await bash($, 'pnpm run build')).context).toEqual(['這個指令先等了 37 秒主機資源（負載 72/100），不是卡住，不要重試'])
  answer = GRANT({ waited_ms: 300000, overrun: true })
  expect((await bash($, 'pnpm run build')).context).toEqual(['等滿 5 分鐘超量放行，已記錄'])
})

test('a wait under a second is not announced; a fail-open answer says nothing', async ($, on) => {
  let answer = GRANT({ waited_ms: 400 })
  rig(on, (a) => (sub(a) === 'acquire' ? ok(answer) : ok('{}')))
  expect((await bash($, 'pnpm run build')).context).toBeUndefined()
  answer = JSON.stringify({ granted: true, fail_open: 'daemon_unreachable' })
  expect((await bash($, 'pnpm run build')).context).toBeUndefined()
})

test('background Bash is not intercepted', async ($, on) => {
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
