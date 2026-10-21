// hooks/gate.test.ts — the wwd entry of the Bash hook (spec §8.2): behind the "entry" switch of pdx.json it asks `pdx entry match` about a
// test or build command and rewrites it to `pdx test`. The old gate's half (a `pdx gate --auto --` prefix, `pdx gate would-wait`, a call moved
// to the background for the gate's queue) went with the gate (WWD-9 9a-2). Run by `claude plugin test cmd/pdx/plugin/purdex`.
import { test, expect } from 'claude-code/testing'

// ---- the hook ----
//
// The test's `on` hooks stand beneath the whole mod as the engine: `process.run` is `pdx` (recording argv and
// answering), `tool.check` is the permission verdict, `tool.call` for Bash is the tool, recording what it ran.

const ok = (stdout: string, exitCode = 0) => ({ value: { exitCode, stdout, stderr: '', isStdoutTruncated: false, isStderrTruncated: false } })
const BASH_OK = { ref: 1, result: { stdout: 'ok', stderr: '', interrupted: false }, text: 'ok' }
const FREE = JSON.stringify({ would_wait: false, class: 'heavy', ahead: 0 })


type Rig = { calls: string[][]; ran: { command: string; bg: unknown; agent?: unknown }[]; checks: { command: string; bg: unknown }[]; verdict: (command: string, bg: unknown) => string | null }
function rig(on: any, answer: (argv: string[]) => any, bash: (e: any) => any = () => BASH_OK, pdxJSON?: string, hang?: string): Rig {
  const r: Rig = { calls: [], ran: [], checks: [], verdict: () => 'allow' }
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
    return answer(e.argv)
  })
  on('tool.call', { tool: 'Bash' }, (_$: any, e: any) => { r.ran.push({ command: e.command, bg: e.run_in_background, agent: e.agentId }); return bash(e) })
  return r
}
const bash = ($: any, command: string, extra: object = {}) => $.tool.call({ tool: 'Bash', command, tool_use_id: 'toolu_1', ...extra })

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
    expect(r.calls).toEqual([]) // pdx is not run at all: the gate's own questions (would-wait) are gone
    expect(r.ran.map((x) => x.command)).toEqual(['go test ./...'])
    expect(out.deny).toBeUndefined()
  })
}

// WWD-9 9a-2: the gate holds nothing, so the hook no longer prefixes a tool binary called by path or moves a call to the background for it.
for (const cmd of ['./node_modules/.bin/vitest run', 'cd spa && ./node_modules/.bin/tsc -b', './vendor/bin/pest --parallel', 'claude plugin test /tmp/x', 'go test ./... && pnpm run build']) {
  test(`the switch off: ${JSON.stringify(cmd)} runs as typed, in the foreground, with no note and no question`, async ($, on) => {
    const r = rig(on, () => ok(FREE), () => BASH_OK, JSON.stringify({ pdx: 'pdx' }))
    const out = await bash($, cmd)
    expect(r.ran).toEqual([{ command: cmd, bg: undefined, agent: undefined }])
    expect(r.calls).toEqual([])
    expect(out.context).toBeUndefined()
  })
}

test('the switch on: a tool binary called by path that no task matches is not prefixed either', async ($, on) => {
  const r = rig(on, entryAnswer({}), () => BASH_OK, ENTRY_ON)
  const out = await bash($, './node_modules/.bin/vitest run')
  expect(r.ran.map((x) => x.command)).toEqual(['./node_modules/.bin/vitest run'])
  expect(r.ran[0].bg).toBeUndefined()
  expect(r.calls.every(isEntry)).toBe(true)
  expect(out.context).toBeUndefined()
})

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

test('a local rewrite stays in the foreground, and pdx is asked about nothing but the entry', async ($, on) => {
  const r = rig(on, entryAnswer({ 'pnpm vitest run a.test.ts': { action: 'rewrite', task: 'spa-file', args: ['a.test.ts'] } }, FREE), () => BASH_OK, ENTRY_ON)
  await bash($, 'pnpm vitest run a.test.ts')
  expect(r.ran[0].bg).toBeUndefined()
  expect(r.calls.every(isEntry)).toBe(true)
})

test('a queued rewrite in the background already is not moved again', async ($, on) => {
  const r = rig(on, entryAnswer({ 'go test ./...': { action: 'rewrite', task: 'go-pkg', args: ['./...'], queued: true } }), () => BASH_OK, ENTRY_ON)
  await bash($, 'go test ./...', { run_in_background: true })
  expect(r.ran[0].bg).toBe(true)
})

test('advise: the call runs as typed, in the foreground, and the note names the task to use', async ($, on) => {
  const r = rig(on, entryAnswer({ 'go test ./...': { action: 'advise', mode: 'advise', hint: 'go-pkg' } }), () => BASH_OK, ENTRY_ON)
  const out = await bash($, 'go test ./...')
  expect(r.ran[0].command).toBe('go test ./...')
  expect(r.ran[0].bg).toBeUndefined()
  expect(out.context.join('\n')).toContain('pdx test go-pkg')
  expect(r.calls.every(isEntry)).toBe(true) // no other question is asked: the gate holds nothing now
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

test('a rewrite and a direct tool binary in one call: only the rewrite is made, the binary by path stays as typed', async ($, on) => {
  const r = rig(on, entryAnswer({ 'npx vitest run src/a.test.ts': REWRITE_FILE }), () => BASH_OK, ENTRY_ON)
  await bash($, 'npx vitest run src/a.test.ts && ./vendor/bin/pest')
  expect(r.ran[0].command).toBe("'pdx' test spa-file src/a.test.ts && ./vendor/bin/pest")
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

for (const cmd of ['go vet < in.txt', 'go vet <<< data', 'go vet <<EOF\nx\nEOF', 'go vet $PKG', 'go vet ./internal/*', 'go vet `pwd`', 'go vet ~/x', 'go vet ./{a,b}', 'go vet ./a?', 'go vet ./[ab]', 'go vet ./a\\ b', "go vet './a*'"]) {
  test(`adhoc: ${cmd} has words the shell would expand, or an input redirection: it is advised and not rewritten`, async ($, on) => {
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
