// hooks/ask.test.ts — 分流 for AskUserQuestion (lead-team spec §6.6, §15 "Mod (U19)"), run by
// `claude plugin test cmd/pdx/plugin/purdex`. The test's `on` hooks stand beneath the plugin as
// the engine: `tool.call` is the native dialog, `process.run` is `pdx`, `session.id` /
// `session.surfaces` are the session, `clock.sleep` is real time (the mod's settle cap is
// measured in real time here, as a person waits for it).
//
// The mod runs in every interactive session on the host, so the tests below the plan's own
// pin one rule above all: whatever fails or is slow on our side, the native dialog's answer
// comes back unchanged, and within a few seconds of the person answering.
import { test, expect } from 'claude-code/testing'

const Q = [{ question: '紅還是藍？', header: '顏色', options: [{ label: '紅', description: 'r' }, { label: '藍', description: 'b' }], multiSelect: false }]
const NATIVE_RED = { ref: 1, result: { questions: Q, answers: { '紅還是藍？': '紅' }, annotations: {} }, text: 'Your questions have been answered', isReadOnly: true }
const ok = (stdout: string, exitCode = 0, stderr = '') => ({ value: { exitCode, stdout, stderr, isStdoutTruncated: false, isStderrTruncated: false } })
const never = () => new Promise<never>(() => {})
const PDX_JSON_CONFIG = '{"pdx":"/opt/pdx/bin/pdx","data_dir":"/tmp/pdx","config":"/tmp/pdx b/config.toml"}'

type Call = string[]
type Resolver = ((v: unknown) => void) | null
// session stands in for the engine: the id, the surfaces, pdx.json (absent by default, as
// under `claude plugin test` on a dev machine), a real-time clock.sleep and ui.log. `fail`
// names a session call that throws instead (the mod's own failure).
function session(on: any, surfaces: string[] = ['terminal'], pdxJSON?: string, fail?: 'session.id' | 'session.surfaces') {
  const refuse = () => { throw new Error('the engine refused ' + fail) }
  on('session.id', fail === 'session.id' ? refuse : () => ({ value: 'sess-test' }))
  on('session.surfaces', fail === 'session.surfaces' ? refuse : () => ({ value: surfaces }))
  on('fs.read', (_$: any, e: any) => (pdxJSON !== undefined && e.path.endsWith('/pdx.json') ? { value: pdxJSON } : { deny: 'ENOENT' }))
  on('clock.sleep', async (_$: any, e: any) => { await new Promise((r) => setTimeout(r, e.ms)); return { value: undefined } })
  const logs: { text: string; to: string }[] = []
  on('ui.log', (_$: any, e: any) => { logs.push({ text: e.text, to: e.to }); return { value: undefined } })
  return logs
}
const sub = (a: readonly string[]) => a[2] // <pdx> ask <sub> …

test('no_responders ⇒ the native dialog runs alone: next(e) once, no further pdx call', async ($, on) => {
  const logs = session(on)
  const calls: Call[] = []
  let nextCalls = 0
  let answerNative: Resolver = null
  on('process.run', (_$: any, e: any) => {
    calls.push([...e.argv])
    // The person answers only after the daemon has said no_responders: the branch under test is the one that runs.
    setTimeout(() => answerNative && answerNative(NATIVE_RED), 50)
    // P8a-1c's stderr shape: `pdx ask: <detail> <code>`, the code last.
    return ok('', 13, 'pdx ask: 沒有連線中的客戶端可以回答 no_responders\n')
  })
  on('tool.call', { tool: 'AskUserQuestion' }, () => { nextCalls++; return new Promise((res) => { answerNative = res }) })
  const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q })
  expect(r.result).toEqual(NATIVE_RED.result)
  expect(nextCalls).toBe(1)
  expect(calls.map(sub)).toEqual(['begin'])
  expect(calls[0].slice(0, 3)).toEqual(['pdx', 'ask', 'begin']) // no pdx.json ⇒ `pdx` from PATH
  expect(calls[0]).toContain('--session'); expect(calls[0]).toContain('sess-test')
  expect(calls[0]).toContain('--kind'); expect(calls[0][calls[0].indexOf('--kind') + 1]).toBe('hook_ask')
  expect(calls[0][calls[0].indexOf('--tool-use') + 1]).toMatch(/.+/)
  expect(calls[0]).not.toContain('--config') // no pdx.json ⇒ pdx's own default config
  expect(JSON.parse(calls[0][calls[0].indexOf('--payload') + 1])).toEqual({ questions: Q })
  // the code is stderr's last token; the line goes to the debug log, never the transcript
  expect(logs).toEqual([{ text: 'pdx-ask: begin exit 13 no_responders: native dialog only', to: 'debug' }])
})

