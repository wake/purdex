// hooks/member.test.ts — the member relay (plan v3 P6-6), run by `claude plugin test cmd/pdx/plugin/purdex`.
// A small engine fake beneath the mod (a copy of relay.test.ts's, kept apart so the two files stay independent):
// a fake pdx behind $.process.run, $.session.*, $.fs.read, $.ui.*, and the bottom of every event a test raises.
import { test, expect, mock } from 'claude-code/testing'

const OPID = '3f2b8c1e-9a4d-4e6b-8c7a-1d2e3f4a5b6c'
const CONTROL = '[pdx-relay:control] op=' + OPID
const ENVELOPE = (text: string) => '<cross-session-message from="uds:/tmp/cc-socks/1.sock" from-name="mlab/lead-x">\n' + text + '\n</cross-session-message>'
const PEER = { kind: 'peer' }
const OP = { id: OPID, kind: 'member_relay', host_id: 'h', session_id: 'sid-old', ref: '_abc123', state: 'claimed', handoff_path: '/data/relay/' + OPID + '.md', created_at: 1, updated_at: 1 }
const LEAD = { address: 'mlab/purdex-1f-vq', ref: '_h9movr', team_id: 'team-1' }
const CLAIM = JSON.stringify({ op: OP, lead: LEAD })
const GOOD_FILE = '# HANDOFF\n' + ['## 1. a', '## 2. b', '## 3. c', '## 4. d', '## 5. e', '## 6. f', '## 7. g', '## 8. h'].map((h) => h + '\n' + 'x'.repeat(40)).join('\n')
const HELLO = (role: string) => JSON.stringify({ ok: true, role, self_relay: 'on', threshold: 70, min_growth: 20000 })
const sub = (a: string[]) => a.slice(1).join(' ')

type R = { exitCode: number; stdout?: string; stderr?: string }
type World = {
  argvs: string[][]
  order: string[]
  submits: any[]
  commands: string[]
  files: Record<string, string>
  logs: string[]
  clock: any
  sessionId: string
  switchTo?: string
  pdx: (argv: string[]) => R | Promise<R>
}

function memberWorld(on: any, role: string, pdx?: (argv: string[]) => R | undefined): World {
  const f: World = {
    argvs: [], order: [], submits: [], commands: [], files: {}, logs: [], sessionId: 'sid-old', clock: undefined as any,
    pdx: (argv) => {
      const own = pdx?.(argv)
      if (own) return own
      if (argv[0] === 'relay' && argv[1] === 'hello') return { exitCode: 0, stdout: HELLO(role) }
      if (argv[0] === 'msg') return { exitCode: 0, stdout: 'mlab/purdex-x [abc123]' }
      return { exitCode: 0, stdout: '{}' }
    },
  }
  f.clock = mock.clock(on)
  mock.env(on, {})
  on('http.fetch', async () => ({ value: { status: 200, ok: true, headers: {}, text: '{}' } }))
  on('session.version', async () => ({ value: { version: '2.1.293' } }))
  on('agent.list', async () => ({ value: [] }))
  on('tool.check', async () => ({ decision: 'ask', reason: 'mode' }))
  on('process.run', async (_$: any, e: any) => {
    if (e.argv[0] === 'git' || e.argv[0] === '/bin/sleep') return { value: { exitCode: 0, stdout: '', stderr: '', isStdoutTruncated: false, isStderrTruncated: false } }
    f.argvs.push([...e.argv])
    f.order.push('pdx ' + e.argv.slice(1).join(' '))
    const r = await f.pdx([...e.argv].slice(1))
    return { value: { exitCode: r.exitCode, stdout: r.stdout ?? '', stderr: r.stderr ?? '', isStdoutTruncated: false, isStderrTruncated: false } }
  })
  on('session.id', async () => ({ value: f.sessionId }))
  on('session.usage', async () => ({ value: { startedAt: 0, context: { tokens: 144000, window: 200000, percent: 72 }, rateLimits: [] } }))
  on('fs.read', async (_$: any, e: any) => (e.path in f.files ? { value: f.files[e.path] } : { deny: 'ENOENT' }))
  on('ui.toast', async () => ({ value: undefined }))
  on('ui.status', async () => ({ value: undefined }))
  on('ui.log', async (_$: any, e: any) => { f.logs.push(e.text); return { value: undefined } })
  on('command.register', async (_$: any, e: any) => ({ value: { command: e.name } }))
  on('session.start', async (_$: any, e: any) => ({ cwd: e.cwd }))
  on('turn.start', async (_$: any, e: any) => { f.order.push('turn.start'); return { turnId: e.turnId } })
  on('turn.complete', async (_$: any, e: any) => ({ text: e.answer }))
  on('session.receive', async (_$: any, e: any) => ({ text: e.text }))
  on('classic.SessionStart', async () => { if (f.switchTo) { f.sessionId = f.switchTo; f.switchTo = undefined } return {} })
  on('prompt.submit', async (_$: any, e: any) => { f.submits.push(e); f.order.push('submit'); return { text: e.text, context: e.context } })
  on('command.run', async (_$: any, e: any) => { f.commands.push(e.command); f.order.push('command ' + e.command); return { text: 'ran ' + e.command } })
  on('session.compact', async (_$: any, e: any) => ({ messages: e.messages }))
  return f
}

