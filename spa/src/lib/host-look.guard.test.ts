// @vitest-environment node
//
// Guard (host-ownership H2 plan §0.14): no screen reads a host's look straight
// off `HostConfig` — it goes through `lib/host-look.ts`. A grep cannot tell
// `host.icon` from `file.icon`, so this runs the TypeScript checker over the
// app program (`tsconfig.app.json`) and counts every READ of a checked field
// on an expression whose type is — or has in a union / intersection — the
// `HostConfig` interface of `stores/useHostStore.ts` (a type alias of it is the
// same symbol).
//
// Counted as a read:
//   - `x.f`, `x?.f`, `x['f']` (string-literal element access);
//   - object-binding destructuring of `f` (`const { f } = host`, nested too);
//   - parameter destructuring `({ f }: HostConfig)`.
// Not a read: the left side of an assignment (`next.icon = x`, `x.f += …`),
// `delete x.f`, an object-literal property (`{ icon: x }`). Writes are the
// writer tests' job.
//
// KNOWN FALSE NEGATIVES (the fixture half pins each one as "not flagged", so a
// change of the detector is visible):
//   - computed access with a non-literal key: `h[f]` (e.g. the look loop of
//     `host-transfer-plan.ts`);
//   - a value first narrowed / copied into another type: `(h as { icon?: string }).icon`,
//     `Pick<HostConfig, 'icon'>`, `Partial<HostConfig>`, a spread `({ ...h }).icon`;
//   - destructuring ASSIGNMENT (`({ icon } = h)`), reads in `.d.ts` files or
//     `any`-typed code;
//   - test files (excluded by design: `*.test.*`, `__tests__/`, `test-setup.ts`,
//     `test-utils*`).
//
// The allowlist is per file AND per field with the EXACT count: a count going
// up (a new direct read) or down (a read moved) fails and prints the
// `file:line field` list. H2a checks the colour / icon fields; H2b adds `name`.
import { describe, expect, it } from 'vitest'
import ts from 'typescript'

// Paths are plain `/`-separated strings (TypeScript's own file names are): the
// app tsconfig has no `@types/node` (see `index.css.test.ts`), so no `node:path`.
const SPA_DIR = decodeURIComponent(new URL('../..', import.meta.url).pathname).replace(/\/$/, '')
const SRC_DIR = `${SPA_DIR}/src`
const HOST_STORE_FILE = `${SRC_DIR}/stores/useHostStore.ts`

/** `fileName` relative to `spa/src`, or null when outside it. */
function srcRelative(fileName: string): string | null {
  return fileName.startsWith(`${SRC_DIR}/`) ? fileName.slice(SRC_DIR.length + 1) : null
}

/** The fields H2a guards. */
export const GUARDED_FIELDS = ['colors', 'color', 'icon', 'iconWeight'] as const

export interface GuardHit {
  /** Relative to `spa/src`, `/`-separated. */
  file: string
  line: number
  field: string
}

// === the detector ===

const ASSIGNMENT_OPERATORS: ReadonlySet<ts.SyntaxKind> = new Set([
  ts.SyntaxKind.EqualsToken,
  ts.SyntaxKind.PlusEqualsToken,
  ts.SyntaxKind.MinusEqualsToken,
  ts.SyntaxKind.AsteriskEqualsToken,
  ts.SyntaxKind.SlashEqualsToken,
  ts.SyntaxKind.BarBarEqualsToken,
  ts.SyntaxKind.AmpersandAmpersandEqualsToken,
  ts.SyntaxKind.QuestionQuestionEqualsToken,
])

/** `node` is written, not read: `node = …`, `node += …`, `delete node`. */
function isWriteTarget(node: ts.Expression): boolean {
  let n: ts.Node = node
  while (ts.isParenthesizedExpression(n.parent)) n = n.parent
  const parent = n.parent
  if (ts.isDeleteExpression(parent)) return true
  return ts.isBinaryExpression(parent) && parent.left === n && ASSIGNMENT_OPERATORS.has(parent.operatorToken.kind)
}

