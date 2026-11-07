const DIR = '/private/tmp/claude-501/-Users-wake-Workspace-wake-purdex/fbff896e-ea34-40fc-8452-c7f0c67787a2/scratchpad/append-spike'
let buf: string[] = []
let seq = 0
let n = 0
async function log($: any, kind: string, data: any) {
  buf.push(JSON.stringify({ n: ++seq, t: Date.now(), kind, data }))
  try { await $.fs.write(DIR + '/log4.jsonl', buf.join('\n') + '\n') } catch (_e) {}
}
const row = (text: string) => ({ message: { type: 'user', content: [{ type: 'text', text }] } })
export const register = (on: any) => {
  on('session.compact', async ($: any, e: any, next: any) => {
    await log($, 'compact.in', { trigger: e.trigger, agentId: e.agentId, nMsgs: e.messages.length })
    if (e.trigger === 'precompute' || e.agentId) return next(e)
    const r = await next(e)
    await log($, 'compact.out', { skip: r.skip, nMsgs: r.messages && r.messages.length, before: r.tokensBefore, after: r.tokensAfter })
    if (r.skip) return r
    const k = ++n
    // the delay the spec chose; the row tells the model to tag its text, so its text shows what it read
    const delay = 1000
    $.clock.after(delay, () => {
      void (async () => {
        try { await log($, 'append', { k, delay, r: (await $.session.append(row('Standing note ' + k + ' from the harness: from now on start every line of text you write with the exact tag [K' + k + '-D' + delay + '].'))).uuid }) } catch (err) { await log($, 'append.err', String(err)) }
      })()
    })
    return r
  })
  on('turn.complete', async ($: any, e: any, next: any) => {
    await log($, 'turn.complete', { reason: e.reason, agentId: e.agentId, trigger: e.trigger })
    let c: any = {}
    try { c = JSON.parse(await $.fs.read(DIR + '/ctl.json')) } catch (_e) {}
    if (!e.agentId && c.compactNow) {
      await $.fs.write(DIR + '/ctl.json', '{}')
      try { await $.session.compact({}) } catch (err) { await log($, 'plugin.compact.err', String(err)) }
    }
    return next(e)
  })
}
