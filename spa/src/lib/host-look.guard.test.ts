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
// Every hit also carries `decl`: the innermost NAMED declaration around the read
// — a function / method / accessor name, a `const f = () => …` name, or the key
// of an object-literal property (`setHostIcon: (…) => …` → `setHostIcon`);
// `<module>` when there is none. The allowlist is file → decl → field → EXACT
// count, and the repo test demands the hit multiset equal it: a new read, a
// removed read, or a read moved into another declaration of the same file
// (same file-wide count) all fail and print `file:line decl field`.
//
// WIDENING THE ALLOWLIST takes two edits, on purpose: the `ALLOWLIST` constant
// AND the verbatim literal pinned in the "pinned" test (which never references
// the constant). Only `stores/useHostStore.ts`, `lib/host-color.ts` and
// `lib/host-look.ts` may appear (the spec §4.2 exception); a test asserts the
// allowlisted files are a subset of those three. H2a checks the colour / icon
// fields; H2b adds `name`.
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
  /** The innermost named declaration around the read (`<module>` when none). */
  decl: string
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

/** A declaration name as text, or undefined when it is not a plain name (binding pattern, computed key). */
function nameText(name: ts.Node | undefined): string | undefined {
  if (!name) return undefined
  if (ts.isIdentifier(name) || ts.isPrivateIdentifier(name) || ts.isStringLiteral(name) || ts.isNumericLiteral(name)) {
    return name.text
  }
  return undefined
}

