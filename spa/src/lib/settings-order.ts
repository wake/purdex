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
 *   | Module-owned (alphabetical) | 11–19  | Browser / Commands / Editor / Files /     |
 *   |                             |        | Monitor / Sync                            |
 *   | Tail built-in               | 20–29  | Dev Environment / Tmux Agent Monitor      |
 *
 * The "Module-owned" band sorts by **English (default) sidebar short label**,
 * not runtime locale. Constants are named after the **module identity**
 * (e.g. `MODULE_QUICK_COMMANDS` even though the sidebar shows "Commands"),
 * because the underlying module ID is the stable identifier; sidebar
 * labels can change without breaking the constants. (Spec §I3)
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
  // Module-owned (alphabetical by English sidebar short label —
  // Browser / Commands / Editor / Files / Monitor / Sync). Constant name
  // tracks module identity, value tracks display order (spec §I3).
  MODULE_BROWSER: 11,             // sidebar: "Browser"
  MODULE_QUICK_COMMANDS: 12,      // sidebar: "Commands"
  MODULE_EDITOR: 13,              // sidebar: "Editor"
  MODULE_FILES: 14,               // sidebar: "Files"
  MODULE_PERFORMANCE_MONITOR: 15, // sidebar: "Monitor"
  MODULE_SYNC: 16,                // sidebar: "Sync"
  // Tail built-in — dev / debug surfaces.
  DEV_ENVIRONMENT: 20,
  TMUX_AGENT_MONITOR: 21,
  // ---- workspace scope ------------------------------------------------
  WORKSPACE_FILES: 10,
} as const