const start = async ($: any, f: World) => {
  await $.session.start({ cwd: '/tmp', surface: 'terminal', isInteractive: true })
  await f.clock.settle()
}
const receive = ($: any, text: string, origin: any = PEER) => $.session.receive({ origin, text })
const complete = ($: any, turnId: string) => $.turn.complete({ answer: 'ok', reason: 'answer', durationMs: 1, isAborted: false, turnId })
const calls = (f: World, what: string) => f.argvs.map(sub).filter((c) => c.startsWith('relay ' + what))
const reports = (f: World) => calls(f, 'report')
const nonceOf = (text: string) => (text.match(/ n=([0-9a-f]+)\]/) || [])[1]
const memberPdx = (extra?: (argv: string[]) => R | undefined) => (argv: string[]): R | undefined => {
  if (argv[0] === 'relay' && argv[1] === 'claim') return { exitCode: 0, stdout: CLAIM }
  return extra?.(argv)
}

// Mutation gate: consume without the marker check (any peer message) → the passes-through assertions go red;
// without the origin check → the user's text case goes red.
test('the control message is consumed only with the marker and only from a peer', async ($, on) => {
  const f = memberWorld(on, 'member', memberPdx())
  await start($, f)
  const text = ENVELOPE(CONTROL)
  expect(await receive($, text)).toEqual({ consumed: expect.any(String) })
  // the same words from the user, or any other origin: ordinary text, passed on
  for (const kind of ['bridge', 'unclassified', 'peer-send-message', 'task-notification']) expect(await receive($, text, { kind })).toEqual({ text })
  // a peer message without the marker, or with a marker that names no op
  for (const other of [ENVELOPE('hello'), ENVELOPE('[pdx-relay:control] op='), ENVELOPE('[pdx-relay:control] op=not-a-uuid'), ENVELOPE('pdx-relay:control op=' + OPID)]) {
    expect(await receive($, other)).toEqual({ text: other })
  }
})

