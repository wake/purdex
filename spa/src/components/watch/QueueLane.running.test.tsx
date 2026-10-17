// The queue lane's running command (journal v5 spec §3.8, U13): the slot holder stands out from the waiting cards in the
// running colour — a tint, a 1 px outline and a dot that breathes with the sidebar's running light — while the waiting cards,
// the free slot and the paused look stay as they were. Theme tokens only, and the text on the tint reads at 4.5:1 or better.
import { describe, it, expect } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { QueueLane } from './QueueLane'
import { TabStatusIndicator } from '../TabStatusIndicator'
import type { FleetHolder, FleetWaiter } from '../../lib/fleet/types'
import { useI18nStore } from '../../stores/useI18nStore'
import laneSource from './QueueLane.tsx?raw'
import testSource from './QueueLane.running.test.tsx?raw'

// node:fs / node:path / __dirname are untyped under tsconfig.app.json (see src/index.css.test.ts); they work at runtime.
// @ts-expect-error node:fs is untyped here.
import { readFileSync } from 'node:fs'
// @ts-expect-error node:path is untyped here.
import { resolve } from 'node:path'
// @ts-expect-error __dirname is untyped here.
const SRC: string = resolve(__dirname, '../..')

const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)
const NOW = Date.parse('2026-10-17T12:00:00Z')
const holder = (o: Partial<FleetHolder> = {}): FleetHolder => ({ class: 'heavy', slot: 1, command: 'npx vitest related', project: 'purdex', session: '', since: NOW - 83_000, pid: 10, ...o })
const waiter = (o: Partial<FleetWaiter> = {}): FleetWaiter => ({ class: 'heavy', place: 1, command: 'pnpm build', project: 'spa', session: '', since: NOW - 30_000, ...o })
const hover = { hot: null, setHot: () => {} }

type LaneOpts = { cls?: 'heavy' | 'light'; total?: number; paused?: boolean; holders?: FleetHolder[]; waiters?: FleetWaiter[] }
const renderLane = ({ cls = 'heavy', total = 2, paused = false, holders = [holder()], waiters = [waiter()] }: LaneOpts = {}) =>
  render(<QueueLane cls={cls} used={holders.length} total={total} paused={paused} held={false} heldReason="" holders={holders} waiters={waiters} now={NOW} hover={hover} />)

const classes = (el: Element) => new Set(el.className.split(/\s+/).filter(Boolean))
const RUNNING = ['bg-status-success-text/12', 'border', 'border-status-success-text']

describe('QueueLane — the running holder stands out (U13)', () => {
  it('a holder card has the running tint, outline and dot; a waiting card has none of them', () => {
    renderLane()
    const card = screen.getByTestId('q-holder')
    for (const c of RUNNING) expect(classes(card), c).toContain(c)
    // The old faint accent look is gone, not stacked under the new one.
    expect(card.className).not.toMatch(/\b(?:bg|border)-accent\b/)
    const dot = within(card).getByTestId('q-holder-dot')
    expect(classes(dot)).toContain('bg-status-success-text')
    expect(classes(dot)).toContain('rounded-full')
    expect(dot).toHaveAttribute('aria-hidden', 'true')
    // The dot sits at the card's left, before the command.
    expect(card.firstElementChild).toBe(dot)

    const waiting = screen.getByTestId('q-waiting')
    expect(waiting.className).not.toMatch(/status-success/)
    expect(within(waiting).queryByTestId('q-holder-dot')).toBeNull()
  })

  it('every holder gets the look, in both lanes; only holders do', () => {
    renderLane({ cls: 'light', total: 3, holders: [holder({ class: 'light', slot: 1, pid: 1 }), holder({ class: 'light', slot: 2, pid: 2, command: 'make lint' })], waiters: [waiter({ class: 'light' }), waiter({ class: 'light', place: 2, command: 'git status' })] })
    const cards = screen.getAllByTestId('q-holder')
    expect(cards).toHaveLength(2)
    for (const card of cards) {
      for (const c of RUNNING) expect(classes(card), c).toContain(c)
      expect(within(card).getAllByTestId('q-holder-dot')).toHaveLength(1)
    }
    expect(screen.getAllByTestId('q-holder-dot')).toHaveLength(2)
    for (const w of screen.getAllByTestId('q-waiting')) expect(w.className).not.toMatch(/status-success/)
  })

  it('the dot breathes with the sidebar\'s running light: the same animation class, the keyframes not copied', () => {
    renderLane()
    const dot = screen.getByTestId('q-holder-dot')
    expect(classes(dot)).toContain('animate-breathe')
    // The sidebar's running light, whatever its animation class is called, is the one the dot carries.
    const { getByTestId } = render(<TabStatusIndicator status="running" mode="replace" isActive={false} />)
    const sidebarAnim = [...classes(getByTestId('tab-status-indicator'))].filter((c) => c.startsWith('animate-'))
    expect(sidebarAnim).toEqual(['animate-breathe'])
    for (const c of sidebarAnim) expect(classes(dot)).toContain(c)
    // One definition of the breathing: index.css's keyframes and utility, none in the lane.
    const css = readFileSync(resolve(SRC, 'index.css'), 'utf8') as string
    expect(css.match(/@keyframes\s+breathe\b/g)).toHaveLength(1)
    expect(css).toMatch(/@utility animate-breathe\s*\{\s*animation:\s*breathe 2s ease-in-out infinite;\s*\}/)
    expect(laneSource).not.toMatch(/@keyframes|animation\s*:/)
  })

  it('under prefers-reduced-motion the dot is still', () => {
    renderLane()
    expect(classes(screen.getByTestId('q-holder-dot'))).toContain('motion-reduce:animate-none')
  })
})

