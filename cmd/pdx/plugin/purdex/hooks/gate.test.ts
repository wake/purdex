// hooks/gate.test.ts — the part of host load control the PATH shims cannot see (plan L1a): direct calls of
// node_modules/.bin/{vitest,tsc} and vendor/bin/{phpunit,pest} get `pdx gate --auto --` in front, and a foreground call
// the gate would make wait is run in the background with a note. Run by `claude plugin test cmd/pdx/plugin/purdex`.
import { test, expect } from 'claude-code/testing'
import { askWouldWaitAll, gatedCandidates, prefixGated } from './gate.js'

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
  ['claude plugin test /tmp/x', P + 'claude plugin test /tmp/x'],
  ['cd /tmp/x && claude plugin test . --file hooks/gate.test.ts', 'cd /tmp/x && ' + P + 'claude plugin test . --file hooks/gate.test.ts'],
  ['FOO=1 /usr/local/bin/claude plugin test dir', 'FOO=1 ' + P + '/usr/local/bin/claude plugin test dir'],
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
  'claude plugin list', // only the test run is held
  'claude mcp test x', // `test` is not claude plugin test
  'claude test dir',
  'foo plugin test dir', // only claude's test run is held
  'pnpm plugin test x',
  'claude plugin',
  'claude -p "plugin test"',
  'claude plugin tests',
  'echo claude plugin test', // an argument
  'claude plugin test x &', // backgrounded by the shell itself
  '',
]
for (const cmd of UNCHANGED) {
  test(`prefixGated leaves ${JSON.stringify(cmd)} alone`, () => {
    expect(prefixGated(cmd, P)).toEqual({ command: cmd, changed: false })
  })
}

test('gatedCandidates: claude plugin test is asked about, other claude commands are not', () => {
  expect(gatedCandidates('cd x && claude plugin test . --file a.test.ts')).toEqual([['claude', 'plugin', 'test', '.', '--file', 'a.test.ts']])
  expect(gatedCandidates('claude plugin list')).toEqual([])
})

