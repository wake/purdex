// Probe 2: does an idle session's peer delivery pass through prompt.submit,
// and can a prompt.submit hook hold the turn past 10 s inside a $ call?
let n = 0

async function log($, msg) {
  const line = new Date().toISOString() + ' ' + msg + '\n'
  try {
    const path = (await $.session.cwd()) + '/probe.log'
    const old = await $.fs.read(path).catch(() => '')
    await $.fs.write(path, old + line)
  } catch {}
}

export function register(on) {
  on('session.start', async ($, e, next) => {
    await log($, 'loaded session=' + (await $.session.id()))
    return next(e)
  })

  on('session.receive', async ($, e, next) => {
    await log($, 'receive origin=' + JSON.stringify(e.origin) + ' text=' + JSON.stringify(String(e.text).slice(-60)))
    return next(e)
  })

  on('prompt.submit', async ($, e, next) => {
    const id = ++n
    await log($, 'submit#' + id + ' origin=' + JSON.stringify(e.origin || null) + ' agentId=' + (e.agentId || '-') + ' text=' + JSON.stringify(String(e.text).slice(-60)))
    if (String(e.text).includes('HOLD15')) {
      await log($, 'submit#' + id + ' holding 15s via $.process.run')
      const r = await $.process.run(['/bin/sleep', '15'], { timeoutMs: 60000 })
      await log($, 'submit#' + id + ' released exit=' + r.exitCode)
    }
    return next(e)
  })

  on('turn.start', async ($, e, next) => {
    if (!e.agentId) await log($, 'turn.start')
    return next(e)
  })
}
