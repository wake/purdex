# Icon System Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the React.lazy icon system (1,445 chunks, 192KB generated file, Suspense flicker) with SVG path data served as static JSON + Fuse.js fuzzy search + TanStack Virtual scrolling.

**Architecture:** Build-time script extracts SVG paths from `@phosphor-icons/core` into 6 per-weight JSON files in `public/icons/` and a metadata JSON in `src/` (bundled). Runtime fetches weight JSONs on demand via `icon-path-cache.ts`, renders icons as inline `<svg>` elements. WorkspaceIconPicker uses Fuse.js for tag-aware search and TanStack Virtual for efficient grid rendering.

**Tech Stack:** React 19, Vite 8, `@phosphor-icons/core` (devDependency), `fuse.js`, `@tanstack/react-virtual`

**Spec:** `docs/superpowers/specs/2026-04-13-icon-system-redesign.md`

---

## File Map

| Action | File | Responsibility |
|--------|------|----------------|
| Create | `spa/scripts/generate-icon-data.mjs` | Build-time: extract SVG paths + metadata from `@phosphor-icons/core` |
| Create | `spa/public/icons/{bold,regular,thin,light,fill,duotone}.json` | Static JSON: per-weight SVG path data (generated) |
| Create | `spa/src/features/workspace/generated/icon-meta.json` | Metadata: name/tags/categories for Fuse.js (generated, bundled) |
| Create | `spa/src/features/workspace/generated/icon-names.ts` | Export: `ICON_NAMES` string array (generated) |
| Create | `spa/src/features/workspace/lib/icon-path-cache.ts` | Runtime: fetch, cache, and serve SVG path data by weight |
| Create | `spa/src/features/workspace/lib/icon-path-cache.test.ts` | Tests for cache layer |
| Rewrite | `spa/src/features/workspace/components/WorkspaceIcon.tsx` | SVG path renderer (replaces React.lazy) |
| Rewrite | `spa/src/features/workspace/components/WorkspaceIcon.test.tsx` | Tests for SVG renderer |
| Rewrite | `spa/src/features/workspace/components/WorkspaceIconPicker.tsx` | Fuse.js search + TanStack Virtual grid |
| Rewrite | `spa/src/features/workspace/components/WorkspaceIconPicker.test.tsx` | Tests for picker |
| Modify | `spa/src/types/tab.ts` | Expand `IconWeight` to 6 weights |
| Modify | `spa/src/features/workspace/components/WorkspaceSettingsPage.tsx` | Expand weight selector to 6 weights |
| Modify | `spa/src/App.tsx` | Add `prefetchWeight('bold')` call |
| Modify | `spa/package.json` | Add deps + scripts |
| Delete | `spa/src/features/workspace/generated/icon-loader.ts` | Replaced by icon-meta.json + icon-names.ts |
| Delete | `spa/scripts/generate-icon-loader.mjs` | Replaced by generate-icon-data.mjs |
| Modify | 7 test files | Update mocks from `icon-loader` to `icon-path-cache` |

---

### Task 1: Install Dependencies + Add Scripts

**Files:**
- Modify: `spa/package.json`

- [ ] **Step 1: Install new dependencies**

```bash
cd spa && pnpm add fuse.js @tanstack/react-virtual && pnpm add -D @phosphor-icons/core
```

- [ ] **Step 2: Add generate scripts to package.json**

In `spa/package.json`, add to `"scripts"`:
```json
"generate:icons": "node scripts/generate-icon-data.mjs",
"predev": "node scripts/generate-icon-data.mjs",
"prebuild": "node scripts/generate-icon-data.mjs"
```

- [ ] **Step 3: Commit**

```bash
git add spa/package.json spa/pnpm-lock.yaml
git commit -m "chore: add fuse.js, @tanstack/react-virtual, @phosphor-icons/core"
```

---

### Task 2: Build-Time Data Generation Script

**Files:**
- Create: `spa/scripts/generate-icon-data.mjs`
- Create: `spa/public/icons/` (6 JSON files)
- Create: `spa/src/features/workspace/generated/icon-meta.json`
- Create: `spa/src/features/workspace/generated/icon-names.ts`

- [ ] **Step 1: Create the generation script**

Create `spa/scripts/generate-icon-data.mjs`:

