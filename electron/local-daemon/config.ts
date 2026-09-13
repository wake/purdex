// Reading and seeding ~/.config/pdx/config.toml from the app. The daemon
// rewrites the file with every key on first start (EnsureHostID), so it is
// parsed with a real TOML parser and Go's defaults are applied here.
import { join } from 'node:path'
import { parse } from 'smol-toml'

export interface DaemonConfig { bind: string; port: number; token: string | null; dataDir: string }

export const DEFAULT_DATA_DIR = (home: string) => join(home, '.config', 'pdx')

export function parseDaemonConfig(toml: string, home: string): DaemonConfig {
  const doc = toml.trim() === '' ? {} : (parse(toml) as Record<string, unknown>)
  const str = (k: string) => (typeof doc[k] === 'string' ? (doc[k] as string) : undefined)
  const num = (k: string) => (typeof doc[k] === 'number' ? (doc[k] as number) : undefined)
  return {
    bind: str('bind') ?? '127.0.0.1',
    port: num('port') ?? 7860,
    token: str('token') ?? null,
    dataDir: str('data_dir') ?? DEFAULT_DATA_DIR(home),
  }
}

export interface Iface { name: string; address: string; family: string; internal: boolean }

// 100.64.0.0/10 → first octet 100, second octet 64..127.
function inCGNAT(addr: string): boolean {
  const m = /^(\d+)\.(\d+)\.(\d+)\.(\d+)$/.exec(addr)
  if (!m) return false
  return Number(m[1]) === 100 && Number(m[2]) >= 64 && Number(m[2]) <= 127
}

export function pickBindAddress(ifaces: Iface[], platform: NodeJS.Platform): { bind: string; note?: string } {
  const candidates = ifaces
    .filter((i) => i.family === 'IPv4' && !i.internal && inCGNAT(i.address))
    .filter((i) => platform !== 'darwin' || i.name.startsWith('utun'))
    .map((i) => i.address)
  if (candidates.length === 1) return { bind: candidates[0] }
  if (candidates.length === 0) {
    return { bind: '127.0.0.1', note: 'No Tailscale interface found; bound to 127.0.0.1. Edit ~/.config/pdx/config.toml and restart to change.' }
  }
  return { bind: '127.0.0.1', note: `Several Tailscale-like addresses (${candidates.join(', ')}); bound to 127.0.0.1. Edit ~/.config/pdx/config.toml and restart to choose one.` }
}

export function renderInitialConfig(bind: string, token: string): string {
  return `bind = "${bind}"\nport = 7860\ntoken = "${token}"\n\n[dev]\nupdate = false\n`
}

export function generateToken(random: (n: number) => Buffer): string {
  return 'purdex_' + random(20).toString('hex')
}
