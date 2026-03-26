# PWA + Platform Capabilities + Browser Pane Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add PWA installability (manifest + icons), platform capabilities detection, and browser PaneContent kind (disabled in SPA mode, ready for Electron).

**Architecture:** PWA is manifest + meta tags only (no service worker). `platform.ts` provides `PlatformCapabilities` based on `window.electronAPI` presence. `PaneContent` gains `browser` kind with conditional registration via `NewTabProvider.disabled`.

**Tech Stack:** React 19 / Vite 8 / Vitest / Phosphor Icons / Zustand 5

**Spec:** `docs/superpowers/specs/2026-03-26-pwa-electron-design.md`

---

## File Structure

### New Files (6)

| File | Responsibility |
|------|---------------|
| `spa/public/manifest.json` | PWA web app manifest |
| `spa/public/icons/icon-192.png` | PWA icon 192x192 |
| `spa/public/icons/icon-512.png` | PWA icon 512x512 |
| `spa/public/icons/icon-maskable-512.png` | PWA maskable icon 512x512 |
| `spa/src/lib/platform.ts` | Platform capabilities detection |
| `spa/src/types/electron.d.ts` | Global window.electronAPI type augmentation (ambient, no imports) |
| `spa/src/components/BrowserPane.tsx` | Browser pane placeholder (WebContentsView IPC in Electron, disabled message in SPA) |

### Modified Files (8)

| File | Change |
|------|--------|
| `spa/index.html` | Add manifest link + PWA meta tags |
| `spa/src/types/tab.ts` | Add `browser` kind to PaneContent union |
| `spa/src/lib/pane-labels.ts` | Add `browser` case to getPaneLabel + getPaneIcon |
| `spa/src/lib/route-utils.ts` | Add `browser` case to tabToUrl |
| `spa/src/lib/new-tab-registry.ts` | Add `disabled?` + `disabledReason?` to NewTabProvider interface |
| `spa/src/lib/register-panes.tsx` | Register browser pane renderer + NewTab provider (disabled) |
| `spa/src/components/NewTabPage.tsx` | Render disabled providers with reason text |
| `spa/src/locales/en.json` + `spa/src/locales/zh-TW.json` | Add browser + pane i18n keys |

---

## Task 1: PWA Manifest + Icons + Meta Tags

**Files:**
- Create: `spa/public/manifest.json`
- Create: `spa/public/icons/icon-192.png`
- Create: `spa/public/icons/icon-512.png`
- Create: `spa/public/icons/icon-maskable-512.png`
- Modify: `spa/index.html`

- [ ] **Step 1: Create manifest.json**

```json
{
  "name": "tmux-box",
  "short_name": "tbox",
  "start_url": "/",
  "display": "standalone",
  "background_color": "#0a0a0a",
  "theme_color": "#0a0a0a",
  "icons": [
    { "src": "/icons/icon-192.png", "sizes": "192x192", "type": "image/png" },
    { "src": "/icons/icon-512.png", "sizes": "512x512", "type": "image/png" },
    { "src": "/icons/icon-maskable-512.png", "sizes": "512x512", "type": "image/png", "purpose": "maskable" }
  ]
}
```

- [ ] **Step 2: Generate PWA icons from existing favicon.svg**

Use the existing `spa/public/favicon.svg` as source. Generate 3 PNG icons:

```bash
# Requires: brew install librsvg (for rsvg-convert) or use sharp-cli
# Option A: rsvg-convert
rsvg-convert -w 192 -h 192 spa/public/favicon.svg > spa/public/icons/icon-192.png
rsvg-convert -w 512 -h 512 spa/public/favicon.svg > spa/public/icons/icon-512.png
rsvg-convert -w 512 -h 512 spa/public/favicon.svg > spa/public/icons/icon-maskable-512.png
```

If `rsvg-convert` is not available, use any SVG-to-PNG tool. The maskable icon should have the same content but with safe-zone padding (the inner 80% circle). For the first iteration, using the same image for all 3 is acceptable.

- [ ] **Step 3: Add PWA meta tags to index.html**