```js
#!/usr/bin/env node
/**
 * Generates icon data from @phosphor-icons/core.
 * Outputs:
 *   - public/icons/{weight}.json     — SVG path data per weight (6 files)
 *   - src/.../generated/icon-meta.json — search metadata (bundled)
 *   - src/.../generated/icon-names.ts  — icon name array
 *
 * Run: node spa/scripts/generate-icon-data.mjs
 */
import { readdirSync, readFileSync, writeFileSync, mkdirSync } from 'fs'
import { join, dirname } from 'path'
import { fileURLToPath } from 'url'
import { createRequire } from 'module'

const __dirname = dirname(fileURLToPath(import.meta.url))
const require = createRequire(import.meta.url)
const { icons } = require('@phosphor-icons/core')
const coreRoot = dirname(require.resolve('@phosphor-icons/core/package.json'))

const publicDir = join(__dirname, '..', 'public', 'icons')
const genDir = join(__dirname, '..', 'src', 'features', 'workspace', 'generated')
mkdirSync(publicDir, { recursive: true })
mkdirSync(genDir, { recursive: true })

// Build name lookup: kebab-name → PascalName
const nameMap = new Map(icons.map((i) => [i.name, i.pascal_name]))

const WEIGHTS = ['bold', 'regular', 'thin', 'light', 'fill', 'duotone']
const SUFFIXES = { bold: '-bold', regular: '', thin: '-thin', light: '-light', fill: '-fill', duotone: '-duotone' }

// 1. Generate per-weight JSON
for (const weight of WEIGHTS) {
  const dir = join(coreRoot, 'assets', weight)
  const files = readdirSync(dir).filter((f) => f.endsWith('.svg'))
  const data = {}

  for (const f of files) {
    const suffix = SUFFIXES[weight]
    const baseName = suffix ? f.replace(suffix + '.svg', '') : f.replace('.svg', '')
    const pascalName = nameMap.get(baseName)
    if (!pascalName) continue

    const svg = readFileSync(join(dir, f), 'utf8')
    const pathEls = svg.match(/<path[^>]*\/>/g) || []
    const paths = pathEls.map((p) => {
      const d = p.match(/d="([^"]+)"/)
      const opacity = p.match(/opacity="([^"]+)"/)
      if (opacity) return { d: d?.[1] ?? '', o: parseFloat(opacity[1]) }
      return d?.[1] ?? ''
    })
    data[pascalName] = paths.length === 1 ? paths[0] : paths
  }

  writeFileSync(join(publicDir, `${weight}.json`), JSON.stringify(data))
  console.log(`  ${weight}.json: ${Object.keys(data).length} icons`)
}

// 2. Generate metadata JSON (bundled via Vite import)
const meta = icons.map((i) => ({
  n: i.pascal_name,
  t: i.tags.filter((t) => t !== '*new*'),
  c: i.categories,
}))
writeFileSync(join(genDir, 'icon-meta.json'), JSON.stringify(meta))
console.log(`  icon-meta.json: ${meta.length} entries`)

// 3. Generate icon-names.ts
const names = icons.map((i) => i.pascal_name).sort()
const tsLines = [
  '// Auto-generated by scripts/generate-icon-data.mjs — do not edit',
  `// Generated: ${new Date().toISOString()} — ${names.length} icons`,
  '',
  `export const ICON_NAMES: string[] = ${JSON.stringify(names)}`,
  '',
  'export const ICON_NAME_SET: Set<string> = new Set(ICON_NAMES)',
  '',
]
writeFileSync(join(genDir, 'icon-names.ts'), tsLines.join('\n'))
console.log(`  icon-names.ts: ${names.length} names`)

console.log('Done.')
```

- [ ] **Step 2: Run the script**

```bash
cd spa && node scripts/generate-icon-data.mjs
```

Expected output:
```
  bold.json: 1512 icons
  regular.json: 1512 icons
  thin.json: 1512 icons
  light.json: 1512 icons
  fill.json: 1512 icons
  duotone.json: 1512 icons
  icon-meta.json: 1512 entries
  icon-names.ts: 1512 names