function hostConfigSymbol(program: ts.Program, checker: ts.TypeChecker): ts.Symbol {
  const sf = program.getSourceFile(HOST_STORE_FILE)
  if (!sf) throw new Error(`guard: ${HOST_STORE_FILE} is not in the program`)
  for (const stmt of sf.statements) {
    if (ts.isInterfaceDeclaration(stmt) && stmt.name.text === 'HostConfig') {
      const sym = checker.getSymbolAtLocation(stmt.name)
      if (sym) return sym
    }
  }
  throw new Error('guard: interface HostConfig not found in useHostStore.ts')
}

export interface DetectOptions {
  fields: readonly string[]
  /** Which source files to scan (absolute file name). */
  include: (fileName: string) => boolean
}

/** Every read of a guarded field on a `HostConfig`-typed expression, in `include`d files. */
export function detectHostConfigReads(program: ts.Program, opts: DetectOptions): GuardHit[] {
  const checker = program.getTypeChecker()
  const target = hostConfigSymbol(program, checker)
  const fields = new Set(opts.fields)

  const isHostConfig = (type: ts.Type): boolean => {
    if (type.isUnionOrIntersection()) return type.types.some(isHostConfig)
    return type.getSymbol() === target || type.aliasSymbol === target
  }

  const hits: GuardHit[] = []
  for (const sf of program.getSourceFiles()) {
    if (sf.isDeclarationFile || !opts.include(sf.fileName)) continue
    const file = srcRelative(sf.fileName) ?? sf.fileName
    const hit = (node: ts.Node, field: string) =>
      hits.push({ file, line: sf.getLineAndCharacterOfPosition(node.getStart(sf)).line + 1, field })

    const visit = (node: ts.Node): void => {
      if (ts.isPropertyAccessExpression(node)) {
        const field = node.name.text
        if (fields.has(field) && !isWriteTarget(node) && isHostConfig(checker.getTypeAtLocation(node.expression))) {
          hit(node.name, field)
        }
      } else if (ts.isElementAccessExpression(node)) {
        const arg = node.argumentExpression
        if (ts.isStringLiteral(arg) || ts.isNoSubstitutionTemplateLiteral(arg)) {
          const field = arg.text
          if (fields.has(field) && !isWriteTarget(node) && isHostConfig(checker.getTypeAtLocation(node.expression))) {
            hit(arg, field)
          }
        }
      } else if (ts.isObjectBindingPattern(node)) {
        const patternType = checker.getTypeAtLocation(node)
        if (isHostConfig(patternType)) {
          for (const el of node.elements) {
            if (el.dotDotDotToken) continue
            const key = el.propertyName ?? el.name
            const field = ts.isIdentifier(key) || ts.isStringLiteral(key) ? key.text : undefined
            if (field !== undefined && fields.has(field)) hit(el, field)
          }
        }
      }
      ts.forEachChild(node, visit)
    }
    visit(sf)
  }
  return hits.sort((a, b) => (a.file < b.file ? -1 : a.file > b.file ? 1 : a.line - b.line))
}

// === programs ===

function appCompilerOptions(): ts.CompilerOptions {
  const configPath = `${SPA_DIR}/tsconfig.app.json`
  const read = ts.readConfigFile(configPath, (p) => ts.sys.readFile(p))
  if (read.error) throw new Error(ts.flattenDiagnosticMessageText(read.error.messageText, '\n'))
  return ts.parseJsonConfigFileContent(read.config, ts.sys, SPA_DIR, undefined, configPath).options
}

/** `spa/src` production sources only. */
export function isGuardedSource(fileName: string): boolean {
  const rel = srcRelative(fileName)
  if (rel === null) return false
  if (!/\.(ts|tsx)$/.test(rel) || rel.endsWith('.d.ts')) return false
  if (/\.test\.[^/]+$/.test(rel)) return false
  if (rel.split('/').includes('__tests__')) return false
  const base = rel.split('/').pop()!
  return base !== 'test-setup.ts' && !base.startsWith('test-utils')
}

