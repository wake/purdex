// hooks/gate.test.ts — the part of host load control the PATH shims cannot see (plan L1a): direct calls of
// node_modules/.bin/{vitest,tsc} and vendor/bin/{phpunit,pest} get `pdx gate --auto --` in front, and a foreground call
// the gate would make wait is run in the background with a note. Run by `claude plugin test cmd/pdx/plugin/purdex`.
import { test, expect } from 'claude-code/testing'
import { gatedCandidates, prefixGated } from './gate.js'

const P = "'pdx' gate --auto -- "

const PREFIXED: [string, string][] = [
  ['./node_modules/.bin/vitest run', P + './node_modules/.bin/vitest run'],
  ['node_modules/.bin/tsc -b', P + 'node_modules/.bin/tsc -b'],
  ['cd spa && ./node_modules/.bin/vitest run', 'cd spa && ' + P + './node_modules/.bin/vitest run'],
  ['../node_modules/.bin/tsc --noEmit', P + '../node_modules/.bin/tsc --noEmit'],
  ['/repo/spa/node_modules/.bin/vitest run', P + '/repo/spa/node_modules/.bin/vitest run'],
  ['vendor/bin/phpunit --filter X', P + 'vendor/bin/phpunit --filter X'],
  ['./vendor/bin/pest', P + './vendor/bin/pest'],
  ['FOO=1 ./vendor/bin/pest', 'FOO=1 ' + P + './vendor/bin/pest'],
  ['FOO="a b" ./vendor/bin/pest', 'FOO="a b" ' + P + './vendor/bin/pest'],
  ['timeout 60 ./vendor/bin/pest 2>&1 | tail', 'timeout 60 ' + P + './vendor/bin/pest 2>&1 | tail'],
  ['env CI=1 nice -n 5 ./node_modules/.bin/vitest run', 'env CI=1 nice -n 5 ' + P + './node_modules/.bin/vitest run'],
  ['./node_modules/.bin/tsc -b; ./vendor/bin/phpunit', P + './node_modules/.bin/tsc -b; ' + P + './vendor/bin/phpunit'],
  ['(cd x && ./vendor/bin/phpunit)', '(cd x && ' + P + './vendor/bin/phpunit)'],
  ['./vendor/bin/phpunit > out.txt', P + './vendor/bin/phpunit > out.txt'],
]
for (const [cmd, want] of PREFIXED) {
  test(`prefixGated ${JSON.stringify(cmd)}`, () => {
    expect(prefixGated(cmd, P)).toEqual({ command: want, changed: true })
  })
}

const UNCHANGED = [
  'npx vitest run', // a shim catches these
  'pnpm exec vitest run',
  'vitest run',
  'go test ./...',
  'ls node_modules/.bin/vitest', // an argument, not the command
  'cat vendor/bin/pest',
  'echo "./node_modules/.bin/vitest run"',
  "echo './vendor/bin/pest'",
  'cat <<EOF\n./node_modules/.bin/tsc\nEOF',
  'echo ok # ./vendor/bin/phpunit',
  './node_modules/.bin/tsc -b &', // backgrounded by the shell itself
  './node_modules/.bin/tsc "unterminated',
  './node_modules/.bin/vitest-other run',
  'pdx gate --auto -- ./vendor/bin/pest', // already through the gate
  './vendor/bin/composer install',
  '',
]
for (const cmd of UNCHANGED) {
  test(`prefixGated leaves ${JSON.stringify(cmd)} alone`, () => {
    expect(prefixGated(cmd, P)).toEqual({ command: cmd, changed: false })
  })
}

test('gatedCandidates: the commands of a call the gate may hold, at most three, none for the unreadable', () => {
  expect(gatedCandidates('go test ./... && pnpm run build')).toEqual([['go', 'test', './...'], ['pnpm', 'run', 'build']])
  expect(gatedCandidates('cd spa && FOO=1 timeout 30 npx vitest run > o.txt')).toEqual([['npx', 'vitest', 'run']])
  expect(gatedCandidates('./vendor/bin/pest --parallel')).toEqual([['./vendor/bin/pest', '--parallel']])
  expect(gatedCandidates('ls -la | grep go')).toEqual([])
  expect(gatedCandidates('go build ./... & go vet ./...')).toEqual([])
  expect(gatedCandidates('go version; go vet; go build; go test')).toHaveLength(3)
})

