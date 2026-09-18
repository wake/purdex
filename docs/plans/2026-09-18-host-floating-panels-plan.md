# Floating draggable panels for the host color editor and icon picker — Implementation Plan

**Goal:** The color editor and the icon picker on the Host settings page open as **floating, draggable windows** (portal, fixed position, title bar as drag handle) instead of expanding inline, and **close when the user clicks anywhere outside** them (or presses Escape).

**Spec addendum (host-color-modes spec §6.3 / D8, second amendment 2026-09-18):** "inline panel" → "floating draggable panel anchored to the swatch, dismissed by outside click / Escape". Same for the host icon picker.

**Tech:** React 19 / Vitest + Testing Library / Tailwind 4 / TS strict. Tests `cd spa && npx vitest run <path>`; lint `pnpm run lint`; typecheck `npx tsc --noEmit -p tsconfig.app.json`.

## Global Constraints

- Worktree `/Users/wake/Workspace/wake/purdex/.claude/worktrees/host-floating-panels`, branch `worktree-host-floating-panels`; every command `cd`s there. Fresh worktree: `cd spa && pnpm install --frozen-lockfile` first if `node_modules` is missing.
- TDD; commits via `git commit --only`. No new dependency.
- One shared component `FloatingPanel` used by both fields. Existing conventions to follow: `createPortal` into `document.body`, `position: fixed`, viewport clamping and Escape handling as in `spa/src/components/editor/BreadcrumbPopover.tsx`; outside-click detection on `mousedown` like `spa/src/hooks/useClickOutside.ts` (but with an **anchor exclusion**, see below).
- **Toggle must keep working**: clicking the swatch/preview button that opened the panel closes it, clicking it again reopens. Because outside-click fires on `mousedown` before the button's `click`, the panel's outside-click handler must ignore mousedowns inside an `anchorRef` element the caller passes (the swatch row / the icon buttons row).
- Dragging: the title bar is the handle; pointer down on it captures the pointer (guard `typeof setPointerCapture === 'function'`), move by delta, release on up/cancel/lostpointercapture. The panel remembers its dragged position until it unmounts. The body (children) is not a drag surface — sliders/inputs inside must keep working.
- Keep every existing test id inside the editor/picker; the panel adds `data-testid="floating-panel"`, `floating-panel-handle`, `floating-panel-close`.
- `HostColorLayerEditor`'s own Done button stays and still calls `onClose`.
- Locale: `common.close` probably exists — check `rg -n '"common.close"' spa/src/locales/en.json`; if not, add `"floating_panel.close": "Close"` / `"關閉"`.

---

### Task 1: `FloatingPanel`

**Files:** `spa/src/components/FloatingPanel.tsx`, `spa/src/components/FloatingPanel.test.tsx`, locales (only if a close key is needed)

**Interfaces:**
```ts
export interface FloatingPanelProps {
  title: string
  /** Element the panel opens under; its rect decides the initial position. */
  anchorRef: React.RefObject<HTMLElement | null>
  onClose: () => void
  width?: number            // default 320
  testId?: string           // default 'floating-panel'
  children: React.ReactNode
}
export function FloatingPanel(props: FloatingPanelProps): React.ReactPortal
```
DOM: portal → `<div role="dialog" aria-label={title} data-testid={testId} class="fixed z-[100] …" style={{ left, top, width }}>` → header `<div data-testid="floating-panel-handle" class="cursor-move select-none …">{title}<button data-testid="floating-panel-close" aria-label=Close>×</button></div>` → body `<div class="p-3">{children}</div>`.

- [ ] **Step 1: Write the failing tests**

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { useRef } from 'react'
import { FloatingPanel } from './FloatingPanel'

function Harness({ onClose, open = true }: { onClose: () => void; open?: boolean }) {
  const anchor = useRef<HTMLButtonElement>(null)
  return (
    <div>
      <button ref={anchor} data-testid="anchor">anchor</button>
      <button data-testid="elsewhere">elsewhere</button>
      {open && (
        <FloatingPanel title="Main" anchorRef={anchor} onClose={onClose}>
          <input data-testid="inside" />
        </FloatingPanel>
      )}
    </div>
  )
}

function rect(el: HTMLElement, r: Partial<DOMRect>) {
  el.getBoundingClientRect = () => ({ left: 100, top: 50, width: 40, height: 20, right: 140, bottom: 70, x: 100, y: 50, toJSON() {}, ...r }) as DOMRect
}

beforeEach(() => {
  Object.defineProperty(window, 'innerWidth', { value: 1000, configurable: true })
  Object.defineProperty(window, 'innerHeight', { value: 800, configurable: true })
})