test('a subagent\'s AskUserQuestion (agentId set) is not relayed: plain next(e), no pdx at all', async ($, on) => {
  session(on)
  const calls: Call[] = []
  let nextCalls = 0
  on('process.run', (_$: any, e: any) => { calls.push([...e.argv]); return ok('{"id":"x"}') })
  on('tool.call', { tool: 'AskUserQuestion' }, () => { nextCalls++; return NATIVE_RED })
  const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q, agentId: 'agent-7' } as any)
  expect(r.result).toEqual(NATIVE_RED.result)
  expect(nextCalls).toBe(1)
  expect(calls).toEqual([])
})

test('pdx is run by the path in pdx.json when the file exists (the installed binary is not on PATH)', async ($, on) => {
  session(on, ['terminal'], '{"pdx":"/opt/pdx/bin/pdx","data_dir":"/tmp/pdx"}')
  const calls: Call[] = []
  on('process.run', (_$: any, e: any) => {
    calls.push([...e.argv])
    if (sub(e.argv) === 'begin') return ok('{"id":"r7"}')
    return ok('{"state":"answered_remote","hook":{"answers":{"紅還是藍？":"藍"}}}')
  })
  on('tool.call', { tool: 'AskUserQuestion' }, never)
  const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q })
  expect(r.result).toEqual({ questions: Q, answers: { '紅還是藍？': '藍' } })
  expect(calls.map((a) => a[0])).toEqual(['/opt/pdx/bin/pdx', '/opt/pdx/bin/pdx'])
  expect(calls.map(sub)).toEqual(['begin', 'wait'])
  for (const a of calls) expect(a).not.toContain('--config') // a pdx.json without config adds none
})

// P5b-1 review: the mod reaches the daemon that installed it. Mutation gate: drop --config
// from any one call → red.
test('with a config in pdx.json every pdx ask call carries --config <path>: begin, wait and report', async ($, on) => {
  session(on, ['terminal'], PDX_JSON_CONFIG)
  const calls: Call[] = []
  let answerNative: Resolver = null
  const reported = new Promise<void>((resolve) => {
    on('process.run', (_$: any, e: any) => {
      calls.push([...e.argv])
      const a = e.argv
      if (sub(a) === 'begin') return ok('{"id":"r8"}')
      if (sub(a) === 'wait') { setTimeout(() => answerNative && answerNative(NATIVE_RED), 20); return never() }
      if (sub(a) === 'report') { resolve(); return ok('{}') }
      return ok('')
    })
  })
  on('tool.call', { tool: 'AskUserQuestion' }, () => new Promise((res) => { answerNative = res }))
  const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q })
  expect(r.result).toEqual(NATIVE_RED.result)
  await reported
  expect(calls.map(sub)).toEqual(['begin', 'wait', 'report'])
  for (const a of calls) {
    expect(a[0]).toBe('/opt/pdx/bin/pdx')
    expect(a.slice(-2)).toEqual(['--config', '/tmp/pdx b/config.toml'])
  }
})

test('headless (no surface) ⇒ plain next(e), no pdx at all', async ($, on) => {
  session(on, [])
  const calls: Call[] = []
  let nextCalls = 0
  on('process.run', (_$: any, e: any) => { calls.push([...e.argv]); return ok('{"id":"x"}') })
  on('tool.call', { tool: 'AskUserQuestion' }, () => { nextCalls++; return NATIVE_RED })
  const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q })
  expect(r.result).toEqual(NATIVE_RED.result)
  expect(nextCalls).toBe(1)
  expect(calls).toEqual([])
})

