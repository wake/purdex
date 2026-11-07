// 探針用的觸發 mod（壓縮控制 spec 探針 1–3、Phase 1 驗收）。用 `claude --plugin-dir <這個資料夾>` 載入，跟 Purdex mod 並存。
// 回合結束時讀 DIR/ctl.json：{"mode":"timer"} 用 $.clock.after(0) 壓縮、"await" 在 hook 裡壓縮、"submit-during" 另外在 3 秒後
// 用 $.prompt.submit 送一句、"skip-plugin" 讓這次壓縮被 skip；讀完就清成 {}。事件時間記在 DIR/log.jsonl。
// 用之前把 DIR 改成工作目錄的絕對路徑。
const DIR = '/ABSOLUTE/PATH/TO/WORK/DIR'
let buf: string[] = []
async function log($: any, kind: string, data: any) {
  buf.push(JSON.stringify({ t: await $.clock.now(), kind, data }))
  try { await $.fs.write(DIR + '/log.jsonl', buf.join('\n') + '\n') } catch (_e) {}
}
const ctx = async ($: any, tag: string) => { try { const u = await $.session.usage(); await log($, tag, u.context) } catch (e) { await log($, tag + '.err', String(e)) } }
async function doCompact($: any, why: string) {
  await log($, 'compact.call', why)
  const t0 = await $.clock.now()
  try { const r = await $.session.compact({}); await log($, 'compact.result', { ms: (await $.clock.now()) - t0, skip: r.skip, n: r.messages && r.messages.length, before: r.tokensBefore, after: r.tokensAfter }) }
  catch (err) { await log($, 'compact.reject', { ms: (await $.clock.now()) - t0, msg: String(err && (err as any).message || err), full: String(err) }) }
  await ctx($, 'usage.after-compact')
}
export const register = (on: any) => {
  on('prompt.submit', async ($: any, e: any, next: any) => { await log($, 'prompt.submit', { text: JSON.stringify(e).slice(0, 300), asUser: e && e.asUser }); const r = await next(e); await log($, 'prompt.submit.next-returned', {}); return r })
  on('turn.start', async ($: any, e: any, next: any) => { await log($, 'turn.start', { turnId: e.turnId }); return next(e) })
  on('session.measure', async ($: any, e: any, next: any) => { await log($, 'session.measure', e.context); return next(e) })
  on('session.compact', async ($: any, e: any, next: any) => {
    await log($, 'compact.enter', { trigger: e.trigger, agentId: e.agentId })
    if (e.trigger === 'plugin' && !e.agentId) {
      let c: any = {}
      try { c = JSON.parse(await $.fs.read(DIR + '/ctl-skip.json')) } catch (_e) {}
      if (c.mode === 'skip-plugin') { await log($, 'compact.skip-returned', {}); return { skip: 'probe' } }
    }
    const r = await next(e)
    await log($, 'compact.next-done', { skip: r.skip, before: r.tokensBefore, after: r.tokensAfter })
    return r
  })
  on('turn.complete', async ($: any, e: any, next: any) => {
    await log($, 'turn.complete', { reason: e.reason, turnId: e.turnId, agentId: e.agentId })
    await ctx($, 'usage.in-turn-complete')
    if (e.agentId) return next(e)
    let c: any = {}
    try { c = JSON.parse(await $.fs.read(DIR + '/ctl.json')) } catch (_e) {}
    if (!c.mode) return next(e)
    await $.fs.write(DIR + '/ctl.json', '{}')
    await log($, 'ctl', c)
    if (c.mode === 'await') { const r = await next(e); await log($, 'next-returned', {}); await doCompact($, 'await'); return r }
    $.clock.after(0, () => { void doCompact($, c.mode) })
    if (c.mode === 'submit-during') $.clock.after(3000, () => { void (async () => {
      await log($, 'submit.call', {}); const t0 = await $.clock.now()
      try { const r = await $.prompt.submit({ text: 'PROGSUBMIT_7731 reply with the single word ok', asUser: true }); await log($, 'submit.result', { ms: (await $.clock.now()) - t0, r }) }
      catch (err) { await log($, 'submit.reject', { ms: (await $.clock.now()) - t0, full: String(err) }) } })() })
    if (c.mode === 'skip-plugin') { await $.fs.write(DIR + '/ctl-skip.json', '{"mode":"skip-plugin"}') }
    return next(e)
  })
}