Current `spa/index.html` head section:
```html
<meta charset="UTF-8" />
<link rel="icon" type="image/svg+xml" href="/favicon.svg" />
<meta name="viewport" content="width=device-width, initial-scale=1.0" />
<title>tmux-box</title>
```

Add after the viewport meta:
```html
<link rel="manifest" href="/manifest.json">
<meta name="theme-color" content="#0a0a0a">
<meta name="apple-mobile-web-app-capable" content="yes">
<meta name="apple-mobile-web-app-status-bar-style" content="black-translucent">
<link rel="apple-touch-icon" href="/icons/icon-192.png">
```

- [ ] **Step 4: Verify build**

Run: `cd spa && pnpm run build`
Expected: Build succeeds, `dist/manifest.json` and `dist/icons/` present in output.

- [ ] **Step 5: Commit**

```bash
git add spa/public/manifest.json spa/public/icons/ spa/index.html
git commit -m "feat: PWA manifest + icons + meta tags for installability"
```

---

## Task 2: Platform Capabilities Detection

**Files:**
- Create: `spa/src/lib/platform.ts`
- Test: `spa/src/lib/platform.test.ts`

- [ ] **Step 1: Write failing tests**

```typescript
// spa/src/lib/platform.test.ts
import { describe, it, expect, afterEach } from 'vitest'
import { getPlatformCapabilities } from './platform'

describe('getPlatformCapabilities', () => {
  afterEach(() => {
    // Clean up
    delete (window as Record<string, unknown>).electronAPI
  })

  it('returns all false when no electronAPI', () => {
    const caps = getPlatformCapabilities()
    expect(caps.canTearOffTab).toBe(false)
    expect(caps.canMergeWindow).toBe(false)
    expect(caps.canBrowserPane).toBe(false)
    expect(caps.canSystemTray).toBe(false)
  })

  it('returns all true when electronAPI exists', () => {
    ;(window as Record<string, unknown>).electronAPI = {
      tearOffTab: async () => {},
      mergeTab: async () => {},
      openBrowserView: async () => {},
      closeBrowserView: async () => {},
      navigateBrowserView: async () => {},
      onTabReceived: () => () => {},
    }
    const caps = getPlatformCapabilities()
    expect(caps.canTearOffTab).toBe(true)
    expect(caps.canMergeWindow).toBe(true)
    expect(caps.canBrowserPane).toBe(true)
    expect(caps.canSystemTray).toBe(true)
  })
})
```

- [ ] **Step 2: Run tests to verify fail**

Run: `cd spa && npx vitest run src/lib/platform.test.ts`

- [ ] **Step 3: Implement platform.ts**

```typescript
// spa/src/lib/platform.ts

export interface PlatformCapabilities {
  canTearOffTab: boolean
  canMergeWindow: boolean
  canBrowserPane: boolean
  canSystemTray: boolean
}

export function getPlatformCapabilities(): PlatformCapabilities {
  const isElectron = !!window.electronAPI
  return {
    canTearOffTab: isElectron,
    canMergeWindow: isElectron,
    canBrowserPane: isElectron,
    canSystemTray: isElectron,
  }
}
```

Then create the ambient type declaration (no imports/exports — globally visible):

```typescript
// spa/src/types/electron.d.ts
interface Window {
  electronAPI?: {
    tearOffTab: (tabJson: string) => Promise<void>
    mergeTab: (tabJson: string, targetWindowId: string) => Promise<void>
    openBrowserView: (url: string, paneId: string) => Promise<void>
    closeBrowserView: (paneId: string) => Promise<void>
    navigateBrowserView: (paneId: string, url: string) => Promise<void>
    onTabReceived: (callback: (tabJson: string) => void) => () => void
  }
}
```

This `.d.ts` file has no imports/exports, so TypeScript treats it as an ambient declaration. `window.electronAPI` is visible in all files without importing anything.
```

- [ ] **Step 4: Run tests to verify pass**

Run: `cd spa && npx vitest run src/lib/platform.test.ts`
Expected: All 2 tests PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/platform.ts spa/src/lib/platform.test.ts spa/src/types/electron.d.ts
git commit -m "feat: platform capabilities detection (window.electronAPI)"
```