// Mutation gate 4: (a) build `{ result }` from the native answers instead of returning the
// native object → red (ref / isReadOnly missing); (b) drop the report → red on `await reported`.
// A healthy daemon's report lands before the answer goes back (`landed`): the mod waits for it
// up to its settle cap, so the report is sent while the dispatch is still alive. Mutation gate:
// never wait for the report (fire and forget) → `landed` is false → red.
test('terminal first ⇒ the native result is returned unchanged and answered_local is reported with its answers', async ($, on) => {
  session(on)
  let releaseWait: Resolver = null
  let answerNative: Resolver = null
  let landed = false
  const reported = new Promise<Call>((resolve) => {
    on('process.run', (_$: any, e: any) => {
      const a = e.argv
      if (sub(a) === 'begin') return ok('{"id":"r2"}\n')
      // The person answers while the first wait round is in flight (the race proper, not the early branch).
      if (sub(a) === 'wait') { setTimeout(() => answerNative && answerNative(NATIVE_RED), 20); return new Promise((res) => { releaseWait = res }) }
      // A healthy daemon: the report takes 100 ms and lands; the answer goes back after it.
      if (sub(a) === 'report') { resolve([...a]); return new Promise((res) => setTimeout(() => { landed = true; res(ok('{}')) }, 100)) }
      return ok('')
    })
  })
  on('tool.call', { tool: 'AskUserQuestion' }, () => new Promise((res) => { answerNative = res }))
  const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q })
  expect(r).toEqual(expect.objectContaining({ result: NATIVE_RED.result, isReadOnly: true, ref: 1 }))
  expect(landed).toBe(true)
  const rep = await reported
  expect(rep.slice(0, 5)).toEqual(['pdx', 'ask', 'report', 'r2', 'answered_local'])
  expect(rep[5]).toBe('--hook')
  expect(JSON.parse(rep[6])).toEqual({ answers: { '紅還是藍？': '紅' } })
  if (releaseWait) releaseWait(ok('{"state":"closed","reason":"answered_local"}'))
})

// Review Focus 5 / spec §6.6 step 5 (terminal_override), the interleaving proper: the phone's
// answer reaches the mod a beat AFTER the terminal's. The fake pdx holds `wait` until the native
// promise has resolved and only then answers answered_remote 藍. The mod must return the native
// result (紅) and report answered_local; the late remote answer is ignored (the daemon, which saw
// the remote decide win its CAS first, records terminal_override — the terminal still stands).
// Mutation gate 8: consult `remote` again after the race (prefer a remote answer that arrived
// after the native one) and drop the loop's `if (stopped) break` → `answers` is 藍 and no
// answered_local report → red. Either guard alone keeps the terminal's answer: the race takes
// the native result first, and the loop discards a round that returns after the race.
test('interleaving: answered_remote arrives after the terminal already answered ⇒ native result, answered_local reported, the late remote ignored', async ($, on) => {
  session(on)
  let answerNative: Resolver = null
  let nativeDone: Promise<unknown> = Promise.resolve()
  const reported = new Promise<Call>((resolve) => {
    on('process.run', (_$: any, e: any) => {
      const a = e.argv
      if (sub(a) === 'begin') return ok('{"id":"r5"}')
      // answered_remote only once the native promise has resolved — never before.
      if (sub(a) === 'wait') return nativeDone.then(() => ok('{"state":"answered_remote","hook":{"answers":{"紅還是藍？":"藍"}}}'))
      if (sub(a) === 'report') { resolve([...a]); return ok('{}') }
      return ok('')
    })
  })
  nativeDone = new Promise((done) => {
    on('tool.call', { tool: 'AskUserQuestion' }, () => new Promise((res) => {
      answerNative = (v) => { res(v); done(v) }
      // the person answers 20 ms after the dialog is up (a timer started before the plugins
      // load could fire before there is a dialog to answer, and the test would hang)
      setTimeout(() => answerNative && answerNative(NATIVE_RED), 20)
    }))
  })
  const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q })
  expect(r).toEqual(expect.objectContaining({ result: NATIVE_RED.result, isReadOnly: true, ref: 1 }))
  expect(r.result.answers).toEqual({ '紅還是藍？': '紅' }) // the terminal's, not the phone's
  const rep = await reported
  expect(rep.slice(2, 5)).toEqual(['report', 'r5', 'answered_local'])
  expect(JSON.parse(rep[6])).toEqual({ answers: { '紅還是藍？': '紅' } })
})