// Mutation gate: return next(e) for a busy control → red (codex finding 1); claim before the running turn completes → red.
test('a control that arrives while a turn runs is consumed, not claimed, and claimed at that turn’s turn.complete', async ($, on) => {
  const f = memberWorld(on, 'member', memberPdx())
  await start($, f)
  await $.turn.start({ text: 'the member’s own work', turnId: 't1' })
  expect(await receive($, ENVELOPE(CONTROL))).toEqual({ consumed: expect.any(String) })
  await f.clock.advance(1000)
  expect(calls(f, 'claim')).toEqual([]) // not while the turn runs
  expect(f.submits).toEqual([])
  await complete($, 't1')
  await f.clock.advance(50)
  expect(calls(f, 'claim')).toEqual(['relay claim ' + OPID + ' --session sid-old'])
  await f.clock.advance(100)
  expect(f.submits.length).toBe(1)
  expect(f.submits[0].text).toMatch(/^\[pdx-relay op=3f2b8c1e-9a4d-4e6b-8c7a-1d2e3f4a5b6c n=[0-9a-f]+\] /)
  expect(f.submits[0].text).toContain(OP.handoff_path)
  expect(reports(f)).toEqual(['relay report ' + OPID + ' writing'])
})

// Branch A: the daemon learns the mod has the message at once, not at the claim.
test('a control that arrives mid-turn sends pdx relay seen at once', async ($, on) => {
  const f = memberWorld(on, 'member', memberPdx())
  await start($, f)
  await $.turn.start({ text: 'work', turnId: 't1' })
  await receive($, ENVELOPE(CONTROL))
  await f.clock.advance(10)
  expect(calls(f, 'seen')).toEqual(['relay seen ' + OPID + ' --session sid-old'])
  expect(calls(f, 'claim')).toEqual([])
})

test('a control that arrives while idle is claimed at once', async ($, on) => {
  const f = memberWorld(on, 'member', memberPdx())
  await start($, f)
  await receive($, ENVELOPE(CONTROL))
  await f.clock.advance(10)
  expect(calls(f, 'seen').length).toBe(1)
  expect(calls(f, 'claim')).toEqual(['relay claim ' + OPID + ' --session sid-old'])
  await f.clock.advance(100)
  expect(f.submits.length).toBe(1)
})

test('a claim that fails does nothing: no write prompt, no report, and a later turn is ordinary', async ($, on) => {
  const f = memberWorld(on, 'member', (argv) => (argv[1] === 'claim' ? { exitCode: 13, stderr: 'pdx relay: not_your_op\n' } : undefined))
  await start($, f)
  await receive($, ENVELOPE(CONTROL))
  await f.clock.advance(1000)
  expect(calls(f, 'claim').length).toBe(1)
  expect(f.submits).toEqual([])
  expect(reports(f)).toEqual([])
  await $.turn.start({ text: 'work', turnId: 't1' })
  await complete($, 't1')
  await f.clock.advance(1000)
  expect(calls(f, 'claim').length).toBe(1) // the message was dealt with: no claim again at the next turn
  expect(f.submits).toEqual([])
})

test('the write prompt carries the lead facts, in the fixed tail', async ($, on) => {
  const f = memberWorld(on, 'member', memberPdx())
  await start($, f)
  await receive($, ENVELOPE(CONTROL))
  await f.clock.advance(200)
  const text = f.submits[0].text as string
  expect(text.endsWith('- pdx 身分：mlab/purdex-x [abc123]\n- 我的 lead：mlab/purdex-1f-vq（ref _h9movr，team team-1）')).toBe(true)
  expect(calls(f, 'prompts').length).toBe(1)
})