// ---- the hook ----
//
// The test's `on` hooks stand beneath the whole mod as the engine: `process.run` is `pdx` (recording argv and
// answering), `tool.check` is the permission verdict, `tool.call` for Bash is the tool, recording what it ran.

const ok = (stdout: string, exitCode = 0) => ({ value: { exitCode, stdout, stderr: '', isStdoutTruncated: false, isStderrTruncated: false } })
const BASH_OK = { ref: 1, result: { stdout: 'ok', stderr: '', interrupted: false }, text: 'ok' }
const FREE = JSON.stringify({ would_wait: false, class: 'heavy', ahead: 0 })
const BUSY = JSON.stringify({ would_wait: true, reason: 'slots', class: 'heavy', ahead: 1, held_by: 'held 5m by "go test ./..." (purdex/spa)',
  note: 'waiting for a heavy slot — 1 ahead; held 5m by "go test ./..." (purdex/spa). This is a normal queue: it starts by itself when that ends; do not interrupt it or run the command another way.' })

type Rig = { calls: string[][]; ran: { command: string; bg: unknown; agent?: unknown }[]; checks: { command: string; bg: unknown }[]; verdict: (command: string, bg: unknown) => string | null }
function rig(on: any, answer: (argv: string[]) => any, bash: (e: any) => any = () => BASH_OK): Rig {
  const r: Rig = { calls: [], ran: [], checks: [], verdict: () => 'allow' }
  on('session.id', () => ({ value: 'sess-1' }))
  on('session.start', async (_$: any, e: any) => ({ cwd: e.cwd }))
  on('env.get', () => ({ value: '/bin/zsh' }))
  on('classic.Stop', async () => ({}))
  on('session.cwd', () => ({ value: '/work/purdex' }))
  on('tool.check', { tool: 'Bash' }, (_$: any, e: any) => { r.checks.push({ command: e.input.command, bg: e.input.run_in_background }); const v = r.verdict(e.input.command, e.input.run_in_background); if (v === null) throw new Error('the engine could not check'); return { decision: v } })
  on('clock.sleep', async (_$: any, e: any) => { await new Promise((res) => setTimeout(res, e.ms)); return { value: undefined } })
  on('fs.read', () => ({ deny: 'ENOENT' }))
  on('ui.log', () => ({ value: undefined }))
  on('process.run', (_$: any, e: any) => { r.calls.push([...e.argv]); return answer(e.argv) })
  on('tool.call', { tool: 'Bash' }, (_$: any, e: any) => { r.ran.push({ command: e.command, bg: e.run_in_background, agent: e.agentId }); return bash(e) })
  return r
}
const bash = ($: any, command: string, extra: object = {}) => $.tool.call({ tool: 'Bash', command, tool_use_id: 'toolu_1', ...extra })
const wouldWaits = (r: Rig) => r.calls.filter((a) => a[1] === 'gate' && a[2] === 'would-wait')

test('a direct call of a tool binary runs behind pdx gate, with a note naming what ran', async ($, on) => {
  const r = rig(on, () => ok(FREE))
  const out = await bash($, 'cd spa && ./node_modules/.bin/vitest run')
  expect(r.ran.map((x) => x.command)).toEqual(["cd spa && 'pdx' gate --auto -- ./node_modules/.bin/vitest run"])
  expect(out.context).toEqual(["Purdex mod 把直接呼叫 node_modules/.bin 或 vendor/bin 的指令交給 pdx gate（主機負載控制），實際執行的是：cd spa && 'pdx' gate --auto -- ./node_modules/.bin/vitest run"])
})

