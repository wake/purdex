// spa/src/lib/profile/projections.ts — what a profile syncs, and the shape
// signal derived from it (Profile Sync spec §4.2, §4.5).
//
// `PROJECTIONS` is the ONLY allowlist: a field travels if and only if its path
// is listed here, and anything unlisted is device-local by construction. The
// same lists are hashed into each section's `fingerprint`, so changing what is
// synced cannot be done without changing the shape signal — and the guard test
// in projections.test.ts then demands a `SECTION_SCHEMA_ORDINAL` bump.
//
// No fetching, no clocks, no stores — every input is a parameter. The one
// environment-dependent call is `sha256Hex` (WebCrypto), behind the two async
// fingerprint functions.
//
// Path grammar (nothing else is implemented):
//   a.b.c          dot-separated own keys
//   a.*.c          `*` = every own key of a record (never array elements)
//   !a.*.b..name   exclusion: remove key `name` at ANY depth under the prefix
//                  `a.*.b` — depth zero included (the prefix node's own `name`)
//                  — descending through objects and arrays
// Exclusions run after all includes, so the order of a list never changes the
// result. A path that matches nothing contributes nothing (optional fields).
import { sha256Hex } from '../crypto-hash'
import type { ProfileSectionKey, SectionKind, SettingsStorageKey } from './types'

export type { ProfileSectionKey, SectionKind } from './types'

/** `'<storageKey>.<field>'` for each field — spelled out one by one on purpose: a new store field stays unsynced until someone lists it here (and bumps the ordinal). */
function settingsPaths(storageKey: SettingsStorageKey, fields: readonly string[]): string[] {
  return fields.map((field) => `${storageKey}.${field}`)
}

export const PROJECTIONS: Record<SectionKind, readonly string[]> = {
  hosts: [
    'hostOrder', 'hosts.*.id', 'hosts.*.name', 'hosts.*.ip', 'hosts.*.port', 'hosts.*.token',
    'hosts.*.order', 'hosts.*.color', 'hosts.*.colors', 'hosts.*.icon', 'hosts.*.iconWeight',
  ],
  workspaces: [
    'order', 'workspaces.*.name', 'workspaces.*.icon', 'workspaces.*.iconWeight', 'workspaces.*.moduleConfig',
  ],
  tabs: [
    'order', 'tabs.*.id', 'tabs.*.pinned', 'tabs.*.locked', 'tabs.*.createdAt', 'tabs.*.layout',
    '!tabs.*.layout..sizes', // split ratios at any depth are device-local (decision 8)
  ],
  settings: [
    // useUISettingsStore has no partialize: every value field persists. All of
    // them are listed EXCEPT `terminalSettingsVersion` (a reconnect bump counter).
    ...settingsPaths('purdex-ui-settings', [
      'terminalRevealDelay', 'terminalRenderer', 'keepAliveCount', 'keepAlivePinned',
      'linkDetectAbsolute', 'linkDetectTilde', 'linkDetectRelativeSlash', 'linkDetectBareFilename',
      'tabIndicatorStyle', 'ccIconVariant', 'codexIconVariant', 'dynamicTabName', 'tabNameTooltipMode',
      'showAgentTitleInStatusBar', 'stripAgentTitleMarker',
      'hostBadgeSidebarEnabled', 'hostBadgeSidebarLineColor', 'hostBadgeSidebarBox', 'hostBadgeSidebarInset',
      'hostBadgeSidebarRadius',
      'hostBadgeTabBarEnabled', 'hostBadgeTabBarLineColor', 'hostBadgeTabBarBox', 'hostBadgeTabBarInset',
      'hostBadgeTabBarRadius',
    ]),
    ...settingsPaths('purdex-editor-settings', [
      'tabSize', 'insertSpaces', 'wordWrap', 'lineNumbers', 'minimap', 'fontSize', 'popupOnMissingFile',
      'autoSearchLayer1', 'contentWidth',
    ]),
    ...settingsPaths('purdex-themes', ['activeThemeId', 'customThemes']),
    ...settingsPaths('purdex-i18n', ['activeLocaleId', 'customLocales']),
    ...settingsPaths('purdex-notification-settings', ['agents']),
    ...settingsPaths('purdex-workspace-settings', ['workspaces']),
    ...settingsPaths('purdex-host-settings', ['hosts']),
    // NOT `knownIds` (derived registry), NOT `activeEditingProfile` (editor UI state).
    ...settingsPaths('purdex-newtab-layout', ['profiles']),
    // The only field taken from useLayoutStore; the rest of it is device-local.
    ...settingsPaths('purdex-layout', ['tabPosition']),
    // NOT `purdex-module-enabled` — nine stores, not ten. useModuleEnabledStore
    // says so itself: toggling a module on or off "is a device-local preference
    // (a host with limited resources can turn off modules it doesn't want to
    // run), not a config to sync between devices". P2a listed `.enabled` here;
    // P2b removed it (settings ordinal 1 → 2). Reversing that costs one line
    // here (plus the `SettingsStorageKey` member) and ANOTHER ordinal bump.
  ],
}