---

## Task 3: PaneContent Browser Kind + Labels + Route

**Files:**
- Modify: `spa/src/types/tab.ts`
- Modify: `spa/src/lib/pane-labels.ts`
- Modify: `spa/src/lib/pane-labels.test.ts`
- Modify: `spa/src/lib/route-utils.ts`

- [ ] **Step 1: Add browser kind to PaneContent**

In `spa/src/types/tab.ts`, add to the PaneContent union:

```diff
 export type PaneContent =
   | { kind: 'new-tab' }
   | { kind: 'session'; sessionCode: string; mode: 'terminal' | 'stream' }
   | { kind: 'dashboard' }
   | { kind: 'history' }
   | { kind: 'settings'; scope: 'global' | { workspaceId: string } }
+  | { kind: 'browser'; url: string }
```

- [ ] **Step 2: Add browser case to getPaneLabel**

In `spa/src/lib/pane-labels.ts`, add case inside `getPaneLabel`:

```typescript
    case 'browser': {
      try { return new URL(content.url).hostname } catch { return content.url }
    }
```

- [ ] **Step 3: Add browser case to getPaneIcon**

In `spa/src/lib/pane-labels.ts`, add case inside `getPaneIcon`:

```typescript
    case 'browser':
      return 'Globe'
```

- [ ] **Step 4: Add browser case to tabToUrl**

In `spa/src/lib/route-utils.ts`, add case inside `tabToUrl`:

```typescript
    case 'browser':
      return '/'
```

- [ ] **Step 5: Update pane-labels.test.ts**

Add test for browser label (with mock t):

```typescript
it('returns hostname for browser pane', () => {
  const content: PaneContent = { kind: 'browser', url: 'https://example.com/path' }
  expect(getPaneLabel(content, sessionStore, workspaceStore, mockT)).toBe('example.com')
})

it('returns raw url for browser pane with invalid url', () => {
  const content: PaneContent = { kind: 'browser', url: 'not-a-url' }
  expect(getPaneLabel(content, sessionStore, workspaceStore, mockT)).toBe('not-a-url')
})

it('returns Globe icon for browser pane', () => {
  const content: PaneContent = { kind: 'browser', url: 'https://example.com' }
  expect(getPaneIcon(content)).toBe('Globe')
})
```

- [ ] **Step 6: Run full test suite**

Run: `cd spa && npx vitest run`
Expected: All tests pass (check for TypeScript exhaustiveness errors in any switch on `content.kind`)

- [ ] **Step 7: Commit**

```bash
git add spa/src/types/tab.ts spa/src/lib/pane-labels.ts spa/src/lib/pane-labels.test.ts spa/src/lib/route-utils.ts
git commit -m "feat: PaneContent browser kind + labels + route mapping"
```

---

## Task 4: i18n Keys for Browser

**Files:**
- Modify: `spa/src/locales/en.json`
- Modify: `spa/src/locales/zh-TW.json`

- [ ] **Step 1: Add browser keys to en.json**

Add after the last `session.*` key block:

```json
  "browser.provider_label": "Browser",
  "browser.requires_app": "Requires desktop app",
  "browser.url_placeholder": "Enter URL...",
```

Note: No `page.pane.browser` key needed — `getPaneLabel` returns `hostname` from URL, not a translated string.
Note: `tray.show_window` and `tray.quit` keys from the spec are deferred to the Electron plan (Phase C).

- [ ] **Step 2: Add browser keys to zh-TW.json**

```json
  "browser.provider_label": "瀏覽器",
  "browser.requires_app": "需要安裝桌面版本",
  "browser.url_placeholder": "輸入網址...",
```

- [ ] **Step 3: Run locale completeness test**

Run: `cd spa && npx vitest run src/locales/locale-completeness.test.ts`
Expected: PASS (both files have same keys)

- [ ] **Step 4: Commit**

