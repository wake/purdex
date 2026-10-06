// Probe (M1): can a pdx peer message be consumed by session.receive, and can the
// mod then drive prompt.submit and /clear from a timer?
const MARK = '[pdx-relay:control]'
let phase = 'idle'

async function log($, msg) {
  const line = new Date().toISOString() + ' [' + phase + '] ' + msg + '\n'
  try {
    const path = (await $.session.cwd()) + '/probe.log'
    const old = await $.fs.read(path).catch(() => '')
    await $.fs.write(path, old + line)
  } catch {}
}

function later($, fn) {
  $.clock.after(100, () => fn().catch((err) => log($, 'deferred failed: ' + String(err))))
}

export function register(on) {
  on('session.start', async ($, e, next) => {
    await log($, 'loaded session=' + (await $.session.id()))
    return next(e)
  })

  on('session.receive', async ($, e, next) => {
    await log($, 'receive origin=' + JSON.stringify(e.origin) + ' agentId=' + (e.agentId || '-') + ' text=' + JSON.stringify(String(e.text).slice(0, 160)))
    if (!String(e.text).includes(MARK)) return next(e)
    phase = 'submitting'
    later($, async () => {
      await $.prompt.submit({ text: '這是探測。請只回覆一行：PROBE-SUBMIT-OK' })
      await log($, 'prompt.submit sent')
    })
    return { consumed: 'pdx-relay control (probe)' }
  })

  on('turn.complete', async ($, e, next) => {
    const r = await next(e)
    if (e.agentId) return r
    await log($, 'turn.complete')
    if (phase === 'submitting') {
      phase = 'clearing'
      later($, async () => {
        await $.command.run({ command: 'clear' })
        await log($, 'command.run(clear) returned')
      })
    }
    return r
  })

  on('classic.SessionStart', async ($, e, next) => {
    const r = await next(e)
    await log($, 'classic.SessionStart source=' + e.source + ' session=' + (await $.session.id()))
    if (e.source === 'clear' && phase === 'clearing') phase = 'done'
    return r
  })
}
