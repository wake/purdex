// spa/src/components/hosts/nex/nex-config-diff.ts — pure helpers for the Nex
// config form: the sandbox profile list, an empty draft, and the
// restart-required check (which the daemon computes — spec §4.4.2).
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

/**
 * True while the daemon reports that its persisted [nex] section differs
 * from the one it booted with (`info.nex.restart_required`, spec §4.4.2).
 * The daemon owns the comparison: it sees the unexpanded boot config, so
 * `~` paths, `path_prepend` and a soft-failed engine are all handled there.
 * `_saved` is kept so callers need not change.
 */
export function restartRequired(_saved: NexConfig | undefined, info: NexInfo | null): boolean {
  return info?.restart_required === true
}

/**
 * Coerces a `nex` section read from the daemon into a complete NexConfig:
 * null/missing lists become [] and a missing `sandbox`/`timeouts` object (or
 * key) falls back to emptyNexConfig()'s defaults. Older daemons and configs
 * without a [nex] section can emit `null` lists (spec §4.4.2).
 */
export function normalizeNexConfig(raw: Partial<NexConfig> | null | undefined): NexConfig {
  const base = emptyNexConfig()
  const r = raw ?? {}
  return {
    ...base,
    ...r,
    repo_roots: r.repo_roots ?? [],
    service_roots: r.service_roots ?? [],
    path_prepend: r.path_prepend ?? [],
    claude_bin: r.claude_bin ?? '',
    cswap_bin: r.cswap_bin ?? '',
    enabled: r.enabled ?? false,
    sandbox: { ...base.sandbox, ...(r.sandbox ?? {}) },
    timeouts: { ...base.timeouts, ...(r.timeouts ?? {}) },
  }
}