describe('QueueLane — what stays as it was', () => {
  it('the waiting card and the free slot keep their look', () => {
    renderLane({ total: 2 })
    expect(classes(screen.getByTestId('q-waiting'))).toEqual(new Set(['shrink-0', 'flex', 'items-center', 'gap-1.5', 'h-7', 'w-[176px]', 'px-2', 'rounded-md', 'border', 'border-border-default', 'bg-surface-secondary', 'text-xs', 'font-mono', 'cursor-default']))
    const free = screen.getByTestId('q-free')
    expect(classes(free)).toEqual(new Set(['shrink-0', 'h-7', 'w-[176px]', 'rounded-md', 'border', 'border-dashed', 'border-border-default', 'grid', 'place-items-center', 'text-[10px]', 'text-text-muted']))
    expect(free).toHaveTextContent(t('watch.queue.free'))
  })

  it('a paused lane keeps its words and colours; a holder in it still shows as running', () => {
    renderLane({ paused: true })
    const lane = screen.getByTestId('lane-heavy')
    expect(within(lane).getByText(t('watch.queue.paused')).className).toBe('text-[10px] text-status-warning-text')
    const gate = within(lane).getByTestId('gate')
    expect(gate).toHaveAttribute('data-state', 'paused')
    expect(classes(gate.firstElementChild!)).toEqual(new Set(['w-px', 'h-5', 'bg-status-warning']))
    expect(within(gate).getByText(t('watch.queue.gate.paused')).className).toBe('text-[10px] whitespace-nowrap text-status-warning-text')
    expect(within(lane).getAllByTestId('q-free')).toHaveLength(1)
    for (const c of RUNNING) expect(classes(within(lane).getByTestId('q-holder')), c).toContain(c)
  })
})

// --- the colour: theme tokens only, readable in every builtin theme ---

const themesCss = readFileSync(resolve(SRC, 'styles/themes.css'), 'utf8') as string
const themes = (): Record<string, Record<string, string>> => {
  const out: Record<string, Record<string, string>> = {}
  for (const m of themesCss.matchAll(/\[data-theme="([\w-]+)"\]\s*\{([^}]*)\}/g)) {
    const vars: Record<string, string> = {}
    for (const d of m[2].matchAll(/--([\w-]+):\s*([^;]+);/g)) vars[d[1]] = d[2].trim()
    out[m[1]] = vars
  }
  return out
}
const rgb = (hex: string) => [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16))
const lum = (c: number[]) => {
  const l = c.map((v) => v / 255).map((v) => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4))
  return 0.2126 * l[0] + 0.7152 * l[1] + 0.0722 * l[2]
}
const contrast = (a: number[], b: number[]) => {
  const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x)
  return (hi + 0.05) / (lo + 0.05)
}
// A `/N` tint is the colour at N % alpha, composited over what is behind the card.
const over = (fg: number[], bg: number[], a: number) => fg.map((v, i) => a * v + (1 - a) * bg[i])

describe('QueueLane — the running colour', () => {
  it('the Tailwind colour falls back to the plain token for a theme without the text tier', () => {
    expect(themesCss).toMatch(/--color-status-success-text:\s*var\(--status-success-text,\s*var\(--status-success\)\);/)
  })

  it('every text on the holder reads at 4.5:1 or better on the tint, in every builtin theme, over either surface the area sits on', () => {
    renderLane()
    const card = screen.getByTestId('q-holder')
    const tint = [...classes(card)].map((c) => c.match(/^bg-status-success-text\/(\d+)$/)).find(Boolean)
    expect(tint, 'the holder has a status-success-text tint').toBeTruthy()
    const alpha = Number(tint![1]) / 100
    const texts = [...card.querySelectorAll('*')].flatMap((el) => [...classes(el)].filter((c) => /^text-text-[\w-]+$/.test(c)).map((c) => c.slice('text-'.length)))
    expect(texts.length).toBeGreaterThan(0)
    const all = themes()
    expect(Object.keys(all).sort()).toEqual(['dark', 'dracula', 'light', 'nord'])
    for (const [id, v] of Object.entries(all)) {
      const green = rgb(v['status-success-text'] ?? v['status-success'])
      for (const surface of ['surface-primary', 'surface-secondary']) {
        const bg = over(green, rgb(v[surface]), alpha)
        for (const tok of texts) expect(contrast(rgb(v[tok]), bg), `${id}: ${tok} on the tint over ${surface}`).toBeGreaterThanOrEqual(4.5)
        // The outline and the dot are the colour itself: they must show against the surface and the tint (non-text, 3:1).
        expect(contrast(green, rgb(v[surface])), `${id}: outline on ${surface}`).toBeGreaterThanOrEqual(3)
        expect(contrast(green, bg), `${id}: dot on the tint over ${surface}`).toBeGreaterThanOrEqual(3)
      }
    }
  })

  it('guard: no hex colour in the files this change touched', () => {
    // Comments are dropped first, so a PR number in one is not taken for a colour.
    const code = (s: string) => s.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
    for (const [name, src] of [['QueueLane.tsx', laneSource], ['QueueLane.running.test.tsx', testSource]] as const) {
      expect(code(src).match(/#[0-9a-fA-F]{3,8}\b/g), name).toBeNull()
    }
  })
})