```

- [ ] **Step 3: Verify generated files**

```bash
ls -lh public/icons/*.json
head -1 src/features/workspace/generated/icon-names.ts
node -e "const m=require('./public/icons/bold.json'); console.log('Acorn:', typeof m.Acorn, m.Acorn?.substring(0,30))"
node -e "const m=require('./public/icons/duotone.json'); console.log('Acorn duotone:', JSON.stringify(m.Acorn).substring(0,80))"
node -e "const m=require('./src/features/workspace/generated/icon-meta.json'); console.log('count:', m.length, 'sample:', JSON.stringify(m[0]))"
```

- [ ] **Step 4: Add public/icons/ to .gitignore**

`public/icons/*.json` 檔案很大（6 × ~600KB），不 commit，靠 `predev`/`prebuild` 自動生成。
`src/.../icon-meta.json` 和 `icon-names.ts` 則 **要 commit**（跟舊 `icon-loader.ts` 一樣），因為 vitest 需要能 resolve 這些 import。

在 `spa/.gitignore` 加入：
```
# Generated icon path data (regenerated by scripts/generate-icon-data.mjs)
public/icons/
```

- [ ] **Step 5: Commit**

```bash
git add spa/scripts/generate-icon-data.mjs spa/.gitignore \
  spa/src/features/workspace/generated/icon-meta.json \
  spa/src/features/workspace/generated/icon-names.ts
git commit -m "feat: add build-time icon data generation script"
```

---

### Task 3: Icon Path Cache Layer

**Files:**
- Create: `spa/src/features/workspace/lib/icon-path-cache.ts`
- Create: `spa/src/features/workspace/lib/icon-path-cache.test.ts`

- [ ] **Step 1: Write the failing tests**

Create `spa/src/features/workspace/lib/icon-path-cache.test.ts`:

```ts
import { describe, it, expect, vi, beforeEach } from 'vitest'

// Reset module state between tests
let prefetchWeight: typeof import('./icon-path-cache').prefetchWeight
let getIconPath: typeof import('./icon-path-cache').getIconPath
let isWeightLoaded: typeof import('./icon-path-cache').isWeightLoaded

beforeEach(async () => {
  vi.restoreAllMocks()
  // Re-import to reset module-level cache
  vi.resetModules()
  const mod = await import('./icon-path-cache')
  prefetchWeight = mod.prefetchWeight
  getIconPath = mod.getIconPath
  isWeightLoaded = mod.isWeightLoaded
})

describe('icon-path-cache', () => {
  it('fetches and caches weight data', async () => {
    const mockData = { Acorn: 'M0,0L10,10', Terminal: 'M5,5L20,20' }
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response(JSON.stringify(mockData), { status: 200 }),
    )

    await prefetchWeight('bold')
    expect(fetch).toHaveBeenCalledWith('/icons/bold.json')
    expect(isWeightLoaded('bold')).toBe(true)
    expect(getIconPath('Acorn', 'bold')).toBe('M0,0L10,10')
    expect(getIconPath('Unknown', 'bold')).toBeNull()
  })

  it('returns null for uncached weight', () => {
    expect(isWeightLoaded('thin')).toBe(false)
    expect(getIconPath('Acorn', 'thin')).toBeNull()
  })

  it('does not fetch again if already cached', async () => {
    const mockData = { Acorn: 'M0,0' }
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response(JSON.stringify(mockData), { status: 200 }),
    )

    await prefetchWeight('bold')
    await prefetchWeight('bold')
    expect(fetch).toHaveBeenCalledTimes(1)
  })

  it('deduplicates concurrent fetches for the same weight', async () => {
    const mockData = { Acorn: 'M0,0' }
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response(JSON.stringify(mockData), { status: 200 }),
    )

    await Promise.all([prefetchWeight('bold'), prefetchWeight('bold')])
    expect(fetch).toHaveBeenCalledTimes(1)
  })

  it('throws on fetch failure', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response('Not Found', { status: 404 }),
    )

    await expect(prefetchWeight('bold')).rejects.toThrow('Failed to fetch icon weight "bold": 404')
  })

  it('allows retry after fetch failure', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(new Response('Error', { status: 500 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ Acorn: 'M0,0' }), { status: 200 }))

    await expect(prefetchWeight('bold')).rejects.toThrow()
    expect(isWeightLoaded('bold')).toBe(false)

    await prefetchWeight('bold')
    expect(isWeightLoaded('bold')).toBe(true)
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd spa && npx vitest run src/features/workspace/lib/icon-path-cache.test.ts
```

Expected: FAIL (module not found)

- [ ] **Step 3: Write the implementation**

Create `spa/src/features/workspace/lib/icon-path-cache.ts`:

```ts
export type PathData = string | Array<string | { d: string; o: number }>
type WeightData = Record<string, PathData>

const cache = new Map<string, WeightData>()
const inflight = new Map<string, Promise<void>>()

/** Prefetch a weight's path data. Deduplicates concurrent calls for the same weight. */
export async function prefetchWeight(weight: string): Promise<void> {
  if (cache.has(weight)) return
  if (inflight.has(weight)) return inflight.get(weight)

  const promise = (async () => {
    const res = await fetch(`/icons/${weight}.json`)
    if (!res.ok) throw new Error(`Failed to fetch icon weight "${weight}": ${res.status}`)
    const data: WeightData = await res.json()
    cache.set(weight, data)
  })()

  inflight.set(weight, promise)
  try {
    await promise
  } finally {
    inflight.delete(weight)
  }
}