test('the prefix is not made when it would change the permission verdict', async ($, on) => {
  const r = rig(on, () => ok(FREE))
  r.verdict = (command) => (command.includes("'pdx' gate") ? 'ask' : 'allow')
  const out = await bash($, './vendor/bin/phpunit')
  expect(r.ran.map((x) => x.command)).toEqual(['./vendor/bin/phpunit'])
  expect(out.context).toBeUndefined()
})

test('the prefix is not made when the permission check gives no verdict', async ($, on) => {
  const r = rig(on, () => ok(FREE))
  r.verdict = () => null
  await bash($, './vendor/bin/phpunit')
  expect(r.ran.map((x) => x.command)).toEqual(['./vendor/bin/phpunit'])
})

test('a call that would wait runs in the background, with the gate\'s own words', async ($, on) => {
  const r = rig(on, () => ok(BUSY))
  const out = await bash($, 'go test ./...')
  expect(r.ran).toHaveLength(1)
  expect(r.ran[0].bg).toBe(true)
  expect(out.context).toHaveLength(1)
  expect(out.context[0]).toContain('改成背景執行')
  expect(out.context[0]).toContain('held 5m by "go test ./..." (purdex/spa)')
  expect(out.context[0]).toContain('不要重跑')
  // pdx was asked once, for the words of the command, not for the whole shell line
  expect(wouldWaits(r)).toEqual([['pdx', 'gate', 'would-wait', '--auto', '--json', '--', 'go', 'test', './...']])
})

test('a call that can start at once stays in the foreground and untouched', async ($, on) => {
  const r = rig(on, () => ok(FREE))
  const out = await bash($, 'go test ./...')
  expect(r.ran).toEqual([{ command: 'go test ./...', bg: undefined, agent: undefined }])
  expect(out.context).toBeUndefined()
})

test('a subagent\'s call is treated the same', async ($, on) => {
  const r = rig(on, () => ok(BUSY))
  await bash($, 'pnpm run build', { agentId: 'agent-7' })
  expect(r.ran[0].bg).toBe(true)
  expect(r.ran[0].agent).toBe('agent-7')
})

test('a call that is in the background already is only prefixed, never asked about', async ($, on) => {
  const r = rig(on, () => ok(BUSY))
  await bash($, './node_modules/.bin/tsc -b', { run_in_background: true })
  expect(wouldWaits(r)).toEqual([])
  expect(r.ran.map((x) => x.command)).toEqual(["'pdx' gate --auto -- ./node_modules/.bin/tsc -b"])
  expect(r.ran[0].bg).toBe(true)
})

test('a command that is not the gate\'s is not asked about', async ($, on) => {
  const r = rig(on, () => ok(BUSY))
  await bash($, 'ls -la | grep x')
  await bash($, 'git status')
  expect(r.calls).toEqual([])
  expect(r.ran.map((x) => x.bg)).toEqual([undefined, undefined])
})

test('what cannot be read, or backgrounds itself, is left alone', async ($, on) => {
  const r = rig(on, () => ok(BUSY))
  await bash($, 'go test ./... &')
  await bash($, 'go test "./...')
  expect(r.calls).toEqual([])
  expect(r.ran.map((x) => x.bg)).toEqual([undefined, undefined])
})

test('the first command that would wait decides; a command that does not is skipped', async ($, on) => {
  const r = rig(on, (a) => ok(a.includes('go') ? FREE : BUSY))
  await bash($, 'go build ./... && pnpm run build')
  expect(wouldWaits(r)).toHaveLength(2)
  expect(r.ran[0].bg).toBe(true)
})

const BAD_ANSWERS: [string, any][] = [['a failing exit', ok('', 20)], ['junk', ok('not json')], ['a would_wait that is not a boolean', ok('{"would_wait":"yes"}')], ['a spawn that fails', { deny: 'spawn failed' }]]
for (const [name, answer] of BAD_ANSWERS) {
  test(`pdx answers badly (${name}) → the call runs as it was`, async ($, on) => {
    const r = rig(on, () => answer)
    const out = await bash($, 'go test ./...')
    expect(r.ran[0].bg).toBeUndefined()
    expect(r.ran[0].command).toBe('go test ./...')
    expect(out.context).toBeUndefined()
  })
}