```bash
git add spa/src/locales/en.json spa/src/locales/zh-TW.json
git commit -m "feat(i18n): browser pane translation keys — en + zh-TW"
```

---

## Task 5: NewTabProvider disabled Support

**Files:**
- Modify: `spa/src/lib/new-tab-registry.ts`
- Modify: `spa/src/components/NewTabPage.tsx`
- Test: `spa/src/lib/new-tab-registry.test.ts` (if exists, otherwise create)

- [ ] **Step 1: Add disabled fields to NewTabProvider interface**

In `spa/src/lib/new-tab-registry.ts`:

```diff
 export interface NewTabProvider {
   id: string
   label: string
   icon: string
   order: number
   component: React.ComponentType<NewTabProviderProps>
+  disabled?: boolean
+  disabledReason?: string  // i18n key
 }
```

- [ ] **Step 2: Update NewTabPage.tsx to render disabled providers**

```diff
 {providers.map((p) => (
   <section key={p.id} className="w-full max-w-md">
-    <h3 className="text-sm font-medium text-text-secondary mb-2 px-2">{t(p.label)}</h3>
-    <p.component onSelect={onSelect} />
+    <h3 className="text-sm font-medium text-text-secondary mb-2 px-2">
+      {t(p.label)}
+      {p.disabled && p.disabledReason && (
+        <span className="text-text-muted text-xs ml-2">— {t(p.disabledReason)}</span>
+      )}
+    </h3>
+    {!p.disabled && <p.component onSelect={onSelect} />}
   </section>
 ))}
```

- [ ] **Step 3: Run full test suite**

Run: `cd spa && npx vitest run`

- [ ] **Step 4: Commit**

```bash
git add spa/src/lib/new-tab-registry.ts spa/src/components/NewTabPage.tsx
git commit -m "feat: NewTabProvider disabled support with reason text"
```

---

## Task 6: BrowserPane + Register Browser Provider + Registration Test

**Files:**
- Create: `spa/src/components/BrowserPane.tsx`
- Create: `spa/src/components/BrowserNewTabSection.tsx`
- Modify: `spa/src/lib/register-panes.tsx`
- Test: `spa/src/lib/register-panes.test.ts`

- [ ] **Step 1: Create BrowserPane.tsx**

```tsx
// spa/src/components/BrowserPane.tsx
import { useI18nStore } from '../stores/useI18nStore'

interface BrowserPaneProps {
  paneId: string
  url: string
}

export function BrowserPane({ paneId, url }: BrowserPaneProps) {
  const t = useI18nStore((s) => s.t)

  // Electron mode: placeholder div for WebContentsView (implemented in Electron plan)
  // SPA mode: disabled message
  if (!window.electronAPI) {
    return (
      <div className="flex-1 flex items-center justify-center">
        <p className="text-sm text-text-muted">{t('browser.requires_app')}</p>
      </div>
    )
  }

  return (
    <div
      className="w-full h-full"
      data-browser-pane={paneId}
      data-browser-url={url}
    />
  )
}
```

Note: The Electron-specific `useEffect` for `openBrowserView`/`closeBrowserView` IPC will be added in the Electron plan. For now this is the placeholder.

- [ ] **Step 2: Create BrowserNewTabSection.tsx**

```tsx
// spa/src/components/BrowserNewTabSection.tsx
import { useState } from 'react'
import { Globe } from '@phosphor-icons/react'
import { useI18nStore } from '../stores/useI18nStore'
import type { NewTabProviderProps } from '../lib/new-tab-registry'

export function BrowserNewTabSection({ onSelect }: NewTabProviderProps) {
  const t = useI18nStore((s) => s.t)
  const [url, setUrl] = useState('')

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    if (!url.trim()) return
    const finalUrl = url.includes('://') ? url : `https://${url}`
    onSelect({ kind: 'browser', url: finalUrl })
  }

  return (
    <form onSubmit={handleSubmit} className="flex items-center gap-2 px-2">
      <Globe size={16} className="text-text-muted flex-shrink-0" />
      <input
        type="text"
        value={url}
        onChange={(e) => setUrl(e.target.value)}
        placeholder={t('browser.url_placeholder')}
        className="flex-1 bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-3 py-1.5 focus:border-border-active focus:outline-none"
      />
    </form>
  )
}
```

- [ ] **Step 3: Register browser pane + provider in register-panes.tsx**

Add imports and registration at the end of `registerBuiltinPanes()`:

```typescript
import { BrowserPane } from '../components/BrowserPane'
import { BrowserNewTabSection } from '../components/BrowserNewTabSection'
import { getPlatformCapabilities } from './platform'

