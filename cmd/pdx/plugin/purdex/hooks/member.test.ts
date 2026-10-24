// hooks/member.test.ts — the member relay (plan v3 P6-6), run by `claude plugin test cmd/pdx/plugin/purdex`.
// A small engine fake beneath the mod (a copy of relay.test.ts's, kept apart so the two files stay independent):
// a fake pdx behind $.process.run, $.session.*, $.fs.read, $.ui.*, and the bottom of every event a test raises.
import { test, expect, mock } from 'claude-code/testing'
import { controlOp, controlReask, parseTeamArgs } from './member.js'

const OPID = '3f2b8c1e-9a4d-4e6b-8c7a-1d2e3f4a5b6c'
const CONTROL = '[pdx-relay:control] op=' + OPID
const ENVELOPE = (text: string) => '<cross-session-message from="uds:/tmp/cc-socks/1.sock" from-name="mlab/lead-x">\n' + text + '\n</cross-session-message>'
const PEER = { kind: 'peer' }
const OP = { id: OPID, kind: 'member_relay', host_id: 'h', session_id: 'sid-old', ref: '_abc123', state: 'claimed', handoff_path: '/data/relay/' + OPID + '.md', created_at: 1, updated_at: 1 }
const LEAD = { address: 'mlab/purdex-1f-vq', ref: '_h9movr', team_id: 'team-1' }
const CLAIM = JSON.stringify({ op: OP, lead: LEAD })
const GOOD_FILE = '# HANDOFF\n' + ['## 1. a', '## 2. b', '## 3. c', '## 4. d', '## 5. e', '## 6. f', '## 7. g', '## 8. h'].map((h) => h + '\n' + 'x'.repeat(40)).join('\n')
const HELLO = (role: string, level = 1) => JSON.stringify({ ok: true, role, self_relay: 'on', threshold: 70, min_growth: 20000, ...(level > 0 ? { idle_intercept: level } : {}) })
const sub = (a: string[]) => a.slice(1).join(' ')

type R = { exitCode: number; stdout?: string; stderr?: string }
type World = {
  argvs: string[][]
  order: string[]
  submits: any[]
  commands: string[]
  files: Record<string, string>
  toasts: string[]
  statuses: (string | undefined)[]
  logs: string[]
  clock: any
  sessionId: string
  usage: { tokens: number; window: number; percent: number }
  switchTo?: string
  failComplete?: boolean
  skipCompact?: boolean
  pdx: (argv: string[]) => R | Promise<R>
}

