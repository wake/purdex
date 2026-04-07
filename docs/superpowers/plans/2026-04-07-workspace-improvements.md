# Workspace Improvements Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Improve workspace UX — redesign chip, add settings page, Phosphor Icons picker, fix tab recall, add empty state.

**Architecture:** WorkspaceChip changes to Notion/Linear-style dropdown header. New WorkspaceSettingsPage renders via SettingsPage scope routing. Phosphor Icons lazy-loaded via generated loader map. Tab recall fix uses getState() to avoid stale closures.

**Tech Stack:** React 19, Zustand 5, Tailwind 4, Vitest, @phosphor-icons/react, Vite import.meta.glob

**Spec:** `docs/superpowers/specs/2026-04-07-workspace-improvements-design.md`

---

## File Map

| File | Action | Responsibility |
|------|--------|----------------|
| `spa/src/features/workspace/hooks.ts` | Modify | Fix handleSelectWorkspace stale closure |
| `spa/scripts/generate-icon-loader.mjs` | Create | Script to generate icon loader from @phosphor-icons/react |
| `spa/src/features/workspace/generated/icon-loader.ts` | Create (generated) | Lazy loader map for ALL Phosphor icons + name list |
| `spa/src/features/workspace/constants.ts` | Modify | Replace WORKSPACE_ICONS with curated category map |
| `spa/src/features/workspace/components/WorkspaceIcon.tsx` | Create | Suspense-wrapped icon component |
| `spa/src/features/workspace/components/WorkspaceChip.tsx` | Rewrite | Dropdown header style |
| `spa/src/features/workspace/components/WorkspaceColorPicker.tsx` | Modify | Extract inline ColorGrid |
| `spa/src/features/workspace/components/WorkspaceIconPicker.tsx` | Rewrite | Phosphor category grid + search |
| `spa/src/features/workspace/components/WorkspaceSettingsPage.tsx` | Create | Workspace settings page |
| `spa/src/features/workspace/components/WorkspaceEmptyState.tsx` | Create | Empty workspace placeholder |
| `spa/src/features/workspace/components/WorkspaceContextMenu.tsx` | Modify | Add "Settings" item |
| `spa/src/features/workspace/components/ActivityBar.tsx` | Modify | Use WorkspaceIcon |
| `spa/src/components/SettingsPage.tsx` | Modify | Scope routing (global vs workspace) |
| `spa/src/hooks/useRouteSync.ts` | Modify | Add setActiveWorkspace for workspace-settings |
| `spa/src/features/workspace/index.ts` | Modify | Export new components |
| `spa/src/App.tsx` | Modify | Chip onClick, context menu wiring, creation flow, empty state |

---

## Task 1: Fix workspace tab recall

**Files:**
- Modify: `spa/src/features/workspace/hooks.ts:29-34`
- Test: `spa/src/features/workspace/hooks.test.ts` (new)

- [ ] **Step 1: Write failing test**

Create `spa/src/features/workspace/hooks.test.ts`:

```typescript
import { describe, it, expect, beforeEach } from 'vitest'
import { useWorkspaceStore } from './store'
import { useTabStore } from '../../stores/useTabStore'
import { createTab } from '../../types/tab'

describe('workspace tab recall', () => {
  beforeEach(() => {
    useWorkspaceStore.getState().reset()
    useTabStore.getState().reset()
  })

  it('handleSelectWorkspace reads latest activeTabId from store', () => {
    // Setup: 2 workspaces, each with a tab
    const tab1 = createTab({ kind: 'dashboard' })
    const tab2 = createTab({ kind: 'hosts' })
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2)
    const ws1 = useWorkspaceStore.getState().addWorkspace('WS1')
    const ws2 = useWorkspaceStore.getState().addWorkspace('WS2')
    useWorkspaceStore.getState().addTabToWorkspace(ws1.id, tab1.id)
    useWorkspaceStore.getState().addTabToWorkspace(ws2.id, tab2.id)
    useWorkspaceStore.getState().setWorkspaceActiveTab(ws1.id, tab1.id)
    useWorkspaceStore.getState().setWorkspaceActiveTab(ws2.id, tab2.id)

    // Switch to ws1 — should activate tab1
    useWorkspaceStore.getState().setActiveWorkspace(ws1.id)
    const ws = useWorkspaceStore.getState().workspaces.find((w) => w.id === ws1.id)
    const allTabs = useTabStore.getState().tabs
    expect(ws?.activeTabId).toBe(tab1.id)
    expect(allTabs[ws!.activeTabId!]).toBeDefined()
  })

  it('falls back to first tab when activeTabId points to closed tab', () => {
    const tab1 = createTab({ kind: 'dashboard' })
    const tab2 = createTab({ kind: 'hosts' })
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2)
    const ws = useWorkspaceStore.getState().addWorkspace('WS')
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, tab1.id)
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, tab2.id)
    useWorkspaceStore.getState().setWorkspaceActiveTab(ws.id, tab1.id)

    // Close tab1 — activeTabId becomes null
    useWorkspaceStore.getState().removeTabFromWorkspace(ws.id, tab1.id)
    useTabStore.getState().closeTab(tab1.id)

    const updated = useWorkspaceStore.getState().workspaces.find((w) => w.id === ws.id)
    expect(updated?.activeTabId).toBeNull()
    // Fallback: first remaining tab
    expect(updated?.tabs[0]).toBe(tab2.id)
  })
})
```

- [ ] **Step 2: Run test to verify it passes** (these are store-level tests, they should pass already — the bug is in the React hook)

Run: `cd spa && npx vitest run src/features/workspace/hooks.test.ts`

- [ ] **Step 3: Fix handleSelectWorkspace**

In `spa/src/features/workspace/hooks.ts`, replace lines 29-34:

```typescript
// BEFORE
const handleSelectWorkspace = useCallback((wsId: string) => {
    setActiveWorkspace(wsId)
    const ws = workspaces.find((w) => w.id === wsId)
    if (ws?.activeTabId) setActiveTab(ws.activeTabId)
    else if (ws?.tabs[0]) setActiveTab(ws.tabs[0])
  }, [workspaces, setActiveWorkspace, setActiveTab])

// AFTER
const handleSelectWorkspace = useCallback((wsId: string) => {
    setActiveWorkspace(wsId)
    const ws = useWorkspaceStore.getState().workspaces.find((w) => w.id === wsId)
    const allTabs = useTabStore.getState().tabs
    if (ws?.activeTabId && allTabs[ws.activeTabId]) setActiveTab(ws.activeTabId)
    else if (ws?.tabs[0]) setActiveTab(ws.tabs[0])
  }, [setActiveWorkspace, setActiveTab])
```

Also add import at top if not present: `import { useWorkspaceStore } from './store'` (already imported via re-export — check and add `useTabStore` direct import).

- [ ] **Step 4: Remove unused `workspaces` from hook deps**

In the same file, remove `workspaces` from the hook's selector if it's no longer used by `handleSelectWorkspace`. Check if other handlers still need it — `handleReorderTabs` uses `activeWorkspaceId` (not `workspaces`), so `workspaces` can be removed from the hook entirely if no other handler uses it.