function repoProgram(): ts.Program {
  const configPath = `${SPA_DIR}/tsconfig.app.json`
  const read = ts.readConfigFile(configPath, (p) => ts.sys.readFile(p))
  const parsed = ts.parseJsonConfigFileContent(read.config, ts.sys, SPA_DIR, undefined, configPath)
  return ts.createProgram({ rootNames: parsed.fileNames.filter(isGuardedSource), options: parsed.options })
}

/**
 * An in-memory program: `files` (name → source) live as virtual files inside
 * `spa/src/lib/__host_look_guard_fixture__/`, so `../../stores/useHostStore`
 * resolves to the REAL `HostConfig`. Everything else is read from disk.
 */
function fixtureProgram(files: Record<string, string>): { program: ts.Program; dir: string } {
  const dir = `${SRC_DIR}/lib/__host_look_guard_fixture__`
  const options = appCompilerOptions()
  const virtual = new Map(Object.entries(files).map(([name, text]) => [`${dir}/${name}`, text]))
  const host = ts.createCompilerHost(options, true)
  const baseGetSourceFile = host.getSourceFile.bind(host)
  const baseFileExists = host.fileExists.bind(host)
  const baseReadFile = host.readFile.bind(host)
  host.fileExists = (f) => virtual.has(f) || baseFileExists(f)
  host.readFile = (f) => virtual.get(f) ?? baseReadFile(f)
  host.getSourceFile = (f, lang, onError, shouldCreate) => {
    const text = virtual.get(f)
    return text !== undefined ? ts.createSourceFile(f, text, lang, true) : baseGetSourceFile(f, lang, onError, shouldCreate)
  }
  return { program: ts.createProgram({ rootNames: [...virtual.keys()], options, host }), dir }
}

// === fixture half ===

const HEADER = `import type { HostConfig } from '../../stores/useHostStore'\ndeclare const h: HostConfig\n`

/** name → [snippet, the fields it must flag (in order)]. */
const FIXTURES: Record<string, [string, string[]]> = {
  // flagged
  'property-read.ts': [`export const v = h.icon`, ['icon']],
  'optional-chain.ts': [`declare const m: HostConfig | undefined\nexport const v = m?.iconWeight`, ['iconWeight']],
  'string-literal-element.ts': [`export const v = h['icon']`, ['icon']],
  'record-optional-chain.ts': [
    `declare const hosts: Record<string, HostConfig | undefined>\ndeclare const id: string\nexport const v = hosts[id]?.colors`,
    ['colors'],
  ],
  'union-with-other.ts': [`declare const u: HostConfig | { colors?: string; name: string }\nexport const v = u.colors`, ['colors']],
  'intersection.ts': [`declare const w: HostConfig & { aliases?: string[] }\nexport const v = w.color`, ['color']],
  'object-destructure.ts': [`const { icon, colors: c } = h\nexport const v = [icon, c]`, ['icon', 'colors']],
  'nested-destructure.ts': [`declare const o: { host: HostConfig }\nconst { host: { iconWeight } } = o\nexport const v = iconWeight`, ['iconWeight']],
  'parameter-destructure.ts': [`export const f = ({ icon }: HostConfig) => icon`, ['icon']],
  'alias.ts': [`type H = HostConfig\ndeclare const a: H\nexport const v = a.icon`, ['icon']],
  'read-in-rhs.ts': [`declare const next: HostConfig\nnext.icon = h.icon`, ['icon']],
  // not flagged
  'assignment-left.ts': [`declare const next: HostConfig\nnext.icon = 'Laptop'\nnext.color ??= '#ffffff'`, []],
  'delete.ts': [`declare const next: HostConfig\ndelete next.icon\ndelete next['colors']`, []],
  'object-literal.ts': [`export const v: Partial<HostConfig> = { icon: 'x', color: '#000000' }`, []],
  'other-type.ts': [`declare const f: { icon: string; colors: string }\nexport const v = [f.icon, f.colors]`, []],
  'unguarded-field.ts': [`export const v = [h.name, h.ip, h.daemonId]`, []],
  // KNOWN FALSE NEGATIVES — pinned blind spots, NOT flagged on purpose
  'blind-variable-key.ts': [`declare const k: 'icon' | 'colors'\nexport const v = h[k]`, []],
  'blind-cast.ts': [`export const v = (h as { icon?: string }).icon`, []],
  'blind-pick.ts': [`const p: Pick<HostConfig, 'icon'> = h\nexport const v = p.icon`, []],
  'blind-partial.ts': [`const p: Partial<HostConfig> = h\nexport const v = p.icon`, []],
  'blind-spread.ts': [`export const v = ({ ...h }).icon`, []],
  'blind-destructure-assign.ts': [`let icon: string | undefined\n;({ icon } = h)\nexport const v = icon`, []],
}