test('gatedCandidates: the commands of a call the gate may hold, distinct ones, at most six, none for the unreadable', () => {
  expect(gatedCandidates('go test ./... && pnpm run build')).toEqual([['go', 'test', './...'], ['pnpm', 'run', 'build']])
  expect(gatedCandidates('cd spa && FOO=1 timeout 30 npx vitest run > o.txt')).toEqual([['npx', 'vitest', 'run']])
  expect(gatedCandidates('./vendor/bin/pest --parallel')).toEqual([['./vendor/bin/pest', '--parallel']])
  expect(gatedCandidates('ls -la | grep go')).toEqual([])
  expect(gatedCandidates('go build ./... & go vet ./...')).toEqual([])
  expect(gatedCandidates('go version; go vet; go build; go test')).toHaveLength(4)
  expect(gatedCandidates('go version; go version; go version; go test ./...')).toEqual([['go', 'version'], ['go', 'test', './...']])
  expect(gatedCandidates('go a; go b; go c; go d; go e; go f; go g; go h')).toHaveLength(6)
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

type Rig = { oldPdx: boolean; batchAnswer?: (argv: string[]) => any; calls: string[][]; ran: { command: string; bg: unknown; agent?: unknown }[]; checks: { command: string; bg: unknown }[]; verdict: (command: string, bg: unknown) => string | null }
function rig(on: any, answer: (argv: string[]) => any, bash: (e: any) => any = () => BASH_OK, pdxJSON?: string, hang?: string): Rig {
  const r: Rig = { oldPdx: false, calls: [], ran: [], checks: [], verdict: () => 'allow' }
  const never = () => new Promise(() => {}) // a dependency that never answers
  on('session.id', () => ({ value: 'sess-1' }))
  on('session.start', async (_$: any, e: any) => ({ cwd: e.cwd }))
  on('env.get', () => ({ value: '/bin/zsh' }))
  on('classic.Stop', async () => ({}))
  on('session.cwd', () => ({ value: '/work/purdex' }))
  on('tool.check', { tool: 'Bash' }, (_$: any, e: any) => {
    if (hang === 'tool.check') return never(); r.checks.push({ command: e.input.command, bg: e.input.run_in_background }); const v = r.verdict(e.input.command, e.input.run_in_background); if (v === null) throw new Error('the engine could not check'); return { decision: v } })
  on('clock.sleep', async (_$: any, e: any) => { await new Promise((res) => setTimeout(res, e.ms)); return { value: undefined } })
  on('fs.read', (_$: any, e: any) => (hang === 'fs.read' ? never() : pdxJSON !== undefined && e.path.endsWith('/pdx.json') ? { value: pdxJSON } : { deny: 'ENOENT' }))
  on('ui.log', () => ({ value: undefined }))
  on('process.run', (_$: any, e: any) => {
    r.calls.push([...e.argv])
    if (hang === 'process.run') return never()
    const bi = e.argv.indexOf('--auto-batch')
    if (bi < 0) return answer(e.argv)
    // pdx's own batch form: the answer of each command is what asking it alone gives; a pdx older than the mod does not know the flag
    if (r.oldPdx) return ok('', 2)
    if (r.batchAnswer) return r.batchAnswer(e.argv)
    const rest = e.argv.slice(bi + 2).filter((x: string) => x !== '--json')
    const outs = JSON.parse(e.argv[bi + 1]).map((a: string[]) => JSON.parse(answer([...e.argv.slice(0, 3), '--auto', '--json', ...rest, '--', ...a]).value.stdout))
    return ok(JSON.stringify(outs))
  })
  on('tool.call', { tool: 'Bash' }, (_$: any, e: any) => { r.ran.push({ command: e.command, bg: e.run_in_background, agent: e.agentId }); return bash(e) })
  return r
}
const bash = ($: any, command: string, extra: object = {}) => $.tool.call({ tool: 'Bash', command, tool_use_id: 'toolu_1', ...extra })
const isAsk = (a: string[]) => a[1] === 'gate' && a[2] === 'would-wait'
// the commands the hook asked about, each as the one-command call that would have asked it (a batch call stands for its commands)
const wouldWaits = (r: Rig) => r.calls.filter(isAsk).flatMap((a) => {
  const bi = a.indexOf('--auto-batch')
  if (bi < 0) return [a]
  const rest = a.slice(bi + 2).filter((x) => x !== '--json')
  return JSON.parse(a[bi + 1]).map((c: string[]) => [...a.slice(0, 3), '--auto', '--json', ...rest, '--', ...c])
})
const askCalls = (r: Rig) => r.calls.filter(isAsk)

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
  expect(out.context[0]).toContain('整段')
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

test('a denial from beneath gets no note and the tool is not run again', async ($, on) => {
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

test('pdx.json names the binary and the config: the question and the prefix both carry them', async ($, on) => {
  const r = rig(on, () => ok(BUSY), () => BASH_OK, JSON.stringify({ pdx: '/opt/pdx/bin/pdx', config: '/home/me/.config/pdx2/config.toml' }))
  const out = await bash($, './vendor/bin/pest')
  expect(wouldWaits(r)).toEqual([['/opt/pdx/bin/pdx', 'gate', 'would-wait', '--auto', '--json', '--config', '/home/me/.config/pdx2/config.toml', '--', './vendor/bin/pest']])
  expect(r.ran[0].command).toBe("'/opt/pdx/bin/pdx' gate --config '/home/me/.config/pdx2/config.toml' --auto -- ./vendor/bin/pest")
  expect(out.context).toHaveLength(2)
})

test('a compound call goes to the background as a whole, and the note says so', async ($, on) => {
  const r = rig(on, () => ok(BUSY))
  const out = await bash($, 'cd spa && go test ./... && cat result.json')
  expect(r.ran).toHaveLength(1)
  expect(r.ran[0].command).toBe('cd spa && go test ./... && cat result.json') // unchanged: it all runs, in order, in the background
  expect(r.ran[0].bg).toBe(true)
  expect(out.context[0]).toContain('整段，包括它前後接的指令')
  expect(out.context[0]).toContain('不要假設它做完了')
})

// A dependency that never answers must not hold the call: the planning has one deadline and the call then runs as it is.
for (const [name, hang] of [['fs.read', 'fs.read'], ['process.run', 'process.run'], ['tool.check', 'tool.check']] as const) {
  test(`${name} never answers → the call runs as it was, within the planning deadline`, { timeoutMs: 15000 }, async ($, on) => {
    const r = rig(on, () => ok(BUSY), () => BASH_OK, undefined, hang)
    const began = Date.now()
    const out = await bash($, './vendor/bin/pest')
    expect(Date.now() - began).toBeLessThan(9000)
    expect(out.result).toEqual(BASH_OK.result)
    expect(r.ran).toHaveLength(1)
  })
}

test('the fourth command of a call is asked about too: it is the one that waits', async ($, on) => {
  const r = rig(on, (a) => ok(a.includes('test') ? BUSY : FREE))
  await bash($, 'go version; go env; go list ./...; go test ./...')
  expect(wouldWaits(r)).toHaveLength(4)
  expect(r.ran[0].bg).toBe(true)
})

// ---- one pdx process for all the commands of a call (G-D1c) ----

test('a call with several commands is asked about in ONE pdx process, and the answers are the same as asking one by one', async ($, on) => {
  const r = rig(on, (a) => ok(a.includes('pnpm') ? BUSY : FREE))
  await bash($, 'go build ./... && pnpm run build && go vet ./...')
  expect(askCalls(r)).toHaveLength(1)
  expect(askCalls(r)[0].slice(0, 4)).toEqual(['pdx', 'gate', 'would-wait', '--auto-batch'])
  expect(JSON.parse(askCalls(r)[0][4])).toEqual([['go', 'build', './...'], ['pnpm', 'run', 'build'], ['go', 'vet', './...']])
  expect(r.ran[0].bg).toBe(true) // pnpm would wait: the middle one decides, as when asked alone
  expect(wouldWaits(r)).toHaveLength(3)
})

test('one command is asked in the plain form, as before', async ($, on) => {
  const r = rig(on, () => ok(FREE))
  await bash($, 'go test ./...')
  expect(askCalls(r)).toEqual([['pdx', 'gate', 'would-wait', '--auto', '--json', '--', 'go', 'test', './...']])
})

test('the batch carries the config of the daemon that installed the mod', async ($, on) => {
  const r = rig(on, () => ok(FREE), () => BASH_OK, JSON.stringify({ pdx: '/opt/pdx/bin/pdx', config: '/home/me/.config/pdx2/config.toml' }))
  await bash($, 'go build ./... && go vet ./...')
  expect(askCalls(r)).toHaveLength(1)
  expect(askCalls(r)[0][0]).toBe('/opt/pdx/bin/pdx')
  expect(askCalls(r)[0].slice(-2)).toEqual(['--config', '/home/me/.config/pdx2/config.toml'])
})

test('a pdx that does not know the batch form is asked one by one: the same answers, at the old cost', async ($, on) => {
  const r = rig(on, (a) => ok(a.includes('pnpm') ? BUSY : FREE))
  r.oldPdx = true
  await bash($, 'go build ./... && pnpm run build')
  expect(askCalls(r)).toHaveLength(3) // the batch that failed, then each command
  expect(r.ran[0].bg).toBe(true)
})

for (const bad of ['not json', '{"would_wait":true}', '[{"would_wait":true}]']) {
  test(`an answer that cannot be read as a list of the right length is asked again one by one: ${bad}`, async ($, on) => {
    const r = rig(on, (a) => ok(a.includes('pnpm') ? BUSY : FREE))
    r.batchAnswer = () => ok(bad)
    await bash($, 'go build ./... && pnpm run build')
    expect(askCalls(r)).toHaveLength(3)
    expect(r.ran[0].bg).toBe(true)
  })
}

test('the list answer is checked entry by entry: an entry that is not an answer counts as no answer', async ($, on) => {
  const r = rig(on, () => ok(BUSY))
  r.batchAnswer = () => ok(JSON.stringify([{ would_wait: 'yes' }, null]))
  await bash($, 'go build ./... && pnpm run build')
  expect(askCalls(r)).toHaveLength(1)
  expect(r.ran[0].bg).toBeUndefined() // neither entry says it would wait
})

test('askWouldWaitAll: nothing to ask is no process', async ($, on) => {
  const r = rig(on, () => ok(BUSY))
  expect(await askWouldWaitAll($, { bin: 'pdx', config: '' }, [])).toEqual([])
  expect(r.calls).toEqual([])
})

// ---- the wwd entry (spec §8.2): behind the "entry" switch of pdx.json, off by default ----
//
// The rules are pdx's (`pdx entry match`); the hook only decides where the answer goes in the call.

const ENTRY_ON = JSON.stringify({ entry: 'on' })
const isEntry = (a: string[]) => a[1] === 'entry'
const entryCalls = (r: Rig) => r.calls.filter(isEntry)
// answers `pdx entry match` from a table keyed by the command's words (after the --), anything else as the gate would
const entryAnswer = (table: Record<string, object>, rest: string = FREE) => (a: string[]) =>
  isEntry(a) ? ok(JSON.stringify(table[a.slice(a.indexOf('--') + 1).join(' ')] ?? { action: 'none' })) : ok(rest)
const REWRITE_FILE = { action: 'rewrite', task: 'spa-file', args: ['src/a.test.ts'] }

for (const [name, pj] of [['no key', JSON.stringify({ pdx: 'pdx' })], ['an empty string', JSON.stringify({ entry: '' })], ['a number', JSON.stringify({ entry: 7 })],
  ['null', JSON.stringify({ entry: null })], ['no pdx.json at all', undefined]] as const) {
  test(`the switch off (${name}): pdx entry is never asked and the call goes the old way, whatever entry would say`, async ($, on) => {
    const r = rig(on, entryAnswer({ 'go test ./...': { action: 'deny', hint: 'go-pkg' } }), () => BASH_OK, pj)
    const out = await bash($, 'go test ./...')
    expect(entryCalls(r)).toEqual([])
    expect(r.ran.map((x) => x.command)).toEqual(['go test ./...'])
    expect(out.deny).toBeUndefined()
  })
}

test('a match is rewritten to pdx test, and the note names it', async ($, on) => {
  const r = rig(on, entryAnswer({ 'pnpm vitest run src/a.test.ts': REWRITE_FILE }), () => BASH_OK, ENTRY_ON)
  const out = await bash($, 'pnpm vitest run src/a.test.ts')
  expect(r.ran.map((x) => x.command)).toEqual(["'pdx' test spa-file src/a.test.ts"])
  expect(out.context.join('\n')).toContain('spa-file')
  expect(entryCalls(r)).toEqual([['pdx', 'entry', 'match', '--json', '--', 'pnpm', 'vitest', 'run', 'src/a.test.ts']])
})

test('only the matching command of a compound call is replaced, and cd tells pdx the directory', async ($, on) => {
  const r = rig(on, entryAnswer({ 'npx vitest run src/a.test.ts': REWRITE_FILE }), () => BASH_OK, ENTRY_ON)
  await bash($, 'cd spa && FOO=1 npx vitest run src/a.test.ts 2>&1 | tail -5 && echo done')
  expect(r.ran[0].command).toBe("cd spa && FOO=1 'pdx' test spa-file src/a.test.ts 2>&1 | tail -5 && echo done")
  expect(entryCalls(r)[0]).toEqual(['pdx', 'entry', 'match', '--json', '--cwd', 'spa', '--', 'npx', 'vitest', 'run', 'src/a.test.ts'])
})

test('cd words add up to the directory pdx is told', async ($, on) => {
  const r = rig(on, entryAnswer({ 'make lint': { action: 'rewrite', task: 'lint' } }), () => BASH_OK, ENTRY_ON)
  await bash($, 'cd /repo/x && cd sub && make lint')
  expect(entryCalls(r)[0].slice(4, 6)).toEqual(['--cwd', '/repo/x/sub'])
  expect(r.ran[0].command).toBe("cd /repo/x && cd sub && 'pdx' test lint")
})

test('arguments are quoted, and a dash first gets --', async ($, on) => {
  const r = rig(on, entryAnswer({ 'go test x': { action: 'rewrite', task: 'go-pkg', args: ['./a b/', '-x', "it's"] } }), () => BASH_OK, ENTRY_ON)
  await bash($, 'go test x')
  expect(r.ran[0].command).toBe("'pdx' test go-pkg -- './a b/' -x 'it'\\''s'")
})

test('a queued rewrite runs in the background', async ($, on) => {
  const r = rig(on, entryAnswer({ 'go test ./...': { action: 'rewrite', task: 'go-pkg', args: ['./...'], queued: true } }), () => BASH_OK, ENTRY_ON)
  const out = await bash($, 'go test ./...')
  expect(r.ran[0].bg).toBe(true)
  expect(out.context.join('\n')).toContain('背景')
})

test('a local rewrite stays in the foreground, and the rewritten command is not asked about as a gate candidate', async ($, on) => {
  const r = rig(on, entryAnswer({ 'pnpm vitest run a.test.ts': { action: 'rewrite', task: 'spa-file', args: ['a.test.ts'] } }, FREE), () => BASH_OK, ENTRY_ON)
  await bash($, 'pnpm vitest run a.test.ts')
  expect(r.ran[0].bg).toBeUndefined()
  expect(wouldWaits(r)).toEqual([])
})

test('a queued rewrite in the background already is not moved again', async ($, on) => {
  const r = rig(on, entryAnswer({ 'go test ./...': { action: 'rewrite', task: 'go-pkg', args: ['./...'], queued: true } }), () => BASH_OK, ENTRY_ON)
  await bash($, 'go test ./...', { run_in_background: true })
  expect(r.ran[0].bg).toBe(true)
})

test('advise: the call goes the old way (prefix, would-wait) and the note names the task to use', async ($, on) => {
  const r = rig(on, entryAnswer({ 'go test ./...': { action: 'advise', mode: 'advise', hint: 'go-pkg' } }, BUSY), () => BASH_OK, ENTRY_ON)
  const out = await bash($, 'go test ./...')
  expect(r.ran[0].command).toBe('go test ./...')
  expect(r.ran[0].bg).toBe(true) // still held by the gate's queue: it would wait
  expect(out.context.join('\n')).toContain('pdx test go-pkg')
  expect(wouldWaits(r)).toHaveLength(1)
})

test('deny: the call is refused with the task to use, and nothing runs', async ($, on) => {
  const r = rig(on, entryAnswer({ 'go test ./...': { action: 'deny', mode: 'deny', hint: 'go-pkg' } }), () => BASH_OK, ENTRY_ON)
  const out = await bash($, 'ls && go test ./...')
  expect(out.deny).toContain('pdx test go-pkg')
  expect(r.ran).toEqual([])
})

test('a deny anywhere in the call refuses all of it, also when another command in it matched', async ($, on) => {
  const r = rig(on, entryAnswer({ 'make lint': { action: 'rewrite', task: 'lint' }, 'go vet ./...': { action: 'deny', hint: 'go-pkg' } }), () => BASH_OK, ENTRY_ON)
  const out = await bash($, 'make lint && go vet ./...')
  expect(out.deny).toBeDefined()
  expect(r.ran).toEqual([])
})

const BAD_ENTRY: [string, any][] = [['a failing exit', ok('', 20)], ['junk', ok('not json')], ['an unknown action', ok('{"action":"explode"}')], ['a spawn that fails', { deny: 'spawn failed' }],
  ['a rewrite without a task', ok('{"action":"rewrite"}')], ['args that are not strings', ok('{"action":"rewrite","task":"t","args":[1]}')]]
for (const [name, bad] of BAD_ENTRY) {
  test(`pdx entry answers badly (${name}) → the call goes the old way`, async ($, on) => {
    const r = rig(on, (a) => (isEntry(a) ? bad : ok(FREE)), () => BASH_OK, ENTRY_ON)
    const out = await bash($, 'go test ./...')
    expect(r.ran[0].command).toBe('go test ./...')
    expect(out.deny).toBeUndefined()
  })
}

test('a failing pdx whose stdout still reads as a rewrite is not believed', async ($, on) => {
  const r = rig(on, (a) => (isEntry(a) ? ok(JSON.stringify(REWRITE_FILE), 3) : ok(FREE)), () => BASH_OK, ENTRY_ON)
  await bash($, 'go test ./...')
  expect(r.ran[0].command).toBe('go test ./...')
})

test('two rewrites in one call are both made, each where its command stood', async ($, on) => {
  const r = rig(on, entryAnswer({ 'make lint': { action: 'rewrite', task: 'lint' }, 'go test ./...': { action: 'rewrite', task: 'go-pkg', args: ['./...'] } }), () => BASH_OK, ENTRY_ON)
  await bash($, 'make lint && echo between && go test ./... | tail -3')
  expect(r.ran[0].command).toBe("'pdx' test lint && echo between && 'pdx' test go-pkg ./... | tail -3")
})

test('a queued rewrite stays in the foreground when the background would change the permission verdict', async ($, on) => {
  const r = rig(on, entryAnswer({ 'go test ./...': { action: 'rewrite', task: 'go-pkg', args: ['./...'], queued: true } }), () => BASH_OK, ENTRY_ON)
  r.verdict = (_command, bg) => (bg === true ? 'ask' : 'allow')
  await bash($, 'go test ./...')
  expect(r.ran[0].command).toBe("'pdx' test go-pkg ./...")
  expect(r.ran[0].bg).toBeUndefined()
})

test('a rewrite and a direct tool binary in one call: both are made', async ($, on) => {
  const r = rig(on, entryAnswer({ 'npx vitest run src/a.test.ts': REWRITE_FILE }), () => BASH_OK, ENTRY_ON)
  await bash($, 'npx vitest run src/a.test.ts && ./vendor/bin/pest')
  expect(r.ran[0].command).toBe("'pdx' test spa-file src/a.test.ts && 'pdx' gate --auto -- ./vendor/bin/pest")
})

// Bash removes a backslash and a newline before it splits words: `go \<newline> test` is `go test`, `te\<newline>st` is `test`.
for (const cmd of ['go \\\n test ./...', 'go \\\ntest ./...', 'g\\\no test ./...', 'go test \\\n  ./...']) {
  test(`a line continuation does not hide the command: ${JSON.stringify(cmd)}`, async ($, on) => {
    const r = rig(on, entryAnswer({ 'go test ./...': { action: 'rewrite', task: 'go-pkg', args: ['./...'] } }), () => BASH_OK, ENTRY_ON)
    await bash($, cmd)
    expect(r.ran[0].command).toBe("'pdx' test go-pkg ./...")
  })
}

test('a line continuation does not hide a command from deny either', async ($, on) => {
  const r = rig(on, entryAnswer({ 'go test ./...': { action: 'deny', hint: 'go-pkg' } }), () => BASH_OK, ENTRY_ON)
  const out = await bash($, 'go \\\n test ./...')
  expect(out.deny).toContain('pdx test go-pkg')
  expect(r.ran).toEqual([])
})

test('with the switch off a line continuation is read as it always was: nothing is asked', async ($, on) => {
  const r = rig(on, entryAnswer({ 'go test ./...': { action: 'deny', hint: 'go-pkg' } }), () => BASH_OK, JSON.stringify({ pdx: 'pdx' }))
  await bash($, 'go \\\n test ./...')
  expect(entryCalls(r)).toEqual([])
  expect(r.ran[0].command).toBe('go \\\n test ./...')
})

test('a continuation inside double quotes joins the word too', async ($, on) => {
  const r = rig(on, entryAnswer({ 'go test ./...': { action: 'rewrite', task: 'go-pkg', args: ['./...'] } }), () => BASH_OK, ENTRY_ON)
  await bash($, 'go "te\\\nst" ./...')
  expect(r.ran[0].command).toBe("'pdx' test go-pkg ./...")
})

test('with the switch off the older steps still read a backslash-newline as they always did', async ($, on) => {
  const r = rig(on, () => ok(FREE), () => BASH_OK, JSON.stringify({ pdx: 'pdx' }))
  await bash($, 'go \\\n test ./...')
  expect(wouldWaits(r)).toEqual([['pdx', 'gate', 'would-wait', '--auto', '--json', '--', 'go', '\n', 'test', './...']])
})

test('the final command and its background are checked as one: a queued rewrite with a direct tool binary falls back whole when the final form would change the verdict', async ($, on) => {
  const r = rig(on, entryAnswer({ 'go test ./...': { action: 'rewrite', task: 'go-pkg', args: ['./...'], queued: true } }), () => BASH_OK, ENTRY_ON)
  r.verdict = (command, bg) => (command.includes('gate --auto') && bg === true ? 'ask' : 'allow')
  await bash($, 'go test ./... && ./vendor/bin/pest')
  expect(r.ran[0].command).toBe('go test ./... && ./vendor/bin/pest')
  expect(r.ran[0].bg).toBeUndefined()
})

test('a manifest that does not read is said, and the call goes on', async ($, on) => {
  const r = rig(on, entryAnswer({ 'go test ./...': { action: 'none', broken: true, reason: 'manifest x: bad' } }), () => BASH_OK, ENTRY_ON)
  const out = await bash($, 'go test ./...')
  expect(r.ran[0].command).toBe('go test ./...')
  expect(out.context.join('\n')).toContain('manifest x: bad')
})

test('a command the entry leaves alone is not noted, and not-tool commands are not even asked', async ($, on) => {
  const r = rig(on, entryAnswer({}), () => BASH_OK, ENTRY_ON)
  const out = await bash($, 'git status && ls')
  expect(entryCalls(r)).toEqual([])
  expect(out.context).toBeUndefined()
  await bash($, 'go version')
  expect(entryCalls(r)).toHaveLength(1) // a TOOLS word is asked; pdx says none
})

for (const cmd of ['go test > out.txt ./...', '(cd x && go test ./...)', 'cd "$DIR" && go test ./...', 'cd $(pwd) && go test ./...', 'go test ./... &']) {
  test(`what cannot be read for sure is not rewritten: ${JSON.stringify(cmd)}`, async ($, on) => {
    const r = rig(on, entryAnswer({ 'go test ./...': REWRITE_FILE }), () => BASH_OK, ENTRY_ON)
    await bash($, cmd)
    expect(r.ran[0].command).toBe(cmd)
  })
}

test('a rewrite that would change the permission verdict is not made', async ($, on) => {
  const r = rig(on, entryAnswer({ 'go test ./...': { action: 'rewrite', task: 'go-pkg', args: ['./...'] } }), () => BASH_OK, ENTRY_ON)
  r.verdict = (command) => (command.includes('test go-pkg') ? 'ask' : 'allow')
  const out = await bash($, 'go test ./...')
  expect(r.ran[0].command).toBe('go test ./...')
  expect(out.context ?? []).toEqual([])
})

test('pdx entry never answers → the call runs as it was, within the planning deadline', { timeoutMs: 15000 }, async ($, on) => {
  const r = rig(on, () => ok(FREE), () => BASH_OK, ENTRY_ON, 'process.run')
  const began = Date.now()
  const out = await bash($, 'go test ./...')
  expect(Date.now() - began).toBeLessThan(9000)
  expect(out.result).toEqual(BASH_OK.result)
  expect(r.ran).toHaveLength(1)
})

test('at most six commands are asked', async ($, on) => {
  const r = rig(on, entryAnswer({}), () => BASH_OK, ENTRY_ON)
  await bash($, 'go a; go b; go c; go d; go e; go f; go g; go h')
  expect(entryCalls(r)).toHaveLength(6)
})

test('the same command in the same directory is asked once, and its answer holds for every place it stands', async ($, on) => {
  const r = rig(on, entryAnswer({ 'go test ./...': { action: 'rewrite', task: 'go-all' } }), () => BASH_OK, ENTRY_ON)
  await bash($, 'go test ./... && echo x && go test ./...')
  expect(entryCalls(r)).toHaveLength(1)
  expect(r.ran[0].command).toBe("'pdx' test go-all && echo x && 'pdx' test go-all")
})

test('the same command in two directories is two questions', async ($, on) => {
  const r = rig(on, entryAnswer({}), () => BASH_OK, ENTRY_ON)
  await bash($, 'cd a && go test ./... && cd ../b && go test ./...')
  expect(entryCalls(r).map((c) => c[5])).toEqual(['a', 'a/../b'])
})

test('an answer for a known directory is not borrowed by the same command where the directory is unknown', async ($, on) => {
  const r = rig(on, entryAnswer({ 'go test ./...': { action: 'rewrite', task: 'go-all' } }), () => BASH_OK, ENTRY_ON)
  await bash($, 'cd a && go test ./... ; echo x ; go test ./...')
  // the second go test runs wherever the shell is if the cd failed: it is left as it is
  expect(r.ran[0].command).toBe("cd a && 'pdx' test go-all ; echo x ; go test ./...")
  expect(entryCalls(r)).toHaveLength(1)
})

test('a deny holds for the repeats too, and an advise note is said once', async ($, on) => {
  const r = rig(on, entryAnswer({ 'go test ./...': { action: 'advise', hint: 'go-pkg' } }), () => BASH_OK, ENTRY_ON)
  const out = await bash($, 'go test ./... ; go test ./...')
  expect(r.ran[0].command).toBe('go test ./... ; go test ./...')
  expect(out.context.filter((x: string) => x.includes('pdx test go-pkg'))).toHaveLength(1)
})

// ---- ad-hoc (mode rewrite, no task declares the command): `pdx test --adhoc -- <argv…>` ----

const ADHOC = { action: 'adhoc', mode: 'rewrite', hint: 'go-pkg' } // never queued: the fast lane (spec 8.3)

test('adhoc: a plain command becomes pdx test --adhoc -- argv, stays in the foreground, and the note says it is ad-hoc', async ($, on) => {
  const r = rig(on, entryAnswer({ 'go vet ./...': ADHOC }), () => BASH_OK, ENTRY_ON)
  const out = await bash($, 'go vet ./...')
  expect(r.ran[0].command).toBe("'pdx' test --adhoc -- go vet ./...")
  expect(r.ran[0].bg).toBeUndefined() // its output streams as it runs, and the caller gets the real exit status
  expect(out.context.join('\n')).toContain('[entry].mode = rewrite')
})

test('adhoc: a redirection among the words of the command is advised and not rewritten (the text is not just the command)', async ($, on) => {
  const r = rig(on, (a: string[]) => (isEntry(a) ? ok(JSON.stringify(ADHOC)) : ok(FREE)), () => BASH_OK, ENTRY_ON)
  const out = await bash($, 'go vet >/dev/null ./...')
  expect(r.ran[0].command).toBe('go vet >/dev/null ./...')
  expect(out.context.join('\n')).toContain('pdx test go-pkg')
})

test('adhoc: the words are quoted as they were typed, the environment words and a cd stay where they are', async ($, on) => {
  const r = rig(on, entryAnswer({ 'go test -run A B ./x': ADHOC }), () => BASH_OK, ENTRY_ON)
  await bash($, "cd sub && FOO=1 go test -run 'A B' ./x")
  expect(r.ran[0].command).toBe("cd sub && FOO=1 'pdx' test --adhoc -- go test -run 'A B' ./x")
  expect(entryCalls(r)[0].slice(4, 6)).toEqual(['--cwd', 'sub'])
})

for (const cmd of ['go vet $PKG', 'go vet ./internal/*', 'go vet `pwd`', 'go vet ~/x', 'go vet ./{a,b}', 'go vet ./a?', 'go vet ./[ab]', 'go vet ./a\\ b', "go vet './a*'"]) {
  test(`adhoc: ${cmd} has words the shell would expand: it is advised and not rewritten`, async ($, on) => {
    const r = rig(on, (a: string[]) => (isEntry(a) ? ok(JSON.stringify(ADHOC)) : ok(FREE)), () => BASH_OK, ENTRY_ON)
    const out = await bash($, cmd)
    expect(r.ran[0].command).toBe(cmd)
    expect(out.context.join('\n')).toContain('pdx test go-pkg')
  })
}

test('adhoc: a redirection after the command stays after it and now belongs to pdx test', async ($, on) => {
  const r = rig(on, entryAnswer({ 'go vet ./...': ADHOC }), () => BASH_OK, ENTRY_ON)
  await bash($, 'go vet ./... 2>&1 | tail -5')
  expect(r.ran[0].command).toBe("'pdx' test --adhoc -- go vet ./... 2>&1 | tail -5")
})

test('adhoc: the permission verdict would change → the call goes the old way', async ($, on) => {
  const r = rig(on, entryAnswer({ 'go vet ./...': ADHOC }), () => BASH_OK, ENTRY_ON)
  r.verdict = (command) => (command.includes('--adhoc') ? 'ask' : 'allow')
  await bash($, 'go vet ./...')
  expect(r.ran[0].command).toBe('go vet ./...')
})

test('adhoc: a task match and an ad-hoc one in one call are both rewritten', async ($, on) => {
  const r = rig(on, entryAnswer({ 'go vet ./...': ADHOC, 'go test ./...': { action: 'rewrite', task: 'go-pkg', args: ['./...'], queued: true } }), () => BASH_OK, ENTRY_ON)
  const out = await bash($, 'go vet ./... && go test ./...')
  expect(r.ran[0].command).toBe("'pdx' test --adhoc -- go vet ./... && 'pdx' test go-pkg ./...")
  expect(out.context.join('\n')).not.toContain('[entry].mode = rewrite') // a task was part of it: the plain wording
})

// A cd carries to the commands after it only along `&&`: after `;`, `||`, a pipe or a newline the command runs wherever the shell
// is when the cd did not happen, and the hook cannot tell which.
const CD_UNSURE: [string, number][] = [
  ['cd missing || go test ./...', 0],
  ['cd spa ; go test ./...', 0],
  ['cd spa\ngo test ./...', 0],
  ['cd spa && make lint ; go test ./...', 1], // make lint is asked (reached by &&); go test is not
  ['cd a && cd b || go test ./...', 0],
  ['cd spa 2>/dev/null && go test ./...', 0],
  ['ls ; cd spa && go test ./...', 1],
  ['go test ./... ; cd spa && go vet ./...', 2], // the first runs where it is; the second in spa
]
for (const [cmd, asked] of CD_UNSURE) {
  test(`cd and the commands after it: ${JSON.stringify(cmd)} asks ${asked}`, async ($, on) => {
    const r = rig(on, entryAnswer({}), () => BASH_OK, ENTRY_ON)
    await bash($, cmd)
    expect(entryCalls(r)).toHaveLength(asked)
  })
}
