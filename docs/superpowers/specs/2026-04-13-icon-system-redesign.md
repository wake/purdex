# Icon System Redesign — 從 React.lazy 遷移到 SVG Path Data

Date: 2026-04-13

## 目標

1. **消除 dist 裡 1,445 個 icon chunk 檔案** → 替換為 6 個靜態 JSON
2. **消除 Suspense 閃爍** → 同步 SVG path 渲染
3. **改善搜尋品質** → Fuse.js 模糊搜尋 + tags/categories
4. **消除 `icon-loader.ts`（192KB 自動生成）** → 用 `meta.json` + per-weight JSON 取代

## 現況

- `@phosphor-icons/react` 2.1.10，1,512 個 icon
- `scripts/generate-icon-loader.mjs` 掃描 CSR entry 產生 `generated/icon-loader.ts`
- `icon-loader.ts`：192KB、1,524 行，`@ts-nocheck`，每個 icon 一行 dynamic import
- `WorkspaceIconPicker`：curated 分類瀏覽 + `string.includes()` 搜尋全集，硬截斷 100 個
- `WorkspaceIcon`：`React.lazy()` + `Suspense` + `ErrorBoundary`，首次閃爍 fallback 字母
- Build 產出：main 453KB gz + 1,445 icon chunks 955KB gz = 1,408KB gz total
- `IconWeight` 目前定義 3 種：`'bold' | 'duotone' | 'fill'`

### 受影響的檔案

**核心（需重寫）：**
- `spa/src/features/workspace/generated/icon-loader.ts` — 刪除
- `spa/scripts/generate-icon-loader.mjs` — 替換為 `generate-icon-data.mjs`
- `spa/src/features/workspace/components/WorkspaceIcon.tsx` — SVG path renderer
- `spa/src/features/workspace/components/WorkspaceIconPicker.tsx` — 虛擬捲動 + Fuse.js

**需更新（修改 import / mock）：**
- `spa/src/types/tab.ts` — `IconWeight` 擴充為 6 種
- `spa/src/features/workspace/components/WorkspaceSettingsPage.tsx` — weight 選項從 `['bold', 'duotone', 'fill']` 擴充為 6 種
- 8 個測試檔 — 更新 mock 對象（`icon-loader` → `icon-path-cache`）

**不需變更：**
- `spa/src/features/workspace/constants.ts` — `CURATED_ICON_CATEGORIES` 維持不變

**不受影響：**
- 所有靜態 `import { Icon } from '@phosphor-icons/react'` 的元件（TabBar、ActivityBar、agent-icons 等）維持不變

## 架構設計

### 資料流

```
Build time                             Runtime
─────────                              ───────
@phosphor-icons/core                   App startup
  │                                      │
  ├─ icons metadata                      ├─ import icon-meta.json (bundled, 27KB gz)
  │  → src/.../icon-meta.json            │  → Fuse.js 索引建立
  │  → src/.../icon-names.ts             │
  │                                      ├─ prefetchWeight('bold')
  ├─ assets/{weight}/*.svg               │
  │  extract <path d="...">              ▼
  │  → public/icons/                   iconPathCache (Map)
  │    ├─ bold.json   (182KB gz)         │
  │    ├─ regular.json(183KB gz)         ├─ WorkspaceIcon: sync SVG render
  │    ├─ thin.json   (192KB gz)         │
  │    ├─ light.json  (195KB gz)         └─ WorkspaceIconPicker:
  │    ├─ fill.json   (156KB gz)             Fuse.js search + TanStack Virtual
  │    └─ duotone.json(220KB gz)             + SVG path render
  │
  └─ generate-icon-data.mjs

Electron bundled mode:
  spa/public/icons/*.json → out/renderer/icons/*.json
  app:///icons/bold.json → out/renderer/icons/bold.json ✓
```

### Build-Time 資料生成

**新腳本**：`spa/scripts/generate-icon-data.mjs`

取代 `generate-icon-loader.mjs`。讀取 `@phosphor-icons/core`：

1. 從 `assets/{weight}/*.svg` 提取 path data，產生 6 個 JSON 放 `public/icons/`：
   ```json
   // bold.json — 單一 path 的 icon
   { "Acorn": "M236,104a60...", "Terminal": "M232,56H24..." }

   // duotone.json — 雙 path + opacity
   { "Acorn": [{"d":"M216,112...","o":0.2}, "M232,104..."] }
   ```
   Vite build 時 `spa/public/icons/` 複製到 `out/renderer/icons/`，Electron `app://` 協定可直接 serve。

2. 產生 `src/features/workspace/generated/icon-meta.json` + `icon-names.ts`：
   - `icon-meta.json`：搜尋索引，放在 `src/` 下供 Vite 靜態 import 打進 bundle（搜尋索引需即時可用，不能走 fetch）：
     ```json
     [
       {"n":"Acorn","t":["savings","nut","vegetable"],"c":["finances","nature"]},
       {"n":"AddressBook","t":["contacts"],"c":["office"]}
     ]
     ```
     欄位壓縮：`n` = pascal_name, `t` = tags (過濾 `*new*`), `c` = categories。
     注意：不再另外產生 `public/icons/meta.json`，metadata 只存在於 bundle 中。
   - `icon-names.ts`：
     ```ts
     // Auto-generated — do not edit
     export const ICON_NAMES: string[] = ['Acorn', 'AddressBook', ...]
     ```
     供型別檢查和非搜尋場景使用（如判斷某 name 是否為合法 icon）。