describe('host-look guard — detector fixtures', { timeout: 60_000 }, () => {
  const { program, dir } = fixtureProgram(
    Object.fromEntries(Object.entries(FIXTURES).map(([name, [snippet]]) => [name, HEADER + snippet + '\n'])),
  )
  const hits = detectHostConfigReads(program, {
    fields: GUARDED_FIELDS,
    include: (f) => f.startsWith(`${dir}/`),
  })

  it('the fixture program type-checks (the snippets mean what they say)', () => {
    const errors = ts
      .getPreEmitDiagnostics(program)
      .filter((d) => d.file?.fileName.startsWith(`${dir}/`))
      .map((d) => `${d.file!.fileName.slice(dir.length + 1)}: ${ts.flattenDiagnosticMessageText(d.messageText, ' ')}`)
    expect(errors).toEqual([])
  })

  it.each(Object.entries(FIXTURES))('%s', (name, [, expected]) => {
    const got = hits.filter((h) => h.file.endsWith(`/${name}`)).map((h) => h.field)
    expect(got).toEqual(expected)
  })
})

// === repo half ===

/**
 * The only production files that may read a colour / icon field off
 * `HostConfig`, with the exact count per field. Measured at H2a T4.
 */
export const ALLOWLIST: Record<string, Partial<Record<(typeof GUARDED_FIELDS)[number], number>>> = {
  // the store: its writers read the current value to build the next one
  // (:447 / :455 `colors`, :488 `color`, :501 the `{ icon, iconWeight }` destructure)
  'stores/useHostStore.ts': { colors: 2, color: 1, icon: 1, iconWeight: 1 },
  // the persisted-state sanitiser (:113–:116)
  'lib/host-color.ts': { colors: 1, color: 1, icon: 1, iconWeight: 1 },
  // THE selector (`lookOfHost`'s destructure)
  'lib/host-look.ts': { colors: 1, color: 1, icon: 1, iconWeight: 1 },
}

describe('host-look guard — repo', { timeout: 60_000 }, () => {
  it('no colour / icon read of HostConfig outside the allowlist, at exactly the allowlisted counts', () => {
    const hits = detectHostConfigReads(repoProgram(), { fields: GUARDED_FIELDS, include: isGuardedSource })
    const problems: string[] = []
    const byFile = new Map<string, GuardHit[]>()
    for (const h of hits) byFile.set(h.file, [...(byFile.get(h.file) ?? []), h])
    for (const [file, list] of byFile) {
      if (!Object.hasOwn(ALLOWLIST, file)) {
        problems.push(...list.map((h) => `${h.file}:${h.line} ${h.field} (file not allowlisted)`))
      }
    }
    for (const [file, counts] of Object.entries(ALLOWLIST)) {
      const list = byFile.get(file) ?? []
      for (const field of GUARDED_FIELDS) {
        const got = list.filter((h) => h.field === field)
        const want = counts[field] ?? 0
        if (got.length !== want) {
          problems.push(
            `${file} ${field}: ${got.length} read(s), allowlisted ${want} — ${got.map((h) => `${h.file}:${h.line}`).join(', ') || 'none'}`,
          )
        }
      }
    }
    expect(problems).toEqual([])
  })
})
