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
 * Two surfaces are exposed:
 *
 * 1. `SETTINGS_ORDER` — the active order values for the **current** state
 *    of the codebase (PR-1: includes PR-1 transitional `MODULE_*_PR1`
 *    constants). All call sites read from this object.
 *
 * 2. `SETTINGS_ORDER_PR2_FUTURE` — values that PR-2 will switch to after
 *    Editor consolidation + Sync upgrade complete. **Not used by any
 *    call site in PR-1.** Kept here so the eventual PR-2 swap is a
 *    one-line constant rename rather than a wholesale rewrite.
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
  // Module-owned (PR-1 transitional). Editor still has three sidebar
  // entries (open-behavior / link-detect / editor); Sync is still
  // registered as a built-in section. PR-2 collapses these into a
  // single Editor entry + upgrades Sync to a module — at that point
  // the values move to SETTINGS_ORDER_PR2_FUTURE below.
  MODULE_PERFORMANCE_MONITOR_PR1: 11,
  MODULE_EDITOR_OPEN_BEHAVIOR_PR1: 12,
  MODULE_EDITOR_LINK_DETECT_PR1: 13,
  MODULE_EDITOR_PR1: 14,
  MODULE_QUICK_COMMANDS_PR1: 15,
  SYNC_PR1: 16,
  // Tail built-in — dev / debug surfaces.
  DEV_ENVIRONMENT: 20,
  TMUX_AGENT_MONITOR: 21,
} as const

/**
 * Order values that PR-2 will adopt once Editor consolidation +
 * Sync upgrade complete. Do not import these constants in PR-1
 * call sites — the file-level lint guard / order test rejects use.
 */
export const SETTINGS_ORDER_PR2_FUTURE = {
  MODULE_EDITOR: 11,
  MODULE_QUICK_COMMANDS: 12,
  MODULE_PERFORMANCE_MONITOR: 13,
  MODULE_SYNC: 14,
} as const