// Inside registerBuiltinPanes(), after the sessions provider:
const caps = getPlatformCapabilities()

registerPaneRenderer('browser', {
  component: ({ pane }) => {
    const content = pane.content
    if (content.kind !== 'browser') return null
    return <BrowserPane paneId={pane.id} url={content.url} />
  },
})

registerNewTabProvider({
  id: 'browser',
  label: 'browser.provider_label',
  icon: 'Globe',
  order: 10,
  component: BrowserNewTabSection,
  disabled: !caps.canBrowserPane,
  disabledReason: 'browser.requires_app',
})
```

- [ ] **Step 4: Write registration test**

Create or update `spa/src/lib/register-panes.test.ts`:

```typescript
import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { clearNewTabRegistry, getNewTabProviders } from './new-tab-registry'
import { registerBuiltinPanes } from './register-panes'

describe('browser provider registration', () => {
  beforeEach(() => {
    clearNewTabRegistry()
  })

  afterEach(() => {
    delete (window as Record<string, unknown>).electronAPI
  })

  it('registers browser provider as disabled when no electronAPI', () => {
    registerBuiltinPanes()
    const browser = getNewTabProviders().find((p) => p.id === 'browser')
    expect(browser).toBeDefined()
    expect(browser?.disabled).toBe(true)
    expect(browser?.disabledReason).toBe('browser.requires_app')
  })

  it('registers browser provider as enabled when electronAPI present', () => {
    ;(window as Record<string, unknown>).electronAPI = { tearOffTab: async () => {} }
    registerBuiltinPanes()
    const browser = getNewTabProviders().find((p) => p.id === 'browser')
    expect(browser).toBeDefined()
    expect(browser?.disabled).toBe(false)
  })
})
```

Note: This test also needs to clear pane registry + settings registry. Check existing test patterns and clear all registries in `beforeEach`.

- [ ] **Step 5: Run full test suite + lint**

Run: `cd spa && npx vitest run && pnpm run lint`

- [ ] **Step 6: Run build**

Run: `cd spa && pnpm run build`

- [ ] **Step 7: Commit**

```bash
git add spa/src/components/BrowserPane.tsx spa/src/components/BrowserNewTabSection.tsx spa/src/lib/register-panes.tsx spa/src/lib/register-panes.test.ts
git commit -m "feat: BrowserPane + BrowserNewTabSection + register browser provider (disabled in SPA)"
```

---

## Task 7: Final Verification

- [ ] **Step 1: Run full test suite**

Run: `cd spa && npx vitest run`
Expected: ALL tests pass

- [ ] **Step 2: Run lint + build**

Run: `cd spa && pnpm run lint && pnpm run build`

- [ ] **Step 3: Manual smoke test — PWA**

1. Open `http://100.64.0.2:5174` in Chrome
2. Check DevTools > Application > Manifest — should show manifest with 3 icons
3. Check the install prompt appears in Chrome address bar (or ⋮ menu > Install)
4. On iOS Safari: Share > Add to Home Screen should work

- [ ] **Step 4: Manual smoke test — Browser pane**

1. Open New Tab page
2. Verify "Browser" section is visible with disabled state + "需要安裝桌面版本" text
3. URL input should NOT be rendered (disabled provider hides component)
4. Verify existing Sessions section still works normally

- [ ] **Step 5: Commit any fixes**

```bash
git add -A && git commit -m "fix: address final PWA + browser pane issues"
```