- [ ] **Step 5: Run full workspace test suite**

Run: `cd spa && npx vitest run src/features/workspace/`

Expected: All tests pass.

- [ ] **Step 6: Commit**

```bash
git add spa/src/features/workspace/hooks.ts spa/src/features/workspace/hooks.test.ts
git commit -m "fix: workspace tab recall — use getState() to avoid stale closure"
```

---

## Task 2: Icon loader generator script

**Files:**
- Create: `spa/scripts/generate-icon-loader.mjs`
- Create: `spa/src/features/workspace/generated/icon-loader.ts` (generated output)

- [ ] **Step 1: Create generator script**

Create `spa/scripts/generate-icon-loader.mjs`:

```javascript
#!/usr/bin/env node
/**
 * Generates icon-loader.ts from @phosphor-icons/react CSR entries.
 * Run: node spa/scripts/generate-icon-loader.mjs
 */
import { readdirSync, writeFileSync, mkdirSync } from 'fs'
import { join, dirname } from 'path'
import { fileURLToPath } from 'url'

const __dirname = dirname(fileURLToPath(import.meta.url))
const csr = join(__dirname, '..', 'node_modules', '@phosphor-icons', 'react', 'dist', 'csr')
const outDir = join(__dirname, '..', 'src', 'features', 'workspace', 'generated')
const outFile = join(outDir, 'icon-loader.ts')

// Read all .es.js files, extract icon names (skip .d.ts)
const files = readdirSync(csr).filter(f => f.endsWith('.es.js'))
const names = files.map(f => f.replace('.es.js', '')).sort()

// Generate file
const lines = [
  '// Auto-generated by scripts/generate-icon-loader.mjs — do not edit manually',
  `// Generated: ${new Date().toISOString()} — ${names.length} icons`,
  '',
  'import type { Icon } from \'@phosphor-icons/react\'',
  '',
  '/** All available Phosphor icon names */',
  `export const ALL_ICON_NAMES: string[] = ${JSON.stringify(names)}`,
  '',
  '/** Lazy loaders — each resolves to a separate chunk */',
  'export const iconLoaders: Record<string, () => Promise<Icon>> = {',
]

for (const name of names) {
  lines.push(`  '${name}': () => import('@phosphor-icons/react/dist/csr/${name}.es.js').then(m => m.default ?? m['${name}']),`)
}

lines.push('}')
lines.push('')

mkdirSync(outDir, { recursive: true })
writeFileSync(outFile, lines.join('\n'), 'utf-8')
console.log(`Generated ${outFile} with ${names.length} icons`)
```

- [ ] **Step 2: Run the script**

Run: `node spa/scripts/generate-icon-loader.mjs`

Expected: `Generated spa/src/features/workspace/generated/icon-loader.ts with ~1512 icons`

- [ ] **Step 3: Verify generated file compiles**

Run: `cd spa && npx tsc --noEmit src/features/workspace/generated/icon-loader.ts 2>&1 | head -5`

If type errors, adjust the `.then(m => ...)` extraction in the script.

- [ ] **Step 4: Add generated dir to .gitignore or commit it**

Add to `spa/.gitignore` if treating as build artifact, OR commit directly since the generated file is deterministic and avoids requiring the script in CI. **Recommendation: commit it** (simpler for subagents).

- [ ] **Step 5: Commit**

```bash
git add spa/scripts/generate-icon-loader.mjs spa/src/features/workspace/generated/
git commit -m "feat: add Phosphor icon loader generator + generated icon-loader.ts"
```

---

## Task 3: Curated icon categories + WorkspaceIcon component

**Files:**
- Modify: `spa/src/features/workspace/constants.ts`
- Create: `spa/src/features/workspace/components/WorkspaceIcon.tsx`
- Test: `spa/src/features/workspace/components/WorkspaceIcon.test.tsx`

- [ ] **Step 1: Replace WORKSPACE_ICONS with curated categories**

Rewrite `spa/src/features/workspace/constants.ts`:

```typescript
export const WORKSPACE_COLORS = [
  '#7a6aaa', '#6aaa7a', '#aa6a7a', '#6a8aaa', '#aa8a6a', '#8a6aaa',
  '#5b8c5a', '#c75050', '#d4a843', '#5a7fbf', '#bf5a9d', '#4abfbf',
]

/** Curated Phosphor icon names by category */
export const CURATED_ICON_CATEGORIES: Record<string, string[]> = {
  general: [
    'House', 'Star', 'Heart', 'Bell', 'Flag', 'Lightning', 'Fire',
    'BookmarkSimple', 'Crown', 'Diamond', 'Eye', 'Fingerprint', 'Gift',
    'Globe', 'Handshake', 'Key', 'Lightbulb', 'MagnifyingGlass', 'Medal',
    'PushPin', 'Shield', 'Sparkle', 'Tag', 'Trophy', 'Umbrella',
  ],
  development: [
    'Terminal', 'Code', 'GitBranch', 'GitCommit', 'GitPullRequest', 'Bug',
    'Database', 'CloudArrowUp', 'Cpu', 'HardDrive', 'Plugs', 'Robot',
    'Atom', 'Brackets', 'BracketsAngle', 'BracketsSquare', 'CodeBlock',
    'DeviceMobile', 'Flask', 'Function', 'Hash', 'Infinity', 'Plug',
    'Pulse', 'Webhook',
  ],
  objects: [
    'Folder', 'FolderOpen', 'File', 'FileText', 'Clipboard', 'Book',
    'BookOpen', 'Lock', 'LockOpen', 'Wrench', 'Gear', 'Hammer',
    'Scissors', 'Pencil', 'Pen', 'Eraser', 'Paperclip', 'Archive',
    'Bag', 'Basket', 'Box', 'Briefcase', 'Package', 'Suitcase', 'Wallet',
  ],
  communication: [
    'ChatCircle', 'ChatDots', 'ChatText', 'Envelope', 'EnvelopeOpen',
    'Phone', 'PhoneCall', 'Megaphone', 'Broadcast', 'Rss', 'At',
    'Link', 'PaperPlane', 'Share', 'ShareNetwork', 'Chats', 'Handshake',
    'SpeakerHigh', 'Microphone', 'VideoCamera', 'Headphones',
    'ChatCircleDots', 'ChatTeardrop', 'Translate', 'UserCircle',
  ],
  media: [
    'Play', 'Pause', 'Stop', 'Camera', 'MusicNote', 'MusicNotes',
    'Image', 'FilmSlate', 'Monitor', 'Desktop', 'Tv', 'Radio',
    'Headphones', 'SpeakerHigh', 'Microphone', 'Record', 'Disc',
    'Playlist', 'Queue', 'Repeat', 'Shuffle', 'SkipForward',
    'Screencast', 'PictureInPicture', 'Aperture',
  ],
  arrows: [
    'ArrowRight', 'ArrowLeft', 'ArrowUp', 'ArrowDown', 'ArrowsClockwise',
    'ArrowSquareOut', 'ArrowBendUpRight', 'CaretRight', 'Compass',
    'MapPin', 'NavigationArrow', 'Signpost', 'Path', 'Crosshair',
    'Target', 'ArrowFatRight', 'ArrowCircleRight', 'ArrowElbowRight',
    'ArrowLineRight', 'ArrowUUpRight', 'Cursor', 'CursorClick',
    'GitDiff', 'Swap', 'Shuffle',
  ],
  nature: [
    'Sun', 'Moon', 'Cloud', 'CloudSun', 'CloudRain', 'Snowflake',
    'Tree', 'Leaf', 'Flower', 'Drop', 'Wind', 'Thermometer',
    'Mountains', 'Wave', 'Rainbow', 'Planet', 'Grains', 'Paw',
    'Bird', 'Butterfly', 'Cat', 'Dog', 'Fish', 'Horse',
    'Bug', 'Cactus',
  ],
  business: [
    'ChartBar', 'ChartLine', 'ChartPie', 'Calendar', 'CalendarCheck',
    'Money', 'CurrencyDollar', 'Bank', 'Buildings', 'Storefront',
    'Receipt', 'Invoice', 'CreditCard', 'Scales', 'Gavel',
    'Presentation', 'Strategy', 'TrendUp', 'TrendDown', 'Percent',
    'Calculator', 'ClipboardText', 'Newspaper', 'Kanban', 'ListChecks',
  ],
}