describe('FloatingPanel', () => {
  it('renders into document.body as a dialog titled with `title`, positioned under the anchor', () => {
    render(<Harness onClose={() => {}} />)
    const panel = screen.getByRole('dialog', { name: 'Main' })
    expect(panel.parentElement).toBe(document.body)
    expect(panel.style.position).toBe('fixed')
  })

  it('positions below the anchor rect, clamped to the viewport', () => {
    const { unmount } = render(<Harness onClose={() => {}} />)
    unmount()
    // re-render with a stubbed anchor rect: stub before the panel mounts by rendering closed first
    const { rerender } = render(<Harness onClose={() => {}} open={false} />)
    rect(screen.getByTestId('anchor'), { left: 990, top: 790, bottom: 800, right: 1000 })
    rerender(<Harness onClose={() => {}} open />)
    const panel = screen.getByTestId('floating-panel')
    expect(parseInt(panel.style.left)).toBeLessThanOrEqual(1000 - 320 - 4)
    expect(parseInt(panel.style.top)).toBeLessThanOrEqual(800 - 4)
  })

  it('closes on mousedown outside, not on mousedown inside or on the anchor', () => {
    const onClose = vi.fn()
    render(<Harness onClose={onClose} />)
    fireEvent.mouseDown(screen.getByTestId('inside'))
    fireEvent.mouseDown(screen.getByTestId('anchor'))
    expect(onClose).not.toHaveBeenCalled()
    fireEvent.mouseDown(screen.getByTestId('elsewhere'))
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('closes on Escape and on the close button', () => {
    const onClose = vi.fn()
    render(<Harness onClose={onClose} />)
    fireEvent.keyDown(document, { key: 'Escape' })
    fireEvent.click(screen.getByTestId('floating-panel-close'))
    expect(onClose).toHaveBeenCalledTimes(2)
  })

  it('dragging the handle moves the panel by the pointer delta and keeps the position', () => {
    render(<Harness onClose={() => {}} />)
    const panel = screen.getByTestId('floating-panel')
    const handle = screen.getByTestId('floating-panel-handle')
    handle.setPointerCapture = () => {}
    handle.releasePointerCapture = () => {}
    const left0 = parseInt(panel.style.left), top0 = parseInt(panel.style.top)
    fireEvent.pointerDown(handle, { clientX: 10, clientY: 10, pointerId: 1, button: 0 })
    fireEvent.pointerMove(handle, { clientX: 60, clientY: 40, pointerId: 1 })
    expect(parseInt(panel.style.left)).toBe(left0 + 50)
    expect(parseInt(panel.style.top)).toBe(top0 + 30)
    fireEvent.pointerUp(handle, { pointerId: 1 })
    fireEvent.pointerMove(handle, { clientX: 500, clientY: 500, pointerId: 1 })
    expect(parseInt(panel.style.left)).toBe(left0 + 50)
  })

  it('a drag never moves the panel fully off-screen', () => {
    render(<Harness onClose={() => {}} />)
    const panel = screen.getByTestId('floating-panel')
    const handle = screen.getByTestId('floating-panel-handle')
    handle.setPointerCapture = () => {}
    handle.releasePointerCapture = () => {}
    fireEvent.pointerDown(handle, { clientX: 0, clientY: 0, pointerId: 1, button: 0 })
    fireEvent.pointerMove(handle, { clientX: -5000, clientY: -5000, pointerId: 1 })
    expect(parseInt(panel.style.left)).toBeGreaterThanOrEqual(-320 + 40)
    expect(parseInt(panel.style.top)).toBeGreaterThanOrEqual(0)
  })

  it('pointer events inside the body do not start a drag', () => {
    render(<Harness onClose={() => {}} />)
    const panel = screen.getByTestId('floating-panel')
    const left0 = panel.style.left
    fireEvent.pointerDown(screen.getByTestId('inside'), { clientX: 10, clientY: 10, pointerId: 1, button: 0 })
    fireEvent.pointerMove(screen.getByTestId('inside'), { clientX: 60, clientY: 40, pointerId: 1 })
    expect(panel.style.left).toBe(left0)
  })
})
```

- [ ] **Step 2: Run** `cd spa && npx vitest run src/components/FloatingPanel.test.tsx` → FAIL.

- [ ] **Step 3: Implement**

```tsx
import { useEffect, useLayoutEffect, useRef, useState, type ReactNode, type RefObject } from 'react'
import { createPortal } from 'react-dom'
import { X } from '@phosphor-icons/react'
import { useI18nStore } from '../stores/useI18nStore'

export interface FloatingPanelProps {
  title: string
  anchorRef: RefObject<HTMLElement | null>
  onClose: () => void
  width?: number
  testId?: string
  children: ReactNode
}

const PADDING = 4
const Z_INDEX = 100
/** How much of the panel must stay on screen when dragged. */
const MIN_VISIBLE = 40

/**
 * Draggable floating window rendered into `document.body`: opens under `anchorRef`,
 * clamped to the viewport; the title bar is the drag handle; closes on Escape, on
 * the × button, and on mousedown outside the panel **and** outside the anchor (the
 * anchor's own click toggles the panel — swallowing its mousedown would re-open
 * what we just closed).
 */
export function FloatingPanel({ title, anchorRef, onClose, width = 320, testId = 'floating-panel', children }: FloatingPanelProps) {
  const t = useI18nStore((s) => s.t)
  const panelRef = useRef<HTMLDivElement>(null)
  const [pos, setPos] = useState<{ left: number; top: number } | null>(null)
  const drag = useRef<{ pointerId: number; startX: number; startY: number; left: number; top: number } | null>(null)

  // Initial position: below the anchor, clamped so the whole panel is visible.
  useLayoutEffect(() => {
    if (pos) return
    const a = anchorRef.current?.getBoundingClientRect()
    const h = panelRef.current?.offsetHeight ?? 0
    let left = a ? a.left : PADDING
    let top = a ? a.bottom + PADDING : PADDING
    left = Math.max(PADDING, Math.min(left, window.innerWidth - width - PADDING))
    if (top + h > window.innerHeight - PADDING) top = Math.max(PADDING, (a ? a.top : window.innerHeight) - PADDING - h)
    top = Math.min(top, window.innerHeight - PADDING)
    setPos({ left, top })
  }, [anchorRef, pos, width])

  useEffect(() => {
    const onMouseDown = (e: MouseEvent) => {
      const target = e.target as Node
      if (panelRef.current?.contains(target)) return
      if (anchorRef.current?.contains(target)) return
      onClose()
    }
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return
      e.preventDefault()
      onClose()
    }
    document.addEventListener('mousedown', onMouseDown)
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('mousedown', onMouseDown)
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [anchorRef, onClose])

  const clamp = (left: number, top: number) => ({
    left: Math.max(MIN_VISIBLE - width, Math.min(left, window.innerWidth - MIN_VISIBLE)),
    top: Math.max(0, Math.min(top, window.innerHeight - MIN_VISIBLE)),
  })

  const content = (
    <div
      ref={panelRef}
      role="dialog"
      aria-label={title}
      data-testid={testId}
      className="fixed bg-surface-elevated border border-border-default rounded-lg shadow-xl flex flex-col"
      style={{ left: pos?.left ?? PADDING, top: pos?.top ?? PADDING, width, zIndex: Z_INDEX }}
    >
      <div
        data-testid="floating-panel-handle"
        className="flex items-center justify-between gap-2 px-3 py-1.5 border-b border-border-default cursor-move select-none touch-none"
        onPointerDown={(e) => {
          if (e.button !== 0 || !pos) return
          drag.current = { pointerId: e.pointerId, startX: e.clientX, startY: e.clientY, left: pos.left, top: pos.top }
          if (typeof e.currentTarget.setPointerCapture === 'function') e.currentTarget.setPointerCapture(e.pointerId)
        }}
        onPointerMove={(e) => {
          const d = drag.current
          if (!d || d.pointerId !== e.pointerId) return
          setPos(clamp(d.left + (e.clientX - d.startX), d.top + (e.clientY - d.startY)))
        }}
        onPointerUp={(e) => { if (drag.current?.pointerId === e.pointerId) drag.current = null }}
        onPointerCancel={() => { drag.current = null }}
        onLostPointerCapture={() => { drag.current = null }}
      >
        <span className="text-xs font-medium text-text-primary truncate">{title}</span>
        <button
          type="button"
          data-testid="floating-panel-close"
          aria-label={t('floating_panel.close')}
          onPointerDown={(e) => e.stopPropagation()}
          onClick={onClose}
          className="rounded p-0.5 text-text-muted hover:text-text-primary hover:bg-surface-hover cursor-pointer"
        >
          <X size={14} />
        </button>
      </div>
      <div className="p-3">{children}</div>
    </div>
  )
  return createPortal(content, document.body)
}
```
Locale: add `floating_panel.close` = "Close" / "關閉" to both files.

- [ ] **Step 4: Run** → PASS (also `npx tsc --noEmit -p tsconfig.app.json`).
- [ ] **Step 5: Commit** `git commit --only spa/src/components/FloatingPanel.tsx spa/src/components/FloatingPanel.test.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json -m "feat(spa): FloatingPanel — draggable portal window closed by outside click or Escape"`

---

### Task 2: Color editor and icon picker open in a `FloatingPanel`

**Files:** `spa/src/components/hosts/HostColorField.tsx` (+ test), `spa/src/components/hosts/HostIconField.tsx` (+ test), `spa/src/components/hosts/HostColorLayerEditor.tsx` (drop its own border/background/max-w wrapper classes — the panel provides the chrome; keep `data-testid="host-color-editor"` and the Done button)

- [ ] **Step 1: Write / adjust the failing tests**

`HostColorField.test.tsx` — add:
```tsx
  it('opens the editor in a floating dialog titled with the layer name; outside mousedown closes it; the swatch still toggles', () => {
    useHostStore.getState().setHostColorLayer(HOST_ID, 'console', 'main', BLUE)
    render(<div><HostColorField hostId={HOST_ID} /><button data-testid="outside">x</button></div>)
    fireEvent.click(layerBtn('main'))
    const dialog = screen.getByRole('dialog', { name: 'Main' })
    expect(dialog.parentElement).toBe(document.body)
    expect(dialog.contains(screen.getByTestId('host-color-editor'))).toBe(true)
    fireEvent.mouseDown(screen.getByTestId('outside'))
    expect(screen.queryByTestId('host-color-editor')).toBeNull()
    fireEvent.mouseDown(layerBtn('main'))
    fireEvent.click(layerBtn('main'))
    expect(screen.getByTestId('host-color-editor')).toBeInTheDocument()
    fireEvent.mouseDown(layerBtn('main'))
    fireEvent.click(layerBtn('main'))
    expect(screen.queryByTestId('host-color-editor')).toBeNull()
  })

  it('mousedown on another swatch does not close first and then fail to open: it switches layers', () => {
    useHostStore.getState().setHostColorLayer(HOST_ID, 'console', 'main', BLUE)
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.click(layerBtn('main'))
    fireEvent.mouseDown(layerBtn('light'))
    fireEvent.click(layerBtn('light'))
    expect(screen.getByRole('dialog', { name: 'Light' })).toBeInTheDocument()
  })
```
The existing "clicking the open layer again closes the editor; Done closes it too" test keeps passing if the swatch row is the anchor (its mousedown is ignored by the panel). Existing queries by test id keep working through the portal.

`HostIconField.test.tsx` — change "toggles the inline picker from the preview button" to expect the picker inside `screen.getByRole('dialog', { name: 'Change host icon' })` (the `hosts.icon.change` string), and add: outside mousedown closes it; selecting an icon closes it (already asserted?) — read the file.

- [ ] **Step 2: Run** the two test files → FAIL.

- [ ] **Step 3: Implement**

`HostColorField.tsx`: `const swatchRowRef = useRef<HTMLDivElement>(null)` on the swatch row `<div ref={swatchRowRef} className="flex flex-wrap …">`; replace `{editorProps && <HostColorLayerEditor key=… {...editorProps} />}` with
```tsx
{editorProps && (
  <FloatingPanel title={t(`hosts.color.layer.${editorProps.layer}`)} anchorRef={swatchRowRef} onClose={() => setOpen(null)} width={320}>
    <HostColorLayerEditor key={`${mode}:${editorProps.layer}`} {...editorProps} />
  </FloatingPanel>
)}
```
`HostColorLayerEditor.tsx` root: keep `data-testid="host-color-editor" role="group" aria-label=…` but change the class to `space-y-3` (no border / bg / padding / max-w — the panel supplies them). Keep the header line with hint + preview swatch.

`HostIconField.tsx`: `const buttonsRef = useRef<HTMLDivElement>(null)` on the `flex items-center gap-2` row; replace the inline `{open && (<div className="border …">…)}` with
```tsx
{open && (
  <FloatingPanel title={t('hosts.icon.change')} anchorRef={buttonsRef} onClose={() => setOpen(false)} width={360}>
    <WorkspaceIconPicker inline … (unchanged props) />
  </FloatingPanel>
)}
```

- [ ] **Step 4: Run** `cd spa && npx vitest run src/components/hosts src/components/FloatingPanel.test.tsx && npx tsc --noEmit -p tsconfig.app.json && pnpm run lint && npx vitest run` → all green.
- [ ] **Step 5: Commit** `git commit --only <touched files> -m "feat(spa): host color editor and icon picker open as draggable floating panels"`

---

## Done criteria
- Full suite / lint / tsc green.
- Live: click a swatch → window pops under it, drag it by the title bar anywhere, sliders keep working, click elsewhere → closes; same for the icon picker; clicking the same swatch toggles.