function memberWorld(on: any, role: string, pdx?: (argv: string[]) => R | undefined): World {
  const f: World = {
    argvs: [], order: [], submits: [], commands: [], files: {}, toasts: [], statuses: [], logs: [], sessionId: 'sid-old', clock: undefined as any, usage: { tokens: 144000, window: 200000, percent: 72 },
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
  on('session.usage', async () => ({ value: { startedAt: 0, context: f.usage, rateLimits: [] } }))
  on('fs.read', async (_$: any, e: any) => (e.path in f.files ? { value: f.files[e.path] } : { deny: 'ENOENT' }))
  on('ui.toast', async (_$: any, e: any) => { f.toasts.push(e.text); return { value: undefined } })
  on('ui.status', async (_$: any, e: any) => { f.statuses.push(e.text); return { value: undefined } })
  on('ui.log', async (_$: any, e: any) => { f.logs.push(e.text); return { value: undefined } })
  on('command.register', async (_$: any, e: any) => ({ value: { command: e.name } }))
  on('session.start', async (_$: any, e: any) => ({ cwd: e.cwd }))
  on('turn.start', async (_$: any, e: any) => { f.order.push('turn.start'); return { turnId: e.turnId } })
  on('turn.complete', async (_$: any, e: any) => { if (f.failComplete) throw new Error('a hook beneath failed'); return { text: e.answer } })
  on('session.receive', async (_$: any, e: any) => ({ text: e.text }))
  on('classic.SessionStart', async () => { if (f.switchTo) { f.sessionId = f.switchTo; f.switchTo = undefined } return {} })
  on('prompt.submit', async (_$: any, e: any) => { f.submits.push(e); f.order.push('submit'); return { text: e.text, context: e.context } })
  on('command.run', async (_$: any, e: any) => { f.commands.push(e.command); f.order.push('command ' + e.command); return { text: 'ran ' + e.command } })
  on('session.compact', async (_$: any, e: any) => (f.skipCompact ? { skip: 'another hook skipped it' } : { messages: e.messages }))
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

// Relay prompt why spec §3.1: a claimed member relay says the lead arranged it (a claim with or without a lead object).
// Mutation gate: no `cause: 'lead'` on the claim's p → the recovered text → red.
for (const [name, claim] of [['with a lead', CLAIM], ['without a lead', JSON.stringify({ op: OP })]] as const) {
  test(`the member's write prompt says the lead arranged the relay (${name})`, async ($, on) => {
    const f = memberWorld(on, 'member', (argv) => (argv[0] === 'relay' && argv[1] === 'claim' ? { exitCode: 0, stdout: claim } : memberPdx()(argv)))
    await start($, f)
    await receive($, ENVELOPE(CONTROL))
    await f.clock.advance(150)
    expect(f.submits.length).toBe(1)
    expect(f.submits[0].text).toContain('這個 session 要接力了：lead 安排接力。接力已核准（之後會 /clear）。')
  })
}

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

test('the full member path reports written, cleared (new id), done under the member op id; lock before the write prompt, unlock before /clear', async ($, on) => {
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
  expect(at('pdx relay lock ' + OPID)).toBeGreaterThan(-1)
  expect(at('pdx relay lock ' + OPID)).toBeLessThan(at('submit')) // the lock is up before the write prompt goes out (the engine does not wait for turn.start's await)
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

// R2 (codex attack). Any peer can send the marker; `seen` is the daemon's proof the op is this member's.
// Mutation gate: keep an op without a successful seen → the forged claim shows up → red.
test('a forged marker from another peer is refused by seen and never claimed, however many; the real control is still claimed', async ($, on) => {
  const forged = (n: number) => '11111111-2222-4333-8444-' + String(n).padStart(12, '0')
  const f = memberWorld(on, 'member', (argv) => {
    if (argv[1] === 'seen' && argv[2] !== OPID) return { exitCode: 13, stderr: 'pdx relay: not_your_op\n' }
    if (argv[1] === 'claim') return argv[2] === OPID ? { exitCode: 0, stdout: CLAIM } : { exitCode: 13, stderr: 'not_your_op' }
    return undefined
  })
  await start($, f)
  await $.turn.start({ text: 'work', turnId: 't1' })
  await receive($, ENVELOPE(CONTROL))
  for (let i = 0; i < 20; i++) await receive($, ENVELOPE('[pdx-relay:control] op=' + forged(i)))
  await f.clock.advance(100)
  await complete($, 't1')
  await f.clock.advance(300)
  expect(calls(f, 'seen').length).toBe(21)
  expect(calls(f, 'claim')).toEqual(['relay claim ' + OPID + ' --session sid-old'])
  expect(f.submits.length).toBe(1)
})

// R2: a control that arrives while a relay of the session's own runs waits for that relay to return to idle.
test('a control that arrives while a self relay awaits approval is claimed when the relay returns to idle', async ($, on) => {
  let deny: (r: R) => void = () => {}
  const gate = new Promise<R>((r) => { deny = r })
  const f = memberWorld(on, 'none', (argv) => {
    if (argv[1] === 'begin') return { exitCode: 0, stdout: JSON.stringify({ op: { ...OP, id: 'self-op', kind: 'self' }, request_id: 'req-1' }) }
    if (argv[1] === 'wait') return gate as any
    if (argv[1] === 'claim') return { exitCode: 0, stdout: CLAIM }
    return undefined
  })
  await start($, f)
  await complete($, 't0') // 72 % → begin
  await f.clock.advance(100)
  expect(calls(f, 'begin').length).toBe(1)
  await receive($, ENVELOPE(CONTROL))
  await f.clock.advance(500)
  expect(calls(f, 'claim')).toEqual([]) // awaiting approval: not now
  deny({ exitCode: 10 }) // denied → back to idle
  await f.clock.advance(500)
  expect(calls(f, 'claim')).toEqual(['relay claim ' + OPID + ' --session sid-old'])
})

// R2: a hook beneath turn.complete that throws must not leave the turn "running" for good.
test('turnRunning is cleared even when a turn.complete hook beneath throws', async ($, on) => {
  const f = memberWorld(on, 'member', memberPdx())
  await start($, f)
  await $.turn.start({ text: 'work', turnId: 't1' })
  f.failComplete = true
  await complete($, 't1').catch(() => {})
  f.failComplete = false
  await receive($, ENVELOPE(CONTROL))
  await f.clock.advance(300)
  expect(calls(f, 'claim').length).toBe(1)
})

// R1 incremental (codex): a seen answer that lands after the relay returned to idle still counts; one that lands after a /clear does not.
test('a seen answer that lands after the relay returned to idle is still kept', async ($, on) => {
  let answer: (r: R) => void = () => {}
  const gate = new Promise<R>((r) => { answer = r })
  const f = memberWorld(on, 'none', (argv) => {
    if (argv[1] === 'begin') return { exitCode: 0, stdout: JSON.stringify({ op: { ...OP, id: 'self-op', kind: 'self' }, request_id: 'req-1' }) }
    if (argv[1] === 'wait') return { exitCode: 10 }
    if (argv[1] === 'seen') return gate as any
    if (argv[1] === 'claim') return { exitCode: 0, stdout: CLAIM }
    return undefined
  })
  await start($, f)
  await complete($, 't0')
  await f.clock.advance(10) // begin → awaiting
  await receive($, ENVELOPE(CONTROL))
  await f.clock.advance(300) // the wait answers denied: back to idle (gen moved) while seen is out
  answer({ exitCode: 0, stdout: '{}' })
  await f.clock.advance(300)
  expect(calls(f, 'claim')).toEqual(['relay claim ' + OPID + ' --session sid-old'])
})

// R2 (codex): a user turn that starts while the lock call is out does not run under the lock; the write waits for it.
// Mutation gate: no turnRunning check after the lock → the write is submitted under the running turn, no unlock → red.
test('a turn that starts while the lock call is out is unlocked and the write waits for its turn.complete', async ($, on) => {
  let release: (r: R) => void = () => {}
  const gate = new Promise<R>((r) => { release = r })
  const f = memberWorld(on, 'member', (argv) => (argv[1] === 'claim' ? { exitCode: 0, stdout: CLAIM } : argv[1] === 'lock' ? (gate as any) : undefined))
  await start($, f)
  await receive($, ENVELOPE(CONTROL))
  await f.clock.advance(300) // claimed; the lock call is out
  expect(calls(f, 'lock').length).toBe(1)
  await $.turn.start({ text: 'the user typed something', turnId: 't1' })
  release({ exitCode: 0, stdout: '{}' })
  await f.clock.advance(300)
  expect(f.submits).toEqual([]) // not while t1 runs
  expect(calls(f, 'unlock').length).toBe(1) // t1 did not run locked
  await complete($, 't1')
  await f.clock.advance(300)
  expect(calls(f, 'lock').length).toBe(2) // locked afresh
  expect(f.submits.length).toBe(1)
  expect(f.order.indexOf('submit')).toBeGreaterThan(f.order.lastIndexOf('pdx relay lock ' + OPID + ' --session sid-old'))
})

// ---- P7-2: the mod reports every compaction it does not intercept ----
const MSGS = [{ role: 'user' as const, text: 'hi', toolUses: [] }]
const compactOf = ($: any, trigger: string) => $.session.compact({ trigger, messages: MSGS })

// (a member's is intercepted since RT-4 T42: see below)
for (const role of ['none', 'lead']) {
  test(`an auto compaction in idle reports once from a timer, whatever the role (${role})`, async ($, on) => {
    const f = memberWorld(on, role)
    await start($, f)
    expect(await compactOf($, 'auto')).toEqual({ messages: MSGS })
    expect(calls(f, 'compacted')).toEqual([]) // from a timer, not inside the hook
    await f.clock.advance(10)
    expect(calls(f, 'compacted')).toEqual(['relay compacted --session sid-old --trigger auto'])
  })
}

test('a manual compaction is reported too (the daemon decides); a precompute is not', async ($, on) => {
  const f = memberWorld(on, 'member')
  await start($, f)
  await compactOf($, 'manual')
  await compactOf($, 'precompute')
  await f.clock.advance(10)
  expect(calls(f, 'compacted')).toEqual(['relay compacted --session sid-old --trigger manual'])
})

// Mutation gate: await the report inside the hook → the compaction is still pending while the report is out → red.
test('the compaction never waits for the report', async ($, on) => {
  let release: (r: R) => void = () => {}
  const gate = new Promise<R>((r) => { release = r })
  const f = memberWorld(on, 'none', (argv) => (argv[1] === 'compacted' ? (gate as any) : undefined))
  await start($, f)
  let done = false
  const p = compactOf($, 'auto').then((r: any) => { done = true; return r })
  await f.clock.advance(10)
  expect(done).toBe(true) // the report is out and unanswered
  expect(calls(f, 'compacted').length).toBe(1)
  release({ exitCode: 0, stdout: '{"noticed":true}' })
  expect(await p).toEqual({ messages: MSGS })
})

// R2 (codex attack): a compaction that a hook beneath skipped did not happen: nothing is reported.
// Mutation gate: report before next(e) → red.
test('a compaction skipped beneath the mod is not reported', async ($, on) => {
  const f = memberWorld(on, 'none')
  await start($, f)
  f.skipCompact = true
  await compactOf($, 'auto')
  await f.clock.advance(50)
  expect(calls(f, 'compacted')).toEqual([])
})


// ---------- unattended quota pause (UP-4): the paused latch and the reask control ----------
// The daemon answers a begin with 409 paused_no_quota when the host is unattended and the relay quota is spent: the session is
// paused, no op exists. The mod goes idle, holds nothing, and asks no more (a latch — the +10 % gate alone would ask again at the
// next 10 points) until the daemon sends `reask` (the person came back / released it) or the session starts over.

const REASK_ID = '7c1e2a4b-0d3f-4a58-9b6e-2f1d3c4b5a69'
const REASK = '[pdx-relay:control] reask id=' + REASK_ID
const PAUSED: R = { exitCode: 13, stderr: 'pdx relay: 接力額度用完，先暫停 paused_no_quota\n' }
const begins = (f: World) => calls(f, 'begin')
// A solo session whose begin is answered by `answer` (default: paused); every other call as memberWorld.
const soloWorld = (on: any, answer: (argv: string[]) => R = () => PAUSED) =>
  memberWorld(on, 'none', (argv) => (argv[0] === 'relay' && argv[1] === 'begin' ? answer(argv) : undefined))
const turn = async ($: any, f: World, id: string) => {
  await $.turn.start({ text: 'work', turnId: id })
  await complete($, id)
  await f.clock.settle()
}

// Mutation gate: the latch does not stop the ask → the second turn (90 %, 18 points over the last ask) begins again (red).
test('a 409 paused_no_quota sets the latch: no self ask on the turns after it, however much the context grows', async ($, on) => {
  const f = soloWorld(on)
  await start($, f)
  await turn($, f, 't1')
  expect(begins(f).length).toBe(1)
  f.usage = { tokens: 180000, window: 200000, percent: 90 }
  await turn($, f, 't2')
  f.usage = { tokens: 198000, window: 200000, percent: 99 }
  await turn($, f, 't3')
  expect(begins(f).length).toBe(1)
  expect(calls(f, 'wait')).toEqual([]) // nothing waits for an approval
})

// Mutation gate: a refusal other than paused_no_quota sets the latch → the second ask (at +10) never happens (red).
test('any other refusal does not latch: the mod asks again at +10, as before', async ($, on) => {
  const f = soloWorld(on, () => ({ exitCode: 13, stderr: 'pdx relay: self_relay_paused' }))
  await start($, f)
  await turn($, f, 't1')
  f.usage = { tokens: 180000, window: 200000, percent: 90 }
  await turn($, f, 't2')
  expect(begins(f).length).toBe(2)
})

// Mutation gate: the reask is handed on to the model (return next(e)) → red.
test('the reask control is consumed — the model never sees it — and sends pdx relay begin --reask once, with the current usage', async ($, on) => {
  const f = soloWorld(on)
  await start($, f)
  await turn($, f, 't1')
  f.usage = { tokens: 180000, window: 200000, percent: 90 }
  const out = await receive($, ENVELOPE(REASK))
  expect(out).toEqual({ consumed: expect.any(String) })
  await f.clock.advance(100)
  expect(begins(f)).toEqual([
    'relay begin --self --session sid-old --used 72 --window 200000',
    'relay begin --self --session sid-old --used 90 --window 200000 --reask ' + REASK_ID,
  ])
  expect(calls(f, 'seen')).toEqual([]) // it is not a member-relay control
  expect(calls(f, 'claim')).toEqual([])
})

// Mutation gate: no seen set → the same id begins twice (red).
test('a reask delivered twice (same id) begins once', async ($, on) => {
  const f = soloWorld(on)
  await start($, f)
  await turn($, f, 't1')
  await receive($, ENVELOPE(REASK))
  await f.clock.advance(100)
  await receive($, ENVELOPE(REASK))
  await f.clock.advance(100)
  expect(begins(f).filter((c) => c.includes('--reask')).length).toBe(1)
})

test('the seen set is bounded: an old id is forgotten, the newest are not repeated', async ($, on) => {
  const f = soloWorld(on)
  await start($, f)
  const ids = Array.from({ length: 40 }, (_, i) => '7c1e2a4b-0d3f-4a58-9b6e-' + String(i).padStart(12, '0'))
  for (const id of ids) {
    await receive($, ENVELOPE('[pdx-relay:control] reask id=' + id))
    await f.clock.advance(100)
  }
  const reasks = () => begins(f).filter((c) => c.includes('--reask')).length
  const n = reasks()
  await receive($, ENVELOPE('[pdx-relay:control] reask id=' + ids[39])) // the newest: still remembered
  await f.clock.advance(100)
  expect(reasks()).toBe(n)
})

// Mutation gate: the reask does not clear the latch → the third turn never asks (red).
test('a reask clears the latch: the mod may ask again at +10 afterwards', async ($, on) => {
  let n = 0
  const f = soloWorld(on, (argv) => (argv.includes('--reask') ? { exitCode: 20, stderr: 'daemon_unavailable' } : (n++, PAUSED)))
  await start($, f)
  await turn($, f, 't1') // paused: latched
  await receive($, ENVELOPE(REASK))
  await f.clock.advance(100) // the reask begin fails (daemon down): idle, latch cleared
  f.usage = { tokens: 190000, window: 200000, percent: 95 }
  await turn($, f, 't2')
  expect(begins(f).length).toBe(3) // t1, the reask, and t2's own ask
})

// Mutation gate: session.start keeps the latch → red.
test('session.start clears the latch', async ($, on) => {
  const f = soloWorld(on)
  await start($, f)
  await turn($, f, 't1')
  f.usage = { tokens: 180000, window: 200000, percent: 90 }
  await turn($, f, 't2')
  expect(begins(f).length).toBe(1) // latched
  await start($, f) // a new session.start (a restart / resume)
  await turn($, f, 't3')
  expect(begins(f).length).toBe(2)
})

// A reask that arrives while a turn runs waits for that turn to end (as a member's claim does): the begin is a turn-boundary act.
test('a reask during a turn is consumed at once and begins at that turn’s end', async ($, on) => {
  const f = soloWorld(on)
  await start($, f)
  await turn($, f, 't1')
  await $.turn.start({ text: 'work', turnId: 't2' })
  expect(await receive($, ENVELOPE(REASK))).toEqual({ consumed: expect.any(String) })
  await f.clock.advance(1000)
  expect(begins(f).filter((c) => c.includes('--reask')).length).toBe(0)
  await complete($, 't2')
  await f.clock.settle()
  expect(begins(f).filter((c) => c.includes('--reask')).length).toBe(1)
})

// A reask that arrives while a relay of the mod's own is out waits for it, and is begun as the relay ends — not at some later turn.
// Mutation gate: drain only at turn.complete (not at the return to idle) → no reask begin here (red).
test('a reask that arrives while a relay is out is begun when that relay ends', async ($, on) => {
  const f = memberWorld(on, 'none', (argv) => {
    if (argv[0] === 'relay' && argv[1] === 'begin') return argv.includes('--reask') ? PAUSED : { exitCode: 0, stdout: JSON.stringify({ op: { ...OP, kind: 'self', state: 'awaiting_approval' }, request_id: 'req-1' }) }
    if (argv[0] === 'relay' && argv[1] === 'wait') return new Promise<R>((resolve) => { (f as any).deny = () => resolve({ exitCode: 10 }) })
    return undefined
  })
  await start($, f)
  await turn($, f, 't1') // begins: awaiting the approval
  expect(await receive($, ENVELOPE(REASK))).toEqual({ consumed: expect.any(String) })
  await f.clock.advance(1000)
  expect(begins(f).filter((c) => c.includes('--reask')).length).toBe(0) // a relay is out
  ;(f as any).deny() // the person denied it: back to idle
  await f.clock.advance(1000)
  expect(begins(f).filter((c) => c.includes('--reask')).length).toBe(1)
})

// Only a peer's text counts, as for the member-relay control.
test('the reask text from the user or any other origin is ordinary text', async ($, on) => {
  const f = soloWorld(on)
  await start($, f)
  const text = ENVELOPE(REASK)
  for (const kind of ['bridge', 'unclassified', 'peer-send-message', 'task-notification']) expect(await receive($, text, { kind })).toEqual({ text })
  for (const other of [ENVELOPE('[pdx-relay:control] reask id='), ENVELOPE('[pdx-relay:control] reask id=not-a-uuid'), ENVELOPE('pdx-relay:control reask id=' + REASK_ID)]) {
    expect(await receive($, other)).toEqual({ text: other })
  }
  await f.clock.advance(1000)
  expect(begins(f)).toEqual([])
})

// The marker must stand alone on its own line of a short message: mentioning it is not sending it (codex).
// Mutation gate: an unanchored match → the embedded cases are consumed (red).
test('a reask marker inside a sentence, a quote or a very long message is ordinary text', async ($, on) => {
  const f = soloWorld(on)
  await start($, f)
  for (const text of [
    ENVELOPE('please note: ' + REASK + ' was sent'),
    ENVELOPE('> ' + REASK),
    ENVELOPE(REASK + ' and then some more words on the same line'),
    ENVELOPE('x'.repeat(3000) + '\n' + REASK),
  ]) expect(await receive($, text)).toEqual({ text })
  await f.clock.advance(1000)
  expect(begins(f)).toEqual([])
  // alone on its line within a short message: the control
  expect(await receive($, ENVELOPE('release\n' + REASK + '\n'))).toEqual({ consumed: expect.any(String) })
})

// A user's /clear is a new session id the daemon never paused: the old conversation's latch must not follow it.
// Mutation gate: keep the latch across /clear → the new session never asks (red).
test('a /clear clears the latch: the new session asks at the threshold', async ($, on) => {
  const f = soloWorld(on)
  await start($, f)
  await turn($, f, 't1')
  expect(begins(f).length).toBe(1)
  f.switchTo = 'sid-new'
  await $.classic.SessionStart({ source: 'clear' })
  await f.clock.settle()
  f.usage = { tokens: 150000, window: 200000, percent: 75 }
  await turn($, f, 't2')
  expect(begins(f).length).toBe(2)
})

// A reask that arrives before hello has answered is kept and begun when it does (not lost, not marked done).
// Mutation gate: drop it when hello is not answered (return without keeping) → no reask begin (red).
test('a reask before hello answers is begun when hello answers, once', async ($, on) => {
  let release!: (r: R) => void
  const gate = new Promise<R>((r) => { release = r })
  const f = soloWorld(on)
  const own = f.pdx
  f.pdx = (argv) => (argv[0] === 'relay' && argv[1] === 'hello' ? gate : own(argv))
  await $.session.start({ cwd: '/tmp', surface: 'terminal', isInteractive: true })
  await f.clock.advance(10)
  expect(await receive($, ENVELOPE(REASK))).toEqual({ consumed: expect.any(String) })
  await f.clock.advance(100)
  expect(begins(f)).toEqual([]) // hello has not answered: nothing is begun yet
  release({ exitCode: 0, stdout: HELLO('none') })
  await f.clock.settle()
  expect(begins(f).filter((c) => c.includes('--reask')).length).toBe(1)
  await receive($, ENVELOPE(REASK)) // the same id again
  await f.clock.settle()
  expect(begins(f).filter((c) => c.includes('--reask')).length).toBe(1)
})

// A begin that is out when the reask arrives answers "paused" later: that must not latch the session again (the reask cleared it).
// Mutation gate: drop the epoch check → the older begin's answer re-sets the latch and the next turn never asks (red).
test('a paused answer to a begin that was out before the reask does not re-set the latch', async ($, on) => {
  let n = 0
  let answerFirst!: (r: R) => void
  const first = new Promise<R>((r) => { answerFirst = r })
  const f = soloWorld(on, (argv) => (argv.includes('--reask') ? { exitCode: 20, stderr: 'daemon_unavailable' } : (n++ === 0 ? (first as any) : PAUSED)))
  await start($, f)
  await $.turn.start({ text: 'work', turnId: 't1' })
  await complete($, 't1') // begin goes out and is not answered yet
  await f.clock.advance(100)
  expect(begins(f).length).toBe(1)
  await receive($, ENVELOPE(REASK)) // arrives while that begin is out
  answerFirst(PAUSED) // the older begin now answers: paused
  await f.clock.settle()
  expect(begins(f).filter((c) => c.includes('--reask')).length).toBe(1) // the reask begin ran (and failed: daemon down)
  f.usage = { tokens: 190000, window: 200000, percent: 95 }
  await turn($, f, 't2')
  expect(begins(f).length).toBe(3) // the latch is clear: t2 asks on its own
})

// A duplicate of a reask already taken is ignored whole: it must not move the latch epoch under an ordinary begin that is out.
// Mutation gate: clear/advance before the duplicate check → that begin's paused answer is dropped and the session keeps asking (red).
test('a duplicate reask does not disarm the latch of a begin that was sent after the first one', async ($, on) => {
  let answerSecond!: (r: R) => void
  const second = new Promise<R>((r) => { answerSecond = r })
  let n = 0
  const f = soloWorld(on, (argv) => (argv.includes('--reask') ? { exitCode: 20, stderr: 'daemon_unavailable' } : (n++ === 1 ? (second as any) : PAUSED)))
  await start($, f)
  await turn($, f, 't1') // paused: latched
  await receive($, ENVELOPE(REASK))
  await f.clock.advance(100) // the reask begin fails: idle, latch clear
  f.usage = { tokens: 170000, window: 200000, percent: 85 }
  await $.turn.start({ text: 'work', turnId: 't2' })
  await complete($, 't2') // an ordinary begin goes out and waits
  await f.clock.advance(100)
  await receive($, ENVELOPE(REASK)) // the same reask again, while that begin is out
  answerSecond(PAUSED)
  await f.clock.settle()
  f.usage = { tokens: 199000, window: 200000, percent: 99 }
  await turn($, f, 't3')
  expect(begins(f).filter((c) => !c.includes('--reask')).length).toBe(2) // t1 and t2 only: the latch from t2's answer stands
})

// The same reask delivered twice back to back: the twin finds the session busy with the first one's relay, is kept, and when that
// relay ends it must NOT begin again — the token was spent.
// Mutation gate: no seen re-check where the call starts and after its awaits → the twin begins a second time (red).
test('two deliveries of one reask that arrive back to back begin once, even after the first relay ends', async ($, on) => {
  let deny!: () => void
  const f = soloWorld(on, (argv) => (argv.includes('--reask') ? { exitCode: 0, stdout: JSON.stringify({ op: { ...OP, kind: 'self', state: 'awaiting_approval' }, request_id: 'req-9' }) } : PAUSED))
  const own = f.pdx
  f.pdx = (argv) => (argv[0] === 'relay' && argv[1] === 'wait' ? new Promise<R>((resolve) => { deny = () => resolve({ exitCode: 10 }) }) : own(argv))
  await start($, f)
  await turn($, f, 't1')
  await receive($, ENVELOPE(REASK))
  await receive($, ENVELOPE(REASK)) // before any timer ran
  await f.clock.advance(1000) // the first reask begins and its wait starts
  expect(begins(f).filter((c) => c.includes('--reask')).length).toBe(1)
  deny() // the card was denied: back to idle
  await f.clock.advance(1000)
  expect(begins(f).filter((c) => c.includes('--reask')).length).toBe(1)
})

// The two parsers do not mistake each other's message.
test('the op control and the reask control are told apart', async () => {
  const peer = (text: string) => ({ origin: PEER, text })
  expect(controlOp(peer(ENVELOPE(CONTROL)))).toBe(OPID)
  expect(controlReask(peer(ENVELOPE(CONTROL)))).toBe('')
  expect(controlReask(peer(ENVELOPE(REASK)))).toBe(REASK_ID)
  expect(controlOp(peer(ENVELOPE(REASK)))).toBe('')
  expect(controlReask({ origin: { kind: 'bridge' }, text: ENVELOPE(REASK) })).toBe('')
})

test('an op control still reaches the member path; a reask is not claimed or seen', async ($, on) => {
  const f = memberWorld(on, 'member', memberPdx())
  await start($, f)
  await receive($, ENVELOPE(CONTROL))
  await f.clock.advance(10)
  expect(calls(f, 'seen').length).toBe(1)
  await receive($, ENVELOPE(REASK))
  await f.clock.advance(10)
  expect(calls(f, 'seen').length).toBe(1)
})

test('hello reports protocol 9 (5 was the one the daemon sends reask to, 6 the person tokens, 7 the ask ladder, 8 the idle half, 9 the member idle intercept)', async ($, on) => {
  const f = soloWorld(on)
  await start($, f)
  expect(calls(f, 'hello')[0]).toContain('--version 9')
})

// An old daemon (no paused_no_quota anywhere) is unchanged: a begin that opens a request waits for it as always.
test('a daemon without the pause behaves as before: begin opens the request and the mod waits', async ($, on) => {
  const f = soloWorld(on, () => ({ exitCode: 0, stdout: JSON.stringify({ op: { ...OP, kind: 'self', state: 'awaiting_approval' }, request_id: 'req-1' }) }))
  await start($, f)
  await turn($, f, 't1')
  await f.clock.advance(100)
  expect(begins(f).length).toBe(1)
  expect(calls(f, 'wait').length).toBeGreaterThan(0)
})

// ---- parseTeamArgs (RT-1c): what follows /team ----
// Mutation gate: treat a note that starts with a keyword as a note / let --model through for a non-spawn → a row red.
test('parseTeamArgs: nothing is show, other text is the note, spawn takes its flags, adopt/release/kill take one ref', () => {
  expect(parseTeamArgs('')).toEqual({ kind: 'show' })
  expect(parseTeamArgs('   ')).toEqual({ kind: 'show' })
  expect(parseTeamArgs(undefined)).toEqual({ kind: 'show' })
  expect(parseTeamArgs('B 線 上限 2')).toEqual({ kind: 'lead', note: 'B 線 上限 2' })
  expect(parseTeamArgs('--model opus 做事')).toEqual({ kind: 'lead', note: '--model opus 做事' }) // the flags belong to spawn only
  expect(parseTeamArgs('spawn 修登入 bug')).toEqual({ kind: 'spawn', task: '修登入 bug' })
  expect(parseTeamArgs('spawn 修 bug --model opus[1m] --effort xhigh')).toEqual({ kind: 'spawn', task: '修 bug', model: 'opus[1m]', effort: 'xhigh' })
  expect(parseTeamArgs('spawn --effort=low 寫測試 --model=sonnet')).toEqual({ kind: 'spawn', task: '寫測試', model: 'sonnet', effort: 'low' })
  expect(parseTeamArgs('adopt _abc123')).toEqual({ kind: 'adopt', ref: '_abc123' })
  expect(parseTeamArgs('release mlab/_abc123')).toEqual({ kind: 'release', ref: 'mlab/_abc123' })
  expect(parseTeamArgs('kill _abc123')).toEqual({ kind: 'kill', ref: '_abc123' })
})

test('parseTeamArgs: bad spawn and ref commands are errors, never guessed', () => {
  const err = (a: string) => (parseTeamArgs(a) as any).error as string
  expect(err('spawn')).toBe('用法：/team spawn <任務> [--model m] [--effort e]')
  expect(err('spawn --model opus')).toBe('用法：/team spawn <任務> [--model m] [--effort e]') // flags alone are no task
  expect(err('spawn x --model')).toContain('--model 需要一個值')
  expect(err('spawn x --model --effort high')).toContain('--model 需要一個值')
  expect(err('spawn x --model "bad name"')).toContain('--model 的值不合法')
  expect(err('spawn x --effort wild')).toContain('--effort 只能是')
  expect(err('adopt')).toBe('用法：/team adopt <ref>')
  expect(err('kill a b')).toBe('用法：/team kill <ref>')
  expect(err('release --all')).toBe('用法：/team release <ref>')
})

// ---- parseTeamArgs with the persona list (name pool v2 spec §5.3): the first word after /team may name the lead ----
const NAMES = {
  leadName: true,
  personas: [
    { code: 'hiro', name: 'Hiro Tanaka' },
    { code: 'freya', name: 'Freya Lindqvist' },
    { code: 'sato', name: 'Hiro Sato' }, // the same first word of the display name as hiro
    { code: 'maria', name: 'María José Ortega' },
  ],
}

// Mutation gate: match on the code only → the display-name row red; case-sensitive → the case row red; first persona wins on a tie
// → the ambiguous row red; keep the word in the note → the note row red.
test('parseTeamArgs with personas: a code or the first word of a display name picks the lead name, the rest is the note', () => {
  expect(parseTeamArgs('freya 修登入 bug', NAMES)).toEqual({ kind: 'lead', note: '修登入 bug', leadName: 'freya' })
  expect(parseTeamArgs('FREYA', NAMES)).toEqual({ kind: 'lead', note: '', leadName: 'freya' }) // case-insensitive, no note
  expect(parseTeamArgs('maría 看一下', NAMES)).toEqual({ kind: 'lead', note: '看一下', leadName: 'maria' }) // the display name's first word
  expect(parseTeamArgs('Sato  多個   空白', NAMES)).toEqual({ kind: 'lead', note: '多個   空白', leadName: 'sato' })
})

test('parseTeamArgs with personas: a word that matches more than one persona, or none, is a card; the daemon never picks', () => {
  // "hiro" is the code of one persona and the first word of two display names (Hiro Tanaka, Hiro Sato): two personas match
  expect(parseTeamArgs('hiro 做事', NAMES)).toEqual({ kind: 'lead', note: 'hiro 做事', card: true })
  expect(parseTeamArgs('B 線 上限 2', NAMES)).toEqual({ kind: 'lead', note: 'B 線 上限 2', card: true })
  expect(parseTeamArgs('--model opus 做事', NAMES)).toEqual({ kind: 'lead', note: '--model opus 做事', card: true })
  expect(parseTeamArgs('', NAMES)).toEqual({ kind: 'show', card: true }) // nothing typed: a session that is no lead gets the card
})

// The other kinds are not names, whatever the list holds; and without the list (an older pdx, or a daemon without the capability)
// everything is exactly what it was.
test('parseTeamArgs with personas: spawn, adopt, release, kill are untouched; no list, or lead_name false, is today’s parse', () => {
  const withSpawnName = { leadName: true, personas: [{ code: 'spawn', name: 'Spawn Person' }, { code: 'kill', name: 'Kill Person' }] }
  expect(parseTeamArgs('spawn 修 bug', withSpawnName)).toEqual({ kind: 'spawn', task: '修 bug' })
  expect(parseTeamArgs('kill _abc123', withSpawnName)).toEqual({ kind: 'kill', ref: '_abc123' })
  expect(parseTeamArgs('freya 修登入 bug')).toEqual({ kind: 'lead', note: 'freya 修登入 bug' })
  expect(parseTeamArgs('freya 修登入 bug', undefined)).toEqual({ kind: 'lead', note: 'freya 修登入 bug' })
  expect(parseTeamArgs('freya 修登入 bug', { leadName: false, personas: NAMES.personas })).toEqual({ kind: 'lead', note: 'freya 修登入 bug' })
  expect(parseTeamArgs('', { leadName: false, personas: [] })).toEqual({ kind: 'show' })
})

// ---- RT-3c T39: a handoff op - the member writes its handoff and goes on, it is closed by the daemon ----

const HANDOFF_OP = { ...OP, mode: 'handoff' }
const handoffPdx = (reuse = false) => (argv: string[]): R | undefined => (argv[0] === 'relay' && argv[1] === 'claim' ? { exitCode: 0, stdout: JSON.stringify({ op: HANDOFF_OP, lead: LEAD, ...(reuse ? { reuse: true } : {}) }) } : undefined)

// The claim writes the handoff and reports written; then the mod does none of the relay's follow-ups: no /clear, no seed, no half
// (status, toast), no threshold check (a member asks its lead at the threshold: not now).
// Mutation gates: clear after a handoff → a /clear command (red); the half's status / toast → red; the member's ask after the file → red.
test('RT-3c T39: a handoff claim writes the file, reports written, and does not clear, half, or ask', async ($, on) => {
  const f = memberWorld(on, 'member', handoffPdx())
  await start($, f)
  await receive($, ENVELOPE(CONTROL))
  await f.clock.advance(200)
  expect(f.submits.length).toBe(1)
  f.files[OP.handoff_path] = GOOD_FILE
  await $.turn.start({ text: f.submits[0].text, turnId: 'tw' })
  await complete($, 'tw')
  await f.clock.advance(300)
  expect(reports(f)).toEqual(['relay report ' + OPID + ' writing', 'relay report ' + OPID + ' written'])
  expect(f.commands).toEqual([])
  expect(f.toasts).toEqual([])
  expect(f.statuses.filter((s) => s !== undefined)).toEqual([])
  expect(calls(f, 'ask')).toEqual([])
  expect(f.submits.length).toBe(1)
  // the lock the write took is lowered (the member lives on until the daemon closes it)
  expect(f.argvs.map(sub).some((c) => c.startsWith('relay unlock ' + OPID))).toBe(true)
})

// The daemon says the file is the member's fresh handoff: nothing is written. Mutation gates: ignore reuse → a write prompt (red).
test('RT-3c T39: a claim that says reuse (the file still checks out) writes nothing: writing, written', async ($, on) => {
  const f = memberWorld(on, 'member', handoffPdx(true))
  f.files[OP.handoff_path] = GOOD_FILE
  await start($, f)
  await receive($, ENVELOPE(CONTROL))
  await f.clock.advance(300)
  expect(f.submits).toEqual([])
  expect(reports(f)).toEqual(['relay report ' + OPID + ' writing', 'relay report ' + OPID + ' written'])
  expect(f.commands).toEqual([])
})

test('RT-3c T39: a reuse whose file no longer checks out is written as any handoff', async ($, on) => {
  const f = memberWorld(on, 'member', handoffPdx(true))
  f.files[OP.handoff_path] = 'short'
  await start($, f)
  await receive($, ENVELOPE(CONTROL))
  await f.clock.advance(300)
  expect(f.submits.length).toBe(1)
  expect(reports(f)).toEqual(['relay report ' + OPID + ' writing'])
})

// An ordinary member relay is as ever (the same world, no mode): clears. Guards the handoff branch against catching every member op.
test('RT-3c T39: a member op without the handoff mode still clears', async ($, on) => {
  const f = memberWorld(on, 'member', memberPdx())
  await start($, f)
  await receive($, ENVELOPE(CONTROL))
  await f.clock.advance(200)
  f.files[OP.handoff_path] = GOOD_FILE
  await $.turn.start({ text: f.submits[0].text, turnId: 'tw' })
  await complete($, 'tw')
  await f.clock.advance(100)
  expect(f.commands).toEqual(['clear'])
})

// ---- RT-4 T42: a member's idle compaction is intercepted ----

const compactEvent = (trigger: string, extra: any = {}) => ({ trigger, messages: [{ role: 'user', text: 'hi', toolUses: [] }], ...extra })
const compactCalls = (f: World) => f.argvs.map(sub).filter((c) => c.startsWith('relay compacted'))

// In a member, an auto compaction with no main turn running is Claude Code's idle compaction: the hook answers {skip} and the daemon is told,
// intercepted, from a timer. Mutation gates: no skip → the compaction runs (red); the report without --intercepted → red; not from a member → red.
test('RT-4 T42: a member’s idle compaction is skipped and reported as intercepted', async ($, on) => {
  const f = memberWorld(on, 'member', memberPdx())
  await start($, f)
  const r = await $.session.compact(compactEvent('auto'))
  expect(r).toEqual({ skip: expect.stringContaining('閒置') })
  await f.clock.advance(100)
  expect(compactCalls(f)).toEqual(['relay compacted --session sid-old --trigger idle --intercepted'])  // no turn ended yet: no stretch name
})

test('RT-4 T42: a context-full auto compaction (a turn is running) in a member runs and is reported as today', async ($, on) => {
  const f = memberWorld(on, 'member', memberPdx())
  await start($, f)
  await $.turn.start({ text: 'work', turnId: 't1' })
  const r = await $.session.compact(compactEvent('auto'))
  expect(r.skip).toBeUndefined()
  await f.clock.advance(100)
  expect(compactCalls(f)).toEqual(['relay compacted --session sid-old --trigger auto'])
})

test('RT-4 T42: a manual compaction in a member runs and is reported as manual', async ($, on) => {
  const f = memberWorld(on, 'member', memberPdx())
  await start($, f)
  const r = await $.session.compact(compactEvent('manual'))
  expect(r.skip).toBeUndefined()
  await f.clock.advance(100)
  expect(compactCalls(f)).toEqual(['relay compacted --session sid-old --trigger manual'])
})

test('RT-4 T42: a session that is not a member is not intercepted', async ($, on) => {
  const f = memberWorld(on, 'none', memberPdx())
  await start($, f)
  const r = await $.session.compact(compactEvent('auto'))
  expect(r.skip).toBeUndefined()
  await f.clock.advance(100)
  expect(compactCalls(f)).toEqual(['relay compacted --session sid-old --trigger auto'])
})

// A member in the middle of a relay keeps the rule it had (an approved relay skips an auto compaction, saying why; nothing is intercepted).
test('RT-4 T42: a member whose relay is under way is not intercepted (the relay’s own skip stays)', async ($, on) => {
  const f = memberWorld(on, 'member', memberPdx())
  await start($, f)
  await receive($, ENVELOPE(CONTROL))
  await f.clock.advance(200) // claimed: the write prompt is out (state approved)
  const r = await $.session.compact(compactEvent('auto'))
  expect(r).toEqual({ skip: expect.stringContaining('接力') })
  await f.clock.advance(100)
  expect(compactCalls(f)).toEqual([])
})

// The report names the idle stretch by when the last main turn ended; the guard needs hello answered; a daemon that routes the intercept silent makes
// the mod drop its role and say hello again. Mutation gates: no --period → red; no helloOK in the guard → red; the role kept → red.
test('RT-4 T42: the interception names the idle stretch by the end of the last turn', async ($, on) => {
  const f = memberWorld(on, 'member', memberPdx())
  await start($, f)
  await f.clock.advance(1000) // the mock clock starts at 0, which is "no turn seen": a real clock never reads 0
  await $.turn.start({ text: 'work', turnId: 't1' })
  await complete($, 't1')
  await f.clock.advance(5000)
  expect(await $.session.compact(compactEvent('auto'))).toEqual({ skip: expect.any(String) })
  await f.clock.advance(100)
  expect(compactCalls(f)).toEqual([expect.stringMatching(/^relay compacted --session sid-old --trigger idle --intercepted --period [1-9][0-9]*$/)])
})

test('RT-4 T42: before hello has answered nothing is intercepted (the role is not the daemon’s yet)', async ($, on) => {
  const f = memberWorld(on, 'member', (argv) => (argv[1] === 'hello' ? { exitCode: 20, stderr: 'unreachable' } : undefined))
  await start($, f)
  const r = await $.session.compact(compactEvent('auto'))
  expect(r.skip).toBeUndefined()
})

// A daemon that does not say it decides idle compactions (an older one, or level 0) leaves the compaction as before: nothing is skipped.
// Mutation gate: the level not read (skip on the role alone) → red.
test('RT-4 T42: a member of a daemon without idle_intercept is not intercepted', async ($, on) => {
  const f = memberWorld(on, 'member', (argv) => (argv[1] === 'hello' ? { exitCode: 0, stdout: HELLO('member', 0) } : undefined))
  await start($, f)
  const r = await $.session.compact(compactEvent('auto'))
  expect(r.skip).toBeUndefined()
  await f.clock.advance(100)
  expect(compactCalls(f).filter((c: string) => c.includes('--intercepted'))).toEqual([]) // the compaction runs and is reported as an ordinary one
})

// R2 of RT-4a: a hello whose answer is lost is said again from a timer (an idle session has no turn end to retry at), with a pause that
// doubles; the answer arms the intercept. Mutation gates: no timer → no second hello (red); the pause not doubled → the third hello comes at
// 60 s (red); a timer per failure (more than one hello per pause) → red.
test('RT-4 R2: an unanswered hello is retried from a timer, doubling the pause, and the answer arms the intercept', async ($, on) => {
  let failures = 2
  const f = memberWorld(on, 'member', (argv) => {
    if (argv[1] === 'hello' && failures-- > 0) return { exitCode: 20, stderr: 'unreachable' }
    return undefined
  })
  await start($, f)
  await f.clock.advance(100)
  expect(calls(f, 'hello').length).toBe(1) // the first one failed
  expect((await $.session.compact(compactEvent('auto'))).skip).toBeUndefined() // no answer yet: no level, nothing skipped
  await f.clock.advance(29_000)
  expect(calls(f, 'hello').length).toBe(1)
  await f.clock.advance(2_000) // 30 s after the failure
  expect(calls(f, 'hello').length).toBe(2) // retried; failed again
  await f.clock.advance(57_000)
  expect(calls(f, 'hello').length).toBe(2) // the pause doubled: not yet at 60 s
  await f.clock.advance(2_000)
  expect(calls(f, 'hello').length).toBe(4) // the third was answered; the fourth is the acknowledgement that carries the level it brought
  await f.clock.advance(300_000)
  expect(calls(f, 'hello').length).toBe(4) // and no more retries or acknowledgements
  expect(calls(f, 'hello').slice(0, 3).every((c: string) => !c.includes('--idle-intercept'))).toBe(true) // before an answer the mod knows nothing
  expect(calls(f, 'hello')[3]).toContain('--idle-intercept 1')
  expect(await $.session.compact(compactEvent('auto'))).toEqual({ skip: expect.any(String) })
})

// One timer at a time: the hello that a turn end sends, and fails, does not add a second timer to the one that is waiting.
// Mutation gate: no pending guard → a hello at 65 s that this test says is not there (red).
test('RT-4 R2: a failed turn-end hello does not add a second retry timer', async ($, on) => {
  const f = memberWorld(on, 'member', (argv) => (argv[1] === 'hello' ? { exitCode: 20, stderr: 'unreachable' } : undefined))
  await start($, f)
  await f.clock.advance(5_000)
  await $.turn.start({ text: 'work', turnId: 't1' })
  await complete($, 't1') // the turn end says hello again: it fails too
  await f.clock.advance(100)
  expect(calls(f, 'hello').length).toBe(2)
  await f.clock.advance(70_000) // 70 s: the timer of the first failure fired at 30 s; its next waits for 60 s more
  expect(calls(f, 'hello').length).toBe(3)
})

// A reload loses the mod's clock: the report then carries no --period (the daemon's own period stands in), and still reaches the daemon.
test('RT-4 T42: with no turn seen since the mod started the report has no period', async ($, on) => {
  const f = memberWorld(on, 'member', memberPdx())
  await start($, f)
  expect(await $.session.compact(compactEvent('auto'))).toEqual({ skip: expect.any(String) })
  await f.clock.advance(100)
  expect(compactCalls(f)).toEqual(['relay compacted --session sid-old --trigger idle --intercepted'])
})

test('RT-4 T42: a daemon that routes the intercept silent makes the mod drop the role: hello again, and the next idle compaction runs', async ($, on) => {
  let role = 'member'
  const f = memberWorld(on, 'member', (argv) => {
    if (argv[1] === 'compacted') return { exitCode: 0, stdout: '{"noticed":false,"route":"silent"}' }
    if (argv[1] === 'hello') return { exitCode: 0, stdout: HELLO(role) }
    return undefined
  })
  await start($, f)
  expect(await $.session.compact(compactEvent('auto'))).toEqual({ skip: expect.any(String) })
  const before = calls(f, 'hello').length
  role = 'none' // the daemon's answer to the hello that follows
  await f.clock.advance(300)
  expect(calls(f, 'hello').length).toBe(before + 1)
  const r2 = await $.session.compact(compactEvent('auto'))
  expect(r2.skip).toBeUndefined()
})

// member_auto off (spec 2026-10-24 §2-1, T4): a mod that heard idle_intercept 1 before the switch went off is intercepted once; the daemon routes that
// silent, the mod drops its role and says hello again, and this time the daemon answers role member WITHOUT idle_intercept. From then on idle
// compactions run as ordinary ones. (The 409 member_relay_is_leads and the +10-point ask wait after relay_unsupported are covered in relay.test.ts:
// "RT-2c: a begin refused member_relay_is_leads at ≥ 90 % still lets the member ask its lead" and "a 409 relay_unsupported also waits for +10 points".)
test('member_auto off: silent, then hello answers member without idle_intercept: the next idle compaction is not intercepted', async ($, on) => {
  let level = 1
  const f = memberWorld(on, 'member', (argv) => {
    if (argv[1] === 'compacted') return { exitCode: 0, stdout: '{"noticed":false,"route":"silent"}' }
    if (argv[1] === 'hello') return { exitCode: 0, stdout: HELLO('member', level) }
    return undefined
  })
  await start($, f)
  expect(await $.session.compact(compactEvent('auto'))).toEqual({ skip: expect.any(String) }) // the one the old hello still armed
  const before = calls(f, 'hello').length
  level = 0 // the daemon's answer to the hello that follows: still a member, no idle_intercept
  await f.clock.advance(300)
  expect(calls(f, 'hello').length).toBe(before + 1)
  const r2 = await $.session.compact(compactEvent('auto'))
  expect(r2.skip).toBeUndefined()
  await f.clock.advance(100)
  // Only the first report was an interception; the second compaction is reported (if at all) as an ordinary one.
  expect(compactCalls(f).filter((c: string) => c.includes('--intercepted')).length).toBe(1)
})
