// spa/src/stores/usePoolHostsStore.ts — the monitoring tabs' pool hosts: order and looks (monitoring tabs spec A3, W1).
//
// `{ order: string[], looks: { [hostId]: PoolHostLook } }`. Every key is the wwd `host_id` (16 lowercase hex, generated
// by `cmd/wwd/main.go`): the same on every device, so — unlike `useHostLookStore` — there is no local↔wire mapping and
// the store stays out of host rewrite (host-reresolve / host-lifecycle). Aliases and Purdex daemon host ids are not keys.
//
// Projected in `settings` (`purdex-pool-hosts.{order,looks}`, PROJECTIONS.settings, ordinal 13) and registered with
// `syncManager`. `looks` fields are `HostLookEntry`'s (W1) minus `name`, plus the sidebar `indicator`.
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'
import { sanitizeHostLookEntry } from './useHostLookStore'
import type { HostLookEntry } from './useHostLookStore'

export type IndicatorItem = 'load' | 'cpu' | 'mem' | 'pressure' | 'sysload' | 'disk' | 'slots'
export type IndicatorShape = 'ring' | 'bar' | 'bars'

/** `ring` and `bar` read `data[0]`; `bars` reads `data[0]` and `data[1]`. */
export interface PoolHostIndicator {
  shape: IndicatorShape
  data: IndicatorItem[]
}

export type PoolHostLook = Omit<HostLookEntry, 'name'> & { indicator?: PoolHostIndicator }

interface PoolHostsState {
  order: string[]
  looks: Record<string, PoolHostLook>
  /** Replaces the order with its sanitised copy. */
  setOrder: (order: readonly string[]) => void
  /** `fn(current)` → the new look (sanitised); `undefined` removes it; the same object back writes nothing. */
  patchLook: (hostId: string, fn: (current: PoolHostLook | undefined) => PoolHostLook | undefined) => void
  /** Writes each look whose host has none yet (skip-if-present); one write, none when nothing is absent. */
  putLooksIfAbsent: (entries: Record<string, PoolHostLook>) => void
  /** Sets `indicator` on every listed host, keeping the other fields. */
  setIndicatorForAll: (hostIds: readonly string[], indicator: PoolHostIndicator) => void
}

export const MAX_POOL_HOSTS = 32

const HOST_ID = /^[0-9a-f]{16}$/
const SHAPES: readonly IndicatorShape[] = ['ring', 'bar', 'bars']
/** The items an indicator can read, in the order the editor lists them. */
export const INDICATOR_ITEMS: readonly IndicatorItem[] = ['load', 'cpu', 'mem', 'pressure', 'sysload', 'disk', 'slots']
const ITEMS = INDICATOR_ITEMS

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

const isHostId = (k: unknown): k is string => typeof k === 'string' && HOST_ID.test(k)
const isItem = (v: unknown): v is IndicatorItem => (ITEMS as readonly unknown[]).includes(v)

function sanitizeIndicator(raw: unknown): PoolHostIndicator | null {
  if (!isRecord(raw) || !(SHAPES as readonly unknown[]).includes(raw.shape) || !Array.isArray(raw.data)) return null
  const shape = raw.shape as IndicatorShape
  // 1–2 items, every one recognised: a malformed list is dropped, never rewritten into a different metric.
  if (raw.data.length < 1 || raw.data.length > 2 || !raw.data.every(isItem)) return null
  const [first, second] = raw.data as IndicatorItem[]
  if (shape !== 'bars') return { shape, data: [first] }
  return { shape, data: [first, second ?? 'pressure'] }
}

/** An untrusted look: the `HostLookEntry` fields (sanitised as `useHostLookStore` does) without `name`, plus a valid `indicator`. `null` when not a record. */
export function sanitizePoolHostLook(raw: unknown): PoolHostLook | null {
  const base = sanitizeHostLookEntry(raw)
  if (base === null || !isRecord(raw)) return null
  const { name: _name, ...look } = base
  void _name
  const indicator = sanitizeIndicator(raw.indicator)
  return indicator === null ? look : { ...look, indicator }
}

function sanitizeOrder(raw: unknown): string[] {
  if (!Array.isArray(raw)) return []
  const out: string[] = []
  for (const id of raw) {
    if (out.length >= MAX_POOL_HOSTS) break
    if (isHostId(id) && !out.includes(id)) out.push(id)
  }
  return out
}

function sanitizeLooks(raw: unknown): Record<string, PoolHostLook> {
  const out: Record<string, PoolHostLook> = {}
  if (!isRecord(raw)) return out
  for (const key of Object.keys(raw)) {
    if (Object.keys(out).length >= MAX_POOL_HOSTS) break
    if (!isHostId(key)) continue
    const look = sanitizePoolHostLook(raw[key])
    if (look !== null) out[key] = look
  }
  return out
}

export const usePoolHostsStore = create<PoolHostsState>()(
  persist(
    (set) => ({
      order: [],
      looks: {},

      setOrder: (order) => set({ order: sanitizeOrder(order) }),

      patchLook: (hostId, fn) =>
        set((state) => {
          if (!isHostId(hostId)) return state
          const current = Object.hasOwn(state.looks, hostId) ? state.looks[hostId] : undefined
          const next = fn(current)
          if (next === current) return state
          if (next === undefined) {
            const { [hostId]: _gone, ...rest } = state.looks
            void _gone
            return { looks: rest }
          }
          const clean = sanitizePoolHostLook(next)
          if (clean === null) return state
          if (current === undefined && Object.keys(state.looks).length >= MAX_POOL_HOSTS) return state
          return { looks: { ...state.looks, [hostId]: clean } }
        }),

      putLooksIfAbsent: (entries) =>
        set((state) => {
          let looks: Record<string, PoolHostLook> | null = null
          for (const key of Object.keys(entries)) {
            if (!isHostId(key) || Object.hasOwn(looks ?? state.looks, key)) continue
            if (Object.keys(looks ?? state.looks).length >= MAX_POOL_HOSTS) break
            const clean = sanitizePoolHostLook(entries[key])
            if (clean === null) continue
            looks ??= { ...state.looks }
            looks[key] = clean
          }
          return looks === null ? state : { looks }
        }),

      setIndicatorForAll: (hostIds, indicator) =>
        set((state) => {
          const clean = sanitizeIndicator(indicator)
          if (clean === null) return state
          const looks = { ...state.looks }
          let changed = false
          for (const id of hostIds) {
            if (!isHostId(id)) continue
            if (!Object.hasOwn(looks, id) && Object.keys(looks).length >= MAX_POOL_HOSTS) continue
            looks[id] = { ...(Object.hasOwn(looks, id) ? looks[id] : {}), indicator: clean }
            changed = true
          }
          return changed ? { looks } : state
        }),
    }),
    {
      name: STORAGE_KEYS.POOL_HOSTS,
      storage: purdexStorage,
      version: 1,
      partialize: (state) => ({ order: state.order, looks: state.looks }),
      // Every arrival — this device's storage, another window, a `settings` apply (apply-to-stores rehydrates after its
      // write) — passes through here. Nothing stored keeps memory as it is, as zustand's default merge does.
      merge: (persisted, current) =>
        isRecord(persisted) ? { ...current, order: sanitizeOrder(persisted.order), looks: sanitizeLooks(persisted.looks) } : current,
    },
  ),
)

syncManager.register(STORAGE_KEYS.POOL_HOSTS, usePoolHostsStore)
