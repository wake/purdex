// @vitest-environment node
//
// Guard (task groups spec §8, TG-2c): the per-host task groups (hostconfig `task_groups`, `task_groups.v1`, a CAS PUT per host) are gone — the groups are `useManualGroupsStore`'s, in the profile. This pins that
// the retired modules stay deleted and that nothing imports them or their entry points again.
import { describe, it, expect } from 'vitest'
// node:fs / node:path / __dirname are untyped under tsconfig.app.json (deliberately no @types/node; see pool/fixtures-sync.guard.test.ts); they work at runtime.
// @ts-expect-error node:fs is untyped here.
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs'
// @ts-expect-error node:path is untyped here.
import { join, relative } from 'node:path'

// @ts-expect-error __dirname is untyped here.
const SRC: string = join(__dirname, '..', '..')
// @ts-expect-error __filename is untyped here.
const SELF: string = relative(SRC, __filename)

const RETIRED_FILES = [
  'lib/task-groups/api.ts',
  'lib/task-groups/loader.ts',
  'lib/task-groups/edits.ts',
  'lib/task-groups/actions.ts',
  'stores/useTaskGroupsStore.ts',
]

/** What no source file may mention any more: the retired modules by path, and their entry points by name. */
const RETIRED_REFERENCES = [
  /task-groups\/(api|loader|edits|actions)['"]/,
  /stores\/useTaskGroupsStore['"]/,
  /\buseTaskGroupsStore\b/,
  /\bstartTaskGroupsLifecycle\b/,
  /\btaskGroupsSupported\b/,
  /\bloadTaskGroupsIfSupported\b/,
  /\bselectTaskGroupsV1\b/,
  /task_groups\.v1/,
  /hostconfig\/task-groups/,
]

function sources(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir) as string[]) {
    if (name === 'node_modules') continue
    const full = join(dir, name)
    if (statSync(full).isDirectory()) sources(full, out)
    else if (/\.(ts|tsx)$/.test(name)) out.push(full)
  }
  return out
}

describe('the per-host task groups stay retired', () => {
  it('the retired modules are not there', () => {
    for (const f of RETIRED_FILES) expect(existsSync(join(SRC, f)), f).toBe(false)
  })

  it('no source file imports or names them', () => {
    const hits: string[] = []
    for (const file of sources(SRC)) {
      const rel = relative(SRC, file)
      if (rel === SELF) continue
      const text = readFileSync(file, 'utf8')
      for (const re of RETIRED_REFERENCES) if (re.test(text)) hits.push(`${rel}: ${re}`)
      // inside the folder the old modules were reached by a relative path
      if (rel.startsWith(join('lib', 'task-groups') + '/') && /from '\.\/(api|loader|edits|actions)'/.test(text)) hits.push(`${rel}: relative import`)
    }
    expect(hits).toEqual([])
  })
})
