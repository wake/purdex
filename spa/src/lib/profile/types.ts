// spa/src/lib/profile/types.ts — the shared vocabulary of the Profile Sync pure
// core (spec 2026-09-20-profile-sync §4.2, §4.5, §4.7). Types only: this file has
// no runtime output, imports nothing at runtime, and takes its material only from
// types that are already exported. `HostState` / `WorkspaceState` are private to
// their stores, so the lib declares its own structural input types here and the
// test files pin them against the real stores.
//
// There is deliberately no bare `Profile` type — the new-tab layout presets
// (`lib/resolve-preset.ts`) used to own that name.
import type { HostConfig } from '../../stores/useHostStore'
import type { Pane, SplitLayout, Tab, Workspace } from '../../types/tab'

// === Section identity ===

/** The four kinds of section a profile document is made of (spec §4.2). */
export type SectionKind = 'hosts' | 'settings' | 'workspaces' | 'tabs'

/** A section key as the daemon stores it: three singletons plus one `tabs.<workspaceId>` per workspace. */
export type ProfileSectionKey = 'hosts' | 'settings' | 'workspaces' | `tabs.${string}`

/** The nine persisted stores the `settings` section draws fields from, by localStorage key (`purdex-module-enabled` and `purdex-editor-settings` are device-local — see PROJECTIONS.settings). */
export type SettingsStorageKey =
  | 'purdex-ui-settings'
  | 'purdex-themes'
  | 'purdex-i18n'
  | 'purdex-notification-settings'
  | 'purdex-workspace-settings'
  | 'purdex-host-settings'
  | 'purdex-newtab-layout'
  | 'purdex-layout'
  | 'purdex-host-looks'

// === Section payloads (what travels) ===

/** A pane layout with every split's `sizes` removed: structure travels, ratios stay device-local (decision 8). */
export type StrippedLayout =
  | { type: 'leaf'; pane: Pane }
  | (Omit<SplitLayout, 'sizes' | 'children'> & { children: StrippedLayout[] })

/** A `Tab` as it appears in a `tabs.<ws>` payload: the projected fields, layout without `sizes`. */
export type TabEntry = Omit<Tab, 'layout'> & { layout: StrippedLayout }

/** A workspace as it appears in the `workspaces` payload: no `id` (it is the record key), no `tabs`, no `activeTabId`. */
export type WorkspaceEntry = Pick<Workspace, 'name' | 'icon' | 'iconWeight' | 'moduleConfig'>

/** Payload of the `hosts` section: the host configs, whole, and their order. */
export interface HostsPayload {
  hosts: Record<string, HostConfig>
  hostOrder: string[]
}

/** Payload of the `workspaces` section: `Workspace[]` converted to order + record. */
export interface WorkspacesPayload {
  order: string[]
  workspaces: Record<string, WorkspaceEntry>
}

/** Payload of one `tabs.<workspaceId>` section: that workspace's tab order and its tabs. */
export interface TabsPayload {
  order: string[]
  tabs: Record<string, TabEntry>
}

/** Payload of the `settings` section: per storage key, the projected fields only. A store may be absent. */
export type SettingsPayload = Partial<Record<SettingsStorageKey, Record<string, unknown>>>

/** Any section payload. */
export type SectionPayload = HostsPayload | WorkspacesPayload | TabsPayload | SettingsPayload

// === Sources (what the builders read — store state, structurally) ===

/** The part of `useHostStore` state the `hosts` builder reads. */
export interface HostsSource {
  hosts: Record<string, HostConfig>
  hostOrder: string[]
}

/** The part of `useWorkspaceStore` state the `workspaces` builder reads. */
export interface WorkspacesSource {
  workspaces: readonly Workspace[]
}

/** The part of `useTabStore` state the `tabs.*` builders read. */
export interface TabsSource {
  tabs: Record<string, Tab>
  tabOrder: readonly string[]
}

/** The state of each settings store, keyed by storage key; both builder input and applier input/output. */
export type SettingsSources = Record<SettingsStorageKey, Record<string, unknown>>

// === Slices (what the appliers take and return) ===

/** Applier slice for `hosts`: the synced part plus the device-local focus the apply must preserve. */
export interface HostsSlice extends HostsSource {
  activeHostId: string | null
  devHostId: string | null
}

/** Applier slice for `workspaces`: the list plus the device-local active workspace. */
export interface WorkspacesSlice {
  workspaces: Workspace[]
  activeWorkspaceId: string | null
}

/** Applier slice for `tabs.<ws>`: the tab record, and the workspaces whose `tabs` / `activeTabId` the apply rewrites. */
export interface TabsSlice {
  tabs: Record<string, Tab>
  workspaces: Workspace[]
}

// === Shape and SOT bookkeeping ===

/** A section's shape signal (spec §4.5): the fingerprint detects a change, the ordinal decides direction. */
export interface Shape {
  fingerprint: string
  ordinal: number
}

/** One row of the daemon's section index: what the SOT holds for a section, without its payload. */
export interface SotIndexEntry {
  section: string
  rev: number
  hash: string
  fingerprint: string
  ordinal: number
}
