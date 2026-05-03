/**
 * Centralized order constants for the Settings sidebar.
 *
 * Order values are **scoped per settings scope** (`purdex` / `workspace` /
 * `host`); the sidebar sorts each scope's contributions independently. The
 * tables below describe the visual bands within each scope. Numbers can
 * legitimately repeat across scopes (e.g. purdex `MODULE_CONFIG = 10` and
 * workspace `WORKSPACE_FILES = 10`) — they never compete because contribution
 * lists are filtered by scope before sorting.
 *
 * **purdex scope** — sidebar at `/settings`:
 *
 *   | Band                        | Range  | Examples                                  |
 *   |-----------------------------|--------|-------------------------------------------|
 *   | Top built-in (core)         | 0 – 4  | Appearance / Terminal / Interface         |
 *   | Top conditional built-in    | 5 – 9  | Electron (gated by canSystemTray)         |
 *   | Modules switchboard         | 10     | `module-config` (single header row)       |
 *   | Module-owned                | 11–19  | Editor / Quick Commands / Perf / Sync     |
 *   | Tail built-in               | 20–29  | Dev Environment / Tmux Agent Monitor      |
 *
 * **workspace scope** — sidebar at `/settings/workspaces/<id>`:
 *
 *   | Band                        | Range  | Examples                                  |
 *   |-----------------------------|--------|-------------------------------------------|
 *   | Module-owned                | 0 – 19 | Editor home path (inline 0) / Files (10)  |
 *
 * **host scope** — sidebar at `/settings/hosts/<id>`:
 *
 *   | Band                        | Range   | Examples                                 |
 *   |-----------------------------|---------|------------------------------------------|
 *   | Module-owned                | 0 – 199 | Editor home path (inline 100)            |
 *
 * `register-modules/index.tsx`, `editor-module.tsx`, and any future
 * `registerSettingsSection` / `registerModule({ settings: [...] })` call
 * MUST import from this file instead of hard-coding numbers. Reviewers
 * watch for hard-coded `order:` literals during PR review.
 *
 * Spec §4.1.3 (PR-2 final values). PR-1's transitional `*_PR1` constants
 * were removed by PR-2 commit 5 once Editor was consolidated and Sync
 * was promoted to a structural module.
 */
export const SETTINGS_ORDER = {
  // ---- purdex scope ---------------------------------------------------
  // Top built-in (core) — always present.
  APPEARANCE: 0,
  TERMINAL: 1,
  INTERFACE: 2,
  // Top conditional built-in.
  ELECTRON: 5,
  // Modules switchboard — single row, header of the modules group.
  MODULE_CONFIG: 10,
  // Module-owned (PR-2 final order).
  MODULE_EDITOR: 11,
  MODULE_QUICK_COMMANDS: 12,
  MODULE_PERFORMANCE_MONITOR: 13,
  MODULE_SYNC: 14,
  // Tail built-in — dev / debug surfaces.
  DEV_ENVIRONMENT: 20,
  TMUX_AGENT_MONITOR: 21,
  // ---- workspace scope ------------------------------------------------
  WORKSPACE_FILES: 10,
} as const
