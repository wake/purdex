// Guard (monitoring tabs plan, common rules; spec A1 / A11): the sidebar's host row and the files this line made pool-only do not read the fleet. The scope is `components/fleet/**`, `components/watch/host/**` (the host page's new files, MON-6d-2 on) and
// the listed `lib/pool` files. Not in scope, by name: `fleet-bridge.ts` (the host page's and the Hosts page's transitional reader, until MON-6 / MON-9b), `host-view.ts`,
// `history.ts`, `use-pool-host-rows.ts`, `use-series-history.ts` (the same two screens), and the one exemption `pool-look-migration.ts` (the one-time copy of the fleet's looks, deleted with the fleet).
// A scoped, non-test file must not import anything from `lib/fleet/` (a type import too) and must not mention the fleet's readers.
import { describe, it, expect } from 'vitest'

// Every non-test source file of the sidebar, the task queue tab (MON-5b) and the pool, raw. Keys are `/src/…` paths.
const SOURCES = import.meta.glob<string>(
  ['/src/components/fleet/**/*.{ts,tsx}', '/src/lib/pool/*.ts', '/src/components/watch/jobs/*.tsx', '/src/features/workspace/components/WatchShieldButton.tsx', '/src/components/watch/host/**/*.{ts,tsx}', '!/src/**/*.test.{ts,tsx}', '!/src/**/*.fixture.ts'],
  { query: '?raw', import: 'default', eager: true },
)
const POOL_FILES = [
  'bar-readings.ts', 'cell-model.ts', 'daemons.ts', 'host-looks.ts', 'host-state.ts', 'parse.ts', 'ring-driver.ts', 'store.ts', 'types.ts', 'use-cell-models.ts', 'use-monitor-hosts.ts', 'words.ts', 'pool-jobs.ts', 'queue-strip.ts', 'host-counts.ts', 'indicator.ts', 'alerts-api.ts', 'alerts-store.ts', 'alerts-events.ts', 'host-history.ts', 'use-host-series.ts', 'queue-flow.ts', 'observations-api.ts', 'events-model.ts', 'host-charts.ts',
]
// Other files made pool-only by the line: the shield beside the Home button reads the alerts (MON-10a), not the fleet.
const OTHER_FILES = ['features/workspace/components/WatchShieldButton.tsx']
// Any module specifier that points into `lib/fleet`: `from '…/fleet/…'`, `from '…/fleet'`, a side-effect `import '…/fleet/…'`, a dynamic `import('…/fleet…')`, `require('…/fleet…')`. (A re-export through a
// barrel outside `lib/fleet` is not followed; nothing in the scope imports a barrel.)
const FLEET_IMPORT = /(?:\bfrom|\bimport|\brequire)\s*\(?\s*['"][^'"]*(?:\/lib|(?<![./])\.\.)\/fleet(?:\/[^'"]*)?['"]/
const FLEET_READERS = /\b(useFleetStore|useFleetHostViews|readFleetView|useFleetConfigStore)\b/

function scoped(): Array<{ name: string; code: string | undefined }> {
  const sidebar = Object.keys(SOURCES).filter((k) => k.startsWith('/src/components/fleet/') || k.startsWith('/src/components/watch/host/')).map((k) => ({ name: k.slice('/src/'.length), code: SOURCES[k] }))
  const jobs = Object.keys(SOURCES).filter((k) => k.startsWith('/src/components/watch/jobs/')).map((k) => ({ name: k.slice('/src/'.length), code: SOURCES[k] }))
  return [...sidebar, ...jobs, ...POOL_FILES.map((f) => ({ name: `lib/pool/${f}`, code: SOURCES[`/src/lib/pool/${f}`] })), ...OTHER_FILES.map((f) => ({ name: f, code: SOURCES[`/src/${f}`] }))]
}

describe('the sidebar and the pool-only files do not read the fleet', () => {
  it('the scope is what it says: the sidebar files and the listed pool files exist', () => {
    const names = scoped().map((f) => f.name)
    expect(names).toContain('components/fleet/FleetCells.tsx')
    expect(names).toContain('components/fleet/HostCell.tsx')
    expect(names).toContain('components/watch/jobs/QueueFlow.tsx')
    expect(names).toContain('components/watch/jobs/JobsPane.tsx')
    expect(names).toContain('lib/pool/cell-model.ts')
    expect(names).toContain('lib/pool/use-monitor-hosts.ts')
    expect(names).toContain('lib/pool/alerts-store.ts')
    expect(names).toContain('features/workspace/components/WatchShieldButton.tsx')
    expect(names).toContain('components/watch/host/MiniChart.tsx')
    for (const f of scoped()) expect(f.code, f.name).toBeTypeOf('string')
  })

  for (const f of scoped()) {
    it(`${f.name} imports nothing from lib/fleet and uses none of the fleet's readers`, () => {
      const code = f.code ?? ''
      expect(FLEET_IMPORT.test(code), 'imports lib/fleet').toBe(false)
      expect(FLEET_READERS.test(code), 'mentions a fleet reader').toBe(false)
    })
  }

  it('the detectors catch what they are for', () => {
    expect(FLEET_IMPORT.test("import type { FleetFast } from '../../lib/fleet/types'")).toBe(true)
    expect(FLEET_IMPORT.test("import { x } from '../fleet/store'")).toBe(true)
    expect(FLEET_IMPORT.test("const m = await import('../fleet/store')")).toBe(true)
    expect(FLEET_IMPORT.test("import '../../lib/fleet/store'")).toBe(true)
    expect(FLEET_IMPORT.test("const m = await import('../../lib/fleet')")).toBe(true)
    expect(FLEET_IMPORT.test("const m = require('../fleet/store')")).toBe(true)
    expect(FLEET_IMPORT.test("export { x } from '../fleet/store'")).toBe(true)
    expect(FLEET_IMPORT.test("import { x } from './fleet-bridge'")).toBe(false)
    // from a component, `../../fleet/…` is components/fleet (the sidebar), not lib/fleet
    expect(FLEET_IMPORT.test("import { QueueSegments } from '../../fleet/QueueStrip'")).toBe(false)
    // the sidebar's own components live in `components/fleet`: importing one of them is not reading the fleet
    expect(FLEET_IMPORT.test("import { CountBadge } from '../../../components/fleet/CountBadge'")).toBe(false)
    expect(FLEET_READERS.test('const s = useFleetStore.getState()')).toBe(true)
  })
})
