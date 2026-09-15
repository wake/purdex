// spa/src/components/hosts/nex/nex-config-diff.ts — pure helpers for the Nex
// config form: an empty draft, a Go duration comparator, and the
// restart-required check that compares a saved NexConfig against what the
// running engine was actually assembled with (NexInfo.effective).
import type { NexConfig, NexInfo } from '../../../lib/host-api'

export const SANDBOX_PROFILES = ['', 'readonly', 'standard', 'trusted', 'handoff'] as const

export function emptyNexConfig(): NexConfig {
  return {
    enabled: false,
    repo_roots: [],
    service_roots: [],
    claude_bin: '',
    cswap_bin: '',
    path_prepend: [],
    sandbox: { max_profile: '', default_profile: '' },
    timeouts: { lease_ttl: '', interrupt: '', turn: '' },
  }
}

// Go duration units, longest first so "ms" is tried before "m" would ever
// wrongly consume its "s". time.Duration also supports "µs" as an alias
// for "us" — both are accepted here.
const DURATION_UNIT_SECONDS: Record<string, number> = {
  h: 3600,
  m: 60,
  s: 1,
  ms: 0.001,
  us: 0.000001,
  'µs': 0.000001,
  ns: 0.000000001,
}

const DURATION_TOKEN = /(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)/g

/**
 * Parses a Go-formatted duration string ("1h2m3s", "90s", "500ms", ...) into
 * a number of seconds, or null when the string is not a valid Go duration.
 * Only used to compare two duration strings for equality — the unit chosen
 * (seconds) is otherwise arbitrary.
 */
export function parseGoDuration(s: string): number | null {
  const str = s.trim()
  if (str === '') return null

  let total = 0
  let matched = false
  let expectedIndex = 0
  DURATION_TOKEN.lastIndex = 0
  let m: RegExpExecArray | null
  while ((m = DURATION_TOKEN.exec(str)) !== null) {
    if (m.index !== expectedIndex) return null
    matched = true
    total += parseFloat(m[1]) * DURATION_UNIT_SECONDS[m[2]]
    expectedIndex = DURATION_TOKEN.lastIndex
  }
  if (!matched || expectedIndex !== str.length) return null
  return total
}

function sortedTrimmed(list: string[]): string[] {
  return list.map((s) => s.trim()).sort()
}

function rootsDiffer(a: string[], b: string[]): boolean {
  const ta = sortedTrimmed(a)
  const tb = sortedTrimmed(b)
  return ta.length !== tb.length || ta.some((v, i) => v !== tb[i])
}

// A saved timeout of '' means "use Nexen's own default" and always counts as
// equal to whatever the running engine resolved it to — only a non-empty
// saved value is compared against the effective one.
function timeoutDiffers(savedValue: string, effectiveValue: string): boolean {
  if (savedValue.trim() === '') return false
  const a = parseGoDuration(savedValue)
  const b = parseGoDuration(effectiveValue)
  if (a === null || b === null) return savedValue.trim() !== effectiveValue.trim()
  return a !== b
}

/**
 * True when the persisted NexConfig and what the running engine was
 * assembled with (NexInfo.effective) have diverged enough that a daemon
 * restart is needed to apply the saved config. See nex.go's Expanded()/the
 * daemon's mount path for what "effective" is built from.
 */
export function restartRequired(saved: NexConfig | undefined, info: NexInfo | null): boolean {
  const cfg = saved ?? emptyNexConfig()
  const mounted = info?.mounted ?? false

  if (cfg.enabled !== mounted) return true
  if (!mounted || !info?.effective) return false

  const eff = info.effective
  if (rootsDiffer(cfg.repo_roots, eff.repo_roots)) return true
  if (rootsDiffer(cfg.service_roots, eff.service_roots)) return true
  if (cfg.claude_bin.trim() !== eff.claude_bin.trim()) return true
  if (cfg.cswap_bin.trim() !== eff.cswap_bin.trim()) return true
  if (cfg.sandbox.max_profile !== eff.max_profile) return true
  if (cfg.sandbox.default_profile !== eff.default_profile) return true
  if (timeoutDiffers(cfg.timeouts.lease_ttl, eff.lease_ttl)) return true
  if (timeoutDiffers(cfg.timeouts.interrupt, eff.interrupt)) return true
  if (timeoutDiffers(cfg.timeouts.turn, eff.turn)) return true

  return false
}