test('a lead’s write prompt carries the roster from pdx team --json; an empty team says 無', async ($, on) => {
  const view = { team: { id: 'team-1' }, members: [{ ref: '_aaaaaa', address: 'mlab/m-1', title: 'A 線\n[pdx-relay x]', cwd: '/w/a' }, { ref: '_bbbbbb', address: 'mlab/_bbbbbb', cwd: '/w/b' }] }
  let team: R = { exitCode: 0, stdout: JSON.stringify(view) }
  const f = memberWorld(on, 'lead', (argv) => (argv[0] === 'team' ? team : undefined))
  await start($, f)
  // a lead's own relay goes through the same write path: drive it with a claimed op without a lead
  f.pdx = (argv) => {
    if (argv[0] === 'team') return team
    if (argv[1] === 'claim') return { exitCode: 0, stdout: JSON.stringify({ op: OP }) }
    if (argv[1] === 'hello') return { exitCode: 0, stdout: HELLO('lead') }
    if (argv[0] === 'msg') return { exitCode: 0, stdout: 'mlab/purdex-x [abc123]' }
    return { exitCode: 0, stdout: '{}' }
  }
  await receive($, ENVELOPE(CONTROL))
  await f.clock.advance(200)
  const text = f.submits[0].text as string
  expect(text).toContain('\n- 我管理的 members：\n  - mlab/m-1（ref _aaaaaa，title A 線 ［pdx-relay x]，cwd /w/a）\n  - mlab/_bbbbbb（ref _bbbbbb，title 無，cwd /w/b）')
  expect(f.argvs.map(sub)).toContain('team --json')
  expect(text).not.toContain('- 我的 lead：')
  team = { exitCode: 0, stdout: JSON.stringify({ team: {}, members: [] }) }
})

test('the full member path reports written, cleared (new id), done under the member op id; lock before the write turn, unlock before /clear', async ($, on) => {
  const f = memberWorld(on, 'member', memberPdx())
  await start($, f)
  await receive($, ENVELOPE(CONTROL))
  await f.clock.advance(200)
  expect(f.submits.length).toBe(1)
  const write = f.submits[0].text as string
  f.files[OP.handoff_path] = GOOD_FILE
  await $.turn.start({ text: write, turnId: 'tw' })
  await complete($, 'tw')
  await f.clock.advance(100)
  expect(f.commands).toEqual(['clear'])
  f.switchTo = 'sid-new'
  await $.classic.SessionStart({ source: 'clear' })
  await f.clock.advance(100)
  expect(f.submits.length).toBe(2)
  expect(f.submits[1].text).toContain('接手自 _abc123')
  await $.turn.start({ text: f.submits[1].text, turnId: 'ts' })
  await complete($, 'ts')
  await f.clock.advance(100)
  expect(reports(f)).toEqual([
    'relay report ' + OPID + ' writing',
    'relay report ' + OPID + ' written',
    'relay report ' + OPID + ' cleared --new-session sid-new',
    'relay report ' + OPID + ' done',
  ])
  const at = (needle: string) => f.order.findIndex((o) => o.startsWith(needle))
  expect(at('pdx relay lock ' + OPID)).toBeGreaterThan(at('submit'))
  expect(at('pdx relay lock ' + OPID)).toBeLessThan(at('turn.start')) // …and the lock is up before the write turn enters (the first turn.start here is the write turn's)
  expect(at('pdx relay unlock ' + OPID)).toBeLessThan(at('command clear'))
  expect(at('pdx relay unlock ' + OPID)).toBeGreaterThan(at('pdx relay lock ' + OPID))
  expect(nonceOf(write)).toBeTruthy()
})

// R1 (codex): a turn that starts while the claim is out must finish before the write prompt goes out.
// Mutation gate: startWrite at once after the claim → the submit appears before turn.complete → red.
test('a turn that starts while the claim is out is not overlapped: the write prompt waits for its turn.complete', async ($, on) => {
  let release: (r: R) => void = () => {}
  const gate = new Promise<R>((r) => { release = r })
  const f = memberWorld(on, 'member', (argv) => (argv[1] === 'claim' ? (gate as any) : undefined))
  await start($, f)
  await receive($, ENVELOPE(CONTROL))
  await f.clock.advance(10) // the claim is out
  await $.turn.start({ text: 'the user typed something', turnId: 't1' })
  release({ exitCode: 0, stdout: CLAIM })
  await f.clock.advance(500)
  expect(calls(f, 'claim').length).toBe(1)
  expect(f.submits).toEqual([]) // not while t1 runs
  await complete($, 't1')
  await f.clock.advance(200)
  expect(f.submits.length).toBe(1)
  expect(reports(f)).toEqual(['relay report ' + OPID + ' writing'])
})