/** Flat set of all curated icon names for quick lookup */
export const CURATED_ICON_SET = new Set(
  Object.values(CURATED_ICON_CATEGORIES).flat()
)
```

- [ ] **Step 2: Write WorkspaceIcon test**

Create `spa/src/features/workspace/components/WorkspaceIcon.test.tsx`:

```typescript
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { WorkspaceIcon } from './WorkspaceIcon'

describe('WorkspaceIcon', () => {
  beforeEach(() => { cleanup() })

  it('shows first char of name when icon is undefined', () => {
    render(<WorkspaceIcon icon={undefined} name="Default" size={18} />)
    expect(screen.getByText('D')).toBeInTheDocument()
  })

  it('shows single-char icon as text (legacy)', () => {
    render(<WorkspaceIcon icon="X" name="Test" size={18} />)
    expect(screen.getByText('X')).toBeInTheDocument()
  })

  it('shows emoji icon as text (legacy)', () => {
    render(<WorkspaceIcon icon="🚀" name="Test" size={18} />)
    expect(screen.getByText('🚀')).toBeInTheDocument()
  })

  it('shows first char as fallback for Phosphor icon name (suspense)', () => {
    // Phosphor icon names are multi-char — during lazy load, fallback to name.charAt(0)
    render(<WorkspaceIcon icon="Rocket" name="Test" size={18} />)
    // In test env, lazy import won't resolve — should show fallback
    expect(screen.getByText('T')).toBeInTheDocument()
  })
})
```

- [ ] **Step 3: Implement WorkspaceIcon**

Create `spa/src/features/workspace/components/WorkspaceIcon.tsx`:

```tsx
import { Suspense, lazy, useMemo } from 'react'
import type { Icon } from '@phosphor-icons/react'
import { CURATED_ICON_SET } from '../constants'
import { iconLoaders } from '../generated/icon-loader'

/** Cache of resolved lazy components to avoid re-creating on every render */
const lazyCache = new Map<string, React.LazyExoticComponent<Icon>>()

function getLazyIcon(name: string): React.LazyExoticComponent<Icon> | null {
  if (lazyCache.has(name)) return lazyCache.get(name)!
  const loader = iconLoaders[name]
  if (!loader) return null
  const LazyComponent = lazy(() => loader().then((comp) => ({ default: comp })))
  lazyCache.set(name, LazyComponent)
  return LazyComponent
}

function isPhosphorName(icon: string): boolean {
  return icon.length > 1 && /^[A-Z]/.test(icon)
}

interface Props {
  icon: string | undefined
  name: string
  size: number
  className?: string
}

export function WorkspaceIcon({ icon, name, size, className }: Props) {
  const fallbackChar = name.charAt(0) || '?'

  // No icon → first char
  if (!icon) {
    return <span className={className} style={{ fontSize: size * 0.6 }}>{fallbackChar}</span>
  }

  // Legacy single-char or emoji → render as text
  if (!isPhosphorName(icon)) {
    return <span className={className} style={{ fontSize: size * 0.6 }}>{icon}</span>
  }

  // Phosphor icon name → lazy load
  const LazyIcon = getLazyIcon(icon)
  if (!LazyIcon) {
    return <span className={className} style={{ fontSize: size * 0.6 }}>{fallbackChar}</span>
  }

  return (
    <Suspense fallback={<span className={className} style={{ fontSize: size * 0.6 }}>{fallbackChar}</span>}>
      <LazyIcon size={size} className={className} />
    </Suspense>
  )
}
```

- [ ] **Step 4: Run tests**

Run: `cd spa && npx vitest run src/features/workspace/components/WorkspaceIcon.test.tsx`

Expected: All 4 tests pass. The Phosphor test shows fallback because lazy import doesn't resolve in vitest.

- [ ] **Step 5: Commit**

```bash
git add spa/src/features/workspace/constants.ts spa/src/features/workspace/components/WorkspaceIcon.tsx spa/src/features/workspace/components/WorkspaceIcon.test.tsx
git commit -m "feat: curated icon categories + WorkspaceIcon component with lazy loading"
```

---

## Task 4: WorkspaceChip redesign (dropdown header style)

**Files:**
- Rewrite: `spa/src/features/workspace/components/WorkspaceChip.tsx`
- Modify: `spa/src/features/workspace/components/WorkspaceChip.test.tsx`
- Modify: `spa/src/features/workspace/components/ActivityBar.tsx`
- Modify: `spa/src/features/workspace/components/ActivityBar.test.tsx`

- [ ] **Step 1: Update WorkspaceChip test**

Rewrite `spa/src/features/workspace/components/WorkspaceChip.test.tsx`:

```typescript
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { WorkspaceChip } from './WorkspaceChip'