test('the background is not used when it would change the permission verdict', async ($, on) => {
  const r = rig(on, () => ok(BUSY))
  r.verdict = (_command, bg) => (bg === true ? 'ask' : 'allow')
  const out = await bash($, 'go test ./...')
  expect(r.ran[0].bg).toBeUndefined()
  expect(out.context).toBeUndefined()
})

test('both together: prefixed and moved to the background, one run', async ($, on) => {
  const r = rig(on, () => ok(BUSY))
  const out = await bash($, './node_modules/.bin/vitest run')
  expect(r.ran).toHaveLength(1)
  expect(r.ran[0].command).toBe("'pdx' gate --auto -- ./node_modules/.bin/vitest run")
  expect(r.ran[0].bg).toBe(true)
  expect(out.context).toHaveLength(2)
})

test('a denial from beneath (a queued lease) gets no note and the tool is not run again', async ($, on) => {
  const r = rig(on, () => ok(FREE), () => ({ deny: 'queued' }))
  const out = await bash($, './vendor/bin/pest')
  expect(out.deny).toBe('queued')
  expect(out.context).toBeUndefined()
  expect(r.ran).toHaveLength(1)
})

test('a tool that throws is run once and the error goes on', async ($, on) => {
  const r = rig(on, () => ok(BUSY), () => { throw new Error('the tool failed') })
  await bash($, 'go test ./...').catch(() => {})
  expect(r.ran).toHaveLength(1)
})

// ---- the order with the lease hook, which is retired in L1b ----
//
// gate.js is registered before lease.js, so it sees the call first. A call it moves to the background reaches the lease
// hook as a background call: in an interactive session that is wrapped in `pdx lease run --ask` (the lease alias of the
// gate), and the command inside is the one gate.js made. This test pins that order; L1b removes the lease half.

const startInteractive = ($: any) => $.session.start({ cwd: '/work', surface: 'terminal', isInteractive: true })

test('pinned order: gate.js moves the call to the background, then the lease hook wraps it', async ($, on) => {
  const r = rig(on, (a) => (a[1] === 'gate' ? ok(BUSY) : ok('{}')))
  await startInteractive($)
  await bash($, 'go test ./...')
  expect(r.calls[0].slice(1, 3)).toEqual(['gate', 'would-wait']) // gate.js asked first
  expect(r.ran).toHaveLength(1)
  expect(r.ran[0].bg).toBe(true)
  expect(r.ran[0].command).toMatch(/^'pdx' lease run --ask --kind test-full /) // …and the lease hook wrapped what it handed on
  expect(r.ran[0].command).toContain('go test ./...')
})

test('pinned order: a direct .bin call is prefixed by gate.js, which the lease hook then recognises as already gated', async ($, on) => {
  const r = rig(on, (a) => (a[1] === 'gate' ? ok(BUSY) : ok('{}')))
  await startInteractive($)
  await bash($, './node_modules/.bin/vitest run')
  expect(r.ran).toHaveLength(1)
  expect(r.ran[0].bg).toBe(true)
  expect(r.ran[0].command).toBe("'pdx' gate --auto -- ./node_modules/.bin/vitest run") // not wrapped a second time
  expect(r.calls.filter((a) => a[1] === 'lease')).toEqual([])
})

test('pinned order: a call that can start at once is not touched by gate.js and the lease hook decides alone', async ($, on) => {
  const r = rig(on, (a) => (a[1] === 'gate' ? ok(FREE) : a[2] === 'acquire' ? ok(JSON.stringify({ id: 'lease-1', granted: true })) : ok('{}')))
  await bash($, 'go test ./...')
  expect(r.calls.map((a) => a[1] + ' ' + a[2])).toEqual(['gate would-wait', 'lease acquire', 'lease release'])
  expect(r.ran[0].bg).toBeUndefined()
})