**新增 devDependency**：`@phosphor-icons/core`

**package.json scripts**：
```json
{
  "generate:icons": "node scripts/generate-icon-data.mjs",
  "predev": "node scripts/generate-icon-data.mjs",
  "prebuild": "node scripts/generate-icon-data.mjs"
}
```
`predev` 確保首次 clone 後 `pnpm dev` 不會因缺少 generated 檔案而失敗。

### Icon Path 快取層

**新檔案**：`spa/src/features/workspace/lib/icon-path-cache.ts`

```ts
type PathData = string | Array<string | { d: string; o: number }>
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

**App startup prefetch**（在 App.tsx 或 store init）：
```ts
// 啟動時載入預設 weight（weight 是 per-workspace，此處載入最常用的 bold 作為預熱）
prefetchWeight('bold')
```

### WorkspaceIcon 重寫

**從**：React.lazy + Suspense + ErrorBoundary（63 行）
**到**：純 SVG path renderer（~40 行）

```tsx
import { getIconPath, isWeightLoaded, prefetchWeight } from '../lib/icon-path-cache'
import { useEffect, useState } from 'react'

export function WorkspaceIcon({ icon, name, size, weight = 'bold', className }: Props) {
  const fallbackChar = name.charAt(0) || '?'
  const textStyle = { fontSize: size * 0.75 }
  const phosphorName = icon && isPhosphorName(icon) ? icon : null

  // Hooks 必須在所有條件分支之前（Rules of Hooks）
  const [, setTick] = useState(0)
  useEffect(() => {
    if (phosphorName && !isWeightLoaded(weight)) {
      prefetchWeight(weight).then(() => setTick(t => t + 1))
    }
  }, [phosphorName, weight])

  // 非 Phosphor icon（emoji 或無 icon）→ 文字 fallback
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
      : <path key={i} d={p.d} opacity={p.o} />
  )
}
```

**關鍵改善**：
- 刪除 `ErrorBoundary`、`Suspense`、`React.lazy`、`lazyCache`
- Weight 已載入時完全同步渲染，零閃爍
- Weight 未載入時短暫 fallback + 自動 fetch（一次性，之後都同步）

### WorkspaceIconPicker 重寫

**新增 dependencies**：
- `fuse.js` — 模糊搜尋
- `@tanstack/react-virtual` — 虛擬捲動

**架構變更**：

1. **搜尋**：`string.includes()` → Fuse.js，搜尋 `n`（name）+ `t`（tags）+ `c`（categories）
2. **渲染**：`React.lazy` per icon → `<svg>` + path data from cache
3. **捲動**：全部渲染 + `.slice(0, 100)` 硬截斷 → TanStack Virtual grid（只渲染可視區域，無上限）
4. **Curated set**：`constants.ts` 的 `CURATED_ICON_CATEGORIES` 維持不變，作為分類瀏覽來源。搜尋時用 `icon-meta.json` 的 `t`（tags）+ `c`（categories）+ `n`（name）

**Picker 行為**：
- 無搜尋時：顯示 curated 分類標籤頁（同現況）
- 搜尋時：Fuse.js 搜全集，結果以虛擬捲動 grid 呈現（移除 100 個上限）
- Weight 切換：使用者可在 picker 中切換 weight 預覽

### IconWeight 擴充

`spa/src/types/tab.ts`：

```ts
// 現在
export type IconWeight = 'bold' | 'duotone' | 'fill'

// 改為
export type IconWeight = 'bold' | 'regular' | 'thin' | 'light' | 'fill' | 'duotone'
```

對應 Phosphor Icons 的全部 6 種 weight。

### 測試策略

**Mock 遷移**：8 個測試檔目前 mock `icon-loader`，需改為 mock `icon-path-cache`：

```ts
// 舊
vi.mock('../generated/icon-loader', () => ({
  ALL_ICON_NAMES: ['House', 'Star'],
  iconLoaders: { House: () => new Promise(() => {}) }
}))

// 新
vi.mock('../lib/icon-path-cache', () => ({
  getIconPath: (name: string) => name === 'House' ? 'M0,0...' : null,
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))
```

**新增測試**：
- `icon-path-cache.test.ts` — fetch + cache 行為
- WorkspaceIconPicker — Fuse.js 搜尋 + 虛擬捲動 render count
- WorkspaceIcon — 同步 SVG 渲染 + weight 未載入 fallback

## Build 產出對比

| 項目 | 重構前 | 重構後 |
|------|--------|--------|
| Main bundle (gz) | 453KB | ~489KB (+27KB meta + ~6.5KB fuse.js + ~2.5KB tanstack) |
| Icon chunks | 1,445 檔 / 955KB gz | 0 |
| Static JSON | 0 | 6 檔 / ~1,128KB gz（按需載入） |
| Generated TS | 192KB / 1,524 行 | ~5KB / ~20 行 |
| 首次載入 JS | 453KB gz | ~489KB gz |
| 搜尋能力 | substring match | Fuse.js fuzzy + tags + categories |
| Icon 渲染 | React.lazy + Suspense | sync SVG path（weight 已載入時） |

## 不在範圍內

- 靜態 import 的 icon（TabBar、ActivityBar 等）不改動
- 不引入 SVG sprite sheet（與 weight 切換和 duotone 相容性差）
- 不做 icon font 方案
- 不移除 `@phosphor-icons/react` dependency（其他元件仍靜態 import）