/**
 * Hand-maintained direction signal (spec §4.5). Bump a kind when its projection
 * changes, and ALSO when only a value domain changes (a new enum member, a
 * re-interpreted field) — the case the fingerprint cannot see.
 */
export const SECTION_SCHEMA_ORDINAL: Record<SectionKind, number> = {
  hosts: 1,
  settings: 2, // 2: `purdex-module-enabled.enabled` removed (device-local, see PROJECTIONS.settings)
  workspaces: 1,
  tabs: 1,
}

// === Section keys ===

// Exactly what the daemon accepts (internal/module/profiles/validate.go):
// ^(hosts|settings|workspaces|tabs\.[A-Za-z0-9_-]{1,64})$
const WORKSPACE_ID_RE = /^[A-Za-z0-9_-]{1,64}$/
const TABS_PREFIX = 'tabs.'

/** `'tabs.ws1'` → `'tabs'`; a key the daemon would reject → `null`. */
export function sectionKind(key: string): SectionKind | null {
  if (key === 'hosts' || key === 'settings' || key === 'workspaces') return key
  return workspaceIdOf(key) === null ? null : 'tabs'
}

/** The section key of a workspace's tabs. Throws rather than build a key the daemon will 400. */
export function tabsSectionKey(workspaceId: string): ProfileSectionKey {
  if (!WORKSPACE_ID_RE.test(workspaceId)) {
    throw new Error(`profile: workspace id ${JSON.stringify(workspaceId)} cannot form a section key`)
  }
  return `${TABS_PREFIX}${workspaceId}`
}

/** The workspace id of a `tabs.<id>` key; `null` for any other key, valid or not. */
export function workspaceIdOf(key: string): string | null {
  if (!key.startsWith(TABS_PREFIX)) return null
  const id = key.slice(TABS_PREFIX.length)
  return WORKSPACE_ID_RE.test(id) ? id : null
}

// === Shape ===

/** SHA-256 of a path list, sorted and joined with `\n` — sorted so that reordering a literal is not a shape change. */
export async function fingerprintOf(paths: readonly string[]): Promise<string> {
  return sha256Hex(new TextEncoder().encode([...paths].sort().join('\n')))
}

/** The fingerprint of a section kind's projection: 64 lowercase hex. */
export function sectionFingerprint(kind: SectionKind): Promise<string> {
  return fingerprintOf(PROJECTIONS[kind])
}

/** `{kind: [fingerprint, ordinal]}` for all four kinds — what the guard test snapshots. */
export async function shapeTable(): Promise<Record<SectionKind, [string, number]>> {
  const row = async (kind: SectionKind): Promise<[string, number]> => [
    await sectionFingerprint(kind),
    SECTION_SCHEMA_ORDINAL[kind],
  ]
  return { hosts: await row('hosts'), settings: await row('settings'), workspaces: await row('workspaces'), tabs: await row('tabs') }
}