test('the person answers before begin has even returned ⇒ native result, and the row begin opened is reported', { timeoutMs: 15000 }, async ($, on) => {
  session(on)
  let answerBegin: Resolver = null
  const reported = new Promise<Call>((resolve) => {
    on('process.run', (_$: any, e: any) => {
      const a = e.argv
      if (sub(a) === 'begin') return new Promise((res) => { answerBegin = res })
      if (sub(a) === 'report') { resolve([...a]); return ok('{}') }
      return ok('')
    })
  })
  on('tool.call', { tool: 'AskUserQuestion' }, () => NATIVE_RED)
  const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q })
  expect(r).toEqual(expect.objectContaining({ result: NATIVE_RED.result, isReadOnly: true }))
  if (answerBegin) answerBegin(ok('{"id":"r9"}'))
  expect((await reported).slice(2, 5)).toEqual(['report', 'r9', 'answered_local'])
})

test('remote first ⇒ { result: { questions, answers } } in the shape M24 measured; the native dialog is left pending', async ($, on) => {
  session(on)
  const calls: Call[] = []
  on('process.run', (_$: any, e: any) => {
    calls.push([...e.argv])
    const a = e.argv
    if (sub(a) === 'begin') return ok('{"id":"r1"}\n')
    if (sub(a) === 'wait') return ok('{"state":"answered_remote","hook":{"answers":{"紅還是藍？":"藍"}}}\n')
    return ok('')
  })
  on('tool.call', { tool: 'AskUserQuestion' }, never)
  const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q })
  expect(r.result).toEqual({ questions: Q, answers: { '紅還是藍？': '藍' } })
  expect(calls.map(sub)).toEqual(['begin', 'wait']) // the daemon closed the row itself: nothing to report
})

// Mutation gate 5: replace `continue` with a return → red (`expected 12, received 1`).
test('still_open loops without returning: N rounds, then the remote answer', async ($, on) => {
  session(on)
  let waits = 0
  on('process.run', (_$: any, e: any) => {
    const a = e.argv
    if (sub(a) === 'begin') return ok('{"id":"r1"}')
    if (sub(a) === 'wait') { waits++; return ok(waits < 12 ? '{"state":"still_open"}' : '{"state":"answered_remote","hook":{"answers":{"紅還是藍？":"藍"}}}') }
    return ok('')
  })
  on('tool.call', { tool: 'AskUserQuestion' }, never)
  const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q })
  expect(r.result).toEqual({ questions: Q, answers: { '紅還是藍？': '藍' } })
  expect(waits).toBe(12)
})

test('a hold across several wait rounds keeps the dialog open: the terminal answers after 3 rounds and wins', async ($, on) => {
  session(on)
  let waits = 0
  let answerNative: Resolver = null
  const reported = new Promise<Call>((resolve) => {
    on('process.run', (_$: any, e: any) => {
      const a = e.argv
      if (sub(a) === 'begin') return ok('{"id":"r3"}')
      if (sub(a) === 'wait') {
        waits++
        if (waits === 3 && answerNative) answerNative(NATIVE_RED) // the person answers during the third round
        return waits <= 3 ? ok('{"state":"still_open"}') : new Promise(() => {})
      }
      if (sub(a) === 'report') { resolve([...a]); return ok('{}') }
      return ok('')
    })
  })
  on('tool.call', { tool: 'AskUserQuestion' }, () => new Promise((res) => { answerNative = res }))
  const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q })
  expect(r).toEqual(expect.objectContaining({ result: NATIVE_RED.result, isReadOnly: true }))
  expect((await reported)[4]).toBe('answered_local')
  expect(waits).toBeGreaterThanOrEqual(3)
})

