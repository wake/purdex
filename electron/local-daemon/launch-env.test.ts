import { describe, expect, it, vi } from 'vitest'
import { buildShellProbeScript, extractBetweenSentinels, resolveShellPath, fallbackPath, buildLaunchEnv, type ExecFn } from './launch-env'

const S = 'PDX_PATH_0123456789abcdef0123456789abcdef'

describe('buildShellProbeScript', () => {
  it('contains no NUL and frames PATH with the sentinel', () => {
    const script = buildShellProbeScript(S)
    expect(script).not.toContain('\0')
    expect(script).toBe(`printf '\\n%s%s%s\\n' '${S}' "$PATH" '${S}'`)
  })
})

describe('extractBetweenSentinels', () => {
  it('ignores banners before and after', () => {
    const out = `Welcome!\nsome plugin noise\n${S}/opt/homebrew/bin:/usr/bin${S}\nbye\n`
    expect(extractBetweenSentinels(out, S)).toBe('/opt/homebrew/bin:/usr/bin')
  })
  it('returns null when a sentinel is missing or the value is empty', () => {
    expect(extractBetweenSentinels(`${S}/x`, S)).toBeNull()
    expect(extractBetweenSentinels(`${S}${S}`, S)).toBeNull()
    expect(extractBetweenSentinels('nothing', S)).toBeNull()
  })
})

describe('resolveShellPath', () => {
  it('uses -ilc first and never puts NUL in argv', async () => {
    const exec: ExecFn = vi.fn(async (_f, args) => {
      for (const a of args) expect(a).not.toContain('\0')
      return { code: 0, stdout: `${S}/a:/b${S}\n`, stderr: '', timedOut: false }
    })
    expect(await resolveShellPath(exec, '/bin/zsh', S)).toEqual({ path: '/a:/b' })
    expect(vi.mocked(exec).mock.calls[0][1][0]).toBe('-ilc')
  })
  it('falls back to -lc when the interactive probe yields nothing', async () => {
    const exec: ExecFn = vi.fn(async (_f, args) => {
      if (args[0] === '-ilc') return { code: 0, stdout: 'banner only\n', stderr: '', timedOut: false }
      return { code: 0, stdout: `${S}/login/bin${S}\n`, stderr: '', timedOut: false }
    })
    expect(await resolveShellPath(exec, '/bin/zsh', S)).toEqual({ path: '/login/bin' })
    expect(vi.mocked(exec).mock.calls.map((c) => c[1][0])).toEqual(['-ilc', '-lc'])
  })
  it('returns null plus a readable error when both probes time out', async () => {
    const exec: ExecFn = async () => ({ code: null, stdout: '', stderr: '', timedOut: true })
    const r = await resolveShellPath(exec, '/bin/zsh', S)
    expect(r.path).toBeNull()
    expect(r.error).toMatch(/-ilc/)
    expect(r.error).toMatch(/-lc/)
    expect(r.error).toMatch(/timed out/)
  })
  it('reports a thrown spawn error rather than swallowing it', async () => {
    const exec: ExecFn = async () => { throw new Error('ENOENT /bin/fish') }
    const r = await resolveShellPath(exec, '/bin/fish', S)
    expect(r.path).toBeNull()
    expect(r.error).toMatch(/ENOENT \/bin\/fish/)
  })
})

describe('fallbackPath', () => {
  it('prefixes brew and ~/.local/bin without a literal tilde', () => {
    expect(fallbackPath('/usr/bin:/bin', '/Users/x')).toBe('/opt/homebrew/bin:/usr/local/bin:/Users/x/.local/bin:/usr/bin:/bin')
    expect(fallbackPath(undefined, '/Users/x')).toBe('/opt/homebrew/bin:/usr/local/bin:/Users/x/.local/bin')
  })
})

describe('buildLaunchEnv', () => {
  it('sets PATH from the shell, PDX_DEV_MODE=1, and reports pathSource "shell"', async () => {
    const exec: ExecFn = async () => ({ code: 0, stdout: `${S}/shell/bin${S}\n`, stderr: '', timedOut: false })
    const r = await buildLaunchEnv({ exec, shell: '/bin/zsh', baseEnv: { HOME: '/Users/x', PATH: '/usr/bin' }, home: '/Users/x', sentinel: () => S })
    expect(r.env.PATH).toBe('/shell/bin')
    expect(r.env.PDX_DEV_MODE).toBe('1')
    expect(r.env.HOME).toBe('/Users/x')
    expect(r.pathSource).toBe('shell')
    expect(r.probeError).toBeUndefined()
  })
  it('uses the fallback when the shell probe fails, and says why (spec §4.4)', async () => {
    const exec: ExecFn = async () => ({ code: 1, stdout: '', stderr: 'boom', timedOut: false })
    const r = await buildLaunchEnv({ exec, shell: undefined, baseEnv: { PATH: '/usr/bin' }, home: '/Users/x', sentinel: () => S })
    expect(r.env.PATH).toBe('/opt/homebrew/bin:/usr/local/bin:/Users/x/.local/bin:/usr/bin')
    expect(r.pathSource).toBe('fallback')
    expect(r.probeError).toMatch(/boom/)
  })
})