// === project ===

type Rec = Record<string, unknown>

/** Marks "this include path matched nothing here". */
const NONE = Symbol('none')

// Assigning `obj['__proto__'] = v` on a plain object rewrites its prototype
// instead of adding a key. Payloads come back from the daemon as parsed JSON,
// where `__proto__` CAN be an own key — it is never copied.
const FORBIDDEN_KEY = '__proto__'

function isRecord(value: unknown): value is Rec {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function ownKeys(node: Rec): string[] {
  return Object.keys(node).filter((k) => k !== FORBIDDEN_KEY)
}

/** Deep copy of an included subtree. `undefined` members are dropped, as JSON would. */
function clone(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(clone)
  if (!isRecord(value)) return value
  const out: Rec = {}
  for (const k of ownKeys(value)) {
    if (value[k] !== undefined) out[k] = clone(value[k])
  }
  return out
}

/** The part of `node` selected by `segs[i..]`, as fresh structure; `NONE` when nothing matches. */
function pick(node: unknown, segs: readonly string[], i: number): unknown {
  if (i === segs.length) return node === undefined ? NONE : clone(node)
  if (!isRecord(node)) return NONE
  const seg = segs[i]
  const keys = seg === '*' ? ownKeys(node) : Object.hasOwn(node, seg) && seg !== FORBIDDEN_KEY ? [seg] : []
  const out: Rec = {}
  let matched = false
  for (const k of keys) {
    const picked = pick(node[k], segs, i + 1)
    if (picked === NONE) continue
    out[k] = picked
    matched = true
  }
  return matched ? out : NONE
}

/** Merges one picked tree into the output, so overlapping includes add up rather than clobber. */
function mergeInto(target: Rec, picked: Rec): void {
  for (const k of Object.keys(picked)) {
    const a = target[k]
    const b = picked[k]
    if (isRecord(a) && isRecord(b)) mergeInto(a, b)
    else target[k] = b
  }
}

interface Exclusion {
  prefix: string[]
  name: string
}

function parseExclusion(path: string): Exclusion {
  const body = path.slice(1)
  const at = body.indexOf('..')
  const prefix = at > 0 ? body.slice(0, at).split('.') : []
  const name = at > 0 ? body.slice(at + 2) : ''
  if (prefix.length === 0 || prefix.includes('') || name === '' || name.includes('.')) {
    throw new Error(`profile: malformed exclusion ${JSON.stringify(path)} (expected "!prefix..name")`)
  }
  return { prefix, name }
}

/** Removes key `name` from `node` and from everything below it, through objects and arrays. */
function stripDeep(node: unknown, name: string): void {
  if (Array.isArray(node)) {
    for (const item of node) stripDeep(item, name)
  } else if (isRecord(node)) {
    delete node[name]
    for (const k of Object.keys(node)) stripDeep(node[k], name)
  }
}

/** Runs one exclusion over the (already fresh, so safely mutable) output tree. */
function exclude(node: unknown, ex: Exclusion, i: number): void {
  if (i === ex.prefix.length) return stripDeep(node, ex.name)
  if (!isRecord(node)) return
  const seg = ex.prefix[i]
  const keys = seg === '*' ? Object.keys(node) : Object.hasOwn(node, seg) ? [seg] : []
  for (const k of keys) exclude(node[k], ex, i + 1)
}

/**
 * Copies out of `source` exactly what `paths` lists. Never mutates `source`;
 * the result shares no structure with it. Always returns an object — empty when
 * nothing matched or `source` is not a record.
 */
export function project(source: unknown, paths: readonly string[]): unknown {
  const out: Rec = {}
  const exclusions: Exclusion[] = []
  for (const path of paths) {
    if (path.startsWith('!')) {
      exclusions.push(parseExclusion(path))
      continue
    }
    const picked = pick(source, path.split('.'), 0)
    if (picked !== NONE && isRecord(picked)) mergeInto(out, picked)
  }
  for (const ex of exclusions) exclude(out, ex, 0)
  return out
}