test('Esc (the native dialog settles without answers) ⇒ dismissed is reported and the native outcome is returned', async ($, on) => {
  session(on)
  const DECLINED = { ref: 2, result: { questions: Q, answers: {} }, text: 'User declined to answer questions', isError: true }
  let answerNative: Resolver = null
  const reported = new Promise<Call>((resolve) => {
    on('process.run', (_$: any, e: any) => {
      const a = e.argv
      if (sub(a) === 'begin') return ok('{"id":"r4"}')
      if (sub(a) === 'wait') { setTimeout(() => answerNative && answerNative(DECLINED), 20); return new Promise(() => {}) }
      if (sub(a) === 'report') { resolve([...a]); return ok('{}') }
      return ok('')
    })
  })
  on('tool.call', { tool: 'AskUserQuestion' }, () => new Promise((res) => { answerNative = res }))
  const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q })
  expect(r).toEqual(expect.objectContaining({ isError: true, text: 'User declined to answer questions' }))
  const rep = await reported
  expect(rep.slice(2, 5)).toEqual(['report', 'r4', 'dismissed'])
  expect(rep).not.toContain('--hook')
})

// The native call failing (next(e) rejects): the row is reported dismissed and the failure
// reaches the engine as it settled — replayed by the .catch, the dialog never run twice.
test('the native dialog failing (next(e) rejects) ⇒ dismissed is reported and the call fails as the native one did, run once', async ($, on) => {
  session(on)
  let nextCalls = 0
  const reported = new Promise<Call>((resolve) => {
    on('process.run', (_$: any, e: any) => {
      const a = e.argv
      if (sub(a) === 'begin') return ok('{"id":"r12"}')
      if (sub(a) === 'wait') return never()
      if (sub(a) === 'report') { resolve([...a]); return ok('{}') }
      return ok('')
    })
  })
  on('tool.call', { tool: 'AskUserQuestion' }, () => { nextCalls++; return new Promise((_res, rej) => setTimeout(() => rej(new Error('the dialog failed')), 30)) })
  const r: any = await $.tool.call({ tool: 'AskUserQuestion', questions: Q }).then((v: any) => ({ value: v }), (err: any) => ({ error: String(err) }))
  expect(r.value).toBeUndefined()
  expect(r.error).toEqual(expect.any(String))
  expect(nextCalls).toBe(1)
  const rep = await reported
  expect(rep.slice(2, 5)).toEqual(['report', 'r12', 'dismissed'])
})

// Mutation gate 3: ignore begin's exit code (`|| { id: 'x' }`) → this test and the
// `begin … ⇒ the native dialog runs alone` ones go red (`calls.map(sub)` gains `wait`).
test('begin that fails (daemon down, exit 20) ⇒ the native dialog runs alone and nothing more is called', async ($, on) => {
  session(on)
  const calls: Call[] = []
  let answerNative: Resolver = null
  on('process.run', (_$: any, e: any) => {
    calls.push([...e.argv])
    setTimeout(() => answerNative && answerNative(NATIVE_RED), 50)
    return ok('', 20, 'pdx ask: 等了 30 秒 daemon 仍沒有回應 daemon_unavailable\n')
  })
  on('tool.call', { tool: 'AskUserQuestion' }, () => new Promise((res) => { answerNative = res }))
  const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q })
  expect(r.result).toEqual(NATIVE_RED.result)
  expect(calls.map(sub)).toEqual(['begin'])
})

// ---------- fail-open: every other way begin, wait or the mod itself can fail ----------

