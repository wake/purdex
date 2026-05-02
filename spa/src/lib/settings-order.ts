/**
 * Centralized order constants for the Settings sidebar.
 *
 * The sidebar is grouped into bands so visual ordering communicates intent:
 *
 *   | Band                        | Range  | Examples                                  |
 *   |-----------------------------|--------|-------------------------------------------|
 *   | Top built-in (core)         | 0 – 4  | Appearance / Terminal / Interface         |
 *   | Top conditional built-in    | 5 – 9  | Electron (gated by canSystemTray)         |
 *   | Modules switchboard         | 10     | `module-config` (single header row)       |
 *   | Module-owned                | 11–19  | Editor / Quick Commands / Perf / Sync     |
 *   | Tail built-in               | 20–29  | Dev Environment / Tmux Agent Monitor      |
 *
 * `register-modules/index.tsx`, `editor-module.tsx`, and any future
 * `registerSettingsSection` / `registerModule({ settings: [...] })` call
 * MUST import from this file instead of hard-coding numbers. Reviewers
 * watch for hard-coded `order:` literals during PR review.
 *
 * NOTE: PR-1 is a transitional state — `module-config` adopts the constant
 * (=10) so it lands above the existing module-owned entries, but the
 * module-owned entries themselves keep transitional hard-coded values
 * (11/12/13/14/15/16) until PR-2 finishes the Editor consolidation +
 * Sync upgrade. PR-2 swaps the remaining hard-coded values for the
 * `MODULE_*` constants below — see spec §4.1.3 / §4.1.4.
 */
export const SETTINGS_ORDER = {
  // Top built-in (core) — always present.
  APPEARANCE: 0,
  TERMINAL: 1,
  INTERFACE: 2,
  // Top conditional built-in.
  ELECTRON: 5,
  // Modules switchboard — single row, header of the modules group.
  MODULE_CONFIG: 10,
  // Module-owned (PR-2 final values; PR-1 keeps transitional numbers).
  MODULE_EDITOR: 11,
  MODULE_QUICK_COMMANDS: 12,
  MODULE_PERFORMANCE_MONITOR: 13,
  MODULE_SYNC: 14,
  // Tail built-in — dev / debug surfaces.
  DEV_ENVIRONMENT: 20,
  TMUX_AGENT_MONITOR: 21,
} as const