describe('WorkspaceChip', () => {
  beforeEach(() => { cleanup() })

  it('renders workspace name', () => {
    render(<WorkspaceChip name="My Workspace" color="#7a6aaa" icon={undefined} onClick={vi.fn()} onContextMenu={vi.fn()} />)
    expect(screen.getByText('My Workspace')).toBeInTheDocument()
  })

  it('renders icon square with first char when no icon', () => {
    render(<WorkspaceChip name="Default" color="#7a6aaa" icon={undefined} onClick={vi.fn()} onContextMenu={vi.fn()} />)
    expect(screen.getByTestId('workspace-chip-icon')).toBeInTheDocument()
    expect(screen.getByText('D')).toBeInTheDocument()
  })

  it('calls onClick when clicked', () => {
    const onClick = vi.fn()
    render(<WorkspaceChip name="WS" color="#aaa" icon={undefined} onClick={onClick} onContextMenu={vi.fn()} />)
    fireEvent.click(screen.getByRole('button'))
    expect(onClick).toHaveBeenCalled()
  })

  it('calls onContextMenu on right click', () => {
    const onContextMenu = vi.fn()
    render(<WorkspaceChip name="WS" color="#aaa" icon={undefined} onClick={vi.fn()} onContextMenu={onContextMenu} />)
    fireEvent.contextMenu(screen.getByRole('button'))
    expect(onContextMenu).toHaveBeenCalled()
  })

  it('does not render when name is null', () => {
    const { container } = render(<WorkspaceChip name={null} color={null} icon={undefined} onClick={vi.fn()} onContextMenu={vi.fn()} />)
    expect(container.firstChild).toBeNull()
  })

  it('renders separator div', () => {
    render(<WorkspaceChip name="WS" color="#aaa" icon={undefined} onClick={vi.fn()} onContextMenu={vi.fn()} />)
    expect(screen.getByTestId('workspace-chip-separator')).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Rewrite WorkspaceChip**

Rewrite `spa/src/features/workspace/components/WorkspaceChip.tsx`:

```tsx
import { CaretDown } from '@phosphor-icons/react'
import { WorkspaceIcon } from './WorkspaceIcon'

interface Props {
  name: string | null
  color: string | null
  icon: string | undefined
  onClick: () => void
  onContextMenu: (e: React.MouseEvent) => void
}

export function WorkspaceChip({ name, color, icon, onClick, onContextMenu }: Props) {
  if (!name) return null
  const c = color ?? '#888'
  return (
    <div className="flex items-center flex-shrink-0">
      <button
        onClick={onClick}
        onContextMenu={onContextMenu}
        className="flex items-center gap-1.5 px-2 py-1 rounded-md cursor-pointer transition-colors hover:bg-surface-hover"
      >
        {/* Icon square */}
        <div
          data-testid="workspace-chip-icon"
          className="w-5 h-5 rounded flex items-center justify-center"
          style={{ backgroundColor: c + '66', color: c }}
        >
          <WorkspaceIcon icon={icon} name={name} size={12} />
        </div>
        {/* Name */}
        <span className="truncate max-w-28 text-[13px] font-semibold" style={{ color: c }}>
          {name}
        </span>
        {/* Chevron */}
        <CaretDown size={10} className="flex-shrink-0 opacity-30" />
      </button>
      {/* Separator */}
      <div data-testid="workspace-chip-separator" className="w-px h-5.5 bg-border-default mx-2 flex-shrink-0" />
    </div>
  )
}
```

- [ ] **Step 3: Update ActivityBar to use WorkspaceIcon**

In `spa/src/features/workspace/components/ActivityBar.tsx`, replace the workspace button icon rendering.

Change line 70 from:
```tsx
{ws.icon ?? ws.name.charAt(0)}
```
to:
```tsx
<WorkspaceIcon icon={ws.icon} name={ws.name} size={14} />
```

Add import at top:
```tsx
import { WorkspaceIcon } from './WorkspaceIcon'
```

- [ ] **Step 4: Update App.tsx WorkspaceChip props (both locations)**

In `spa/src/App.tsx`, add `icon` prop to both WorkspaceChip usages.

Electron titlebar (around line 226):
```tsx
<WorkspaceChip
  name={activeWs.name}
  color={activeWs.color}
  icon={activeWs.icon}
  onClick={() => {}}
  onContextMenu={(e) => handleWsContextMenu(e, activeWs.id)}
/>
```

SPA tabbar (around line 286):
```tsx
<WorkspaceChip
  name={activeWs.name}
  color={activeWs.color}
  icon={activeWs.icon}
  onClick={() => {}}
  onContextMenu={(e) => handleWsContextMenu(e, activeWs.id)}
/>
```

(onClick will be wired to settings page in Task 8.)

- [ ] **Step 5: Run tests**

Run: `cd spa && npx vitest run src/features/workspace/components/WorkspaceChip.test.tsx src/features/workspace/components/ActivityBar.test.tsx`

Expected: All pass.

- [ ] **Step 6: Commit**

```bash
git add spa/src/features/workspace/components/WorkspaceChip.tsx spa/src/features/workspace/components/WorkspaceChip.test.tsx spa/src/features/workspace/components/ActivityBar.tsx spa/src/App.tsx
git commit -m "feat: redesign WorkspaceChip to dropdown header style + use WorkspaceIcon in ActivityBar"
```

---

## Task 5: Extract inline ColorGrid from WorkspaceColorPicker

**Files:**
- Modify: `spa/src/features/workspace/components/WorkspaceColorPicker.tsx`
- Modify: `spa/src/features/workspace/components/WorkspaceColorPicker.test.tsx`

- [ ] **Step 1: Refactor WorkspaceColorPicker to extract ColorGrid**

Rewrite `spa/src/features/workspace/components/WorkspaceColorPicker.tsx`:

```tsx
import { Check } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { WORKSPACE_COLORS } from '../constants'

interface ColorGridProps {
  currentColor: string
  onSelect: (color: string) => void
}

/** Inline color grid — used in both modal and settings page */
export function ColorGrid({ currentColor, onSelect }: ColorGridProps) {
  return (
    <div className="grid grid-cols-6 gap-2">
      {WORKSPACE_COLORS.map((color) => (
        <button key={color} data-color={color} aria-pressed={color === currentColor} onClick={() => onSelect(color)}
          className={`w-8 h-8 rounded-full cursor-pointer flex items-center justify-center transition-transform hover:scale-110 ${
            color === currentColor ? 'ring-2 ring-white ring-offset-2 ring-offset-surface-secondary' : ''
          }`} style={{ backgroundColor: color }}>
          {color === currentColor && <Check size={14} className="text-white" />}
        </button>
      ))}
    </div>
  )
}

interface PickerProps {
  currentColor: string
  onSelect: (color: string) => void
  onCancel: () => void
}

/** Modal wrapper — used in context menu flow */
export function WorkspaceColorPicker({ currentColor, onSelect, onCancel }: PickerProps) {
  const t = useI18nStore((s) => s.t)
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
      <div className="bg-surface-secondary border border-border-default rounded-lg shadow-xl w-full max-w-xs mx-4 p-5">
        <h3 className="text-sm font-semibold text-text-primary mb-3">{t('workspace.change_color')}</h3>
        <ColorGrid currentColor={currentColor} onSelect={onSelect} />
        <div className="flex justify-end mt-4">
          <button onClick={onCancel} className="px-3 py-1.5 rounded text-xs bg-surface-tertiary text-text-secondary hover:text-text-primary cursor-pointer">
            {t('common.cancel')}
          </button>
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Run existing tests**

Run: `cd spa && npx vitest run src/features/workspace/components/WorkspaceColorPicker.test.tsx`

Expected: All 3 tests pass (ColorGrid is an internal detail, tests access via parent).

- [ ] **Step 3: Commit**

```bash
git add spa/src/features/workspace/components/WorkspaceColorPicker.tsx
git commit -m "refactor: extract inline ColorGrid from WorkspaceColorPicker"
```

---

## Task 6: Phosphor Icons Picker

**Files:**
- Rewrite: `spa/src/features/workspace/components/WorkspaceIconPicker.tsx`
- Rewrite: `spa/src/features/workspace/components/WorkspaceIconPicker.test.tsx`

- [ ] **Step 1: Write tests for new picker**

Rewrite `spa/src/features/workspace/components/WorkspaceIconPicker.test.tsx`:

```typescript
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { WorkspaceIconPicker } from './WorkspaceIconPicker'
import { CURATED_ICON_CATEGORIES } from '../constants'

const firstCategory = Object.keys(CURATED_ICON_CATEGORIES)[0]
const firstCategoryIcons = CURATED_ICON_CATEGORIES[firstCategory]

describe('WorkspaceIconPicker', () => {
  beforeEach(() => { cleanup() })

  it('renders category tabs', () => {
    render(<WorkspaceIconPicker currentIcon={undefined} onSelect={vi.fn()} onCancel={vi.fn()} />)
    for (const cat of Object.keys(CURATED_ICON_CATEGORIES)) {
      expect(screen.getByTestId(`category-${cat}`)).toBeInTheDocument()
    }
  })

  it('renders icon grid for default category', () => {
    render(<WorkspaceIconPicker currentIcon={undefined} onSelect={vi.fn()} onCancel={vi.fn()} />)
    const buttons = screen.getAllByRole('button').filter((b) => b.getAttribute('data-icon'))
    expect(buttons.length).toBe(firstCategoryIcons.length)
  })

  it('calls onSelect with icon name', () => {
    const onSelect = vi.fn()
    render(<WorkspaceIconPicker currentIcon={undefined} onSelect={onSelect} onCancel={vi.fn()} />)
    const buttons = screen.getAllByRole('button').filter((b) => b.getAttribute('data-icon'))
    fireEvent.click(buttons[0])
    expect(onSelect).toHaveBeenCalledWith(firstCategoryIcons[0])
  })

  it('filters icons by search text', () => {
    render(<WorkspaceIconPicker currentIcon={undefined} onSelect={vi.fn()} onCancel={vi.fn()} />)
    const search = screen.getByPlaceholderText(/search/i)
    fireEvent.change(search, { target: { value: 'House' } })
    const buttons = screen.getAllByRole('button').filter((b) => b.getAttribute('data-icon'))
    // Should find at least 'House' in curated results
    expect(buttons.length).toBeGreaterThanOrEqual(1)
    expect(buttons.some((b) => b.getAttribute('data-icon') === 'House')).toBe(true)
  })

  it('renders clear button and calls onSelect with empty string', () => {
    const onSelect = vi.fn()
    render(<WorkspaceIconPicker currentIcon="Star" onSelect={onSelect} onCancel={vi.fn()} />)
    const clearBtn = screen.getByTestId('clear-icon')
    fireEvent.click(clearBtn)
    expect(onSelect).toHaveBeenCalledWith('')
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd spa && npx vitest run src/features/workspace/components/WorkspaceIconPicker.test.tsx`

Expected: FAIL (old component doesn't match new tests).

- [ ] **Step 3: Implement new WorkspaceIconPicker**

Rewrite `spa/src/features/workspace/components/WorkspaceIconPicker.tsx`:

```tsx
import { useState, useMemo, Suspense, lazy } from 'react'
import { MagnifyingGlass, X } from '@phosphor-icons/react'
import type { Icon } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { CURATED_ICON_CATEGORIES, CURATED_ICON_SET } from '../constants'
import { ALL_ICON_NAMES, iconLoaders } from '../generated/icon-loader'

/** Cache of resolved lazy components */
const lazyCache = new Map<string, React.LazyExoticComponent<Icon>>()

function getLazy(name: string): React.LazyExoticComponent<Icon> | null {
  if (lazyCache.has(name)) return lazyCache.get(name)!
  const loader = iconLoaders[name]
  if (!loader) return null
  const L = lazy(() => loader().then((comp) => ({ default: comp })))
  lazyCache.set(name, L)
  return L
}

function IconCell({ name, selected, onSelect }: { name: string; selected: boolean; onSelect: () => void }) {
  const LazyIcon = getLazy(name)
  return (
    <button
      data-icon={name}
      title={name}
      aria-pressed={selected}
      onClick={onSelect}
      className={`w-8 h-8 rounded-md flex items-center justify-center cursor-pointer transition-colors ${
        selected
          ? 'bg-accent/20 ring-2 ring-accent text-text-primary'
          : 'bg-surface-tertiary text-text-secondary hover:text-text-primary hover:bg-surface-hover'
      }`}
    >
      {LazyIcon ? (
        <Suspense fallback={<span className="text-[10px] opacity-40">{name.charAt(0)}</span>}>
          <LazyIcon size={18} />
        </Suspense>
      ) : (
        <span className="text-xs">{name.charAt(0)}</span>
      )}
    </button>
  )
}

const categoryLabels: Record<string, string> = {
  general: 'General', development: 'Dev', objects: 'Objects',
  communication: 'Chat', media: 'Media', arrows: 'Arrows',
  nature: 'Nature', business: 'Biz',
}

interface Props {
  currentIcon: string | undefined
  onSelect: (icon: string) => void
  onCancel: () => void
  inline?: boolean
}

export function WorkspaceIconPicker({ currentIcon, onSelect, onCancel, inline }: Props) {
  const t = useI18nStore((s) => s.t)
  const categories = Object.keys(CURATED_ICON_CATEGORIES)
  const [activeCategory, setActiveCategory] = useState(categories[0])
  const [search, setSearch] = useState('')

  const displayIcons = useMemo(() => {
    if (!search.trim()) return CURATED_ICON_CATEGORIES[activeCategory] ?? []
    const q = search.trim().toLowerCase()
    // Search curated first, then full library
    const curated = Object.values(CURATED_ICON_CATEGORIES).flat()
      .filter((n) => n.toLowerCase().includes(q))
    const full = ALL_ICON_NAMES
      .filter((n) => n.toLowerCase().includes(q) && !CURATED_ICON_SET.has(n))
    return [...curated, ...full].slice(0, 100) // cap at 100 results
  }, [search, activeCategory])

  const content = (
    <div className={inline ? '' : 'bg-surface-secondary border border-border-default rounded-lg shadow-xl w-full max-w-sm mx-4 p-5'}>
      {!inline && <h3 className="text-sm font-semibold text-text-primary mb-3">{t('workspace.change_icon')}</h3>}

      {/* Search */}
      <div className="relative mb-3">
        <MagnifyingGlass size={14} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-text-tertiary" />
        <input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search icons..."
          className="w-full pl-8 pr-3 py-1.5 bg-surface-tertiary border border-border-subtle rounded-md text-xs text-text-primary placeholder:text-text-tertiary focus:outline-none focus:ring-1 focus:ring-accent"
        />
      </div>

      {/* Category tabs (hidden during search) */}
      {!search.trim() && (
        <div className="flex flex-wrap gap-1 mb-3">
          {categories.map((cat) => (
            <button
              key={cat}
              data-testid={`category-${cat}`}
              onClick={() => setActiveCategory(cat)}
              className={`px-2 py-0.5 rounded text-[11px] cursor-pointer transition-colors ${
                activeCategory === cat
                  ? 'bg-accent/20 text-accent font-semibold'
                  : 'text-text-secondary hover:text-text-primary hover:bg-surface-hover'
              }`}
            >
              {categoryLabels[cat] ?? cat}
            </button>
          ))}
        </div>
      )}

      {/* Icon grid */}
      <div className="grid grid-cols-8 gap-1.5 max-h-48 overflow-y-auto">
        {displayIcons.map((name) => (
          <IconCell
            key={name}
            name={name}
            selected={name === currentIcon}
            onSelect={() => onSelect(name)}
          />
        ))}
      </div>

      {/* Clear + cancel */}
      <div className="flex items-center justify-between mt-3">
        <button
          data-testid="clear-icon"
          onClick={() => onSelect('')}
          className="flex items-center gap-1 px-2 py-1 rounded text-xs text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer"
        >
          <X size={12} />
          {t('common.clear') ?? 'Clear'}
        </button>
        {!inline && (
          <button onClick={onCancel} className="px-3 py-1.5 rounded text-xs bg-surface-tertiary text-text-secondary hover:text-text-primary cursor-pointer">
            {t('common.cancel')}
          </button>
        )}
      </div>
    </div>
  )

  if (inline) return content
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
      {content}
    </div>
  )
}
```

- [ ] **Step 4: Run tests**

Run: `cd spa && npx vitest run src/features/workspace/components/WorkspaceIconPicker.test.tsx`

Expected: All 5 tests pass.

- [ ] **Step 5: Commit**

```bash
git add spa/src/features/workspace/components/WorkspaceIconPicker.tsx spa/src/features/workspace/components/WorkspaceIconPicker.test.tsx
git commit -m "feat: rewrite WorkspaceIconPicker with Phosphor categories + search"
```

---

## Task 7: WorkspaceSettingsPage

**Files:**
- Create: `spa/src/features/workspace/components/WorkspaceSettingsPage.tsx`
- Create: `spa/src/features/workspace/components/WorkspaceSettingsPage.test.tsx`
- Modify: `spa/src/components/SettingsPage.tsx`

- [ ] **Step 1: Write tests**

Create `spa/src/features/workspace/components/WorkspaceSettingsPage.test.tsx`:

```typescript
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { WorkspaceSettingsPage } from './WorkspaceSettingsPage'
import { useWorkspaceStore } from '../store'

describe('WorkspaceSettingsPage', () => {
  let wsId: string

  beforeEach(() => {
    cleanup()
    useWorkspaceStore.getState().reset()
    const ws = useWorkspaceStore.getState().addWorkspace('Test WS', { color: '#7a6aaa' })
    wsId = ws.id
  })

  it('renders workspace name in editable input', () => {
    render(<WorkspaceSettingsPage workspaceId={wsId} />)
    const input = screen.getByDisplayValue('Test WS')
    expect(input).toBeInTheDocument()
  })

  it('updates workspace name on input change + blur', () => {
    render(<WorkspaceSettingsPage workspaceId={wsId} />)
    const input = screen.getByDisplayValue('Test WS')
    fireEvent.change(input, { target: { value: 'Renamed' } })
    fireEvent.blur(input)
    expect(useWorkspaceStore.getState().workspaces[0].name).toBe('Renamed')
  })

  it('renders color grid', () => {
    render(<WorkspaceSettingsPage workspaceId={wsId} />)
    const colorBtns = screen.getAllByRole('button').filter((b) => b.getAttribute('data-color'))
    expect(colorBtns.length).toBe(12) // WORKSPACE_COLORS.length
  })

  it('renders delete button', () => {
    render(<WorkspaceSettingsPage workspaceId={wsId} />)
    expect(screen.getByTestId('delete-workspace-btn')).toBeInTheDocument()
  })

  it('shows "not found" when workspace does not exist', () => {
    render(<WorkspaceSettingsPage workspaceId="nonexistent" />)
    expect(screen.getByText(/not found/i)).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd spa && npx vitest run src/features/workspace/components/WorkspaceSettingsPage.test.tsx`

Expected: FAIL (file doesn't exist yet).

- [ ] **Step 3: Implement WorkspaceSettingsPage**

Create `spa/src/features/workspace/components/WorkspaceSettingsPage.tsx`:

```tsx
import { useState, useCallback } from 'react'
import { Trash } from '@phosphor-icons/react'
import { useWorkspaceStore } from '../store'
import { useI18nStore } from '../../../stores/useI18nStore'
import { WorkspaceIcon } from './WorkspaceIcon'
import { ColorGrid } from './WorkspaceColorPicker'
import { WorkspaceIconPicker } from './WorkspaceIconPicker'

interface Props {
  workspaceId: string
}

export function WorkspaceSettingsPage({ workspaceId }: Props) {
  const t = useI18nStore((s) => s.t)
  const ws = useWorkspaceStore((s) => s.workspaces.find((w) => w.id === workspaceId))
  const renameWorkspace = useWorkspaceStore((s) => s.renameWorkspace)
  const setWorkspaceColor = useWorkspaceStore((s) => s.setWorkspaceColor)
  const setWorkspaceIcon = useWorkspaceStore((s) => s.setWorkspaceIcon)

  const [nameInput, setNameInput] = useState(ws?.name ?? '')
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false)

  const handleNameBlur = useCallback(() => {
    const trimmed = nameInput.trim()
    if (trimmed && trimmed !== ws?.name) {
      renameWorkspace(workspaceId, trimmed)
    } else {
      setNameInput(ws?.name ?? '')
    }
  }, [nameInput, ws?.name, workspaceId, renameWorkspace])

  const handleNameKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Enter') (e.target as HTMLInputElement).blur()
  }, [])

  const handleIconSelect = useCallback((icon: string) => {
    if (icon === '') {
      // Clear icon — store accepts undefined but setWorkspaceIcon takes string
      // Use empty string to signal clear, store should handle
      setWorkspaceIcon(workspaceId, '')
    } else {
      setWorkspaceIcon(workspaceId, icon)
    }
  }, [workspaceId, setWorkspaceIcon])

  if (!ws) {
    return (
      <div className="flex items-center justify-center h-full text-text-secondary">
        Workspace not found
      </div>
    )
  }

  return (
    <div className="h-full overflow-y-auto">
      <div className="max-w-lg mx-auto px-6 py-10">
        {/* Header: Icon + Name */}
        <div className="flex flex-col items-center gap-3 mb-8">
          <div
            className="w-16 h-16 rounded-xl flex items-center justify-center text-2xl"
            style={{ backgroundColor: ws.color + '33', color: ws.color }}
          >
            <WorkspaceIcon icon={ws.icon} name={ws.name} size={32} />
          </div>
          <input
            value={nameInput}
            onChange={(e) => setNameInput(e.target.value)}
            onBlur={handleNameBlur}
            onKeyDown={handleNameKeyDown}
            className="text-center text-lg font-semibold bg-transparent text-text-primary border-b border-transparent hover:border-border-default focus:border-accent focus:outline-none px-2 py-1 transition-colors"
          />
        </div>

        {/* Color */}
        <section className="mb-8">
          <h3 className="text-xs font-semibold text-text-secondary uppercase tracking-wider mb-3">
            {t('workspace.change_color') ?? 'Color'}
          </h3>
          <ColorGrid currentColor={ws.color} onSelect={(color) => setWorkspaceColor(workspaceId, color)} />
        </section>

        {/* Icon */}
        <section className="mb-8">
          <h3 className="text-xs font-semibold text-text-secondary uppercase tracking-wider mb-3">
            {t('workspace.change_icon') ?? 'Icon'}
          </h3>
          <WorkspaceIconPicker
            currentIcon={ws.icon}
            onSelect={handleIconSelect}
            onCancel={() => {}}
            inline
          />
        </section>

        {/* Danger Zone */}
        <section className="border-t border-border-subtle pt-6">
          <h3 className="text-xs font-semibold text-red-400 uppercase tracking-wider mb-3">
            Danger Zone
          </h3>
          <button
            data-testid="delete-workspace-btn"
            onClick={() => setShowDeleteConfirm(true)}
            className="flex items-center gap-2 px-4 py-2 rounded-md border border-red-500/30 text-red-400 text-sm hover:bg-red-500/10 cursor-pointer transition-colors"
          >
            <Trash size={16} />
            {t('workspace.delete') ?? 'Delete Workspace'}
          </button>
          {/* Delete confirmation is handled by parent (App.tsx) via onDelete callback */}
        </section>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Wire SettingsPage scope routing**

Modify `spa/src/components/SettingsPage.tsx`:

```typescript
import { useState } from 'react'
import type { PaneRendererProps } from '../lib/pane-registry'
import { getSettingsSections } from '../lib/settings-section-registry'
import { SettingsSidebar } from './settings/SettingsSidebar'
import { WorkspaceSettingsPage } from '../features/workspace/components/WorkspaceSettingsPage'

let lastSection: string | null = null

// eslint-disable-next-line react-refresh/only-export-components
export function resetLastSection() { lastSection = null }

export function SettingsPage(props: PaneRendererProps) {
  // Workspace-scoped settings → delegate to WorkspaceSettingsPage
  const scope = props.pane.content.kind === 'settings' ? (props.pane.content as any).scope : 'global'
  if (typeof scope === 'object' && scope.workspaceId) {
    return <WorkspaceSettingsPage workspaceId={scope.workspaceId} />
  }

  // Global settings → existing logic
  return <GlobalSettingsPage />
}

function GlobalSettingsPage() {
  const sections = getSettingsSections()
  const [activeSection, setActiveSection] = useState(
    () => {
      if (lastSection && sections.some((s) => s.id === lastSection)) return lastSection
      return sections.find((s) => s.component)?.id ?? ''
    },
  )

  const handleSelectSection = (id: string) => {
    lastSection = id
    setActiveSection(id)
  }

  const ActiveComponent = sections.find((s) => s.id === activeSection)?.component

  return (
    <div className="flex h-full">
      <SettingsSidebar activeSection={activeSection} onSelectSection={handleSelectSection} />
      <div className="flex-1 overflow-y-auto p-6">
        {ActiveComponent && <ActiveComponent />}
      </div>
    </div>
  )
}
```

- [ ] **Step 5: Run tests**

Run: `cd spa && npx vitest run src/features/workspace/components/WorkspaceSettingsPage.test.tsx src/components/SettingsPage.test.tsx`

Expected: All pass.

- [ ] **Step 6: Commit**

```bash
git add spa/src/features/workspace/components/WorkspaceSettingsPage.tsx spa/src/features/workspace/components/WorkspaceSettingsPage.test.tsx spa/src/components/SettingsPage.tsx
git commit -m "feat: WorkspaceSettingsPage + SettingsPage scope routing"
```

---

## Task 8: Integration wiring (context menu, App.tsx, useRouteSync)

**Files:**
- Modify: `spa/src/features/workspace/components/WorkspaceContextMenu.tsx`
- Modify: `spa/src/hooks/useRouteSync.ts`
- Modify: `spa/src/features/workspace/index.ts`
- Modify: `spa/src/App.tsx`

- [ ] **Step 1: Add "Settings" to WorkspaceContextMenu**

In `spa/src/features/workspace/components/WorkspaceContextMenu.tsx`:

Add `onSettings` to Props interface:
```typescript
interface Props {
  position: { x: number; y: number }
  onRename: () => void
  onChangeColor: () => void
  onChangeIcon: () => void
  onSettings: () => void
  onDelete: () => void
  onClose: () => void
}
```

Update component parameter destructuring to include `onSettings`.

Add to menuItems array, before the separator:
```typescript
import { PencilSimple, Palette, Smiley, Trash, GearSix } from '@phosphor-icons/react'

const menuItems = [
  { label: t('workspace.rename'), icon: PencilSimple, onClick: onRename },
  { label: t('workspace.change_color'), icon: Palette, onClick: onChangeColor },
  { label: t('workspace.change_icon'), icon: Smiley, onClick: onChangeIcon },
  { label: t('nav.settings') ?? 'Settings', icon: GearSix, onClick: onSettings },
  { type: 'separator' as const },
  { label: t('workspace.delete'), icon: Trash, onClick: onDelete, danger: true },
]
```

- [ ] **Step 2: Fix useRouteSync workspace-settings activation**

In `spa/src/hooks/useRouteSync.ts`, find the `workspace-settings` case (around line 99):

```typescript
// BEFORE
case 'workspace-settings':
  openSingletonTab({ kind: 'settings', scope: { workspaceId: parsed.workspaceId } })
  break

// AFTER
case 'workspace-settings': {
  const { setActiveWorkspace } = useWorkspaceStore.getState()
  setActiveWorkspace(parsed.workspaceId)
  openSingletonTab({ kind: 'settings', scope: { workspaceId: parsed.workspaceId } })
  break
}
```

Add import at top if not present: `import { useWorkspaceStore } from '../features/workspace'`

- [ ] **Step 3: Update workspace index.ts exports**

In `spa/src/features/workspace/index.ts`, add new exports:

```typescript
export { WorkspaceSettingsPage } from './components/WorkspaceSettingsPage'
export { WorkspaceEmptyState } from './components/WorkspaceEmptyState'
export { WorkspaceIcon } from './components/WorkspaceIcon'
export { ColorGrid } from './components/WorkspaceColorPicker'
```

- [ ] **Step 4: Wire App.tsx — chip onClick, context menu, creation flow**

In `spa/src/App.tsx`:

**a) Helper function for opening workspace settings** (add near other handlers):

```typescript
const openWsSettings = useCallback((wsId: string) => {
  const tabId = useTabStore.getState().openSingletonTab({ kind: 'settings', scope: { workspaceId: wsId } })
  useWorkspaceStore.getState().insertTab(tabId, wsId)
  handleSelectTab(tabId)
}, [handleSelectTab])
```

**b) WorkspaceChip onClick** — replace `onClick={() => {}}` with `onClick={() => openWsSettings(activeWs.id)}` (BOTH Electron and SPA locations).

**c) WorkspaceContextMenu onSettings** — add `onSettings` prop to the existing `<WorkspaceContextMenu>` render in App.tsx. Find the render (search for `<WorkspaceContextMenu`) and add the prop alongside existing ones:

```tsx
onSettings={() => openWsSettings(wsContextMenu.wsId)}
```

All other existing props (`onRename`, `onChangeColor`, `onChangeIcon`, `onDelete`, `onClose`) remain unchanged.

**d) Creation flow** — modify `onAddWorkspace` handler to open settings page after creation:

```typescript
onAddWorkspace={() => {
  if (workspaces.length === 0 && tabOrder.length > 0) {
    const ws = useWorkspaceStore.getState().addWorkspace('Workspace 1')
    setMigrateDialog({ wsId: ws.id, wsName: ws.name })
    // Settings page opened after MigrateDialog completes (see below)
  } else {
    const count = workspaces.length + 1
    const ws = useWorkspaceStore.getState().addWorkspace(`Workspace ${count}`)
    openWsSettings(ws.id)
  }
}}
```

**e) MigrateDialog callbacks** — in App.tsx, find the MigrateTabsDialog's `onMigrate` and `onSkip` callbacks. Add `openWsSettings(migrateDialog!.wsId)` as the last line of each callback, after `setMigrateDialog(null)`:

```typescript
// In onMigrate callback, after existing logic:
setMigrateDialog(null)
openWsSettings(migrateDialog!.wsId)  // ← add this line

// In onSkip callback, after existing logic:
setMigrateDialog(null)
openWsSettings(migrateDialog!.wsId)  // ← add this line
```

- [ ] **Step 5: Run context menu test**

Run: `cd spa && npx vitest run src/features/workspace/components/WorkspaceContextMenu.test.tsx`

If test references old props, update to include `onSettings={vi.fn()}`.

- [ ] **Step 6: Run full test suite**

Run: `cd spa && npx vitest run`

Expected: All tests pass.

- [ ] **Step 7: Commit**

```bash
git add spa/src/features/workspace/components/WorkspaceContextMenu.tsx spa/src/hooks/useRouteSync.ts spa/src/features/workspace/index.ts spa/src/App.tsx
git commit -m "feat: wire workspace settings — chip click, context menu, creation flow, route sync"
```

---

## Task 9: Empty workspace state

**Files:**
- Create: `spa/src/features/workspace/components/WorkspaceEmptyState.tsx`
- Create: `spa/src/features/workspace/components/WorkspaceEmptyState.test.tsx`
- Modify: `spa/src/App.tsx`

- [ ] **Step 1: Write test**

Create `spa/src/features/workspace/components/WorkspaceEmptyState.test.tsx`:

```typescript
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { WorkspaceEmptyState } from './WorkspaceEmptyState'

describe('WorkspaceEmptyState', () => {
  beforeEach(() => { cleanup() })

  it('renders empty message', () => {
    render(<WorkspaceEmptyState />)
    expect(screen.getByText(/no tabs/i)).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Implement component**

Create `spa/src/features/workspace/components/WorkspaceEmptyState.tsx`:

```tsx
import { Plus } from '@phosphor-icons/react'

export function WorkspaceEmptyState() {
  return (
    <div className="flex-1 flex flex-col items-center justify-center text-text-secondary gap-3">
      <div className="text-sm">No tabs in this workspace</div>
      <div className="flex items-center gap-1.5 text-xs text-text-tertiary">
        <Plus size={12} />
        <span>Press + to create a tab</span>
      </div>
    </div>
  )
}
```

- [ ] **Step 3: Wire in App.tsx**

In `spa/src/App.tsx`, find the content area (around the `<TabContent>` render). Wrap it with a condition:

```tsx
import { WorkspaceEmptyState } from './features/workspace'

// Inside the content area div:
<div className="flex-1 flex overflow-hidden">
  {visibleTabIds.length === 0 && activeWorkspaceId !== null ? (
    <WorkspaceEmptyState />
  ) : (
    <TabContent
      activeTab={activeTab ?? null}
      allTabs={tabOrder.map((id) => tabs[id]).filter(Boolean)}
    />
  )}
</div>
```

- [ ] **Step 4: Run tests**

Run: `cd spa && npx vitest run src/features/workspace/components/WorkspaceEmptyState.test.tsx`

Expected: Pass.

- [ ] **Step 5: Run full test suite + lint**

Run: `cd spa && npx vitest run && pnpm run lint`

Expected: All pass, no lint errors.

- [ ] **Step 6: Commit**

```bash
git add spa/src/features/workspace/components/WorkspaceEmptyState.tsx spa/src/features/workspace/components/WorkspaceEmptyState.test.tsx spa/src/features/workspace/index.ts spa/src/App.tsx
git commit -m "feat: empty workspace state + conditional rendering in content area"
```

---

## Task 10: Final verification

- [ ] **Step 1: Run full test suite**

Run: `cd spa && npx vitest run`

Expected: All tests pass (should be 985+ tests).

- [ ] **Step 2: Run lint**

Run: `cd spa && pnpm run lint`

Expected: No errors.

- [ ] **Step 3: Build check**

Run: `cd spa && pnpm run build`

Expected: Build succeeds. Check for warnings about chunk sizes (Phosphor icons lazy chunks are expected).

- [ ] **Step 4: Manual smoke test**

Start dev server: `cd spa && pnpm run dev`

Verify:
1. WorkspaceChip shows dropdown header style (icon square + bold name + chevron + separator)
2. Click chip → opens workspace settings page
3. Right-click chip → context menu with "Settings" item
4. Workspace settings page: edit name, change color, change icon, delete
5. Icons picker: category tabs, search, clear
6. Switch workspaces → correct tab recalled
7. Switch to empty workspace → empty state shown
8. Create new workspace → settings page opens
9. ActivityBar shows WorkspaceIcon

- [ ] **Step 5: Commit any fixes from smoke test**

---

## Appendix: Store tweak (included in Task 7 scope)

In Task 7 Step 3, also modify `spa/src/features/workspace/store.ts` `setWorkspaceIcon`:

```typescript
// BEFORE
setWorkspaceIcon: (wsId, icon) =>
  set((state) => ({
    workspaces: state.workspaces.map((ws) =>
      ws.id === wsId ? { ...ws, icon } : ws,
    ),
  })),

// AFTER
setWorkspaceIcon: (wsId, icon) =>
  set((state) => ({
    workspaces: state.workspaces.map((ws) =>
      ws.id === wsId ? { ...ws, icon: icon || undefined } : ws,
    ),
  })),
```

This ensures empty string (from "Clear" button) resets to `undefined` (fallback to `name.charAt(0)`).