/** The innermost named declaration enclosing `node` (see the header), or `<module>`. */
export function enclosingDecl(node: ts.Node): string {
  for (let n: ts.Node | undefined = node.parent; n; n = n.parent) {
    let name: string | undefined
    if (
      ts.isFunctionDeclaration(n) ||
      ts.isFunctionExpression(n) ||
      ts.isMethodDeclaration(n) ||
      ts.isGetAccessorDeclaration(n) ||
      ts.isSetAccessorDeclaration(n) ||
      ts.isPropertyAssignment(n) ||
      ts.isPropertyDeclaration(n) ||
      ts.isClassDeclaration(n)
    ) {
      name = nameText(n.name)
    } else if (
      ts.isVariableDeclaration(n) &&
      n.initializer &&
      (ts.isArrowFunction(n.initializer) || ts.isFunctionExpression(n.initializer))
    ) {
      name = nameText(n.name)
    }
    if (name !== undefined) return name
  }
  return '<module>'
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
      hits.push({
        file,
        line: sf.getLineAndCharacterOfPosition(node.getStart(sf)).line + 1,
        field,
        decl: enclosingDecl(node),
      })

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

/** name → [snippet, the `decl` of each hit (in order)]. */
const DECL_FIXTURES: Record<string, [string, string[]]> = {
  'decl-module.ts': [`export const v = h.icon`, ['<module>']],
  'decl-function.ts': [`export function f() { return h.icon }`, ['f']],
  'decl-arrow-const.ts': [`export const f = () => [h.icon, h.color]`, ['f', 'f']],
  'decl-nested.ts': [`export function outer() { function inner() { return h.icon } return [inner(), h.color] }`, ['inner', 'outer']],
  'decl-property-key.ts': [
    `export const store = { setIcon: (x: HostConfig) => [1].map(() => x.icon), other: () => h.colors }`,
    ['setIcon', 'other'],
  ],
  'decl-method.ts': [`export class C { m() { return h.iconWeight } get g() { return h.icon } }`, ['m', 'g']],
  'decl-destructure.ts': [`export function f() { const { icon, color } = h; return [icon, color] }`, ['f', 'f']],
}

describe('host-look guard — enclosing declaration', { timeout: 60_000 }, () => {
  const { program, dir } = fixtureProgram(
    Object.fromEntries(Object.entries(DECL_FIXTURES).map(([name, [snippet]]) => [name, HEADER + snippet + '\n'])),
  )
  const hits = detectHostConfigReads(program, { fields: GUARDED_FIELDS, include: (f) => f.startsWith(`${dir}/`) })

  it.each(Object.entries(DECL_FIXTURES))('%s', (name, [, expected]) => {
    expect(hits.filter((h) => h.file.endsWith(`/${name}`)).map((h) => h.decl)).toEqual(expected)
  })
})

// === the allowlist check ===

type GuardedField = (typeof GUARDED_FIELDS)[number]
/** file (relative to `spa/src`) → enclosing declaration → field → exact read count. */
export type Allowlist = Record<string, Record<string, Partial<Record<GuardedField, number>>>>

/**
 * Every difference between the hit multiset and `allowlist`, keyed by
 * (file, decl, field): a key with more reads than allowlisted, fewer, or not
 * allowlisted at all. Empty = exact match.
 */
export function allowlistProblems(hits: readonly GuardHit[], allowlist: Allowlist): string[] {
  const key = (file: string, decl: string, field: string) => JSON.stringify([file, decl, field])
  const got = new Map<string, GuardHit[]>()
  for (const h of hits) {
    const k = key(h.file, h.decl, h.field)
    got.set(k, [...(got.get(k) ?? []), h])
  }
  const want = new Map<string, number>()
  for (const [file, decls] of Object.entries(allowlist)) {
    for (const [decl, counts] of Object.entries(decls)) {
      for (const [field, n] of Object.entries(counts)) want.set(key(file, decl, field), n ?? 0)
    }
  }
  const problems: string[] = []
  for (const k of new Set([...got.keys(), ...want.keys()])) {
    const list = got.get(k) ?? []
    const n = want.get(k) ?? 0
    if (list.length === n) continue
    const [file, decl, field] = JSON.parse(k) as [string, string, string]
    const where = list.map((h) => `${h.file}:${h.line}`).join(', ') || 'none'
    problems.push(
      want.has(k)
        ? `${file} ${decl} ${field}: ${list.length} read(s), allowlisted ${n} — ${where}`
        : `${file} ${decl} ${field}: ${list.length} read(s), not allowlisted — ${where}`,
    )
  }
  return problems.sort()
}

describe('host-look guard — allowlistProblems', { timeout: 60_000 }, () => {
  const scan = (src: string) => {
    const { program, dir } = fixtureProgram({ 'swap.ts': HEADER + src + '\n' })
    return detectHostConfigReads(program, { fields: GUARDED_FIELDS, include: (f) => f.startsWith(`${dir}/`) })
  }
  const FILE = 'lib/__host_look_guard_fixture__/swap.ts'
  const allow: Allowlist = { [FILE]: { keep: { icon: 1 }, twice: { color: 2 } } }
  const baseline = [
    `export function keep(x: HostConfig) { return x.icon }`,
    `export function other(x: HostConfig) { return x.name }`,
    `export function twice(x: HostConfig) { return [x.color, x.color] }`,
  ].join('\n')

  it('the baseline matches its allowlist', () => {
    expect(allowlistProblems(scan(baseline), allow)).toEqual([])
  })

  it('a same-file, same-count swap into another declaration fails', () => {
    const swapped = baseline
      .replace('keep(x: HostConfig) { return x.icon }', 'keep(x: HostConfig) { return x.name }')
      .replace('other(x: HostConfig) { return x.name }', 'other(x: HostConfig) { return x.icon }')
    expect(swapped).not.toBe(baseline)
    expect(allowlistProblems(scan(swapped), allow)).toEqual([
      `${FILE} keep icon: 0 read(s), allowlisted 1 — none`,
      `${FILE} other icon: 1 read(s), not allowlisted — ${FILE}:4`,
    ])
  })

  it('the count inside one declaration must match too', () => {
    const once = baseline.replace('[x.color, x.color]', '[x.color, x.name]')
    expect(allowlistProblems(scan(once), allow)).toEqual([`${FILE} twice color: 1 read(s), allowlisted 2 — ${FILE}:5`])
  })

  it('a read in a file that is not allowlisted fails', () => {
    expect(allowlistProblems(scan(baseline), {})).toHaveLength(2)
  })
})

// === repo half ===

/**
 * The only production reads of a colour / icon field off `HostConfig`: file →
 * enclosing declaration → field → exact count. Measured at H2a T4.
 * Widening this ALSO requires editing the pinned literal below (see the header).
 */
export const ALLOWLIST: Allowlist = {
  // the store: its writers read the current value to build the next one
  'stores/useHostStore.ts': {
    setHostColor: { colors: 1 }, // the current console alpha
    setHostColorLayer: { colors: 1, color: 1 }, // `{ ...host.colors }`, the `{ color: _legacy }` drop
    setHostIcon: { icon: 1, iconWeight: 1 }, // the `{ icon: _i, iconWeight: _w }` drop
  },
  // the persisted-state sanitiser
  'lib/host-color.ts': {
    sanitizeHostConfig: { colors: 1, color: 1, icon: 1, iconWeight: 1 },
  },
  // THE selector
  'lib/host-look.ts': {
    lookOfHost: { colors: 1, color: 1, icon: 1, iconWeight: 1 },
  },
}

/** The spec §4.2 exception: the only files the allowlist may ever name. */
const ALLOWLIST_FILES_MAY_BE = ['stores/useHostStore.ts', 'lib/host-color.ts', 'lib/host-look.ts']

describe('host-look guard — repo', { timeout: 60_000 }, () => {
  it('the colour / icon reads of HostConfig are exactly the allowlist (file, declaration, field, count)', () => {
    const hits = detectHostConfigReads(repoProgram(), { fields: GUARDED_FIELDS, include: isGuardedSource })
    expect(allowlistProblems(hits, ALLOWLIST)).toEqual([])
  })

  it('the allowlist is pinned verbatim (widening it takes a second, deliberate edit here)', () => {
    expect(ALLOWLIST).toEqual({
      'stores/useHostStore.ts': {
        setHostColor: { colors: 1 },
        setHostColorLayer: { colors: 1, color: 1 },
        setHostIcon: { icon: 1, iconWeight: 1 },
      },
      'lib/host-color.ts': {
        sanitizeHostConfig: { colors: 1, color: 1, icon: 1, iconWeight: 1 },
      },
      'lib/host-look.ts': {
        lookOfHost: { colors: 1, color: 1, icon: 1, iconWeight: 1 },
      },
    })
  })

  it('the allowlisted files are a subset of the spec §4.2 three', () => {
    expect(Object.keys(ALLOWLIST).filter((f) => !ALLOWLIST_FILES_MAY_BE.includes(f))).toEqual([])
  })
})