// Every begin answer that is not exit 0 with an id: the native dialog runs alone, nothing more.
for (const [name, answer] of [
  ['exits 21 (a daemon without /api/ask)', ok('', 21, 'pdx ask: 這個 daemon 沒有 /api/ask 路由，請先更新 daemon unsupported\n')],
  ['exits 1 (a response without an id)', ok('', 1, 'pdx ask: daemon 回應缺少 id invalid_response\n')],
  ['exits 2 (usage: an argv this pdx does not take)', ok('', 2, 'pdx ask: unknown flag\n')],
  ['exits 0 with no id', ok('{}')],
  ['exits 0 with a body that is not JSON', ok('not json')],
  ['is refused by the host (the call rejects)', 'reject'],
] as const) {
  test(`begin that ${name} ⇒ the native dialog runs alone and nothing more is called`, async ($, on) => {
    session(on)
    const calls: Call[] = []
    let answerNative: Resolver = null
    on('process.run', (_$: any, e: any) => {
      calls.push([...e.argv])
      setTimeout(() => answerNative && answerNative(NATIVE_RED), 50)
      if (answer === 'reject') throw new Error('spawn pdx ENOENT')
      return answer
    })
    on('tool.call', { tool: 'AskUserQuestion' }, () => new Promise((res) => { answerNative = res }))
    const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q })
    expect(r).toEqual(expect.objectContaining({ result: NATIVE_RED.result, isReadOnly: true, ref: 1 }))
    expect(calls.map(sub)).toEqual(['begin'])
  })
}

// A wait that fails (or answers something the mod cannot act on) ends the race: the native
// dialog runs on alone, no second wait (a body the mod cannot read never makes it spin), and
// the person's answer is still reported so the cards show it.
for (const [name, answer] of [
  ['exits 1 (a state the CLI does not know)', ok('', 1, 'pdx ask: daemon 回應的 state 無法辨識 invalid_response\n')],
  ['exits 20 (the daemon went away mid-hold)', ok('', 20, 'pdx ask: daemon 沒有回應\n')],
  ['exits 0 with a body that is not JSON', ok('not json')],
  ['exits 0 with a state the mod does not know', ok('{"state":"weird"}')],
  ['exits 0 with answered_remote but no answers', ok('{"state":"answered_remote","hook":{}}')],
  ['is refused by the host (the call rejects)', 'reject'],
] as const) {
  test(`wait that ${name} ⇒ the native dialog runs on alone, no second wait, its answer reported`, async ($, on) => {
    session(on)
    const calls: Call[] = []
    let answerNative: Resolver = null
    const reported = new Promise<Call>((resolve) => {
      on('process.run', (_$: any, e: any) => {
        calls.push([...e.argv])
        const a = e.argv
        if (sub(a) === 'begin') return ok('{"id":"r10"}')
        if (sub(a) === 'wait') {
          setTimeout(() => answerNative && answerNative(NATIVE_RED), 50) // the person answers after the wait failed
          if (answer === 'reject') throw new Error('the child was killed')
          return answer
        }
        if (sub(a) === 'report') { resolve([...a]); return ok('{}') }
        return ok('')
      })
    })
    on('tool.call', { tool: 'AskUserQuestion' }, () => new Promise((res) => { answerNative = res }))
    const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q })
    expect(r).toEqual(expect.objectContaining({ result: NATIVE_RED.result, isReadOnly: true, ref: 1 }))
    expect((await reported).slice(2, 5)).toEqual(['report', 'r10', 'answered_local'])
    expect(calls.map(sub)).toEqual(['begin', 'wait', 'report'])
  })
}

test('a row closed another way (abandoned) ⇒ the native dialog runs on alone, nothing reported', async ($, on) => {
  session(on)
  const calls: Call[] = []
  let answerNative: Resolver = null
  on('process.run', (_$: any, e: any) => {
    calls.push([...e.argv])
    const a = e.argv
    if (sub(a) === 'begin') return ok('{"id":"r11"}')
    if (sub(a) === 'wait') { setTimeout(() => answerNative && answerNative(NATIVE_RED), 50); return ok('{"state":"closed","reason":"abandoned"}') }
    return ok('{}')
  })
  on('tool.call', { tool: 'AskUserQuestion' }, () => new Promise((res) => { answerNative = res }))
  const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q })
  expect(r).toEqual(expect.objectContaining({ result: NATIVE_RED.result, isReadOnly: true, ref: 1 }))
  expect(calls.map(sub)).toEqual(['begin', 'wait'])
})