/** Sync path lookup — returns null if weight not yet cached */
export function getIconPath(name: string, weight: string): PathData | null {
  return cache.get(weight)?.[name] ?? null
}

/** Check if weight is loaded */
export function isWeightLoaded(weight: string): boolean {
  return cache.has(weight)
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd spa && npx vitest run src/features/workspace/lib/icon-path-cache.test.ts
```

Expected: 6 passing

- [ ] **Step 5: Commit**

```bash
git add spa/src/features/workspace/lib/icon-path-cache.ts spa/src/features/workspace/lib/icon-path-cache.test.ts
git commit -m "feat: add icon-path-cache with fetch, dedup, and error handling"
```

---

### Task 4: Expand IconWeight + Update WorkspaceSettingsPage

**Files:**
- Modify: `spa/src/types/tab.ts:40`
- Modify: `spa/src/features/workspace/components/WorkspaceSettingsPage.tsx:95`

- [ ] **Step 1: Expand IconWeight type**

In `spa/src/types/tab.ts`, change line 40:

```ts
// Before
export type IconWeight = 'bold' | 'duotone' | 'fill'

// After
export type IconWeight = 'bold' | 'regular' | 'thin' | 'light' | 'fill' | 'duotone'
```

- [ ] **Step 2: Update WorkspaceSettingsPage weight selector**

In `spa/src/features/workspace/components/WorkspaceSettingsPage.tsx`, change line 95:

```tsx
// Before
{(['bold', 'duotone', 'fill'] as const).map((w) => (

// After
{(['bold', 'regular', 'thin', 'light', 'fill', 'duotone'] as const).map((w) => (
```

- [ ] **Step 3: Run type check**

```bash
cd spa && npx tsc -b --noEmit
```

Expected: no errors

- [ ] **Step 4: Commit**

```bash
git add spa/src/types/tab.ts spa/src/features/workspace/components/WorkspaceSettingsPage.tsx
git commit -m "feat: expand IconWeight to all 6 Phosphor weights"
```

---

### Task 5: Rewrite WorkspaceIcon

**Files:**
- Rewrite: `spa/src/features/workspace/components/WorkspaceIcon.tsx`
- Rewrite: `spa/src/features/workspace/components/WorkspaceIcon.test.tsx`

- [ ] **Step 1: Write the failing tests**

Replace `spa/src/features/workspace/components/WorkspaceIcon.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'

vi.mock('../lib/icon-path-cache', () => ({
  getIconPath: (name: string) => {
    if (name === 'Rocket') return 'M0,0L10,10Z'
    if (name === 'Acorn') return [{ d: 'M0,0', o: 0.2 }, 'M5,5L20,20']
    return null
  },
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))

import { WorkspaceIcon } from './WorkspaceIcon'

describe('WorkspaceIcon', () => {
  beforeEach(() => cleanup())

  it('shows first char of name when icon is undefined', () => {
    render(<WorkspaceIcon icon={undefined} name="Default" size={18} />)
    expect(screen.getByText('D')).toBeInTheDocument()
  })

  it('shows emoji icon as text', () => {
    render(<WorkspaceIcon icon="🚀" name="Test" size={18} />)
    expect(screen.getByText('🚀')).toBeInTheDocument()
  })

  it('renders SVG for valid Phosphor icon name', () => {
    const { container } = render(<WorkspaceIcon icon="Rocket" name="Test" size={18} />)
    const svg = container.querySelector('svg')
    expect(svg).toBeInTheDocument()
    expect(svg?.getAttribute('viewBox')).toBe('0 0 256 256')
    expect(svg?.getAttribute('width')).toBe('18')
    const path = svg?.querySelector('path')
    expect(path?.getAttribute('d')).toBe('M0,0L10,10Z')
  })

  it('renders duotone SVG with multiple paths and opacity', () => {
    const { container } = render(<WorkspaceIcon icon="Acorn" name="Test" size={18} weight="duotone" />)
    const paths = container.querySelectorAll('path')
    expect(paths).toHaveLength(2)
    expect(paths[0].getAttribute('opacity')).toBe('0.2')
    expect(paths[1].getAttribute('d')).toBe('M5,5L20,20')
  })

  it('shows fallback when icon name has no path data', () => {
    render(<WorkspaceIcon icon="NonExistent" name="Foo" size={18} />)
    expect(screen.getByText('F')).toBeInTheDocument()
  })

  it('shows single-char icon as text (legacy)', () => {
    render(<WorkspaceIcon icon="X" name="Test" size={18} />)
    expect(screen.getByText('X')).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd spa && npx vitest run src/features/workspace/components/WorkspaceIcon.test.tsx
```

Expected: FAIL (import errors — old WorkspaceIcon still imports icon-loader)

- [ ] **Step 3: Write the implementation**

Replace `spa/src/features/workspace/components/WorkspaceIcon.tsx`:

```tsx
import { useEffect, useState } from 'react'
import type { IconWeight } from '../../../types/tab'
import { getIconPath, isWeightLoaded, prefetchWeight, type PathData } from '../lib/icon-path-cache'

function isPhosphorName(icon: string): boolean {
  return icon.length > 1 && /^[A-Z]/.test(icon)
}

interface Props {
  icon: string | undefined
  name: string
  size: number
  weight?: IconWeight
  className?: string
}

export function WorkspaceIcon({ icon, name, size, weight = 'bold', className }: Props) {
  const fallbackChar = name.charAt(0) || '?'
  const textStyle = { fontSize: size * 0.75 }
  const phosphorName = icon && isPhosphorName(icon) ? icon : null

  // Hooks must be called before any conditional returns (Rules of Hooks)
  const [, setTick] = useState(0)
  useEffect(() => {
    if (phosphorName && !isWeightLoaded(weight)) {
      prefetchWeight(weight).then(() => setTick((t) => t + 1)).catch(() => {})
    }
  }, [phosphorName, weight])

  if (!phosphorName) {
    return <span className={className} style={textStyle}>{icon || fallbackChar}</span>
  }

  const pathData = getIconPath(phosphorName, weight)
  if (!pathData) {
    return <span className={className} style={textStyle}>{fallbackChar}</span>
  }

  return (
    <svg width={size} height={size} viewBox="0 0 256 256" fill="currentColor" className={className}>
      {renderPaths(pathData)}
    </svg>
  )
}

function renderPaths(data: PathData) {
  if (typeof data === 'string') return <path d={data} />
  return data.map((p, i) =>
    typeof p === 'string'
      ? <path key={i} d={p} />
      : <path key={i} d={p.d} opacity={p.o} />,
  )
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd spa && npx vitest run src/features/workspace/components/WorkspaceIcon.test.tsx
```

Expected: 6 passing

- [ ] **Step 5: Commit**

```bash
git add spa/src/features/workspace/components/WorkspaceIcon.tsx spa/src/features/workspace/components/WorkspaceIcon.test.tsx
git commit -m "feat: rewrite WorkspaceIcon to use SVG path data"
```

---

### Task 6: Rewrite WorkspaceIconPicker

**Files:**
- Rewrite: `spa/src/features/workspace/components/WorkspaceIconPicker.tsx`
- Rewrite: `spa/src/features/workspace/components/WorkspaceIconPicker.test.tsx`

- [ ] **Step 1: Write the failing tests**

Replace `spa/src/features/workspace/components/WorkspaceIconPicker.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'

vi.mock('../lib/icon-path-cache', () => ({
  getIconPath: () => 'M0,0L10,10',
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))

vi.mock('../generated/icon-meta.json', () => ({
  default: [
    { n: 'House', t: ['home', 'building'], c: ['general'] },
    { n: 'Star', t: ['favorite', 'rating'], c: ['general'] },
    { n: 'Heart', t: ['love', 'like'], c: ['general'] },
    { n: 'Envelope', t: ['mail', 'email', 'message'], c: ['communication'] },
    { n: 'Terminal', t: ['console', 'cli', 'command'], c: ['development'] },
  ],
}))

import { WorkspaceIconPicker } from './WorkspaceIconPicker'
import { CURATED_ICON_CATEGORIES } from '../constants'

const firstCategory = Object.keys(CURATED_ICON_CATEGORIES)[0]
const firstCategoryIcons = CURATED_ICON_CATEGORIES[firstCategory]

describe('WorkspaceIconPicker', () => {
  beforeEach(() => cleanup())

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

  it('searches by tag (fuzzy) — "mail" finds Envelope', () => {
    render(<WorkspaceIconPicker currentIcon={undefined} onSelect={vi.fn()} onCancel={vi.fn()} />)
    const search = screen.getByPlaceholderText(/search/i)
    fireEvent.change(search, { target: { value: 'mail' } })
    const buttons = screen.getAllByRole('button').filter((b) => b.getAttribute('data-icon'))
    expect(buttons.some((b) => b.getAttribute('data-icon') === 'Envelope')).toBe(true)
  })

  it('searches by name — "term" finds Terminal', () => {
    render(<WorkspaceIconPicker currentIcon={undefined} onSelect={vi.fn()} onCancel={vi.fn()} />)
    const search = screen.getByPlaceholderText(/search/i)
    fireEvent.change(search, { target: { value: 'term' } })
    const buttons = screen.getAllByRole('button').filter((b) => b.getAttribute('data-icon'))
    expect(buttons.some((b) => b.getAttribute('data-icon') === 'Terminal')).toBe(true)
  })

  it('clears icon selection', () => {
    const onSelect = vi.fn()
    render(<WorkspaceIconPicker currentIcon="Star" onSelect={onSelect} onCancel={vi.fn()} />)
    fireEvent.click(screen.getByTestId('clear-icon'))
    expect(onSelect).toHaveBeenCalledWith('')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd spa && npx vitest run src/features/workspace/components/WorkspaceIconPicker.test.tsx
```

Expected: FAIL

- [ ] **Step 3: Write the implementation**

Replace `spa/src/features/workspace/components/WorkspaceIconPicker.tsx`:

```tsx
import { useState, useMemo, useRef } from 'react'
import { MagnifyingGlass, X } from '@phosphor-icons/react'
import Fuse from 'fuse.js'
import { useVirtualizer } from '@tanstack/react-virtual'
import { useI18nStore } from '../../../stores/useI18nStore'
import { CURATED_ICON_CATEGORIES } from '../constants'
import { getIconPath, isWeightLoaded, prefetchWeight } from '../lib/icon-path-cache'
import type { PathData } from '../lib/icon-path-cache'
import iconMetaData from '../generated/icon-meta.json'

interface IconMeta {
  n: string
  t: string[]
  c: string[]
}

const iconMeta: IconMeta[] = iconMetaData as IconMeta[]

const fuse = new Fuse(iconMeta, {
  keys: ['n', 't', 'c'],
  threshold: 0.3,
})

const COLS = 8

function renderPaths(data: PathData) {
  if (typeof data === 'string') return <path d={data} />
  return data.map((p, i) =>
    typeof p === 'string'
      ? <path key={i} d={p} />
      : <path key={i} d={p.d} opacity={p.o} />,
  )
}

function IconCell({
  name, selected, onSelect, weight,
}: {
  name: string; selected: boolean; onSelect: () => void; weight: string
}) {
  const pathData = getIconPath(name, weight)
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
      {pathData ? (
        <svg width={18} height={18} viewBox="0 0 256 256" fill="currentColor">
          {renderPaths(pathData)}
        </svg>
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
  const [weight, setWeight] = useState('bold')
  const scrollRef = useRef<HTMLDivElement>(null)

  // Ensure current weight is loaded
  if (!isWeightLoaded(weight)) {
    prefetchWeight(weight).catch(() => {})
  }

  const displayIcons = useMemo(() => {
    if (!search.trim()) return CURATED_ICON_CATEGORIES[activeCategory] ?? []
    return fuse.search(search.trim()).map((r) => r.item.n)
  }, [search, activeCategory])

  const rowCount = Math.ceil(displayIcons.length / COLS)

  const virtualizer = useVirtualizer({
    count: rowCount,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => 38, // 32px icon + 6px gap
    overscan: 3,
  })

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

      {/* Virtualized icon grid */}
      <div ref={scrollRef} className="max-h-48 overflow-y-auto p-0.5">
        <div
          style={{ height: virtualizer.getTotalSize(), position: 'relative' }}
        >
          {virtualizer.getVirtualItems().map((vRow) => {
            const startIdx = vRow.index * COLS
            const rowIcons = displayIcons.slice(startIdx, startIdx + COLS)
            return (
              <div
                key={vRow.key}
                className="flex gap-1.5"
                style={{
                  position: 'absolute',
                  top: 0,
                  left: 0,
                  transform: `translateY(${vRow.start}px)`,
                  height: vRow.size,
                }}
              >
                {rowIcons.map((name) => (
                  <IconCell
                    key={name}
                    name={name}
                    selected={name === currentIcon}
                    onSelect={() => onSelect(name)}
                    weight={weight}
                  />
                ))}
              </div>
            )
          })}
        </div>
      </div>

      {/* Clear + cancel */}
      <div className="flex items-center justify-between mt-3">
        <button
          data-testid="clear-icon"
          onClick={() => onSelect('')}
          className="flex items-center gap-1 px-2 py-1 rounded text-xs text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer"
        >
          <X size={12} />
          Clear
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

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd spa && npx vitest run src/features/workspace/components/WorkspaceIconPicker.test.tsx
```

Expected: 6 passing

- [ ] **Step 5: Commit**

```bash
git add spa/src/features/workspace/components/WorkspaceIconPicker.tsx spa/src/features/workspace/components/WorkspaceIconPicker.test.tsx
git commit -m "feat: rewrite WorkspaceIconPicker with Fuse.js search + TanStack Virtual"
```

---

### Task 7: Prefetch on App Startup

**Files:**
- Modify: `spa/src/App.tsx`

- [ ] **Step 1: Add prefetch call**

At the top of `spa/src/App.tsx`, add the import:

```ts
import { prefetchWeight } from './features/workspace/lib/icon-path-cache'
```

Then call it at module level (before the component, after imports):

```ts
// Prefetch default icon weight so WorkspaceIcon renders instantly
prefetchWeight('bold').catch(() => {})
```

- [ ] **Step 2: Run type check**

```bash
cd spa && npx tsc -b --noEmit
```

Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add spa/src/App.tsx
git commit -m "feat: prefetch bold icon weight on app startup"
```

---

### Task 8: Update Test Mocks (7 Files)

**Files:**
- Modify: `spa/src/features/workspace/components/ActivityBar.test.tsx`
- Modify: `spa/src/features/workspace/components/WorkspaceSettingsPage.test.tsx`
- Modify: `spa/src/components/SortableTab.test.tsx`
- Modify: `spa/src/components/SettingsPage.test.tsx`
- Modify: `spa/src/hooks/useRouteSync.test.ts`
- Modify: `spa/src/hooks/useShortcuts.test.ts`
- Modify: `spa/src/lib/register-modules.test.ts`

All these files currently mock `icon-loader`. Replace each mock with the new `icon-path-cache` mock. The mock path is relative to each test file's location.

- [ ] **Step 1: Update ActivityBar.test.tsx**

In `spa/src/features/workspace/components/ActivityBar.test.tsx`, replace:
```ts
vi.mock('../generated/icon-loader', () => ({
  iconLoaders: {},
}))
```
With:
```ts
vi.mock('../lib/icon-path-cache', () => ({
  getIconPath: () => null,
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))
```

- [ ] **Step 2: Update WorkspaceSettingsPage.test.tsx**

In `spa/src/features/workspace/components/WorkspaceSettingsPage.test.tsx`, replace:
```ts
vi.mock('../generated/icon-loader', () => ({
  ALL_ICON_NAMES: ['House', 'Star'],
  iconLoaders: {
    House: () => new Promise(() => {}),
    Star: () => new Promise(() => {}),
  },
}))
```
With:
```ts
vi.mock('../lib/icon-path-cache', () => ({
  getIconPath: () => 'M0,0',
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))
```

- [ ] **Step 3: Update SortableTab.test.tsx**

In `spa/src/components/SortableTab.test.tsx`, replace:
```ts
vi.mock('../features/workspace/generated/icon-loader', () => ({
  iconLoaders: {},
}))
```
With:
```ts
vi.mock('../features/workspace/lib/icon-path-cache', () => ({
  getIconPath: () => null,
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))
```

- [ ] **Step 4: Update SettingsPage.test.tsx**

In `spa/src/components/SettingsPage.test.tsx`, replace the `icon-loader` mock block with:
```ts
vi.mock('../features/workspace/generated/icon-loader', () => ({
  ALL_ICON_NAMES: ['House', 'Star'],
  iconLoaders: {
    House: () => new Promise(() => {}),
    Star: () => new Promise(() => {}),
  },
}))
```
→
```ts
vi.mock('../features/workspace/lib/icon-path-cache', () => ({
  getIconPath: () => 'M0,0',
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))
```

- [ ] **Step 5: Update useRouteSync.test.ts**

In `spa/src/hooks/useRouteSync.test.ts`, replace:
```ts
vi.mock('../features/workspace/generated/icon-loader', () => ({
  ALL_ICON_NAMES: [],
  iconLoaders: {},
}))
```
With:
```ts
vi.mock('../features/workspace/lib/icon-path-cache', () => ({
  getIconPath: () => null,
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))
```

- [ ] **Step 6: Update useShortcuts.test.ts**

In `spa/src/hooks/useShortcuts.test.ts`, replace:
```ts
vi.mock('../features/workspace/generated/icon-loader', () => ({
  ALL_ICON_NAMES: [],
  iconLoaders: {},
}))
```
With:
```ts
vi.mock('../features/workspace/lib/icon-path-cache', () => ({
  getIconPath: () => null,
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))
```

- [ ] **Step 7: Update register-modules.test.ts**

In `spa/src/lib/register-modules.test.ts`, replace:
```ts
vi.mock('../features/workspace/generated/icon-loader', () => ({
  ALL_ICON_NAMES: [],
  iconLoaders: {},
}))
```
With:
```ts
vi.mock('../features/workspace/lib/icon-path-cache', () => ({
  getIconPath: () => null,
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))
```

- [ ] **Step 8: Run full test suite**

```bash
cd spa && npx vitest run
```

Expected: all tests pass

- [ ] **Step 9: Commit**

```bash
git add spa/src/features/workspace/components/ActivityBar.test.tsx \
  spa/src/features/workspace/components/WorkspaceSettingsPage.test.tsx \
  spa/src/components/SortableTab.test.tsx \
  spa/src/components/SettingsPage.test.tsx \
  spa/src/hooks/useRouteSync.test.ts \
  spa/src/hooks/useShortcuts.test.ts \
  spa/src/lib/register-modules.test.ts
git commit -m "test: migrate icon-loader mocks to icon-path-cache"
```

---

### Task 9: Delete Old Icon Loader

**Files:**
- Delete: `spa/src/features/workspace/generated/icon-loader.ts`
- Delete: `spa/scripts/generate-icon-loader.mjs`

- [ ] **Step 1: Delete the files**

```bash
cd spa && rm src/features/workspace/generated/icon-loader.ts scripts/generate-icon-loader.mjs
```

- [ ] **Step 2: Verify no remaining references**

```bash
cd spa && grep -r "icon-loader" src/ --include="*.ts" --include="*.tsx" | grep -v node_modules | grep -v ".test."
```

Expected: no output (all references should have been updated in prior tasks)

- [ ] **Step 3: Run full test suite + type check**

```bash
cd spa && npx tsc -b --noEmit && npx vitest run
```

Expected: no type errors, all tests pass

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "chore: remove legacy icon-loader.ts and generate-icon-loader.mjs"
```

---

### Task 10: Build Verification

**Files:** none (verification only)

- [ ] **Step 1: Run lint**

```bash
cd spa && pnpm run lint
```

Expected: no errors

- [ ] **Step 2: Build the SPA**

```bash
cd spa && pnpm run build
```

Expected: successful build

- [ ] **Step 3: Verify build output**

```bash
echo "=== JS files count ===" && ls dist/assets/*.js | wc -l
echo "=== icon chunks (uppercase) ===" && ls dist/assets/[A-Z]*.js 2>/dev/null | wc -l
echo "=== static JSON ===" && ls -lh dist/icons/*.json
echo "=== main bundle ===" && ls -lh dist/assets/index-*.js
echo "=== main bundle gzipped ===" && gzip -c dist/assets/index-*.js | wc -c
echo "=== total dist ===" && du -sh dist/
```

Expected:
- JS file count: ~1 (just main bundle, possibly a few vendor chunks)
- Icon chunks: 0
- Static JSON: 6 files in `dist/icons/`
- Main bundle: ~489KB gzipped (within range)

- [ ] **Step 4: Commit the plan completion**

No code to commit. Proceed to visual testing if dev server is available.