// The latency bound: a daemon that does not answer must never hold the person's answer.
// `pdx ask begin` against a daemon that is down answers exit 20 only after the client's 30 s
// grace; here it never answers inside the test. The native answer comes back within the mod's
// settle cap (3 s), measured in real time. Mutation gate: wait for begin without the cap → red.
test('a daemon that does not answer begin (down: exit 20 only after its 30 s grace) never holds the terminal answer: back within 5 s', { timeoutMs: 15000 }, async ($, on) => {
  session(on)
  const calls: Call[] = []
  let answerNative: Resolver = null
  on('process.run', (_$: any, e: any) => {
    calls.push([...e.argv])
    setTimeout(() => answerNative && answerNative(NATIVE_RED), 50)
    return never()
  })
  on('tool.call', { tool: 'AskUserQuestion' }, () => new Promise((res) => { answerNative = res }))
  const t0 = Date.now()
  const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q })
  const ms = Date.now() - t0
  expect(r).toEqual(expect.objectContaining({ result: NATIVE_RED.result, isReadOnly: true, ref: 1 }))
  expect(ms).toBeLessThan(5000)
  expect(calls.map(sub)).toEqual(['begin'])
})

// The same bound for the report: the row is open, the person answers, and the daemon then stops
// answering. The report is sent (its call is out before the answer goes back), but the answer is
// back within 5 s. Mutation gate: wait for the report without the cap (an unbounded wait) → red.
test('a daemon that does not answer the report never holds the terminal answer: back within 5 s, the report sent', { timeoutMs: 15000 }, async ($, on) => {
  session(on)
  let answerNative: Resolver = null
  let report: Call | null = null
  on('process.run', (_$: any, e: any) => {
    const a = e.argv
    if (sub(a) === 'begin') return ok('{"id":"r6"}')
    if (sub(a) === 'wait') { setTimeout(() => answerNative && answerNative(NATIVE_RED), 20); return never() }
    if (sub(a) === 'report') { report = [...a]; return never() }
    return ok('')
  })
  on('tool.call', { tool: 'AskUserQuestion' }, () => new Promise((res) => { answerNative = res }))
  const t0 = Date.now()
  const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q })
  const ms = Date.now() - t0
  expect(r).toEqual(expect.objectContaining({ result: NATIVE_RED.result, isReadOnly: true, ref: 1 }))
  expect(ms).toBeLessThan(5000)
  expect(report).not.toBeNull()
  expect(report!.slice(2, 5)).toEqual(['report', 'r6', 'answered_local'])
})

// The mod's own failure: its `.catch` answers next(e), which replays the native call as it
// settled — the dialog is never drawn twice.
test('the mod failing after the dialog is up (session.id throws) ⇒ the native answer, the dialog drawn once', async ($, on) => {
  session(on, ['terminal'], undefined, 'session.id')
  const calls: Call[] = []
  let nextCalls = 0
  on('process.run', (_$: any, e: any) => { calls.push([...e.argv]); return ok('{"id":"x"}') })
  on('tool.call', { tool: 'AskUserQuestion' }, () => { nextCalls++; return new Promise((res) => setTimeout(() => res(NATIVE_RED), 30)) })
  const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q })
  expect(r).toEqual(expect.objectContaining({ result: NATIVE_RED.result, isReadOnly: true, ref: 1 }))
  expect(nextCalls).toBe(1)
  expect(calls).toEqual([])
})

test('the mod failing before the dialog is up (session.surfaces throws) ⇒ the native dialog runs once, as without the mod', async ($, on) => {
  session(on, ['terminal'], undefined, 'session.surfaces')
  const calls: Call[] = []
  let nextCalls = 0
  on('process.run', (_$: any, e: any) => { calls.push([...e.argv]); return ok('{"id":"x"}') })
  on('tool.call', { tool: 'AskUserQuestion' }, () => { nextCalls++; return NATIVE_RED })
  const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q })
  expect(r).toEqual(expect.objectContaining({ result: NATIVE_RED.result, isReadOnly: true, ref: 1 }))
  expect(nextCalls).toBe(1)
  expect(calls).toEqual([])
})
